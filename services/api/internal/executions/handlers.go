// Package executions implements execution submission and read endpoints.
package executions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
	"github.com/agent-platform/agent-platform/services/api/internal/auth"
	"github.com/agent-platform/agent-platform/services/api/internal/pagination"
	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

const maxExecutionInput = 256 << 10

type Handlers struct {
	store        *Store
	orchestrator *OrchestratorClient
	log          *zap.Logger
}

func NewHandlers(store *Store, orchestrator *OrchestratorClient, log *zap.Logger) *Handlers {
	return &Handlers{store: store, orchestrator: orchestrator, log: log}
}

type createRequest struct {
	WorkflowID      string         `json:"workflow_id"`
	WorkflowVersion int            `json:"workflow_version"`
	Input           map[string]any `json:"input"`
	DeadlineAt      *time.Time     `json:"deadline_at,omitempty"`
	Priority        int32          `json:"priority"`
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principal, _ := auth.PrincipalFrom(r.Context())

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(idempotencyKey) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "idempotency_key_required",
			"Idempotency-Key must be 8..128 visible ASCII characters")
		return
	}

	var request createRequest
	if err := respond.Decode(w, r, 1<<20, &request); err != nil {
		if respond.IsBodyTooLarge(err) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 1 MiB")
			return
		}
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Input == nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "input must be a JSON object")
		return
	}
	if !strings.HasPrefix(request.WorkflowID, "wf_") {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "workflow_id must start with wf_")
		return
	}
	if request.WorkflowVersion < 1 || request.WorkflowVersion > 1<<31-1 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "workflow_version must be a positive integer")
		return
	}
	if request.Priority < -100 || request.Priority > 100 {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "priority must be between -100 and 100")
		return
	}
	inputJSON, err := json.Marshal(request.Input)
	if err != nil || len(inputJSON) > maxExecutionInput {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "input exceeds 256 KiB or is not serialisable")
		return
	}
	input, err := structpb.NewStruct(request.Input)
	if err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "input is not a valid JSON object")
		return
	}

	protoRequest := &orchestratorv1.SubmitExecutionRequest{
		OrgId:           membership.OrgID,
		ProjectId:       chi.URLParam(r, "project_id"),
		WorkflowId:      request.WorkflowID,
		WorkflowVersion: int32(request.WorkflowVersion),
		Input:           input,
		RequestId:       idempotencyKey,
		RequestedBy:     principal.ID,
		Traceparent:     r.Header.Get("traceparent"),
		Priority:        request.Priority,
	}
	if request.DeadlineAt != nil {
		protoRequest.DeadlineAt = timestamppb.New(request.DeadlineAt.UTC())
	}

	callCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	submitted, err := h.orchestrator.Submit(callCtx, protoRequest)
	if err != nil {
		h.respondSubmitError(w, r, err)
		return
	}

	execution, err := h.store.Get(r.Context(), membership.OrgID, protoRequest.ProjectId, submitted.ExecutionId)
	if err != nil {
		h.log.Error("execution committed but read failed",
			zap.String("execution_id", submitted.ExecutionId), zap.Error(err))
		respond.Error(w, r, http.StatusInternalServerError, "internal", "execution committed; fetch it with GET")
		return
	}
	w.Header().Set("Location", "/v1/orgs/"+membership.OrgID+"/projects/"+protoRequest.ProjectId+"/executions/"+execution.ID)
	respond.JSON(w, http.StatusAccepted, execution)
}

func (h *Handlers) respondSubmitError(w http.ResponseWriter, r *http.Request, err error) {
	st := status.Convert(err)
	switch st.Code() {
	case codes.NotFound:
		respond.Error(w, r, http.StatusNotFound, "not_found", st.Message())
	case codes.InvalidArgument, codes.FailedPrecondition:
		respond.ErrorWithDetails(w, r, http.StatusUnprocessableEntity, "validation_failed", st.Message(),
			map[string]any{"grpc_code": st.Code().String()})
	case codes.AlreadyExists:
		respond.Error(w, r, http.StatusConflict, "idempotency_conflict", st.Message())
	case codes.ResourceExhausted:
		respond.Error(w, r, http.StatusTooManyRequests, "quota_exceeded", st.Message())
	case codes.Unavailable, codes.DeadlineExceeded:
		respond.Error(w, r, http.StatusServiceUnavailable, "orchestrator_unavailable", "execution service is unavailable")
	default:
		h.log.Error("submit execution failed", zap.String("code", st.Code().String()), zap.Error(err))
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not submit execution")
	}
}

// Cancel records a durable cancellation request and returns the current execution snapshot.
func (h *Handlers) Cancel(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principal, _ := auth.PrincipalFrom(r.Context())
	executionID := chi.URLParam(r, "execution_id")
	projectID := chi.URLParam(r, "project_id")

	var request struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		if err := respond.Decode(w, r, 4096, &request); err != nil && !errors.Is(err, io.EOF) {
			if respond.IsBodyTooLarge(err) {
				respond.Error(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 4 KiB")
				return
			}
			respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}

	callCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := h.orchestrator.Cancel(callCtx, &orchestratorv1.CancelExecutionRequest{
		OrgId:       membership.OrgID,
		ExecutionId: executionID,
		RequestedBy: principal.ID,
		Reason:      request.Reason,
	})
	if err != nil {
		st := status.Convert(err)
		switch st.Code() {
		case codes.NotFound:
			respond.Error(w, r, http.StatusNotFound, "not_found", "execution not found")
		case codes.FailedPrecondition:
			respond.Error(w, r, http.StatusConflict, "conflict", st.Message())
		case codes.Unavailable, codes.DeadlineExceeded:
			respond.Error(w, r, http.StatusServiceUnavailable, "orchestrator_unavailable", "execution service is unavailable")
		default:
			h.log.Error("execution cancellation failed", zap.String("execution_id", executionID), zap.Error(err))
			respond.Error(w, r, http.StatusInternalServerError, "internal", "could not cancel execution")
		}
		return
	}
	if !result.GetAccepted() {
		respond.Error(w, r, http.StatusConflict, "conflict", "execution is already terminal or failing")
		return
	}
	execution, err := h.store.Get(r.Context(), membership.OrgID, projectID, executionID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "cancellation committed; fetch the execution with GET")
		return
	}
	respond.JSON(w, http.StatusAccepted, execution)
}

// Input returns the original submitted execution input for an execution detail view.
func (h *Handlers) Input(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	raw, err := h.store.Input(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "execution_id"))
	if err != nil {
		h.respondStoreError(w, r, err, "could not load execution input")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"input": raw})
}

// Replay returns the durable history of an execution without re-running anything.
func (h *Handlers) Replay(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	projectID := chi.URLParam(r, "project_id")
	executionID := chi.URLParam(r, "execution_id")
	execution, err := h.store.Get(r.Context(), membership.OrgID, projectID, executionID)
	if err != nil {
		h.respondStoreError(w, r, err, "could not load execution replay")
		return
	}
	tasks, err := h.store.ListTasks(r.Context(), membership.OrgID, projectID, executionID, "", 500)
	if err != nil {
		h.respondStoreError(w, r, err, "could not load replay tasks")
		return
	}
	events, err := h.store.ListEvents(r.Context(), membership.OrgID, projectID, executionID, 0, 1000)
	if err != nil {
		h.respondStoreError(w, r, err, "could not load replay events")
		return
	}
	outputs, err := h.store.Outputs(r.Context(), membership.OrgID, projectID, executionID)
	if err != nil {
		h.respondStoreError(w, r, err, "could not load replay outputs")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"mode": "historical", "execution": execution, "tasks": tasks, "outputs": outputs, "events": events,
	})
}

// Reexecute creates a new execution using the prior input and explicitly selected prior outputs.
func (h *Handlers) Reexecute(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principal, _ := auth.PrincipalFrom(r.Context())
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(idempotencyKey) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "idempotency_key_required",
			"Idempotency-Key must be 8..128 visible ASCII characters")
		return
	}
	projectID := chi.URLParam(r, "project_id")
	executionID := chi.URLParam(r, "execution_id")
	original, err := h.store.Get(r.Context(), membership.OrgID, projectID, executionID)
	if err != nil {
		h.respondStoreError(w, r, err, "could not load execution")
		return
	}
	var request struct {
		Input        map[string]any `json:"input"`
		ReuseOutputs []string       `json:"reuse_outputs"`
		Priority     int32          `json:"priority"`
	}
	if r.Body != nil {
		if err := respond.Decode(w, r, 1<<20, &request); err != nil && !errors.Is(err, io.EOF) {
			respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	input := request.Input
	if input == nil {
		raw, err := h.store.Input(r.Context(), membership.OrgID, projectID, executionID)
		if err != nil {
			h.respondStoreError(w, r, err, "could not load prior input")
			return
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			respond.Error(w, r, http.StatusInternalServerError, "internal", "prior input is not an object")
			return
		}
	}
	protoInput, err := structpb.NewStruct(input)
	if err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "input must be a JSON object")
		return
	}
	seed := map[string]*structpb.Struct{}
	if len(request.ReuseOutputs) > 0 {
		outputs, err := h.store.Outputs(r.Context(), membership.OrgID, projectID, executionID)
		if err != nil {
			h.respondStoreError(w, r, err, "could not load prior outputs")
			return
		}
		for _, nodeID := range request.ReuseOutputs {
			raw, ok := outputs[nodeID]
			if !ok {
				respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
					"reuse_outputs references a node without a succeeded output: "+nodeID)
				return
			}
			value := map[string]any{}
			if err := json.Unmarshal(raw, &value); err != nil {
				respond.Error(w, r, http.StatusInternalServerError, "internal", "prior output is not an object")
				return
			}
			converted, err := structpb.NewStruct(value)
			if err != nil {
				respond.Error(w, r, http.StatusInternalServerError, "internal", "prior output is not a JSON object")
				return
			}
			seed[nodeID] = converted
		}
	}
	callCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	submitted, err := h.orchestrator.Submit(callCtx, &orchestratorv1.SubmitExecutionRequest{
		OrgId:               membership.OrgID,
		ProjectId:           projectID,
		WorkflowId:          original.WorkflowID,
		WorkflowVersion:     int32(original.WorkflowVersion), //nolint:gosec // version is bounded by the control plane.
		Input:               protoInput,
		RequestId:           idempotencyKey,
		RequestedBy:         principal.ID,
		Traceparent:         r.Header.Get("traceparent"),
		Priority:            request.Priority,
		ReplayOfExecutionId: executionID,
		SeedOutputs:         seed,
	})
	if err != nil {
		h.respondSubmitError(w, r, err)
		return
	}
	execution, err := h.store.Get(r.Context(), membership.OrgID, projectID, submitted.ExecutionId)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "re-execution committed; fetch it with GET")
		return
	}
	w.Header().Set("Location", "/v1/orgs/"+membership.OrgID+"/projects/"+projectID+"/executions/"+execution.ID)
	respond.JSON(w, http.StatusAccepted, execution)
}

// Resume starts a new execution from a failed or cancelled execution, reusing every node whose output succeeded.
func (h *Handlers) Resume(w http.ResponseWriter, r *http.Request) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principal, _ := auth.PrincipalFrom(r.Context())
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(idempotencyKey) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "idempotency_key_required",
			"Idempotency-Key must be 8..128 visible ASCII characters")
		return
	}
	projectID := chi.URLParam(r, "project_id")
	executionID := chi.URLParam(r, "execution_id")
	original, err := h.store.Get(r.Context(), membership.OrgID, projectID, executionID)
	if err != nil {
		h.respondStoreError(w, r, err, "could not load execution")
		return
	}
	if original.State != "failed" && original.State != "cancelled" {
		respond.Error(w, r, http.StatusConflict, "conflict", "only a failed or cancelled execution can be resumed")
		return
	}
	var request struct {
		Priority int32 `json:"priority"`
	}
	if r.Body != nil {
		if err := respond.Decode(w, r, 4096, &request); err != nil && !errors.Is(err, io.EOF) {
			respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	rawInput, err := h.store.Input(r.Context(), membership.OrgID, projectID, executionID)
	if err != nil {
		h.respondStoreError(w, r, err, "could not load prior input")
		return
	}
	input := map[string]any{}
	if err := json.Unmarshal(rawInput, &input); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "prior input is not an object")
		return
	}
	protoInput, err := structpb.NewStruct(input)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "prior input is not a JSON object")
		return
	}
	outputs, err := h.store.Outputs(r.Context(), membership.OrgID, projectID, executionID)
	if err != nil {
		h.respondStoreError(w, r, err, "could not load prior outputs")
		return
	}
	seed := map[string]*structpb.Struct{}
	for nodeID, raw := range outputs {
		value := map[string]any{}
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		converted, err := structpb.NewStruct(value)
		if err != nil {
			continue
		}
		seed[nodeID] = converted
	}
	callCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	submitted, err := h.orchestrator.Submit(callCtx, &orchestratorv1.SubmitExecutionRequest{
		OrgId:               membership.OrgID,
		ProjectId:           projectID,
		WorkflowId:          original.WorkflowID,
		WorkflowVersion:     int32(original.WorkflowVersion), //nolint:gosec // bounded by the control plane.
		Input:               protoInput,
		RequestId:           idempotencyKey,
		RequestedBy:         principal.ID,
		Traceparent:         r.Header.Get("traceparent"),
		Priority:            request.Priority,
		ReplayOfExecutionId: executionID,
		SeedOutputs:         seed,
	})
	if err != nil {
		h.respondSubmitError(w, r, err)
		return
	}
	resumed, err := h.store.Get(r.Context(), membership.OrgID, projectID, submitted.ExecutionId)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "resume committed; fetch it with GET")
		return
	}
	respond.JSON(w, http.StatusAccepted, resumed)
}

func (h *Handlers) ApproveTask(w http.ResponseWriter, r *http.Request) {
	h.signalTask(w, r, "approve")
}

func (h *Handlers) RejectTask(w http.ResponseWriter, r *http.Request) {
	h.signalTask(w, r, "reject")
}

func (h *Handlers) SignalTask(w http.ResponseWriter, r *http.Request) {
	h.signalTask(w, r, "signal")
}

func (h *Handlers) signalTask(w http.ResponseWriter, r *http.Request, action string) {
	if err := auth.RequireRole(r.Context(), auth.RoleOperator); err != nil {
		respond.Error(w, r, http.StatusForbidden, "forbidden", "operator role required")
		return
	}
	membership, _ := auth.MembershipFrom(r.Context())
	principal, _ := auth.PrincipalFrom(r.Context())
	var request struct {
		Comment string         `json:"comment"`
		Payload map[string]any `json:"payload"`
	}
	if r.Body != nil {
		if err := respond.Decode(w, r, 16384, &request); err != nil && !errors.Is(err, io.EOF) {
			if respond.IsBodyTooLarge(err) {
				respond.Error(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 16 KiB")
				return
			}
			respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	}
	payload, err := structpb.NewStruct(request.Payload)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", "payload must be a JSON object")
		return
	}
	callCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := h.orchestrator.SignalTask(callCtx, &orchestratorv1.SignalTaskRequest{
		OrgId:       membership.OrgID,
		ProjectId:   chi.URLParam(r, "project_id"),
		ExecutionId: chi.URLParam(r, "execution_id"),
		TaskId:      chi.URLParam(r, "task_id"),
		Action:      action,
		Comment:     request.Comment,
		Actor:       principal.ID,
		Payload:     payload,
	})
	if err != nil {
		st := status.Convert(err)
		switch st.Code() {
		case codes.NotFound:
			respond.Error(w, r, http.StatusNotFound, "not_found", "execution or task not found")
		case codes.FailedPrecondition:
			respond.Error(w, r, http.StatusConflict, "conflict", st.Message())
		case codes.Unavailable, codes.DeadlineExceeded:
			respond.Error(w, r, http.StatusServiceUnavailable, "orchestrator_unavailable", "execution service is unavailable")
		default:
			h.log.Error("task signal failed", zap.String("task_id", chi.URLParam(r, "task_id")), zap.Error(err))
			respond.Error(w, r, http.StatusInternalServerError, "internal", "could not signal task")
		}
		return
	}
	respond.JSON(w, http.StatusAccepted, map[string]any{
		"task_id": result.GetTaskId(), "node_type": result.GetNodeType(),
		"accepted": result.GetAccepted(), "control_state": result.GetControlState(),
	})
}

// ListDelegations returns the supervisor-to-child delegation records for one execution hierarchy.
func (h *Handlers) ListDelegations(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	items, err := h.store.ListDelegations(r.Context(), membership.OrgID,
		chi.URLParam(r, "project_id"), chi.URLParam(r, "execution_id"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, "not_found", "execution not found")
			return
		}
		h.log.Error("delegation list failed", zap.Error(err))
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list delegations")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// ListApprovals returns durable approval records and their audit trail.
func (h *Handlers) ListApprovals(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	items, err := h.store.ListApprovals(r.Context(), membership.OrgID,
		chi.URLParam(r, "project_id"), chi.URLParam(r, "execution_id"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respond.Error(w, r, http.StatusNotFound, "not_found", "execution not found")
			return
		}
		h.log.Error("approval list failed", zap.Error(err))
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list approvals")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	stateFilter := r.URL.Query().Get("state")
	if stateFilter != "" && !validExecutionState(stateFilter) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "invalid execution state filter")
		return
	}
	sortOrder := r.URL.Query().Get("sort")
	if sortOrder == "" {
		sortOrder = "created_at_desc"
	}
	if sortOrder != "created_at_desc" && sortOrder != "created_at_asc" {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
			"sort must be created_at_desc or created_at_asc")
		return
	}
	parseTime := func(name string) *time.Time {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			return nil
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", name+" must be RFC3339")
			return nil
		}
		return &value
	}
	createdAfter := parseTime("created_after")
	if r.URL.Query().Get("created_after") != "" && createdAfter == nil {
		return
	}
	createdBefore := parseTime("created_before")
	if r.URL.Query().Get("created_before") != "" && createdBefore == nil {
		return
	}
	if createdAfter != nil && createdBefore != nil && !createdAfter.Before(*createdBefore) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed",
			"created_after must be before created_before")
		return
	}
	limit := pagination.Limit(r, 50, 100)
	items, nextCursor, err := h.store.List(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		limit, r.URL.Query().Get("cursor"), stateFilter, r.URL.Query().Get("workflow_id"),
		sortOrder, createdAfter, createdBefore)
	if err != nil {
		if err.Error() == "invalid cursor" {
			respond.Error(w, r, http.StatusUnprocessableEntity, "invalid_cursor", "cursor is invalid")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list executions")
		return
	}
	response := map[string]any{"items": items}
	if nextCursor != "" {
		response["next_cursor"] = nextCursor
	} else {
		response["next_cursor"] = nil
	}
	respond.JSON(w, http.StatusOK, response)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	execution, err := h.store.Get(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "execution_id"))
	if err != nil {
		h.respondStoreError(w, r, err, "could not load execution")
		return
	}
	respond.JSON(w, http.StatusOK, execution)
}

func (h *Handlers) ListTasks(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	stateFilter := r.URL.Query().Get("state")
	if stateFilter != "" && !validTaskState(stateFilter) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "invalid task state filter")
		return
	}
	items, err := h.store.ListTasks(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "execution_id"), stateFilter, pagination.Limit(r, 100, 500))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list tasks")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handlers) GetTask(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	detail, err := h.store.GetTask(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "execution_id"), chi.URLParam(r, "task_id"))
	if err != nil {
		h.respondStoreError(w, r, err, "could not load task")
		return
	}
	respond.JSON(w, http.StatusOK, detail)
}

func (h *Handlers) ListEvents(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "after must be a non-negative integer")
			return
		}
		after = parsed
	}
	items, err := h.store.ListEvents(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		chi.URLParam(r, "execution_id"), after, pagination.Limit(r, 100, 100))
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, "internal", "could not list events")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

func (h *Handlers) respondStoreError(w http.ResponseWriter, r *http.Request, err error, message string) {
	if errors.Is(err, ErrNotFound) {
		respond.Error(w, r, http.StatusNotFound, "not_found", "execution or task not found")
		return
	}
	h.log.Error("execution read failed", zap.Error(err))
	respond.Error(w, r, http.StatusInternalServerError, "internal", message)
}

func validIdempotencyKey(key string) bool {
	if len(key) < 8 || len(key) > 128 {
		return false
	}
	for _, char := range key {
		if char < 33 || char > 126 {
			return false
		}
	}
	return true
}

func validExecutionState(state string) bool {
	switch state {
	case "pending", "running", "failing", "cancel_requested", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func validTaskState(state string) bool {
	switch state {
	case "pending", "assigned", "running", "succeeded", "failed", "cancelled", "skipped":
		return true
	default:
		return false
	}
}

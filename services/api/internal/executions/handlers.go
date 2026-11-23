// Package executions implements execution submission and read endpoints.
package executions

import (
	"context"
	"encoding/json"
	"errors"
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

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	membership, _ := auth.MembershipFrom(r.Context())
	stateFilter := r.URL.Query().Get("state")
	if stateFilter != "" && !validExecutionState(stateFilter) {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "invalid execution state filter")
		return
	}
	limit := pagination.Limit(r, 50, 100)
	items, nextCursor, err := h.store.List(r.Context(), membership.OrgID, chi.URLParam(r, "project_id"),
		limit, r.URL.Query().Get("cursor"), stateFilter, r.URL.Query().Get("workflow_id"))
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

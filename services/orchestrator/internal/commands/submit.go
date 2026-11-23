package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	orchestratorv1 "github.com/agent-platform/agent-platform/gen/orchestrator/v1"
	"github.com/agent-platform/agent-platform/internal/outbox"
	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/internal/platform/postgres"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

const maxInputBytes = 256 << 10

// Quotas bound execution submission per project.
type Quotas struct {
	MaxActive            int
	MaxQueued            int
	MaxExecutionDuration time.Duration
}

// SubmitExecution validates the pinned workflow version and commits the execution, materialised tasks, event and
// outbox row in one transaction. It never starts work; a scheduler/reaper owns assignments in Phase 05.
func (s *Service) SubmitExecution(ctx context.Context, req *orchestratorv1.SubmitExecutionRequest) (*orchestratorv1.SubmitExecutionResponse, error) {
	if err := validateSubmitRequest(req); err != nil {
		return nil, err
	}

	input := map[string]any{}
	if req.Input != nil {
		input = req.Input.AsMap()
	}
	inputJSON, err := json.Marshal(input)
	if err != nil || len(inputJSON) > maxInputBytes {
		return nil, status.Error(codes.InvalidArgument, "input must be a JSON object of at most 256 KiB")
	}
	inputDigest, err := contracts.Digest(input)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "input cannot be canonicalised")
	}

	now := time.Now().UTC()
	maxDeadline := now.Add(s.quotas.MaxExecutionDuration)
	var requestedDeadline *time.Time
	if req.DeadlineAt != nil {
		if !req.DeadlineAt.IsValid() {
			return nil, status.Error(codes.InvalidArgument, "deadline_at is invalid")
		}
		value := req.DeadlineAt.AsTime().UTC()
		if value.After(maxDeadline) {
			return nil, status.Errorf(codes.InvalidArgument, "deadline_at exceeds the maximum execution duration of %s", s.quotas.MaxExecutionDuration)
		}
		requestedDeadline = &value
	}
	deadline := maxDeadline
	if requestedDeadline != nil {
		deadline = *requestedDeadline
	}

	requestDigest, err := contracts.Digest(struct {
		WorkflowID      string         `json:"workflow_id"`
		WorkflowVersion int32          `json:"workflow_version"`
		Input           map[string]any `json:"input"`
		DeadlineAt      *string        `json:"deadline_at"`
	}{
		WorkflowID:      req.WorkflowId,
		WorkflowVersion: req.WorkflowVersion,
		Input:           input,
		DeadlineAt:      optionalTime(requestedDeadline),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "could not canonicalise the request")
	}

	response := &orchestratorv1.SubmitExecutionResponse{}
	err = postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialize identical request IDs so a concurrent duplicate becomes a replay, not a 409.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
			req.OrgId+":"+req.RequestId); err != nil {
			return err
		}
		// Idempotency is checked first: a replay must return the committed execution, never a new one.
		var existingID, existingDigest, existingState string
		var existingVersion int64
		var existingCreated time.Time
		err := tx.QueryRow(ctx, `
			SELECT id, request_digest, state, state_version, created_at
			FROM execution.executions
			WHERE org_id = $1 AND request_id = $2`,
			req.OrgId, req.RequestId).
			Scan(&existingID, &existingDigest, &existingState, &existingVersion, &existingCreated)
		if err == nil {
			if existingDigest != requestDigest {
				return status.Error(codes.AlreadyExists, "idempotency key was already used with a different request")
			}
			response = &orchestratorv1.SubmitExecutionResponse{
				ExecutionId:  existingID,
				State:        existingState,
				StateVersion: existingVersion,
				Replayed:     true,
				CreatedAt:    timestamppb.New(existingCreated),
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var workflowVersionID, workflowState string
		var graphJSON []byte
		err = tx.QueryRow(ctx, `
			SELECT id, status, graph
			FROM control.workflow_versions
			WHERE org_id = $1 AND project_id = $2 AND workflow_id = $3 AND version = $4`,
			req.OrgId, req.ProjectId, req.WorkflowId, req.WorkflowVersion).
			Scan(&workflowVersionID, &workflowState, &graphJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return status.Error(codes.NotFound, "workflow version not found")
		}
		if err != nil {
			return err
		}
		if workflowState != "published" {
			return status.Error(codes.FailedPrecondition, "workflow version is not published")
		}
		graph, err := contracts.ParseWorkflowGraph(graphJSON)
		if err != nil {
			return status.Errorf(codes.FailedPrecondition, "workflow graph is invalid: %v", err)
		}
		if graph.InputSchema != nil {
			if err := validateAgainstSchema(inputJSON, *graph.InputSchema); err != nil {
				return status.Errorf(codes.InvalidArgument, "input does not match the workflow input schema: %v", err)
			}
		}

		agentVersionIDs := make(map[string]string)
		for _, node := range graph.Nodes {
			if node.Agent == nil {
				continue
			}
			var resolvedID, agentVersionState string
			err := tx.QueryRow(ctx, `
				SELECT id, status FROM control.agent_versions
				WHERE org_id = $1 AND project_id = $2 AND agent_id = $3 AND version = $4`,
				req.OrgId, req.ProjectId, node.Agent.AgentID, node.Agent.Version).
				Scan(&resolvedID, &agentVersionState)
			if errors.Is(err, pgx.ErrNoRows) {
				return status.Errorf(codes.InvalidArgument, "node %q references a missing agent version", node.ID)
			}
			if err != nil {
				return err
			}
			if agentVersionState != "published" && agentVersionState != "deprecated" {
				return status.Errorf(codes.InvalidArgument, "node %q references an unpublished agent version", node.ID)
			}
			agentVersionIDs[node.ID] = resolvedID
		}

		// Serialize project submissions so the quota check and insert cannot overshoot the limit.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
			req.OrgId+":"+req.ProjectId); err != nil {
			return err
		}
		var queued, active int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE state = 'pending'),
			       count(*) FILTER (WHERE state IN ('running', 'failing', 'cancel_requested'))
			FROM execution.executions
			WHERE org_id = $1 AND project_id = $2`,
			req.OrgId, req.ProjectId).Scan(&queued, &active); err != nil {
			return err
		}
		if active >= s.quotas.MaxActive {
			return status.Errorf(codes.ResourceExhausted, "project has reached its active execution limit of %d", s.quotas.MaxActive)
		}
		if queued >= s.quotas.MaxQueued {
			return status.Errorf(codes.ResourceExhausted, "project has reached its queued execution limit of %d", s.quotas.MaxQueued)
		}

		executionID := idgen.New("exec")
		var createdAt time.Time
		err = tx.QueryRow(ctx, `
			INSERT INTO execution.executions
				(id, org_id, project_id, workflow_id, workflow_version_id, state, state_version,
				 request_id, request_digest, input, input_digest, created_by, deadline_at, next_event_seq)
			VALUES ($1, $2, $3, $4, $5, 'pending', 1, $6, $7, $8::jsonb, $9, $10, $11, 2)
			RETURNING created_at`,
			executionID, req.OrgId, req.ProjectId, req.WorkflowId, workflowVersionID,
			req.RequestId, requestDigest, inputJSON, inputDigest, req.RequestedBy, deadline).
			Scan(&createdAt)
		if err != nil {
			if dbErr := asUniqueViolation(err); dbErr {
				return status.Error(codes.AlreadyExists, "an execution with this request id already exists")
			}
			return err
		}

		taskIDs := make(map[string]string, len(graph.Nodes))
		for _, node := range graph.Nodes {
			taskID := idgen.New("task")
			taskIDs[node.ID] = taskID

			maxAttempts := 1
			if node.Retry != nil && node.Retry.MaxAttempts != nil {
				maxAttempts = *node.Retry.MaxAttempts
			}
			timeoutSeconds := 600
			if node.TimeoutSeconds != nil {
				timeoutSeconds = *node.TimeoutSeconds
			}
			sideEffect := string(contracts.SideEffectReadOnly)
			if node.SideEffectClass != nil {
				sideEffect = string(*node.SideEffectClass)
			}

			var agentVersionID, toolVersionID *string
			if node.Type == contracts.NodeTypeAgent {
				value := agentVersionIDs[node.ID]
				agentVersionID = &value
			} else if node.Tool != nil {
				value := fmt.Sprintf("%s:%d", node.Tool.ToolID, node.Tool.Version)
				toolVersionID = &value
			}

			_, err := tx.Exec(ctx, `
				INSERT INTO execution.execution_tasks
					(id, execution_id, org_id, node_id, node_type, agent_version_id, tool_version_id,
					 worker_pool, state, attempt_count, max_attempts, timeout_seconds, side_effect_class)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', 0, $9, $10, $11)`,
				taskID, executionID, req.OrgId, node.ID, string(node.Type),
				nullableArg(agentVersionID), nullableArg(toolVersionID), nullableArg(node.WorkerPool),
				maxAttempts, timeoutSeconds, sideEffect)
			if err != nil {
				return err
			}
		}

		for _, edge := range graph.Edges {
			var whenDigest *string
			if edge.When != nil {
				value := contracts.SHA256Digest([]byte(*edge.When))
				whenDigest = &value
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO execution.task_dependencies
					(execution_id, task_id, depends_on_task_id, org_id, when_expr, when_digest)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				executionID, taskIDs[edge.To], taskIDs[edge.From], req.OrgId,
				nullableArg(edge.When), nullableArg(whenDigest))
			if err != nil {
				return err
			}
		}

		counts := contracts.ExecutionCounts{Total: len(graph.Nodes), Pending: len(graph.Nodes)}
		eventPayload := contracts.ExecutionCreatedPayload{
			Seq:               1,
			WorkflowVersionID: workflowVersionID,
			State:             "pending",
			StateVersion:      1,
			Counts:            counts,
		}
		payloadJSON, err := json.Marshal(eventPayload)
		if err != nil {
			return err
		}
		eventID := idgen.New("evt")
		_, err = tx.Exec(ctx, `
			INSERT INTO execution.execution_events
				(event_id, execution_id, org_id, seq, event_type, entity_type, entity_id,
				 actor_type, actor_id, payload, trace_id)
			VALUES ($1, $2, $3, 1, 'execution.created', 'execution', $2, 'user', $4, $5::jsonb, $6)`,
			eventID, executionID, req.OrgId, req.RequestedBy, payloadJSON, nullableString(req.Traceparent))
		if err != nil {
			return err
		}

		subject := fmt.Sprintf("events.org.%s.execution.%s.created", req.OrgId, executionID)
		message, err := outbox.NewMessage(req.OrgId, subject, "execution.created", &executionID, eventPayload, nil)
		if err != nil {
			return err
		}
		store := outbox.NewStore(s.pool)
		if err := store.Insert(ctx, tx, message); err != nil {
			return err
		}

		response = &orchestratorv1.SubmitExecutionResponse{
			ExecutionId:  executionID,
			State:        "pending",
			StateVersion: 1,
			Replayed:     false,
			CreatedAt:    timestamppb.New(createdAt),
		}
		return nil
	})
	if err != nil {
		s.log.Error("submit execution failed", zap.Error(err))
		return nil, err
	}
	return response, nil
}

func validateSubmitRequest(req *orchestratorv1.SubmitExecutionRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	if req.OrgId == "" || req.ProjectId == "" || req.WorkflowId == "" {
		return status.Error(codes.InvalidArgument, "org_id, project_id and workflow_id are required")
	}
	if req.WorkflowVersion < 1 {
		return status.Error(codes.InvalidArgument, "workflow_version must be at least 1")
	}
	if len(req.RequestId) < 8 || len(req.RequestId) > 128 {
		return status.Error(codes.InvalidArgument, "request_id must be 8..128 characters")
	}
	if req.RequestedBy == "" {
		return status.Error(codes.InvalidArgument, "requested_by is required")
	}
	return nil
}

func optionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func asUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullableArg(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func validateAgainstSchema(inputJSON []byte, schemaMap map[string]any) error {
	schemaJSON, err := json.Marshal(schemaMap)
	if err != nil {
		return fmt.Errorf("workflow input_schema is not serialisable")
	}
	schemaDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return fmt.Errorf("workflow input_schema is invalid")
	}
	const schemaID = "urn:agent-platform:workflow:input"
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(schemaID, schemaDocument); err != nil {
		return fmt.Errorf("workflow input_schema is invalid")
	}
	schema, err := compiler.Compile(schemaID)
	if err != nil {
		return fmt.Errorf("workflow input_schema is invalid")
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(inputJSON))
	if err != nil {
		return fmt.Errorf("input is not valid JSON")
	}
	if err := schema.Validate(instance); err != nil {
		return err
	}
	return nil
}

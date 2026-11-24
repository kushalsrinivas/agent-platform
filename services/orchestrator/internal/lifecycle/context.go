package lifecycle

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

// TaskContext is everything a worker needs to execute one accepted attempt. The orchestrator is the only reader
// of control-plane rows on this path; the worker never queries PostgreSQL.
type TaskContext struct {
	OrgID            string
	ProjectID        string
	ExecutionID      string
	TaskID           string
	AttemptID        string
	NodeID           string
	NodeType         string
	AgentVersionID   string
	ToolVersionID    string
	VersionSpec      map[string]any
	ExecutionInput   map[string]any
	NodeParams       map[string]any
	UpstreamOutputs  map[string]map[string]any
	OutputSchema     map[string]any
	NodeInput        map[string]any
	Deadline         time.Time
	Traceparent      string
	ResumeCheckpoint *ResumeCheckpoint
	IdempotencyKey   string
}

// GetTaskContext returns the pinned, immutable execution context for a live attempt. The fence is the same one
// results use: owner, lease, unexpired and open attempt.
func (s *Service) GetTaskContext(ctx context.Context, workerID, taskID, attemptID, leaseID string) (*TaskContext, error) {
	result := &TaskContext{}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecutionForTask(ctx, tx, taskID)
		if err != nil {
			if isNoRows(err) {
				return ErrStaleLease
			}
			return err
		}
		t, err := s.lockTask(ctx, tx, taskID)
		if err != nil {
			if isNoRows(err) {
				return ErrStaleLease
			}
			return err
		}
		a, err := s.lockAttempt(ctx, tx, attemptID)
		if err != nil {
			if isNoRows(err) {
				return ErrStaleLease
			}
			return err
		}
		if a.TaskID != t.ID || a.ExecutionID != e.ID || a.WorkerID != workerID || a.LeaseID != leaseID ||
			t.OwnerWorkerID == nil || *t.OwnerWorkerID != workerID ||
			t.LeaseID == nil || *t.LeaseID != leaseID || t.LeaseExpired {
			return ErrStaleLease
		}
		if (t.State != "assigned" && t.State != "running") || a.Outcome != nil {
			return ErrStaleLease
		}

		result.OrgID = e.OrgID
		result.ProjectID = e.ProjectID
		result.ExecutionID = e.ID
		result.TaskID = t.ID
		result.AttemptID = a.ID
		result.NodeID = t.NodeID
		result.NodeType = t.NodeType
		if t.AgentVersionID != nil {
			result.AgentVersionID = *t.AgentVersionID
		}
		if t.ToolVersionID != nil {
			result.ToolVersionID = *t.ToolVersionID
		}

		// The task deadline is the node timeout, capped by the execution deadline.
		deadline := time.Now().UTC().Add(time.Duration(t.TimeoutSeconds) * time.Second)
		if e.DeadlineAt != nil && e.DeadlineAt.Before(deadline) {
			deadline = *e.DeadlineAt
		}
		result.Deadline = deadline

		if result.AgentVersionID != "" {
			var specJSON []byte
			if err := tx.QueryRow(ctx, `
				SELECT spec FROM control.agent_versions WHERE id = $1 AND org_id = $2`,
				result.AgentVersionID, e.OrgID).Scan(&specJSON); err != nil {
				return err
			}
			spec := map[string]any{}
			if err := json.Unmarshal(specJSON, &spec); err != nil {
				return err
			}
			result.VersionSpec = spec
		}

		var graphJSON []byte
		if err := tx.QueryRow(ctx, `
			SELECT graph FROM control.workflow_versions WHERE id = $1 AND org_id = $2`,
			e.WorkflowVersionID, e.OrgID).Scan(&graphJSON); err != nil {
			return err
		}
		graph, err := contracts.ParseWorkflowGraph(graphJSON)
		if err != nil {
			return err
		}
		for _, node := range graph.Nodes {
			if node.ID != t.NodeID {
				continue
			}
			if node.Params != nil {
				result.NodeParams = *node.Params
			}
			if node.OutputSchema != nil {
				result.OutputSchema = *node.OutputSchema
			}
			// The workflow node can override the agent-version checkpoint settings. "inherit" keeps the agent
			// default; "none" disables checkpoints for this node.
			if result.VersionSpec != nil {
				applyCheckpointOverride(result.VersionSpec, node.CheckpointMode, node.RequireCheckpoint)
			}
			break
		}
		// Delegated and other synthetic tasks have no workflow node; their pinned task row is authoritative.
		if result.OutputSchema == nil && len(t.OutputSchema) > 2 {
			if err := json.Unmarshal(t.OutputSchema, &result.OutputSchema); err != nil {
				return err
			}
		}
		if result.NodeParams == nil && len(t.Params) > 2 {
			if err := json.Unmarshal(t.Params, &result.NodeParams); err != nil {
				return err
			}
		}

		input := map[string]any{}
		if err := tx.QueryRow(ctx, `
			SELECT input FROM execution.executions WHERE id = $1`, e.ID).Scan(&input); err != nil {
			return err
		}
		result.ExecutionInput = input

		// A tool node gets a logical key that survives retries. External tools must deduplicate on it because a
		// worker crash can happen after the side effect and before the result is committed.
		result.IdempotencyKey = t.ID
		if t.IdempotencyKey != nil && *t.IdempotencyKey != "" {
			result.IdempotencyKey = e.ID + ":" + t.ID + ":" + *t.IdempotencyKey
		}

		resume, err := s.latestCheckpoint(ctx, tx, t, e, a.ID)
		if err != nil {
			return err
		}
		result.ResumeCheckpoint = resume

		deps, err := s.dependencies(ctx, tx, e, t)
		if err != nil {
			return err
		}
		result.UpstreamOutputs = map[string]map[string]any{}
		var inputFrom []string
		if len(t.InputFrom) > 0 && string(t.InputFrom) != "null" {
			_ = json.Unmarshal(t.InputFrom, &inputFrom)
		}
		source := deps.outputs
		if inputFrom == nil {
			source = deps.inboundOutputs
		}
		for nodeID, output := range source {
			if inputFrom == nil {
				result.UpstreamOutputs[nodeID] = output
				continue
			}
			for _, selected := range inputFrom {
				if selected == nodeID {
					result.UpstreamOutputs[nodeID] = output
					break
				}
			}
		}
		nodeInput, err := s.resolveNodeInput(ctx, tx, e, t, deps)
		if err != nil {
			return err
		}
		result.NodeInput = nodeInput
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func applyCheckpointOverride(spec map[string]any, mode *string, require *bool) {
	if mode == nil && require == nil {
		return
	}
	checkpoint, ok := spec["checkpoint"].(map[string]any)
	if !ok {
		checkpoint = map[string]any{}
		spec["checkpoint"] = checkpoint
	}
	if mode != nil && *mode != "inherit" {
		checkpoint["mode"] = *mode
	}
	if require != nil {
		checkpoint["require_checkpoint"] = *require
	}
}

// TraceparentFor returns the propagated trace context for an attempt, if one was recorded.
func (s *Service) TraceparentFor(ctx context.Context, executionID string) string {
	var traceparent pgtype.Text
	err := s.pool.QueryRow(ctx, `
		SELECT trace_id FROM execution.execution_events
		WHERE execution_id = $1 AND event_type = 'execution.created' LIMIT 1`, executionID).Scan(&traceparent)
	if err != nil || !traceparent.Valid {
		return ""
	}
	return traceparent.String
}

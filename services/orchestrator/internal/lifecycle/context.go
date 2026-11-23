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
	OrgID           string
	ProjectID       string
	ExecutionID     string
	TaskID          string
	AttemptID       string
	NodeID          string
	NodeType        string
	AgentVersionID  string
	ToolVersionID   string
	VersionSpec     map[string]any
	ExecutionInput  map[string]any
	NodeParams      map[string]any
	UpstreamOutputs map[string]map[string]any
	OutputSchema    map[string]any
	Deadline        time.Time
	Traceparent     string
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
			break
		}

		input := map[string]any{}
		if err := tx.QueryRow(ctx, `
			SELECT input FROM execution.executions WHERE id = $1`, e.ID).Scan(&input); err != nil {
			return err
		}
		result.ExecutionInput = input

		rows, err := tx.Query(ctx, `
			SELECT upstream.node_id, attempt.result_inline
			FROM execution.task_dependencies d
			JOIN execution.execution_tasks upstream
			  ON upstream.id = d.depends_on_task_id AND upstream.execution_id = d.execution_id
			JOIN execution.task_attempts attempt ON attempt.id = upstream.latest_attempt_id
			WHERE d.task_id = $1 AND d.execution_id = $2 AND upstream.state = 'succeeded'
			  AND attempt.result_inline IS NOT NULL`, t.ID, e.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		result.UpstreamOutputs = map[string]map[string]any{}
		for rows.Next() {
			var nodeID string
			output := map[string]any{}
			if err := rows.Scan(&nodeID, &output); err != nil {
				return err
			}
			result.UpstreamOutputs[nodeID] = output
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return result, nil
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

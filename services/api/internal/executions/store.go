package executions

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

var ErrNotFound = errors.New("execution not found")

type Counts struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Assigned  int `json:"assigned"`
	Running   int `json:"running"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	Skipped   int `json:"skipped"`
}

type Execution struct {
	ID                string     `json:"id"`
	OrgID             string     `json:"org_id"`
	ProjectID         string     `json:"project_id"`
	WorkflowID        string     `json:"workflow_id"`
	WorkflowVersionID string     `json:"workflow_version_id"`
	WorkflowVersion   int        `json:"workflow_version"`
	State             string     `json:"state"`
	StateVersion      int64      `json:"state_version"`
	Priority          int        `json:"priority"`
	Counts            Counts     `json:"counts"`
	InputDigest       string     `json:"input_digest"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	EndedAt           *time.Time `json:"ended_at"`
	DeadlineAt        *time.Time `json:"deadline_at"`
	CancelReason      *string    `json:"cancel_reason"`
	FailureCode       *string    `json:"failure_code"`
	FailureDetail     *string    `json:"failure_detail"`
}

type Task struct {
	ID             string     `json:"id"`
	ExecutionID    string     `json:"execution_id"`
	NodeID         string     `json:"node_id"`
	NodeType       string     `json:"node_type"`
	State          string     `json:"state"`
	StateVersion   int64      `json:"state_version"`
	Priority       int        `json:"priority"`
	AttemptCount   int        `json:"attempt_count"`
	MaxAttempts    int        `json:"max_attempts"`
	AgentVersionID *string    `json:"agent_version_id"`
	ToolVersionID  *string    `json:"tool_version_id"`
	SkipReason     *string    `json:"skip_reason"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at"`
}

// Approval is one durable human review record.
type Approval struct {
	ID          string             `json:"id"`
	TaskID      string             `json:"task_id"`
	Prompt      string             `json:"prompt"`
	Policy      string             `json:"policy"`
	Approvers   []string           `json:"approvers"`
	Status      string             `json:"status"`
	RequestedAt time.Time          `json:"requested_at"`
	ExpiresAt   *time.Time         `json:"expires_at"`
	DecidedAt   *time.Time         `json:"decided_at"`
	DecidedBy   *string            `json:"decided_by"`
	Reason      *string            `json:"reason"`
	Decisions   []ApprovalDecision `json:"decisions"`
}

// ApprovalDecision is one immutable vote in an approval's audit trail.
type ApprovalDecision struct {
	ID         string    `json:"id"`
	ApprovalID string    `json:"approval_id"`
	Actor      string    `json:"actor"`
	Decision   string    `json:"decision"`
	Comment    string    `json:"comment"`
	CreatedAt  time.Time `json:"created_at"`
}

// Delegation is one supervisor-to-child assignment in an execution hierarchy.
type Delegation struct {
	ID                string     `json:"id"`
	RootExecutionID   string     `json:"root_execution_id"`
	ParentExecutionID string     `json:"parent_execution_id"`
	ParentTaskID      string     `json:"parent_task_id"`
	ChildExecutionID  string     `json:"child_execution_id"`
	AgentID           string     `json:"agent_id"`
	AgentVersion      int        `json:"agent_version"`
	Status            string     `json:"status"`
	ErrorCode         *string    `json:"error_code"`
	ErrorMessage      *string    `json:"error_message"`
	CreatedAt         time.Time  `json:"created_at"`
	FinishedAt        *time.Time `json:"finished_at"`
}

type Attempt struct {
	ID                  string          `json:"id"`
	TaskID              string          `json:"task_id"`
	AttemptNumber       int             `json:"attempt_number"`
	WorkerID            string          `json:"worker_id"`
	Outcome             *string         `json:"outcome"`
	OutcomeUnknown      bool            `json:"outcome_unknown"`
	EffectUncertain     bool            `json:"effect_uncertain"`
	ErrorCode           *string         `json:"error_code"`
	ErrorMessage        *string         `json:"error_message"`
	DispatchedAt        time.Time       `json:"dispatched_at"`
	AcceptedAt          *time.Time      `json:"accepted_at"`
	StartedAt           *time.Time      `json:"started_at"`
	FinishedAt          *time.Time      `json:"finished_at"`
	ResultAvailable     bool            `json:"result_available"`
	CheckpointAvailable bool            `json:"checkpoint_available"`
	Metrics             json.RawMessage `json:"metrics"`
}

type TaskResult struct {
	Outcome      string          `json:"outcome"`
	Output       json.RawMessage `json:"output"`
	ErrorCode    *string         `json:"error_code"`
	ErrorMessage *string         `json:"error_message"`
}

type TaskDetail struct {
	Task
	Attempts []Attempt   `json:"attempts"`
	Result   *TaskResult `json:"result"`
}

type Event struct {
	Seq        int64           `json:"seq"`
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id"`
	FromState  *string         `json:"from_state"`
	ToState    *string         `json:"to_state"`
	Reason     *string         `json:"reason"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

const executionSelect = `
	SELECT e.id, e.org_id, e.project_id, e.workflow_id, e.workflow_version_id,
	       COALESCE(wv.version, 0), e.state, e.state_version, e.priority, e.input_digest, e.created_by,
	       e.created_at, e.started_at, e.ended_at, e.deadline_at, e.cancel_reason,
	       e.failure_code, e.failure_detail, COALESCE(c.counts, '{}'::jsonb)
	FROM execution.executions e
	LEFT JOIN control.workflow_versions wv ON wv.id = e.workflow_version_id AND wv.org_id = e.org_id
	LEFT JOIN LATERAL (
		SELECT jsonb_object_agg(state, cnt) AS counts
		FROM (
			SELECT state, count(*)::int AS cnt
			FROM execution.execution_tasks
			WHERE execution_id = e.id
			GROUP BY state
		) grouped
	) c ON true`

func (s *Store) Get(ctx context.Context, orgID, projectID, executionID string) (Execution, error) {
	var execution Execution
	var counts []byte
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, executionSelect+`
			WHERE e.org_id = $1 AND e.project_id = $2 AND e.id = $3`,
			orgID, projectID, executionID).
			Scan(&execution.ID, &execution.OrgID, &execution.ProjectID, &execution.WorkflowID,
				&execution.WorkflowVersionID, &execution.WorkflowVersion, &execution.State,
				&execution.StateVersion, &execution.Priority, &execution.InputDigest, &execution.CreatedBy,
				&execution.CreatedAt, &execution.StartedAt, &execution.EndedAt, &execution.DeadlineAt,
				&execution.CancelReason, &execution.FailureCode, &execution.FailureDetail, &counts)
	})
	if database.IsNotFound(err) {
		return Execution{}, ErrNotFound
	}
	if err != nil {
		return Execution{}, err
	}
	execution.Counts = decodeCounts(counts)
	return execution, nil
}

func (s *Store) List(ctx context.Context, orgID, projectID string, limit int, cursor, state, workflowID,
	sortOrder string, createdAfter, createdBefore *time.Time) ([]Execution, string, error) {
	executions := make([]Execution, 0, limit)
	var nextCursor string
	direction := "DESC"
	comparison := "<"
	if sortOrder == "created_at_asc" {
		direction = "ASC"
		comparison = ">"
	}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		query := executionSelect + ` WHERE e.org_id = $1 AND e.project_id = $2`
		args := []any{orgID, projectID}
		if state != "" {
			args = append(args, state)
			query += ` AND e.state = $` + itoa(len(args))
		}
		if workflowID != "" {
			args = append(args, workflowID)
			query += ` AND e.workflow_id = $` + itoa(len(args))
		}
		if createdAfter != nil {
			args = append(args, *createdAfter)
			query += ` AND e.created_at >= $` + itoa(len(args))
		}
		if createdBefore != nil {
			args = append(args, *createdBefore)
			query += ` AND e.created_at < $` + itoa(len(args))
		}
		if cursor != "" {
			parts, err := database.DecodeCursor(cursor)
			if err != nil || len(parts) != 2 {
				return errors.New("invalid cursor")
			}
			args = append(args, parts[0], parts[1])
			query += ` AND (e.created_at, e.id) ` + comparison + ` ($` + itoa(len(args)-1) +
				`::timestamptz, $` + itoa(len(args)) + `)`
		}
		args = append(args, limit+1)
		query += ` ORDER BY e.created_at ` + direction + `, e.id ` + direction + ` LIMIT $` + itoa(len(args))

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var execution Execution
			var counts []byte
			if err := rows.Scan(&execution.ID, &execution.OrgID, &execution.ProjectID, &execution.WorkflowID,
				&execution.WorkflowVersionID, &execution.WorkflowVersion, &execution.State,
				&execution.StateVersion, &execution.Priority, &execution.InputDigest, &execution.CreatedBy,
				&execution.CreatedAt, &execution.StartedAt, &execution.EndedAt, &execution.DeadlineAt,
				&execution.CancelReason, &execution.FailureCode, &execution.FailureDetail, &counts); err != nil {
				return err
			}
			execution.Counts = decodeCounts(counts)
			executions = append(executions, execution)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(executions) > limit {
			executions = executions[:limit]
			last := executions[limit-1]
			nextCursor = database.EncodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
		}
		return nil
	})
	return executions, nextCursor, err
}

func (s *Store) ListTasks(ctx context.Context, orgID, projectID, executionID, state string, limit int) ([]Task, error) {
	tasks := make([]Task, 0, limit)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		query := `
			SELECT t.id, t.execution_id, t.node_id, t.node_type, t.state, t.state_version,
			       t.priority, t.attempt_count, t.max_attempts, t.agent_version_id, t.tool_version_id,
			       t.skip_reason, t.created_at, t.started_at, t.ended_at
			FROM execution.execution_tasks t
			JOIN execution.executions e ON e.id = t.execution_id
			WHERE t.org_id = $1 AND t.execution_id = $2 AND e.project_id = $3`
		args := []any{orgID, executionID, projectID}
		if state != "" {
			args = append(args, state)
			query += ` AND t.state = $` + itoa(len(args))
		}
		args = append(args, limit)
		query += ` ORDER BY t.node_id LIMIT $` + itoa(len(args))

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var task Task
			if err := rows.Scan(&task.ID, &task.ExecutionID, &task.NodeID, &task.NodeType,
				&task.State, &task.StateVersion, &task.Priority, &task.AttemptCount, &task.MaxAttempts,
				&task.AgentVersionID, &task.ToolVersionID, &task.SkipReason,
				&task.CreatedAt, &task.StartedAt, &task.EndedAt); err != nil {
				return err
			}
			tasks = append(tasks, task)
		}
		return rows.Err()
	})
	return tasks, err
}

func (s *Store) GetTask(ctx context.Context, orgID, projectID, executionID, taskID string) (TaskDetail, error) {
	var detail TaskDetail
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT t.id, t.execution_id, t.node_id, t.node_type, t.state, t.state_version,
			       t.priority, t.attempt_count, t.max_attempts, t.agent_version_id, t.tool_version_id,
			       t.skip_reason, t.created_at, t.started_at, t.ended_at
			FROM execution.execution_tasks t
			JOIN execution.executions e ON e.id = t.execution_id
			WHERE t.org_id = $1 AND t.execution_id = $2 AND t.id = $3 AND e.project_id = $4`,
			orgID, executionID, taskID, projectID).
			Scan(&detail.ID, &detail.ExecutionID, &detail.NodeID, &detail.NodeType, &detail.State,
				&detail.StateVersion, &detail.Priority, &detail.AttemptCount, &detail.MaxAttempts,
				&detail.AgentVersionID, &detail.ToolVersionID, &detail.SkipReason,
				&detail.CreatedAt, &detail.StartedAt, &detail.EndedAt)
		if err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT id, task_id, attempt_number, worker_id, outcome, outcome_unknown, effect_uncertain,
			       error_code, error_message, dispatched_at, accepted_at, started_at, finished_at,
			       (result_inline IS NOT NULL OR result_object_key IS NOT NULL), (checkpoint_id IS NOT NULL),
			       COALESCE(metrics, '{}'::jsonb)
			FROM execution.task_attempts
			WHERE org_id = $1 AND task_id = $2
			ORDER BY attempt_number`, orgID, taskID)
		if err != nil {
			return err
		}
		defer rows.Close()
		detail.Attempts = make([]Attempt, 0)
		for rows.Next() {
			var attempt Attempt
			if err := rows.Scan(&attempt.ID, &attempt.TaskID, &attempt.AttemptNumber, &attempt.WorkerID,
				&attempt.Outcome, &attempt.OutcomeUnknown, &attempt.EffectUncertain,
				&attempt.ErrorCode, &attempt.ErrorMessage, &attempt.DispatchedAt,
				&attempt.AcceptedAt, &attempt.StartedAt, &attempt.FinishedAt,
				&attempt.ResultAvailable, &attempt.CheckpointAvailable, &attempt.Metrics); err != nil {
				return err
			}
			detail.Attempts = append(detail.Attempts, attempt)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		var result TaskResult
		var output json.RawMessage
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(a.result_inline, t.result_inline, 'null'::jsonb),
			       COALESCE(a.outcome, t.state), a.error_code, a.error_message
			FROM execution.execution_tasks t
			LEFT JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
			WHERE t.org_id = $1 AND t.id = $2`,
			orgID, taskID).Scan(&output, &result.Outcome, &result.ErrorCode, &result.ErrorMessage); err != nil {
			return err
		}
		result.Output = output
		detail.Result = &result
		return nil
	})
	if database.IsNotFound(err) {
		return TaskDetail{}, ErrNotFound
	}
	return detail, err
}

func (s *Store) ListEvents(ctx context.Context, orgID, projectID, executionID string, after int64, limit int) ([]Event, error) {
	events := make([]Event, 0, limit)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT ev.seq, ev.event_id, ev.event_type, ev.entity_type, ev.entity_id,
			       ev.actor_type, ev.actor_id, ev.from_state, ev.to_state, ev.reason,
			       ev.payload, ev.created_at
			FROM execution.execution_events ev
			JOIN execution.executions e ON e.id = ev.execution_id
			WHERE ev.org_id = $1 AND ev.execution_id = $2 AND e.project_id = $3 AND ev.seq > $4
			ORDER BY ev.seq
			LIMIT $5`, orgID, executionID, projectID, after, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var event Event
			if err := rows.Scan(&event.Seq, &event.EventID, &event.EventType, &event.EntityType,
				&event.EntityID, &event.ActorType, &event.ActorID, &event.FromState, &event.ToState,
				&event.Reason, &event.Payload, &event.CreatedAt); err != nil {
				return err
			}
			events = append(events, event)
		}
		return rows.Err()
	})
	return events, err
}

func decodeCounts(raw []byte) Counts {
	var counts Counts
	if len(raw) == 0 {
		return counts
	}
	var states map[string]int
	if err := json.Unmarshal(raw, &states); err != nil {
		return counts
	}
	for state, count := range states {
		counts.Total += count
		switch state {
		case "pending":
			counts.Pending = count
		case "assigned":
			counts.Assigned = count
		case "running":
			counts.Running = count
		case "succeeded":
			counts.Succeeded = count
		case "failed":
			counts.Failed = count
		case "cancelled":
			counts.Cancelled = count
		case "skipped":
			counts.Skipped = count
		}
	}
	return counts
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}

// Input returns the original submitted input for a replay.
// ListDelegations returns the delegations in one root execution hierarchy.
func (s *Store) ListDelegations(ctx context.Context, orgID, projectID, executionID string) ([]Delegation, error) {
	delegations := []Delegation{}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM execution.executions
				WHERE id = $1 AND org_id = $2 AND project_id = $3
			)`, executionID, orgID, projectID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		rows, err := tx.Query(ctx, `
			SELECT id, root_execution_id, parent_execution_id, parent_task_id, child_execution_id,
			       agent_id, agent_version, status, error_code, error_message, created_at, finished_at
			FROM execution.delegations
			WHERE org_id = $1 AND root_execution_id = $2
			ORDER BY created_at, id`, orgID, executionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var delegation Delegation
			if err := rows.Scan(&delegation.ID, &delegation.RootExecutionID, &delegation.ParentExecutionID,
				&delegation.ParentTaskID, &delegation.ChildExecutionID, &delegation.AgentID,
				&delegation.AgentVersion, &delegation.Status, &delegation.ErrorCode, &delegation.ErrorMessage,
				&delegation.CreatedAt, &delegation.FinishedAt); err != nil {
				return err
			}
			delegations = append(delegations, delegation)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return delegations, nil
}

// ListApprovals returns the approval audit rows for one execution.
func (s *Store) ListApprovals(ctx context.Context, orgID, projectID, executionID string) ([]Approval, error) {
	approvals := []Approval{}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM execution.executions
				WHERE id = $1 AND org_id = $2 AND project_id = $3
			)`, executionID, orgID, projectID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		rows, err := tx.Query(ctx, `
			SELECT id, task_id, prompt, policy, approvers, status, requested_at, expires_at,
			       decided_at, decided_by, reason
			FROM execution.approvals
			WHERE execution_id = $1
			ORDER BY requested_at, id`, executionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		index := map[string]int{}
		for rows.Next() {
			var approval Approval
			if err := rows.Scan(&approval.ID, &approval.TaskID, &approval.Prompt, &approval.Policy,
				&approval.Approvers, &approval.Status, &approval.RequestedAt, &approval.ExpiresAt,
				&approval.DecidedAt, &approval.DecidedBy, &approval.Reason); err != nil {
				return err
			}
			approval.Decisions = []ApprovalDecision{}
			index[approval.ID] = len(approvals)
			approvals = append(approvals, approval)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		decisionRows, err := tx.Query(ctx, `
			SELECT d.id, d.approval_id, d.actor, d.decision, d.comment, d.created_at
			FROM execution.approval_decisions d
			JOIN execution.approvals a ON a.id = d.approval_id
			WHERE a.execution_id = $1
			ORDER BY d.created_at, d.id`, executionID)
		if err != nil {
			return err
		}
		defer decisionRows.Close()
		for decisionRows.Next() {
			var decision ApprovalDecision
			if err := decisionRows.Scan(&decision.ID, &decision.ApprovalID, &decision.Actor,
				&decision.Decision, &decision.Comment, &decision.CreatedAt); err != nil {
				return err
			}
			if position, ok := index[decision.ApprovalID]; ok {
				approvals[position].Decisions = append(approvals[position].Decisions, decision)
			}
		}
		return decisionRows.Err()
	})
	if err != nil {
		return nil, err
	}
	return approvals, nil
}

func (s *Store) Input(ctx context.Context, orgID, projectID, executionID string) (json.RawMessage, error) {
	var input json.RawMessage
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT e.input FROM execution.executions e
			WHERE e.org_id = $1 AND e.project_id = $2 AND e.id = $3`,
			orgID, projectID, executionID).Scan(&input)
	})
	if database.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return input, err
}

// Outputs returns the latest inline output of every succeeded task.
func (s *Store) Outputs(ctx context.Context, orgID, projectID, executionID string) (map[string]json.RawMessage, error) {
	outputs := map[string]json.RawMessage{}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT t.node_id, COALESCE(a.result_inline, t.result_inline, '{}'::jsonb)
			FROM execution.execution_tasks t
			JOIN execution.executions e ON e.id = t.execution_id
			LEFT JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
			WHERE t.org_id = $1 AND t.execution_id = $2 AND e.project_id = $3
			  AND t.state = 'succeeded'
			ORDER BY t.node_id`, orgID, executionID, projectID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var nodeID string
			var output json.RawMessage
			if err := rows.Scan(&nodeID, &output); err != nil {
				return err
			}
			outputs[nodeID] = output
		}
		return rows.Err()
	})
	return outputs, err
}

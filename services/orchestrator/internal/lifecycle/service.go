// Package lifecycle owns every execution and task state transition after submission: dispatch, worker
// acceptance, fenced results, retries, completion and lease expiry. PostgreSQL is authoritative; NATS only
// transports committed decisions.
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/outbox"
	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/internal/platform/postgres"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

const (
	// LeaseTTL bounds how long a crashed worker can hold a task before the reaper retries it.
	LeaseTTL = 30 * time.Second
	// HeartbeatTimeout is when a worker stops receiving new assignments.
	HeartbeatTimeout = 15 * time.Second
)

// Service applies lifecycle transitions inside short database transactions.
type Service struct {
	pool     *pgxpool.Pool
	log      *zap.Logger
	store    *outbox.Store
	leaseTTL time.Duration
}

// New builds the lifecycle service.
func New(pool *pgxpool.Pool, log *zap.Logger) *Service {
	return &Service{pool: pool, log: log, store: outbox.NewStore(pool), leaseTTL: LeaseTTL}
}

type execution struct {
	ID                string
	OrgID             string
	ProjectID         string
	WorkflowVersionID string
	State             string
	StateVersion      int64
	NextEventSeq      int64
	DeadlineAt        *time.Time
	StartedAt         *time.Time
	EndedAt           *time.Time
	FailureCode       *string
}

type task struct {
	ID              string
	ExecutionID     string
	OrgID           string
	NodeID          string
	NodeType        string
	State           string
	StateVersion    int64
	AttemptCount    int
	MaxAttempts     int
	TimeoutSeconds  int
	AgentVersionID  *string
	ToolVersionID   *string
	WorkerPool      *string
	OwnerWorkerID   *string
	LeaseID         *string
	LeaseExpiresAt  *time.Time
	LatestAttemptID *string
	LeaseExpired    bool
}

type attempt struct {
	ID            string
	TaskID        string
	ExecutionID   string
	OrgID         string
	AttemptNumber int
	WorkerID      string
	LeaseID       string
	Outcome       *string
	DispatchedAt  time.Time
	AcceptedAt    *time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
}

func (s *Service) lockExecution(ctx context.Context, tx pgx.Tx, executionID string) (*execution, error) {
	var e execution
	var deadline, started, ended pgtype.Timestamptz
	var failure pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT id, org_id, project_id, workflow_version_id, state, state_version, next_event_seq,
		       deadline_at, started_at, ended_at, failure_code
		FROM execution.executions
		WHERE id = $1
		FOR UPDATE`, executionID).
		Scan(&e.ID, &e.OrgID, &e.ProjectID, &e.WorkflowVersionID, &e.State, &e.StateVersion,
			&e.NextEventSeq, &deadline, &started, &ended, &failure)
	if err != nil {
		return nil, err
	}
	e.DeadlineAt = timePtr(deadline)
	e.StartedAt = timePtr(started)
	e.EndedAt = timePtr(ended)
	e.FailureCode = textPtr(failure)
	return &e, nil
}

func (s *Service) lockExecutionForTask(ctx context.Context, tx pgx.Tx, taskID string) (*execution, error) {
	var executionID string
	if err := tx.QueryRow(ctx,
		`SELECT execution_id FROM execution.execution_tasks WHERE id = $1`, taskID).Scan(&executionID); err != nil {
		return nil, err
	}
	return s.lockExecution(ctx, tx, executionID)
}

func (s *Service) lockTask(ctx context.Context, tx pgx.Tx, taskID string) (*task, error) {
	var t task
	var agentVersion, toolVersion, workerPool, owner, lease, latest pgtype.Text
	var leaseExpires pgtype.Timestamptz
	err := tx.QueryRow(ctx, `
		SELECT id, execution_id, org_id, node_id, node_type, state, state_version, attempt_count,
		       max_attempts, timeout_seconds, agent_version_id, tool_version_id, worker_pool,
		       owner_worker_id, lease_id, lease_expires_at, latest_attempt_id,
		       (lease_expires_at IS NOT NULL AND lease_expires_at <= now())
		FROM execution.execution_tasks
		WHERE id = $1
		FOR UPDATE`, taskID).
		Scan(&t.ID, &t.ExecutionID, &t.OrgID, &t.NodeID, &t.NodeType, &t.State, &t.StateVersion,
			&t.AttemptCount, &t.MaxAttempts, &t.TimeoutSeconds, &agentVersion, &toolVersion, &workerPool,
			&owner, &lease, &leaseExpires, &latest, &t.LeaseExpired)
	if err != nil {
		return nil, err
	}
	t.AgentVersionID = textPtr(agentVersion)
	t.ToolVersionID = textPtr(toolVersion)
	t.WorkerPool = textPtr(workerPool)
	t.OwnerWorkerID = textPtr(owner)
	t.LeaseID = textPtr(lease)
	t.LeaseExpiresAt = timePtr(leaseExpires)
	t.LatestAttemptID = textPtr(latest)
	return &t, nil
}

func (s *Service) lockAttempt(ctx context.Context, tx pgx.Tx, attemptID string) (*attempt, error) {
	var a attempt
	var outcome pgtype.Text
	var accepted, started, finished pgtype.Timestamptz
	err := tx.QueryRow(ctx, `
		SELECT id, task_id, execution_id, org_id, attempt_number, worker_id, lease_id, outcome,
		       dispatched_at, accepted_at, started_at, finished_at
		FROM execution.task_attempts
		WHERE id = $1
		FOR UPDATE`, attemptID).
		Scan(&a.ID, &a.TaskID, &a.ExecutionID, &a.OrgID, &a.AttemptNumber, &a.WorkerID, &a.LeaseID,
			&outcome, &a.DispatchedAt, &accepted, &started, &finished)
	if err != nil {
		return nil, err
	}
	a.Outcome = textPtr(outcome)
	a.AcceptedAt = timePtr(accepted)
	a.StartedAt = timePtr(started)
	a.FinishedAt = timePtr(finished)
	return &a, nil
}

type eventSpec struct {
	MessageType string
	EntityType  string
	EntityID    string
	From        *string
	To          *string
	ActorType   string
	ActorID     string
	Subject     string
	TaskID      *string
	AttemptID   *string
	WorkerID    *string
	Payload     func(seq int64) any
}

func (s *Service) appendEvent(ctx context.Context, tx pgx.Tx, e *execution, spec eventSpec) error {
	seq := e.NextEventSeq
	payload, err := json.Marshal(spec.Payload(seq))
	if err != nil {
		return fmt.Errorf("lifecycle: marshal event payload: %w", err)
	}
	eventID := idgen.New("evt")
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution.execution_events
			(event_id, execution_id, org_id, seq, event_type, entity_type, entity_id,
			 actor_type, actor_id, from_state, to_state, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb)`,
		eventID, e.ID, e.OrgID, seq, spec.MessageType, spec.EntityType, spec.EntityID,
		spec.ActorType, spec.ActorID, spec.From, spec.To, payload); err != nil {
		return fmt.Errorf("lifecycle: insert event: %w", err)
	}

	message, err := outbox.NewMessage(e.OrgID, e.ProjectID, spec.Subject, spec.MessageType, &e.ID, spec.Payload(seq), nil)
	if err != nil {
		return err
	}
	message.TaskID = spec.TaskID
	message.AttemptID = spec.AttemptID
	message.WorkerID = spec.WorkerID
	message.CorrelationID = &e.ID
	if err := s.store.Insert(ctx, tx, message); err != nil {
		return err
	}

	e.NextEventSeq = seq + 1
	if _, err := tx.Exec(ctx,
		`UPDATE execution.executions SET next_event_seq = $2 WHERE id = $1`, e.ID, e.NextEventSeq); err != nil {
		return fmt.Errorf("lifecycle: advance event sequence: %w", err)
	}
	return nil
}

func executionSubject(e *execution, suffix string) string {
	return fmt.Sprintf("events.org.%s.execution.%s.%s", e.OrgID, e.ID, suffix)
}

func taskSubject(e *execution, taskID, suffix string) string {
	return fmt.Sprintf("events.org.%s.execution.%s.task.%s.%s", e.OrgID, e.ID, taskID, suffix)
}

func (s *Service) counts(ctx context.Context, tx pgx.Tx, executionID string) (contracts.ExecutionCounts, error) {
	var counts contracts.ExecutionCounts
	err := tx.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE state = 'pending'),
		       count(*) FILTER (WHERE state = 'assigned'),
		       count(*) FILTER (WHERE state = 'running'),
		       count(*) FILTER (WHERE state = 'succeeded'),
		       count(*) FILTER (WHERE state = 'failed'),
		       count(*) FILTER (WHERE state = 'cancelled'),
		       count(*) FILTER (WHERE state = 'skipped')
		FROM execution.execution_tasks
		WHERE execution_id = $1`, executionID).
		Scan(&counts.Total, &counts.Pending, &counts.Assigned, &counts.Running,
			&counts.Succeeded, &counts.Failed, &counts.Cancelled, &counts.Skipped)
	return counts, err
}

func (s *Service) transitionExecution(ctx context.Context, tx pgx.Tx, e *execution, to, reason string,
	failureCode *string) error {
	if e.State == to {
		return nil
	}
	from := e.State
	e.StateVersion++
	var startedAt, endedAt *time.Time
	if e.StartedAt != nil {
		startedAt = e.StartedAt
	}
	if to == "running" && startedAt == nil {
		now := time.Now().UTC()
		startedAt = &now
	}
	if terminalState(to) {
		now := time.Now().UTC()
		endedAt = &now
	}
	if _, err := tx.Exec(ctx, `
		UPDATE execution.executions
		SET state = $2, state_version = $3, started_at = $4, ended_at = $5,
		    failure_code = COALESCE($6, failure_code)
		WHERE id = $1`,
		e.ID, to, e.StateVersion, startedAt, endedAt, failureCode); err != nil {
		return fmt.Errorf("lifecycle: transition execution: %w", err)
	}
	e.State = to
	e.StartedAt = startedAt
	e.EndedAt = endedAt
	if failureCode != nil {
		e.FailureCode = failureCode
	}

	// The counts reflect the transitioned task set; both writers share this transaction.
	counts, err := s.counts(ctx, tx, e.ID)
	if err != nil {
		return err
	}
	var reasonPtr, codePtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	if failureCode != nil {
		codePtr = failureCode
	}
	return s.appendEvent(ctx, tx, e, eventSpec{
		MessageType: contracts.EventExecutionStateChanged,
		EntityType:  "execution",
		EntityID:    e.ID,
		From:        &from,
		To:          &to,
		ActorType:   "system",
		ActorID:     "orchestrator",
		Subject:     executionSubject(e, "state_changed"),
		Payload: func(seq int64) any {
			return contracts.ExecutionStateChangedPayload{
				Seq:          seq,
				From:         from,
				To:           to,
				StateVersion: e.StateVersion,
				Reason:       reasonPtr,
				FailureCode:  codePtr,
				Counts:       counts,
			}
		},
	})
}

// evaluateCompletion is called in the transaction of the task transition that may have finished the last task.
func (s *Service) evaluateCompletion(ctx context.Context, tx pgx.Tx, e *execution) error {
	if terminalState(e.State) {
		return nil
	}
	counts, err := s.counts(ctx, tx, e.ID)
	if err != nil {
		return err
	}
	if counts.Pending+counts.Assigned+counts.Running > 0 {
		return nil
	}

	switch e.State {
	case "running":
		if counts.Failed == 0 && counts.Cancelled == 0 && counts.Succeeded > 0 {
			var blockedSkips int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM execution.execution_tasks
				WHERE execution_id = $1 AND state = 'skipped' AND skip_reason = 'dependency_failed'`,
				e.ID).Scan(&blockedSkips); err != nil {
				return err
			}
			if blockedSkips == 0 {
				return s.transitionExecution(ctx, tx, e, "succeeded", "all_tasks_terminal", nil)
			}
		}
		code := "TASK_FAILED"
		return s.transitionExecution(ctx, tx, e, "failed", "all_tasks_terminal", &code)
	case "failing":
		code := "TASK_FAILED"
		if e.FailureCode != nil {
			code = *e.FailureCode
		}
		return s.transitionExecution(ctx, tx, e, "failed", "failure_quiescent", &code)
	case "pending":
		code := "NO_TASKS_STARTED"
		if err := s.transitionExecution(ctx, tx, e, "failing", "no_schedulable_tasks", &code); err != nil {
			return err
		}
		return s.transitionExecution(ctx, tx, e, "failed", "failure_quiescent", &code)
	case "cancel_requested":
		// Cancellation propagation is deferred; finalise conservatively as cancelled.
		return s.transitionExecution(ctx, tx, e, "cancelled", "cancellation_quiescent", nil)
	}
	return nil
}

// failFast cancels all pending work and then evaluates quiescence.
func (s *Service) failFast(ctx context.Context, tx pgx.Tx, e *execution, code string) error {
	if e.State == "pending" || e.State == "running" {
		if err := s.transitionExecution(ctx, tx, e, "failing", "task_failed", &code); err != nil {
			return err
		}
	}
	if err := s.cancelPendingTasks(ctx, tx, e); err != nil {
		return err
	}
	return s.evaluateCompletion(ctx, tx, e)
}

func (s *Service) cancelPendingTasks(ctx context.Context, tx pgx.Tx, e *execution) error {
	rows, err := tx.Query(ctx, `
		SELECT id, node_id, state_version
		FROM execution.execution_tasks
		WHERE execution_id = $1 AND state = 'pending'
		ORDER BY created_at
		FOR UPDATE`, e.ID)
	if err != nil {
		return err
	}
	type pendingTask struct {
		id, nodeID string
		version    int64
	}
	var pending []pendingTask
	for rows.Next() {
		var item pendingTask
		if err := rows.Scan(&item.id, &item.nodeID, &item.version); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, item := range pending {
		if _, err := tx.Exec(ctx, `
			UPDATE execution.execution_tasks
			SET state = 'cancelled', state_version = state_version + 1, ended_at = now(),
			    owner_worker_id = NULL, lease_id = NULL, lease_expires_at = NULL, updated_at = now()
			WHERE id = $1 AND state = 'pending'`, item.id); err != nil {
			return err
		}
		from, to := "pending", "cancelled"
		taskID, nodeID, version := item.id, item.nodeID, item.version+1
		if err := s.appendEvent(ctx, tx, e, eventSpec{
			MessageType: contracts.EventTaskStateChanged,
			EntityType:  "task",
			EntityID:    taskID,
			From:        &from,
			To:          &to,
			ActorType:   "system",
			ActorID:     "orchestrator",
			Subject:     taskSubject(e, taskID, "state_changed"),
			TaskID:      &taskID,
			Payload: func(seq int64) any {
				return contracts.TaskStateChangedPayload{
					Seq: seq, TaskID: taskID, NodeID: nodeID,
					From: from, To: to, StateVersion: version,
				}
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) releaseWorkerSlot(ctx context.Context, tx pgx.Tx, workerID string) error {
	if workerID == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE execution.workers
		SET reserved_slots = GREATEST(reserved_slots - 1, 0), updated_at = now()
		WHERE id = $1`, workerID)
	return err
}

func retryBackoff(attemptNumber int) time.Duration {
	base := time.Second
	for i := 1; i < attemptNumber && base < time.Minute; i++ {
		base *= 2
	}
	if base > time.Minute {
		base = time.Minute
	}
	jitter := 0.5 + rand.Float64() //nolint:gosec // retry jitter is not a security decision.
	return time.Duration(float64(base) * jitter)
}

func terminalState(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func timePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func ptr[T any](value T) *T {
	return &value
}

// withTx is a small alias so the lifecycle files share one transaction helper.
func (s *Service) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return postgres.WithTx(ctx, s.pool, fn)
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// OutboxStats is the operator-facing publisher lag snapshot.
type OutboxStats struct {
	Pending           int64      `json:"pending"`
	OldestAvailableAt *time.Time `json:"oldest_available_at,omitempty"`
	Retrying          int64      `json:"retrying"`
}

// OutboxStats reports unpublished rows and how long the oldest has been due.
func (s *Service) OutboxStats(ctx context.Context) (OutboxStats, error) {
	var stats OutboxStats
	var oldest pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), min(available_at), count(*) FILTER (WHERE attempts > 0)
		FROM execution.outbox
		WHERE published_at IS NULL`).Scan(&stats.Pending, &oldest, &stats.Retrying)
	if err != nil {
		return OutboxStats{}, err
	}
	stats.OldestAvailableAt = timePtr(oldest)
	return stats, nil
}

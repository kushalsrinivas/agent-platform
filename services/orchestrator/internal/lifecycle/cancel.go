package lifecycle

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

// CancelOutcome is the durable result of a cancellation command.
type CancelOutcome struct {
	ExecutionID  string
	State        string
	StateVersion int64
	CancelEpoch  int
	Accepted     bool
}

// ErrNotFound is returned when an execution does not belong to the requested organization.
var ErrNotFound = errors.New("lifecycle: execution not found")

// CancelExecution records a durable cancellation request and asks live workers to stop their attempts. The
// transaction is the decision: a later orchestrator restart sees cancel_requested and can never restart work.
func (s *Service) CancelExecution(ctx context.Context, orgID, executionID, requestedBy, reason string) (CancelOutcome, error) {
	if strings.TrimSpace(orgID) == "" || strings.TrimSpace(executionID) == "" {
		return CancelOutcome{}, errors.New("lifecycle: organization and execution are required")
	}
	if requestedBy == "" {
		requestedBy = "operator"
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	outcome := CancelOutcome{ExecutionID: executionID}
	var notifications []CancelNotification
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecution(ctx, tx, executionID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if e.OrgID != orgID {
			return ErrNotFound
		}
		outcome.State = e.State
		outcome.StateVersion = e.StateVersion
		outcome.CancelEpoch = e.CancelEpoch
		if e.State == "cancelled" {
			// Cancellation is idempotent after completion as documented by the API contract.
			outcome.Accepted = true
			return nil
		}
		if e.State == "cancel_requested" {
			outcome.Accepted = true
			// Re-send the command on a retry: the first attempt may have committed after the worker session was
			// already gone, and delivery is best effort.
			notifications, err = s.activeAttempts(ctx, tx, e.ID, cancelReason(reason), e.CancelEpoch)
			if err != nil {
				return err
			}
			if err := s.evaluateCompletion(ctx, tx, e); err != nil {
				return err
			}
			outcome.State = e.State
			outcome.StateVersion = e.StateVersion
			return nil
		}
		if terminalState(e.State) || e.State == "failing" {
			return nil
		}
		outcome.Accepted = true
		outcome.CancelEpoch = e.CancelEpoch + 1
		notifications, err = s.cancelExecutionTx(ctx, tx, e, "user", requestedBy, cancelReason(reason))
		if err != nil {
			return err
		}
		outcome.State = e.State
		outcome.StateVersion = e.StateVersion
		return nil
	})
	if err != nil {
		return CancelOutcome{}, err
	}
	s.notifyCancellations(notifications)
	return outcome, nil
}

func cancelReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "cancel_requested"
	}
	return reason
}

// EnforceDeadlinesOnce moves executions past their deadline into cancel_requested. The worker context deadline
// usually fires first; this pass makes the decision durable when the worker is gone.
func (s *Service) EnforceDeadlinesOnce(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id FROM execution.executions
		WHERE deadline_at IS NOT NULL AND deadline_at <= now()
		  AND state IN ('pending', 'running')
		ORDER BY deadline_at
		LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type dueExecution struct{ id, orgID string }
	var due []dueExecution
	for rows.Next() {
		var item dueExecution
		if err := rows.Scan(&item.id, &item.orgID); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	enforced := 0
	for _, item := range due {
		var notifications []CancelNotification
		err := s.withTx(ctx, func(tx pgx.Tx) error {
			e, err := s.lockExecution(ctx, tx, item.id)
			if err != nil {
				if isNoRows(err) {
					return nil
				}
				return err
			}
			if e.State != "pending" && e.State != "running" {
				return nil
			}
			notifications, err = s.cancelExecutionTx(ctx, tx, e, "deadline", "deadline", "execution_deadline")
			return err
		})
		if err != nil {
			s.log.Error("deadline enforcement failed", zap.String("execution_id", item.id), zap.Error(err))
			continue
		}
		s.notifyCancellations(notifications)
		enforced++
	}
	return enforced, nil
}

// cancelExecutionTx is shared by user cancellation and deadline enforcement. It returns the workers that must be
// notified after the transaction commits.
func (s *Service) cancelExecutionTx(ctx context.Context, tx pgx.Tx, e *execution,
	cancelReason, requestedBy, reason string) ([]CancelNotification, error) {
	from := e.State
	e.State = "cancel_requested"
	e.StateVersion++
	e.CancelEpoch++
	e.CancelReason = &cancelReason
	now := time.Now().UTC()
	e.CancelRequestedAt = &now
	e.CancelRequestedBy = &requestedBy

	if _, err := tx.Exec(ctx, `
		UPDATE execution.executions
		SET state = 'cancel_requested', state_version = $2, cancel_epoch = $3,
		    cancel_reason = $4, cancel_requested_at = now(), cancel_requested_by = $5,
		    ended_at = NULL
		WHERE id = $1`, e.ID, e.StateVersion, e.CancelEpoch, cancelReason, requestedBy); err != nil {
		return nil, err
	}

	notifications, err := s.activeAttempts(ctx, tx, e.ID, reason, e.CancelEpoch)
	if err != nil {
		return nil, err
	}
	if err := s.cancelPendingTasks(ctx, tx, e); err != nil {
		return nil, err
	}
	if err := s.cancelChildExecutions(ctx, tx, e, cancelReason, requestedBy, reason, 0); err != nil {
		return nil, err
	}

	counts, err := s.counts(ctx, tx, e.ID)
	if err != nil {
		return nil, err
	}
	reasonPtr := reason
	subject := executionSubject(e, "state_changed")
	if err := s.appendEvent(ctx, tx, e, eventSpec{
		MessageType: contracts.EventExecutionStateChanged,
		EntityType:  "execution",
		EntityID:    e.ID,
		From:        &from,
		To:          &e.State,
		ActorType:   actorForCancel(cancelReason),
		ActorID:     requestedBy,
		Subject:     subject,
		Payload: func(seq int64) any {
			return contracts.ExecutionStateChangedPayload{
				Seq:          seq,
				From:         from,
				To:           "cancel_requested",
				StateVersion: e.StateVersion,
				Reason:       &reasonPtr,
				Counts:       counts,
			}
		},
	}); err != nil {
		return nil, err
	}

	// If there is no active work, cancellation is already complete.
	if counts.Pending+counts.Assigned+counts.Running == 0 {
		if err := s.evaluateCompletion(ctx, tx, e); err != nil {
			return nil, err
		}
	}
	return notifications, nil
}

// cancelChildExecutions propagates cancellation to child executions (subworkflow and loop bodies). The child's
// active attempts are fenced by their leases; pending child work is cancelled immediately.
func (s *Service) cancelChildExecutions(ctx context.Context, tx pgx.Tx, parent *execution,
	cancelReason, requestedBy, reason string, depth int) error {
	if depth > 8 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id FROM execution.executions
		WHERE parent_execution_id = $1 AND state IN ('pending', 'running', 'cancel_requested')
		ORDER BY created_at`, parent.ID)
	if err != nil {
		return err
	}
	var childIDs []string
	for rows.Next() {
		var childID string
		if err := rows.Scan(&childID); err != nil {
			rows.Close()
			return err
		}
		childIDs = append(childIDs, childID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, childID := range childIDs {
		child, err := s.lockExecution(ctx, tx, childID)
		if err != nil {
			return err
		}
		if terminalState(child.State) || child.State == "failing" {
			continue
		}
		from := child.State
		if child.State != "cancel_requested" {
			child.State = "cancel_requested"
			child.StateVersion++
			child.CancelEpoch++
			child.CancelReason = &cancelReason
			now := time.Now().UTC()
			child.CancelRequestedAt = &now
			child.CancelRequestedBy = &requestedBy
			if _, err := tx.Exec(ctx, `
				UPDATE execution.executions
				SET state = 'cancel_requested', state_version = $2, cancel_epoch = $3,
				    cancel_reason = $4, cancel_requested_at = now(), cancel_requested_by = $5,
				    ended_at = NULL
				WHERE id = $1`, child.ID, child.StateVersion, child.CancelEpoch, cancelReason, requestedBy); err != nil {
				return err
			}
			counts, err := s.counts(ctx, tx, child.ID)
			if err != nil {
				return err
			}
			reasonPtr := reason
			if err := s.appendEvent(ctx, tx, child, eventSpec{
				MessageType: contracts.EventExecutionStateChanged,
				EntityType:  "execution",
				EntityID:    child.ID,
				From:        &from,
				To:          &child.State,
				ActorType:   actorForCancel(cancelReason),
				ActorID:     requestedBy,
				Subject:     executionSubject(child, "state_changed"),
				Payload: func(seq int64) any {
					return contracts.ExecutionStateChangedPayload{
						Seq: seq, From: from, To: "cancel_requested",
						StateVersion: child.StateVersion, Reason: &reasonPtr, Counts: counts,
					}
				},
			}); err != nil {
				return err
			}
		}
		if err := s.cancelPendingTasks(ctx, tx, child); err != nil {
			return err
		}
		if err := s.cancelChildExecutions(ctx, tx, child, cancelReason, requestedBy, reason, depth+1); err != nil {
			return err
		}
		if err := s.evaluateCompletion(ctx, tx, child); err != nil {
			return err
		}
	}
	return nil
}

func actorForCancel(cancelReason string) string {
	if cancelReason == "deadline" {
		return "system"
	}
	return "user"
}

func (s *Service) activeAttempts(ctx context.Context, tx pgx.Tx, executionID, reason string,
	epoch int) ([]CancelNotification, error) {
	rows, err := tx.Query(ctx, `
		SELECT t.id, COALESCE(t.owner_worker_id, ''), a.id, a.lease_id
		FROM execution.execution_tasks t
		JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
		WHERE t.execution_id = $1 AND t.state IN ('assigned', 'running') AND a.outcome IS NULL`,
		executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notifications []CancelNotification
	for rows.Next() {
		var item CancelNotification
		if err := rows.Scan(&item.TaskID, &item.WorkerID, &item.AttemptID, &item.LeaseID); err != nil {
			return nil, err
		}
		item.Reason = reason
		item.Epoch = epoch
		notifications = append(notifications, item)
	}
	return notifications, rows.Err()
}

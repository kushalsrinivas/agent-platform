package lifecycle

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

// ReapOnce expires leases and retires silent workers. A lease whose dispatch was never published is closed as
// incomplete and does not consume the retry budget: it never reached a worker.
func (s *Service) ReapOnce(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE execution.workers
		SET status = 'offline', updated_at = now()
		WHERE status = 'active' AND last_heartbeat_at < now() - make_interval(secs => $1)`,
		int(HeartbeatTimeout/time.Second)); err != nil {
		return 0, err
	}
	// Reconcile reserved slots with actual active attempts. A completed transition releases a slot, but a
	// deleted execution or a crashed writer must not strand capacity forever.
	if _, err := s.pool.Exec(ctx, `
		WITH actual AS (
			SELECT w.id, count(t.id) AS slots
			FROM execution.workers w
			LEFT JOIN execution.execution_tasks t
			  ON t.owner_worker_id = w.id AND t.state IN ('assigned', 'running')
			GROUP BY w.id
		)
		UPDATE execution.workers w
		SET reserved_slots = actual.slots, updated_at = now()
		FROM actual
		WHERE w.id = actual.id AND w.reserved_slots <> actual.slots`); err != nil {
		return 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id
		FROM execution.execution_tasks
		WHERE state IN ('assigned', 'running') AND lease_expires_at IS NOT NULL
		  AND lease_expires_at < now()
		ORDER BY lease_expires_at
		LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			return 0, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	reaped := 0
	for _, taskID := range taskIDs {
		ok, err := s.reapTask(ctx, taskID)
		if err != nil {
			s.log.Error("lease reaper failed", zap.String("task_id", taskID), zap.Error(err))
			continue
		}
		if ok {
			reaped++
		}
	}
	return reaped, nil
}

func (s *Service) reapTask(ctx context.Context, taskID string) (bool, error) {
	reaped := false
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecutionForTask(ctx, tx, taskID)
		if err != nil {
			if isNoRows(err) {
				return nil
			}
			return err
		}
		if terminalState(e.State) {
			return nil
		}
		t, err := s.lockTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if (t.State != "assigned" && t.State != "running") || !t.LeaseExpired {
			return nil
		}

		var a *attempt
		if t.LatestAttemptID != nil {
			a, err = s.lockAttempt(ctx, tx, *t.LatestAttemptID)
			if err != nil && !isNoRows(err) {
				return err
			}
			if a != nil && a.Outcome != nil {
				return nil
			}
		}
		workerID := ""
		from := t.State
		if t.OwnerWorkerID != nil {
			workerID = *t.OwnerWorkerID
		}

		neverPublished := false
		if a != nil {
			var unpublished int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM execution.outbox
				WHERE task_id = $1 AND attempt_id = $2 AND message_type = 'task.dispatch'
				  AND published_at IS NULL`, t.ID, a.ID).Scan(&unpublished); err != nil {
				return err
			}
			neverPublished = unpublished > 0
		}

		outcome := "lease_expired"
		errorCode := "LEASE_EXPIRED"
		if neverPublished {
			outcome = "incomplete"
			errorCode = "DISPATCH_NOT_PUBLISHED"
		}
		if a != nil {
			effectUncertain := a.StartedAt != nil
			if _, err := tx.Exec(ctx, `
				UPDATE execution.task_attempts
				SET outcome = $2, finished_at = now(), outcome_unknown = true,
				    effect_uncertain = $3, error_code = $4,
				    error_message = CASE WHEN $2 = 'incomplete'
				        THEN 'dispatch was never published' ELSE 'lease expired' END
				WHERE id = $1`, a.ID, outcome, effectUncertain, errorCode); err != nil {
				return err
			}
		}
		if err := s.releaseWorkerSlot(ctx, tx, workerID); err != nil {
			return err
		}

		retry := (neverPublished || t.AttemptCount < t.MaxAttempts) &&
			e.State != "failing" && e.State != "cancel_requested"
		var nextAttempt *time.Time
		to := "failed"
		if retry {
			to = "pending"
			next := time.Now().UTC().Add(retryBackoff(t.AttemptCount))
			nextAttempt = &next
		} else if e.State == "cancel_requested" {
			to = "cancelled"
		}
		if neverPublished && retry {
			if _, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = 'pending', state_version = state_version + 1,
				    attempt_count = GREATEST(attempt_count - 1, 0),
				    owner_worker_id = NULL, lease_id = NULL, lease_expires_at = NULL,
				    next_attempt_at = $2, updated_at = now()
				WHERE id = $1`, t.ID, nextAttempt); err != nil {
				return err
			}
		} else if retry {
			if _, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = 'pending', state_version = state_version + 1,
				    owner_worker_id = NULL, lease_id = NULL, lease_expires_at = NULL,
				    next_attempt_at = $2, updated_at = now()
				WHERE id = $1`, t.ID, nextAttempt); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = $2, state_version = state_version + 1, ended_at = now(),
				    owner_worker_id = NULL, lease_id = NULL, lease_expires_at = NULL, updated_at = now()
				WHERE id = $1`, t.ID, to); err != nil {
				return err
			}
		}

		if a != nil {
			if err := s.appendAttemptFinished(ctx, tx, e, t, a, outcome, errorCode); err != nil {
				return err
			}
		}
		taskIDValue, nodeID, version := t.ID, t.NodeID, t.StateVersion+1
		var attemptPtr *string
		if a != nil {
			attemptPtr = &a.ID
		}
		var workerPtr *string
		if workerID != "" {
			workerPtr = &workerID
		}
		reason := errorCode
		if err := s.appendEvent(ctx, tx, e, eventSpec{
			MessageType: contracts.EventTaskStateChanged,
			EntityType:  "task",
			EntityID:    taskIDValue,
			From:        &from,
			To:          &to,
			ActorType:   "system",
			ActorID:     "reaper",
			Subject:     taskSubject(e, taskIDValue, "state_changed"),
			TaskID:      &taskIDValue,
			AttemptID:   attemptPtr,
			WorkerID:    workerPtr,
			Payload: func(seq int64) any {
				return contracts.TaskStateChangedPayload{
					Seq: seq, TaskID: taskIDValue, NodeID: nodeID,
					From: from, To: to, StateVersion: version,
					AttemptID: attemptPtr, Reason: &reason,
				}
			},
		}); err != nil {
			return err
		}

		if neverPublished && retry && a != nil {
			// The attempt was never visible to a worker; remove it so its attempt number can be reused.
			if _, err := tx.Exec(ctx, `DELETE FROM execution.task_attempts WHERE id = $1`, a.ID); err != nil {
				return err
			}
		}

		switch to {
		case "failed":
			if err := s.failFast(ctx, tx, e, errorCode); err != nil {
				return err
			}
		case "cancelled":
			if err := s.evaluateCompletion(ctx, tx, e); err != nil {
				return err
			}
		}
		reaped = true
		return nil
	})
	return reaped, err
}

// RunReaper expires leases and silent workers on a fixed cadence.
func (s *Service) RunReaper(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	s.log.Info("lease reaper started", zap.Duration("interval", interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		count, err := s.ReapOnce(ctx, 20)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("lease reaper pass failed", zap.Error(err))
		} else if count > 0 {
			s.log.Info("expired leases reaped", zap.Int("count", count))
		}
		select {
		case <-ctx.Done():
			s.log.Info("lease reaper stopped")
			return nil
		case <-ticker.C:
		}
	}
}

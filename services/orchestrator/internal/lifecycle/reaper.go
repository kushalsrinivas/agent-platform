package lifecycle

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/platform/postgres"
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
		SET status = 'unhealthy', status_changed_at = now(), updated_at = now()
		WHERE status IN ('starting', 'available', 'busy')
		  AND last_heartbeat_at < now() - make_interval(secs => $1)`,
		int(s.heartbeatTimeout/time.Second)); err != nil {
		return 0, err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE execution.workers
		SET status = 'terminated', status_changed_at = now(), updated_at = now()
		WHERE status IN ('starting', 'available', 'busy', 'unhealthy')
		  AND last_heartbeat_at < now() - make_interval(secs => $1)`,
		int(s.workerTerminateAfter/time.Second)); err != nil {
		return 0, err
	}
	// Reconcile reserved slots with actual active attempts. Lock each worker before counting so a concurrent
	// dispatch cannot have its fresh reservation overwritten by a stale count.

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
		ok, err := s.reapTask(ctx, taskID, false)
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

func (s *Service) reapTask(ctx context.Context, taskID string, force bool) (bool, error) {
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
		if force {
			s.log.Debug("forced reap candidate",
				zap.String("task_id", t.ID),
				zap.String("task_state", t.State),
				zap.String("execution_state", e.State),
				zap.Bool("lease_expired", t.LeaseExpired),
				zap.Any("latest_attempt_id", t.LatestAttemptID),
			)
		}
		if t.State != "assigned" && t.State != "running" {
			return nil
		}
		if !force && !t.LeaseExpired {
			return nil
		}

		var a *attempt
		if t.LatestAttemptID != nil {
			a, err = s.lockAttempt(ctx, tx, *t.LatestAttemptID)
			if err != nil && !isNoRows(err) {
				return err
			}
			if force {
				if a == nil {
					s.log.Debug("forced reap has no attempt row", zap.String("attempt_id", *t.LatestAttemptID))
				} else {
					s.log.Debug("forced reap attempt", zap.String("attempt_id", a.ID), zap.Any("outcome", a.Outcome))
				}
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

// ReconcileWorkerSlots repairs reserved-slot counters from actual active attempts. Run it at startup, before the
// dispatcher is accepting work; calling it concurrently with dispatch can overwrite a fresh reservation.
func (s *Service) ReconcileWorkerSlots(ctx context.Context) error {
	return postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM execution.workers FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		var workerIDs []string
		for rows.Next() {
			var workerID string
			if err := rows.Scan(&workerID); err != nil {
				rows.Close()
				return err
			}
			workerIDs = append(workerIDs, workerID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, workerID := range workerIDs {
			var status string
			var maxConcurrency, reported int
			if err := tx.QueryRow(ctx, `
				SELECT status, max_concurrency, reported_available_slots
				FROM execution.workers WHERE id = $1`, workerID).
				Scan(&status, &maxConcurrency, &reported); err != nil {
				return err
			}
			var actual int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM execution.execution_tasks
				WHERE owner_worker_id = $1 AND state IN ('assigned', 'running')`, workerID).
				Scan(&actual); err != nil {
				return err
			}
			newStatus := s.statusForWorker(actual, maxConcurrency, reported, status)
			if _, err := tx.Exec(ctx, `
				UPDATE execution.workers
				SET reserved_slots = $2,
				    status = $3,
				    status_changed_at = CASE WHEN status <> $3 THEN now() ELSE status_changed_at END,
				    updated_at = now()
				WHERE id = $1`, workerID, actual, newStatus); err != nil {
				return err
			}
		}
		return nil
	})
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

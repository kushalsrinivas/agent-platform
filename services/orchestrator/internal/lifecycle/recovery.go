package lifecycle

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// RecoveryReport is one recovery pass, exposed for logs and operator tests.
type RecoveryReport struct {
	Reaped             int
	DeadlinesEnforced  int
	ExecutionsRepaired int
	TasksRepaired      int
	OutboxPending      int64
}

// RecoverOnce performs the periodic reconciliation the orchestrator owns: expired leases, due deadlines,
// interrupted executions, orphaned active tasks and incomplete transitions. It is deliberately conservative:
// every repair goes through the same fenced state transitions as normal work.
func (s *Service) RecoverOnce(ctx context.Context, limit int) (RecoveryReport, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	report := RecoveryReport{}
	reaped, err := s.ReapOnce(ctx, limit)
	if err != nil {
		return report, err
	}
	report.Reaped = reaped

	enforced, err := s.EnforceDeadlinesOnce(ctx, limit)
	if err != nil {
		return report, err
	}
	report.DeadlinesEnforced = enforced

	orphaned, err := s.reapTerminatedWorkerTasks(ctx, limit)
	if err != nil {
		return report, err
	}
	report.Reaped += orphaned

	dangling, err := s.repairActiveTasksWithoutAttempt(ctx, limit)
	if err != nil {
		return report, err
	}
	report.TasksRepaired = dangling

	repaired, err := s.reconcileExecutions(ctx, limit)
	if err != nil {
		return report, err
	}
	report.ExecutionsRepaired = repaired

	stats, err := s.OutboxStats(ctx)
	if err != nil {
		return report, err
	}
	report.OutboxPending = stats.Pending
	return report, nil
}

// reapTerminatedWorkerTasks closes attempts whose worker is definitively gone instead of waiting for the lease
// clock. Fencing still protects against a worker that comes back: the old lease is invalid.
func (s *Service) reapTerminatedWorkerTasks(ctx context.Context, limit int) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id
		FROM execution.execution_tasks t
		JOIN execution.workers w ON w.id = t.owner_worker_id
		WHERE t.state IN ('assigned', 'running')
		  AND w.status = 'terminated'
		ORDER BY t.updated_at
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
		ok, err := s.reapTask(ctx, taskID, true)
		if err != nil {
			s.log.Warn("orphaned task reap failed", zap.String("task_id", taskID), zap.Error(err))
			continue
		}
		if ok {
			reaped++
		}
	}
	return reaped, nil
}

// repairActiveTasksWithoutAttempt handles an active task whose latest attempt row is missing. This can only be an
// interrupted multi-row transition; replaying the same lease decision is safe and never invents a success.
func (s *Service) repairActiveTasksWithoutAttempt(ctx context.Context, limit int) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id
		FROM execution.execution_tasks t
		LEFT JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
		WHERE t.state IN ('assigned', 'running')
		  AND (t.latest_attempt_id IS NULL OR a.id IS NULL)
		ORDER BY t.updated_at
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
	repaired := 0
	for _, taskID := range taskIDs {
		ok, err := s.reapTask(ctx, taskID, true)
		if err != nil {
			s.log.Warn("dangling active task repair failed", zap.String("task_id", taskID), zap.Error(err))
			continue
		}
		if ok {
			repaired++
		}
	}
	return repaired, nil
}

// reconcileExecutions repairs executions that have no active task but never reached a terminal state. This is
// what makes a restarted orchestrator converge instead of leaving a completed execution running forever.
func (s *Service) reconcileExecutions(ctx context.Context, limit int) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id
		FROM execution.executions e
		WHERE e.state IN ('pending', 'running', 'failing', 'cancel_requested')
		  AND NOT EXISTS (
		      SELECT 1 FROM execution.execution_tasks t
		      WHERE t.execution_id = e.id AND t.state IN ('pending', 'assigned', 'running')
		  )
		ORDER BY e.created_at
		LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	var executionIDs []string
	for rows.Next() {
		var executionID string
		if err := rows.Scan(&executionID); err != nil {
			rows.Close()
			return 0, err
		}
		executionIDs = append(executionIDs, executionID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	repaired := 0
	for _, executionID := range executionIDs {
		changed := false
		err := s.withTx(ctx, func(tx pgx.Tx) error {
			e, err := s.lockExecution(ctx, tx, executionID)
			if err != nil {
				if isNoRows(err) {
					return nil
				}
				return err
			}
			if terminalState(e.State) {
				return nil
			}
			var active int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM execution.execution_tasks
				WHERE execution_id = $1 AND state IN ('pending', 'assigned', 'running')`,
				executionID).Scan(&active); err != nil {
				return err
			}
			if active > 0 {
				return nil
			}
			changed = true
			return s.evaluateCompletion(ctx, tx, e)
		})
		if err != nil {
			s.log.Warn("execution reconciliation failed", zap.String("execution_id", executionID), zap.Error(err))
			continue
		}
		if changed {
			repaired++
		}
	}
	return repaired, nil
}

// RunRecovery runs the recovery pass on a fixed cadence.
func (s *Service) RunRecovery(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	s.log.Info("recovery manager started", zap.Duration("interval", interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		report, err := s.RecoverOnce(ctx, 20)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("recovery pass failed", zap.Error(err))
		} else if report.Reaped > 0 || report.DeadlinesEnforced > 0 ||
			report.ExecutionsRepaired > 0 || report.TasksRepaired > 0 {
			s.log.Info("recovery pass repaired state",
				zap.Int("leases_reaped", report.Reaped),
				zap.Int("deadlines_enforced", report.DeadlinesEnforced),
				zap.Int("executions_repaired", report.ExecutionsRepaired),
				zap.Int("tasks_repaired", report.TasksRepaired),
				zap.Int64("outbox_pending", report.OutboxPending),
			)
		}
		select {
		case <-ctx.Done():
			s.log.Info("recovery manager stopped")
			return nil
		case <-ticker.C:
		}
	}
}

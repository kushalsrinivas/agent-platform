package lifecycle

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/outbox"
	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

type dispatchCandidate struct {
	TaskID      string
	ExecutionID string
}

// DispatchOnce assigns up to limit eligible pending tasks, one transaction per assignment. It is safe to run
// concurrently with other dispatchers, result handlers and the reaper: the execution row is locked first and the
// task is re-checked under that lock.
func (s *Service) DispatchOnce(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, execution_id
		FROM (
			SELECT t.id, t.execution_id, e.org_id,
			       t.priority, t.created_at,
			       row_number() OVER (
			           PARTITION BY e.org_id
			           ORDER BY t.priority DESC, t.created_at, t.id
			       ) AS tenant_rank,
			       t.priority + LEAST(FLOOR(EXTRACT(EPOCH FROM (now() - t.created_at)) / 60)::integer, 200)
			           AS effective_priority
			FROM execution.execution_tasks t
			JOIN execution.executions e ON e.id = t.execution_id
			WHERE t.state = 'pending'
			  AND e.state IN ('pending', 'running')
			  AND (t.next_attempt_at IS NULL OR t.next_attempt_at <= now())
			  AND t.attempt_count < t.max_attempts
			  AND NOT EXISTS (
			      SELECT 1
			      FROM execution.task_dependencies d
			      JOIN execution.execution_tasks upstream
			        ON upstream.id = d.depends_on_task_id AND upstream.execution_id = d.execution_id
			      WHERE d.task_id = t.id AND d.execution_id = t.execution_id
			        AND upstream.state NOT IN ('succeeded', 'failed', 'cancelled', 'skipped')
			  )
		) ranked
		ORDER BY tenant_rank, effective_priority DESC, created_at, id
		LIMIT $1`, limit)
	if err != nil {
		return 0, fmt.Errorf("lifecycle: scan dispatch candidates: %w", err)
	}
	var candidates []dispatchCandidate
	for rows.Next() {
		var candidate dispatchCandidate
		if err := rows.Scan(&candidate.TaskID, &candidate.ExecutionID); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, candidate)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	dispatched := 0
	for _, candidate := range candidates {
		s.log.Debug("dispatch candidate considered",
			zap.String("task_id", candidate.TaskID),
			zap.String("execution_id", candidate.ExecutionID),
		)
		assigned, err := s.dispatchOne(ctx, candidate.TaskID)
		if err != nil {
			s.log.Error("dispatch candidate failed",
				zap.String("task_id", candidate.TaskID),
				zap.String("execution_id", candidate.ExecutionID),
				zap.Error(err),
			)
			continue
		}
		if assigned {
			dispatched++
		}
	}
	return dispatched, nil
}

func (s *Service) dispatchOne(ctx context.Context, taskID string) (bool, error) {
	assigned := false
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecutionForTask(ctx, tx, taskID)
		if err != nil {
			if isNoRows(err) {
				return nil
			}
			return err
		}
		if e.State != "pending" && e.State != "running" {
			s.log.Debug("dispatch skipped: execution not schedulable",
				zap.String("task_id", taskID), zap.String("execution_state", e.State))
			return nil
		}
		t, err := s.lockTask(ctx, tx, taskID)
		if err != nil {
			if isNoRows(err) {
				return nil
			}
			return err
		}
		if t.State != "pending" {
			s.log.Debug("dispatch skipped: task not pending",
				zap.String("task_id", taskID), zap.String("task_state", t.State))
			return nil
		}

		deps, err := s.dependencies(ctx, tx, t)
		if err != nil {
			return err
		}
		if !deps.terminal {
			s.log.Debug("dispatch skipped: dependencies not terminal", zap.String("task_id", taskID))
			return nil
		}
		if !deps.anySucceeded {
			return s.skipTask(ctx, tx, e, t, deps.reason)
		}
		allowed, err := s.limitsAllow(ctx, tx, e, t)
		if err != nil {
			return err
		}
		if !allowed {
			s.log.Debug("dispatch skipped: admission limit", zap.String("task_id", taskID), zap.Int("priority", t.Priority))
			return nil
		}

		workerID, err := s.pickWorker(ctx, tx, t)
		if err != nil {
			return err
		}
		if workerID == "" {
			s.log.Debug("no worker with a free slot", zap.String("task_id", t.ID))
			return nil
		}

		specDigest, err := s.specDigest(ctx, tx, t)
		if err != nil {
			return err
		}
		deadline := time.Now().UTC().Add(10 * time.Minute)
		if e.DeadlineAt != nil {
			deadline = *e.DeadlineAt
		}
		var leaseExpires time.Time
		if err := tx.QueryRow(ctx, `SELECT now() + make_interval(secs => $1)`,
			int(s.leaseTTL/time.Second)).Scan(&leaseExpires); err != nil {
			return err
		}
		attemptID := idgen.New("att")
		leaseID := uuid.NewString()
		attemptNumber := t.AttemptCount + 1

		if _, err := tx.Exec(ctx, `
			INSERT INTO execution.task_attempts
				(id, task_id, execution_id, org_id, attempt_number, worker_id, lease_id, dispatched_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7::uuid, now())`,
			attemptID, t.ID, e.ID, e.OrgID, attemptNumber, workerID, leaseID); err != nil {
			return fmt.Errorf("lifecycle: insert attempt: %w", err)
		}

		tag, err := tx.Exec(ctx, `
			UPDATE execution.execution_tasks
			SET state = 'assigned', state_version = state_version + 1, attempt_count = $2,
			    owner_worker_id = $3, lease_id = $4::uuid, lease_expires_at = $5,
			    latest_attempt_id = $6, next_attempt_at = NULL, updated_at = now()
			WHERE id = $1 AND state = 'pending'`,
			t.ID, attemptNumber, workerID, leaseID, leaseExpires, attemptID)
		if err != nil {
			return fmt.Errorf("lifecycle: assign task: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return nil
		}

		tag, err = tx.Exec(ctx, `
			UPDATE execution.workers
			SET reserved_slots = reserved_slots + 1,
			    status = CASE WHEN reserved_slots + 1 >= LEAST(max_concurrency, reported_available_slots)
			        THEN 'busy' ELSE 'available' END,
			    status_changed_at = CASE WHEN status <> (
			        CASE WHEN reserved_slots + 1 >= LEAST(max_concurrency, reported_available_slots)
			            THEN 'busy' ELSE 'available' END) THEN now() ELSE status_changed_at END,
			    updated_at = now()
			WHERE id = $1
			  AND status IN ('available', 'busy')
			  AND reserved_slots < LEAST(max_concurrency, reported_available_slots)`, workerID)
		if err != nil {
			return fmt.Errorf("lifecycle: reserve worker slot: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("lifecycle: worker %s lost capacity during assignment", workerID)
		}

		from, to := "pending", "assigned"
		taskIDValue, nodeID, attemptIDValue, version := t.ID, t.NodeID, attemptID, t.StateVersion+1
		if err := s.appendEvent(ctx, tx, e, eventSpec{
			MessageType: contracts.EventTaskStateChanged,
			EntityType:  "task",
			EntityID:    taskIDValue,
			From:        &from,
			To:          &to,
			ActorType:   "system",
			ActorID:     "orchestrator",
			Subject:     taskSubject(e, taskIDValue, "state_changed"),
			TaskID:      &taskIDValue,
			AttemptID:   &attemptIDValue,
			WorkerID:    &workerID,
			Payload: func(seq int64) any {
				return contracts.TaskStateChangedPayload{
					Seq: seq, TaskID: taskIDValue, NodeID: nodeID,
					From: from, To: to, StateVersion: version, AttemptID: &attemptIDValue,
				}
			},
		}); err != nil {
			return err
		}

		payload := contracts.TaskDispatchPayload{
			AttemptNumber:  attemptNumber,
			LeaseID:        leaseID,
			NodeID:         t.NodeID,
			NodeType:       contracts.WorkflowNodeType(t.NodeType),
			AgentVersionID: t.AgentVersionID,
			ToolVersionID:  t.ToolVersionID,
			SpecDigest:     specDigest,
			Deadline:       deadline.UTC().Format(time.RFC3339Nano),
			LeaseExpiresAt: leaseExpires.UTC().Format(time.RFC3339Nano),
		}
		message, err := outbox.NewMessage(e.OrgID, e.ProjectID, "tasks.dispatch."+workerID,
			contracts.EventTaskDispatch, &e.ID, payload, nil)
		if err != nil {
			return err
		}
		message.TaskID = &taskIDValue
		message.AttemptID = &attemptIDValue
		message.WorkerID = &workerID
		message.CorrelationID = &e.ID
		if err := s.store.Insert(ctx, tx, message); err != nil {
			return err
		}

		assigned = true
		s.log.Debug("task dispatched",
			zap.String("task_id", t.ID),
			zap.String("worker_id", workerID),
			zap.String("attempt_id", attemptID),
			zap.Int("priority", t.Priority),
		)
		return nil
	})
	return assigned, err
}

type dependencyState struct {
	terminal     bool
	anySucceeded bool
	reason       string
}

func (s *Service) dependencies(ctx context.Context, tx pgx.Tx, t *task) (dependencyState, error) {
	rows, err := tx.Query(ctx, `
		SELECT upstream.state
		FROM execution.task_dependencies d
		JOIN execution.execution_tasks upstream
		  ON upstream.id = d.depends_on_task_id AND upstream.execution_id = d.execution_id
		WHERE d.task_id = $1 AND d.execution_id = $2`, t.ID, t.ExecutionID)
	if err != nil {
		return dependencyState{}, err
	}
	defer rows.Close()

	result := dependencyState{terminal: true}
	sawAny := false
	allUnsuccessfulSkipped := true
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			return dependencyState{}, err
		}
		sawAny = true
		switch state {
		case "succeeded":
			result.anySucceeded = true
		case "skipped":
			// Terminal but unavailable; an all-skipped branch is not a dependency failure.
		case "failed", "cancelled":
			allUnsuccessfulSkipped = false
		case "pending", "assigned", "running":
			result.terminal = false
			allUnsuccessfulSkipped = false
		}
	}
	if err := rows.Err(); err != nil {
		return dependencyState{}, err
	}
	if !sawAny {
		// Roots always activate.
		return dependencyState{terminal: true, anySucceeded: true}, nil
	}
	if result.anySucceeded {
		return result, nil
	}
	if allUnsuccessfulSkipped {
		result.reason = "upstream_not_taken"
	} else {
		result.reason = "dependency_failed"
	}
	return result, nil
}

func (s *Service) specDigest(ctx context.Context, tx pgx.Tx, t *task) (string, error) {
	if t.AgentVersionID != nil {
		var digest string
		err := tx.QueryRow(ctx, `
			SELECT digest FROM control.agent_versions
			WHERE id = $1 AND org_id = $2`,
			*t.AgentVersionID, t.OrgID).Scan(&digest)
		if err != nil {
			return "", fmt.Errorf("lifecycle: load pinned agent version %s: %w", *t.AgentVersionID, err)
		}
		return digest, nil
	}
	if t.ToolVersionID != nil {
		return contracts.SHA256Digest([]byte(*t.ToolVersionID)), nil
	}
	return "", fmt.Errorf("lifecycle: task %s has no pinned version", t.ID)
}

func (s *Service) limitsAllow(ctx context.Context, tx pgx.Tx, e *execution, t *task) (bool, error) {
	if s.maxActiveTasksPerTenant > 0 {
		var active int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM execution.execution_tasks
			WHERE org_id = $1 AND state IN ('assigned', 'running')`, e.OrgID).Scan(&active); err != nil {
			return false, err
		}
		if active >= s.maxActiveTasksPerTenant {
			return false, nil
		}
	}
	if t.Provider != nil {
		if limit := s.providerConcurrency[*t.Provider]; limit > 0 {
			var active int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM execution.execution_tasks
				WHERE provider = $1 AND state IN ('assigned', 'running')`, *t.Provider).Scan(&active); err != nil {
				return false, err
			}
			if active >= limit {
				return false, nil
			}
		}
	}
	if t.ToolVersionID != nil {
		if limit := s.toolConcurrency[*t.ToolVersionID]; limit > 0 {
			var active int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM execution.execution_tasks
				WHERE tool_version_id = $1 AND state IN ('assigned', 'running')`, *t.ToolVersionID).Scan(&active); err != nil {
				return false, err
			}
			if active >= limit {
				return false, nil
			}
		}
	}
	return true, nil
}

func requiredCapability(nodeType string) string {
	if nodeType == "tool" {
		return "tool_execution"
	}
	return "agent_execution"
}

func (s *Service) pickWorker(ctx context.Context, tx pgx.Tx, t *task) (string, error) {
	pool := ""
	if t.WorkerPool != nil {
		pool = *t.WorkerPool
	}
	var runtimeABI any
	if t.RuntimeABI != nil {
		runtimeABI = *t.RuntimeABI
	}
	var runtimeDigest any
	if t.RuntimeDigest != nil {
		runtimeDigest = *t.RuntimeDigest
	}
	var workerID string
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM execution.workers
		WHERE status IN ('available', 'busy')
		  AND last_heartbeat_at > now() - make_interval(secs => $2)
		  AND reserved_slots < LEAST(max_concurrency, reported_available_slots)
		  AND ($1 = '' OR pool = $1)
		  AND $3 = ANY(capabilities)
		  AND ($4::integer IS NULL OR $4 = ANY(runtime_abis))
		  AND (cardinality(runtime_digests) = 0 OR $5::text IS NULL OR $5 = ANY(runtime_digests))
		ORDER BY reserved_slots ASC, cpu_usage_percent ASC NULLS LAST, last_heartbeat_at ASC, id ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED`,
		pool, int(s.heartbeatTimeout/time.Second), requiredCapability(t.NodeType), runtimeABI, runtimeDigest).
		Scan(&workerID)
	if isNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lifecycle: select worker: %w", err)
	}
	return workerID, nil
}

func (s *Service) skipTask(ctx context.Context, tx pgx.Tx, e *execution, t *task, reason string) error {
	if reason == "" {
		reason = "dependency_failed"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE execution.execution_tasks
		SET state = 'skipped', state_version = state_version + 1, skip_reason = $2,
		    ended_at = now(), owner_worker_id = NULL, lease_id = NULL,
		    lease_expires_at = NULL, updated_at = now()
		WHERE id = $1 AND state = 'pending'`, t.ID, reason); err != nil {
		return fmt.Errorf("lifecycle: skip task: %w", err)
	}
	from, to := "pending", "skipped"
	taskID, nodeID, version, skipReason := t.ID, t.NodeID, t.StateVersion+1, reason
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
				From: from, To: to, StateVersion: version, SkipReason: &skipReason,
			}
		},
	}); err != nil {
		return err
	}
	return s.evaluateCompletion(ctx, tx, e)
}

// RunDispatcher schedules eligible tasks until the context is cancelled.
func (s *Service) RunDispatcher(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	s.log.Info("scheduler started", zap.Duration("interval", interval), zap.Duration("lease_ttl", s.leaseTTL))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		count, err := s.DispatchOnce(ctx, 20)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("scheduler pass failed", zap.Error(err))
		} else if count > 0 {
			s.log.Debug("tasks dispatched", zap.Int("count", count))
		}
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopped")
			return nil
		case <-ticker.C:
		}
	}
}

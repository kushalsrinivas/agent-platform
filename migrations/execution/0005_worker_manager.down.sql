DROP INDEX IF EXISTS execution.execution_tasks_tenant_active_idx;
DROP INDEX IF EXISTS execution.execution_tasks_tool_active_idx;
DROP INDEX IF EXISTS execution.execution_tasks_provider_active_idx;
DROP INDEX IF EXISTS execution.execution_tasks_schedule_idx;

ALTER TABLE execution.execution_tasks
    DROP COLUMN IF EXISTS model,
    DROP COLUMN IF EXISTS provider,
    DROP COLUMN IF EXISTS runtime_digest,
    DROP COLUMN IF EXISTS runtime_abi,
    DROP COLUMN IF EXISTS priority;

ALTER TABLE execution.executions DROP COLUMN IF EXISTS priority;

DROP INDEX IF EXISTS execution.workers_schedulable_idx;
CREATE INDEX workers_schedulable_idx
    ON execution.workers (pool, status, last_heartbeat_at)
    WHERE status = 'active';

ALTER TABLE execution.workers DROP CONSTRAINT workers_status_check;
UPDATE execution.workers
SET status = CASE
    WHEN status IN ('starting', 'available', 'busy') THEN 'active'
    WHEN status = 'terminated' THEN 'offline'
    ELSE status
END;
ALTER TABLE execution.workers ADD CONSTRAINT workers_status_check CHECK (
    status IN ('active', 'unhealthy', 'draining', 'offline')
);
ALTER TABLE execution.workers ALTER COLUMN status SET DEFAULT 'active';

ALTER TABLE execution.workers
    DROP COLUMN IF EXISTS status_changed_at,
    DROP COLUMN IF EXISTS memory_used_mb,
    DROP COLUMN IF EXISTS cpu_usage_percent,
    DROP COLUMN IF EXISTS memory_mb,
    DROP COLUMN IF EXISTS cpu_cores,
    DROP COLUMN IF EXISTS capabilities,
    DROP COLUMN IF EXISTS runtime_version,
    DROP COLUMN IF EXISTS runtime;

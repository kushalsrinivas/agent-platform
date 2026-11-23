DROP POLICY IF EXISTS tenant_isolation ON execution.outbox;
DROP POLICY IF EXISTS tenant_isolation ON execution.execution_events;
DROP POLICY IF EXISTS tenant_isolation ON execution.checkpoints;
DROP POLICY IF EXISTS tenant_isolation ON execution.task_attempts;
DROP POLICY IF EXISTS tenant_isolation ON execution.task_dependencies;
DROP POLICY IF EXISTS tenant_isolation ON execution.execution_tasks;
DROP POLICY IF EXISTS tenant_isolation ON execution.executions;

DROP TRIGGER IF EXISTS task_attempts_immutable ON execution.task_attempts;
DROP FUNCTION IF EXISTS execution.enforce_attempt_immutability();
DROP TRIGGER IF EXISTS execution_events_append_only ON execution.execution_events;
DROP FUNCTION IF EXISTS execution.enforce_event_append_only();
DROP TRIGGER IF EXISTS workers_touch_updated_at ON execution.workers;
DROP TRIGGER IF EXISTS execution_tasks_touch_updated_at ON execution.execution_tasks;
DROP FUNCTION IF EXISTS execution.touch_updated_at();

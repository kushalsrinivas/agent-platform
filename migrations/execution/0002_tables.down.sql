ALTER TABLE execution.execution_tasks DROP CONSTRAINT IF EXISTS execution_tasks_latest_attempt_fk;
ALTER TABLE execution.task_attempts DROP CONSTRAINT IF EXISTS task_attempts_checkpoint_fk;

DROP TABLE IF EXISTS execution.outbox;
DROP TABLE IF EXISTS execution.execution_events;
DROP TABLE IF EXISTS execution.checkpoints;
DROP TABLE IF EXISTS execution.task_attempts;
DROP TABLE IF EXISTS execution.task_dependencies;
DROP TABLE IF EXISTS execution.execution_tasks;
DROP TABLE IF EXISTS execution.workers;
DROP TABLE IF EXISTS execution.executions;

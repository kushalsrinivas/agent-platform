ALTER TABLE execution.outbox
    DROP COLUMN IF EXISTS traceparent,
    DROP COLUMN IF EXISTS causation_id,
    DROP COLUMN IF EXISTS correlation_id,
    DROP COLUMN IF EXISTS worker_id,
    DROP COLUMN IF EXISTS attempt_id,
    DROP COLUMN IF EXISTS task_id,
    DROP COLUMN IF EXISTS project_id;

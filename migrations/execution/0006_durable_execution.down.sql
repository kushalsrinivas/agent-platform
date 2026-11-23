-- Reverse Phase 08 durable execution schema changes. Development only: restoring UNIQUE(attempt_id) fails if an
-- attempt already has several checkpoints, which is expected once the system has run.

DROP INDEX IF EXISTS execution.execution_tasks_dead_letter_idx;
DROP INDEX IF EXISTS execution.checkpoints_task_latest_idx;
DROP INDEX IF EXISTS execution.checkpoints_attempt_sequence_idx;

ALTER TABLE execution.executions
    DROP COLUMN IF EXISTS cancel_requested_by,
    DROP COLUMN IF EXISTS cancel_requested_at;

ALTER TABLE execution.execution_tasks
    DROP COLUMN IF EXISTS dead_lettered_at,
    DROP COLUMN IF EXISTS last_failure_code,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS retry_policy;

ALTER TABLE execution.checkpoints
    DROP CONSTRAINT IF EXISTS checkpoints_parent_fk;
ALTER TABLE execution.checkpoints
    DROP CONSTRAINT IF EXISTS checkpoints_object_key_length_check;
ALTER TABLE execution.checkpoints
    DROP CONSTRAINT IF EXISTS checkpoints_inline_size_check;
ALTER TABLE execution.checkpoints
    DROP CONSTRAINT IF EXISTS checkpoints_payload_check;
ALTER TABLE execution.checkpoints
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS state_inline,
    DROP COLUMN IF EXISTS sequence,
    DROP COLUMN IF EXISTS parent_checkpoint_id,
    DROP COLUMN IF EXISTS workflow_version_id,
    DROP COLUMN IF EXISTS node_id;

ALTER TABLE execution.checkpoints
    ALTER COLUMN object_key SET NOT NULL;
ALTER TABLE execution.checkpoints
    ADD CONSTRAINT checkpoints_attempt_id_key UNIQUE (attempt_id);

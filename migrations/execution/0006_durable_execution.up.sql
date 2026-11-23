-- Phase 08 durable execution: checkpoint lineage, versioned state, retry policy, dead letters and durable
-- cancellation audit. Checkpoints are written by the orchestrator only; Python never touches this schema.

-- ---------------------------------------------------------------------------
-- Checkpoints: an attempt may now produce several checkpoints, and a small state can live inline while a large
-- state is offloaded to the artifact store. The object key is assigned by the orchestrator.
-- ---------------------------------------------------------------------------

ALTER TABLE execution.checkpoints
    DROP CONSTRAINT IF EXISTS checkpoints_attempt_id_key;

ALTER TABLE execution.checkpoints
    ADD COLUMN node_id text NOT NULL DEFAULT 'legacy' CHECK (node_id ~ '^[a-z][a-z0-9_]{0,63}$'),
    ADD COLUMN workflow_version_id text NOT NULL DEFAULT 'legacy',
    ADD COLUMN parent_checkpoint_id text,
    ADD COLUMN sequence integer NOT NULL DEFAULT 1 CHECK (sequence >= 1),
    ADD COLUMN state_inline bytea,
    ADD COLUMN idempotency_key text CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 500);

ALTER TABLE execution.checkpoints
    ALTER COLUMN object_key DROP NOT NULL;

ALTER TABLE execution.checkpoints
    ADD CONSTRAINT checkpoints_payload_check CHECK (
        (object_key IS NOT NULL AND state_inline IS NULL)
        OR (object_key IS NULL AND state_inline IS NOT NULL)
    ),
    ADD CONSTRAINT checkpoints_inline_size_check CHECK (
        state_inline IS NULL OR octet_length(state_inline) BETWEEN 1 AND 33554432
    ),
    ADD CONSTRAINT checkpoints_object_key_length_check CHECK (
        object_key IS NULL OR length(object_key) BETWEEN 1 AND 1024
    ),
    ADD CONSTRAINT checkpoints_parent_fk
        FOREIGN KEY (parent_checkpoint_id) REFERENCES execution.checkpoints (id) ON DELETE SET NULL;

CREATE UNIQUE INDEX checkpoints_attempt_sequence_idx
    ON execution.checkpoints (attempt_id, sequence);
CREATE INDEX checkpoints_task_latest_idx
    ON execution.checkpoints (task_id, created_at DESC, sequence DESC);

-- ---------------------------------------------------------------------------
-- Retry engine configuration. The effective node policy (node override or workflow default) is frozen on the
-- task at materialisation so a retry never depends on a later workflow edit.
-- ---------------------------------------------------------------------------

ALTER TABLE execution.execution_tasks
    ADD COLUMN retry_policy jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(retry_policy) = 'object' AND octet_length(retry_policy::text) <= 4096
    ),
    ADD COLUMN idempotency_key text CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 500),
    ADD COLUMN last_failure_code text CHECK (last_failure_code IS NULL OR length(last_failure_code) BETWEEN 1 AND 128),
    ADD COLUMN dead_lettered_at timestamptz;

CREATE INDEX execution_tasks_dead_letter_idx
    ON execution.execution_tasks (dead_lettered_at)
    WHERE dead_lettered_at IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Durable cancellation audit. The state machine already carried cancel_epoch; record who requested it and when
-- so an orchestrator restart cannot lose the decision.
-- ---------------------------------------------------------------------------

ALTER TABLE execution.executions
    ADD COLUMN cancel_requested_at timestamptz,
    ADD COLUMN cancel_requested_by text CHECK (
        cancel_requested_by IS NULL OR length(cancel_requested_by) BETWEEN 1 AND 255
    );

UPDATE execution.executions
SET cancel_requested_at = COALESCE(ended_at, now()),
    cancel_requested_by = 'legacy'
WHERE state = 'cancel_requested' AND cancel_requested_at IS NULL;

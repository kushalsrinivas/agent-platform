-- Outbox rows now carry the envelope context the publisher needs. The payload column stays the type-specific
-- payload; the publisher wraps it in the versioned envelope before handing it to NATS.

ALTER TABLE execution.outbox
    ADD COLUMN project_id text,
    ADD COLUMN task_id text,
    ADD COLUMN attempt_id text,
    ADD COLUMN worker_id text,
    ADD COLUMN correlation_id text,
    ADD COLUMN causation_id text,
    ADD COLUMN traceparent text;

UPDATE execution.outbox o
SET project_id = e.project_id
FROM execution.executions e
WHERE o.execution_id = e.id
  AND o.project_id IS NULL;

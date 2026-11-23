-- Phase 06 worker manager: worker capabilities/resources, scheduling priority and pinned runtime metadata.

ALTER TABLE execution.workers
    ADD COLUMN runtime             text NOT NULL DEFAULT 'python' CHECK (length(runtime) BETWEEN 1 AND 64),
    ADD COLUMN runtime_version     text NOT NULL DEFAULT 'unknown' CHECK (length(runtime_version) BETWEEN 1 AND 128),
    ADD COLUMN capabilities        text[] NOT NULL DEFAULT '{}'::text[] CHECK (array_position(capabilities, NULL) IS NULL),
    ADD COLUMN cpu_cores           double precision NOT NULL DEFAULT 1 CHECK (cpu_cores > 0 AND cpu_cores <= 1024),
    ADD COLUMN memory_mb           bigint NOT NULL DEFAULT 1024 CHECK (memory_mb > 0),
    ADD COLUMN cpu_usage_percent   double precision CHECK (
        cpu_usage_percent IS NULL OR (cpu_usage_percent >= 0 AND cpu_usage_percent <= 100)
    ),
    ADD COLUMN memory_used_mb      bigint CHECK (memory_used_mb IS NULL OR memory_used_mb >= 0),
    ADD COLUMN status_changed_at   timestamptz NOT NULL DEFAULT now();

ALTER TABLE execution.workers DROP CONSTRAINT workers_status_check;
UPDATE execution.workers
SET status = CASE status WHEN 'active' THEN 'available' WHEN 'offline' THEN 'terminated' ELSE status END;
ALTER TABLE execution.workers ADD CONSTRAINT workers_status_check CHECK (
    status IN ('starting', 'available', 'busy', 'draining', 'unhealthy', 'terminated')
);
ALTER TABLE execution.workers ALTER COLUMN status SET DEFAULT 'available';

DROP INDEX execution.workers_schedulable_idx;
CREATE INDEX workers_schedulable_idx
    ON execution.workers (pool, status, last_heartbeat_at)
    WHERE status IN ('available', 'busy');

ALTER TABLE execution.executions
    ADD COLUMN priority integer NOT NULL DEFAULT 0 CHECK (priority BETWEEN -100 AND 100);

ALTER TABLE execution.execution_tasks
    ADD COLUMN priority       integer NOT NULL DEFAULT 0 CHECK (priority BETWEEN -100 AND 100),
    ADD COLUMN runtime_abi    integer CHECK (runtime_abi IS NULL OR runtime_abi >= 1),
    ADD COLUMN runtime_digest text CHECK (runtime_digest IS NULL OR runtime_digest ~ '^sha256:[0-9a-f]{64}$'),
    ADD COLUMN provider       text CHECK (provider IS NULL OR length(provider) BETWEEN 1 AND 64),
    ADD COLUMN model          text CHECK (model IS NULL OR length(model) BETWEEN 1 AND 128);

UPDATE execution.execution_tasks t
SET runtime_abi = av.runtime_abi,
    runtime_digest = av.spec->'runtime'->>'image_digest',
    provider = av.spec->'model'->>'provider',
    model = av.spec->'model'->>'name'
FROM control.agent_versions av
WHERE t.agent_version_id = av.id AND t.node_type = 'agent';

UPDATE execution.execution_tasks t
SET priority = e.priority
FROM execution.executions e
WHERE t.execution_id = e.id;

CREATE INDEX execution_tasks_schedule_idx
    ON execution.execution_tasks (state, priority DESC, created_at)
    WHERE state = 'pending';
CREATE INDEX execution_tasks_provider_active_idx
    ON execution.execution_tasks (provider)
    WHERE state IN ('assigned', 'running') AND provider IS NOT NULL;
CREATE INDEX execution_tasks_tool_active_idx
    ON execution.execution_tasks (tool_version_id)
    WHERE state IN ('assigned', 'running') AND tool_version_id IS NOT NULL;
CREATE INDEX execution_tasks_tenant_active_idx
    ON execution.execution_tasks (org_id)
    WHERE state IN ('assigned', 'running');

-- Execution schema: authoritative execution state. Only the orchestrator writes these tables.
--
-- No cross-schema foreign keys: control definitions are validated by the orchestrator at materialisation. All
-- timestamps that order work (leases, deadlines, backoff) use the PostgreSQL clock.

CREATE TABLE execution.executions (
    id                  text PRIMARY KEY CHECK (id ~ '^exec_[A-Za-z0-9_-]{8,64}$'),
    org_id              text NOT NULL,
    project_id          text NOT NULL,
    workflow_id         text NOT NULL,
    workflow_version_id text NOT NULL,
    state               text NOT NULL DEFAULT 'pending' CHECK (
        state IN ('pending', 'running', 'failing', 'cancel_requested', 'succeeded', 'failed', 'cancelled')
    ),
    state_version       bigint NOT NULL DEFAULT 1 CHECK (state_version >= 1),
    request_id          text NOT NULL CHECK (length(request_id) BETWEEN 1 AND 200),
    request_digest      text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
    input               jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(input) = 'object'),
    input_digest        text NOT NULL CHECK (input_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_by          text NOT NULL CHECK (length(created_by) BETWEEN 1 AND 255),
    created_at          timestamptz NOT NULL DEFAULT now(),
    started_at          timestamptz,
    ended_at            timestamptz,
    deadline_at         timestamptz,
    cancel_epoch        integer NOT NULL DEFAULT 0 CHECK (cancel_epoch >= 0),
    cancel_reason       text CHECK (cancel_reason IN ('user', 'deadline')),
    failure_code        text,
    failure_detail      text,
    next_event_seq      bigint NOT NULL DEFAULT 1 CHECK (next_event_seq >= 1),
    UNIQUE (org_id, request_id),
    UNIQUE (id, org_id),
    CHECK (octet_length(input::text) <= 262144),
    CHECK (ended_at IS NULL OR started_at IS NULL OR ended_at >= started_at),
    CHECK (
        (state IN ('succeeded', 'failed', 'cancelled') AND ended_at IS NOT NULL)
        OR (state NOT IN ('succeeded', 'failed', 'cancelled') AND ended_at IS NULL)
    ),
    CHECK (state <> 'failed' OR failure_code IS NOT NULL),
    CHECK (cancel_reason IS NULL OR state IN ('cancel_requested', 'cancelled', 'failed'))
);

CREATE INDEX executions_org_created_idx
    ON execution.executions (org_id, project_id, created_at DESC, id DESC);
CREATE INDEX executions_org_state_idx ON execution.executions (org_id, state);
CREATE INDEX executions_schedulable_idx
    ON execution.executions (state, created_at)
    WHERE state IN ('pending', 'running', 'failing', 'cancel_requested');
CREATE INDEX executions_deadline_idx
    ON execution.executions (deadline_at)
    WHERE deadline_at IS NOT NULL AND state NOT IN ('succeeded', 'failed', 'cancelled');

CREATE TABLE execution.workers (
    id                       text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    boot_id                  uuid NOT NULL,
    pool                     text NOT NULL DEFAULT 'default' CHECK (length(pool) BETWEEN 1 AND 64),
    status                   text NOT NULL DEFAULT 'active' CHECK (
        status IN ('active', 'unhealthy', 'draining', 'offline')
    ),
    max_concurrency          integer NOT NULL DEFAULT 1 CHECK (max_concurrency BETWEEN 1 AND 1024),
    reported_available_slots integer NOT NULL DEFAULT 0 CHECK (
        reported_available_slots >= 0 AND reported_available_slots <= max_concurrency
    ),
    reserved_slots           integer NOT NULL DEFAULT 0 CHECK (
        reserved_slots >= 0 AND reserved_slots <= max_concurrency
    ),
    labels                   jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object'),
    runtime_abis             integer[] NOT NULL DEFAULT '{}'::integer[] CHECK (array_position(runtime_abis, NULL) IS NULL),
    runtime_digests          text[] NOT NULL DEFAULT '{}'::text[] CHECK (array_position(runtime_digests, NULL) IS NULL),
    binary_version           text NOT NULL,
    protocol_version         integer NOT NULL CHECK (protocol_version >= 1),
    session_id               text,
    registered_at            timestamptz NOT NULL DEFAULT now(),
    last_heartbeat_at        timestamptz NOT NULL DEFAULT now(),
    drained_at               timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX workers_schedulable_idx
    ON execution.workers (pool, status, last_heartbeat_at)
    WHERE status = 'active';
CREATE INDEX workers_heartbeat_idx ON execution.workers (status, last_heartbeat_at);

CREATE TABLE execution.execution_tasks (
    id                 text PRIMARY KEY CHECK (id ~ '^task_[A-Za-z0-9_-]{8,64}$'),
    execution_id       text NOT NULL,
    org_id             text NOT NULL,
    node_id            text NOT NULL CHECK (node_id ~ '^[a-z][a-z0-9_]{0,63}$'),
    node_type          text NOT NULL CHECK (node_type IN ('agent', 'tool')),
    agent_version_id   text,
    tool_version_id    text,
    worker_pool        text,
    state              text NOT NULL DEFAULT 'pending' CHECK (
        state IN ('pending', 'assigned', 'running', 'succeeded', 'failed', 'cancelled', 'skipped')
    ),
    state_version      bigint NOT NULL DEFAULT 1 CHECK (state_version >= 1),
    attempt_count      integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts       integer NOT NULL DEFAULT 1 CHECK (max_attempts BETWEEN 1 AND 10),
    timeout_seconds    integer NOT NULL DEFAULT 600 CHECK (timeout_seconds BETWEEN 1 AND 86400),
    next_attempt_at    timestamptz,
    owner_worker_id    text REFERENCES execution.workers (id) ON DELETE RESTRICT,
    lease_id           uuid,
    lease_expires_at   timestamptz,
    latest_attempt_id  text,
    skip_reason        text CHECK (
        skip_reason IN ('branch_not_taken', 'upstream_not_taken', 'dependency_failed')
    ),
    side_effect_class  text NOT NULL DEFAULT 'read_only' CHECK (
        side_effect_class IN ('read_only', 'idempotent', 'non_idempotent')
    ),
    started_at         timestamptz,
    ended_at           timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (execution_id, node_id),
    UNIQUE (id, execution_id, org_id),
    FOREIGN KEY (execution_id, org_id) REFERENCES execution.executions (id, org_id) ON DELETE CASCADE,
    CHECK (attempt_count <= max_attempts),
    CHECK (
        (node_type = 'agent' AND agent_version_id IS NOT NULL AND tool_version_id IS NULL)
        OR (node_type = 'tool' AND tool_version_id IS NOT NULL AND agent_version_id IS NULL)
    ),
    CHECK (
        (state IN ('assigned', 'running')
            AND owner_worker_id IS NOT NULL
            AND lease_id IS NOT NULL
            AND lease_expires_at IS NOT NULL)
        OR (state NOT IN ('assigned', 'running')
            AND owner_worker_id IS NULL
            AND lease_id IS NULL
            AND lease_expires_at IS NULL)
    ),
    CHECK (
        (state = 'skipped' AND skip_reason IS NOT NULL)
        OR (state <> 'skipped' AND skip_reason IS NULL)
    ),
    CHECK (started_at IS NULL OR started_at >= created_at),
    CHECK (ended_at IS NULL OR started_at IS NULL OR ended_at >= started_at),
    CHECK (
        (state IN ('succeeded', 'failed', 'cancelled', 'skipped') AND ended_at IS NOT NULL)
        OR (state NOT IN ('succeeded', 'failed', 'cancelled', 'skipped') AND ended_at IS NULL)
    )
);

CREATE INDEX execution_tasks_execution_state_idx ON execution.execution_tasks (execution_id, state);
CREATE INDEX execution_tasks_schedulable_idx
    ON execution.execution_tasks (state, next_attempt_at)
    WHERE state = 'pending';
CREATE INDEX execution_tasks_org_state_idx ON execution.execution_tasks (org_id, state);
CREATE INDEX execution_tasks_worker_idx
    ON execution.execution_tasks (owner_worker_id)
    WHERE owner_worker_id IS NOT NULL;
CREATE UNIQUE INDEX execution_tasks_lease_idx
    ON execution.execution_tasks (lease_id)
    WHERE lease_id IS NOT NULL;

CREATE TABLE execution.task_dependencies (
    execution_id       text NOT NULL,
    task_id            text NOT NULL,
    depends_on_task_id text NOT NULL,
    org_id             text NOT NULL,
    when_expr          text,
    when_digest        text,
    PRIMARY KEY (task_id, depends_on_task_id),
    CHECK (task_id <> depends_on_task_id),
    CHECK (
        (when_expr IS NULL AND when_digest IS NULL)
        OR (when_expr IS NOT NULL AND when_digest IS NOT NULL)
    ),
    CHECK (when_expr IS NULL OR length(when_expr) BETWEEN 1 AND 2000),
    CHECK (when_digest IS NULL OR when_digest ~ '^sha256:[0-9a-f]{64}$'),
    FOREIGN KEY (task_id, execution_id, org_id)
        REFERENCES execution.execution_tasks (id, execution_id, org_id) ON DELETE CASCADE,
    FOREIGN KEY (depends_on_task_id, execution_id, org_id)
        REFERENCES execution.execution_tasks (id, execution_id, org_id) ON DELETE CASCADE
);

CREATE INDEX task_dependencies_upstream_idx
    ON execution.task_dependencies (depends_on_task_id, execution_id);

CREATE TABLE execution.task_attempts (
    id                  text PRIMARY KEY CHECK (id ~ '^att_[A-Za-z0-9_-]{8,64}$'),
    task_id             text NOT NULL,
    execution_id        text NOT NULL,
    org_id              text NOT NULL,
    attempt_number      integer NOT NULL CHECK (attempt_number >= 1),
    worker_id           text NOT NULL CHECK (length(worker_id) BETWEEN 1 AND 128),
    lease_id            uuid NOT NULL UNIQUE,
    outcome             text CHECK (
        outcome IN ('succeeded', 'failed', 'cancelled', 'lease_expired', 'incomplete')
    ),
    outcome_unknown     boolean NOT NULL DEFAULT false,
    effect_uncertain    boolean NOT NULL DEFAULT false,
    dispatched_at       timestamptz NOT NULL DEFAULT now(),
    accepted_at         timestamptz,
    started_at          timestamptz,
    finished_at         timestamptz,
    error_code          text,
    error_message       text,
    result_inline       jsonb CHECK (jsonb_typeof(result_inline) = 'object'),
    result_object_key   text,
    result_sha256       text CHECK (result_sha256 IS NULL OR result_sha256 ~ '^sha256:[0-9a-f]{64}$'),
    result_size_bytes   bigint CHECK (result_size_bytes IS NULL OR result_size_bytes >= 0),
    result_content_type text,
    checkpoint_id       text,
    metrics             jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(metrics) = 'object' AND octet_length(metrics::text) <= 65536
    ),
    runtime_version     text,
    UNIQUE (task_id, attempt_number),
    UNIQUE (id, task_id, execution_id, org_id),
    FOREIGN KEY (task_id, execution_id, org_id)
        REFERENCES execution.execution_tasks (id, execution_id, org_id) ON DELETE CASCADE,
    CHECK ((finished_at IS NULL AND outcome IS NULL) OR (finished_at IS NOT NULL AND outcome IS NOT NULL)),
    CHECK (accepted_at IS NULL OR accepted_at >= dispatched_at),
    CHECK (started_at IS NULL OR started_at >= dispatched_at),
    CHECK (finished_at IS NULL OR finished_at >= dispatched_at),
    CHECK (accepted_at IS NULL OR finished_at IS NULL OR finished_at >= accepted_at),
    CHECK (result_inline IS NULL OR octet_length(result_inline::text) <= 65536),
    CHECK (
        result_object_key IS NULL
        OR (result_sha256 IS NOT NULL AND result_size_bytes IS NOT NULL)
    ),
    CHECK (result_object_key IS NULL OR length(result_object_key) BETWEEN 1 AND 1024)
);

CREATE UNIQUE INDEX task_attempts_open_idx
    ON execution.task_attempts (task_id)
    WHERE finished_at IS NULL;
CREATE INDEX task_attempts_task_idx ON execution.task_attempts (task_id, attempt_number DESC);
CREATE INDEX task_attempts_execution_idx ON execution.task_attempts (execution_id);

CREATE TABLE execution.checkpoints (
    id                       text PRIMARY KEY CHECK (id ~ '^ckpt_[A-Za-z0-9_-]{8,64}$'),
    execution_id             text NOT NULL,
    task_id                  text NOT NULL,
    attempt_id               text NOT NULL,
    org_id                   text NOT NULL,
    object_key               text NOT NULL UNIQUE CHECK (length(object_key) BETWEEN 1 AND 1024),
    sha256                   text NOT NULL CHECK (sha256 ~ '^sha256:[0-9a-f]{64}$'),
    size_bytes               bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 33554432),
    checkpoint_schema_version integer NOT NULL CHECK (checkpoint_schema_version >= 1),
    runtime_abi              integer NOT NULL CHECK (runtime_abi >= 1),
    runtime_version          text NOT NULL CHECK (length(runtime_version) BETWEEN 1 AND 128),
    agent_version_id         text NOT NULL,
    created_at               timestamptz NOT NULL DEFAULT now(),
    expires_at               timestamptz,
    UNIQUE (attempt_id),
    FOREIGN KEY (attempt_id, task_id, execution_id, org_id)
        REFERENCES execution.task_attempts (id, task_id, execution_id, org_id) ON DELETE CASCADE,
    FOREIGN KEY (task_id, execution_id, org_id)
        REFERENCES execution.execution_tasks (id, execution_id, org_id) ON DELETE CASCADE
);

CREATE INDEX checkpoints_task_created_idx ON execution.checkpoints (task_id, created_at DESC);
CREATE INDEX checkpoints_expires_idx ON execution.checkpoints (expires_at) WHERE expires_at IS NOT NULL;

ALTER TABLE execution.task_attempts
    ADD CONSTRAINT task_attempts_checkpoint_fk
    FOREIGN KEY (checkpoint_id) REFERENCES execution.checkpoints (id) ON DELETE SET NULL;

ALTER TABLE execution.execution_tasks
    ADD CONSTRAINT execution_tasks_latest_attempt_fk
    FOREIGN KEY (latest_attempt_id) REFERENCES execution.task_attempts (id) ON DELETE SET NULL;

CREATE TABLE execution.execution_events (
    id          bigserial PRIMARY KEY,
    event_id    text NOT NULL UNIQUE CHECK (event_id ~ '^evt_[A-Za-z0-9_-]{8,64}$'),
    execution_id text NOT NULL,
    org_id      text NOT NULL,
    seq         bigint NOT NULL CHECK (seq >= 1),
    event_type  text NOT NULL CHECK (
        event_type IN ('execution.created', 'execution.state_changed', 'task.state_changed', 'task.attempt_finished')
    ),
    entity_type text NOT NULL CHECK (entity_type IN ('execution', 'task', 'attempt')),
    entity_id   text NOT NULL CHECK (length(entity_id) BETWEEN 1 AND 128),
    actor_type  text NOT NULL CHECK (actor_type IN ('user', 'system', 'worker')),
    actor_id    text NOT NULL CHECK (length(actor_id) BETWEEN 1 AND 255),
    from_state  text,
    to_state    text,
    reason      text,
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 262144
    ),
    trace_id    text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (execution_id, seq),
    FOREIGN KEY (execution_id, org_id) REFERENCES execution.executions (id, org_id) ON DELETE CASCADE
);

CREATE INDEX execution_events_org_created_idx ON execution.execution_events (org_id, created_at DESC);
CREATE INDEX execution_events_type_idx ON execution.execution_events (event_type, created_at DESC);
CREATE INDEX execution_events_payload_gin_idx ON execution.execution_events USING gin (payload jsonb_path_ops);

CREATE TABLE execution.outbox (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id     text NOT NULL UNIQUE CHECK (message_id ~ '^msg_[A-Za-z0-9_-]{8,64}$'),
    subject        text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
    message_type   text NOT NULL CHECK (length(message_type) BETWEEN 1 AND 128),
    schema_version integer NOT NULL DEFAULT 1 CHECK (schema_version >= 1),
    org_id         text NOT NULL,
    execution_id   text,
    payload        jsonb NOT NULL CHECK (
        jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 262144
    ),
    headers        jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(headers) = 'object'),
    created_at     timestamptz NOT NULL DEFAULT now(),
    available_at   timestamptz NOT NULL DEFAULT now(),
    published_at   timestamptz,
    attempts       integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error     text,
    FOREIGN KEY (execution_id, org_id) REFERENCES execution.executions (id, org_id) ON DELETE CASCADE
);

CREATE INDEX outbox_claim_idx
    ON execution.outbox (available_at, id)
    WHERE published_at IS NULL;
CREATE INDEX outbox_published_idx ON execution.outbox (published_at) WHERE published_at IS NOT NULL;
CREATE INDEX outbox_org_idx ON execution.outbox (org_id, created_at);

-- Phase 10 multi-agent orchestration: delegation hierarchy, policies and durable agent messages.

ALTER TABLE execution.executions
    ADD COLUMN delegation_policy jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(delegation_policy) = 'object' AND octet_length(delegation_policy::text) <= 65536
    ),
    ADD COLUMN delegation_depth integer NOT NULL DEFAULT 0 CHECK (delegation_depth BETWEEN 0 AND 64);

CREATE TABLE execution.delegations (
    id                 text PRIMARY KEY CHECK (id ~ '^dlg_[A-Za-z0-9_-]{8,64}$'),
    org_id             text NOT NULL,
    root_execution_id  text NOT NULL,
    parent_execution_id text NOT NULL,
    parent_task_id     text NOT NULL,
    child_execution_id text NOT NULL UNIQUE,
    agent_id           text NOT NULL,
    agent_version      integer NOT NULL CHECK (agent_version >= 1),
    request            jsonb NOT NULL CHECK (
        jsonb_typeof(request) = 'object' AND octet_length(request::text) <= 262144
    ),
    shared_context     jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(shared_context) = 'object' AND octet_length(shared_context::text) <= 262144
    ),
    context_refs       text[] NOT NULL DEFAULT '{}'::text[] CHECK (array_position(context_refs, NULL) IS NULL),
    status             text NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')
    ),
    result_inline      jsonb CHECK (result_inline IS NULL OR jsonb_typeof(result_inline) = 'object'),
    error_code         text,
    error_message      text,
    retries            integer NOT NULL DEFAULT 0 CHECK (retries >= 0),
    idempotency_key    text NOT NULL DEFAULT '' CHECK (length(idempotency_key) <= 300),
    created_at         timestamptz NOT NULL DEFAULT now(),
    finished_at        timestamptz,
    FOREIGN KEY (root_execution_id) REFERENCES execution.executions (id) ON DELETE CASCADE,
    FOREIGN KEY (parent_execution_id) REFERENCES execution.executions (id) ON DELETE CASCADE,
    FOREIGN KEY (child_execution_id) REFERENCES execution.executions (id) ON DELETE CASCADE
);

CREATE INDEX delegations_root_created_idx
    ON execution.delegations (root_execution_id, created_at);
CREATE INDEX delegations_parent_idx
    ON execution.delegations (parent_execution_id, created_at);
CREATE INDEX delegations_status_idx
    ON execution.delegations (root_execution_id, status)
    WHERE status IN ('pending', 'running');
CREATE UNIQUE INDEX delegations_idempotency_key_idx
    ON execution.delegations (parent_task_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE TABLE execution.agent_events (
    id                  text PRIMARY KEY CHECK (id ~ '^evtmsg_[A-Za-z0-9_-]{8,64}$'),
    org_id              text NOT NULL,
    root_execution_id   text NOT NULL,
    execution_id        text NOT NULL,
    task_id             text,
    topic               text NOT NULL CHECK (length(topic) BETWEEN 1 AND 200),
    payload             jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    created_at          timestamptz NOT NULL DEFAULT now(),
    consumed_by_execution_id text,
    consumed_at         timestamptz,
    FOREIGN KEY (root_execution_id) REFERENCES execution.executions (id) ON DELETE CASCADE,
    FOREIGN KEY (execution_id) REFERENCES execution.executions (id) ON DELETE CASCADE
);

CREATE INDEX agent_events_wait_idx
    ON execution.agent_events (root_execution_id, topic, created_at)
    WHERE consumed_by_execution_id IS NULL;

CREATE TABLE execution.approvals (
    id            text PRIMARY KEY CHECK (id ~ '^apr_[A-Za-z0-9_-]{8,64}$'),
    org_id        text NOT NULL,
    project_id    text NOT NULL,
    execution_id  text NOT NULL,
    task_id       text NOT NULL UNIQUE,
    prompt        text NOT NULL DEFAULT '' CHECK (length(prompt) <= 2000),
    policy        text NOT NULL DEFAULT 'any' CHECK (policy IN ('any', 'all')),
    approvers     text[] NOT NULL DEFAULT '{}'::text[] CHECK (
        array_position(approvers, NULL) IS NULL AND cardinality(approvers) <= 20
    ),
    status        text NOT NULL DEFAULT 'pending' CHECK (
        status IN ('pending', 'approved', 'rejected', 'expired')
    ),
    requested_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz,
    decided_at    timestamptz,
    decided_by    text,
    reason        text CHECK (reason IS NULL OR length(reason) <= 2000),
    FOREIGN KEY (execution_id) REFERENCES execution.executions (id) ON DELETE CASCADE
);

CREATE INDEX approvals_execution_idx
    ON execution.approvals (execution_id, requested_at);
CREATE INDEX approvals_pending_idx
    ON execution.approvals (expires_at)
    WHERE status = 'pending';

CREATE TABLE execution.approval_decisions (
    id          text PRIMARY KEY CHECK (id ~ '^apd_[A-Za-z0-9_-]{8,64}$'),
    approval_id text NOT NULL,
    actor       text NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
    decision    text NOT NULL CHECK (decision IN ('approve', 'reject', 'expire')),
    comment     text NOT NULL DEFAULT '' CHECK (length(comment) <= 2000),
    created_at  timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (approval_id) REFERENCES execution.approvals (id) ON DELETE CASCADE
);

CREATE INDEX approval_decisions_approval_idx
    ON execution.approval_decisions (approval_id, created_at);

-- Tenant isolation for the Phase 10 tables. approval_decisions inherits the approval's tenant.
ALTER TABLE execution.delegations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.delegations
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.agent_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.agent_events
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.approvals ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.approvals
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.approval_decisions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.approval_decisions
    USING (EXISTS (
        SELECT 1 FROM execution.approvals a
        WHERE a.id = approval_id AND a.org_id = current_setting('app.tenant_id', true)
    ))
    WITH CHECK (EXISTS (
        SELECT 1 FROM execution.approvals a
        WHERE a.id = approval_id AND a.org_id = current_setting('app.tenant_id', true)
    ));

GRANT SELECT ON execution.approvals, execution.approval_decisions, execution.delegations
TO app_api, app_readonly_ops;
REVOKE SELECT ON execution.agent_events FROM app_api, app_readonly_ops;

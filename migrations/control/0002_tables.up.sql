-- Control-plane tables. Immutable definitions, tenancy, credentials and request idempotency.
--
-- The API is the only writer of this schema. The orchestrator gets SELECT-only access for materialisation.
-- Tenant-owned tables carry org_id first in indexes and uniqueness constraints (data-ownership.md §5).

CREATE TABLE control.organizations (
    id         text PRIMARY KEY CHECK (id ~ '^org_[A-Za-z0-9_-]{8,64}$'),
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    slug       text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE control.projects (
    id         text PRIMARY KEY CHECK (id ~ '^proj_[A-Za-z0-9_-]{8,64}$'),
    org_id     text NOT NULL REFERENCES control.organizations (id) ON DELETE RESTRICT,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    slug       text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived', 'deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, org_id),
    UNIQUE (org_id, slug)
);

CREATE INDEX projects_org_status_idx ON control.projects (org_id, status);

CREATE TABLE control.memberships (
    org_id       text NOT NULL REFERENCES control.organizations (id) ON DELETE CASCADE,
    principal_id text NOT NULL CHECK (length(principal_id) BETWEEN 1 AND 255),
    role         text NOT NULL CHECK (role IN ('owner', 'operator', 'viewer')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, principal_id)
);

-- Discovery happens before a tenant context exists; this lookup is the only path that does not use RLS.
CREATE INDEX memberships_principal_idx ON control.memberships (principal_id);

CREATE TABLE control.agents (
    id             text PRIMARY KEY CHECK (id ~ '^agt_[A-Za-z0-9_-]{8,64}$'),
    org_id         text NOT NULL,
    project_id     text NOT NULL,
    name           text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description    text NOT NULL DEFAULT '',
    latest_version integer NOT NULL DEFAULT 0 CHECK (latest_version >= 0),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name),
    UNIQUE (id, org_id, project_id),
    FOREIGN KEY (project_id, org_id) REFERENCES control.projects (id, org_id) ON DELETE RESTRICT
);

CREATE INDEX agents_org_project_idx ON control.agents (org_id, project_id, name);

CREATE TABLE control.agent_versions (
    id           text PRIMARY KEY CHECK (id ~ '^agtv_[A-Za-z0-9_-]{8,64}$'),
    agent_id     text NOT NULL,
    org_id       text NOT NULL,
    project_id   text NOT NULL,
    version      integer NOT NULL CHECK (version >= 1),
    status       text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'deprecated')),
    digest       text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    spec         jsonb NOT NULL CHECK (jsonb_typeof(spec) = 'object' AND octet_length(spec::text) <= 1048576),
    runtime_abi  integer NOT NULL CHECK (runtime_abi >= 1),
    created_by   text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    UNIQUE (agent_id, version),
    UNIQUE (agent_id, digest),
    UNIQUE (id, org_id, project_id),
    FOREIGN KEY (agent_id, org_id, project_id) REFERENCES control.agents (id, org_id, project_id) ON DELETE RESTRICT,
    CHECK (
        (status = 'draft' AND published_at IS NULL)
        OR (status IN ('published', 'deprecated') AND published_at IS NOT NULL)
    )
);

CREATE INDEX agent_versions_org_status_idx ON control.agent_versions (org_id, project_id, status, version DESC);

CREATE TABLE control.workflows (
    id             text PRIMARY KEY CHECK (id ~ '^wf_[A-Za-z0-9_-]{8,64}$'),
    org_id         text NOT NULL,
    project_id     text NOT NULL,
    name           text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description    text NOT NULL DEFAULT '',
    latest_version integer NOT NULL DEFAULT 0 CHECK (latest_version >= 0),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name),
    UNIQUE (id, org_id, project_id),
    FOREIGN KEY (project_id, org_id) REFERENCES control.projects (id, org_id) ON DELETE RESTRICT
);

CREATE INDEX workflows_org_project_idx ON control.workflows (org_id, project_id, name);

CREATE TABLE control.workflow_versions (
    id           text PRIMARY KEY CHECK (id ~ '^wfv_[A-Za-z0-9_-]{8,64}$'),
    workflow_id  text NOT NULL,
    org_id       text NOT NULL,
    project_id   text NOT NULL,
    version      integer NOT NULL CHECK (version >= 1),
    status       text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'deprecated')),
    digest       text NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    graph        jsonb NOT NULL CHECK (
        jsonb_typeof(graph) = 'object'
        AND graph ? 'nodes'
        AND graph ? 'edges'
        AND jsonb_typeof(graph -> 'nodes') = 'array'
        AND jsonb_typeof(graph -> 'edges') = 'array'
        AND octet_length(graph::text) <= 4194304
    ),
    created_by   text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    UNIQUE (workflow_id, version),
    UNIQUE (workflow_id, digest),
    UNIQUE (id, org_id, project_id),
    FOREIGN KEY (workflow_id, org_id, project_id) REFERENCES control.workflows (id, org_id, project_id) ON DELETE RESTRICT,
    CHECK (
        (status = 'draft' AND published_at IS NULL)
        OR (status IN ('published', 'deprecated') AND published_at IS NOT NULL)
    )
);

CREATE INDEX workflow_versions_org_status_idx ON control.workflow_versions (org_id, project_id, status, version DESC);
CREATE INDEX workflow_versions_graph_gin_idx ON control.workflow_versions USING gin (graph jsonb_path_ops);

CREATE TABLE control.credentials (
    id          text PRIMARY KEY CHECK (id ~ '^cred_[A-Za-z0-9_-]{8,64}$'),
    org_id      text NOT NULL,
    project_id  text NOT NULL,
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    ciphertext  bytea NOT NULL CHECK (octet_length(ciphertext) > 0),
    nonce       bytea NOT NULL CHECK (octet_length(nonce) > 0),
    kms_key_id  text NOT NULL CHECK (length(kms_key_id) BETWEEN 1 AND 255),
    created_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    rotated_at  timestamptz,
    UNIQUE (project_id, name),
    FOREIGN KEY (project_id, org_id) REFERENCES control.projects (id, org_id) ON DELETE RESTRICT
);

CREATE INDEX credentials_org_idx ON control.credentials (org_id, project_id);

CREATE TABLE control.idempotency_keys (
    org_id          text NOT NULL REFERENCES control.organizations (id) ON DELETE CASCADE,
    key             text NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
    principal_id    text NOT NULL,
    method          text NOT NULL,
    path            text NOT NULL,
    request_hash    text NOT NULL CHECK (request_hash ~ '^sha256:[0-9a-f]{64}$'),
    response_status integer,
    response_body   jsonb,
    resource_id     text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    PRIMARY KEY (org_id, key)
);

CREATE INDEX idempotency_keys_expires_idx ON control.idempotency_keys (expires_at);

COMMENT ON TABLE control.memberships IS 'OIDC principals; no local users table. Tenant context is derived from membership.';
COMMENT ON TABLE control.credentials IS 'Envelope-encrypted secret values. The API writes write-only values and returns names only.';
COMMENT ON TABLE control.idempotency_keys IS 'Replay protection for non-execution POSTs; execution dedupe uses execution.executions.request_id.';

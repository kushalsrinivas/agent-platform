-- Phase 14: permission groups, encrypted secret metadata, and append-only tenant audit.

-- Permission groups: owner > admin > developer/operator > viewer. "operator" remains for API compatibility.
ALTER TABLE control.memberships DROP CONSTRAINT memberships_role_check;
ALTER TABLE control.memberships
    ADD CONSTRAINT memberships_role_check
        CHECK (role IN ('owner', 'admin', 'developer', 'operator', 'viewer'));

ALTER TABLE control.api_keys DROP CONSTRAINT api_keys_role_check;
ALTER TABLE control.api_keys
    ADD CONSTRAINT api_keys_role_check
        CHECK (role IN ('owner', 'admin', 'developer', 'operator', 'viewer'));

-- Secrets are project-scoped, optionally environment-scoped. Values are envelope-encrypted by the API.
ALTER TABLE control.credentials
    ADD COLUMN environment_id text,
    ADD COLUMN description text NOT NULL DEFAULT '',
    ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    ADD COLUMN last_accessed_at timestamptz,
    ADD COLUMN revoked_at timestamptz;

ALTER TABLE control.credentials DROP CONSTRAINT credentials_project_id_name_key;
CREATE UNIQUE INDEX credentials_scope_name_key
    ON control.credentials (project_id, COALESCE(environment_id, ''), name);
CREATE INDEX credentials_org_project_env_idx ON control.credentials (org_id, project_id, environment_id);

-- Audit trail is tenant-scoped and append-only. The API never receives UPDATE/DELETE grants.
CREATE TABLE control.audit_events (
    id            text PRIMARY KEY CHECK (id ~ '^aud_[A-Za-z0-9_-]{8,64}$'),
    org_id        text NOT NULL,
    project_id    text,
    actor_type    text NOT NULL CHECK (actor_type IN ('user', 'api_key', 'system', 'worker')),
    actor_id      text NOT NULL,
    action        text NOT NULL CHECK (length(action) BETWEEN 1 AND 120),
    resource_type text NOT NULL CHECK (length(resource_type) BETWEEN 1 AND 80),
    resource_id   text NOT NULL DEFAULT '',
    outcome       text NOT NULL CHECK (outcome IN ('success', 'denied', 'failure')),
    details       jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_org_created_idx ON control.audit_events (org_id, created_at DESC, id);
CREATE INDEX audit_events_action_created_idx ON control.audit_events (action, created_at DESC);

ALTER TABLE control.audit_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.audit_events
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

CREATE FUNCTION control.enforce_audit_append_only() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' AND current_user IN ('app_retention', 'postgres') THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'audit_events is append-only (current_user=%)', current_user
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

CREATE TRIGGER audit_events_append_only
    BEFORE UPDATE OR DELETE ON control.audit_events
    FOR EACH ROW EXECUTE FUNCTION control.enforce_audit_append_only();

GRANT SELECT, INSERT ON control.audit_events TO app_api, app_orchestrator;
-- Secret resolution records last use, but the orchestrator cannot change ciphertext or metadata.
GRANT UPDATE (last_accessed_at) ON control.credentials TO app_orchestrator;
GRANT SELECT ON control.audit_events TO app_readonly_ops;
REVOKE UPDATE, DELETE ON control.audit_events FROM app_api, app_orchestrator, app_readonly_ops;

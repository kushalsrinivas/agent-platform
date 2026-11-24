-- Developer API: API keys and project environments.

CREATE TABLE control.environments (
    id         text PRIMARY KEY CHECK (id ~ '^env_[A-Za-z0-9_-]{8,64}$'),
    org_id     text NOT NULL,
    project_id text NOT NULL REFERENCES control.projects (id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    slug       text NOT NULL CHECK (slug ~ '^[a-z][a-z0-9-]{1,62}$'),
    kind       text NOT NULL DEFAULT 'development' CHECK (kind IN ('development', 'staging', 'production')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, slug)
);

CREATE TRIGGER environments_touch_updated_at
    BEFORE UPDATE ON control.environments
    FOR EACH ROW EXECUTE FUNCTION control.touch_updated_at();

CREATE TABLE control.api_keys (
    id           text PRIMARY KEY CHECK (id ~ '^key_[A-Za-z0-9_-]{8,64}$'),
    org_id       text NOT NULL,
    project_id   text REFERENCES control.projects (id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    key_prefix   text NOT NULL CHECK (length(key_prefix) BETWEEN 8 AND 24),
    key_hash     text NOT NULL UNIQUE CHECK (key_hash ~ '^[a-f0-9]{64}$'),
    role         text NOT NULL CHECK (role IN ('viewer', 'operator', 'owner')),
    created_by   text NOT NULL CHECK (length(created_by) BETWEEN 1 AND 255),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz,
    revoked_at   timestamptz
);

CREATE INDEX api_keys_org_idx ON control.api_keys (org_id, created_at DESC);
CREATE INDEX api_keys_project_idx ON control.api_keys (project_id, created_at DESC)
    WHERE project_id IS NOT NULL;

-- Key verification happens before a tenant context exists. The function updates last_used_at and returns only
-- the key's own scope; it never returns the hash.
CREATE OR REPLACE FUNCTION control.resolve_api_key(p_hash text)
    RETURNS TABLE (key_id text, org_id text, project_id text, role text, principal_id text)
    LANGUAGE sql VOLATILE SECURITY DEFINER
    SET search_path = control, pg_temp
AS
$$
UPDATE api_keys
SET last_used_at = now()
WHERE key_hash = p_hash
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
RETURNING id, org_id, project_id, role, 'apikey_' || id
$$;

REVOKE ALL ON FUNCTION control.resolve_api_key(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION control.resolve_api_key(text) TO app_api;

ALTER TABLE control.environments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.environments
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE control.api_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.api_keys
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON control.environments, control.api_keys TO app_api;
GRANT SELECT ON control.environments, control.api_keys TO app_readonly_ops;
GRANT SELECT ON control.environments TO app_orchestrator;

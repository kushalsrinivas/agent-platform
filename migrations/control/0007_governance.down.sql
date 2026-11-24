REVOKE UPDATE (last_accessed_at) ON control.credentials FROM app_orchestrator;
DROP TABLE IF EXISTS control.audit_events;

ALTER TABLE control.credentials
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS last_accessed_at,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS environment_id;

DROP INDEX IF EXISTS control.credentials_scope_name_key;
DROP INDEX IF EXISTS control.credentials_org_project_env_idx;
ALTER TABLE control.credentials
    ADD CONSTRAINT credentials_project_id_name_key UNIQUE (project_id, name);

ALTER TABLE control.api_keys DROP CONSTRAINT api_keys_role_check;
ALTER TABLE control.api_keys
    ADD CONSTRAINT api_keys_role_check CHECK (role IN ('viewer', 'operator', 'owner'));

ALTER TABLE control.memberships DROP CONSTRAINT memberships_role_check;
ALTER TABLE control.memberships
    ADD CONSTRAINT memberships_role_check CHECK (role IN ('owner', 'operator', 'viewer'));

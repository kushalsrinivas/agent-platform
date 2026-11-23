-- Control-plane security: updated_at maintenance, version immutability, RLS and grants.

CREATE FUNCTION control.touch_updated_at() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER organizations_touch_updated_at
    BEFORE UPDATE ON control.organizations
    FOR EACH ROW EXECUTE FUNCTION control.touch_updated_at();

CREATE TRIGGER projects_touch_updated_at
    BEFORE UPDATE ON control.projects
    FOR EACH ROW EXECUTE FUNCTION control.touch_updated_at();

CREATE TRIGGER agents_touch_updated_at
    BEFORE UPDATE ON control.agents
    FOR EACH ROW EXECUTE FUNCTION control.touch_updated_at();

CREATE TRIGGER workflows_touch_updated_at
    BEFORE UPDATE ON control.workflows
    FOR EACH ROW EXECUTE FUNCTION control.touch_updated_at();

-- Published version content is frozen. Only the two legal status transitions may update a row.
CREATE FUNCTION control.enforce_version_immutability() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF (to_jsonb(NEW) - 'status' - 'published_at')
        IS DISTINCT FROM (to_jsonb(OLD) - 'status' - 'published_at') THEN
        RAISE EXCEPTION 'version content is immutable (table %)', TG_TABLE_NAME
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    IF OLD.status = 'draft' AND NEW.status = 'published' THEN
        IF NEW.published_at IS NULL THEN
            RAISE EXCEPTION 'publishing a version requires published_at';
        END IF;
    ELSIF OLD.status = 'published' AND NEW.status = 'deprecated' THEN
        IF NEW.published_at IS DISTINCT FROM OLD.published_at THEN
            RAISE EXCEPTION 'deprecation may not change published_at';
        END IF;
    ELSE
        RAISE EXCEPTION 'illegal version status transition % -> %', OLD.status, NEW.status
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_versions_immutable
    BEFORE UPDATE ON control.agent_versions
    FOR EACH ROW EXECUTE FUNCTION control.enforce_version_immutability();

CREATE TRIGGER workflow_versions_immutable
    BEFORE UPDATE ON control.workflow_versions
    FOR EACH ROW EXECUTE FUNCTION control.enforce_version_immutability();

-- Tenant isolation. Application code must SET LOCAL app.tenant_id before any tenant query. A missing setting
-- produces no rows (current_setting(..., true) is NULL), never all rows.
ALTER TABLE control.organizations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.organizations
    USING (id = current_setting('app.tenant_id', true))
    WITH CHECK (id = current_setting('app.tenant_id', true));

ALTER TABLE control.projects ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.projects
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE control.memberships ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.memberships
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE control.agents ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.agents
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE control.agent_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.agent_versions
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE control.workflows ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.workflows
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE control.workflow_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.workflow_versions
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE control.credentials ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.credentials
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE control.idempotency_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON control.idempotency_keys
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

-- Organisation discovery is the only query before a tenant context exists. SECURITY DEFINER is required to read
-- memberships across the caller's own rows; the function returns nothing without app.principal_id.
CREATE FUNCTION control.list_orgs_for_principal()
    RETURNS TABLE (org_id text, name text, slug text, role text)
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = control, pg_temp
AS
$$
SELECT m.org_id, o.name, o.slug, m.role
FROM memberships m
JOIN organizations o ON o.id = m.org_id
WHERE m.principal_id = current_setting('app.principal_id', true)
  AND o.status = 'active'
ORDER BY o.name;
$$;

REVOKE ALL ON FUNCTION control.list_orgs_for_principal() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION control.list_orgs_for_principal() TO app_api;

-- Scheduling and retention are intentionally cross-tenant; break-glass ops are read-only.
ALTER ROLE app_orchestrator BYPASSRLS;
ALTER ROLE app_retention BYPASSRLS;
ALTER ROLE app_readonly_ops BYPASSRLS;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA control TO app_api;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA control TO app_api;
GRANT SELECT ON ALL TABLES IN SCHEMA control TO app_orchestrator, app_readonly_ops;

-- Versions and definitions are never deleted or hard-deleted by the API: parents are soft-deleted.
REVOKE DELETE ON
    control.organizations,
    control.projects,
    control.agents,
    control.agent_versions,
    control.workflows,
    control.workflow_versions
FROM app_api;

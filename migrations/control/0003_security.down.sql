DROP FUNCTION IF EXISTS control.list_orgs_for_principal();

DROP POLICY IF EXISTS tenant_isolation ON control.idempotency_keys;
DROP POLICY IF EXISTS tenant_isolation ON control.credentials;
DROP POLICY IF EXISTS tenant_isolation ON control.workflow_versions;
DROP POLICY IF EXISTS tenant_isolation ON control.workflows;
DROP POLICY IF EXISTS tenant_isolation ON control.agent_versions;
DROP POLICY IF EXISTS tenant_isolation ON control.agents;
DROP POLICY IF EXISTS tenant_isolation ON control.memberships;
DROP POLICY IF EXISTS tenant_isolation ON control.projects;
DROP POLICY IF EXISTS tenant_isolation ON control.organizations;

DROP TRIGGER IF EXISTS workflow_versions_immutable ON control.workflow_versions;
DROP TRIGGER IF EXISTS agent_versions_immutable ON control.agent_versions;
DROP TRIGGER IF EXISTS workflows_touch_updated_at ON control.workflows;
DROP TRIGGER IF EXISTS agents_touch_updated_at ON control.agents;
DROP TRIGGER IF EXISTS projects_touch_updated_at ON control.projects;
DROP TRIGGER IF EXISTS organizations_touch_updated_at ON control.organizations;
DROP FUNCTION IF EXISTS control.enforce_version_immutability();
DROP FUNCTION IF EXISTS control.touch_updated_at();

ALTER ROLE app_orchestrator NOBYPASSRLS;
ALTER ROLE app_retention NOBYPASSRLS;
ALTER ROLE app_readonly_ops NOBYPASSRLS;

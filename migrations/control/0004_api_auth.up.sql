-- API authentication helpers.
--
-- The API must resolve a principal's membership before it can set app.tenant_id, so this lookup cannot use RLS.
-- It is a narrowly scoped SECURITY DEFINER function: one row, caller-supplied org and principal, no writes.

CREATE OR REPLACE FUNCTION control.authorize_membership(p_org_id text, p_principal_id text)
    RETURNS text
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = control, pg_temp
AS
$$
SELECT m.role
FROM memberships m
WHERE m.org_id = p_org_id
  AND m.principal_id = p_principal_id
$$;

REVOKE ALL ON FUNCTION control.authorize_membership(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION control.authorize_membership(text, text) TO app_api;

-- Organization list needs creation timestamps for the public Organization resource.
DROP FUNCTION IF EXISTS control.list_orgs_for_principal();

CREATE FUNCTION control.list_orgs_for_principal()
    RETURNS TABLE (org_id text, name text, slug text, role text, created_at timestamptz)
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path = control, pg_temp
AS
$$
SELECT m.org_id, o.name, o.slug, m.role, o.created_at
FROM memberships m
JOIN organizations o ON o.id = m.org_id
WHERE m.principal_id = current_setting('app.principal_id', true)
  AND o.status = 'active'
ORDER BY o.name;
$$;

REVOKE ALL ON FUNCTION control.list_orgs_for_principal() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION control.list_orgs_for_principal() TO app_api;

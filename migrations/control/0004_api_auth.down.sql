DROP FUNCTION IF EXISTS control.authorize_membership(text, text);

DROP FUNCTION IF EXISTS control.list_orgs_for_principal();

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

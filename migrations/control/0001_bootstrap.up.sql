-- Local development bootstrap for the control schema.
--
-- Roles are created here so a fresh `docker compose up` needs no manual setup. In production the roles are
-- provisioned outside migrations by the platform operator; this block is guarded and only creates what is
-- missing. Passwords are development-only and never used by a deployed environment.
--
-- Schema and grants are deliberately separate from table DDL: control tables arrive in Phase 03 and inherit
-- these default privileges.

CREATE SCHEMA IF NOT EXISTS control;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_api') THEN
        CREATE ROLE app_api LOGIN PASSWORD 'dev_api_password';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_orchestrator') THEN
        CREATE ROLE app_orchestrator LOGIN PASSWORD 'dev_orchestrator_password';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_retention') THEN
        CREATE ROLE app_retention LOGIN PASSWORD 'dev_retention_password';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app_readonly_ops') THEN
        CREATE ROLE app_readonly_ops LOGIN PASSWORD 'dev_readonly_password';
    END IF;
END
$$;

-- No role owns both schemas; migration roles carry DDL and are separate from application roles.
GRANT USAGE ON SCHEMA control TO app_api, app_orchestrator, app_readonly_ops;

-- API owns the control schema.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA control TO app_api;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA control TO app_api;

-- The orchestrator reads definitions and credentials while materialising an execution.
GRANT SELECT ON ALL TABLES IN SCHEMA control TO app_orchestrator, app_readonly_ops;

ALTER DEFAULT PRIVILEGES IN SCHEMA control
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_api;
ALTER DEFAULT PRIVILEGES IN SCHEMA control
    GRANT USAGE, SELECT ON SEQUENCES TO app_api;
ALTER DEFAULT PRIVILEGES IN SCHEMA control
    GRANT SELECT ON TABLES TO app_orchestrator, app_readonly_ops;

COMMENT ON SCHEMA control IS 'Control plane: organizations, projects, agents, workflows, credentials.';

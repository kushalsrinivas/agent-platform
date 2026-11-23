-- Local development bootstrap for the execution schema.
--
-- Runs after the control migration (the migrate job enforces order). Role creation is guarded so this file is
-- also runnable on its own during development. Table DDL and the append-only trigger on execution_events
-- arrive in Phase 03.

CREATE SCHEMA IF NOT EXISTS execution;

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

GRANT USAGE ON SCHEMA execution TO app_orchestrator, app_api, app_retention, app_readonly_ops;

-- The orchestrator is the only writer of execution state.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA execution TO app_orchestrator;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA execution TO app_orchestrator;

-- The API reads execution state for queries; it never writes it.
GRANT SELECT ON ALL TABLES IN SCHEMA execution TO app_api, app_readonly_ops;

-- Retention deletes only through its own reviewed job (Phase 04+).
GRANT SELECT, DELETE ON ALL TABLES IN SCHEMA execution TO app_retention;

ALTER DEFAULT PRIVILEGES IN SCHEMA execution
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_orchestrator;
ALTER DEFAULT PRIVILEGES IN SCHEMA execution
    GRANT USAGE, SELECT ON SEQUENCES TO app_orchestrator;
ALTER DEFAULT PRIVILEGES IN SCHEMA execution
    GRANT SELECT ON TABLES TO app_api, app_readonly_ops;
ALTER DEFAULT PRIVILEGES IN SCHEMA execution
    GRANT SELECT, DELETE ON TABLES TO app_retention;

COMMENT ON SCHEMA execution IS 'Execution state: executions, tasks, attempts, leases, events, outbox.';

-- Execution security: RLS, append-only audit, immutability of closed attempts and grants.

CREATE FUNCTION execution.touch_updated_at() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_tasks_touch_updated_at
    BEFORE UPDATE ON execution.execution_tasks
    FOR EACH ROW EXECUTE FUNCTION execution.touch_updated_at();

CREATE TRIGGER workers_touch_updated_at
    BEFORE UPDATE ON execution.workers
    FOR EACH ROW EXECUTE FUNCTION execution.touch_updated_at();

-- The orchestrator has INSERT and SELECT only, but the trigger makes the intent explicit and survives a future
-- accidental grant. Retention and the migration superuser may delete under the documented retention policy.
CREATE FUNCTION execution.enforce_event_append_only() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' AND current_user IN ('app_retention', 'postgres') THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'execution_events is append-only (current_user=%)', current_user
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

CREATE TRIGGER execution_events_append_only
    BEFORE UPDATE OR DELETE ON execution.execution_events
    FOR EACH ROW EXECUTE FUNCTION execution.enforce_event_append_only();

-- An attempt is immutable once closed. The only permitted later update is the retention redaction of result_inline.
CREATE FUNCTION execution.enforce_attempt_immutability() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF OLD.finished_at IS NOT NULL THEN
        IF (to_jsonb(NEW) - 'result_inline') IS DISTINCT FROM (to_jsonb(OLD) - 'result_inline') THEN
            RAISE EXCEPTION 'closed task_attempts are immutable (attempt %)', OLD.id
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
        IF NEW.result_inline IS NOT NULL THEN
            RAISE EXCEPTION 'closed attempts may only have result_inline cleared';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER task_attempts_immutable
    BEFORE UPDATE ON execution.task_attempts
    FOR EACH ROW EXECUTE FUNCTION execution.enforce_attempt_immutability();

ALTER TABLE execution.executions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.executions
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.execution_tasks ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.execution_tasks
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.task_dependencies ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.task_dependencies
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.task_attempts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.task_attempts
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.checkpoints ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.checkpoints
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.execution_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.execution_events
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

ALTER TABLE execution.outbox ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON execution.outbox
    USING (org_id = current_setting('app.tenant_id', true))
    WITH CHECK (org_id = current_setting('app.tenant_id', true));

-- The API reads execution state and worker metadata; it never writes execution tables and cannot see the outbox.
GRANT SELECT ON
    execution.executions,
    execution.execution_tasks,
    execution.task_dependencies,
    execution.task_attempts,
    execution.checkpoints,
    execution.execution_events,
    execution.workers
TO app_api, app_readonly_ops;

REVOKE ALL ON execution.outbox FROM app_api, app_readonly_ops;

-- The orchestrator is the only writer. The audit table is insert-only.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA execution TO app_orchestrator;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA execution TO app_orchestrator;
REVOKE UPDATE, DELETE ON execution.execution_events FROM app_orchestrator;
GRANT SELECT, INSERT ON execution.execution_events TO app_orchestrator;

-- Retention deletes by terminal time, redacts inline results and never changes terminal state.
GRANT SELECT, DELETE ON ALL TABLES IN SCHEMA execution TO app_retention;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA execution TO app_retention;
GRANT UPDATE (result_inline) ON execution.task_attempts TO app_retention;

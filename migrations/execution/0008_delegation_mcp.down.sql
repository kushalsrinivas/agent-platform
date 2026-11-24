DROP TABLE IF EXISTS execution.agent_events;
DROP TABLE IF EXISTS execution.delegations;
ALTER TABLE execution.executions
    DROP COLUMN IF EXISTS delegation_depth,
    DROP COLUMN IF EXISTS delegation_policy;

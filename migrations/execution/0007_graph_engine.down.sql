DROP INDEX IF EXISTS execution.task_dependencies_mapping_idx;
ALTER TABLE execution.task_dependencies
    DROP COLUMN IF EXISTS priority,
    DROP COLUMN IF EXISTS data_mapping;

ALTER TABLE execution.execution_tasks DROP CONSTRAINT IF EXISTS execution_tasks_check1;
ALTER TABLE execution.execution_tasks ADD CONSTRAINT execution_tasks_check1 CHECK (
    (node_type = 'agent' AND agent_version_id IS NOT NULL AND tool_version_id IS NULL)
    OR (node_type = 'tool' AND tool_version_id IS NOT NULL AND agent_version_id IS NULL)
);
ALTER TABLE execution.execution_tasks DROP CONSTRAINT IF EXISTS execution_tasks_node_type_check;
ALTER TABLE execution.execution_tasks ADD CONSTRAINT execution_tasks_node_type_check CHECK (
    node_type IN ('agent', 'tool')
);
ALTER TABLE execution.execution_tasks
    DROP COLUMN IF EXISTS control_state,
    DROP COLUMN IF EXISTS join_policy,
    DROP COLUMN IF EXISTS result_inline,
    DROP COLUMN IF EXISTS params,
    DROP COLUMN IF EXISTS resources,
    DROP COLUMN IF EXISTS output_schema,
    DROP COLUMN IF EXISTS input_from,
    DROP COLUMN IF EXISTS input_schema,
    DROP COLUMN IF EXISTS config;

DROP INDEX IF EXISTS execution.executions_replay_idx;
DROP INDEX IF EXISTS execution.executions_parent_idx;
ALTER TABLE execution.executions
    DROP CONSTRAINT IF EXISTS executions_parent_task_check,
    DROP CONSTRAINT IF EXISTS executions_replay_fk,
    DROP CONSTRAINT IF EXISTS executions_parent_fk,
    DROP COLUMN IF EXISTS replay_of_execution_id,
    DROP COLUMN IF EXISTS iteration,
    DROP COLUMN IF EXISTS parent_task_id,
    DROP COLUMN IF EXISTS parent_execution_id;

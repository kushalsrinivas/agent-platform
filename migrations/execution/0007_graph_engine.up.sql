-- Phase 09 graph engine: explicit node types, control-node state, edge mappings/priority and child executions.

ALTER TABLE execution.executions
    ADD COLUMN parent_execution_id text,
    ADD COLUMN parent_task_id text,
    ADD COLUMN iteration integer NOT NULL DEFAULT 0 CHECK (iteration >= 0),
    ADD COLUMN replay_of_execution_id text,
    ADD CONSTRAINT executions_parent_fk
        FOREIGN KEY (parent_execution_id) REFERENCES execution.executions (id) ON DELETE CASCADE,
    ADD CONSTRAINT executions_replay_fk
        FOREIGN KEY (replay_of_execution_id) REFERENCES execution.executions (id) ON DELETE SET NULL,
    ADD CONSTRAINT executions_parent_task_check CHECK (
        parent_task_id IS NULL OR length(parent_task_id) BETWEEN 1 AND 128
    );

CREATE INDEX executions_parent_idx
    ON execution.executions (parent_execution_id);
CREATE INDEX executions_replay_idx
    ON execution.executions (replay_of_execution_id)
    WHERE replay_of_execution_id IS NOT NULL;

ALTER TABLE execution.execution_tasks
    ADD COLUMN config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 131072
    ),
    ADD COLUMN input_from jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (
        jsonb_typeof(input_from) = 'array' AND octet_length(input_from::text) <= 4096
    ),
    ADD COLUMN input_schema jsonb CHECK (
        input_schema IS NULL OR jsonb_typeof(input_schema) = 'object'
    ),
    ADD COLUMN output_schema jsonb CHECK (
        output_schema IS NULL OR jsonb_typeof(output_schema) = 'object'
    ),
    ADD COLUMN result_inline jsonb CHECK (
        result_inline IS NULL OR (jsonb_typeof(result_inline) = 'object' AND octet_length(result_inline::text) <= 65536)
    ),
    ADD COLUMN params jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(params) = 'object' AND octet_length(params::text) <= 65536
    ),
    ADD COLUMN resources jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(resources) = 'object' AND octet_length(resources::text) <= 4096
    ),
    ADD COLUMN join_policy jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(join_policy) = 'object' AND octet_length(join_policy::text) <= 4096
    ),
    ADD COLUMN control_state jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(control_state) = 'object' AND octet_length(control_state::text) <= 65536
    );

ALTER TABLE execution.execution_tasks DROP CONSTRAINT execution_tasks_node_type_check;
ALTER TABLE execution.execution_tasks ADD CONSTRAINT execution_tasks_node_type_check CHECK (
    node_type IN ('start', 'end', 'agent', 'llm', 'tool', 'condition', 'transform', 'parallel',
                  'join', 'delay', 'approval', 'subworkflow', 'loop')
);

ALTER TABLE execution.execution_tasks DROP CONSTRAINT execution_tasks_check1;
ALTER TABLE execution.execution_tasks ADD CONSTRAINT execution_tasks_check1 CHECK (
    (node_type IN ('agent', 'llm') AND agent_version_id IS NOT NULL AND tool_version_id IS NULL)
    OR (node_type = 'tool' AND tool_version_id IS NOT NULL AND agent_version_id IS NULL)
    OR (node_type NOT IN ('agent', 'llm', 'tool') AND agent_version_id IS NULL AND tool_version_id IS NULL)
);

ALTER TABLE execution.task_dependencies
    ADD COLUMN data_mapping jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(data_mapping) = 'object' AND octet_length(data_mapping::text) <= 131072
    ),
    ADD COLUMN priority integer NOT NULL DEFAULT 0 CHECK (priority BETWEEN -100 AND 100);

CREATE INDEX task_dependencies_mapping_idx
    ON execution.task_dependencies (task_id)
    WHERE data_mapping <> '{}'::jsonb;

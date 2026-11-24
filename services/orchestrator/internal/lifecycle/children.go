package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/outbox"
	"github.com/agent-platform/agent-platform/internal/platform/idgen"
	"github.com/agent-platform/agent-platform/packages/contracts"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/graph"
)

// startChildExecution creates or polls the child execution for a subworkflow/loop node. The parent task stays
// pending; its control_state carries the child id, iteration and previous output. A restart resumes polling.
func (s *Service) startChildExecution(ctx context.Context, tx pgx.Tx, parent *execution, t *task,
	deferred *graph.Deferred, state map[string]any) error {
	if deferred.Child == nil {
		return errors.New("child request is missing")
	}
	config := contracts.NodeConfig{}
	if len(t.Config) > 0 {
		_ = json.Unmarshal(t.Config, &config)
	}
	childID, _ := state["child_execution_id"].(string)
	if childID == "" {
		childExecutionID, iteration, err := s.createChildExecution(ctx, tx, parent, t, deferred.Child)
		if err != nil {
			return err
		}
		state["child_execution_id"] = childExecutionID
		state["iteration"] = iteration
		return s.deferControlTask(ctx, tx, t, state, time.Now().UTC().Add(time.Second))
	}

	var childState, failureCode string
	if err := tx.QueryRow(ctx, `
		SELECT state, COALESCE(failure_code, '') FROM execution.executions
		WHERE id = $1 AND parent_task_id = $2`, childID, t.ID).Scan(&childState, &failureCode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.failChild(ctx, tx, t, "SUBWORKFLOW_FAILED", "child execution disappeared")
		}
		return err
	}
	switch childState {
	case "pending", "running", "failing", "cancel_requested":
		return s.deferControlTask(ctx, tx, t, state, time.Now().UTC().Add(time.Second))
	case "failed", "cancelled":
		code := failureCode
		if code == "" {
			code = "SUBWORKFLOW_FAILED"
		}
		return s.failChild(ctx, tx, t, code, "child execution did not succeed")
	}

	output, err := s.childOutput(ctx, tx, childID, config.OutputFrom)
	if err != nil {
		return err
	}
	lastIteration := controlIteration(t)
	if value, ok := state["iteration"].(float64); ok {
		lastIteration = int(value)
	} else if value, ok := state["iteration"].(int); ok {
		lastIteration = value
	}
	if config.Loop == nil {
		return s.completeControlTask(ctx, tx, parent, t, map[string]any{
			"execution_id": childID,
			"output":       output,
			"iteration":    lastIteration,
		})
	}
	exit := false
	if config.Loop.ExitWhen != nil {
		program, err := contracts.CompileCondition(*config.Loop.ExitWhen)
		if err != nil {
			return s.failChild(ctx, tx, t, "LOOP_EXIT_INVALID", err.Error())
		}
		variables := map[string]any{
			contracts.ExprInput:     deferred.Child.Input,
			contracts.ExprOutputs:   output,
			contracts.ExprPrevious:  output,
			contracts.ExprIteration: lastIteration,
			contracts.ExprExecution: map[string]any{"id": parent.ID, "iteration": lastIteration},
			contracts.ExprNode:      map[string]any{"id": t.NodeID, "type": t.NodeType},
		}
		exit, err = contracts.EvaluateCondition(program, variables)
		if err != nil {
			return s.failChild(ctx, tx, t, "LOOP_EXIT_INVALID", err.Error())
		}
	}
	if exit || lastIteration >= config.Loop.MaxIterations {
		return s.completeControlTask(ctx, tx, parent, t, map[string]any{
			"execution_id": childID,
			"output":       output,
			"iteration":    lastIteration,
			"iterations":   lastIteration,
			"exited":       exit,
		})
	}
	// Continue at the next iteration. The executor rebuilds the child request after the child id is cleared.
	delete(state, "child_execution_id")
	state["previous"] = output
	state["iteration"] = lastIteration
	return s.deferControlTask(ctx, tx, t, state, time.Now().UTC())
}

func (s *Service) failChild(ctx context.Context, tx pgx.Tx, t *task, code, message string) error {
	e, err := s.lockExecutionForTask(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	return s.failPendingControlTask(ctx, tx, e, t, code, message)
}

func (s *Service) deferControlTask(ctx context.Context, tx pgx.Tx, t *task, state map[string]any,
	next time.Time) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE execution.execution_tasks
		SET control_state = $2::jsonb, next_attempt_at = $3, updated_at = now()
		WHERE id = $1 AND state = 'pending'`, t.ID, encoded, next)
	return err
}

func (s *Service) createChildExecution(ctx context.Context, tx pgx.Tx, parent *execution, t *task,
	request *graph.ChildRequest) (string, int, error) {
	var depth int
	if err := tx.QueryRow(ctx, `
		WITH RECURSIVE chain AS (
			SELECT id, parent_execution_id, 1 AS depth FROM execution.executions WHERE id = $1
			UNION ALL
			SELECT e.id, e.parent_execution_id, c.depth + 1
			FROM execution.executions e JOIN chain c ON e.id = c.parent_execution_id
			WHERE c.depth < 64
		)
		SELECT COALESCE(max(depth), 1) FROM chain`, parent.ID).Scan(&depth); err != nil {
		return "", 0, err
	}
	if depth >= 32 {
		return "", 0, errors.New("subworkflow nesting exceeds the supported depth")
	}
	var workflowVersionID, status string
	var graphJSON []byte
	if err := tx.QueryRow(ctx, `
		SELECT id, status, graph FROM control.workflow_versions
		WHERE org_id = $1 AND project_id = $2 AND workflow_id = $3 AND version = $4`,
		parent.OrgID, parent.ProjectID, request.WorkflowID, request.Version).
		Scan(&workflowVersionID, &status, &graphJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, fmt.Errorf("child workflow version %s v%d was not found", request.WorkflowID, request.Version)
		}
		return "", 0, err
	}
	if status != "published" {
		return "", 0, fmt.Errorf("child workflow version is not published")
	}
	graphValue, err := contracts.ParseWorkflowGraph(graphJSON)
	if err != nil {
		return "", 0, fmt.Errorf("child workflow graph is invalid: %w", err)
	}
	iteration := request.Iteration
	if iteration < 1 {
		iteration = 1
	}
	childInput := map[string]any{}
	for key, value := range request.Input {
		childInput[key] = value
	}
	if graphValue.InputSchema != nil {
		encodedSchema, err := json.Marshal(*graphValue.InputSchema)
		if err != nil {
			return "", 0, err
		}
		if err := validateNodeInput(childInput, encodedSchema); err != nil {
			return "", 0, fmt.Errorf("child input is invalid: %w", err)
		}
	}
	inputJSON, err := json.Marshal(childInput)
	if err != nil || len(inputJSON) > maxChildInputBytes {
		return "", 0, errors.New("child input exceeds 256 KiB")
	}
	inputDigest, err := contracts.Digest(childInput)
	if err != nil {
		return "", 0, err
	}
	requestID := "child:" + t.ID + ":" + strconv.Itoa(iteration)
	requestDigest, err := contracts.Digest(struct {
		WorkflowVersionID string         `json:"workflow_version_id"`
		Input             map[string]any `json:"input"`
		Iteration         int            `json:"iteration"`
	}{workflowVersionID, childInput, iteration})
	if err != nil {
		return "", 0, err
	}
	var existingID, existingDigest, existingState string
	var existingVersion int64
	err = tx.QueryRow(ctx, `
		SELECT id, request_digest, state, state_version FROM execution.executions
		WHERE org_id = $1 AND request_id = $2`, parent.OrgID, requestID).
		Scan(&existingID, &existingDigest, &existingState, &existingVersion)
	if err == nil {
		if existingDigest != requestDigest {
			return "", 0, errors.New("child idempotency key conflict")
		}
		return existingID, iteration, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", 0, err
	}
	childID := idgen.New("exec")
	delegationPolicy := parent.DelegationPolicy
	if len(delegationPolicy) == 0 {
		delegationPolicy = json.RawMessage("{}")
	}
	priority := t.Priority
	var deadline *time.Time
	if parent.DeadlineAt != nil {
		value := *parent.DeadlineAt
		deadline = &value
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution.executions
			(id, org_id, project_id, workflow_id, workflow_version_id, state, state_version, priority,
			 request_id, request_digest, input, input_digest, created_by, deadline_at, next_event_seq,
			 parent_execution_id, parent_task_id, iteration, delegation_policy, delegation_depth)
		VALUES ($1, $2, $3, $4, $5, 'pending', 1, $6, $7, $8, $9::jsonb, $10, 'orchestrator', $11, 2,
		        $12, $13, $14, $15::jsonb, $16)`,
		childID, parent.OrgID, parent.ProjectID, request.WorkflowID, workflowVersionID, priority,
		requestID, requestDigest, inputJSON, inputDigest, deadline, parent.ID, t.ID, iteration,
		delegationPolicy, parent.DelegationDepth+1); err != nil {
		return "", 0, err
	}
	if err := s.materializeChildTasks(ctx, tx, childID, parent.OrgID, parent.ProjectID,
		graphValue, priority); err != nil {
		return "", 0, err
	}
	if err := s.insertChildCreated(ctx, tx, parent, childID, workflowVersionID,
		len(graphValue.Nodes)); err != nil {
		return "", 0, err
	}
	s.log.Info("child execution created",
		zapString("parent_execution_id", parent.ID),
		zapString("parent_task_id", t.ID),
		zapString("child_execution_id", childID),
		zapInt("iteration", iteration),
	)
	return childID, iteration, nil
}

const maxChildInputBytes = 256 << 10

func (s *Service) materializeChildTasks(ctx context.Context, tx pgx.Tx, executionID, orgID, projectID string,
	graphValue *contracts.WorkflowGraph, priority int) error {
	taskIDs := make(map[string]string, len(graphValue.Nodes))
	for _, node := range graphValue.Nodes {
		taskID := idgen.New("task")
		taskIDs[node.ID] = taskID
		retry := node.Retry
		if retry == nil && graphValue.Policy != nil {
			retry = graphValue.Policy.Retry
		}
		maxAttempts := 1
		if retry != nil && retry.MaxAttempts != nil {
			maxAttempts = *retry.MaxAttempts
		}
		retryJSON := []byte("{}")
		if retry != nil {
			retryJSON, _ = json.Marshal(retry)
		}
		timeoutSeconds := 600
		if node.TimeoutSeconds != nil {
			timeoutSeconds = *node.TimeoutSeconds
		}
		sideEffect := string(contracts.SideEffectReadOnly)
		if node.SideEffectClass != nil {
			sideEffect = string(*node.SideEffectClass)
		}
		var agentVersionID, toolVersionID *string
		var runtimeABI *int
		var runtimeDigest, provider, model *string
		switch node.Type {
		case contracts.NodeTypeAgent, contracts.NodeTypeLLM:
			var resolvedID, state string
			var abi int
			var specJSON []byte
			if err := tx.QueryRow(ctx, `
				SELECT id, status, runtime_abi, spec FROM control.agent_versions
				WHERE org_id = $1 AND project_id = $2 AND agent_id = $3 AND version = $4`,
				orgID, projectID, node.Agent.AgentID, node.Agent.Version).
				Scan(&resolvedID, &state, &abi, &specJSON); err != nil {
				return fmt.Errorf("node %q agent version: %w", node.ID, err)
			}
			if state != "published" && state != "deprecated" {
				return fmt.Errorf("node %q agent version is not published", node.ID)
			}
			agentVersionID = &resolvedID
			runtimeABI = &abi
			var spec struct {
				Model struct {
					Provider string `json:"provider"`
					Name     string `json:"name"`
				} `json:"model"`
				Runtime struct {
					ImageDigest string `json:"image_digest"`
				} `json:"runtime"`
			}
			_ = json.Unmarshal(specJSON, &spec)
			if spec.Runtime.ImageDigest != "" {
				value := spec.Runtime.ImageDigest
				runtimeDigest = &value
			}
			if spec.Model.Provider != "" {
				value := spec.Model.Provider
				provider = &value
			}
			if spec.Model.Name != "" {
				value := spec.Model.Name
				model = &value
			}
		case contracts.NodeTypeTool:
			value := node.Tool.ToolID + ":" + strconv.Itoa(node.Tool.Version)
			toolVersionID = &value
		}
		configJSON := []byte("{}")
		if node.Config != nil {
			configJSON, _ = json.Marshal(node.Config)
		}
		paramsJSON := []byte("{}")
		if node.Params != nil {
			paramsJSON, _ = json.Marshal(*node.Params)
		}
		inputFromJSON := []byte("[]")
		if node.InputFrom != nil {
			inputFromJSON, _ = json.Marshal(*node.InputFrom)
		}
		var inputSchemaJSON, outputSchemaJSON []byte
		if node.InputSchema != nil {
			inputSchemaJSON, _ = json.Marshal(*node.InputSchema)
		}
		if node.OutputSchema != nil {
			outputSchemaJSON, _ = json.Marshal(*node.OutputSchema)
		}
		resourcesJSON := []byte("{}")
		if node.Resources != nil {
			resourcesJSON, _ = json.Marshal(node.Resources)
		}
		joinJSON := []byte("{}")
		if node.Config != nil && node.Config.Join != nil {
			joinJSON, _ = json.Marshal(node.Config.Join)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO execution.execution_tasks
				(id, execution_id, org_id, node_id, node_type, agent_version_id, tool_version_id,
				 worker_pool, priority, runtime_abi, runtime_digest, provider, model,
				 state, attempt_count, max_attempts, timeout_seconds, side_effect_class,
				 retry_policy, idempotency_key, config, input_from, input_schema, output_schema, params,
				 resources, join_policy)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			        'pending', 0, $14, $15, $16, $17::jsonb, $18, $19::jsonb, $20::jsonb, $21::jsonb,
			        $22::jsonb, $23::jsonb, $24::jsonb, $25::jsonb)`,
			taskID, executionID, orgID, node.ID, string(node.Type), agentVersionID, toolVersionID,
			node.WorkerPool, priority, runtimeABI, runtimeDigest, provider, model,
			maxAttempts, timeoutSeconds, sideEffect, retryJSON, node.IdempotencyKey, configJSON,
			inputFromJSON, inputSchemaJSON, outputSchemaJSON, paramsJSON, resourcesJSON, joinJSON); err != nil {
			return err
		}
	}
	for _, edge := range graphValue.Edges {
		var whenDigest *string
		if edge.When != nil {
			value := contracts.SHA256Digest([]byte(*edge.When))
			whenDigest = &value
		}
		mappingJSON := []byte("{}")
		if edge.DataMapping != nil {
			mappingJSON, _ = json.Marshal(*edge.DataMapping)
		}
		priorityValue := 0
		if edge.Priority != nil {
			priorityValue = *edge.Priority
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO execution.task_dependencies
				(execution_id, task_id, depends_on_task_id, org_id, when_expr, when_digest, data_mapping, priority)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)`,
			executionID, taskIDs[edge.To], taskIDs[edge.From], orgID,
			edge.When, whenDigest, mappingJSON, priorityValue); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) insertChildCreated(ctx context.Context, tx pgx.Tx, parent *execution, childID,
	workflowVersionID string, nodeCount int) error {
	payload := contracts.ExecutionCreatedPayload{
		Seq: 1, WorkflowVersionID: workflowVersionID, State: "pending", StateVersion: 1,
		Counts: contracts.ExecutionCounts{Total: nodeCount, Pending: nodeCount},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO execution.execution_events
			(event_id, execution_id, org_id, seq, event_type, entity_type, entity_id,
			 actor_type, actor_id, payload)
		VALUES ($1, $2, $3, 1, 'execution.created', 'execution', $2, 'system', 'orchestrator', $4::jsonb)`,
		idgen.New("evt"), childID, parent.OrgID, encoded); err != nil {
		return err
	}
	subject := fmt.Sprintf("events.org.%s.execution.%s.created", parent.OrgID, childID)
	message, err := outbox.NewMessage(parent.OrgID, parent.ProjectID, subject, "execution.created", &childID, payload, nil)
	if err != nil {
		return err
	}
	message.CorrelationID = &childID
	if err := s.store.Insert(ctx, tx, message); err != nil {
		return err
	}
	return nil
}

func (s *Service) childOutput(ctx context.Context, tx pgx.Tx, childID string, outputFrom *[]string) (map[string]any, error) {
	rows, err := tx.Query(ctx, `
		SELECT t.node_id, COALESCE(a.result_inline, t.result_inline, '{}'::jsonb)
		FROM execution.execution_tasks t
		LEFT JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
		WHERE t.execution_id = $1 AND t.state = 'succeeded'
		ORDER BY t.node_id`, childID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := map[string]any{}
	for rows.Next() {
		var nodeID string
		var value map[string]any
		if err := rows.Scan(&nodeID, &value); err != nil {
			return nil, err
		}
		if outputFrom != nil {
			include := false
			for _, selected := range *outputFrom {
				if selected == nodeID {
					include = true
					break
				}
			}
			if !include {
				continue
			}
		}
		output[nodeID] = value
	}
	return output, rows.Err()
}

func zapString(key, value string) zap.Field  { return zap.String(key, value) }
func zapInt(key string, value int) zap.Field { return zap.Int(key, value) }

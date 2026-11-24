package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/packages/contracts"
	"github.com/agent-platform/agent-platform/services/orchestrator/internal/graph"
)

// resolveNodeInput builds the typed input object for one node from execution input, upstream outputs, edge data
// mappings and loop context. Mappings are CEL and never execute arbitrary code.
func (s *Service) resolveNodeInput(ctx context.Context, tx pgx.Tx, e *execution, t *task,
	deps dependencyState) (map[string]any, error) {
	executionInput := map[string]any{}
	if err := tx.QueryRow(ctx, `SELECT input FROM execution.executions WHERE id = $1`, e.ID).
		Scan(&executionInput); err != nil {
		return nil, err
	}
	params := decodeObject(t.Params)
	selected := map[string]any{}
	var inputFrom []string
	if len(t.InputFrom) > 0 && string(t.InputFrom) != "null" {
		_ = json.Unmarshal(t.InputFrom, &inputFrom)
	}
	if inputFrom == nil {
		for nodeID, output := range deps.inboundOutputs {
			selected[nodeID] = output
		}
	} else {
		for _, nodeID := range inputFrom {
			if output, ok := deps.outputs[nodeID]; ok {
				selected[nodeID] = output
			}
		}
	}
	iteration := e.Iteration + controlIteration(t)
	previous, _ := t.ControlState["previous"].(map[string]any)
	input := map[string]any{
		"input":     executionInput,
		"outputs":   selected,
		"params":    params,
		"iteration": iteration,
	}
	if previous != nil {
		input["previous"] = previous
	}
	variables := map[string]any{
		contracts.ExprInput:   executionInput,
		contracts.ExprOutputs: deps.outputs,
		contracts.ExprParams:  params,
		contracts.ExprExecution: map[string]any{
			"id":        e.ID,
			"iteration": iteration,
		},
		contracts.ExprIteration: iteration,
		contracts.ExprPrevious:  previous,
		contracts.ExprNode:      map[string]any{"id": t.NodeID, "type": t.NodeType},
	}
	for _, edge := range deps.edges {
		if !edge.Active || len(edge.DataMapping) == 0 {
			continue
		}
		for target, expression := range edge.DataMapping {
			program, err := contracts.CompileExpression(expression)
			if err != nil {
				return nil, fmt.Errorf("edge %s -> %s mapping %s: %w", edge.NodeID, t.NodeID, target, err)
			}
			value, err := contracts.EvaluateExpression(program, variables)
			if err != nil {
				return nil, fmt.Errorf("edge %s -> %s mapping %s: %w", edge.NodeID, t.NodeID, target, err)
			}
			setMapPath(input, target, value)
		}
	}
	return input, nil
}

// validateNodeInput enforces the node input_schema before a worker is assigned or a control node runs.
func validateNodeInput(input map[string]any, schema json.RawMessage) error {
	if len(schema) == 0 || string(schema) == "null" {
		return nil
	}
	var schemaMap map[string]any
	if err := json.Unmarshal(schema, &schemaMap); err != nil {
		return fmt.Errorf("node input_schema is invalid: %w", err)
	}
	raw, err := json.Marshal(schemaMap)
	if err != nil {
		return fmt.Errorf("node input_schema is invalid: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("node input_schema is invalid: %w", err)
	}
	const schemaID = "urn:agent-platform:node:input"
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaID, document); err != nil {
		return fmt.Errorf("node input_schema is invalid: %w", err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return fmt.Errorf("node input_schema is invalid: %w", err)
	}
	instanceRaw, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("node input is not serialisable: %w", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(instanceRaw))
	if err != nil {
		return fmt.Errorf("node input is not valid JSON: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("node input does not match input_schema: %w", err)
	}
	return nil
}

func (s *Service) dispatchControl(ctx context.Context, tx pgx.Tx, e *execution, t *task,
	deps dependencyState, input map[string]any) error {
	executor, ok := s.registry.Get(contracts.WorkflowNodeType(t.NodeType))
	if !ok {
		return s.failPendingControlTask(ctx, tx, e, t, "NODE_TYPE_UNSUPPORTED",
			fmt.Sprintf("no executor for node type %q", t.NodeType))
	}
	config := contracts.NodeConfig{}
	if len(t.Config) > 0 {
		if err := json.Unmarshal(t.Config, &config); err != nil {
			return s.failPendingControlTask(ctx, tx, e, t, "NODE_CONFIG_INVALID", "node config is invalid")
		}
	}
	previous, _ := t.ControlState["previous"].(map[string]any)
	rootInput, _ := input["input"].(map[string]any)
	nodeInput := graph.NodeInput{
		ExecutionID: e.ID,
		TaskID:      t.ID,
		NodeID:      t.NodeID,
		NodeType:    contracts.WorkflowNodeType(t.NodeType),
		Execution:   map[string]any{"id": e.ID, "iteration": e.Iteration},
		RootInput:   rootInput,
		Input:       input,
		Outputs:     deps.outputs,
		Params:      decodeObject(t.Params),
		Config:      &config,
		Iteration:   e.Iteration + controlIteration(t),
		Previous:    previous,
		State:       t.ControlState,
		Now:         time.Now().UTC(),
	}
	output, err := executor.Execute(ctx, nodeInput)
	if err != nil {
		if errors.Is(err, graph.ErrWorkerRequired) {
			return s.failPendingControlTask(ctx, tx, e, t, "NODE_TYPE_UNSUPPORTED",
				"worker node was routed to the control executor")
		}
		return s.failPendingControlTask(ctx, tx, e, t, "NODE_EXECUTION_FAILED", err.Error())
	}
	if output.Deferred == nil && len(t.OutputSchema) > 0 {
		if err := validateNodeInput(output.Output, t.OutputSchema); err != nil {
			return s.failPendingControlTask(ctx, tx, e, t, "NODE_OUTPUT_INVALID", err.Error())
		}
	}
	if output.Deferred != nil {
		state := output.Deferred.State
		if state == nil {
			state = t.ControlState
		}
		next := time.Now().UTC().Add(5 * time.Second)
		if output.Deferred.ReadyAt != nil {
			next = *output.Deferred.ReadyAt
		}
		if output.Deferred.Child != nil {
			if err := s.startChildExecution(ctx, tx, e, t, output.Deferred, state); err != nil {
				return s.failPendingControlTask(ctx, tx, e, t, "SUBWORKFLOW_FAILED", err.Error())
			}
			return nil
		}
		if t.NodeType == string(contracts.NodeTypeApproval) {
			if err := s.ensureApproval(ctx, tx, e, t, config, state); err != nil {
				return s.failPendingControlTask(ctx, tx, e, t, "APPROVAL_PERSIST_FAILED", err.Error())
			}
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			return s.failPendingControlTask(ctx, tx, e, t, "NODE_CONFIG_INVALID", "control state is not serialisable")
		}
		_, err = tx.Exec(ctx, `
			UPDATE execution.execution_tasks
			SET control_state = $2::jsonb, next_attempt_at = $3, updated_at = now()
			WHERE id = $1 AND state = 'pending'`, t.ID, encoded, next)
		return err
	}
	return s.completeControlTask(ctx, tx, e, t, output.Output)
}

func (s *Service) completeControlTask(ctx context.Context, tx pgx.Tx, e *execution, t *task,
	output map[string]any) error {
	if output == nil {
		output = map[string]any{}
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return s.failPendingControlTask(ctx, tx, e, t, "NODE_OUTPUT_INVALID", "control output is not serialisable")
	}
	from, to := "pending", "succeeded"
	version := t.StateVersion + 1
	tag, err := tx.Exec(ctx, `
		UPDATE execution.execution_tasks
		SET state = 'succeeded', state_version = state_version + 1, ended_at = now(),
		    result_inline = $2::jsonb,
		    owner_worker_id = NULL, lease_id = NULL, lease_expires_at = NULL,
		    next_attempt_at = NULL, updated_at = now()
		WHERE id = $1 AND state = 'pending'`, t.ID, encoded)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if t.NodeType == string(contracts.NodeTypeApproval) {
		if err := s.finishApproval(ctx, tx, t, output); err != nil {
			return err
		}
	}
	taskID, nodeID := t.ID, t.NodeID
	if err := s.appendEvent(ctx, tx, e, eventSpec{
		MessageType: contracts.EventTaskStateChanged,
		EntityType:  "task",
		EntityID:    taskID,
		From:        &from,
		To:          &to,
		ActorType:   "system",
		ActorID:     "orchestrator",
		Subject:     taskSubject(e, taskID, "state_changed"),
		TaskID:      &taskID,
		Payload: func(seq int64) any {
			return contracts.TaskStateChangedPayload{
				Seq: seq, TaskID: taskID, NodeID: nodeID,
				From: from, To: to, StateVersion: version,
			}
		},
	}); err != nil {
		return err
	}
	return s.evaluateCompletion(ctx, tx, e)
}

func (s *Service) failPendingControlTask(ctx context.Context, tx pgx.Tx, e *execution, t *task,
	code, message string) error {
	reason := code
	if len(message) > 500 {
		message = message[:500]
	}
	from, to := "pending", "failed"
	version := t.StateVersion + 1
	if t.NodeType == string(contracts.NodeTypeApproval) {
		state := approvalStateFromTask(t)
		if err := s.finishApproval(ctx, tx, t, state); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE execution.execution_tasks
		SET state = 'failed', state_version = state_version + 1, ended_at = now(),
		    last_failure_code = $2, dead_lettered_at = COALESCE(dead_lettered_at, now()),
		    next_attempt_at = NULL, updated_at = now()
		WHERE id = $1 AND state = 'pending'`, t.ID, code); err != nil {
		return err
	}
	taskID, nodeID := t.ID, t.NodeID
	if err := s.appendEvent(ctx, tx, e, eventSpec{
		MessageType: contracts.EventTaskStateChanged,
		EntityType:  "task",
		EntityID:    taskID,
		From:        &from,
		To:          &to,
		ActorType:   "system",
		ActorID:     "orchestrator",
		Subject:     taskSubject(e, taskID, "state_changed"),
		TaskID:      &taskID,
		Payload: func(seq int64) any {
			return contracts.TaskStateChangedPayload{
				Seq: seq, TaskID: taskID, NodeID: nodeID,
				From: from, To: to, StateVersion: version, Reason: &reason,
			}
		},
	}); err != nil {
		return err
	}
	s.log.Warn("control node failed",
		zap.String("task_id", t.ID), zap.String("node_id", t.NodeID),
		zap.String("code", code), zap.String("message", message))
	return s.failFast(ctx, tx, e, code)
}

func decodeObject(raw json.RawMessage) map[string]any {
	result := map[string]any{}
	if len(raw) == 0 || string(raw) == "null" {
		return result
	}
	_ = json.Unmarshal(raw, &result)
	return result
}

func setMapPath(target map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := target
	for index, part := range parts {
		if index == len(parts)-1 {
			current[part] = value
			return
		}
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
}

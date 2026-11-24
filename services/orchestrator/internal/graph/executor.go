// Package graph is the Go workflow engine: node executor registry and control-node semantics. Worker nodes
// (agent/llm/tool) are executed by the worker runtime; every other node is resolved inside the orchestrator.
package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/cel"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

// ErrWorkerRequired is returned by a worker-node adapter. The dispatcher then assigns the task normally.
var ErrWorkerRequired = errors.New("graph: node requires a worker")

// NodeConfig is the immutable node definition.
type NodeConfig = contracts.WorkflowNode

// NodeInput is the resolved input context for one control-node evaluation.
type NodeInput struct {
	ExecutionID string
	TaskID      string
	NodeID      string
	NodeType    contracts.WorkflowNodeType
	Execution   map[string]any
	RootInput   map[string]any
	Input       map[string]any
	Outputs     map[string]map[string]any
	Params      map[string]any
	Config      *contracts.NodeConfig
	Iteration   int
	Previous    map[string]any
	State       map[string]any
	Now         time.Time
}

// NodeOutput is either a completed control-node result or a deferred decision with persisted state.
type NodeOutput struct {
	Output   map[string]any
	Deferred *Deferred
}

// Deferred tells the dispatcher to leave the task pending. ReadyAt schedules a retry; Child asks the lifecycle
// layer to create/continue a child execution.
type Deferred struct {
	ReadyAt *time.Time
	Reason  string
	State   map[string]any
	Child   *ChildRequest
}

// ChildRequest is a subworkflow/loop body execution request.
type ChildRequest struct {
	WorkflowID string
	Version    int
	Input      map[string]any
	Iteration  int
}

// NodeExecutor validates and executes one node kind.
type NodeExecutor interface {
	Validate(config NodeConfig) error
	Execute(ctx context.Context, input NodeInput) (NodeOutput, error)
}

// Registry maps a node type to its executor. Worker nodes deliberately return ErrWorkerRequired.
type Registry struct {
	executors map[contracts.WorkflowNodeType]NodeExecutor
	cache     sync.Map // expression -> compiled program
}

// NewRegistry builds the built-in registry.
func NewRegistry() *Registry {
	registry := &Registry{executors: map[contracts.WorkflowNodeType]NodeExecutor{}}
	registry.register(contracts.NodeTypeStart, &passThroughExecutor{})
	registry.register(contracts.NodeTypeEnd, &passThroughExecutor{})
	registry.register(contracts.NodeTypeParallel, &passThroughExecutor{})
	registry.register(contracts.NodeTypeAgent, &workerExecutor{})
	registry.register(contracts.NodeTypeLLM, &workerExecutor{})
	registry.register(contracts.NodeTypeTool, &workerExecutor{})
	registry.register(contracts.NodeTypeCondition, &conditionExecutor{cache: &registry.cache})
	registry.register(contracts.NodeTypeTransform, &transformExecutor{cache: &registry.cache})
	registry.register(contracts.NodeTypeJoin, &joinExecutor{})
	registry.register(contracts.NodeTypeDelay, &delayExecutor{})
	registry.register(contracts.NodeTypeApproval, &approvalExecutor{})
	registry.register(contracts.NodeTypeSubworkflow, &subworkflowExecutor{})
	registry.register(contracts.NodeTypeLoop, &loopExecutor{})
	return registry
}

func (r *Registry) register(nodeType contracts.WorkflowNodeType, executor NodeExecutor) {
	r.executors[nodeType] = executor
}

// Get returns the executor for a node type.
func (r *Registry) Get(nodeType contracts.WorkflowNodeType) (NodeExecutor, bool) {
	executor, ok := r.executors[nodeType]
	return executor, ok
}

// Validate runs the executor's structural validation.
func (r *Registry) Validate(node contracts.WorkflowNode) error {
	executor, ok := r.Get(node.Type)
	if !ok {
		return fmt.Errorf("no executor registered for node type %q", node.Type)
	}
	return executor.Validate(node)
}

// WorkerNode reports whether the node requires a worker assignment.
func (r *Registry) WorkerNode(nodeType contracts.WorkflowNodeType) bool {
	return nodeType.WorkerNode()
}

// Expressions returns the engine's variable context.
func Expressions(input NodeInput) map[string]any {
	return map[string]any{
		contracts.ExprInput:     input.RootInput,
		contracts.ExprNodeInput: input.Input,
		contracts.ExprOutputs:   input.Outputs,
		contracts.ExprParams:    input.Params,
		contracts.ExprExecution: map[string]any{
			"id":        input.ExecutionID,
			"iteration": input.Iteration,
		},
		contracts.ExprIteration: input.Iteration,
		contracts.ExprPrevious:  input.Previous,
		contracts.ExprNode: map[string]any{
			"id":   input.NodeID,
			"type": string(input.NodeType),
		},
	}
}

type passThroughExecutor struct{}

func (e *passThroughExecutor) Validate(_ NodeConfig) error {
	return nil
}

func (e *passThroughExecutor) Execute(_ context.Context, input NodeInput) (NodeOutput, error) {
	return NodeOutput{Output: map[string]any{"node": input.NodeID, "type": string(input.NodeType)}}, nil
}

type workerExecutor struct{}

func (e *workerExecutor) Validate(config NodeConfig) error {
	if config.Type == contracts.NodeTypeTool {
		if config.Tool == nil {
			return errors.New("tool node requires a tool reference")
		}
		return nil
	}
	if config.Agent == nil {
		return fmt.Errorf("%s node requires an agent reference", config.Type)
	}
	return nil
}

func (e *workerExecutor) Execute(context.Context, NodeInput) (NodeOutput, error) {
	return NodeOutput{}, ErrWorkerRequired
}

type conditionExecutor struct {
	cache *sync.Map
}

func (e *conditionExecutor) Validate(config NodeConfig) error {
	if config.Config == nil || config.Config.Expression == nil || strings.TrimSpace(*config.Config.Expression) == "" {
		return errors.New("condition node requires config.expression")
	}
	return nil
}

func (e *conditionExecutor) Execute(_ context.Context, input NodeInput) (NodeOutput, error) {
	if input.Config == nil || input.Config.Expression == nil {
		return NodeOutput{}, errors.New("condition node requires config.expression")
	}
	program, err := compileCached(e.cache, *input.Config.Expression, true)
	if err != nil {
		return NodeOutput{}, err
	}
	result, err := contracts.EvaluateCondition(program, Expressions(input))
	if err != nil {
		return NodeOutput{}, err
	}
	return NodeOutput{Output: map[string]any{"result": result}}, nil
}

type transformExecutor struct {
	cache *sync.Map
}

func (e *transformExecutor) Validate(config NodeConfig) error {
	if config.Config == nil || config.Config.Mapping == nil || len(*config.Config.Mapping) == 0 {
		return errors.New("transform node requires config.mapping")
	}
	return nil
}

func (e *transformExecutor) Execute(_ context.Context, input NodeInput) (NodeOutput, error) {
	if input.Config == nil || input.Config.Mapping == nil {
		return NodeOutput{}, errors.New("transform node requires config.mapping")
	}
	output := map[string]any{}
	for target, expression := range *input.Config.Mapping {
		program, err := compileCached(e.cache, expression, false)
		if err != nil {
			return NodeOutput{}, fmt.Errorf("transform %s: %w", target, err)
		}
		value, err := contracts.EvaluateExpression(program, Expressions(input))
		if err != nil {
			return NodeOutput{}, fmt.Errorf("transform %s: %w", target, err)
		}
		setPath(output, target, value)
	}
	return NodeOutput{Output: output}, nil
}

type joinExecutor struct{}

func (e *joinExecutor) Validate(config NodeConfig) error {
	if config.Config == nil || config.Config.Join == nil {
		return errors.New("join node requires config.join")
	}
	return nil
}

func (e *joinExecutor) Execute(_ context.Context, input NodeInput) (NodeOutput, error) {
	branches := make(map[string]any, len(input.Outputs))
	for nodeID, output := range input.Outputs {
		branches[nodeID] = output
	}
	return NodeOutput{Output: map[string]any{"branches": branches}}, nil
}

type delayExecutor struct{}

func (e *delayExecutor) Validate(config NodeConfig) error {
	if config.Config == nil || config.Config.Delay == nil {
		return errors.New("delay node requires config.delay")
	}
	return nil
}

func (e *delayExecutor) Execute(_ context.Context, input NodeInput) (NodeOutput, error) {
	if input.Config == nil || input.Config.Delay == nil {
		return NodeOutput{}, errors.New("delay node requires config.delay")
	}
	state := cloneState(input.State)
	now := input.Now
	if state["ready_at"] == nil {
		delay := input.Config.Delay
		switch {
		case delay.Seconds != nil:
			ready := now.Add(time.Duration(*delay.Seconds) * time.Second)
			state["ready_at"] = ready.UTC().Format(time.RFC3339Nano)
		case delay.Until != nil:
			ready, err := time.Parse(time.RFC3339, *delay.Until)
			if err != nil {
				return NodeOutput{}, fmt.Errorf("delay until is invalid: %w", err)
			}
			state["ready_at"] = ready.UTC().Format(time.RFC3339Nano)
		case delay.Event != nil:
			state["waiting_for"] = *delay.Event
		}
	}
	if signal, ok := state["signal"]; ok {
		return NodeOutput{Output: map[string]any{"event": state["waiting_for"], "payload": signal}}, nil
	}
	readyRaw, _ := state["ready_at"].(string)
	if readyRaw == "" {
		return NodeOutput{Deferred: &Deferred{ReadyAt: timePtr(now.Add(time.Second)), Reason: "event", State: state}}, nil
	}
	ready, err := time.Parse(time.RFC3339Nano, readyRaw)
	if err != nil {
		return NodeOutput{}, fmt.Errorf("delay ready_at is invalid: %w", err)
	}
	if now.Before(ready) {
		return NodeOutput{Deferred: &Deferred{ReadyAt: &ready, Reason: "delay", State: state}}, nil
	}
	return NodeOutput{Output: map[string]any{"waited_until": ready.UTC().Format(time.RFC3339Nano)}}, nil
}

type approvalExecutor struct{}

func (e *approvalExecutor) Validate(_ NodeConfig) error {
	return nil
}

func (e *approvalExecutor) Execute(_ context.Context, input NodeInput) (NodeOutput, error) {
	state := cloneState(input.State)
	config := approvalConfig(input)
	status, _ := state["status"].(string)
	if status == "" {
		state["status"] = "pending"
		state["requested_at"] = input.Now.UTC().Format(time.RFC3339Nano)
		state["policy"] = config.policy
		state["approvers"] = config.approvers
		state["decisions"] = map[string]any{}
		if config.expiresIn > 0 {
			state["expires_at"] = input.Now.Add(time.Duration(config.expiresIn) * time.Second).
				UTC().Format(time.RFC3339Nano)
		}
		return NodeOutput{Deferred: &Deferred{
			ReadyAt: timePtr(input.Now.Add(5 * time.Second)), Reason: "approval", State: state,
		}}, nil
	}
	if status == "pending" {
		if raw, _ := state["expires_at"].(string); raw != "" {
			if expiresAt, err := time.Parse(time.RFC3339Nano, raw); err == nil && !input.Now.Before(expiresAt) {
				state["status"] = "expired"
				state["by"] = "system"
				state["comment"] = "approval expired"
				state["reason"] = "expired"
				state["at"] = input.Now.UTC().Format(time.RFC3339Nano)
				status = "expired"
			}
		}
	}
	switch status {
	case "approved":
		return NodeOutput{Output: approvalOutput(state, true)}, nil
	case "rejected", "expired":
		output := approvalOutput(state, false)
		if config.onReject == "fail" {
			return NodeOutput{}, fmt.Errorf("approval %s by %v", status, state["by"])
		}
		return NodeOutput{Output: output}, nil
	default:
		return NodeOutput{Deferred: &Deferred{
			ReadyAt: timePtr(input.Now.Add(5 * time.Second)), Reason: "approval", State: state,
		}}, nil
	}
}

type approvalSettings struct {
	onReject  string
	policy    string
	approvers []string
	expiresIn int
}

func approvalConfig(input NodeInput) approvalSettings {
	settings := approvalSettings{onReject: "fail", policy: "any"}
	if input.Config == nil || input.Config.Approval == nil {
		return settings
	}
	config := input.Config.Approval
	if config.OnReject != nil {
		settings.onReject = *config.OnReject
	}
	if config.Policy != nil {
		settings.policy = *config.Policy
	}
	if config.Approvers != nil {
		settings.approvers = append([]string{}, config.Approvers...)
	}
	if config.ExpiresInSeconds != nil {
		settings.expiresIn = *config.ExpiresInSeconds
	}
	return settings
}

func approvalOutput(state map[string]any, approved bool) map[string]any {
	return map[string]any{
		"approved": approved, "status": state["status"], "by": state["by"],
		"comment": state["comment"], "reason": state["reason"], "at": state["at"],
		"decisions": state["decisions"], "expired": state["status"] == "expired",
	}
}

type subworkflowExecutor struct{}

func (e *subworkflowExecutor) Validate(config NodeConfig) error {
	if config.Config == nil || config.Config.Workflow == nil {
		return errors.New("subworkflow node requires config.workflow")
	}
	return nil
}

func (e *subworkflowExecutor) Execute(_ context.Context, input NodeInput) (NodeOutput, error) {
	if input.Config == nil || input.Config.Workflow == nil {
		return NodeOutput{}, errors.New("subworkflow node requires config.workflow")
	}
	childInput, err := evaluateMapping(input, input.Config.Input)
	if err != nil {
		return NodeOutput{}, err
	}
	return NodeOutput{Deferred: &Deferred{
		ReadyAt: timePtr(input.Now.Add(time.Second)),
		Reason:  "child_execution",
		Child: &ChildRequest{
			WorkflowID: input.Config.Workflow.WorkflowID,
			Version:    input.Config.Workflow.Version,
			Input:      childInput,
		},
	}}, nil
}

type loopExecutor struct{}

func (e *loopExecutor) Validate(config NodeConfig) error {
	if config.Config == nil || config.Config.Workflow == nil || config.Config.Loop == nil {
		return errors.New("loop node requires config.workflow and config.loop")
	}
	return nil
}

func (e *loopExecutor) Execute(_ context.Context, input NodeInput) (NodeOutput, error) {
	if input.Config == nil || input.Config.Workflow == nil || input.Config.Loop == nil {
		return NodeOutput{}, errors.New("loop node requires config.workflow and config.loop")
	}
	childInput, err := evaluateMapping(input, input.Config.Input)
	if err != nil {
		return NodeOutput{}, err
	}
	return NodeOutput{Deferred: &Deferred{
		ReadyAt: timePtr(input.Now.Add(time.Second)),
		Reason:  "loop_child",
		Child: &ChildRequest{
			WorkflowID: input.Config.Workflow.WorkflowID,
			Version:    input.Config.Workflow.Version,
			Input:      childInput,
			Iteration:  input.Iteration + 1,
		},
	}}, nil
}

func evaluateMapping(input NodeInput, mapping *map[string]string) (map[string]any, error) {
	if mapping == nil || len(*mapping) == 0 {
		result := map[string]any{}
		source := input.Input
		if root, ok := input.Input["input"].(map[string]any); ok {
			source = root
		}
		for key, value := range source {
			result[key] = value
		}
		return result, nil
	}
	result := map[string]any{}
	for target, expression := range *mapping {
		program, err := compileCached(nil, expression, false)
		if err != nil {
			return nil, fmt.Errorf("input mapping %s: %w", target, err)
		}
		value, err := contracts.EvaluateExpression(program, Expressions(input))
		if err != nil {
			return nil, fmt.Errorf("input mapping %s: %w", target, err)
		}
		setPath(result, target, value)
	}
	return result, nil
}

func compileCached(cache *sync.Map, expression string, condition bool) (cel.Program, error) {
	key := expression
	if condition {
		key = "?" + expression
	}
	if cache != nil {
		if value, ok := cache.Load(key); ok {
			return value.(cel.Program), nil
		}
	}
	var program cel.Program
	var err error
	if condition {
		program, err = contracts.CompileCondition(expression)
	} else {
		program, err = contracts.CompileExpression(expression)
	}
	if err != nil {
		return nil, err
	}
	if cache != nil {
		cache.Store(key, program)
	}
	return program, nil
}

func cloneState(state map[string]any) map[string]any {
	result := make(map[string]any, len(state))
	for key, value := range state {
		result[key] = value
	}
	return result
}

func setPath(target map[string]any, path string, value any) {
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

func timePtr(value time.Time) *time.Time { return &value }

package contracts

import (
	"strings"
	"time"
	"unicode/utf8"
)

var retryOnValues = []string{"transient", "timeout", "worker_lost", "result_invalid", "cancelled", "any"}

// Validate enforces the graph contract: nodes, edges, DAG shape, input references and node policies.
func (g *WorkflowGraph) Validate() error {
	v := &validator{}
	validateGraph(v, g)
	return v.err()
}

// ParseWorkflowGraph strictly decodes and validates a persisted workflow graph.
func ParseWorkflowGraph(data []byte) (*WorkflowGraph, error) {
	var graph WorkflowGraph
	if err := parseStrict(data, &graph); err != nil {
		return nil, err
	}
	if err := graph.Validate(); err != nil {
		return nil, err
	}
	return &graph, nil
}

// Validate enforces the full document contract. It reports every problem it finds in one pass.
func (d *WorkflowDocument) Validate() error {
	v := &validator{}

	if d.SchemaVersion != 1 {
		v.addf("schema_version must be 1")
	}
	v.requiredString("workflow_id", d.WorkflowID, 1, 128)
	v.pattern("workflow_id", d.WorkflowID, workflowIDPattern)
	v.requiredString("name", d.Name, 1, 120)
	if d.Description != nil && utf8.RuneCountInString(*d.Description) > 2000 {
		v.addf("description must be at most 2000 characters")
	}

	validateGraph(v, &d.WorkflowGraph)

	return v.err()
}

func validateGraph(v *validator, g *WorkflowGraph) {
	if g.Nodes == nil {
		v.addf("nodes is required")
	}
	if g.Edges == nil {
		v.addf("edges is required (use [] for a single-node workflow)")
	}
	if len(g.Nodes) == 0 {
		v.addf("at least one node is required")
	}
	if len(g.Nodes) > 500 {
		v.addf("at most 500 nodes are allowed")
	}
	if len(g.Edges) > 2000 {
		v.addf("at most 2000 edges are allowed")
	}

	if g.Policy != nil {
		if g.Policy.OnFailure != nil {
			v.enum("policy.on_failure", *g.Policy.OnFailure, "fail_fast", "continue")
		}
		if g.Policy.Retry != nil {
			v.validateRetry("policy.retry", g.Policy.Retry)
		}
	}

	nodeIndex := make(map[string]WorkflowNode, len(g.Nodes))
	adjacency := make(map[string][]string, len(g.Nodes))
	reverse := make(map[string][]string, len(g.Nodes))
	indegree := make(map[string]int, len(g.Nodes))

	for i := range g.Nodes {
		node := &g.Nodes[i]
		field := "nodes[" + itoa(i) + "]"
		v.requiredString(field+".id", node.ID, 1, 64)
		v.pattern(field+".id", node.ID, nodeIDPattern)
		if _, exists := nodeIndex[node.ID]; exists {
			v.addf("duplicate node id %q", node.ID)
		}
		nodeIndex[node.ID] = *node
		indegree[node.ID] = 0
		var defaultRetry *RetryPolicy
		if g.Policy != nil {
			defaultRetry = g.Policy.Retry
		}
		v.validateNode(field, node, defaultRetry)
	}

	seenEdges := make(map[string]bool, len(g.Edges))
	for i := range g.Edges {
		edge := &g.Edges[i]
		field := "edges[" + itoa(i) + "]"
		v.requiredString(field+".from", edge.From, 1, 64)
		v.requiredString(field+".to", edge.To, 1, 64)
		v.pattern(field+".from", edge.From, nodeIDPattern)
		v.pattern(field+".to", edge.To, nodeIDPattern)
		if edge.From == edge.To {
			v.addf("%s is a self edge", field)
		}
		if _, ok := nodeIndex[edge.From]; !ok {
			v.addf("%s references unknown source node %q", field, edge.From)
		}
		if _, ok := nodeIndex[edge.To]; !ok {
			v.addf("%s references unknown target node %q", field, edge.To)
		}
		key := edge.From + "\x00" + edge.To
		if seenEdges[key] {
			v.addf("duplicate edge %s -> %s", edge.From, edge.To)
		}
		seenEdges[key] = true
		if edge.When != nil {
			if utf8.RuneCountInString(*edge.When) > 2000 {
				v.addf("%s.when must be at most 2000 characters", field)
			}
			if _, err := CompileCondition(*edge.When); err != nil {
				v.addf("%s.when: %v", field, err)
			}
		}
		if edge.Priority != nil && (*edge.Priority < -100 || *edge.Priority > 100) {
			v.addf("%s.priority must be between -100 and 100", field)
		}
		if edge.DataMapping != nil {
			for target, expression := range *edge.DataMapping {
				if !validDataPath(target) {
					v.addf("%s.data_mapping target %q is not a valid dotted path", field, target)
				}
				if strings.TrimSpace(expression) == "" {
					v.addf("%s.data_mapping[%q] expression is empty", field, target)
					continue
				}
				if _, err := CompileExpression(expression); err != nil {
					v.addf("%s.data_mapping[%q]: %v", field, target, err)
				}
			}
		}
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		reverse[edge.To] = append(reverse[edge.To], edge.From)
		indegree[edge.To]++
	}

	// Kahn's algorithm: every node must be emitted or the graph contains a cycle.
	queue := make([]string, 0, len(g.Nodes))
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adjacency[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(g.Nodes) {
		v.addf("workflow graph contains a cycle")
	}

	validateEntryAndTermination(v, g, adjacency, reverse, nodeIndex)

	// Join thresholds depend on the number of inbound edges.
	for i := range g.Nodes {
		node := &g.Nodes[i]
		if node.Type != NodeTypeJoin || node.Config == nil || node.Config.Join == nil {
			continue
		}
		join := node.Config.Join
		if join.Mode == "successful" {
			inbound := len(reverse[node.ID])
			if join.Successful == nil {
				v.addf("node %q join.successful is required when mode=successful", node.ID)
			} else if *join.Successful < 1 || *join.Successful > inbound {
				v.addf("node %q join.successful must be between 1 and %d", node.ID, inbound)
			}
		}
	}

	for i := range g.Nodes {
		node := &g.Nodes[i]
		if node.InputFrom == nil {
			continue
		}
		ancestors := reachableAncestors(node.ID, reverse)
		seen := make(map[string]bool, len(*node.InputFrom))
		for _, ref := range *node.InputFrom {
			if seen[ref] {
				v.addf("node %q has duplicate input_from reference %q", node.ID, ref)
			}
			seen[ref] = true
			if ref == node.ID {
				v.addf("node %q cannot read its own output", node.ID)
			}
			if _, ok := nodeIndex[ref]; !ok {
				v.addf("node %q input_from references unknown node %q", node.ID, ref)
			} else if !ancestors[ref] {
				v.addf("node %q input_from references non-upstream node %q", node.ID, ref)
			}
		}
		if len(*node.InputFrom) > 16 {
			v.addf("node %q may select at most 16 upstream outputs", node.ID)
		}
	}
}

func (v *validator) validateNode(field string, node *WorkflowNode, defaultRetry *RetryPolicy) {
	if !validNodeType(node.Type) {
		v.addf("%s.type %q is not supported", field, node.Type)
		return
	}
	worker := node.Type.WorkerNode()
	if node.Type == NodeTypeAgent || node.Type == NodeTypeLLM {
		if node.Agent == nil {
			v.addf("%s.agent is required when type=%s", field, node.Type)
		}
		if node.Tool != nil {
			v.addf("%s.tool is not allowed when type=%s", field, node.Type)
		}
	} else if node.Type == NodeTypeTool {
		if node.Tool == nil {
			v.addf("%s.tool is required when type=tool", field)
		}
		if node.Agent != nil {
			v.addf("%s.agent is not allowed when type=tool", field)
		}
	} else if node.Agent != nil || node.Tool != nil {
		v.addf("%s.agent/tool are only allowed on agent, llm and tool nodes", field)
	}
	if node.Agent != nil {
		v.pattern(field+".agent.agent_id", node.Agent.AgentID, agentIDPattern)
		if node.Agent.Version < 1 {
			v.addf("%s.agent.version must be at least 1", field)
		}
	}
	if node.Tool != nil {
		v.requiredString(field+".tool.tool_id", node.Tool.ToolID, 1, 128)
		if node.Tool.Version < 1 {
			v.addf("%s.tool.version must be at least 1", field)
		}
	}
	if node.WorkerPool != nil {
		v.requiredString(field+".worker_pool", *node.WorkerPool, 1, 64)
		if !worker {
			v.addf("%s.worker_pool is only meaningful on worker nodes", field)
		}
	}
	if node.TimeoutSeconds != nil && (*node.TimeoutSeconds < 1 || *node.TimeoutSeconds > 86400) {
		v.addf("%s.timeout_seconds must be between 1 and 86400", field)
	}
	if node.SideEffectClass != nil {
		v.enum(field+".side_effect_class", string(*node.SideEffectClass),
			string(SideEffectReadOnly), string(SideEffectIdempotent), string(SideEffectNonIdempotent))
	}
	if node.IdempotencyKey != nil {
		v.requiredString(field+".idempotency_key", *node.IdempotencyKey, 1, 500)
	}
	if node.CheckpointMode != nil {
		v.enum(field+".checkpoint_mode", *node.CheckpointMode,
			CheckpointModeNone, CheckpointModeOnNodeEnd, CheckpointModeInherit)
		if !worker {
			v.addf("%s.checkpoint_mode is only meaningful on worker nodes", field)
		}
	}
	if node.RequireCheckpoint != nil && *node.RequireCheckpoint && node.CheckpointMode != nil &&
		*node.CheckpointMode == CheckpointModeNone {
		v.addf("%s.require_checkpoint cannot be true when checkpoint_mode=none", field)
	}
	if node.Retry != nil {
		v.validateRetry(field+".retry", node.Retry)
	}
	if node.Resources != nil {
		if node.Resources.CPU != nil && (*node.Resources.CPU <= 0 || *node.Resources.CPU > 1024) {
			v.addf("%s.resources.cpu_cores must be between 0 and 1024", field)
		}
		if node.Resources.MemoryMB != nil && (*node.Resources.MemoryMB < 16 || *node.Resources.MemoryMB > 1<<20) {
			v.addf("%s.resources.memory_mb must be between 16 and 1048576", field)
		}
	}
	v.validateNodeConfig(field, node)

	// ADR 0006: non-idempotent work is never retried without an idempotency key.
	maxAttempts := 1
	if defaultRetry != nil && defaultRetry.MaxAttempts != nil {
		maxAttempts = *defaultRetry.MaxAttempts
	}
	if node.Retry != nil && node.Retry.MaxAttempts != nil {
		maxAttempts = *node.Retry.MaxAttempts
	}
	sideEffect := SideEffectReadOnly
	if node.SideEffectClass != nil {
		sideEffect = *node.SideEffectClass
	}
	if sideEffect == SideEffectNonIdempotent && maxAttempts > 1 && node.IdempotencyKey == nil {
		v.addf("%s: non_idempotent nodes require idempotency_key when max_attempts > 1", field)
	}
}

func (v *validator) validateNodeConfig(field string, node *WorkflowNode) {
	config := node.Config
	if config == nil {
		if requiresConfig(node.Type) {
			v.addf("%s.config is required when type=%s", field, node.Type)
		}
		return
	}
	switch node.Type {
	case NodeTypeStart, NodeTypeEnd, NodeTypeParallel:
		if config.Expression != nil || config.Mapping != nil || config.Join != nil || config.Delay != nil ||
			config.Approval != nil || config.Workflow != nil || config.Loop != nil || config.Input != nil ||
			config.OutputFrom != nil {
			v.addf("%s.config is not allowed when type=%s", field, node.Type)
		}
	case NodeTypeCondition:
		if config.Expression == nil || strings.TrimSpace(*config.Expression) == "" {
			v.addf("%s.config.expression is required when type=condition", field)
		} else if _, err := CompileCondition(*config.Expression); err != nil {
			v.addf("%s.config.expression: %v", field, err)
		}
	case NodeTypeTransform:
		if config.Mapping == nil || len(*config.Mapping) == 0 {
			v.addf("%s.config.mapping is required when type=transform", field)
		} else {
			for target, expression := range *config.Mapping {
				if !validDataPath(target) {
					v.addf("%s.config.mapping target %q is not a valid dotted path", field, target)
				}
				if strings.TrimSpace(expression) == "" {
					v.addf("%s.config.mapping[%q] expression is empty", field, target)
					continue
				}
				if _, err := CompileExpression(expression); err != nil {
					v.addf("%s.config.mapping[%q]: %v", field, target, err)
				}
			}
		}
	case NodeTypeJoin:
		if config.Join == nil {
			v.addf("%s.config.join is required when type=join", field)
			return
		}
		v.enum(field+".config.join.mode", config.Join.Mode, "all", "successful")
		v.enumDefault(field+".config.join.on_partial", config.Join.OnPartial, "fail", "skip", "continue")
		if config.Join.Mode == "successful" {
			if config.Join.Successful == nil {
				v.addf("%s.config.join.successful is required when mode=successful", field)
			} else if *config.Join.Successful < 1 || *config.Join.Successful > 500 {
				v.addf("%s.config.join.successful must be between 1 and 500", field)
			}
		}
	case NodeTypeDelay:
		if config.Delay == nil {
			v.addf("%s.config.delay is required when type=delay", field)
			return
		}
		set := 0
		if config.Delay.Seconds != nil {
			set++
			if *config.Delay.Seconds < 1 || *config.Delay.Seconds > 604800 {
				v.addf("%s.config.delay.seconds must be between 1 and 604800", field)
			}
		}
		if config.Delay.Until != nil {
			set++
			when, err := time.Parse(time.RFC3339, *config.Delay.Until)
			if err != nil {
				v.addf("%s.config.delay.until must be an RFC3339 timestamp", field)
			} else if when.Before(time.Now().UTC().Add(-24 * time.Hour)) {
				v.addf("%s.config.delay.until is implausibly old", field)
			}
		}
		if config.Delay.Event != nil {
			set++
			v.requiredString(field+".config.delay.event", *config.Delay.Event, 1, 200)
		}
		if set != 1 {
			v.addf("%s.config.delay must set exactly one of seconds, until or event", field)
		}
	case NodeTypeApproval:
		if config.Approval != nil {
			if config.Approval.OnReject != nil {
				v.enumDefault(field+".config.approval.on_reject", *config.Approval.OnReject, "fail", "skip", "continue")
			}
			if config.Approval.Policy != nil {
				v.enumDefault(field+".config.approval.policy", *config.Approval.Policy, "any", "all")
				if *config.Approval.Policy == "all" && len(config.Approval.Approvers) == 0 {
					v.addf("%s.config.approval.approvers is required when policy=all", field)
				}
			}
			if len(config.Approval.Approvers) > 20 {
				v.addf("%s.config.approval.approvers may contain at most 20 principals", field)
			}
			seen := map[string]bool{}
			for i, approver := range config.Approval.Approvers {
				if strings.TrimSpace(approver) == "" || len(approver) > 120 {
					v.addf("%s.config.approval.approvers[%d] must be 1..120 characters", field, i)
				}
				if seen[approver] {
					v.addf("%s.config.approval.approvers[%d] is duplicated", field, i)
				}
				seen[approver] = true
			}
			if config.Approval.ExpiresInSeconds != nil &&
				(*config.Approval.ExpiresInSeconds < 1 || *config.Approval.ExpiresInSeconds > 604800) {
				v.addf("%s.config.approval.expires_in_seconds must be between 1 and 604800", field)
			}
		}
	case NodeTypeSubworkflow:
		validateSubworkflow(v, field, config, false)
	case NodeTypeLoop:
		validateSubworkflow(v, field, config, true)
	}
}

func validateSubworkflow(v *validator, field string, config *NodeConfig, loop bool) {
	if config.Workflow == nil {
		v.addf("%s.config.workflow is required", field)
	} else {
		v.pattern(field+".config.workflow.workflow_id", config.Workflow.WorkflowID, workflowIDPattern)
		if config.Workflow.Version < 1 {
			v.addf("%s.config.workflow.version must be at least 1", field)
		}
	}
	if config.Input != nil {
		for target, expression := range *config.Input {
			if !validDataPath(target) {
				v.addf("%s.config.input target %q is not a valid dotted path", field, target)
			}
			if strings.TrimSpace(expression) == "" {
				v.addf("%s.config.input[%q] expression is empty", field, target)
				continue
			}
			if _, err := CompileExpression(expression); err != nil {
				v.addf("%s.config.input[%q]: %v", field, target, err)
			}
		}
	}
	if config.OutputFrom != nil {
		for _, nodeID := range *config.OutputFrom {
			if !nodeIDPattern.MatchString(nodeID) {
				v.addf("%s.config.output_from contains invalid node id %q", field, nodeID)
			}
		}
	}
	if loop {
		if config.Loop == nil {
			v.addf("%s.config.loop is required when type=loop", field)
			return
		}
		if config.Loop.MaxIterations < 1 || config.Loop.MaxIterations > 100 {
			v.addf("%s.config.loop.max_iterations must be between 1 and 100", field)
		}
		if config.Loop.ExitWhen != nil {
			if _, err := CompileCondition(*config.Loop.ExitWhen); err != nil {
				v.addf("%s.config.loop.exit_when: %v", field, err)
			}
		}
	} else if config.Loop != nil {
		v.addf("%s.config.loop is only allowed when type=loop", field)
	}
}

func validNodeType(nodeType WorkflowNodeType) bool {
	switch nodeType {
	case NodeTypeStart, NodeTypeEnd, NodeTypeAgent, NodeTypeLLM, NodeTypeTool, NodeTypeCondition,
		NodeTypeTransform, NodeTypeParallel, NodeTypeJoin, NodeTypeDelay, NodeTypeApproval,
		NodeTypeSubworkflow, NodeTypeLoop:
		return true
	default:
		return false
	}
}

func requiresConfig(nodeType WorkflowNodeType) bool {
	switch nodeType {
	case NodeTypeCondition, NodeTypeTransform, NodeTypeJoin, NodeTypeDelay, NodeTypeSubworkflow, NodeTypeLoop:
		return true
	default:
		return false
	}
}

func validDataPath(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for index, char := range part {
			if index == 0 && !isIdentifierStart(char) {
				return false
			}
			if index > 0 && !isIdentifierPart(char) {
				return false
			}
		}
	}
	return true
}

func isIdentifierStart(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_'
}

func isIdentifierPart(char rune) bool {
	return isIdentifierStart(char) || (char >= '0' && char <= '9')
}

func validateEntryAndTermination(v *validator, g *WorkflowGraph, adjacency, reverse map[string][]string,
	nodeIndex map[string]WorkflowNode) {
	var starts, ends []string
	for _, node := range g.Nodes {
		switch node.Type {
		case NodeTypeStart:
			starts = append(starts, node.ID)
			if len(reverse[node.ID]) != 0 {
				v.addf("start node %q must not have incoming edges", node.ID)
			}
		case NodeTypeEnd:
			ends = append(ends, node.ID)
			if len(adjacency[node.ID]) != 0 {
				v.addf("end node %q must not have outgoing edges", node.ID)
			}
		}
	}
	if len(starts) > 1 {
		v.addf("at most one start node is allowed")
	}
	if len(ends) > 1 {
		v.addf("at most one end node is allowed")
	}
	roots := 0
	for _, node := range g.Nodes {
		if len(reverse[node.ID]) == 0 {
			roots++
		}
	}
	if roots == 0 {
		v.addf("workflow graph has no entry point")
	}
	if len(starts) == 1 {
		reachable := reachableSet(starts[0], adjacency)
		for _, node := range g.Nodes {
			if !reachable[node.ID] {
				v.addf("node %q is not reachable from start node %q", node.ID, starts[0])
			}
		}
	}
	if len(ends) == 1 {
		reachesEnd := reverseReachable(ends[0], reverse)
		for _, node := range g.Nodes {
			if node.ID != ends[0] && !reachesEnd[node.ID] {
				v.addf("node %q cannot reach end node %q", node.ID, ends[0])
			}
		}
	}
	_ = nodeIndex
}

func reachableSet(start string, adjacency map[string][]string) map[string]bool {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[current] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen
}

func reverseReachable(target string, reverse map[string][]string) map[string]bool {
	seen := map[string]bool{target: true}
	queue := []string{target}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, previous := range reverse[current] {
			if !seen[previous] {
				seen[previous] = true
				queue = append(queue, previous)
			}
		}
	}
	return seen
}

func (v *validator) validateRetry(field string, retry *RetryPolicy) {
	if retry.MaxAttempts != nil && (*retry.MaxAttempts < 1 || *retry.MaxAttempts > 10) {
		v.addf("%s.max_attempts must be between 1 and 10", field)
	}
	if retry.BackoffBaseMS != nil && (*retry.BackoffBaseMS < 100 || *retry.BackoffBaseMS > 60000) {
		v.addf("%s.backoff_base_ms must be between 100 and 60000", field)
	}
	if retry.BackoffMaxMS != nil && (*retry.BackoffMaxMS < 100 || *retry.BackoffMaxMS > 600000) {
		v.addf("%s.backoff_max_ms must be between 100 and 600000", field)
	}
	if retry.BackoffBaseMS != nil && retry.BackoffMaxMS != nil && *retry.BackoffBaseMS > *retry.BackoffMaxMS {
		v.addf("%s.backoff_base_ms cannot exceed backoff_max_ms", field)
	}
	if retry.RetryOn != nil {
		seen := make(map[string]bool, len(*retry.RetryOn))
		for _, value := range *retry.RetryOn {
			v.enum(field+".retry_on", value, retryOnValues...)
			if seen[value] {
				v.addf("%s.retry_on contains duplicate %q", field, value)
			}
			seen[value] = true
		}
	}
}

func reachableAncestors(nodeID string, reverse map[string][]string) map[string]bool {
	seen := make(map[string]bool)
	queue := append([]string(nil), reverse[nodeID]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		queue = append(queue, reverse[current]...)
	}
	return seen
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}

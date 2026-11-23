package contracts

import (
	"unicode/utf8"
)

var retryOnValues = []string{"transient", "timeout", "worker_lost", "cancelled", "any"}

// Validate enforces the workflow contract. It reports every problem it finds in one pass.
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
	if d.Nodes == nil {
		v.addf("nodes is required")
	}
	if d.Edges == nil {
		v.addf("edges is required (use [] for a single-node workflow)")
	}
	if len(d.Nodes) == 0 {
		v.addf("at least one node is required")
	}
	if len(d.Nodes) > 500 {
		v.addf("at most 500 nodes are allowed")
	}
	if len(d.Edges) > 2000 {
		v.addf("at most 2000 edges are allowed")
	}

	if d.Policy != nil && d.Policy.OnFailure != nil {
		v.enum("policy.on_failure", *d.Policy.OnFailure, "fail_fast", "continue")
	}

	nodeIndex := make(map[string]WorkflowNode, len(d.Nodes))
	adjacency := make(map[string][]string, len(d.Nodes))
	reverse := make(map[string][]string, len(d.Nodes))
	indegree := make(map[string]int, len(d.Nodes))

	for i := range d.Nodes {
		node := &d.Nodes[i]
		field := "nodes[" + itoa(i) + "]"
		v.requiredString(field+".id", node.ID, 1, 64)
		v.pattern(field+".id", node.ID, nodeIDPattern)
		if _, exists := nodeIndex[node.ID]; exists {
			v.addf("duplicate node id %q", node.ID)
		}
		nodeIndex[node.ID] = *node
		indegree[node.ID] = 0
		v.validateNode(field, node)
	}

	seenEdges := make(map[string]bool, len(d.Edges))
	for i := range d.Edges {
		edge := &d.Edges[i]
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
		if edge.When != nil && utf8.RuneCountInString(*edge.When) > 2000 {
			v.addf("%s.when must be at most 2000 characters", field)
		}
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		reverse[edge.To] = append(reverse[edge.To], edge.From)
		indegree[edge.To]++
	}

	// Kahn's algorithm: every node must be emitted or the graph contains a cycle.
	queue := make([]string, 0, len(d.Nodes))
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
	if visited != len(d.Nodes) {
		v.addf("workflow graph contains a cycle")
	}

	for i := range d.Nodes {
		node := &d.Nodes[i]
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

	return v.err()
}

func (v *validator) validateNode(field string, node *WorkflowNode) {
	if node.Type != NodeTypeAgent && node.Type != NodeTypeTool {
		v.addf("%s.type must be agent or tool", field)
	}
	if node.Type == NodeTypeAgent {
		if node.Agent == nil {
			v.addf("%s.agent is required when type=agent", field)
		}
		if node.Tool != nil {
			v.addf("%s.tool is not allowed when type=agent", field)
		}
	}
	if node.Type == NodeTypeTool {
		if node.Tool == nil {
			v.addf("%s.tool is required when type=tool", field)
		}
		if node.Agent != nil {
			v.addf("%s.agent is not allowed when type=tool", field)
		}
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
	}
	if node.RequireCheckpoint != nil && *node.RequireCheckpoint && node.CheckpointMode != nil &&
		*node.CheckpointMode == CheckpointModeNone {
		v.addf("%s.require_checkpoint cannot be true when checkpoint_mode=none", field)
	}
	if node.Retry != nil {
		v.validateRetry(field+".retry", node.Retry)
	}
	if node.InputFrom != nil && len(*node.InputFrom) > 0 && (*node.InputFrom)[0] == "" {
		v.addf("%s.input_from contains an empty reference", field)
	}

	// ADR 0006: non-idempotent work is never retried without an idempotency key.
	maxAttempts := 1
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

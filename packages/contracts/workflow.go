// Package contracts defines the shared, versioned workflow, task and event contracts.
//
// The JSON Schemas in packages/schemas are the published structural contract; the typed models here implement the
// semantic rules that JSON Schema cannot express (DAG shape, upstream input references, safe retry combinations).
// Python models in runtimes/python/src/contracts mirror these rules and are tested against the same fixtures.
package contracts

// WorkflowNodeType is an executable node kind. A graph may declare explicit start/end nodes; without them roots
// and terminal nodes are implicit for backwards compatibility.
type WorkflowNodeType string

// Node types.
const (
	NodeTypeStart       WorkflowNodeType = "start"
	NodeTypeEnd         WorkflowNodeType = "end"
	NodeTypeAgent       WorkflowNodeType = "agent"
	NodeTypeLLM         WorkflowNodeType = "llm"
	NodeTypeTool        WorkflowNodeType = "tool"
	NodeTypeCondition   WorkflowNodeType = "condition"
	NodeTypeTransform   WorkflowNodeType = "transform"
	NodeTypeParallel    WorkflowNodeType = "parallel"
	NodeTypeJoin        WorkflowNodeType = "join"
	NodeTypeDelay       WorkflowNodeType = "delay"
	NodeTypeApproval    WorkflowNodeType = "approval"
	NodeTypeSubworkflow WorkflowNodeType = "subworkflow"
	NodeTypeLoop        WorkflowNodeType = "loop"
)

// WorkerNode reports whether the node runs in a worker/runtime process. All other node types are resolved by the
// Go orchestration engine.
func (t WorkflowNodeType) WorkerNode() bool {
	return t == NodeTypeAgent || t == NodeTypeLLM || t == NodeTypeTool
}

// SideEffectClass declares retry safety. Non-idempotent nodes require an idempotency key or max_attempts=1.
type SideEffectClass string

// Side-effect classes.
const (
	SideEffectReadOnly      SideEffectClass = "read_only"
	SideEffectIdempotent    SideEffectClass = "idempotent"
	SideEffectNonIdempotent SideEffectClass = "non_idempotent"
)

// Checkpoint modes accepted by a workflow node.
const (
	CheckpointModeNone      = "none"
	CheckpointModeOnNodeEnd = "on_node_end"
	CheckpointModeInherit   = "inherit"
)

// WorkflowDocument is the authoring contract. Only the graph fields are persisted on workflow_versions; the
// metadata (id, name, description) lives on the workflow row.
type WorkflowDocument struct {
	SchemaVersion int     `json:"schema_version"`
	WorkflowID    string  `json:"workflow_id"`
	Name          string  `json:"name"`
	Description   *string `json:"description,omitempty"`

	WorkflowGraph
}

// WorkflowGraph is the immutable part of a workflow version and the input to its digest.
type WorkflowGraph struct {
	Nodes       []WorkflowNode  `json:"nodes"`
	Edges       []WorkflowEdge  `json:"edges"`
	Policy      *WorkflowPolicy `json:"policy,omitempty"`
	InputSchema *map[string]any `json:"input_schema,omitempty"`
}

// WorkflowNode is one executable node. Start/end nodes are implicit.
type WorkflowNode struct {
	ID                string           `json:"id"`
	Type              WorkflowNodeType `json:"type"`
	Agent             *AgentRef        `json:"agent,omitempty"`
	Tool              *ToolRef         `json:"tool,omitempty"`
	Params            *map[string]any  `json:"params,omitempty"`
	Config            *NodeConfig      `json:"config,omitempty"`
	InputFrom         *[]string        `json:"input_from,omitempty"`
	InputSchema       *map[string]any  `json:"input_schema,omitempty"`
	OutputSchema      *map[string]any  `json:"output_schema,omitempty"`
	WorkerPool        *string          `json:"worker_pool,omitempty"`
	TimeoutSeconds    *int             `json:"timeout_seconds,omitempty"`
	Retry             *RetryPolicy     `json:"retry,omitempty"`
	SideEffectClass   *SideEffectClass `json:"side_effect_class,omitempty"`
	IdempotencyKey    *string          `json:"idempotency_key,omitempty"`
	CheckpointMode    *string          `json:"checkpoint_mode,omitempty"`
	RequireCheckpoint *bool            `json:"require_checkpoint,omitempty"`
	Resources         *NodeResources   `json:"resources,omitempty"`
}

// NodeConfig is the type-specific configuration of a control node. Worker nodes keep their existing agent/tool
// refs and params; control nodes use these fields instead of executing arbitrary code in the orchestrator.
type NodeConfig struct {
	Expression *string            `json:"expression,omitempty"`
	Mapping    *map[string]string `json:"mapping,omitempty"`
	Join       *JoinPolicy        `json:"join,omitempty"`
	Delay      *DelayConfig       `json:"delay,omitempty"`
	Approval   *ApprovalConfig    `json:"approval,omitempty"`
	Workflow   *SubworkflowRef    `json:"workflow,omitempty"`
	Loop       *LoopConfig        `json:"loop,omitempty"`
	Input      *map[string]string `json:"input,omitempty"`
	OutputFrom *[]string          `json:"output_from,omitempty"`
}

// NodeResources bounds placement by worker capacity.
type NodeResources struct {
	CPU      *float64 `json:"cpu_cores,omitempty"`
	MemoryMB *int     `json:"memory_mb,omitempty"`
}

// JoinPolicy controls fan-in. Mode "all" waits for every active branch; "successful" waits for N successes.
type JoinPolicy struct {
	Mode       string `json:"mode"`
	Successful *int   `json:"successful,omitempty"`
	OnPartial  string `json:"on_partial,omitempty"`
}

// DelayConfig makes a node wait with persisted scheduler state. Exactly one of seconds/until/event is used.
type DelayConfig struct {
	Seconds *int    `json:"seconds,omitempty"`
	Until   *string `json:"until,omitempty"`
	Event   *string `json:"event,omitempty"`
}

// ApprovalConfig waits for one or more human decisions delivered through the API.
type ApprovalConfig struct {
	Prompt               *string  `json:"prompt,omitempty"`
	OnReject             *string  `json:"on_reject,omitempty"`
	Approvers            []string `json:"approvers,omitempty"`
	Policy               *string  `json:"policy,omitempty"`
	ExpiresInSeconds     *int     `json:"expires_in_seconds,omitempty"`
	RejectReasonRequired *bool    `json:"reject_reason_required,omitempty"`
}

// SubworkflowRef pins a published workflow version used as a child execution.
type SubworkflowRef struct {
	WorkflowID string `json:"workflow_id"`
	Version    int    `json:"version"`
}

// LoopConfig repeats a child workflow a bounded number of times.
type LoopConfig struct {
	MaxIterations int     `json:"max_iterations"`
	ExitWhen      *string `json:"exit_when,omitempty"`
}

// AgentRef pins an exact agent version. "latest" is resolved at publish time, never here.
type AgentRef struct {
	AgentID string `json:"agent_id"`
	Version int    `json:"version"`
}

// ToolRef pins an exact platform-provided tool version.
type ToolRef struct {
	ToolID  string `json:"tool_id"`
	Version int    `json:"version"`
}

// WorkflowEdge connects two nodes. When is an optional CEL expression over upstream outputs.
type WorkflowEdge struct {
	From        string             `json:"from"`
	To          string             `json:"to"`
	When        *string            `json:"when,omitempty"`
	DataMapping *map[string]string `json:"data_mapping,omitempty"`
	Priority    *int               `json:"priority,omitempty"`
}

// RetryPolicy is the node-level retry budget. Omitted fields take documented defaults at execution time.
type RetryPolicy struct {
	MaxAttempts   *int      `json:"max_attempts,omitempty"`
	RetryOn       *[]string `json:"retry_on,omitempty"`
	BackoffBaseMS *int      `json:"backoff_base_ms,omitempty"`
	BackoffMaxMS  *int      `json:"backoff_max_ms,omitempty"`
}

// WorkflowPolicy sets execution-wide behaviour. Retry is the default for nodes that do not override it.
type WorkflowPolicy struct {
	OnFailure *string      `json:"on_failure,omitempty"`
	Retry     *RetryPolicy `json:"retry,omitempty"`
}

// Graph returns the immutable graph portion of the document.
func (d WorkflowDocument) Graph() WorkflowGraph { return d.WorkflowGraph }

// CanonicalJSON returns RFC 8785 bytes for the graph.
func (g WorkflowGraph) CanonicalJSON() ([]byte, error) {
	return Canonicalize(g)
}

// Digest is the content digest stored on workflow_versions.
func (g WorkflowGraph) Digest() (string, error) {
	return Digest(g)
}

// CanonicalGraph returns RFC 8785 bytes for the graph that is persisted on workflow_versions.
func (d WorkflowDocument) CanonicalGraph() ([]byte, error) {
	return Canonicalize(d.WorkflowGraph)
}

// GraphDigest is the content digest stored on workflow_versions.
func (d WorkflowDocument) GraphDigest() (string, error) {
	return Digest(d.WorkflowGraph)
}

// CanonicalDocument returns RFC 8785 bytes for the full authoring document.
func (d WorkflowDocument) CanonicalDocument() ([]byte, error) {
	return Canonicalize(d)
}

// DocumentDigest covers metadata and graph. Cross-language tests compare both digests.
func (d WorkflowDocument) DocumentDigest() (string, error) {
	return Digest(d)
}

// ParseWorkflowDocument strictly decodes and semantically validates a workflow document.
func ParseWorkflowDocument(data []byte) (*WorkflowDocument, error) {
	var doc WorkflowDocument
	if err := parseStrict(data, &doc); err != nil {
		return nil, err
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return &doc, nil
}

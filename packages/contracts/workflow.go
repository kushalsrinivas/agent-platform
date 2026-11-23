// Package contracts defines the shared, versioned workflow, task and event contracts.
//
// The JSON Schemas in packages/schemas are the published structural contract; the typed models here implement the
// semantic rules that JSON Schema cannot express (DAG shape, upstream input references, safe retry combinations).
// Python models in runtimes/python/src/contracts mirror these rules and are tested against the same fixtures.
package contracts

// WorkflowNodeType is an executable node kind. Start/end nodes are implicit roots and terminals.
type WorkflowNodeType string

// Node types.
const (
	NodeTypeAgent WorkflowNodeType = "agent"
	NodeTypeTool  WorkflowNodeType = "tool"
)

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
	InputFrom         *[]string        `json:"input_from,omitempty"`
	WorkerPool        *string          `json:"worker_pool,omitempty"`
	TimeoutSeconds    *int             `json:"timeout_seconds,omitempty"`
	Retry             *RetryPolicy     `json:"retry,omitempty"`
	SideEffectClass   *SideEffectClass `json:"side_effect_class,omitempty"`
	IdempotencyKey    *string          `json:"idempotency_key,omitempty"`
	CheckpointMode    *string          `json:"checkpoint_mode,omitempty"`
	RequireCheckpoint *bool            `json:"require_checkpoint,omitempty"`
	OutputSchema      *map[string]any  `json:"output_schema,omitempty"`
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
	From string  `json:"from"`
	To   string  `json:"to"`
	When *string `json:"when,omitempty"`
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

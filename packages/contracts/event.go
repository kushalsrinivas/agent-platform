package contracts

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

// Event type names are the normative message types from docs/communication.md §7.
const (
	EventExecutionCreated      = "execution.created"
	EventExecutionStateChanged = "execution.state_changed"
	EventTaskStateChanged      = "task.state_changed"
	EventTaskAttemptFinished   = "task.attempt_finished"
	EventTaskDispatch          = "task.dispatch"
)

// EventSpec names the payload schema for a message type. All payloads are version 1.
type EventSpec struct {
	PayloadSchema string
}

// SupportedEventTypes maps each supported message type to its payload JSON Schema.
var SupportedEventTypes = map[string]EventSpec{
	EventExecutionCreated:      {PayloadSchema: "events/execution.created.schema.json"},
	EventExecutionStateChanged: {PayloadSchema: "events/execution.state_changed.schema.json"},
	EventTaskStateChanged:      {PayloadSchema: "events/task.state_changed.schema.json"},
	EventTaskAttemptFinished:   {PayloadSchema: "events/task.attempt_finished.schema.json"},
	EventTaskDispatch:          {PayloadSchema: "events/task.dispatch.schema.json"},
}

// EventEnvelope is the internal message envelope. Payload remains raw until the message type selects a schema.
type EventEnvelope struct {
	MessageID     string          `json:"message_id"`
	MessageType   string          `json:"message_type"`
	SchemaVersion int             `json:"schema_version"`
	TenantID      *string         `json:"tenant_id,omitempty"`
	ProjectID     *string         `json:"project_id,omitempty"`
	ExecutionID   *string         `json:"execution_id,omitempty"`
	TaskID        *string         `json:"task_id,omitempty"`
	AttemptID     *string         `json:"attempt_id,omitempty"`
	WorkerID      *string         `json:"worker_id,omitempty"`
	Timestamp     string          `json:"timestamp"`
	CorrelationID *string         `json:"correlation_id,omitempty"`
	CausationID   *string         `json:"causation_id,omitempty"`
	Traceparent   *string         `json:"traceparent,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// ParsedEvent pairs a validated envelope with its typed payload.
type ParsedEvent struct {
	Envelope EventEnvelope
	Payload  any
}

// ExecutionCounts is the task-state snapshot carried by execution events.
type ExecutionCounts struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Assigned  int `json:"assigned"`
	Running   int `json:"running"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	Skipped   int `json:"skipped"`
}

// ExecutionCreatedPayload is the execution.created message payload.
type ExecutionCreatedPayload struct {
	Seq               int64           `json:"seq"`
	WorkflowVersionID string          `json:"workflow_version_id"`
	State             string          `json:"state"`
	StateVersion      int64           `json:"state_version"`
	Counts            ExecutionCounts `json:"counts"`
}

// ExecutionStateChangedPayload is the execution.state_changed message payload.
type ExecutionStateChangedPayload struct {
	Seq          int64           `json:"seq"`
	From         string          `json:"from"`
	To           string          `json:"to"`
	StateVersion int64           `json:"state_version"`
	Reason       *string         `json:"reason,omitempty"`
	FailureCode  *string         `json:"failure_code,omitempty"`
	Counts       ExecutionCounts `json:"counts"`
}

// TaskStateChangedPayload is the task.state_changed message payload.
type TaskStateChangedPayload struct {
	Seq          int64   `json:"seq"`
	TaskID       string  `json:"task_id"`
	NodeID       string  `json:"node_id"`
	From         string  `json:"from"`
	To           string  `json:"to"`
	StateVersion int64   `json:"state_version"`
	Reason       *string `json:"reason,omitempty"`
	AttemptID    *string `json:"attempt_id,omitempty"`
	SkipReason   *string `json:"skip_reason,omitempty"`
}

// TaskAttemptFinishedPayload is the task.attempt_finished message payload.
type TaskAttemptFinishedPayload struct {
	Seq             int64   `json:"seq"`
	AttemptID       string  `json:"attempt_id"`
	AttemptNumber   int     `json:"attempt_number"`
	WorkerID        string  `json:"worker_id"`
	Outcome         string  `json:"outcome"`
	ErrorCode       *string `json:"error_code,omitempty"`
	EffectUncertain bool    `json:"effect_uncertain"`
	DurationMS      *int64  `json:"duration_ms,omitempty"`
}

// TaskDispatchPayload is the bus pointer for a committed assignment.
type TaskDispatchPayload struct {
	AttemptNumber  int              `json:"attempt_number"`
	LeaseID        string           `json:"lease_id"`
	NodeID         string           `json:"node_id"`
	NodeType       WorkflowNodeType `json:"node_type"`
	AgentVersionID *string          `json:"agent_version_id,omitempty"`
	ToolVersionID  *string          `json:"tool_version_id,omitempty"`
	SpecDigest     string           `json:"spec_digest"`
	Deadline       string           `json:"deadline"`
	LeaseExpiresAt string           `json:"lease_expires_at"`
}

var executionStates = []string{"pending", "running", "failing", "cancel_requested", "succeeded", "failed", "cancelled"}
var taskStates = []string{"pending", "assigned", "running", "succeeded", "failed", "cancelled", "skipped"}
var attemptOutcomes = []string{"succeeded", "failed", "cancelled", "lease_expired", "incomplete"}
var skipReasons = []string{"branch_not_taken", "upstream_not_taken", "dependency_failed"}

// ParseEvent validates the envelope, dispatches on message_type and validates the typed payload.
func ParseEvent(data []byte) (*ParsedEvent, error) {
	var envelope EventEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("contracts: decode event envelope: %w", err)
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}

	var payload any
	switch envelope.MessageType {
	case EventExecutionCreated:
		p := &ExecutionCreatedPayload{}
		if err := json.Unmarshal(envelope.Payload, p); err != nil {
			return nil, fmt.Errorf("contracts: decode execution.created payload: %w", err)
		}
		payload = p
	case EventExecutionStateChanged:
		p := &ExecutionStateChangedPayload{}
		if err := json.Unmarshal(envelope.Payload, p); err != nil {
			return nil, fmt.Errorf("contracts: decode execution.state_changed payload: %w", err)
		}
		payload = p
	case EventTaskStateChanged:
		p := &TaskStateChangedPayload{}
		if err := json.Unmarshal(envelope.Payload, p); err != nil {
			return nil, fmt.Errorf("contracts: decode task.state_changed payload: %w", err)
		}
		payload = p
	case EventTaskAttemptFinished:
		p := &TaskAttemptFinishedPayload{}
		if err := json.Unmarshal(envelope.Payload, p); err != nil {
			return nil, fmt.Errorf("contracts: decode task.attempt_finished payload: %w", err)
		}
		payload = p
	case EventTaskDispatch:
		p := &TaskDispatchPayload{}
		if err := json.Unmarshal(envelope.Payload, p); err != nil {
			return nil, fmt.Errorf("contracts: decode task.dispatch payload: %w", err)
		}
		payload = p
	}

	if err := validatePayload(payload); err != nil {
		return nil, err
	}
	return &ParsedEvent{Envelope: envelope, Payload: payload}, nil
}

// Validate enforces the envelope contract, including required context per message type.
func (e *EventEnvelope) Validate() error {
	v := &validator{}
	v.pattern("message_id", e.MessageID, messageIDPattern)
	if _, ok := SupportedEventTypes[e.MessageType]; !ok {
		v.addf("message_type %q is not a supported event type", e.MessageType)
	}
	if e.SchemaVersion != 1 {
		v.addf("schema_version must be 1")
	}
	if e.TenantID == nil {
		v.addf("tenant_id is required for tenant-scoped messages")
	} else {
		v.pattern("tenant_id", *e.TenantID, orgIDPattern)
	}
	if e.ProjectID == nil {
		v.addf("project_id is required")
	} else {
		v.pattern("project_id", *e.ProjectID, projectIDPattern)
	}
	if e.ExecutionID == nil {
		v.addf("execution_id is required")
	} else {
		v.pattern("execution_id", *e.ExecutionID, executionIDPattern)
	}
	if e.MessageType == EventTaskStateChanged || e.MessageType == EventTaskAttemptFinished || e.MessageType == EventTaskDispatch {
		if e.TaskID == nil {
			v.addf("task_id is required for %s", e.MessageType)
		} else {
			v.pattern("task_id", *e.TaskID, taskIDPattern)
		}
	}
	if e.MessageType == EventTaskStateChanged || e.MessageType == EventTaskAttemptFinished || e.MessageType == EventTaskDispatch {
		if e.AttemptID == nil {
			v.addf("attempt_id is required for %s", e.MessageType)
		} else {
			v.pattern("attempt_id", *e.AttemptID, attemptIDPattern)
		}
	}
	if e.MessageType == EventTaskDispatch && e.WorkerID == nil {
		v.addf("worker_id is required for task.dispatch")
	}
	if e.WorkerID != nil {
		v.pattern("worker_id", *e.WorkerID, workerIDPattern)
	}
	if !validateTimestamp("timestamp", e.Timestamp) {
		v.addf("timestamp must be an RFC 3339 timestamp")
	}
	if e.Traceparent != nil {
		v.pattern("traceparent", *e.Traceparent, traceparentPattern)
	}
	if len(e.Payload) == 0 {
		v.addf("payload is required")
	}
	return v.err()
}

func validatePayload(payload any) error {
	switch p := payload.(type) {
	case *ExecutionCreatedPayload:
		return p.Validate()
	case *ExecutionStateChangedPayload:
		return p.Validate()
	case *TaskStateChangedPayload:
		return p.Validate()
	case *TaskAttemptFinishedPayload:
		return p.Validate()
	case *TaskDispatchPayload:
		return p.Validate()
	default:
		return fmt.Errorf("contracts: no validator for payload %T", payload)
	}
}

func validateCounts(v *validator, counts ExecutionCounts) {
	values := map[string]int{
		"total": counts.Total, "pending": counts.Pending, "assigned": counts.Assigned,
		"running": counts.Running, "succeeded": counts.Succeeded, "failed": counts.Failed,
		"cancelled": counts.Cancelled, "skipped": counts.Skipped,
	}
	sum := 0
	for field, value := range values {
		if value < 0 {
			v.addf("counts.%s must be non-negative", field)
		}
		if field != "total" {
			sum += value
		}
	}
	if counts.Total != sum {
		v.addf("counts.total must equal the sum of task states")
	}
}

// Validate enforces the execution.created payload contract.
func (p *ExecutionCreatedPayload) Validate() error {
	v := &validator{}
	if p.Seq < 1 {
		v.addf("seq must be at least 1")
	}
	v.pattern("workflow_version_id", p.WorkflowVersionID, workflowVersionIDPattern)
	v.enum("state", p.State, executionStates...)
	if p.StateVersion < 1 {
		v.addf("state_version must be at least 1")
	}
	validateCounts(v, p.Counts)
	return v.err()
}

// Validate enforces the execution.state_changed payload contract.
func (p *ExecutionStateChangedPayload) Validate() error {
	v := &validator{}
	if p.Seq < 1 {
		v.addf("seq must be at least 1")
	}
	v.enum("from", p.From, executionStates...)
	v.enum("to", p.To, executionStates...)
	if p.StateVersion < 2 {
		v.addf("state_version must be at least 2")
	}
	if p.FailureCode != nil && utf8.RuneCountInString(*p.FailureCode) > 128 {
		v.addf("failure_code must be at most 128 characters")
	}
	validateCounts(v, p.Counts)
	return v.err()
}

// Validate enforces the task.state_changed payload contract.
func (p *TaskStateChangedPayload) Validate() error {
	v := &validator{}
	if p.Seq < 1 {
		v.addf("seq must be at least 1")
	}
	v.pattern("task_id", p.TaskID, taskIDPattern)
	v.pattern("node_id", p.NodeID, nodeIDPattern)
	v.enum("from", p.From, taskStates...)
	v.enum("to", p.To, taskStates...)
	if p.StateVersion < 1 {
		v.addf("state_version must be at least 1")
	}
	if p.AttemptID != nil {
		v.pattern("attempt_id", *p.AttemptID, attemptIDPattern)
	}
	if p.SkipReason != nil {
		v.enum("skip_reason", *p.SkipReason, skipReasons...)
	}
	return v.err()
}

// Validate enforces the task.attempt_finished payload contract.
func (p *TaskAttemptFinishedPayload) Validate() error {
	v := &validator{}
	if p.Seq < 1 {
		v.addf("seq must be at least 1")
	}
	v.pattern("attempt_id", p.AttemptID, attemptIDPattern)
	if p.AttemptNumber < 1 {
		v.addf("attempt_number must be at least 1")
	}
	v.requiredString("worker_id", p.WorkerID, 1, 128)
	v.enum("outcome", p.Outcome, attemptOutcomes...)
	if p.ErrorCode != nil && utf8.RuneCountInString(*p.ErrorCode) > 128 {
		v.addf("error_code must be at most 128 characters")
	}
	if p.DurationMS != nil && *p.DurationMS < 0 {
		v.addf("duration_ms must be non-negative")
	}
	return v.err()
}

// Validate enforces the task.dispatch payload contract.
func (p *TaskDispatchPayload) Validate() error {
	v := &validator{}
	if p.AttemptNumber < 1 {
		v.addf("attempt_number must be at least 1")
	}
	if !validateUUID("lease_id", p.LeaseID) {
		v.addf("lease_id must be a UUID")
	}
	v.pattern("node_id", p.NodeID, nodeIDPattern)
	if p.NodeType != NodeTypeAgent && p.NodeType != NodeTypeTool {
		v.addf("node_type must be agent or tool")
	}
	if p.NodeType == NodeTypeAgent && p.AgentVersionID == nil {
		v.addf("agent_version_id is required for an agent task")
	}
	if p.NodeType == NodeTypeTool && p.ToolVersionID == nil {
		v.addf("tool_version_id is required for a tool task")
	}
	if p.NodeType == NodeTypeAgent && p.ToolVersionID != nil {
		v.addf("tool_version_id is not allowed for an agent task")
	}
	if p.NodeType == NodeTypeTool && p.AgentVersionID != nil {
		v.addf("agent_version_id is not allowed for a tool task")
	}
	if p.AgentVersionID != nil {
		v.pattern("agent_version_id", *p.AgentVersionID, agentVersionIDPattern)
	}
	if p.ToolVersionID != nil {
		v.requiredString("tool_version_id", *p.ToolVersionID, 1, 128)
	}
	v.pattern("spec_digest", p.SpecDigest, digestPattern)
	if !validateTimestamp("deadline", p.Deadline) {
		v.addf("deadline must be an RFC 3339 timestamp")
	}
	if !validateTimestamp("lease_expires_at", p.LeaseExpiresAt) {
		v.addf("lease_expires_at must be an RFC 3339 timestamp")
	}
	return v.err()
}

// EventTime parses the envelope timestamp for consumers that need a time value.
func (e EventEnvelope) EventTime() (time.Time, error) {
	return time.Parse(time.RFC3339, e.Timestamp)
}

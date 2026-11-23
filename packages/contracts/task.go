package contracts

import (
	"encoding/json"
	"unicode/utf8"
)

// TaskOutcome values accepted from a worker result.
type TaskOutcome string

// Task outcome values.
const (
	TaskOutcomeSucceeded TaskOutcome = "succeeded"
	TaskOutcomeFailed    TaskOutcome = "failed"
	TaskOutcomeCancelled TaskOutcome = "cancelled"
)

// TaskAttemptOutcome includes the reaper-owned closures that never come from a worker result.
type TaskAttemptOutcome string

// Attempt closure outcomes.
const (
	AttemptSucceeded    TaskAttemptOutcome = "succeeded"
	AttemptFailed       TaskAttemptOutcome = "failed"
	AttemptCancelled    TaskAttemptOutcome = "cancelled"
	AttemptLeaseExpired TaskAttemptOutcome = "lease_expired"
	AttemptIncomplete   TaskAttemptOutcome = "incomplete"
)

// TaskDispatch is the self-contained descriptor a worker validates before accepting an attempt.
type TaskDispatch struct {
	SchemaVersion  int              `json:"schema_version"`
	OrgID          string           `json:"org_id"`
	ProjectID      string           `json:"project_id"`
	ExecutionID    string           `json:"execution_id"`
	TaskID         string           `json:"task_id"`
	AttemptID      string           `json:"attempt_id"`
	LeaseID        string           `json:"lease_id"`
	AttemptNumber  int              `json:"attempt_number"`
	NodeID         string           `json:"node_id"`
	NodeType       WorkflowNodeType `json:"node_type"`
	AgentVersionID *string          `json:"agent_version_id,omitempty"`
	ToolVersionID  *string          `json:"tool_version_id,omitempty"`
	SpecDigest     string           `json:"spec_digest"`
	Deadline       string           `json:"deadline"`
	LeaseExpiresAt string           `json:"lease_expires_at"`
}

// ArtifactRef is an immutable object beside the inline output.
type ArtifactRef struct {
	ObjectKey   string  `json:"object_key"`
	SHA256      string  `json:"sha256"`
	SizeBytes   int64   `json:"size_bytes"`
	ContentType *string `json:"content_type,omitempty"`
}

// CheckpointRef is the worker-reported object metadata; the orchestrator assigns the checkpoint row ID.
type CheckpointRef struct {
	ObjectKey               string `json:"object_key"`
	SHA256                  string `json:"sha256"`
	SizeBytes               int64  `json:"size_bytes"`
	CheckpointSchemaVersion int    `json:"checkpoint_schema_version"`
	RuntimeABI              int    `json:"runtime_abi"`
}

// FailureClassification drives retry policy. Unknown classifications are rejected at the boundary.
type FailureClassification string

// Failure classifications.
const (
	FailureTransient     FailureClassification = "transient"
	FailurePermanent     FailureClassification = "permanent"
	FailureTimeout       FailureClassification = "timeout"
	FailureCancelled     FailureClassification = "cancelled"
	FailureWorkerError   FailureClassification = "worker_error"
	FailureResultInvalid FailureClassification = "result_invalid"
)

// TaskFailure is the sanitised failure detail. Provider response bodies and secrets are never included.
type TaskFailure struct {
	Code            string                `json:"code"`
	Message         string                `json:"message"`
	Classification  FailureClassification `json:"classification"`
	EffectUncertain bool                  `json:"effect_uncertain"`
}

// TaskMetrics carries attempt telemetry. It is diagnostic, never used for correctness.
type TaskMetrics struct {
	DurationMS *int64          `json:"duration_ms,omitempty"`
	TokensIn   *int64          `json:"tokens_in,omitempty"`
	TokensOut  *int64          `json:"tokens_out,omitempty"`
	Steps      *int            `json:"steps,omitempty"`
	Extra      *map[string]any `json:"extra,omitempty"`
}

// TaskResult is the fenced attempt result contract. Success always contains an inline output object.
type TaskResult struct {
	SchemaVersion  int             `json:"schema_version"`
	TaskID         string          `json:"task_id"`
	AttemptID      string          `json:"attempt_id"`
	LeaseID        string          `json:"lease_id"`
	Outcome        TaskOutcome     `json:"outcome"`
	Output         *map[string]any `json:"output,omitempty"`
	OutputArtifact *ArtifactRef    `json:"output_artifact,omitempty"`
	Checkpoint     *CheckpointRef  `json:"checkpoint,omitempty"`
	Failure        *TaskFailure    `json:"failure,omitempty"`
	Metrics        *TaskMetrics    `json:"metrics,omitempty"`
	RuntimeVersion *string         `json:"runtime_version,omitempty"`
	StartedAt      *string         `json:"started_at,omitempty"`
	FinishedAt     *string         `json:"finished_at,omitempty"`
}

// ParseTaskDispatch strictly decodes and validates a task dispatch contract.
func ParseTaskDispatch(data []byte) (*TaskDispatch, error) {
	var dispatch TaskDispatch
	if err := parseStrict(data, &dispatch); err != nil {
		return nil, err
	}
	if err := dispatch.Validate(); err != nil {
		return nil, err
	}
	return &dispatch, nil
}

// ParseTaskResult strictly decodes and validates a task result contract.
func ParseTaskResult(data []byte) (*TaskResult, error) {
	var result TaskResult
	if err := parseStrict(data, &result); err != nil {
		return nil, err
	}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return &result, nil
}

// Validate enforces the task dispatch contract.
func (d *TaskDispatch) Validate() error {
	v := &validator{}
	if d.SchemaVersion != 1 {
		v.addf("schema_version must be 1")
	}
	v.pattern("org_id", d.OrgID, orgIDPattern)
	v.pattern("project_id", d.ProjectID, projectIDPattern)
	v.pattern("execution_id", d.ExecutionID, executionIDPattern)
	v.pattern("task_id", d.TaskID, taskIDPattern)
	v.pattern("attempt_id", d.AttemptID, attemptIDPattern)
	if !validateUUID("lease_id", d.LeaseID) {
		v.addf("lease_id must be a UUID")
	}
	if d.AttemptNumber < 1 {
		v.addf("attempt_number must be at least 1")
	}
	v.pattern("node_id", d.NodeID, nodeIDPattern)
	if d.NodeType != NodeTypeAgent && d.NodeType != NodeTypeTool {
		v.addf("node_type must be agent or tool")
	}
	if d.NodeType == NodeTypeAgent {
		if d.AgentVersionID == nil {
			v.addf("agent_version_id is required for an agent task")
		}
		if d.ToolVersionID != nil {
			v.addf("tool_version_id is not allowed for an agent task")
		}
	}
	if d.NodeType == NodeTypeTool {
		if d.ToolVersionID == nil {
			v.addf("tool_version_id is required for a tool task")
		}
		if d.AgentVersionID != nil {
			v.addf("agent_version_id is not allowed for a tool task")
		}
	}
	if d.AgentVersionID != nil {
		v.pattern("agent_version_id", *d.AgentVersionID, agentVersionIDPattern)
	}
	if d.ToolVersionID != nil {
		v.requiredString("tool_version_id", *d.ToolVersionID, 1, 128)
	}
	v.pattern("spec_digest", d.SpecDigest, digestPattern)
	if !validateTimestamp("deadline", d.Deadline) {
		v.addf("deadline must be an RFC 3339 timestamp")
	}
	if !validateTimestamp("lease_expires_at", d.LeaseExpiresAt) {
		v.addf("lease_expires_at must be an RFC 3339 timestamp")
	}
	return v.err()
}

// Validate enforces the task result contract.
func (r *TaskResult) Validate() error {
	v := &validator{}
	if r.SchemaVersion != 1 {
		v.addf("schema_version must be 1")
	}
	v.pattern("task_id", r.TaskID, taskIDPattern)
	v.pattern("attempt_id", r.AttemptID, attemptIDPattern)
	if !validateUUID("lease_id", r.LeaseID) {
		v.addf("lease_id must be a UUID")
	}
	v.enum("outcome", string(r.Outcome), string(TaskOutcomeSucceeded), string(TaskOutcomeFailed), string(TaskOutcomeCancelled))

	switch r.Outcome {
	case TaskOutcomeSucceeded:
		if r.Output == nil {
			v.addf("a successful result requires an inline output object")
		}
		if r.Failure != nil {
			v.addf("a successful result cannot carry a failure")
		}
	case TaskOutcomeFailed:
		if r.Failure == nil {
			v.addf("a failed result requires a failure object")
		}
	}
	if r.Output != nil {
		raw, err := json.Marshal(*r.Output)
		if err != nil {
			v.addf("output is not JSON-serialisable: %v", err)
		} else if len(raw) > 65536 {
			v.addf("inline output exceeds 64 KiB")
		}
	}
	if r.OutputArtifact != nil {
		artifact := r.OutputArtifact
		v.requiredString("output_artifact.object_key", artifact.ObjectKey, 1, 1024)
		v.pattern("output_artifact.sha256", artifact.SHA256, digestPattern)
		if artifact.SizeBytes < 0 {
			v.addf("output_artifact.size_bytes must be non-negative")
		}
		if artifact.ContentType != nil && utf8.RuneCountInString(*artifact.ContentType) > 128 {
			v.addf("output_artifact.content_type must be at most 128 characters")
		}
	}
	if r.Checkpoint != nil {
		cp := r.Checkpoint
		// The checkpoint ID is created by the orchestrator; the worker reports object metadata only.
		if cp.ObjectKey == "" {
			v.addf("checkpoint.object_key is required")
		}
		v.pattern("checkpoint.sha256", cp.SHA256, digestPattern)
		if cp.SizeBytes < 1 || cp.SizeBytes > 33554432 {
			v.addf("checkpoint.size_bytes must be between 1 and 33554432")
		}
		if cp.CheckpointSchemaVersion < 1 {
			v.addf("checkpoint.checkpoint_schema_version must be at least 1")
		}
		if cp.RuntimeABI < 1 {
			v.addf("checkpoint.runtime_abi must be at least 1")
		}
	}
	if r.Failure != nil {
		f := r.Failure
		v.requiredString("failure.code", f.Code, 1, 128)
		if utf8.RuneCountInString(f.Message) > 4096 {
			v.addf("failure.message must be at most 4096 characters")
		}
		v.enum("failure.classification", string(f.Classification),
			string(FailureTransient), string(FailurePermanent), string(FailureTimeout),
			string(FailureCancelled), string(FailureWorkerError), string(FailureResultInvalid))
	}
	if r.Metrics != nil {
		m := r.Metrics
		if m.DurationMS != nil && *m.DurationMS < 0 {
			v.addf("metrics.duration_ms must be non-negative")
		}
		if m.TokensIn != nil && *m.TokensIn < 0 {
			v.addf("metrics.tokens_in must be non-negative")
		}
		if m.TokensOut != nil && *m.TokensOut < 0 {
			v.addf("metrics.tokens_out must be non-negative")
		}
	}
	if r.RuntimeVersion != nil && utf8.RuneCountInString(*r.RuntimeVersion) > 128 {
		v.addf("runtime_version must be at most 128 characters")
	}
	if r.StartedAt != nil && !validateTimestamp("started_at", *r.StartedAt) {
		v.addf("started_at must be an RFC 3339 timestamp")
	}
	if r.FinishedAt != nil && !validateTimestamp("finished_at", *r.FinishedAt) {
		v.addf("finished_at must be an RFC 3339 timestamp")
	}
	return v.err()
}

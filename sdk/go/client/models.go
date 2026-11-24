// Package client is the Go SDK for the Agent Platform developer API.
package client

import (
	"encoding/json"
	"time"
)

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

type Project struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Environment struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Agent struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	Description   *string   `json:"description"`
	LatestVersion int       `json:"latest_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type AgentVersion struct {
	ID          string          `json:"id"`
	AgentID     string          `json:"agent_id"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	Digest      string          `json:"digest"`
	Spec        json.RawMessage `json:"spec"`
	RuntimeABI  int             `json:"runtime_abi"`
	CreatedBy   string          `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedAt *time.Time      `json:"published_at"`
}

type Workflow struct {
	ID              string    `json:"id"`
	OrgID           string    `json:"org_id"`
	ProjectID       string    `json:"project_id"`
	Name            string    `json:"name"`
	Description     *string   `json:"description"`
	LatestVersion   int       `json:"latest_version"`
	DeployedVersion *int      `json:"deployed_version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type WorkflowVersion struct {
	ID          string          `json:"id"`
	WorkflowID  string          `json:"workflow_id"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	Digest      string          `json:"digest"`
	Graph       json.RawMessage `json:"graph"`
	CreatedBy   string          `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
	PublishedAt *time.Time      `json:"published_at"`
}

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

type Execution struct {
	ID                string          `json:"id"`
	OrgID             string          `json:"org_id"`
	ProjectID         string          `json:"project_id"`
	WorkflowID        string          `json:"workflow_id"`
	WorkflowVersionID string          `json:"workflow_version_id"`
	WorkflowVersion   int             `json:"workflow_version"`
	State             string          `json:"state"`
	StateVersion      int64           `json:"state_version"`
	Priority          int             `json:"priority"`
	Counts            ExecutionCounts `json:"counts"`
	InputDigest       string          `json:"input_digest"`
	CreatedBy         string          `json:"created_by"`
	CreatedAt         time.Time       `json:"created_at"`
	StartedAt         *time.Time      `json:"started_at"`
	EndedAt           *time.Time      `json:"ended_at"`
	DeadlineAt        *time.Time      `json:"deadline_at"`
	CancelReason      *string         `json:"cancel_reason"`
	FailureCode       *string         `json:"failure_code"`
	FailureDetail     *string         `json:"failure_detail"`
}

type Task struct {
	ID             string     `json:"id"`
	ExecutionID    string     `json:"execution_id"`
	NodeID         string     `json:"node_id"`
	NodeType       string     `json:"node_type"`
	State          string     `json:"state"`
	StateVersion   int64      `json:"state_version"`
	Priority       int        `json:"priority"`
	AttemptCount   int        `json:"attempt_count"`
	MaxAttempts    int        `json:"max_attempts"`
	AgentVersionID *string    `json:"agent_version_id"`
	ToolVersionID  *string    `json:"tool_version_id"`
	SkipReason     *string    `json:"skip_reason"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	EndedAt        *time.Time `json:"ended_at"`
}

type Attempt struct {
	ID                  string     `json:"id"`
	TaskID              string     `json:"task_id"`
	AttemptNumber       int        `json:"attempt_number"`
	WorkerID            string     `json:"worker_id"`
	Outcome             *string    `json:"outcome"`
	OutcomeUnknown      bool       `json:"outcome_unknown"`
	EffectUncertain     bool       `json:"effect_uncertain"`
	ErrorCode           *string    `json:"error_code"`
	ErrorMessage        *string    `json:"error_message"`
	DispatchedAt        time.Time  `json:"dispatched_at"`
	AcceptedAt          *time.Time `json:"accepted_at"`
	StartedAt           *time.Time `json:"started_at"`
	FinishedAt          *time.Time `json:"finished_at"`
	ResultAvailable     bool       `json:"result_available"`
	CheckpointAvailable bool       `json:"checkpoint_available"`
}

type TaskDetail struct {
	Task
	Attempts []Attempt       `json:"attempts"`
	Result   json.RawMessage `json:"result"`
}

type Event struct {
	Seq        int64           `json:"seq"`
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id"`
	FromState  *string         `json:"from_state"`
	ToState    *string         `json:"to_state"`
	Reason     *string         `json:"reason"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Approval struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id,omitempty"`
	ExecutionID string     `json:"execution_id,omitempty"`
	TaskID      string     `json:"task_id"`
	Prompt      string     `json:"prompt"`
	Policy      string     `json:"policy"`
	Approvers   []string   `json:"approvers"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requested_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	DecidedAt   *time.Time `json:"decided_at"`
	DecidedBy   *string    `json:"decided_by"`
	Reason      *string    `json:"reason"`
}

type Delegation struct {
	ID                string     `json:"id"`
	RootExecutionID   string     `json:"root_execution_id"`
	ParentExecutionID string     `json:"parent_execution_id"`
	ParentTaskID      string     `json:"parent_task_id"`
	ChildExecutionID  string     `json:"child_execution_id"`
	AgentID           string     `json:"agent_id"`
	AgentVersion      int        `json:"agent_version"`
	Status            string     `json:"status"`
	ErrorCode         *string    `json:"error_code"`
	ErrorMessage      *string    `json:"error_message"`
	CreatedAt         time.Time  `json:"created_at"`
	FinishedAt        *time.Time `json:"finished_at"`
}

type MCPServer struct {
	Name         string          `json:"name"`
	Transport    string          `json:"transport"`
	Command      string          `json:"command"`
	Args         []string        `json:"args"`
	URL          string          `json:"url"`
	AuthRef      string          `json:"auth_credential_ref"`
	ToolPolicies json.RawMessage `json:"tool_policies"`
}

type Tool struct {
	ToolID      string          `json:"tool_id"`
	Name        string          `json:"name"`
	Version     int             `json:"version"`
	Description string          `json:"description"`
	Source      string          `json:"source"`
	Schema      json.RawMessage `json:"schema"`
}

type Worker struct {
	ID              string    `json:"id"`
	Pool            string    `json:"pool"`
	Status          string    `json:"status"`
	MaxConcurrency  int       `json:"max_concurrency"`
	AvailableSlots  int       `json:"available_slots"`
	ReservedSlots   int       `json:"reserved_slots"`
	Runtime         string    `json:"runtime"`
	RuntimeVersion  string    `json:"runtime_version"`
	BinaryVersion   string    `json:"binary_version"`
	CPUCores        float64   `json:"cpu_cores"`
	CPUUsagePercent *float64  `json:"cpu_usage_percent"`
	MemoryMB        int64     `json:"memory_mb"`
	MemoryUsedMB    *int64    `json:"memory_used_mb"`
	Capabilities    []string  `json:"capabilities"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
}

type UsageProject struct {
	ProjectID  string `json:"project_id"`
	Executions int64  `json:"executions"`
	Succeeded  int64  `json:"succeeded"`
	Failed     int64  `json:"failed"`
	Attempts   int64  `json:"attempts"`
	TokensIn   int64  `json:"tokens_in"`
	TokensOut  int64  `json:"tokens_out"`
	DurationMS int64  `json:"duration_ms"`
}

type Usage struct {
	From     time.Time      `json:"from"`
	To       time.Time      `json:"to"`
	Projects []UsageProject `json:"projects"`
	Total    UsageProject   `json:"total"`
}

type APIKey struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	ProjectID  *string    `json:"project_id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	Role       string     `json:"role"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	Key        string     `json:"key,omitempty"`
}

type ValidationResult struct {
	Valid bool `json:"valid"`
	Nodes int  `json:"nodes"`
	Edges int  `json:"edges"`
}

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

package agents

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

var imageDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// AgentSpec is the immutable agent version content.
type AgentSpec struct {
	Model        Model           `json:"model"`
	Instructions string          `json:"instructions"`
	Tools        []ToolBinding   `json:"tools,omitempty"`
	Runtime      Runtime         `json:"runtime"`
	Limits       *Limits         `json:"limits,omitempty"`
	Checkpoint   *Checkpoint     `json:"checkpoint,omitempty"`
	OutputSchema *map[string]any `json:"output_schema,omitempty"`
}

type Model struct {
	Provider      string          `json:"provider"`
	Name          string          `json:"name"`
	CredentialRef string          `json:"credential_ref"`
	Parameters    *map[string]any `json:"parameters,omitempty"`
}

type ToolBinding struct {
	ToolID  string          `json:"tool_id"`
	Version int             `json:"version"`
	Config  *map[string]any `json:"config,omitempty"`
}

type Runtime struct {
	ABIVersion  int    `json:"abi_version"`
	ImageDigest string `json:"image_digest"`
}

type Limits struct {
	MaxTurns       *int `json:"max_turns,omitempty"`
	TimeoutSeconds *int `json:"timeout_seconds,omitempty"`
}

type Checkpoint struct {
	Mode              string `json:"mode,omitempty"`
	RequireCheckpoint *bool  `json:"require_checkpoint,omitempty"`
}

// Validate enforces the agent version contract.
func (s *AgentSpec) Validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(s.Model.Provider) == "" || len(s.Model.Provider) > 64 {
		add("model.provider must be 1..64 characters")
	}
	if strings.TrimSpace(s.Model.Name) == "" || len(s.Model.Name) > 128 {
		add("model.name must be 1..128 characters")
	}
	if strings.TrimSpace(s.Model.CredentialRef) == "" || len(s.Model.CredentialRef) > 120 {
		add("model.credential_ref must be 1..120 characters")
	}
	if n := utf8.RuneCountInString(s.Instructions); n < 1 || n > 200000 {
		add("instructions must be 1..200000 characters")
	}
	if len(s.Tools) > 64 {
		add("at most 64 tools may be bound")
	}
	seenTools := make(map[string]bool, len(s.Tools))
	for i, tool := range s.Tools {
		if strings.TrimSpace(tool.ToolID) == "" || len(tool.ToolID) > 128 {
			add("tools[%d].tool_id must be 1..128 characters", i)
		}
		if tool.Version < 1 {
			add("tools[%d].version must be at least 1", i)
		}
		if seenTools[tool.ToolID] {
			add("tools[%d].tool_id %q is bound more than once", i, tool.ToolID)
		}
		seenTools[tool.ToolID] = true
	}
	if s.Runtime.ABIVersion < 1 || s.Runtime.ABIVersion > 1000 {
		add("runtime.abi_version must be between 1 and 1000")
	}
	if !imageDigestPattern.MatchString(s.Runtime.ImageDigest) {
		add("runtime.image_digest must be a sha256 digest, not a tag")
	}
	if s.Limits != nil {
		if s.Limits.MaxTurns != nil && (*s.Limits.MaxTurns < 1 || *s.Limits.MaxTurns > 1000) {
			add("limits.max_turns must be between 1 and 1000")
		}
		if s.Limits.TimeoutSeconds != nil && (*s.Limits.TimeoutSeconds < 1 || *s.Limits.TimeoutSeconds > 86400) {
			add("limits.timeout_seconds must be between 1 and 86400")
		}
	}
	if s.Checkpoint != nil {
		mode := s.Checkpoint.Mode
		if mode != "" && mode != "none" && mode != "on_node_end" {
			add("checkpoint.mode must be none or on_node_end")
		}
		if s.Checkpoint.RequireCheckpoint != nil && *s.Checkpoint.RequireCheckpoint && mode == "none" {
			add("checkpoint.require_checkpoint cannot be true when mode=none")
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// Digest is SHA-256 over canonical JSON of {spec, runtime_abi}, as promised by the API contract.
func (s AgentSpec) Digest() (string, error) {
	return contracts.Digest(struct {
		Spec       AgentSpec `json:"spec"`
		RuntimeABI int       `json:"runtime_abi"`
	}{Spec: s, RuntimeABI: s.Runtime.ABIVersion})
}

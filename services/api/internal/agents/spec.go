package agents

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

var (
	imageDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	secretKeyPattern   = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[-_]?key|authorization|credential|cookie)`)
	secretValuePattern = regexp.MustCompile(`(?i)(apk_[a-z0-9_-]{8,}|bearer\s+[a-z0-9._~+/=-]{12,}|sk-[a-z0-9_-]{12,})`)
)

// AgentSpec is the immutable agent version content.
type AgentSpec struct {
	Model         Model           `json:"model"`
	Instructions  string          `json:"instructions"`
	Tools         []ToolBinding   `json:"tools,omitempty"`
	Runtime       Runtime         `json:"runtime"`
	Limits        *Limits         `json:"limits,omitempty"`
	Checkpoint    *Checkpoint     `json:"checkpoint,omitempty"`
	OutputSchema  *map[string]any `json:"output_schema,omitempty"`
	Orchestration *Orchestration  `json:"orchestration,omitempty"`
	MCPServers    []MCPServer     `json:"mcp_servers,omitempty"`
}

// Orchestration declares a supervisor agent's delegation budget and the exact child agents it may use.
type Orchestration struct {
	AllowedAgents      []AllowedAgent `json:"allowed_agents"`
	MaxChildren        *int           `json:"max_children,omitempty"`
	MaxDepth           *int           `json:"max_depth,omitempty"`
	MaxParallel        *int           `json:"max_parallel,omitempty"`
	MaxTotalTokens     *int64         `json:"max_total_tokens,omitempty"`
	MaxDurationSeconds *int           `json:"max_duration_seconds,omitempty"`
	MaxRetries         *int           `json:"max_retries,omitempty"`
}

type AllowedAgent struct {
	AgentID string `json:"agent_id"`
	Version int    `json:"version"`
	Name    string `json:"name,omitempty"`
}

// MCPServer registers a Model Context Protocol server for this agent version.
type MCPServer struct {
	Name              string                `json:"name"`
	Transport         string                `json:"transport"`
	Command           string                `json:"command,omitempty"`
	Args              []string              `json:"args,omitempty"`
	Env               map[string]string     `json:"env,omitempty"`
	URL               string                `json:"url,omitempty"`
	TimeoutSeconds    *float64              `json:"timeout_seconds,omitempty"`
	AuthCredentialRef string                `json:"auth_credential_ref,omitempty"`
	ToolPolicies      map[string]ToolPolicy `json:"tool_policies,omitempty"`
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
	Policy  *ToolPolicy     `json:"policy,omitempty"`
}

// ToolPolicy is the runtime-enforced permission and execution envelope for one tool.
type ToolPolicy struct {
	Permissions      ToolPermissions `json:"permissions,omitempty"`
	TimeoutSeconds   *float64        `json:"timeout_seconds,omitempty"`
	ConcurrencyLimit *int            `json:"concurrency_limit,omitempty"`
	AuthScope        string          `json:"auth_scope,omitempty"`
	MaxRetries       *int            `json:"max_retries,omitempty"`
	Idempotency      string          `json:"idempotency,omitempty"`
	RequiresApproval *bool           `json:"requires_approval,omitempty"`
}

type ToolPermissions struct {
	Read        *bool `json:"read,omitempty"`
	Write       *bool `json:"write,omitempty"`
	Destructive *bool `json:"destructive,omitempty"`
	Network     *bool `json:"network,omitempty"`
}

func validateToolPolicy(prefix string, policy *ToolPolicy, add func(string, ...any)) {
	if policy == nil {
		return
	}
	if policy.TimeoutSeconds != nil && (*policy.TimeoutSeconds <= 0 || *policy.TimeoutSeconds > 300) {
		add("%s.timeout_seconds must be between 0 and 300", prefix)
	}
	if policy.ConcurrencyLimit != nil && (*policy.ConcurrencyLimit < 1 || *policy.ConcurrencyLimit > 100) {
		add("%s.concurrency_limit must be between 1 and 100", prefix)
	}
	if len(policy.AuthScope) > 120 {
		add("%s.auth_scope must be at most 120 characters", prefix)
	}
	if policy.MaxRetries != nil && (*policy.MaxRetries < 0 || *policy.MaxRetries > 5) {
		add("%s.max_retries must be between 0 and 5", prefix)
	}
	if policy.Idempotency != "" && policy.Idempotency != "required" && policy.Idempotency != "optional" {
		add("%s.idempotency must be required or optional", prefix)
	}
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
		validateToolPolicy(fmt.Sprintf("tools[%d].policy", i), tool.Policy, add)
	}
	if s.Runtime.ABIVersion < 1 || s.Runtime.ABIVersion > 1000 {
		add("runtime.abi_version must be between 1 and 1000")
	}
	if !imageDigestPattern.MatchString(s.Runtime.ImageDigest) {
		add("runtime.image_digest must be a sha256 digest, not a tag")
	}
	if s.Model.Parameters != nil && containsSecret(*s.Model.Parameters) {
		add("model.parameters must not contain credentials; use model.credential_ref")
	}
	for i, tool := range s.Tools {
		if tool.Config != nil && containsSecret(*tool.Config) {
			add("tools[%d].config must not contain credentials", i)
		}
	}
	if s.Limits != nil {
		if s.Limits.MaxTurns != nil && (*s.Limits.MaxTurns < 1 || *s.Limits.MaxTurns > 1000) {
			add("limits.max_turns must be between 1 and 1000")
		}
		if s.Limits.TimeoutSeconds != nil && (*s.Limits.TimeoutSeconds < 1 || *s.Limits.TimeoutSeconds > 86400) {
			add("limits.timeout_seconds must be between 1 and 86400")
		}
	}
	if s.Orchestration != nil {
		orch := s.Orchestration
		if len(orch.AllowedAgents) == 0 {
			add("orchestration.allowed_agents must contain at least one agent")
		}
		if len(orch.AllowedAgents) > 50 {
			add("orchestration.allowed_agents may contain at most 50 agents")
		}
		seen := map[string]bool{}
		for i, allowed := range orch.AllowedAgents {
			if !strings.HasPrefix(allowed.AgentID, "agt_") {
				add("orchestration.allowed_agents[%d].agent_id must start with agt_", i)
			}
			if allowed.Version < 1 {
				add("orchestration.allowed_agents[%d].version must be at least 1", i)
			}
			key := allowed.AgentID + ":" + fmt.Sprint(allowed.Version)
			if seen[key] {
				add("orchestration.allowed_agents[%d] is duplicated", i)
			}
			seen[key] = true
		}
		if orch.MaxChildren != nil && (*orch.MaxChildren < 1 || *orch.MaxChildren > 500) {
			add("orchestration.max_children must be between 1 and 500")
		}
		if orch.MaxDepth != nil && (*orch.MaxDepth < 1 || *orch.MaxDepth > 32) {
			add("orchestration.max_depth must be between 1 and 32")
		}
		if orch.MaxParallel != nil && (*orch.MaxParallel < 1 || *orch.MaxParallel > 100) {
			add("orchestration.max_parallel must be between 1 and 100")
		}
		if orch.MaxTotalTokens != nil && *orch.MaxTotalTokens < 1 {
			add("orchestration.max_total_tokens must be positive")
		}
		if orch.MaxDurationSeconds != nil && (*orch.MaxDurationSeconds < 1 || *orch.MaxDurationSeconds > 604800) {
			add("orchestration.max_duration_seconds must be between 1 and 604800")
		}
		if orch.MaxRetries != nil && (*orch.MaxRetries < 0 || *orch.MaxRetries > 10) {
			add("orchestration.max_retries must be between 0 and 10")
		}
	}
	if len(s.MCPServers) > 16 {
		add("at most 16 MCP servers may be registered")
	}
	seenMCP := map[string]bool{}
	for i, server := range s.MCPServers {
		if server.Name == "" || len(server.Name) > 64 {
			add("mcp_servers[%d].name must be 1..64 characters", i)
		}
		if seenMCP[server.Name] {
			add("mcp_servers[%d].name %q is duplicated", i, server.Name)
		}
		seenMCP[server.Name] = true
		switch server.Transport {
		case "stdio":
			if strings.TrimSpace(server.Command) == "" {
				add("mcp_servers[%d].command is required for stdio", i)
			}
		case "streamable_http":
			if err := validateMCPURL(server.URL); err != nil {
				add("mcp_servers[%d].url %v", i, err)
			}
		default:
			add("mcp_servers[%d].transport must be stdio or streamable_http", i)
		}
		if server.TimeoutSeconds != nil && (*server.TimeoutSeconds <= 0 || *server.TimeoutSeconds > 300) {
			add("mcp_servers[%d].timeout_seconds must be between 0 and 300", i)
		}
		if len(server.Args) > 50 {
			add("mcp_servers[%d].args may contain at most 50 values", i)
		}
		if len(server.Env) > 32 {
			add("mcp_servers[%d].env may contain at most 32 values", i)
		}
		for key, value := range server.Env {
			if len(key) == 0 || len(key) > 128 || len(value) > 4096 {
				add("mcp_servers[%d].env entries must be 1..128 character keys and at most 4096 character values", i)
			}
			if secretKeyPattern.MatchString(key) || secretValuePattern.MatchString(value) {
				add("mcp_servers[%d].env must not contain credentials; use auth_credential_ref", i)
			}
		}
		for name, policy := range server.ToolPolicies {
			if name == "" || len(name) > 128 {
				add("mcp_servers[%d].tool_policies keys must be 1..128 characters", i)
			}
			validateToolPolicy(fmt.Sprintf("mcp_servers[%d].tool_policies[%q]", i, name), &policy, add)
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

func validateMCPURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return errors.New("must be an absolute http(s) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("must use http or https")
	}
	if parsed.User != nil {
		return errors.New("must not embed credentials")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("must not target loopback")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("must not target private, loopback or link-local address space")
		}
	}
	return nil
}

func containsSecret(values map[string]any) bool {
	for key, value := range values {
		if secretKeyPattern.MatchString(key) {
			return true
		}
		switch typed := value.(type) {
		case string:
			if secretValuePattern.MatchString(typed) {
				return true
			}
		case map[string]any:
			if containsSecret(typed) {
				return true
			}
		case []any:
			for _, item := range typed {
				if nested, ok := item.(map[string]any); ok && containsSecret(nested) {
					return true
				}
			}
		}
	}
	return false
}

// Digest is SHA-256 over canonical JSON of {spec, runtime_abi}, as promised by the API contract.
func (s AgentSpec) Digest() (string, error) {
	return contracts.Digest(struct {
		Spec       AgentSpec `json:"spec"`
		RuntimeABI int       `json:"runtime_abi"`
	}{Spec: s, RuntimeABI: s.Runtime.ABIVersion})
}

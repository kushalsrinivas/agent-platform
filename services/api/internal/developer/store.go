// Package developer exposes read-only developer resources: fleet, tools, MCP, usage and approvals.
package developer

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/database"
)

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

type Tool struct {
	ToolID      string         `json:"tool_id"`
	Name        string         `json:"name"`
	Version     int            `json:"version"`
	Description string         `json:"description"`
	Source      string         `json:"source"`
	Schema      map[string]any `json:"schema"`
}

func BuiltinTools() []Tool {
	return []Tool{
		{ToolID: "echo", Name: "echo", Version: 1, Source: "builtin",
			Description: "Return the arguments unchanged.", Schema: map[string]any{"type": "object"}},
		{ToolID: "add", Name: "add", Version: 1, Source: "builtin",
			Description: "Sum a non-empty list of numbers.",
			Schema: map[string]any{"type": "object", "required": []string{"numbers"},
				"properties": map[string]any{"numbers": map[string]any{"type": "array",
					"items": map[string]any{"type": "number"}}}}},
		{ToolID: "tool_web_search", Name: "tool_web_search", Version: 1, Source: "builtin",
			Description: "Search the web through the platform tool runtime.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string"}}}},
		{ToolID: "tool_news_fetch", Name: "tool_news_fetch", Version: 1, Source: "builtin",
			Description: "Fetch RSS or Atom news feeds and return recent items.",
			Schema: map[string]any{"type": "object", "required": []string{"urls"},
				"properties": map[string]any{"urls": map[string]any{"type": "array",
					"items": map[string]any{"type": "string"}}}}},
	}
}

type MCPServer struct {
	Name         string           `json:"name"`
	Transport    string           `json:"transport"`
	Command      string           `json:"command,omitempty"`
	Args         []string         `json:"args,omitempty"`
	URL          string           `json:"url,omitempty"`
	AuthRef      string           `json:"auth_credential_ref,omitempty"`
	ToolPolicies map[string]any   `json:"tool_policies,omitempty"`
	Agents       []MCPAgentSource `json:"agents"`
}

type MCPAgentSource struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Version   int    `json:"version"`
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

type Approval struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id"`
	ExecutionID string     `json:"execution_id"`
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

type Store struct {
	db *database.DB
}

func NewStore(db *database.DB) *Store { return &Store{db: db} }

func (s *Store) Workers(ctx context.Context, pool, status string, limit int) ([]Worker, error) {
	query := `
		SELECT id, pool, status, max_concurrency, reported_available_slots, reserved_slots,
		       runtime, runtime_version, binary_version, cpu_cores, cpu_usage_percent,
		       memory_mb, memory_used_mb, capabilities, last_heartbeat_at
		FROM execution.workers
		WHERE ($1 = '' OR pool = $1) AND ($2 = '' OR status = $2)
		ORDER BY pool, status, id
		LIMIT $3`
	rows, err := s.db.Pool().Query(ctx, query, pool, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	workers := make([]Worker, 0)
	for rows.Next() {
		var worker Worker
		if err := rows.Scan(&worker.ID, &worker.Pool, &worker.Status, &worker.MaxConcurrency,
			&worker.AvailableSlots, &worker.ReservedSlots, &worker.Runtime, &worker.RuntimeVersion,
			&worker.BinaryVersion, &worker.CPUCores, &worker.CPUUsagePercent, &worker.MemoryMB,
			&worker.MemoryUsedMB, &worker.Capabilities, &worker.LastHeartbeatAt); err != nil {
			return nil, err
		}
		workers = append(workers, worker)
	}
	return workers, rows.Err()
}

func (s *Store) MCPServers(ctx context.Context, orgID, projectID string) ([]MCPServer, error) {
	servers := map[string]*MCPServer{}
	order := []string{}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.name, av.version, server.value
			FROM control.agents a
			JOIN control.agent_versions av ON av.agent_id = a.id AND av.org_id = a.org_id
			CROSS JOIN LATERAL jsonb_array_elements(COALESCE(av.spec->'mcp_servers', '[]'::jsonb)) AS server(value)
			WHERE a.org_id = $1 AND a.project_id = $2
			  AND av.status IN ('published', 'deprecated')
			  AND av.version = (
			      SELECT max(v.version) FROM control.agent_versions v
			      WHERE v.agent_id = a.id AND v.org_id = a.org_id
			        AND v.status IN ('published', 'deprecated')
			  )
			ORDER BY a.name, av.version`, orgID, projectID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var agentID, agentName string
			var version int
			var raw []byte
			if err := rows.Scan(&agentID, &agentName, &version, &raw); err != nil {
				return err
			}
			var value struct {
				Name         string         `json:"name"`
				Transport    string         `json:"transport"`
				Command      string         `json:"command"`
				Args         []string       `json:"args"`
				URL          string         `json:"url"`
				AuthRef      string         `json:"auth_credential_ref"`
				ToolPolicies map[string]any `json:"tool_policies"`
			}
			if err := json.Unmarshal(raw, &value); err != nil || value.Name == "" {
				continue
			}
			server, ok := servers[value.Name]
			if !ok {
				server = &MCPServer{
					Name: value.Name, Transport: value.Transport, Command: value.Command,
					Args: value.Args, URL: value.URL, AuthRef: value.AuthRef,
					ToolPolicies: value.ToolPolicies, Agents: []MCPAgentSource{},
				}
				servers[value.Name] = server
				order = append(order, value.Name)
			}
			server.Agents = append(server.Agents, MCPAgentSource{
				AgentID: agentID, AgentName: agentName, Version: version,
			})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	items := make([]MCPServer, 0, len(order))
	for _, name := range order {
		items = append(items, *servers[name])
	}
	return items, nil
}

func (s *Store) Usage(ctx context.Context, orgID, projectID string, from, to time.Time) (Usage, error) {
	usage := Usage{From: from, To: to, Projects: []UsageProject{}}
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			WITH execs AS (
				SELECT project_id, id, state
				FROM execution.executions
				WHERE org_id = $1 AND created_at >= $2 AND created_at < $3
				  AND ($4 = '' OR project_id = $4)
			), attempts AS (
				SELECT e.project_id, count(*) AS attempts,
				       COALESCE(sum((a.metrics->>'tokens_in')::bigint), 0) AS tokens_in,
				       COALESCE(sum((a.metrics->>'tokens_out')::bigint), 0) AS tokens_out,
				       COALESCE(sum((a.metrics->>'duration_ms')::bigint), 0) AS duration_ms
				FROM execs e JOIN execution.task_attempts a ON a.execution_id = e.id
				GROUP BY e.project_id
			)
			SELECT e.project_id,
			       count(DISTINCT e.id),
			       count(DISTINCT e.id) FILTER (WHERE e.state = 'succeeded'),
			       count(DISTINCT e.id) FILTER (WHERE e.state = 'failed'),
			       COALESCE(a.attempts, 0), COALESCE(a.tokens_in, 0),
			       COALESCE(a.tokens_out, 0), COALESCE(a.duration_ms, 0)
			FROM execs e LEFT JOIN attempts a ON a.project_id = e.project_id
			GROUP BY e.project_id, a.attempts, a.tokens_in, a.tokens_out, a.duration_ms
			ORDER BY e.project_id`, orgID, from, to, projectID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item UsageProject
			if err := rows.Scan(&item.ProjectID, &item.Executions, &item.Succeeded, &item.Failed,
				&item.Attempts, &item.TokensIn, &item.TokensOut, &item.DurationMS); err != nil {
				return err
			}
			usage.Projects = append(usage.Projects, item)
			usage.Total.Executions += item.Executions
			usage.Total.Succeeded += item.Succeeded
			usage.Total.Failed += item.Failed
			usage.Total.Attempts += item.Attempts
			usage.Total.TokensIn += item.TokensIn
			usage.Total.TokensOut += item.TokensOut
			usage.Total.DurationMS += item.DurationMS
		}
		return rows.Err()
	})
	return usage, err
}

func (s *Store) Approvals(ctx context.Context, orgID, projectID, status string, limit int) ([]Approval, error) {
	approvals := make([]Approval, 0, limit)
	err := s.db.WithTenant(ctx, orgID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id, e.project_id, a.execution_id, a.task_id, a.prompt, a.policy, a.approvers,
			       a.status, a.requested_at, a.expires_at, a.decided_at, a.decided_by, a.reason
			FROM execution.approvals a
			JOIN execution.executions e ON e.id = a.execution_id
			WHERE a.org_id = $1 AND ($2 = '' OR e.project_id = $2) AND ($3 = '' OR a.status = $3)
			ORDER BY a.requested_at DESC, a.id DESC
			LIMIT $4`, orgID, projectID, status, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item Approval
			if err := rows.Scan(&item.ID, &item.ProjectID, &item.ExecutionID, &item.TaskID, &item.Prompt,
				&item.Policy, &item.Approvers, &item.Status, &item.RequestedAt, &item.ExpiresAt,
				&item.DecidedAt, &item.DecidedBy, &item.Reason); err != nil {
				return err
			}
			approvals = append(approvals, item)
		}
		return rows.Err()
	})
	return approvals, err
}

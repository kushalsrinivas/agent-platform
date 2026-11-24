// Command agentctl is the developer CLI for the Agent Platform.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/agent-platform/agent-platform/sdk/go/client"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "login":
		err = login(os.Args[2:])
	case "init":
		err = initProject(os.Args[2:])
	case "project":
		err = projectCommand(ctx, os.Args[2:])
	case "agent":
		err = agentCommand(ctx, os.Args[2:])
	case "workflow":
		err = workflowCommand(ctx, os.Args[2:])
	case "executions":
		err = executionsCommand(ctx, os.Args[2:])
	case "workers":
		err = workersCommand(ctx, os.Args[2:])
	case "tools":
		err = toolsCommand(ctx, os.Args[2:])
	case "approvals":
		err = approvalsCommand(ctx, os.Args[2:])
	case "usage":
		err = usageCommand(ctx, os.Args[2:])
	case "dev":
		err = devCommand(ctx, os.Args[2:])
	case "version":
		fmt.Println("agentctl 0.2.0")
	case "help", "-h", "--help":
		usage()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`agentctl — Agent Platform developer CLI

Usage:
  agentctl login --api-url URL --api-key KEY [--org ORG] [--project PROJECT]
  agentctl init [--dir DIR] [--name NAME]
  agentctl project create --name NAME --slug SLUG
  agentctl project use --project PROJECT
  agentctl project list
  agentctl agent create --file agent.json [--name NAME]
  agentctl agent deploy --agent AGENT_ID [--version 1]
  agentctl workflow create --file workflow.json [--name NAME] [--deploy]
  agentctl workflow validate --file workflow.json
  agentctl workflow deploy --workflow WORKFLOW_ID [--version 1]
  agentctl workflow run --workflow WORKFLOW_ID [--version 1] [--input FILE|JSON] [--follow]
  agentctl executions list [--state STATE] [--workflow WORKFLOW_ID] [--limit N]
  agentctl executions inspect EXECUTION_ID
  agentctl executions logs EXECUTION_ID [--after SEQ]
  agentctl workers list [--pool POOL] [--status STATUS]
  agentctl tools list
  agentctl approvals list [--status STATUS]
  agentctl usage [--from RFC3339] [--to RFC3339]
  agentctl dev --file workflow.json [--input FILE|JSON] [--pool POOL] [--no-worker]
  agentctl version

Environment:
  AGENT_PLATFORM_URL, AGENT_PLATFORM_API_KEY, AGENT_PLATFORM_TOKEN,
  AGENT_PLATFORM_ORG, AGENT_PLATFORM_PROJECT, AGENTCTL_CONFIG,
  AGENTCTL_WORKER_BIN, AGENTCTL_POOL.
`)
}

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	apiURL := fs.String("api-url", "", "API base URL")
	apiKey := fs.String("api-key", "", "API key (use - to read stdin)")
	token := fs.String("token", "", "OIDC bearer token")
	org := fs.String("org", "", "default organization id")
	project := fs.String("project", "", "default project id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *apiURL != "" {
		cfg.APIURL = *apiURL
	}
	switch {
	case *apiKey == "-":
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return err
		}
		cfg.APIKey = strings.TrimSpace(string(raw))
	case *apiKey != "":
		cfg.APIKey = strings.TrimSpace(*apiKey)
	}
	if *token != "" {
		cfg.Token = strings.TrimSpace(*token)
	}
	if cfg.APIKey == "" && cfg.Token == "" {
		return errors.New("an API key or token is required")
	}
	cfg.OrgID = firstNonEmpty(*org, cfg.OrgID)
	cfg.ProjectID = firstNonEmpty(*project, cfg.ProjectID)
	api := newClient(cfg)
	orgs, err := api.ListOrganizations(context.Background())
	if err != nil {
		return fmt.Errorf("credentials could not be verified: %w", err)
	}
	if cfg.OrgID == "" && len(orgs) == 1 {
		cfg.OrgID = orgs[0].ID
	}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("logged in to %s (%d organizations)\n", cfg.APIURL, len(orgs))
	fmt.Printf("config: %s\n", configPath())
	if cfg.OrgID != "" {
		fmt.Printf("org: %s\n", cfg.OrgID)
	}
	return nil
}

type manifest struct {
	Name         string `json:"name"`
	APIURL       string `json:"api_url"`
	OrgID        string `json:"org_id,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	AgentFile    string `json:"agent_file"`
	WorkflowFile string `json:"workflow_file"`
	InputFile    string `json:"input_file"`
	Pool         string `json:"pool"`
}

func initProject(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dir := fs.String("dir", ".", "target directory")
	name := fs.String("name", "hello-agent", "project name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	m := manifest{
		Name: *name, APIURL: cfg.APIURL, OrgID: cfg.OrgID, ProjectID: cfg.ProjectID,
		AgentFile: "agent.json", WorkflowFile: "workflow.json", InputFile: "input.json", Pool: cfg.Pool,
	}
	agentSpec := map[string]any{
		"model":        map[string]any{"provider": "echo", "name": "echo-1", "credential_ref": "default"},
		"instructions": "You are a helpful agent. Return a concise answer.",
		"runtime":      map[string]any{"abi_version": 1, "image_digest": "sha256:" + strings.Repeat("a", 64)},
		"limits":       map[string]any{"max_turns": 4, "timeout_seconds": 120},
		"checkpoint":   map[string]any{"mode": "on_node_end", "require_checkpoint": false},
	}
	agent := map[string]any{"name": *name, "description": "created by agentctl init", "spec": agentSpec}
	workflow := map[string]any{
		"name": *name, "description": "created by agentctl init",
		"graph": map[string]any{
			"nodes": []any{
				map[string]any{"id": "start", "type": "start"},
				map[string]any{"id": "answer", "type": "agent",
					"agent":       map[string]any{"agent_id": "agt_REPLACE_WITH_AGENT_ID", "version": 1},
					"worker_pool": cfg.Pool, "timeout_seconds": 120},
				map[string]any{"id": "end", "type": "end"},
			},
			"edges": []any{
				map[string]any{"from": "start", "to": "answer"},
				map[string]any{"from": "answer", "to": "end"},
			},
			"input_schema": map[string]any{"type": "object", "required": []any{"topic"}},
		},
	}
	if err := writeIfAbsent(filepath.Join(*dir, "agent.json"), agent); err != nil {
		return err
	}
	if err := writeIfAbsent(filepath.Join(*dir, "workflow.json"), workflow); err != nil {
		return err
	}
	if err := writeIfAbsent(filepath.Join(*dir, "input.json"), map[string]any{"topic": "hello"}); err != nil {
		return err
	}
	if err := writeIfAbsent(filepath.Join(*dir, "agentctl.json"), m); err != nil {
		return err
	}
	fmt.Printf("initialized %s in %s\n", *name, *dir)
	fmt.Println("next:")
	fmt.Println("  1. agentctl project create --name \"My project\" --slug my-project")
	fmt.Println("  2. agentctl agent create --file agent.json")
	fmt.Println("  3. replace agt_REPLACE_WITH_AGENT_ID in workflow.json")
	fmt.Println("  4. agentctl workflow create --file workflow.json --deploy")
	fmt.Println("  5. agentctl workflow run --workflow WORKFLOW_ID --input input.json --follow")
	return nil
}

func writeIfAbsent(path string, value any) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func projectCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("project subcommand required: create, use, list")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("project create", flag.ExitOnError)
		name := fs.String("name", "", "project name")
		slug := fs.String("slug", "", "project slug")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" || *slug == "" {
			return errors.New("--name and --slug are required")
		}
		project, err := newClient(cfg).CreateProject(ctx, cfg.OrgID, *name, *slug)
		if err != nil {
			return err
		}
		cfg.ProjectID = project.ID
		if err := saveConfig(cfg); err != nil {
			return err
		}
		fmt.Printf("project %s (%s)\n", project.ID, project.Slug)
	case "use":
		fs := flag.NewFlagSet("project use", flag.ExitOnError)
		project := fs.String("project", "", "project id")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *project == "" {
			return errors.New("--project is required")
		}
		cfg.ProjectID = *project
		if err := saveConfig(cfg); err != nil {
			return err
		}
		fmt.Printf("project: %s\n", cfg.ProjectID)
	case "list":
		projects, err := newClient(cfg).ListProjects(ctx, cfg.OrgID)
		if err != nil {
			return err
		}
		for _, project := range projects {
			fmt.Printf("%s  %-24s %s\n", project.ID, project.Slug, project.Name)
		}
	default:
		return fmt.Errorf("unknown project subcommand %q", args[0])
	}
	return nil
}

func agentCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("agent subcommand required: create, deploy")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	api := newClient(cfg)
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("agent create", flag.ExitOnError)
		file := fs.String("file", "", "agent JSON file")
		name := fs.String("name", "", "agent name override")
		description := fs.String("description", "", "description override")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		payload, err := readJSONFile(*file)
		if err != nil {
			return err
		}
		spec, ok := payload["spec"].(map[string]any)
		if !ok {
			spec = payload
		}
		agentName := firstNonEmpty(*name, stringValue(payload["name"]))
		if agentName == "" {
			return errors.New("agent name is required (in file or --name)")
		}
		agent, err := api.CreateAgent(ctx, cfg.ProjectID, agentName,
			firstNonEmpty(*description, stringValue(payload["description"])), spec)
		if err != nil {
			return err
		}
		fmt.Printf("agent %s version %d\n", agent.ID, agent.LatestVersion)
	case "deploy":
		fs := flag.NewFlagSet("agent deploy", flag.ExitOnError)
		agentID := fs.String("agent", "", "agent id")
		version := fs.Int("version", 0, "version (default latest)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *agentID == "" {
			return errors.New("--agent is required")
		}
		if *version == 0 {
			agent, err := api.GetAgent(ctx, cfg.ProjectID, *agentID)
			if err != nil {
				return err
			}
			*version = agent.LatestVersion
		}
		deployed, err := api.DeployAgent(ctx, cfg.ProjectID, *agentID, *version)
		if err != nil {
			return err
		}
		fmt.Printf("agent %s v%d %s\n", *agentID, deployed.Version, deployed.Status)
	default:
		return fmt.Errorf("unknown agent subcommand %q", args[0])
	}
	return nil
}

func workflowCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("workflow subcommand required: create, validate, deploy, run")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	api := newClient(cfg)
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("workflow create", flag.ExitOnError)
		file := fs.String("file", "", "workflow JSON file")
		name := fs.String("name", "", "workflow name override")
		deploy := fs.Bool("deploy", false, "publish version 1 after creation")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		payload, err := readJSONFile(*file)
		if err != nil {
			return err
		}
		graph, ok := payload["graph"].(map[string]any)
		if !ok {
			graph = payload
		}
		validation, err := api.ValidateWorkflow(ctx, cfg.ProjectID, graph)
		if err != nil {
			return err
		}
		if !validation.Valid {
			return errors.New("workflow graph is invalid")
		}
		workflow, err := api.CreateWorkflow(ctx, cfg.ProjectID,
			firstNonEmpty(*name, stringValue(payload["name"])),
			stringValue(payload["description"]), graph)
		if err != nil {
			return err
		}
		fmt.Printf("workflow %s version %d\n", workflow.ID, workflow.LatestVersion)
		if *deploy {
			version, err := api.DeployWorkflow(ctx, cfg.ProjectID, workflow.ID, workflow.LatestVersion)
			if err != nil {
				return err
			}
			fmt.Printf("workflow %s v%d %s\n", workflow.ID, version.Version, version.Status)
		}
	case "validate":
		fs := flag.NewFlagSet("workflow validate", flag.ExitOnError)
		file := fs.String("file", "", "workflow JSON file")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		payload, err := readJSONFile(*file)
		if err != nil {
			return err
		}
		graph, ok := payload["graph"].(map[string]any)
		if !ok {
			graph = payload
		}
		result, err := api.ValidateWorkflow(ctx, cfg.ProjectID, graph)
		if err != nil {
			return err
		}
		fmt.Printf("valid: %d nodes, %d edges\n", result.Nodes, result.Edges)
	case "deploy":
		fs := flag.NewFlagSet("workflow deploy", flag.ExitOnError)
		workflowID := fs.String("workflow", "", "workflow id")
		version := fs.Int("version", 0, "version (default latest)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *workflowID == "" {
			return errors.New("--workflow is required")
		}
		if *version == 0 {
			workflow, err := api.GetWorkflow(ctx, cfg.ProjectID, *workflowID)
			if err != nil {
				return err
			}
			*version = workflow.LatestVersion
		}
		deployed, err := api.DeployWorkflow(ctx, cfg.ProjectID, *workflowID, *version)
		if err != nil {
			return err
		}
		fmt.Printf("workflow %s v%d %s\n", *workflowID, deployed.Version, deployed.Status)
	case "run":
		fs := flag.NewFlagSet("workflow run", flag.ExitOnError)
		workflowID := fs.String("workflow", "", "workflow id")
		version := fs.Int("version", 0, "workflow version (default latest)")
		input := fs.String("input", "", "input JSON or @file")
		follow := fs.Bool("follow", false, "stream events until terminal")
		priority := fs.Int("priority", 0, "priority")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *workflowID == "" {
			return errors.New("--workflow is required")
		}
		if *version == 0 {
			workflow, err := api.GetWorkflow(ctx, cfg.ProjectID, *workflowID)
			if err != nil {
				return err
			}
			*version = workflow.LatestVersion
		}
		payload, err := readJSONValue(*input)
		if err != nil {
			return err
		}
		execution, err := api.ExecuteWorkflow(ctx, cfg.ProjectID, *workflowID, *version, payload,
			client.ExecuteOptions{Priority: *priority})
		if err != nil {
			return err
		}
		fmt.Printf("execution %s %s\n", execution.ID, execution.State)
		if *follow {
			if err := streamExecution(ctx, api, cfg.ProjectID, execution.ID, 0, true); err != nil {
				return err
			}
			final, err := api.WaitExecution(ctx, cfg.ProjectID, execution.ID, 10*time.Minute, 500*time.Millisecond)
			if err != nil {
				return err
			}
			fmt.Printf("final: %s\n", final.State)
			if final.State != "succeeded" {
				return fmt.Errorf("execution ended %s", final.State)
			}
		}
	default:
		return fmt.Errorf("unknown workflow subcommand %q", args[0])
	}
	return nil
}

func executionsCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("executions subcommand required: list, inspect, logs")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	api := newClient(cfg)
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("executions list", flag.ExitOnError)
		state := fs.String("state", "", "state filter")
		workflowID := fs.String("workflow", "", "workflow id filter")
		limit := fs.Int("limit", 20, "page size")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		page, err := api.ListExecutions(ctx, cfg.ProjectID, client.ExecutionFilter{
			State: *state, WorkflowID: *workflowID, Limit: *limit,
		})
		if err != nil {
			return err
		}
		for _, execution := range page.Items {
			fmt.Printf("%s  %-18s %-26s %s\n", execution.ID, execution.State,
				execution.WorkflowID, execution.CreatedAt.Format(time.RFC3339))
		}
	case "inspect":
		if len(args) < 2 {
			return errors.New("execution id is required")
		}
		execution, err := api.GetExecution(ctx, cfg.ProjectID, args[1])
		if err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(execution, "", "  ")
		fmt.Println(string(raw))
		tasks, err := api.ListTasks(ctx, cfg.ProjectID, execution.ID, "")
		if err != nil {
			return err
		}
		for _, task := range tasks {
			fmt.Printf("task %-16s %-12s %-18s attempts=%d\n", task.NodeID, task.NodeType, task.State,
				task.AttemptCount)
		}
	case "logs":
		if len(args) < 2 {
			return errors.New("execution id is required")
		}
		fs := flag.NewFlagSet("executions logs", flag.ExitOnError)
		after := fs.Int64("after", 0, "start after event sequence")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		return streamExecution(ctx, api, cfg.ProjectID, args[1], *after, false)
	default:
		return fmt.Errorf("unknown executions subcommand %q", args[0])
	}
	return nil
}

func streamExecution(ctx context.Context, api *client.Client, projectID, executionID string,
	after int64, compact bool) error {
	return api.StreamEvents(ctx, projectID, executionID, after, func(event client.Event) error {
		if compact {
			state := event.EntityID
			if event.ToState != nil && *event.ToState != "" {
				state = *event.ToState
			}
			fmt.Printf("%6d %-24s %-10s %s\n", event.Seq, event.EventType, event.EntityType, state)
		} else {
			raw, _ := json.Marshal(event)
			fmt.Println(string(raw))
		}
		return nil
	})
}

func workersCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("workers list is required")
	}
	fs := flag.NewFlagSet("workers list", flag.ExitOnError)
	pool := fs.String("pool", "", "pool filter")
	status := fs.String("status", "", "status filter")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	workers, err := newClient(cfg).ListWorkers(ctx, *pool, *status)
	if err != nil {
		return err
	}
	for _, worker := range workers {
		fmt.Printf("%s  %-16s %-10s %d/%d %s\n", worker.ID, worker.Pool, worker.Status,
			worker.AvailableSlots, worker.MaxConcurrency, worker.LastHeartbeatAt.Format(time.RFC3339))
	}
	return nil
}

func toolsCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("tools list is required")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	tools, err := newClient(cfg).ListTools(ctx, cfg.ProjectID)
	if err != nil {
		return err
	}
	for _, tool := range tools {
		fmt.Printf("%-20s v%d  %-8s %s\n", tool.ToolID, tool.Version, tool.Source, tool.Description)
	}
	return nil
}

func approvalsCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("approvals list is required")
	}
	fs := flag.NewFlagSet("approvals list", flag.ExitOnError)
	status := fs.String("status", "", "status filter")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	approvals, err := newClient(cfg).ListApprovals(ctx, cfg.ProjectID, *status, 50)
	if err != nil {
		return err
	}
	for _, approval := range approvals {
		fmt.Printf("%s  %-10s %-18s %s\n", approval.ID, approval.Status, approval.TaskID,
			approval.Prompt)
	}
	return nil
}

func usageCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("usage", flag.ExitOnError)
	from := fs.String("from", "", "RFC3339 start")
	to := fs.String("to", "", "RFC3339 end")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	var fromTime, toTime time.Time
	if *from != "" {
		fromTime, err = time.Parse(time.RFC3339, *from)
		if err != nil {
			return err
		}
	}
	if *to != "" {
		toTime, err = time.Parse(time.RFC3339, *to)
		if err != nil {
			return err
		}
	}
	usage, err := newClient(cfg).GetUsage(ctx, cfg.ProjectID, fromTime, toTime)
	if err != nil {
		return err
	}
	fmt.Printf("executions=%d succeeded=%d failed=%d attempts=%d tokens_in=%d tokens_out=%d duration_ms=%d\n",
		usage.Total.Executions, usage.Total.Succeeded, usage.Total.Failed, usage.Total.Attempts,
		usage.Total.TokensIn, usage.Total.TokensOut, usage.Total.DurationMS)
	return nil
}

func newClient(cfg config) *client.Client {
	return client.NewWithConfig(client.Config{
		BaseURL: cfg.APIURL, APIKey: cfg.APIKey, Token: cfg.Token,
		OrgID: cfg.OrgID, ProjectID: cfg.ProjectID,
	})
}

func readJSONFile(path string) (map[string]any, error) {
	if path == "" {
		return nil, errors.New("--file is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, errors.New("file exceeds 4 MiB")
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return value, nil
}

func readJSONValue(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	if strings.HasPrefix(raw, "@") {
		raw = raw[1:]
	}
	if strings.HasPrefix(raw, "{") {
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		return value, nil
	}
	return readJSONFile(raw)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

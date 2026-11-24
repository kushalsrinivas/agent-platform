//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	platform "github.com/agent-platform/agent-platform/sdk/go/client"
)

func TestDeveloperAPIKeySDKExitCriterion(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("developer_" + suffix)
	pool := "developer-" + suffix
	startRuntimeWorker(t, suffix, pool)
	org := client.request(http.MethodPost, "/v1/orgs", map[string]any{
		"name": "Developer Org", "slug": "developer-" + suffix,
	}, nil, http.StatusCreated)
	orgID := asString(t, org, "id")
	admin := adminPool(t)
	t.Cleanup(func() { cleanupAPITestOrg(t, admin, orgID) })
	project := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects", map[string]any{
		"name": "Developer Project", "slug": "developer",
	}, nil, http.StatusCreated)
	projectID := asString(t, project, "id")

	key := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/api-keys", map[string]any{
		"name": "ci", "project_id": projectID, "role": "operator",
	}, nil, http.StatusCreated)
	apiKey := asString(t, key, "key")
	if !strings.HasPrefix(apiKey, "apk_") {
		t.Fatalf("api key = %q", apiKey)
	}

	api := platform.NewWithConfig(platform.Config{
		BaseURL: client.base, APIKey: apiKey, OrgID: orgID, ProjectID: projectID,
	})
	ctx := context.Background()
	spec := map[string]any{
		"model":        map[string]any{"provider": "echo", "name": "echo-1", "credential_ref": "default"},
		"instructions": "Return the input.",
		"runtime":      map[string]any{"abi_version": 1, "image_digest": "sha256:" + strings.Repeat("a", 64)},
		"limits":       map[string]any{"max_turns": 4, "timeout_seconds": 60},
		"checkpoint":   map[string]any{"mode": "on_node_end", "require_checkpoint": false},
	}
	agent, err := api.CreateAgent(ctx, projectID, "developer-agent", "", spec)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	deployedAgent, err := api.DeployAgent(ctx, projectID, agent.ID, 1)
	if err != nil || deployedAgent.Status != "published" {
		t.Fatalf("deploy agent=%v err=%v", deployedAgent, err)
	}
	graph := map[string]any{
		"nodes": []any{
			map[string]any{"id": "start", "type": "start"},
			map[string]any{"id": "answer", "type": "agent",
				"agent":       map[string]any{"agent_id": agent.ID, "version": 1},
				"worker_pool": pool, "timeout_seconds": 60},
			map[string]any{"id": "end", "type": "end"},
		},
		"edges": []any{
			map[string]any{"from": "start", "to": "answer"},
			map[string]any{"from": "answer", "to": "end"},
		},
		"input_schema": map[string]any{"type": "object", "required": []any{"topic"}},
	}
	validation, err := api.ValidateWorkflow(ctx, projectID, graph)
	if err != nil || !validation.Valid {
		t.Fatalf("validate workflow=%v err=%v", validation, err)
	}
	workflow, err := api.CreateWorkflow(ctx, projectID, "developer-flow", "", graph)
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	deployed, err := api.DeployWorkflow(ctx, projectID, workflow.ID, workflow.LatestVersion)
	if err != nil || deployed.Status != "published" {
		t.Fatalf("deploy workflow=%v err=%v", deployed, err)
	}
	execution, err := api.ExecuteWorkflow(ctx, projectID, workflow.ID, deployed.Version,
		map[string]any{"topic": "phase 11"}, platform.ExecuteOptions{})
	if err != nil {
		t.Fatalf("execute workflow: %v", err)
	}
	final, err := api.WaitExecution(ctx, projectID, execution.ID, 60*time.Second, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("wait execution: %v", err)
	}
	if final.State != "succeeded" {
		t.Fatalf("execution state=%s failure=%v", final.State, final.FailureCode)
	}
	events, err := api.ListEvents(ctx, projectID, execution.ID, 0)
	if err != nil || len(events) == 0 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	tools, err := api.ListTools(ctx, projectID)
	if err != nil || len(tools) == 0 {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	workers, err := api.ListWorkers(ctx, "", "")
	if err != nil || len(workers) == 0 {
		t.Fatalf("workers=%v err=%v", workers, err)
	}
	usage, err := api.GetUsage(ctx, projectID, time.Time{}, time.Time{})
	if err != nil || usage.Total.Executions < 1 {
		t.Fatalf("usage=%v err=%v", usage, err)
	}
	environment, err := api.CreateEnvironment(ctx, projectID, "Development", "development", "development")
	if err != nil || environment.Slug != "development" {
		t.Fatalf("environment=%v err=%v", environment, err)
	}
	environments, err := api.ListEnvironments(ctx, projectID)
	if err != nil || len(environments) != 1 {
		t.Fatalf("environments=%v err=%v", environments, err)
	}
	keys := client.request(http.MethodGet, "/v1/orgs/"+orgID+"/api-keys", nil, nil, http.StatusOK)
	if items := asList(t, keys["items"], "api keys"); len(items) != 1 {
		t.Fatalf("api keys=%v", items)
	}
	if servers, err := api.ListMCPServers(ctx, projectID); err != nil {
		t.Fatalf("mcp servers: %v", err)
	} else {
		_ = servers
	}

	// Cancel a durable delay, then resume it as a new execution.
	delayGraph := map[string]any{
		"nodes": []any{
			map[string]any{"id": "start", "type": "start"},
			map[string]any{"id": "wait", "type": "delay", "config": map[string]any{
				"delay": map[string]any{"seconds": 8},
			}},
			map[string]any{"id": "end", "type": "end"},
		},
		"edges": []any{
			map[string]any{"from": "start", "to": "wait"},
			map[string]any{"from": "wait", "to": "end"},
		},
	}
	delayWorkflow, err := api.CreateWorkflow(ctx, projectID, "delay-flow", "", delayGraph)
	if err != nil {
		t.Fatalf("create delay workflow: %v", err)
	}
	if _, err := api.DeployWorkflow(ctx, projectID, delayWorkflow.ID, delayWorkflow.LatestVersion); err != nil {
		t.Fatalf("deploy delay workflow: %v", err)
	}
	delayExecution, err := api.ExecuteWorkflow(ctx, projectID, delayWorkflow.ID,
		delayWorkflow.LatestVersion, map[string]any{}, platform.ExecuteOptions{})
	if err != nil {
		t.Fatalf("execute delay workflow: %v", err)
	}
	time.Sleep(time.Second)
	if _, err := api.CancelExecution(ctx, projectID, delayExecution.ID, "resume test"); err != nil {
		t.Fatalf("cancel delay execution: %v", err)
	}
	cancelled, err := api.WaitExecution(ctx, projectID, delayExecution.ID, 30*time.Second, 300*time.Millisecond)
	if err != nil || cancelled.State != "cancelled" {
		t.Fatalf("cancelled=%v err=%v", cancelled, err)
	}
	resumed, err := api.ResumeExecution(ctx, projectID, delayExecution.ID)
	if err != nil || resumed.ID == delayExecution.ID {
		t.Fatalf("resumed=%v err=%v", resumed, err)
	}
	recovered, err := api.WaitExecution(ctx, projectID, resumed.ID, 60*time.Second, 500*time.Millisecond)
	if err != nil || recovered.State != "succeeded" {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}

	// A project-scoped key cannot read another project.
	other := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects", map[string]any{
		"name": "Other", "slug": "other",
	}, nil, http.StatusCreated)
	otherID := asString(t, other, "id")
	if _, err := api.GetExecution(ctx, otherID, execution.ID); err == nil {
		t.Fatal("project-scoped API key crossed into another project")
	}

	// Revoking the key invalidates it immediately.
	client.request(http.MethodDelete, "/v1/orgs/"+orgID+"/api-keys/"+asString(t, key, "id"),
		nil, nil, http.StatusOK)
	if _, err := api.ListOrganizations(ctx); err == nil {
		t.Fatal("revoked API key still authenticated")
	}
}

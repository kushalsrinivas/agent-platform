//go:build integration

package integration

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMCPDiscoveryInvocationAndPolicy(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("mcp_" + suffix)
	pool := "mcp-" + suffix

	startRuntimeWorker(t, suffix, pool)
	orgID, projectID, _ := createWorkerProject(t, client, suffix)

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	serverPath := filepath.Join(root, "runtimes", "python", "tests", "mcp_test_server.py")
	spec := echoAgentSpec()
	spec["mcp_servers"] = []any{map[string]any{
		"name": "fixture", "transport": "stdio",
		"command": runtimePython(t), "args": []any{"-u", serverPath},
		"timeout_seconds": 10,
		"tool_policies": map[string]any{
			"echo": map[string]any{"permissions": map[string]any{"read": true, "network": true}},
		},
	}}
	agentID := createAgentVersion(t, client, orgID, projectID, "mcp-agent", spec)
	servers := client.request(http.MethodGet,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/mcp/servers", nil, nil, http.StatusOK)
	if items := asList(t, servers["items"], "mcp servers"); len(items) != 1 {
		t.Fatalf("mcp registry returned %d servers, want 1", len(items))
	}

	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{
			"name": "mcp-flow",
			"version": map[string]any{"graph": map[string]any{
				"nodes": []any{map[string]any{
					"id": "use_mcp", "type": "agent",
					"agent":       map[string]any{"agent_id": agentID, "version": 1},
					"worker_pool": pool, "timeout_seconds": 120,
				}},
				"edges":        []any{},
				"policy":       map[string]any{"on_failure": "fail_fast"},
				"input_schema": map[string]any{"type": "object", "required": []any{"topic"}},
			}},
		}, nil, http.StatusCreated)
	workflowID := asString(t, workflow, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows/"+workflowID+"/versions/1:publish",
		nil, nil, http.StatusOK)

	run := func(key, topic string) string {
		execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
			map[string]any{
				"workflow_id": workflowID, "workflow_version": 1,
				"input": map[string]any{"topic": topic},
			}, map[string]string{"Idempotency-Key": key}, http.StatusAccepted)
		executionID := asString(t, execution, "id")
		waitExecutionState(t, client, fixtureExecution{
			orgID: orgID, projectID: projectID, workflowID: workflowID, executionID: executionID,
		}, "succeeded")
		var report string
		if err := adminPool(t).QueryRow(context.Background(), `
			SELECT COALESCE(a.result_inline->>'report', '')
			FROM execution.execution_tasks t
			JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
			WHERE t.execution_id = $1`, executionID).Scan(&report); err != nil {
			t.Fatalf("load MCP report: %v", err)
		}
		return report
	}

	report := run("mcp-echo-"+suffix, `tool:mcp:{"server":"fixture","tool":"echo","arguments":{"text":"hello"}}`)
	if !strings.Contains(report, "structured") || !strings.Contains(report, "hello") {
		t.Fatalf("MCP echo output missing from report: %s", report)
	}

	denied := run("mcp-destructive-"+suffix,
		`tool:mcp:{"server":"fixture","tool":"delete_record","arguments":{"id":"42"}}`)
	if !strings.Contains(denied, "PERMISSION_DENIED") {
		t.Fatalf("destructive MCP call was not denied: %s", denied)
	}
}

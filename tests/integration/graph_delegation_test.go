//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func createAgentVersion(t *testing.T, client *apiClient, orgID, projectID, name string, spec map[string]any) string {
	t.Helper()
	agent := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/agents", map[string]any{
		"name": name, "description": "phase 10 delegation test",
		"version": map[string]any{"spec": spec},
	}, nil, http.StatusCreated)
	agentID := asString(t, agent, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/agents/"+agentID+"/versions/1:publish",
		nil, nil, http.StatusOK)
	return agentID
}

func echoAgentSpec() map[string]any {
	return map[string]any{
		"model": map[string]any{
			"provider": "echo", "name": "echo-1", "credential_ref": "default",
		},
		"instructions": "Return the input.",
		"runtime": map[string]any{
			"abi_version": 1, "image_digest": "sha256:" + strings.Repeat("a", 64),
		},
		"limits":     map[string]any{"max_turns": 4, "timeout_seconds": 120},
		"checkpoint": map[string]any{"mode": "on_node_end", "require_checkpoint": false},
	}
}

func TestSupervisorDelegatesConcurrentlyAndRecovers(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("delegation_" + suffix)
	pool := "delegation-" + suffix

	// Independent worker processes make the concurrency assertion independent of runtime startup jitter.
	for _, name := range []string{"", "-b", "-c", "-d"} {
		startRuntimeWorkerCapacityEnv(t, suffix+name, pool, 1,
			[]string{"AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS=2"})
	}

	orgID, projectID, _ := createWorkerProject(t, client, suffix)
	var executionID string
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		admin := adminPool(t)
		var state, failure string
		_ = admin.QueryRow(context.Background(),
			`SELECT state, COALESCE(failure_code, '') FROM execution.executions WHERE id = $1`, executionID).
			Scan(&state, &failure)
		t.Logf("cleanup state=%s failure=%s", state, failure)
		rows, err := admin.Query(context.Background(), `
			SELECT t.node_id, t.state, COALESCE(t.last_failure_code, ''),
			       COALESCE(a.error_message, '') || ' schema=' || COALESCE(t.output_schema::text, 'NULL')
			FROM execution.execution_tasks t
			LEFT JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
			WHERE t.execution_id = $1 OR t.execution_id IN (
				SELECT id FROM execution.executions WHERE parent_execution_id = $1
			) ORDER BY t.execution_id, t.node_id`, executionID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var node, taskState, code, message string
				_ = rows.Scan(&node, &taskState, &code, &message)
				t.Logf("cleanup task node=%s state=%s code=%s message=%s", node, taskState, code, message)
			}
		}
	})
	research := createAgentVersion(t, client, orgID, projectID, "research", echoAgentSpec())
	analysis := createAgentVersion(t, client, orgID, projectID, "analysis", echoAgentSpec())
	// A delegated child whose declared output schema cannot be satisfied fails deterministically.
	codingSpec := echoAgentSpec()
	codingSpec["output_schema"] = map[string]any{"type": "object", "required": []any{"missing_field"}}
	coding := createAgentVersion(t, client, orgID, projectID, "coding", codingSpec)
	fallback := createAgentVersion(t, client, orgID, projectID, "fallback", echoAgentSpec())
	admin := adminPool(t)

	supervisorSpec := echoAgentSpec()
	supervisorSpec["instructions"] = "Delegate work and aggregate the results."
	supervisorSpec["orchestration"] = map[string]any{
		"allowed_agents": []any{
			map[string]any{"agent_id": research, "version": 1, "name": "research"},
			map[string]any{"agent_id": analysis, "version": 1, "name": "analysis"},
			map[string]any{"agent_id": coding, "version": 1, "name": "coding"},
			map[string]any{"agent_id": fallback, "version": 1, "name": "fallback"},
		},
		"max_children": 4, "max_depth": 3, "max_parallel": 4, "max_retries": 0,
	}
	supervisor := createAgentVersion(t, client, orgID, projectID, "supervisor", supervisorSpec)

	delegate := map[string]any{
		"tasks": []any{
			map[string]any{"agent_id": research, "version": 1, "task": map[string]any{"topic": "research"}},
			map[string]any{"agent_id": analysis, "version": 1, "task": map[string]any{"topic": "analysis"}},
			map[string]any{"agent_id": coding, "version": 1, "task": map[string]any{"topic": "coding"}},
		},
		"fallback_agent_id": fallback,
		"fallback_version":  1,
		"wait":              true,
		"wait_seconds":      120,
	}
	topicPayload, err := json.Marshal(delegate)
	if err != nil {
		t.Fatal(err)
	}
	topic := "tool:delegate:" + string(topicPayload)

	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{
			"name": "supervisor-flow",
			"version": map[string]any{"graph": map[string]any{
				"nodes": []any{map[string]any{
					"id": "supervisor", "type": "agent",
					"agent":       map[string]any{"agent_id": supervisor, "version": 1},
					"worker_pool": pool, "timeout_seconds": 180,
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

	execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		map[string]any{
			"workflow_id": workflowID, "workflow_version": 1,
			"input": map[string]any{"topic": topic},
		}, map[string]string{"Idempotency-Key": "delegation-" + suffix}, http.StatusAccepted)
	executionID = asString(t, execution, "id")
	fixture := fixtureExecution{orgID: orgID, projectID: projectID, workflowID: workflowID, executionID: executionID}
	waitExecutionStateFor(t, client, fixture, "succeeded", 120*time.Second)

	var report string
	if err := admin.QueryRow(context.Background(), `
		SELECT COALESCE(a.result_inline->>'report', '')
		FROM execution.execution_tasks t
		JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
		WHERE t.execution_id = $1 AND t.node_id = 'supervisor'`, executionID).Scan(&report); err != nil {
		t.Fatalf("load supervisor report: %v", err)
	}
	if !strings.Contains(report, "recovered") || !strings.Contains(report, "fallback") {
		t.Fatalf("supervisor report did not aggregate delegation results: %s", report)
	}

	var delegations, succeeded, failed, children, depthOne int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*),
		       count(*) FILTER (WHERE status = 'succeeded'),
		       count(*) FILTER (WHERE status = 'failed'),
		       count(*) FILTER (WHERE child_execution_id IS NOT NULL),
		       count(*) FILTER (WHERE parent_execution_id = $1)
		FROM execution.delegations WHERE root_execution_id = $1`, executionID).
		Scan(&delegations, &succeeded, &failed, &children, &depthOne); err != nil {
		t.Fatalf("load delegation counts: %v", err)
	}
	if delegations != 4 || children != 4 || succeeded != 3 || failed != 1 || depthOne != 4 {
		t.Fatalf("delegations=%d children=%d succeeded=%d failed=%d depth=%d, want 4/4/3/1/4",
			delegations, children, succeeded, failed, depthOne)
	}

	var failedChildren, nestedDepth int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE state = 'failed'),
		       COALESCE(max(delegation_depth), 0)
		FROM execution.executions WHERE parent_execution_id = $1`, executionID).
		Scan(&failedChildren, &nestedDepth); err != nil {
		t.Fatalf("load child states: %v", err)
	}
	if failedChildren != 1 || nestedDepth != 1 {
		t.Fatalf("failed children=%d depth=%d, want one failed child at depth 1", failedChildren, nestedDepth)
	}

	delegationsAPI := client.request(http.MethodGet,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/executions/"+executionID+"/delegations",
		nil, nil, http.StatusOK)
	if items := asList(t, delegationsAPI["items"], "delegations"); len(items) != 4 {
		t.Fatalf("delegation API returned %d rows, want 4", len(items))
	}

	var overlapping int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*)
		FROM execution.executions a
		JOIN execution.executions b ON a.id < b.id
		WHERE a.parent_execution_id = $1 AND b.parent_execution_id = $1
		  AND a.started_at IS NOT NULL AND b.started_at IS NOT NULL
		  AND a.started_at < COALESCE(b.ended_at, now())
		  AND b.started_at < COALESCE(a.ended_at, now())`, executionID).Scan(&overlapping); err != nil {
		t.Fatalf("load child overlap: %v", err)
	}
	if overlapping < 1 {
		t.Fatal("delegated children did not execute concurrently")
	}

}

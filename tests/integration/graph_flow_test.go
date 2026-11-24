//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func graphNode(id, nodeType string, extra map[string]any) map[string]any {
	node := map[string]any{"id": id, "type": nodeType}
	for key, value := range extra {
		node[key] = value
	}
	return node
}

func TestGraphEngineTenNodeWorkflow(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("graph_flow_" + suffix)
	pool := "graph-flow-" + suffix

	orgID, projectID, agentID := createWorkerProject(t, client, suffix)
	agent := map[string]any{"agent_id": agentID, "version": 1}
	nodes := []map[string]any{
		graphNode("start", "start", nil),
		graphNode("research", "agent", map[string]any{"agent": agent, "timeout_seconds": 60,
			"retry": map[string]any{"max_attempts": 2, "retry_on": []any{"worker_lost", "transient"}}}),
		graphNode("fan_out", "parallel", nil),
		graphNode("web", "tool", map[string]any{"tool": map[string]any{"tool_id": "tool_web_search", "version": 1}}),
		graphNode("db", "tool", map[string]any{"tool": map[string]any{"tool_id": "tool_web_search", "version": 1}}),
		graphNode("file", "tool", map[string]any{"tool": map[string]any{"tool_id": "tool_web_search", "version": 1}}),
		graphNode("join", "join", map[string]any{"config": map[string]any{"join": map[string]any{"mode": "all"}}}),
		graphNode("analysis", "agent", map[string]any{"agent": agent, "timeout_seconds": 60}),
		graphNode("quality", "condition", map[string]any{"config": map[string]any{"expression": "input.score >= 80"}}),
		graphNode("approve", "transform", map[string]any{"config": map[string]any{"mapping": map[string]any{
			"decision": "'approved'", "report": "outputs.analysis.report",
		}}}),
		graphNode("revise", "agent", map[string]any{"agent": agent, "timeout_seconds": 60}),
		graphNode("end", "end", nil),
	}
	for _, node := range nodes {
		if node["type"] == "agent" || node["type"] == "llm" || node["type"] == "tool" {
			node["worker_pool"] = pool
		}
	}
	edges := []map[string]any{
		{"from": "start", "to": "research"},
		{"from": "research", "to": "fan_out"},
		{"from": "fan_out", "to": "web", "priority": 3},
		{"from": "fan_out", "to": "db", "priority": 2},
		{"from": "fan_out", "to": "file", "priority": 1},
		{"from": "web", "to": "join"},
		{"from": "db", "to": "join"},
		{"from": "file", "to": "join"},
		{"from": "join", "to": "analysis", "data_mapping": map[string]any{
			"research_report": "outputs.research.report",
			"branch_count":    "size(outputs.join.branches)",
		}},
		{"from": "analysis", "to": "quality"},
		{"from": "quality", "to": "approve", "when": "outputs.quality.result == true"},
		{"from": "quality", "to": "revise", "when": "outputs.quality.result == false"},
		{"from": "approve", "to": "end"},
		{"from": "revise", "to": "end"},
	}
	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{"name": "graph-engine", "version": map[string]any{"graph": map[string]any{
			"nodes": nodes, "edges": edges,
			"policy": map[string]any{"on_failure": "fail_fast"},
			"input_schema": map[string]any{
				"type": "object", "required": []any{"topic", "score"},
				"properties": map[string]any{"topic": map[string]any{"type": "string"}, "score": map[string]any{"type": "integer"}},
			},
		}}}, nil, http.StatusCreated)
	workflowID := asString(t, workflow, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows/"+workflowID+"/versions/1:publish",
		nil, nil, http.StatusOK)

	worker := startRuntimeWorkerCapacityEnv(t, suffix, pool, 4, nil)
	fixture := fixtureExecution{orgID: orgID, projectID: projectID, workflowID: workflowID}
	submit := func(key string, score int) {
		execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
			map[string]any{
				"workflow_id": workflowID, "workflow_version": 1,
				"input": map[string]any{"topic": "graph workflow", "score": score},
			}, map[string]string{"Idempotency-Key": key}, http.StatusAccepted)
		fixture.executionID = asString(t, execution, "id")
		fixture.taskIDs = loadTaskIDs(t, fixture.executionID)
		waitExecutionStateFor(t, client, fixture, "succeeded", 90*time.Second)
	}

	submit("graph-approve-"+suffix, 92)
	admin := adminPool(t)
	states := map[string]string{}
	rows, err := admin.Query(context.Background(), `
		SELECT node_id, state FROM execution.execution_tasks WHERE execution_id = $1`, fixture.executionID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var nodeID, state string
		if err := rows.Scan(&nodeID, &state); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		states[nodeID] = state
	}
	rows.Close()
	if len(states) != 12 {
		t.Fatalf("task count = %d, want 12", len(states))
	}
	for _, nodeID := range []string{"start", "research", "fan_out", "web", "db", "file", "join", "analysis", "quality", "approve", "end"} {
		if states[nodeID] != "succeeded" {
			t.Fatalf("node %s state=%s, want succeeded (all=%v)", nodeID, states[nodeID], states)
		}
	}
	if states["revise"] != "skipped" {
		t.Fatalf("revise state=%s, want skipped", states["revise"])
	}
	var analysisReport string
	if err := admin.QueryRow(context.Background(), `
		SELECT COALESCE(a.result_inline->>'report', '')
		FROM execution.execution_tasks t JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
		WHERE t.execution_id = $1 AND t.node_id = 'analysis'`, fixture.executionID).Scan(&analysisReport); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(analysisReport, "research_report") || !strings.Contains(analysisReport, "branch_count") {
		t.Fatalf("mapped input did not reach analysis: %q", analysisReport)
	}

	// Re-run the same graph with a failing quality score; the other branch must activate.
	submit("graph-revise-"+suffix, 40)
	var reviseState, approveState string
	if err := admin.QueryRow(context.Background(), `
		SELECT
		  max(state) FILTER (WHERE node_id = 'revise'),
		  max(state) FILTER (WHERE node_id = 'approve')
		FROM execution.execution_tasks WHERE execution_id = $1`, fixture.executionID).
		Scan(&reviseState, &approveState); err != nil {
		t.Fatal(err)
	}
	if reviseState != "succeeded" || approveState != "skipped" {
		t.Fatalf("conditional branch states revise=%s approve=%s", reviseState, approveState)
	}

	// Crash a worker during the research node, then let a replacement worker finish the same graph. The research
	// node's retry policy allows the orchestrator to create a new attempt from durable state.
	worker.crash()
	dead := startRuntimeWorkerCapacityEnv(t, suffix+"dead", pool, 4,
		[]string{"AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS=8"})
	crashFixture := fixtureExecution{orgID: orgID, projectID: projectID, workflowID: workflowID}
	submitWithKey := func(key string, score int) {
		execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
			map[string]any{
				"workflow_id": workflowID, "workflow_version": 1,
				"input": map[string]any{"topic": "graph recovery", "score": score},
			}, map[string]string{"Idempotency-Key": key}, http.StatusAccepted)
		crashFixture.executionID = asString(t, execution, "id")
		crashFixture.taskIDs = loadTaskIDs(t, crashFixture.executionID)
	}
	submitWithKey("graph-recovery-"+suffix, 88)
	deadline := time.Now().Add(20 * time.Second)
	var researchTaskID string
	for time.Now().Before(deadline) {
		if err := admin.QueryRow(context.Background(), `
			SELECT id FROM execution.execution_tasks
			WHERE execution_id = $1 AND node_id = 'research' AND state = 'running'`,
			crashFixture.executionID).Scan(&researchTaskID); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if researchTaskID == "" {
		t.Fatal("research task never reached running")
	}
	dead.crash()
	forceWorkerGone(t, dead.id, researchTaskID)
	startRuntimeWorkerCapacityEnv(t, suffix+"live", pool, 4, nil)
	waitExecutionStateFor(t, client, crashFixture, "succeeded", 90*time.Second)
	var researchAttempts int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM execution.task_attempts
		WHERE execution_id = $1 AND task_id = $2`,
		crashFixture.executionID, researchTaskID).Scan(&researchAttempts); err != nil {
		t.Fatal(err)
	}
	if researchAttempts < 2 {
		t.Fatalf("research attempts = %d, want a recovered retry", researchAttempts)
	}
}

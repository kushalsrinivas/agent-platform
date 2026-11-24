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

func createGraphWorkflow(t *testing.T, client *apiClient, orgID, projectID, name string,
	nodes []map[string]any, edges []map[string]any, inputSchema map[string]any) string {
	t.Helper()
	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{"name": name, "version": map[string]any{"graph": map[string]any{
			"nodes": nodes, "edges": edges, "policy": map[string]any{"on_failure": "fail_fast"},
			"input_schema": inputSchema,
		}}}, nil, http.StatusCreated)
	workflowID := asString(t, workflow, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows/"+workflowID+"/versions/1:publish",
		nil, nil, http.StatusOK)
	return workflowID
}

func runGraphExecution(t *testing.T, client *apiClient, orgID, projectID, workflowID, key string,
	input map[string]any) fixtureExecution {
	t.Helper()
	execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		map[string]any{"workflow_id": workflowID, "workflow_version": 1, "input": input},
		map[string]string{"Idempotency-Key": key}, http.StatusAccepted)
	return fixtureExecution{orgID: orgID, projectID: projectID, workflowID: workflowID,
		executionID: asString(t, execution, "id"), taskIDs: loadTaskIDs(t, asString(t, execution, "id"))}
}

func TestGraphSubworkflowLoopApprovalDelayReplay(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("graph_children_" + suffix)
	pool := "graph-children-" + suffix
	orgID, projectID, agentID := createWorkerProject(t, client, suffix)
	agent := map[string]any{"agent_id": agentID, "version": 1}

	bodyNodes := []map[string]any{
		{"id": "start", "type": "start"},
		{"id": "work", "type": "agent", "agent": agent, "worker_pool": pool, "timeout_seconds": 60},
		{"id": "end", "type": "end"},
	}
	bodyEdges := []map[string]any{{"from": "start", "to": "work"}, {"from": "work", "to": "end"}}
	bodyInput := map[string]any{"type": "object", "required": []any{"topic"}}
	bodyWorkflowID := createGraphWorkflow(t, client, orgID, projectID, "body-workflow", bodyNodes, bodyEdges, bodyInput)

	startRuntimeWorkerCapacityEnv(t, suffix, pool, 4, nil)

	// Subworkflow: one child execution, parent waits and returns its output.
	childNodes := []map[string]any{
		{"id": "start", "type": "start"},
		{"id": "child", "type": "subworkflow", "config": map[string]any{
			"workflow": map[string]any{"workflow_id": bodyWorkflowID, "version": 1},
			"input":    map[string]any{"topic": "input.topic"},
		}},
		{"id": "end", "type": "end"},
	}
	childEdges := []map[string]any{{"from": "start", "to": "child"}, {"from": "child", "to": "end"}}
	childWorkflowID := createGraphWorkflow(t, client, orgID, projectID, "parent-subworkflow", childNodes, childEdges,
		map[string]any{"type": "object", "required": []any{"topic"}})
	subFixture := runGraphExecution(t, client, orgID, projectID, childWorkflowID, "sub-"+suffix,
		map[string]any{"topic": "child topic"})
	waitExecutionStateFor(t, client, subFixture, "succeeded", 90*time.Second)

	var childCount int
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT count(*) FROM execution.executions WHERE parent_execution_id = $1`,
		subFixture.executionID).Scan(&childCount); err != nil {
		t.Fatal(err)
	}
	if childCount != 1 {
		t.Fatalf("subworkflow child count = %d, want 1", childCount)
	}

	// Bounded loop: the body runs twice and exits when iteration >= 2.
	loopNodes := []map[string]any{
		{"id": "start", "type": "start"},
		{"id": "loop", "type": "loop", "config": map[string]any{
			"workflow": map[string]any{"workflow_id": bodyWorkflowID, "version": 1},
			"input":    map[string]any{"topic": "input.topic"},
			"loop":     map[string]any{"max_iterations": 3, "exit_when": "iteration >= 2"},
		}},
		{"id": "end", "type": "end"},
	}
	loopEdges := []map[string]any{{"from": "start", "to": "loop"}, {"from": "loop", "to": "end"}}
	loopWorkflowID := createGraphWorkflow(t, client, orgID, projectID, "parent-loop", loopNodes, loopEdges,
		map[string]any{"type": "object", "required": []any{"topic"}})
	loopFixture := runGraphExecution(t, client, orgID, projectID, loopWorkflowID, "loop-"+suffix,
		map[string]any{"topic": "loop topic"})
	waitExecutionStateFor(t, client, loopFixture, "succeeded", 120*time.Second)
	var loopChildren, iterations int
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT count(*) FROM execution.executions WHERE parent_execution_id = $1`,
		loopFixture.executionID).Scan(&loopChildren); err != nil {
		t.Fatal(err)
	}
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT COALESCE((result_inline->>'iterations')::int, 0)
		FROM execution.execution_tasks WHERE execution_id = $1 AND node_id = 'loop'`,
		loopFixture.executionID).Scan(&iterations); err != nil {
		t.Fatal(err)
	}
	if loopChildren != 2 || iterations != 2 {
		t.Fatalf("loop children=%d iterations=%d, want 2/2", loopChildren, iterations)
	}

	// Approval: the task waits for an operator decision.
	approvalNodes := []map[string]any{
		{"id": "start", "type": "start"},
		{"id": "review", "type": "approval", "config": map[string]any{"approval": map[string]any{"prompt": "ship?"}}},
		{"id": "end", "type": "end"},
	}
	approvalEdges := []map[string]any{{"from": "start", "to": "review"}, {"from": "review", "to": "end"}}
	approvalWorkflowID := createGraphWorkflow(t, client, orgID, projectID, "approval-flow", approvalNodes, approvalEdges,
		map[string]any{"type": "object"})
	approvalFixture := runGraphExecution(t, client, orgID, projectID, approvalWorkflowID, "approval-"+suffix,
		map[string]any{})
	time.Sleep(2 * time.Second)
	var reviewTaskID string
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT id FROM execution.execution_tasks WHERE execution_id = $1 AND node_id = 'review'`,
		approvalFixture.executionID).Scan(&reviewTaskID); err != nil {
		t.Fatal(err)
	}
	taskPath := "/v1/orgs/" + orgID + "/projects/" + projectID + "/executions/" + approvalFixture.executionID +
		"/tasks/" + reviewTaskID + ":approve"
	client.request(http.MethodPost, taskPath, map[string]any{"comment": "approved in test"}, nil, http.StatusAccepted)
	waitExecutionStateFor(t, client, approvalFixture, "succeeded", 60*time.Second)

	// Durable delay: the node is resolved after persisted ready_at, not by sleeping in a worker.
	delayNodes := []map[string]any{
		{"id": "start", "type": "start"},
		{"id": "pause", "type": "delay", "config": map[string]any{"delay": map[string]any{"seconds": 1}}},
		{"id": "end", "type": "end"},
	}
	delayEdges := []map[string]any{{"from": "start", "to": "pause"}, {"from": "pause", "to": "end"}}
	delayWorkflowID := createGraphWorkflow(t, client, orgID, projectID, "delay-flow", delayNodes, delayEdges,
		map[string]any{"type": "object"})
	delayFixture := runGraphExecution(t, client, orgID, projectID, delayWorkflowID, "delay-"+suffix,
		map[string]any{})
	waitExecutionStateFor(t, client, delayFixture, "succeeded", 60*time.Second)

	// Historical replay never runs anything; re-execution reuses an explicitly selected prior output.
	replayPath := "/v1/orgs/" + orgID + "/projects/" + projectID + "/executions/" + subFixture.executionID + "/replay"
	replay := client.request(http.MethodGet, replayPath, nil, nil, http.StatusOK)
	if replay["mode"] != "historical" {
		t.Fatalf("historical replay mode = %v", replay["mode"])
	}
	bodyFixture := runGraphExecution(t, client, orgID, projectID, bodyWorkflowID, "replay-source-"+suffix,
		map[string]any{"topic": "replay source"})
	waitExecutionStateFor(t, client, bodyFixture, "succeeded", 60*time.Second)
	var output string
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT COALESCE(a.result_inline->>'report', '')
		FROM execution.execution_tasks t JOIN execution.task_attempts a ON a.id = t.latest_attempt_id
		WHERE t.execution_id = $1 AND t.node_id = 'work'`, bodyFixture.executionID).Scan(&output); err != nil {
		t.Fatal(err)
	}
	replayPath = "/v1/orgs/" + orgID + "/projects/" + projectID + "/executions/" + bodyFixture.executionID + ":replay"
	reexecuted := client.request(http.MethodPost, replayPath, map[string]any{
		"input": map[string]any{"topic": "replay source"}, "reuse_outputs": []any{"work"},
	}, map[string]string{"Idempotency-Key": "reexec-" + suffix}, http.StatusAccepted)
	replayFixture := fixtureExecution{orgID: orgID, projectID: projectID, workflowID: bodyWorkflowID,
		executionID: asString(t, reexecuted, "id")}
	waitExecutionStateFor(t, client, replayFixture, "succeeded", 60*time.Second)
	var seeded string
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT COALESCE(result_inline->>'report', '') FROM execution.execution_tasks
		WHERE execution_id = $1 AND node_id = 'work'`, replayFixture.executionID).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != output {
		t.Fatalf("seeded output %q does not match prior output %q", seeded, output)
	}
}

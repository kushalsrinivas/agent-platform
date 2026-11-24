//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type apiClient struct {
	t     *testing.T
	base  string
	token string
}

func newAPIClient(t *testing.T) *apiClient {
	t.Helper()
	base := os.Getenv("API_URL")
	if base == "" {
		base = "http://localhost:8080"
	}
	return &apiClient{t: t, base: strings.TrimRight(base, "/")}
}

func (c *apiClient) request(method, path string, body any, headers map[string]string, wantStatus int) map[string]any {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))

	var payload map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			c.t.Fatalf("%s %s: decode response %q: %v", method, path, raw, err)
		}
	}
	if response.StatusCode != wantStatus {
		c.t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.StatusCode, wantStatus, raw)
	}
	return payload
}

func (c *apiClient) devToken(principal string) string {
	c.t.Helper()
	payload := c.request(http.MethodPost, "/v1/dev/token", map[string]any{"principal_id": principal}, nil, http.StatusOK)
	token, _ := payload["access_token"].(string)
	if token == "" {
		c.t.Fatal("dev token response has no access_token")
	}
	return token
}

func asMap(t *testing.T, value any, field string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %T", field, value)
	}
	return result
}

func asList(t *testing.T, value any, field string) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is not an array: %T", field, value)
	}
	return result
}

func asString(t *testing.T, object map[string]any, field string) string {
	t.Helper()
	value, ok := object[field].(string)
	if !ok || value == "" {
		t.Fatalf("%s is missing or not a string: %v", field, object[field])
	}
	return value
}

func asInt(t *testing.T, object map[string]any, field string) int {
	t.Helper()
	value, ok := object[field].(float64)
	if !ok {
		t.Fatalf("%s is missing or not a number: %v", field, object[field])
	}
	return int(value)
}

func TestAPIControlPlaneFlow(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	principal := "user_" + suffix
	client.token = client.devToken(principal)

	org := client.request(http.MethodPost, "/v1/orgs", map[string]any{
		"name": "API Flow Org", "slug": "api-flow-" + suffix,
	}, nil, http.StatusCreated)
	orgID := asString(t, org, "id")
	admin := adminPool(t)
	t.Cleanup(func() { cleanupAPITestOrg(t, admin, orgID) })

	orgList := client.request(http.MethodGet, "/v1/orgs", nil, nil, http.StatusOK)
	orgItems := asList(t, orgList["items"], "organizations")
	if len(orgItems) != 1 || asString(t, asMap(t, orgItems[0], "organization"), "id") != orgID {
		t.Fatalf("organization list mismatch: %v", orgList)
	}

	list := client.request(http.MethodGet, "/v1/orgs/"+orgID+"/projects", nil, nil, http.StatusOK)
	if len(asList(t, list["items"], "projects")) != 0 {
		t.Fatal("new organization must have no projects")
	}

	project := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects", map[string]any{
		"name": "Research", "slug": "research",
	}, nil, http.StatusCreated)
	projectID := asString(t, project, "id")

	members := client.request(http.MethodGet, "/v1/orgs/"+orgID+"/members", nil, nil, http.StatusOK)
	memberItems := asList(t, members["items"], "members")
	if len(memberItems) != 1 {
		t.Fatalf("expected one owner membership, got %d", len(memberItems))
	}
	if role := asMap(t, memberItems[0], "member")["role"]; role != "owner" {
		t.Fatalf("creator role = %v, want owner", role)
	}

	agent := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/agents", map[string]any{
		"name":        "researcher",
		"description": "Phase 04 API flow agent",
		"version": map[string]any{"spec": map[string]any{
			"model": map[string]any{
				"provider": "echo", "name": "echo-1", "credential_ref": "default",
			},
			"instructions": "You are a careful research assistant.",
			"runtime": map[string]any{
				"abi_version": 1, "image_digest": "sha256:" + strings.Repeat("a", 64),
			},
			"limits":     map[string]any{"max_turns": 8, "timeout_seconds": 300},
			"checkpoint": map[string]any{"mode": "on_node_end", "require_checkpoint": false},
		}},
	}, nil, http.StatusCreated)
	agentID := asString(t, agent, "id")

	agentPath := "/v1/orgs/" + orgID + "/projects/" + projectID + "/agents/" + agentID
	publishedAgent := client.request(http.MethodPost, agentPath+"/versions/1:publish", nil, nil, http.StatusOK)
	if publishedAgent["status"] != "published" {
		t.Fatalf("agent status = %v, want published", publishedAgent["status"])
	}

	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows", map[string]any{
		"name": "research-brief",
		"version": map[string]any{"graph": map[string]any{
			"nodes": []any{
				map[string]any{
					"id": "collect", "type": "tool",
					"tool":            map[string]any{"tool_id": "tool_web_search", "version": 1},
					"timeout_seconds": 120,
				},
				map[string]any{
					"id": "plan", "type": "agent",
					"agent":      map[string]any{"agent_id": agentID, "version": 1},
					"input_from": []any{"collect"}, "timeout_seconds": 300,
				},
			},
			"edges":        []any{map[string]any{"from": "collect", "to": "plan"}},
			"policy":       map[string]any{"on_failure": "fail_fast"},
			"input_schema": map[string]any{"type": "object", "required": []any{"topic"}},
		}},
	}, nil, http.StatusCreated)
	workflowID := asString(t, workflow, "id")
	workflowPath := "/v1/orgs/" + orgID + "/projects/" + projectID + "/workflows/" + workflowID
	publishedWorkflow := client.request(http.MethodPost, workflowPath+"/versions/1:publish", nil, nil, http.StatusOK)
	if publishedWorkflow["status"] != "published" {
		t.Fatalf("workflow status = %v, want published", publishedWorkflow["status"])
	}

	idempotency := "req-" + suffix
	body := map[string]any{
		"workflow_id": workflowID, "workflow_version": 1,
		"input": map[string]any{"topic": "Durable execution"},
	}
	execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		body, map[string]string{"Idempotency-Key": idempotency}, http.StatusAccepted)
	executionID := asString(t, execution, "id")
	if state := execution["state"]; state != "pending" && state != "running" && state != "succeeded" {
		t.Fatalf("execution state = %v, want an accepted state", state)
	}
	counts := asMap(t, execution["counts"], "counts")
	if asInt(t, counts, "total") != 2 {
		t.Fatalf("counts = %v, want total=2", counts)
	}

	replayed := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		body, map[string]string{"Idempotency-Key": idempotency}, http.StatusAccepted)
	if asString(t, replayed, "id") != executionID {
		t.Fatal("idempotent replay returned a different execution")
	}

	conflict := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		map[string]any{"workflow_id": workflowID, "workflow_version": 1, "input": map[string]any{"topic": "different"}},
		map[string]string{"Idempotency-Key": idempotency}, http.StatusConflict)
	if code := asMap(t, conflict["error"], "error")["code"]; code != "idempotency_conflict" {
		t.Fatalf("conflict code = %v, want idempotency_conflict", code)
	}

	invalidInput := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		map[string]any{"workflow_id": workflowID, "workflow_version": 1, "input": map[string]any{}},
		map[string]string{"Idempotency-Key": "invalid-" + suffix}, http.StatusUnprocessableEntity)
	if code := asMap(t, invalidInput["error"], "error")["code"]; code != "validation_failed" {
		t.Fatalf("invalid-input code = %v, want validation_failed", code)
	}

	executionPath := "/v1/orgs/" + orgID + "/projects/" + projectID + "/executions/" + executionID
	fetched := client.request(http.MethodGet, executionPath, nil, nil, http.StatusOK)
	if asString(t, fetched, "id") != executionID || asInt(t, asMap(t, fetched["counts"], "counts"), "total") != 2 {
		t.Fatalf("execution readback mismatch: %v", fetched)
	}

	tasks := client.request(http.MethodGet, executionPath+"/tasks", nil, nil, http.StatusOK)
	taskItems := asList(t, tasks["items"], "tasks")
	if len(taskItems) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(taskItems))
	}
	for _, item := range taskItems {
		task := asMap(t, item, "task")
		switch task["state"] {
		case "pending", "assigned", "running", "succeeded":
		default:
			t.Fatalf("task state = %v, want a live or completed state", task["state"])
		}
	}

	events := client.request(http.MethodGet, executionPath+"/events", nil, nil, http.StatusOK)
	eventItems := asList(t, events["items"], "events")
	if len(eventItems) < 1 {
		t.Fatal("expected at least the execution.created event")
	}
	if event := asMap(t, eventItems[0], "event"); event["event_type"] != "execution.created" {
		t.Fatalf("event type = %v", event["event_type"])
	}

	// The execution must be durable in PostgreSQL, including tasks, dependencies, audit and outbox.
	ctx := context.Background()
	var state, requestID string
	var eventCount, taskCount, dependencyCount, outboxCount int
	if err := admin.QueryRow(ctx, `SELECT state, request_id FROM execution.executions WHERE id = $1`, executionID).
		Scan(&state, &requestID); err != nil {
		t.Fatalf("query execution: %v", err)
	}
	switch state {
	case "pending", "running", "succeeded":
	default:
		t.Fatalf("committed execution state=%s request_id=%s", state, requestID)
	}
	if requestID != idempotency {
		t.Fatalf("committed execution request_id=%s", requestID)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM execution.execution_tasks WHERE execution_id = $1`, executionID).
		Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM execution.task_dependencies WHERE execution_id = $1`, executionID).
		Scan(&dependencyCount); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM execution.execution_events WHERE execution_id = $1`, executionID).
		Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM execution.outbox WHERE execution_id = $1`, executionID).
		Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != 2 || dependencyCount != 1 || eventCount < 1 || outboxCount < 1 {
		t.Fatalf("durable rows: tasks=%d dependencies=%d events=%d outbox=%d", taskCount, dependencyCount, eventCount, outboxCount)
	}
	var executionCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM execution.executions WHERE org_id = $1`, orgID).
		Scan(&executionCount); err != nil {
		t.Fatal(err)
	}
	if executionCount != 1 {
		t.Fatalf("expected exactly one execution after replay/conflict/invalid-input attempts, got %d", executionCount)
	}

	// RBAC: a viewer can read but not create.
	viewerPrincipal := "viewer_" + suffix
	client.request(http.MethodPost, "/v1/orgs/"+orgID+"/members",
		map[string]any{"principal_id": viewerPrincipal, "role": "viewer"}, nil, http.StatusCreated)
	viewer := newAPIClient(t)
	viewer.token = viewer.devToken(viewerPrincipal)
	viewer.request(http.MethodGet, "/v1/orgs/"+orgID+"/projects", nil, nil, http.StatusOK)
	viewer.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects",
		map[string]any{"name": "Nope", "slug": "nope"}, nil, http.StatusForbidden)

	// The last owner cannot be demoted or removed; ordinary role changes and removals work.
	client.request(http.MethodPatch, "/v1/orgs/"+orgID+"/members/"+principal,
		map[string]any{"role": "viewer"}, nil, http.StatusConflict)
	client.request(http.MethodDelete, "/v1/orgs/"+orgID+"/members/"+principal, nil, nil, http.StatusConflict)
	client.request(http.MethodPatch, "/v1/orgs/"+orgID+"/members/"+viewerPrincipal,
		map[string]any{"role": "operator"}, nil, http.StatusOK)
	client.request(http.MethodDelete, "/v1/orgs/"+orgID+"/members/"+viewerPrincipal, nil, nil, http.StatusNoContent)

	// Cross-tenant IDs are indistinguishable from missing IDs.
	outsider := newAPIClient(t)
	outsider.token = outsider.devToken("outsider_" + suffix)
	outsider.request(http.MethodGet, "/v1/orgs/"+orgID+"/projects", nil, nil, http.StatusNotFound)
	outsider.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects",
		map[string]any{"name": "Nope", "slug": "nope"}, nil, http.StatusNotFound)

	t.Logf("flow complete: org=%s project=%s agent=%s workflow=%s execution=%s", orgID, projectID, agentID, workflowID, executionID)
}

func cleanupAPITestOrg(t *testing.T, admin *pgxpool.Pool, orgID string) {
	t.Helper()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	queries := []string{
		`DELETE FROM execution.executions WHERE org_id = $1`,
		`DELETE FROM control.workflow_versions WHERE org_id = $1`,
		`DELETE FROM control.workflows WHERE org_id = $1`,
		`DELETE FROM control.agent_versions WHERE org_id = $1`,
		`DELETE FROM control.agents WHERE org_id = $1`,
		`DELETE FROM control.memberships WHERE org_id = $1`,
		`DELETE FROM control.api_keys WHERE org_id = $1`,
		`DELETE FROM control.credentials WHERE org_id = $1`,
		`DELETE FROM control.audit_events WHERE org_id = $1`,
		`DELETE FROM control.idempotency_keys WHERE org_id = $1`,
		`DELETE FROM control.projects WHERE org_id = $1`,
		`DELETE FROM control.organizations WHERE id = $1`,
	}
	for _, query := range queries {
		if _, err := admin.Exec(cleanupCtx, query, orgID); err != nil {
			t.Logf("cleanup %q: %v", query, err)
		}
	}
	// Deleting an in-flight execution skips the normal slot release. Tests are sequential, so reset the
	// fleet's counters once the test's task rows are gone.
	if _, err := admin.Exec(cleanupCtx, `
		UPDATE execution.workers
		SET reserved_slots = 0,
		    status = CASE WHEN status = 'busy' THEN 'available' ELSE status END,
		    updated_at = now()`); err != nil {
		t.Logf("cleanup worker slots: %v", err)
	}
}

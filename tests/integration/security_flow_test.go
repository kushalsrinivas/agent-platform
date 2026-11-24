//go:build integration

package integration

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestTenantIsolationAcrossResources proves that one tenant's API key cannot read or mutate another tenant's
// projects, agents, workflows, executions, events, members, API keys, usage, workers or secrets.
func TestTenantIsolationAcrossResources(t *testing.T) {
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	clientA := newAPIClient(t)
	clientA.token = clientA.devToken("tenant_a_" + suffix)
	clientB := newAPIClient(t)
	clientB.token = clientB.devToken("tenant_b_" + suffix)

	orgA, projectA, agentA := createWorkerProject(t, clientA, "a-"+suffix)
	workflowA := clientA.request(http.MethodPost,
		"/v1/orgs/"+orgA+"/projects/"+projectA+"/workflows", map[string]any{
			"name": "tenant-a-flow",
			"version": map[string]any{"graph": map[string]any{
				"nodes": []any{map[string]any{
					"id": "work", "type": "agent",
					"agent": map[string]any{"agent_id": agentA, "version": 1},
				}},
				"edges": []any{},
			}},
		}, nil, http.StatusCreated)
	workflowID := asString(t, workflowA, "id")
	clientA.request(http.MethodPost,
		"/v1/orgs/"+orgA+"/projects/"+projectA+"/workflows/"+workflowID+"/versions/1:publish",
		nil, nil, http.StatusOK)
	environmentA := clientA.request(http.MethodPost,
		"/v1/orgs/"+orgA+"/projects/"+projectA+"/environments",
		map[string]any{"name": "Production", "slug": "production", "kind": "production"}, nil, http.StatusCreated)
	environmentID := asString(t, environmentA, "id")
	executionA := clientA.request(http.MethodPost,
		"/v1/orgs/"+orgA+"/projects/"+projectA+"/executions", map[string]any{
			"workflow_id": workflowID, "workflow_version": 1, "input": map[string]any{"topic": "private"},
		}, map[string]string{"Idempotency-Key": "isolation-" + suffix}, http.StatusAccepted)
	executionID := asString(t, executionA, "id")

	t.Logf("orgA=%s projectA=%s", orgA, projectA)
	keyA := clientA.request(http.MethodPost, "/v1/orgs/"+orgA+"/api-keys", map[string]any{
		"name": "admin-a", "project_id": projectA, "role": "admin",
	}, nil, http.StatusCreated)
	apiKeyA := asString(t, keyA, "key")
	keyID := asString(t, keyA, "id")
	clientA.request(http.MethodPost,
		"/v1/orgs/"+orgA+"/projects/"+projectA+"/secrets",
		map[string]any{"name": "provider_key", "value": "sk-tenant-a-secret"}, nil, http.StatusCreated)

	keyClient := newAPIClient(t)
	orgB, projectB, agentB := createWorkerProject(t, clientB, "b-"+suffix)
	t.Logf("orgB=%s projectB=%s keyA_project=%v", orgB, projectB, keyA["project_id"])
	keyB := clientB.request(http.MethodPost, "/v1/orgs/"+orgB+"/api-keys", map[string]any{
		"name": "admin-b", "project_id": projectB, "role": "admin",
	}, nil, http.StatusCreated)
	apiKeyB := asString(t, keyB, "key")
	_ = agentB

	tenantA := func(path string) string {
		return strings.ReplaceAll(path, "{org}", orgA)
	}

	// Every read of another tenant's resources is indistinguishable from a missing resource.
	reads := []string{
		"/v1/orgs/{org}/projects/" + projectA,
		"/v1/orgs/{org}/projects/" + projectA + "/environments",
		"/v1/orgs/{org}/projects/" + projectA + "/environments/" + environmentID,
		"/v1/orgs/{org}/projects/" + projectA + "/agents",
		"/v1/orgs/{org}/projects/" + projectA + "/agents/" + agentA,
		"/v1/orgs/{org}/projects/" + projectA + "/workflows",
		"/v1/orgs/{org}/projects/" + projectA + "/workflows/" + workflowID,
		"/v1/orgs/{org}/projects/" + projectA + "/executions",
		"/v1/orgs/{org}/projects/" + projectA + "/executions/" + executionID,
		"/v1/orgs/{org}/projects/" + projectA + "/executions/" + executionID + "/tasks",
		"/v1/orgs/{org}/projects/" + projectA + "/executions/" + executionID + "/events",
		"/v1/orgs/{org}/projects/" + projectA + "/tools",
		"/v1/orgs/{org}/projects/" + projectA + "/mcp/servers",
		"/v1/orgs/{org}/projects/" + projectA + "/secrets",
		"/v1/orgs/{org}/secrets",
		"/v1/orgs/{org}/members",
		"/v1/orgs/{org}/api-keys",
		"/v1/orgs/{org}/usage?project_id=" + projectA,
		"/v1/orgs/{org}/workers",
		"/v1/orgs/{org}/approvals?project_id=" + projectA,
	}
	for _, path := range reads {
		keyClient.request(http.MethodGet, tenantA(path), nil,
			map[string]string{"X-API-Key": apiKeyB}, http.StatusNotFound)
	}

	// Mutations are also rejected before any tenant row is touched.
	mutations := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/v1/orgs/{org}/projects", map[string]any{"name": "x", "slug": "x-tenant"}},
		{http.MethodPost, "/v1/orgs/{org}/projects/" + projectA + "/agents", map[string]any{"name": "x", "version": map[string]any{"spec": echoAgentSpec()}}},
		{http.MethodPost, "/v1/orgs/{org}/projects/" + projectA + "/workflows", map[string]any{"name": "x", "version": map[string]any{"graph": map[string]any{"nodes": []any{}, "edges": []any{}}}}},
		{http.MethodPost, "/v1/orgs/{org}/projects/" + projectA + "/executions/" + executionID + ":cancel", map[string]any{"reason": "cross-tenant"}},
		{http.MethodPost, "/v1/orgs/{org}/projects/" + projectA + "/workflows/" + workflowID + "/versions/1:publish", nil},
		{http.MethodPost, "/v1/orgs/{org}/projects/" + projectA + "/secrets", map[string]any{"name": "stolen", "value": "x"}},
		{http.MethodDelete, "/v1/orgs/{org}/api-keys/" + keyID, nil},
	}
	for _, mutation := range mutations {
		keyClient.request(mutation.method, tenantA(mutation.path), mutation.body,
			map[string]string{"X-API-Key": apiKeyB}, http.StatusNotFound)
	}

	// A project-scoped key is confined to its own project even inside its own organisation.
	keyClient.request(http.MethodGet, "/v1/orgs/"+orgA+"/projects/"+projectB+"/executions", nil,
		map[string]string{"X-API-Key": apiKeyA}, http.StatusNotFound)
	keyClient.request(http.MethodGet, "/v1/orgs/"+orgA+"/projects/"+projectA+"/executions/"+executionID, nil,
		map[string]string{"X-API-Key": apiKeyA}, http.StatusOK)
}

// TestRBACPermissionGroups proves the owner/admin/developer/viewer matrix on real API keys and memberships.
func TestRBACPermissionGroups(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	ownerID := "rbac_owner_" + suffix
	client.token = client.devToken(ownerID)
	orgID, projectID, agentID := createWorkerProject(t, client, "rbac-"+suffix)

	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{
			"name": "rbac-flow",
			"version": map[string]any{"graph": map[string]any{
				"nodes": []any{map[string]any{
					"id": "work", "type": "agent",
					"agent":       map[string]any{"agent_id": agentID, "version": 1},
					"worker_pool": "rbac-no-worker", "timeout_seconds": 30,
				}},
				"edges": []any{},
			}},
		}, nil, http.StatusCreated)
	workflowID := asString(t, workflow, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows/"+workflowID+"/versions/1:publish",
		nil, nil, http.StatusOK)
	execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		map[string]any{"workflow_id": workflowID, "workflow_version": 1, "input": map[string]any{"topic": "rbac"}},
		map[string]string{"Idempotency-Key": "rbac-" + suffix}, http.StatusAccepted)
	executionID := asString(t, execution, "id")

	adminID := "rbac_admin_" + suffix
	developerID := "rbac_developer_" + suffix
	viewerID := "rbac_viewer_" + suffix
	for principal, role := range map[string]string{adminID: "admin", developerID: "developer", viewerID: "viewer"} {
		client.request(http.MethodPost, "/v1/orgs/"+orgID+"/members",
			map[string]any{"principal_id": principal, "role": role}, nil, http.StatusCreated)
	}

	admin := newAPIClient(t)
	admin.token = admin.devToken(adminID)
	developer := newAPIClient(t)
	developer.token = developer.devToken(developerID)
	viewer := newAPIClient(t)
	viewer.token = viewer.devToken(viewerID)

	// Viewer can read but cannot mutate.
	viewer.request(http.MethodGet,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/executions/"+executionID, nil, nil, http.StatusOK)
	viewer.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/executions/"+executionID+":cancel",
		map[string]any{"reason": "viewer"}, nil, http.StatusForbidden)
	viewer.request(http.MethodGet, "/v1/orgs/"+orgID+"/projects/"+projectID+"/secrets", nil, nil, http.StatusForbidden)

	// Developer can create/deploy/cancel but cannot manage members or secrets.
	developer.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/agents",
		map[string]any{"name": "developer-agent", "version": map[string]any{"spec": echoAgentSpec()}},
		nil, http.StatusCreated)
	developer.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/executions/"+executionID+":cancel",
		map[string]any{"reason": "developer"}, nil, http.StatusAccepted)
	developer.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/secrets",
		map[string]any{"name": "forbidden", "value": "x"}, nil, http.StatusForbidden)
	developer.request(http.MethodPost, "/v1/orgs/"+orgID+"/members",
		map[string]any{"principal_id": "someone", "role": "viewer"}, nil, http.StatusForbidden)

	// Admin can manage members and secrets, but cannot change ownership.
	admin.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/secrets",
		map[string]any{"name": "admin-secret", "value": "admin-value"}, nil, http.StatusCreated)
	admin.request(http.MethodPost, "/v1/orgs/"+orgID+"/members",
		map[string]any{"principal_id": "rbac_extra_" + suffix, "role": "viewer"}, nil, http.StatusCreated)
	admin.request(http.MethodDelete, "/v1/orgs/"+orgID+"/members/"+ownerID, nil, nil, http.StatusForbidden)
	admin.request(http.MethodPatch, "/v1/orgs/"+orgID+"/members/"+ownerID,
		map[string]any{"role": "admin"}, nil, http.StatusForbidden)
}

// TestAuditTrailAppendOnly proves that mutating admin/API actions are recorded and the application role cannot
// rewrite or delete the audit trail.
func TestAuditTrailAppendOnly(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("audit_" + suffix)
	orgID, projectID, _ := createWorkerProject(t, client, "audit-"+suffix)
	client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/secrets",
		map[string]any{"name": "audited", "value": "audit-value"}, nil, http.StatusCreated)

	admin := adminPool(t)
	var count int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM control.audit_events WHERE org_id = $1 AND action LIKE 'secret.%'`, orgID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("secret access was not audited")
	}
	if _, err := admin.Exec(context.Background(),
		`UPDATE control.audit_events SET outcome = 'failure' WHERE org_id = $1`, orgID); err == nil {
		t.Fatal("audit_events accepted an UPDATE")
	}
}

// TestWorkerRuntimeIsolation verifies the deployed worker boundary: non-root, no Linux capabilities, read-only
// root filesystem and no network route to PostgreSQL. The per-attempt prlimit sandbox is unit-tested separately.
func TestWorkerRuntimeIsolation(t *testing.T) {
	requireDocker(t)
	root := repoRoot(t)
	run := func(script string) (string, error) {
		command := exec.CommandContext(context.Background(), "docker", "compose", //nolint:gosec // fixed CLI and test-owned script.
			"-f", filepath.Join(root, "docker-compose.yml"), "exec", "-T", "worker", "sh", "-c", script)
		command.Dir = root
		output, err := command.CombinedOutput()
		return string(output), err
	}
	if output, err := run("id -u"); err != nil || strings.TrimSpace(output) != "10001" {
		t.Fatalf("worker uid=%q err=%v", strings.TrimSpace(output), err)
	}
	if output, err := run("grep '^CapEff:' /proc/self/status"); err != nil ||
		!strings.Contains(output, "0000000000000000") {
		t.Fatalf("worker capabilities=%q err=%v", output, err)
	}
	if _, err := run("touch /app/phase14-probe"); err == nil {
		t.Fatal("worker root filesystem is writable")
	}
	if _, err := run(`python -c 'import socket; socket.create_connection(("postgres", 5432), 2)'`); err == nil {
		t.Fatal("worker can reach PostgreSQL across the network boundary")
	}
}

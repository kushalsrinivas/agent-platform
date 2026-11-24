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

func createApprovalWorkflow(t *testing.T, client *apiClient, orgID, projectID, name string,
	approval map[string]any) (string, string) {
	t.Helper()
	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{
			"name": name,
			"version": map[string]any{"graph": map[string]any{
				"nodes": []any{
					map[string]any{"id": "review", "type": "approval",
						"config": map[string]any{"approval": approval}},
					map[string]any{"id": "done", "type": "end"},
				},
				"edges":  []any{map[string]any{"from": "review", "to": "done"}},
				"policy": map[string]any{"on_failure": "fail_fast"},
			}},
		}, nil, http.StatusCreated)
	workflowID := asString(t, workflow, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows/"+workflowID+"/versions/1:publish",
		nil, nil, http.StatusOK)
	return workflowID, name
}

func submitApprovalExecution(t *testing.T, client *apiClient, orgID, projectID, workflowID, key string) string {
	t.Helper()
	execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		map[string]any{"workflow_id": workflowID, "workflow_version": 1, "input": map[string]any{}},
		map[string]string{"Idempotency-Key": key}, http.StatusAccepted)
	return asString(t, execution, "id")
}

func waitApproval(t *testing.T, client *apiClient, orgID, projectID, executionID, wantStatus string) map[string]any {
	t.Helper()
	path := "/v1/orgs/" + orgID + "/projects/" + projectID + "/executions/" + executionID + "/approvals"
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response := client.request(http.MethodGet, path, nil, nil, http.StatusOK)
		items := asList(t, response["items"], "approvals")
		if len(items) == 1 {
			approval := asMap(t, items[0], "approval")
			if approval["status"] == wantStatus {
				return approval
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("approval for execution %s did not reach %s", executionID, wantStatus)
	return nil
}

func TestApprovalAssignmentPolicyAuditAndExpiry(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	approverA := "user-a-" + suffix
	approverB := "user-b-" + suffix
	client.token = client.devToken(approverA)
	orgID, projectID, _ := createWorkerProject(t, client, suffix)

	admin := adminPool(t)
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control.memberships (org_id, principal_id, role) VALUES ($1, $2, 'operator')`,
		orgID, approverB); err != nil {
		t.Fatalf("add second approver membership: %v", err)
	}
	clientB := newAPIClient(t)
	clientB.token = clientB.devToken(approverB)

	workflowID, _ := createApprovalWorkflow(t, client, orgID, projectID, "approval-all", map[string]any{
		"prompt": "Ship the release?", "policy": "all", "approvers": []any{approverA, approverB},
	})
	executionID := submitApprovalExecution(t, client, orgID, projectID, workflowID, "approval-all-"+suffix)
	approval := waitApproval(t, client, orgID, projectID, executionID, "pending")
	taskID := asString(t, approval, "task_id")
	orgApprovals := client.request(http.MethodGet,
		"/v1/orgs/"+orgID+"/approvals?status=pending&project_id="+projectID, nil, nil, http.StatusOK)
	if items := asList(t, orgApprovals["items"], "approvals"); len(items) != 1 {
		t.Fatalf("organisation approval list returned %d rows, want 1", len(items))
	}
	if approval["policy"] != "all" || len(asList(t, approval["approvers"], "approvers")) != 2 {
		t.Fatalf("approval assignment malformed: %v", approval)
	}

	path := "/v1/orgs/" + orgID + "/projects/" + projectID + "/executions/" + executionID + "/tasks/" + taskID
	client.request(http.MethodPost, path+":approve", map[string]any{"comment": "A approves"}, nil, http.StatusAccepted)
	stillPending := waitApproval(t, client, orgID, projectID, executionID, "pending")
	if len(asList(t, stillPending["decisions"], "decisions")) != 1 {
		t.Fatalf("first vote was not audited: %v", stillPending)
	}

	clientB.request(http.MethodPost, path+":approve", map[string]any{"comment": "B approves"}, nil, http.StatusAccepted)
	approved := waitApproval(t, client, orgID, projectID, executionID, "approved")
	if approved["decided_by"] != approverB {
		t.Fatalf("decided_by = %v, want %s", approved["decided_by"], approverB)
	}
	if len(asList(t, approved["decisions"], "decisions")) != 2 {
		t.Fatalf("approval audit did not record both votes: %v", approved)
	}
	waitExecutionState(t, client, fixtureExecution{
		orgID: orgID, projectID: projectID, workflowID: workflowID, executionID: executionID,
	}, "succeeded")

	rejectWorkflow, _ := createApprovalWorkflow(t, client, orgID, projectID, "approval-reject", map[string]any{
		"prompt": "Delete production?", "policy": "any", "reject_reason_required": true,
	})
	rejectExecution := submitApprovalExecution(t, client, orgID, projectID, rejectWorkflow, "approval-reject-"+suffix)
	rejectApproval := waitApproval(t, client, orgID, projectID, rejectExecution, "pending")
	rejectTask := asString(t, rejectApproval, "task_id")
	rejectPath := "/v1/orgs/" + orgID + "/projects/" + projectID + "/executions/" + rejectExecution +
		"/tasks/" + rejectTask
	client.request(http.MethodPost, rejectPath+":reject", map[string]any{"comment": ""}, nil, http.StatusConflict)
	client.request(http.MethodPost, rejectPath+":reject",
		map[string]any{"comment": "unsafe change"}, nil, http.StatusAccepted)
	rejected := waitApproval(t, client, orgID, projectID, rejectExecution, "rejected")
	if rejected["reason"] != "unsafe change" {
		t.Fatalf("rejection reason = %v, want unsafe change", rejected["reason"])
	}
	waitExecutionState(t, client, fixtureExecution{
		orgID: orgID, projectID: projectID, workflowID: rejectWorkflow, executionID: rejectExecution,
	}, "failed")
}

func TestApprovalExpiryReleasesExecution(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("approval_expiry_" + suffix)
	orgID, projectID, _ := createWorkerProject(t, client, suffix)

	workflowID, _ := createApprovalWorkflow(t, client, orgID, projectID, "approval-expiry", map[string]any{
		"prompt": "Expire quickly", "expires_in_seconds": 1, "on_reject": "continue",
	})
	executionID := submitApprovalExecution(t, client, orgID, projectID, workflowID, "approval-expiry-"+suffix)
	waitApproval(t, client, orgID, projectID, executionID, "pending")
	expired := waitApproval(t, client, orgID, projectID, executionID, "expired")
	if expired["status"] != "expired" {
		t.Fatalf("approval did not expire: %v", expired)
	}
	waitExecutionState(t, client, fixtureExecution{
		orgID: orgID, projectID: projectID, workflowID: workflowID, executionID: executionID,
	}, "succeeded")
}

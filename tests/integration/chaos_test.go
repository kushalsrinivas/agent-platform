//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Phase 16 chaos suite. It reuses the integration harness and Docker control helpers from recovery_flow_test.go.
// The full scenario matrix and the suites covering scenarios not duplicated here are in
// docs/phase-16-testing-and-release.md.

func chaosSuffix() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
}

// createChaosFixture builds an organisation, project, optional provider credential, agent, workflow and one
// execution. It is used when the agent spec or retry policy must differ from the shared scheduler fixture.
func createChaosFixture(t *testing.T, client *apiClient, suffix, pool string, spec map[string]any, retry map[string]any) fixtureExecution {
	t.Helper()
	org := client.request(http.MethodPost, "/v1/orgs", map[string]any{
		"name": "Chaos Org", "slug": "chaos-" + suffix,
	}, nil, http.StatusCreated)
	orgID := asString(t, org, "id")
	admin := adminPool(t)
	t.Cleanup(func() { cleanupAPITestOrg(t, admin, orgID) })

	project := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects", map[string]any{
		"name": "Chaos Project", "slug": "chaos-project",
	}, nil, http.StatusCreated)
	projectID := asString(t, project, "id")

	if model, ok := spec["model"].(map[string]any); ok {
		if ref, _ := model["credential_ref"].(string); ref != "" && ref != "default" {
			client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/secrets",
				map[string]any{"name": ref, "value": "chaos-provider-key"}, nil, http.StatusCreated)
		}
	}

	agent := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/agents", map[string]any{
		"name": "chaos-agent", "description": "phase 16 chaos fixture",
		"version": map[string]any{"spec": spec},
	}, nil, http.StatusCreated)
	agentID := asString(t, agent, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/agents/"+agentID+"/versions/1:publish",
		nil, nil, http.StatusOK)

	node := map[string]any{
		"id": "agent", "type": "agent",
		"agent":       map[string]any{"agent_id": agentID, "version": 1},
		"worker_pool": pool, "timeout_seconds": 60,
	}
	if retry != nil {
		node["retry"] = retry
	}
	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{
			"name": "chaos-flow",
			"version": map[string]any{"graph": map[string]any{
				"nodes":        []any{node},
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
			"input": map[string]any{"topic": "chaos"},
		}, map[string]string{"Idempotency-Key": "chaos-" + suffix}, http.StatusAccepted)

	return fixtureExecution{
		orgID: orgID, projectID: projectID, workflowID: workflowID,
		executionID: asString(t, execution, "id"),
		taskIDs:     loadTaskIDs(t, asString(t, execution, "id")),
	}
}

// Scenario: cancel a three-branch parallel execution while every branch is running. Every task must end
// cancelled and no branch may commit a success after the cancellation decision.
func TestChaosCancelDuringParallelExecution(t *testing.T) {
	client := newAPIClient(t)
	suffix := chaosSuffix()
	client.token = client.devToken("chaos_cancel_" + suffix)
	pool := "chaos-cancel-" + suffix

	startRuntimeWorkerCapacityEnv(t, suffix, pool, 3, []string{"AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS=5"})
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 3, Independent: true,
	})

	admin := adminPool(t)
	deadline := time.Now().Add(20 * time.Second)
	for {
		var running int
		if err := admin.QueryRow(context.Background(), `
			SELECT count(*) FROM execution.execution_tasks
			WHERE execution_id = $1 AND state = 'running'`, fixture.executionID).Scan(&running); err != nil {
			t.Fatal(err)
		}
		if running == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("parallel branches never all reached running (last count %d)", running)
		}
		time.Sleep(100 * time.Millisecond)
	}

	client.request(http.MethodPost,
		"/v1/orgs/"+fixture.orgID+"/projects/"+fixture.projectID+"/executions/"+fixture.executionID+":cancel",
		map[string]any{"reason": "chaos cancel during parallel"}, nil, http.StatusAccepted)

	waitExecutionStateFor(t, client, fixture, "cancelled", 30*time.Second)

	var tasks, notCancelled, succeededAttempts, cancelledAttempts int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE state <> 'cancelled')
		FROM execution.execution_tasks WHERE execution_id = $1`,
		fixture.executionID).Scan(&tasks, &notCancelled); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE outcome = 'succeeded'),
		       count(*) FILTER (WHERE outcome = 'cancelled')
		FROM execution.task_attempts WHERE execution_id = $1`,
		fixture.executionID).Scan(&succeededAttempts, &cancelledAttempts); err != nil {
		t.Fatal(err)
	}
	if tasks != 3 || notCancelled != 0 {
		t.Fatalf("tasks=%d notCancelled=%d, want 3 and 0", tasks, notCancelled)
	}
	if succeededAttempts != 0 {
		t.Fatalf("cancel committed %d successful branch attempts", succeededAttempts)
	}
	if cancelledAttempts == 0 {
		t.Fatal("no branch attempt recorded a cancelled outcome")
	}
}

// Scenario: the model provider is unreachable. Retries are governed by policy and the execution fails with a
// durable failure code instead of hanging or committing partial output.
func TestChaosProviderUnavailableRetriesAndFailsSafely(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := newAPIClient(t)
	suffix := chaosSuffix()
	client.token = client.devToken("chaos_provider_" + suffix)
	pool := "chaos-provider-" + suffix

	spec := echoAgentSpec()
	spec["model"] = map[string]any{
		"provider": "openai_compatible", "name": "chaos-model", "credential_ref": "chaos_provider_key",
		"parameters": map[string]any{"base_url": server.URL + "/v1", "timeout_seconds": 5},
	}
	fixture := createChaosFixture(t, client, suffix, pool, spec, map[string]any{
		"max_attempts": 2, "retry_on": []any{"transient", "timeout"},
	})
	started := time.Now()
	startRuntimeWorker(t, suffix, pool)

	waitExecutionStateFor(t, client, fixture, "failed", 90*time.Second)

	if hits.Load() < 2 {
		t.Fatalf("provider hits = %d, want at least 2 attempts", hits.Load())
	}
	var attempts, failedAttempts int
	var errorCode string
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE outcome = 'failed'), COALESCE(max(error_code), '')
		FROM execution.task_attempts WHERE execution_id = $1`,
		fixture.executionID).Scan(&attempts, &failedAttempts, &errorCode); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || failedAttempts != 2 {
		t.Fatalf("attempts=%d failedAttempts=%d, want 2 and 2", attempts, failedAttempts)
	}
	if errorCode == "" {
		t.Fatal("provider failure did not record an error code")
	}
	if time.Since(started) < time.Second {
		t.Fatal("retry completed without backoff")
	}

	execution := client.request(http.MethodGet,
		"/v1/orgs/"+fixture.orgID+"/projects/"+fixture.projectID+"/executions/"+fixture.executionID,
		nil, nil, http.StatusOK)
	if code, _ := execution["failure_code"].(string); code == "" {
		t.Fatal("failed execution has no failure_code")
	}
}

// Scenario: concurrent duplicate submission with one idempotency key. Exactly one logical execution exists and
// no request returns a server error.
func TestChaosConcurrentDuplicateExecutionRequest(t *testing.T) {
	client := newAPIClient(t)
	suffix := chaosSuffix()
	client.token = client.devToken("chaos_dup_" + suffix)
	pool := "chaos-dup-" + suffix

	startRuntimeWorker(t, suffix, pool)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{Pool: pool, NodeCount: 1})
	key := "chaos-dup-" + suffix
	body := map[string]any{
		"workflow_id": fixture.workflowID, "workflow_version": 1,
		"input": map[string]any{"topic": "duplicate"},
	}

	const requests = 10
	var wg sync.WaitGroup
	statuses := make([]int, requests)
	ids := make([]string, requests)
	errs := make([]error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			statuses[index], ids[index], errs[index] = chaosSubmit(client, fixture, key, body)
		}(i)
	}
	wg.Wait()

	unique := map[string]bool{}
	for i := range statuses {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		switch statuses[i] {
		case http.StatusAccepted, http.StatusOK, http.StatusConflict:
		default:
			t.Fatalf("request %d returned status %d", i, statuses[i])
		}
		if ids[i] != "" {
			unique[ids[i]] = true
		}
	}
	if len(unique) != 1 {
		t.Fatalf("duplicate submissions produced %d logical executions: %v", len(unique), unique)
	}

	var count int
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT count(*) FROM execution.executions WHERE org_id = $1 AND request_id = $2`,
		fixture.orgID, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("executions for idempotency key = %d, want 1", count)
	}
}

// waitExecutionsSucceeded polls the durable state once per batch instead of once per execution, keeping the
// suite independent of the API rate limit.
func waitExecutionsSucceeded(t *testing.T, ids []string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var total, succeeded, ended int
		if err := adminPool(t).QueryRow(context.Background(), `
			SELECT count(*),
			       count(*) FILTER (WHERE state = 'succeeded'),
			       count(*) FILTER (WHERE state IN ('succeeded', 'failed', 'cancelled'))
			FROM execution.executions WHERE id = ANY($1)`, ids).Scan(&total, &succeeded, &ended); err != nil {
			t.Fatal(err)
		}
		if total == len(ids) && succeeded == total {
			return
		}
		if ended == total {
			rows, err := adminPool(t).Query(context.Background(), `
				SELECT e.id, e.state, COALESCE(e.failure_code, ''),
				       COALESCE((SELECT string_agg(t.state || ':' || COALESCE(t.last_failure_code, '') ||
				                                   ':attempts=' || t.attempt_count || '/' || t.max_attempts, ',')
				                 FROM execution.execution_tasks t WHERE t.execution_id = e.id), '')
				FROM execution.executions e WHERE e.id = ANY($1) AND e.state <> 'succeeded'`, ids)
			if err == nil {
				for rows.Next() {
					var id, state, code, tasks string
					if scanErr := rows.Scan(&id, &state, &code, &tasks); scanErr == nil {
						t.Logf("failed execution %s state=%s code=%s tasks=%s", id, state, code, tasks)
					}
				}
				rows.Close()
			}
			t.Fatalf("executions ended without all succeeding: total=%d succeeded=%d", total, succeeded)
		}
		if time.Now().After(deadline) {
			t.Fatalf("executions did not all succeed within %s: total=%d succeeded=%d", timeout, total, succeeded)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func chaosSubmit(client *apiClient, fixture fixtureExecution, key string, body map[string]any) (int, string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	path := "/v1/orgs/" + fixture.orgID + "/projects/" + fixture.projectID + "/executions"
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, client.base+path, bytes.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Idempotency-Key", key)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = response.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var decoded map[string]any
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &decoded)
	}
	id, _ := decoded["id"].(string)
	return response.StatusCode, id, nil
}

// Scenario: the worker pod restarts while queued and in-flight work exists. Kubernetes restart semantics are
// reproduced with the Compose worker container; durable leases recover every execution exactly once.
func TestChaosWorkerPodRestart(t *testing.T) {
	requireDocker(t)
	client := newAPIClient(t)
	suffix := chaosSuffix()
	client.token = client.devToken("chaos_pod_" + suffix)

	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: "default", NodeCount: 1, MaxAttempts: 3, RetryOn: []any{"worker_lost", "transient"},
	})
	fixtures := []fixtureExecution{fixture}
	for i := 0; i < 8; i++ {
		fixtures = append(fixtures, submitExtraExecution(t, client, fixture,
			fmt.Sprintf("chaos-pod-%s-%d", suffix, i), 0))
	}

	dockerCompose(t, "restart", "worker")
	ids := make([]string, 0, len(fixtures))
	for _, item := range fixtures {
		ids = append(ids, item.executionID)
	}
	waitExecutionsSucceeded(t, ids, 240*time.Second)
	var duplicates int
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT count(*) FROM (
			SELECT task_id FROM execution.task_attempts
			WHERE execution_id = ANY($1) AND outcome = 'succeeded'
			GROUP BY task_id HAVING count(*) > 1
		) duplicate_successes`, ids).Scan(&duplicates); err != nil {
		t.Fatal(err)
	}
	if duplicates != 0 {
		t.Fatalf("%d tasks committed duplicate successful attempts after the restart", duplicates)
	}
}

//go:build integration

package integration

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/worker/delivery"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

// Phase 08 recovery suite. Scenarios 5 (crash after commit before ack) and 9 (lease expires while the old worker
// is still running) are covered by TestMessagingRedeliveryAndDuplicateSuppression and
// TestWorkerManagerStaleWorkerCannotCommit in messaging_flow_test.go.

func recoverySuffix() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
}

func waitTaskStateWithin(t *testing.T, executionID, want string, timeout time.Duration) (string, string) {
	t.Helper()
	admin := adminPool(t)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var taskID, attemptID, state string
		err := admin.QueryRow(context.Background(), `
			SELECT t.id, COALESCE(t.latest_attempt_id, ''), t.state
			FROM execution.execution_tasks t
			WHERE t.execution_id = $1
			ORDER BY t.created_at LIMIT 1`, executionID).Scan(&taskID, &attemptID, &state)
		if err == nil && state == want {
			return taskID, attemptID
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", ""
}

func forceWorkerGone(t *testing.T, workerID, taskID string) {
	t.Helper()
	admin := adminPool(t)
	if _, err := admin.Exec(context.Background(), `
		UPDATE execution.execution_tasks SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1 AND state IN ('assigned', 'running')`, taskID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		UPDATE execution.workers SET status = 'terminated', last_heartbeat_at = now() - interval '5 minutes'
		WHERE id = $1`, workerID); err != nil {
		t.Fatalf("terminate worker: %v", err)
	}
}

func waitExecutionStateFor(t *testing.T, client *apiClient, fixture fixtureExecution, want string,
	timeout time.Duration) {
	t.Helper()
	path := "/v1/orgs/" + fixture.orgID + "/projects/" + fixture.projectID + "/executions/" + fixture.executionID
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		execution := client.request(http.MethodGet, path, nil, nil, http.StatusOK)
		state, _ := execution["state"].(string)
		if state == want {
			return
		}
		if state == "failed" && want != "failed" {
			t.Fatalf("execution %s ended failed", fixture.executionID)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("execution %s did not reach %s within %s", fixture.executionID, want, timeout)
}

func checkpointCount(t *testing.T, taskID string) int {
	t.Helper()
	var count int
	if err := adminPool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM execution.checkpoints WHERE task_id = $1`, taskID).Scan(&count); err != nil {
		t.Fatalf("count checkpoints: %v", err)
	}
	return count
}

func waitCheckpointCount(t *testing.T, taskID string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if checkpointCount(t, taskID) >= want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("task %s produced %d checkpoints, want at least %d", taskID, checkpointCount(t, taskID), want)
}

// Scenario 1: the worker is registered and receives an assignment, but dies before accepting/executing it.
func TestRecoveryWorkerDiesBeforeReceiving(t *testing.T) {
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_before_" + suffix)
	pool := "recover-before-" + suffix

	dead := startTestWorkerWithCapacity(t, suffix+"dead", pool, 1)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1, MaxAttempts: 2, RetryOn: []any{"worker_lost", "transient"},
	})
	taskID, _ := waitTaskStateWithin(t, fixture.executionID, "assigned", 15*time.Second)
	if taskID == "" {
		t.Fatal("task was never assigned to the worker that never received it")
	}
	dead.crash()
	forceWorkerGone(t, dead.id, taskID)

	startRuntimeWorker(t, suffix+"live", pool)
	waitExecutionStateFor(t, client, fixture, "succeeded", 60*time.Second)
	assertAttemptOutcomes(t, fixture.executionID, "lease_expired", "succeeded")
}

// Scenario 2: the worker accepts the task and dies before performing work.
func TestRecoveryWorkerDiesAfterReceiving(t *testing.T) {
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_after_" + suffix)
	pool := "recover-after-" + suffix

	dead := startTestWorkerWithCapacity(t, suffix+"dead", pool, 1)
	started := make(chan struct{})
	dead.startConsumer(t, &delivery.Handler{
		Client: dead.control, WorkerID: dead.id, Log: zap.NewNop(),
		Execute: func(ctx context.Context, _ *contracts.TaskDispatch) (delivery.ExecutionOutcome, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-ctx.Done()
			return delivery.ExecutionOutcome{}, ctx.Err()
		},
	}, nil)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1, MaxAttempts: 2, RetryOn: []any{"worker_lost", "transient"},
	})
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("worker never received the task")
	}
	taskID, _ := waitTaskStateWithin(t, fixture.executionID, "running", 10*time.Second)
	if taskID == "" {
		t.Fatal("task never reached running after acceptance")
	}
	dead.crash()
	forceWorkerGone(t, dead.id, taskID)

	startRuntimeWorker(t, suffix+"live", pool)
	waitExecutionStateFor(t, client, fixture, "succeeded", 60*time.Second)
	assertAttemptOutcomes(t, fixture.executionID, "lease_expired", "succeeded")
}

// Scenario 3: the worker dies during model generation, before any checkpoint exists.
func TestRecoveryWorkerDiesDuringModelGeneration(t *testing.T) {
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_model_" + suffix)
	pool := "recover-model-" + suffix

	dead := startRuntimeWorkerEnv(t, suffix+"dead", pool, []string{"AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS=10"})
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1, MaxAttempts: 2, RetryOn: []any{"worker_lost", "timeout"},
	})
	taskID, _ := waitTaskStateWithin(t, fixture.executionID, "running", 15*time.Second)
	if taskID == "" {
		t.Fatal("task never reached running")
	}
	time.Sleep(500 * time.Millisecond)
	if count := checkpointCount(t, taskID); count != 0 {
		t.Fatalf("a checkpoint was written before the simulated crash: %d", count)
	}
	dead.crash()
	forceWorkerGone(t, dead.id, taskID)

	startRuntimeWorker(t, suffix+"live", pool)
	waitExecutionStateFor(t, client, fixture, "succeeded", 60*time.Second)
	assertAttemptOutcomes(t, fixture.executionID, "lease_expired", "succeeded")
}

// Scenario 4: the model and final checkpoint completed, but the worker died before reporting the result.
func TestRecoveryResumesCompletedCheckpoint(t *testing.T) {
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_checkpoint_" + suffix)
	pool := "recover-checkpoint-" + suffix

	dead := startRuntimeWorkerEnv(t, suffix+"dead", pool, []string{"AGENT_RUNTIME_TEST_PAUSE_SECONDS=10"})
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1, MaxAttempts: 2, RetryOn: []any{"worker_lost", "timeout"},
	})
	taskID, firstAttempt := waitTaskStateWithin(t, fixture.executionID, "running", 15*time.Second)
	if taskID == "" {
		t.Fatal("task never reached running")
	}
	waitCheckpointCount(t, taskID, 1, 20*time.Second)
	dead.crash()
	forceWorkerGone(t, dead.id, taskID)

	startRuntimeWorkerEnv(t, suffix+"live", pool, []string{"AGENT_RUNTIME_RESUME_MARKER=1"})
	waitExecutionStateFor(t, client, fixture, "succeeded", 60*time.Second)
	assertAttemptOutcomes(t, fixture.executionID, "lease_expired", "succeeded")

	admin := adminPool(t)
	var firstOutcome, secondOutcome, secondOutput string
	var secondSteps int
	if err := admin.QueryRow(context.Background(), `
		SELECT COALESCE(a.outcome, '') FROM execution.task_attempts a
		WHERE a.id = $1`, firstAttempt).Scan(&firstOutcome); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(context.Background(), `
		SELECT COALESCE(a.outcome, ''), COALESCE(a.result_inline->>'resumed', ''),
		       COALESCE((a.metrics->>'steps')::int, 0)
		FROM execution.task_attempts a
		WHERE a.task_id = $1 AND a.attempt_number = 2`, taskID).Scan(&secondOutcome, &secondOutput, &secondSteps); err != nil {
		t.Fatal(err)
	}
	if firstOutcome != "lease_expired" || secondOutcome != "succeeded" {
		t.Fatalf("attempt outcomes first=%s second=%s", firstOutcome, secondOutcome)
	}
	if secondOutput != "true" {
		t.Fatalf("second attempt did not resume the completed checkpoint (output resumed=%q)", secondOutput)
	}
	if secondSteps != 1 {
		t.Fatalf("resumed attempt steps = %d, want 1 (a re-run would call the model again)", secondSteps)
	}
	var secondAttemptCheckpoints int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM execution.checkpoints WHERE task_id = $1 AND attempt_id <> $2`,
		taskID, firstAttempt).Scan(&secondAttemptCheckpoints); err != nil {
		t.Fatal(err)
	}
	if secondAttemptCheckpoints != 0 {
		t.Fatalf("resumed attempt wrote %d checkpoints; it should return the durable result", secondAttemptCheckpoints)
	}
}

// Scenario 10: a recoverable tool side effect completed and was checkpointed, then the worker crashed. The retry
// must resume after the tool and must not repeat the side effect.
func TestRecoveryDoesNotRepeatCheckpointedToolEffect(t *testing.T) {
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_tool_" + suffix)
	pool := "recover-tool-" + suffix
	counter := filepath.Join(t.TempDir(), "tool-calls.log")

	dead := startRuntimeWorkerEnv(t, suffix+"dead", pool, []string{
		"AGENT_RUNTIME_TEST_PAUSE_AFTER_TOOL_SECONDS=10",
		"AGENT_RUNTIME_TOOL_COUNTER_FILE=" + counter,
	})
	orgID, projectID, agentID := createWorkerProjectWithTools(t, client, suffix,
		[]any{map[string]any{"tool_id": "add", "version": 1}})
	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{"name": "checkpoint-tool", "version": map[string]any{"graph": map[string]any{
			"nodes": []any{map[string]any{
				"id": "plan", "type": "agent",
				"agent":       map[string]any{"agent_id": agentID, "version": 1},
				"worker_pool": pool, "timeout_seconds": 60,
				"retry":              map[string]any{"max_attempts": 2, "retry_on": []any{"worker_lost", "timeout"}},
				"checkpoint_mode":    "on_node_end",
				"require_checkpoint": true,
			}},
			"edges":        []any{},
			"policy":       map[string]any{"on_failure": "fail_fast"},
			"input_schema": map[string]any{"type": "object", "required": []any{"topic"}},
		}}}, nil, http.StatusCreated)
	workflowID := asString(t, workflow, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows/"+workflowID+"/versions/1:publish",
		nil, nil, http.StatusOK)
	execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		map[string]any{
			"workflow_id": workflowID, "workflow_version": 1,
			"input": map[string]any{"topic": "tool:add:[1,2,3]"},
		}, map[string]string{"Idempotency-Key": "recover-tool-" + suffix}, http.StatusAccepted)
	fixture := fixtureExecution{orgID: orgID, projectID: projectID, workflowID: workflowID,
		executionID: asString(t, execution, "id"), taskIDs: loadTaskIDs(t, asString(t, execution, "id"))}

	taskID, _ := waitTaskStateWithin(t, fixture.executionID, "running", 15*time.Second)
	if taskID == "" {
		t.Fatal("task never reached running")
	}
	// Two durable boundaries: the model response with the tool call and the tool result. The final node checkpoint
	// has not been written yet because the runtime is paused inside the tool step.
	waitCheckpointCount(t, taskID, 2, 20*time.Second)
	dead.crash()
	forceWorkerGone(t, dead.id, taskID)

	startRuntimeWorkerEnv(t, suffix+"live", pool, []string{
		"AGENT_RUNTIME_RESUME_MARKER=1",
		"AGENT_RUNTIME_TOOL_COUNTER_FILE=" + counter,
	})
	waitExecutionStateFor(t, client, fixture, "succeeded", 60*time.Second)

	data, err := os.ReadFile(counter) //nolint:gosec // test-owned temporary file.
	if err != nil {
		t.Fatalf("read tool counter: %v", err)
	}
	calls := strings.Count(strings.TrimSpace(string(data)), "\n") + 1
	if strings.TrimSpace(string(data)) == "" {
		calls = 0
	}
	if calls != 1 {
		t.Fatalf("tool side effect ran %d times, want exactly 1", calls)
	}
	admin := adminPool(t)
	var steps int
	var resumed string
	if err := admin.QueryRow(context.Background(), `
		SELECT COALESCE((metrics->>'steps')::int, 0), COALESCE(result_inline->>'resumed', '')
		FROM execution.task_attempts WHERE task_id = $1 AND attempt_number = 2`, taskID).
		Scan(&steps, &resumed); err != nil {
		t.Fatal(err)
	}
	// The tool checkpoint recorded step 1; the resumed attempt makes only the final model call (step 2). A fresh
	// run would also reach step 2 but would execute the tool again, which the counter already rules out.
	if steps != 2 || resumed != "" {
		t.Fatalf("resumed attempt steps=%d resumed=%q, want 2/empty", steps, resumed)
	}
}

func assertAttemptOutcomes(t *testing.T, executionID string, want ...string) {
	t.Helper()
	rows, err := adminPool(t).Query(context.Background(), `
		SELECT COALESCE(outcome, '') FROM execution.task_attempts
		WHERE execution_id = $1 ORDER BY attempt_number`, executionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var outcome string
		if err := rows.Scan(&outcome); err != nil {
			t.Fatal(err)
		}
		got = append(got, outcome)
	}
	if len(got) != len(want) {
		t.Fatalf("attempt outcomes = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("attempt outcomes = %v, want %v", got, want)
		}
	}
}

// A workflow deadline is a durable cancellation with reason=deadline. With no worker, recovery must finalise it as
// failed/EXECUTION_TIMEOUT rather than leaving it pending forever.
func TestRecoveryExecutionDeadline(t *testing.T) {
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_deadline_" + suffix)
	pool := "recover-deadline-" + suffix
	deadline := time.Now().Add(2 * time.Second)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1, DeadlineAt: &deadline,
	})

	waitExecutionStateFor(t, client, fixture, "failed", 60*time.Second)
	var failureCode, reason string
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT COALESCE(failure_code, ''), COALESCE(cancel_reason, '')
		FROM execution.executions WHERE id = $1`, fixture.executionID).Scan(&failureCode, &reason); err != nil {
		t.Fatal(err)
	}
	if failureCode != "EXECUTION_TIMEOUT" || reason != "deadline" {
		t.Fatalf("deadline result code=%q reason=%q, want EXECUTION_TIMEOUT/deadline", failureCode, reason)
	}
	var taskState string
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT state FROM execution.execution_tasks WHERE execution_id = $1`,
		fixture.executionID).Scan(&taskState); err != nil {
		t.Fatal(err)
	}
	if taskState != "cancelled" {
		t.Fatalf("task state=%s, want cancelled", taskState)
	}
}

// --- infrastructure restarts -------------------------------------------------

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func requireDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon is not available")
	}
}

func dockerCompose(t *testing.T, args ...string) {
	t.Helper()
	root := repoRoot(t)
	commandArgs := append([]string{"compose", "-f", filepath.Join(root, "docker-compose.yml")}, args...)
	command := exec.CommandContext(context.Background(), "docker", commandArgs...) //nolint:gosec // fixed docker CLI with test-owned arguments.
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("docker compose %v: %v\n%s", args, err, output)
	}
}

// pausePostgres pauses the database and registers a cleanup that retries unpausing even if the test aborts or the
// Docker daemon is briefly slow. A skipped test must never leave PostgreSQL paused for the rest of the suite.
func pausePostgres(t *testing.T) func() {
	t.Helper()
	dockerCompose(t, "pause", "postgres")
	restored := false
	restore := func() {
		if restored {
			return
		}
		root := repoRoot(t)
		for attempt := 0; attempt < 20; attempt++ {
			command := exec.CommandContext(context.Background(), "docker", "compose", "-f", filepath.Join(root, "docker-compose.yml"), "unpause", "postgres") //nolint:gosec // fixed CLI with test-owned arguments.
			command.Dir = root
			output, err := command.CombinedOutput()
			if err == nil {
				restored = true
				return
			}
			t.Logf("unpause postgres attempt %d failed: %v\n%s", attempt+1, err, output)
			time.Sleep(2 * time.Second)
		}
		t.Errorf("postgres could not be unpaused; the stack may need manual recovery")
	}
	t.Cleanup(restore)
	return restore
}

func natsTCPAddress(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("ORCHESTRATOR_NATS_URL")
	if raw == "" {
		return "localhost:4222"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		t.Fatalf("invalid ORCHESTRATOR_NATS_URL %q", raw)
	}
	return parsed.Host
}

func waitForTCP(t *testing.T, address string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := dialer.DialContext(context.Background(), "tcp", address) //nolint:gosec // test-only address.
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("address %s did not accept connections within %s", address, timeout)
}

// Scenario 6: the orchestrator restarts. A queued execution must still run, and a cancelled execution must never
// restart.
func TestRecoveryOrchestratorRestart(t *testing.T) {
	requireDocker(t)
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_orch_" + suffix)
	pool := "recover-orch-" + suffix

	queued := createSchedulerFixture(t, client, suffix, schedulerOptions{Pool: pool, NodeCount: 1})
	cancelledFixture := submitExtraExecution(t, client, queued, "cancel-"+suffix, 0)
	cancelPath := "/v1/orgs/" + cancelledFixture.orgID + "/projects/" + cancelledFixture.projectID +
		"/executions/" + cancelledFixture.executionID + ":cancel"
	client.request(http.MethodPost, cancelPath, map[string]any{"reason": "restart durability"}, nil, http.StatusAccepted)

	dockerCompose(t, "restart", "orchestrator")
	waitForTCP(t, orchestratorGRPCAddr(t), 60*time.Second)

	waitExecutionStateFor(t, client, cancelledFixture, "cancelled", 30*time.Second)
	startRuntimeWorker(t, suffix, pool)
	waitExecutionStateFor(t, client, queued, "succeeded", 60*time.Second)

	var cancelledAttempts int
	if err := adminPool(t).QueryRow(context.Background(), `
		SELECT count(*) FROM execution.task_attempts WHERE execution_id = $1`,
		cancelledFixture.executionID).Scan(&cancelledAttempts); err != nil {
		t.Fatal(err)
	}
	if cancelledAttempts != 0 {
		t.Fatalf("cancelled execution produced %d attempts after the restart", cancelledAttempts)
	}
}

// Scenario 7: NATS restarts while an attempt is in flight. Durable state is in PostgreSQL; the outbox and worker
// consumer must reconnect.
func TestRecoveryNATSReconnect(t *testing.T) {
	requireDocker(t)
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_nats_" + suffix)
	pool := "recover-nats-" + suffix

	startRuntimeWorkerEnv(t, suffix, pool, []string{"AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS=5"})
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{Pool: pool, NodeCount: 1})
	if taskID, _ := waitTaskStateWithin(t, fixture.executionID, "running", 15*time.Second); taskID == "" {
		t.Fatal("task never reached running")
	}

	dockerCompose(t, "restart", "nats")
	waitForTCP(t, natsTCPAddress(t), 60*time.Second)
	waitExecutionStateFor(t, client, fixture, "succeeded", 90*time.Second)
}

// Scenario 8: PostgreSQL becomes unavailable briefly. The orchestrator must not lose a committed execution.
func TestRecoveryDatabaseUnavailable(t *testing.T) {
	requireDocker(t)
	client := newAPIClient(t)
	suffix := recoverySuffix()
	client.token = client.devToken("recover_db_" + suffix)
	pool := "recover-db-" + suffix

	startRuntimeWorkerEnv(t, suffix, pool, []string{"AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS=4"})
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{Pool: pool, NodeCount: 1})
	if taskID, _ := waitTaskStateWithin(t, fixture.executionID, "running", 15*time.Second); taskID == "" {
		t.Fatal("task never reached running")
	}

	restore := pausePostgres(t)
	time.Sleep(2 * time.Second)
	restore()
	waitExecutionStateFor(t, client, fixture, "succeeded", 90*time.Second)
}

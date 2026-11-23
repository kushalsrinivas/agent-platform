//go:build integration

package integration

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/worker/delivery"
	"github.com/agent-platform/agent-platform/internal/worker/execution"
)

// runtimePython resolves the host Python venv used by the runtime integration test.
func runtimePython(t *testing.T) string {
	t.Helper()
	venv := os.Getenv("RUNTIME_PYTHON")
	if venv == "" {
		// The test binary runs with its package directory as the working directory.
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatalf("resolve repository root: %v", err)
		}
		venv = filepath.Join(root, "runtimes", "python", ".venv", "bin", "python")
	}
	if _, err := os.Stat(venv); err != nil { //nolint:gosec // test-only path from the repository root.
		t.Fatalf("python runtime venv is missing (%v); run: cd runtimes/python && uv sync --frozen", err)
	}
	return venv
}

func startRuntimeWorker(t *testing.T, suffix, pool string) *testWorker {
	t.Helper()
	worker := startTestWorkerWithCapacity(t, suffix, pool, 1)
	executor := execution.NewExecutor(worker.control, execution.Config{
		CommandTemplate: "\"" + runtimePython(t) + "\" -m agent_runtime serve --socket {socket}",
		RuntimeABI:      1,
		RuntimeVersion:  "0.1.0",
		StartupTimeout:  30 * time.Second,
		KillGrace:       5 * time.Second,
	}, zap.NewNop())
	worker.startConsumer(t, &delivery.Handler{
		Client:   worker.control,
		WorkerID: worker.id,
		Execute:  executor.Execute,
		Log:      zap.NewNop(),
	}, nil)
	return worker
}

func TestRuntimeLangGraphAgentExecution(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("runtime_" + suffix)
	pool := "runtime-" + suffix

	startRuntimeWorker(t, suffix, pool)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1,
		OutputSchema: map[string]any{
			"type": "object", "required": []any{"report"},
			"properties": map[string]any{"report": map[string]any{"type": "string"}},
		},
	})
	waitExecutionState(t, client, fixture, "succeeded")

	admin := adminPool(t)
	var outcome, runtimeVersion, report, errorCode string
	var tokensIn, steps int
	if err := admin.QueryRow(context.Background(), `
		SELECT a.outcome, COALESCE(a.runtime_version, ''), COALESCE(a.error_code, ''),
		       a.result_inline->>'report', COALESCE((a.metrics->>'tokens_in')::int, 0),
		       COALESCE((a.metrics->>'steps')::int, 0)
		FROM execution.task_attempts a
		WHERE a.execution_id = $1`, fixture.executionID).
		Scan(&outcome, &runtimeVersion, &errorCode, &report, &tokensIn, &steps); err != nil {
		t.Fatal(err)
	}
	if outcome != "succeeded" || errorCode != "" {
		t.Fatalf("attempt outcome=%s error=%q", outcome, errorCode)
	}
	if runtimeVersion != "0.1.0" {
		t.Fatalf("runtime_version = %q, want 0.1.0", runtimeVersion)
	}
	if !strings.HasPrefix(report, "[echo:") {
		t.Fatalf("report = %q, want the echo provider output", report)
	}
	if tokensIn <= 0 || steps != 1 {
		t.Fatalf("metrics tokens_in=%d steps=%d, want positive tokens and one step", tokensIn, steps)
	}

	var events int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM execution.execution_events
		WHERE execution_id = $1 AND event_type = 'task.attempt_finished'`,
		fixture.executionID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("attempt_finished events = %d, want 1", events)
	}
}

func TestRuntimeOutputSchemaRejectsInvalidOutput(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("runtime_schema_" + suffix)
	pool := "runtime-schema-" + suffix

	startRuntimeWorker(t, suffix, pool)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1,
		OutputSchema: map[string]any{
			"type": "object", "required": []any{"missing_field"},
		},
	})

	path := "/v1/orgs/" + fixture.orgID + "/projects/" + fixture.projectID + "/executions/" + fixture.executionID
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		execution := client.request(http.MethodGet, path, nil, nil, http.StatusOK)
		state, _ := execution["state"].(string)
		if state == "failed" {
			break
		}
		if state == "succeeded" {
			t.Fatal("invalid runtime output was accepted")
		}
		time.Sleep(200 * time.Millisecond)
	}

	admin := adminPool(t)
	var attemptOutcome, errorCode string
	if err := admin.QueryRow(context.Background(), `
		SELECT outcome, COALESCE(error_code, '') FROM execution.task_attempts WHERE execution_id = $1`,
		fixture.executionID).Scan(&attemptOutcome, &errorCode); err != nil {
		t.Fatal(err)
	}
	if attemptOutcome != "failed" || errorCode != "RESULT_SCHEMA_INVALID" {
		t.Fatalf("attempt outcome=%s error=%q, want failed/RESULT_SCHEMA_INVALID", attemptOutcome, errorCode)
	}
	var executionState string
	if err := admin.QueryRow(context.Background(), `
		SELECT state FROM execution.executions WHERE id = $1`, fixture.executionID).Scan(&executionState); err != nil {
		t.Fatal(err)
	}
	if executionState != "failed" {
		t.Fatalf("execution state = %s, want failed", executionState)
	}
}

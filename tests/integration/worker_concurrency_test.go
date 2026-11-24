//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A single worker process must execute up to its advertised capacity concurrently. Without this, a supervisor
// waiting on delegated children can deadlock the same worker's consumer.
func TestWorkerExecutesCapacityConcurrently(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("worker_concurrency_" + suffix)
	pool := "worker-concurrency-" + suffix

	startRuntimeWorkerCapacityEnv(t, suffix, pool, 2,
		[]string{"AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS=3"})
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 2, Independent: true,
	})

	admin := adminPool(t)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var running int
		if err := admin.QueryRow(context.Background(), `
			SELECT count(*) FROM execution.execution_tasks
			WHERE execution_id = $1 AND state = 'running'`, fixture.executionID).Scan(&running); err != nil {
			t.Fatal(err)
		}
		if running >= 2 {
			waitExecutionStateFor(t, client, fixture, "succeeded", 90*time.Second)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("a capacity-2 worker did not run two attempts concurrently")
}

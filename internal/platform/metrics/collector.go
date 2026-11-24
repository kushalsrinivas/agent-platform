package metrics

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RunCollector samples queue, outbox and database saturation gauges for the orchestrator. Sampling failures leave
// the previous values in place; monitoring never affects scheduling.
func RunCollector(ctx context.Context, pool *pgxpool.Pool, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		collectOnce(ctx, pool)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func collectOnce(ctx context.Context, pool *pgxpool.Pool) {
	var pending, activeTasks, activeExecutions, outboxPending int64
	var oldestPendingSeconds, outboxOldestSeconds float64
	err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM execution.execution_tasks WHERE state = 'pending'),
		  (SELECT count(*) FROM execution.execution_tasks WHERE state IN ('assigned', 'running')),
		  (SELECT count(*) FROM execution.executions WHERE state IN ('pending', 'running')),
		  (SELECT count(*) FROM execution.outbox WHERE published_at IS NULL),
		  COALESCE((SELECT EXTRACT(EPOCH FROM (now() - min(created_at)))
		            FROM execution.execution_tasks WHERE state = 'pending'), 0),
		  COALESCE((SELECT EXTRACT(EPOCH FROM (now() - min(created_at)))
		            FROM execution.outbox WHERE published_at IS NULL), 0)`).
		Scan(&pending, &activeTasks, &activeExecutions, &outboxPending, &oldestPendingSeconds, &outboxOldestSeconds)
	if err == nil {
		QueuePendingTasks.Set(float64(pending))
		QueueActiveTasks.Set(float64(activeTasks))
		ExecutionsActive.Set(float64(activeExecutions))
		OutboxPending.Set(float64(outboxPending))
		QueueOldestPendingSeconds.Set(oldestPendingSeconds)
		OutboxOldestSeconds.Set(outboxOldestSeconds)
	}
	stats := pool.Stat()
	DBConnections.WithLabelValues("acquired").Set(float64(stats.AcquiredConns()))
	DBConnections.WithLabelValues("idle").Set(float64(stats.IdleConns()))
	DBConnections.WithLabelValues("total").Set(float64(stats.TotalConns()))
	DBConnections.WithLabelValues("max").Set(float64(stats.MaxConns()))
}

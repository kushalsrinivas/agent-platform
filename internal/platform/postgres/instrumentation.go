package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type queryStartKey struct{}

type queryStart struct {
	at  time.Time
	sql string
}

// queryTracer logs slow queries and failures without logging parameter values.
type queryTracer struct {
	log  *zap.Logger
	slow time.Duration
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryStartKey{}, queryStart{at: time.Now(), sql: data.SQL})
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	start, ok := ctx.Value(queryStartKey{}).(queryStart)
	if !ok {
		return
	}
	duration := time.Since(start.at)
	query := oneLine(start.sql)

	if data.Err != nil {
		t.log.Error("database query failed",
			zap.Duration("duration", duration),
			zap.String("sql", query),
			zap.Error(data.Err),
		)
		return
	}
	if duration >= t.slow {
		t.log.Warn("slow database query",
			zap.Duration("duration", duration),
			zap.String("sql", query),
			zap.String("rows_affected", data.CommandTag.String()),
		)
	}
}

func oneLine(sql string) string {
	sql = strings.Join(strings.Fields(sql), " ")
	if len(sql) > 500 {
		return sql[:500] + "…"
	}
	return sql
}

// WithTx runs fn in one read-committed transaction. A returned error rolls back; nil commits. Callers own the
// transaction boundary and must not perform network I/O inside it unless the state change requires it.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

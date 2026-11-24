package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type queryStartKey struct{}

type queryStart struct {
	at   time.Time
	sql  string
	span trace.Span
}

// queryTracer logs slow queries and failures without logging parameter values.
type queryTracer struct {
	log  *zap.Logger
	slow time.Duration
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, span := otel.Tracer("postgres").Start(ctx, "db.query",
		trace.WithAttributes(attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", firstWord(data.SQL))))
	return context.WithValue(ctx, queryStartKey{}, queryStart{at: time.Now(), sql: data.SQL, span: span})
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	start, ok := ctx.Value(queryStartKey{}).(queryStart)
	if !ok {
		return
	}
	duration := time.Since(start.at)
	query := oneLine(start.sql)
	if start.span != nil {
		defer start.span.End()
		start.span.SetAttributes(attribute.Int64("db.duration_ms", duration.Milliseconds()))
		if data.Err != nil {
			start.span.RecordError(data.Err)
			start.span.SetStatus(codes.Error, "query failed")
		}
	}

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

func firstWord(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "unknown"
	}
	return strings.ToUpper(fields[0])
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

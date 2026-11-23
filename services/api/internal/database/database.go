// Package database wraps the API connection pool with tenant-scoped transactions and query helpers.
package database

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agent-platform/agent-platform/internal/platform/postgres"
)

// DB is the API's database handle. Every tenant query must run through WithTenant.
type DB struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool}
}

// Pool exposes the pool for health checks only.
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// WithTenant runs fn in a transaction with app.tenant_id set for RLS. The setting is transaction-local.
func (db *DB) WithTenant(ctx context.Context, orgID string, fn func(pgx.Tx) error) error {
	if orgID == "" {
		return errors.New("database: tenant id is required")
	}
	return postgres.WithTx(ctx, db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, orgID); err != nil {
			return err
		}
		return fn(tx)
	})
}

// WithPrincipal runs fn with app.principal_id set for the pre-tenant organization discovery function.
func (db *DB) WithPrincipal(ctx context.Context, principalID string, fn func(pgx.Tx) error) error {
	return postgres.WithTx(ctx, db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.principal_id', $1, true)`, principalID); err != nil {
			return err
		}
		return fn(tx)
	})
}

func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func IsCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// EncodeCursor base64url-encodes opaque cursor parts.
func EncodeCursor(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "|")))
}

// DecodeCursor reverses EncodeCursor.
func DecodeCursor(cursor string) ([]string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(raw), "|")
	return parts, nil
}

//go:build integration

// Package integration verifies the local infrastructure contract: migrations applied, schemas and roles exist,
// and NATS JetStream is reachable. It intentionally does not need the Go services to be running.
//
// Run with: make integration-test
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func TestPostgresMigrations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	url := envOr("API_DATABASE_URL", "postgres://app_api:dev_api_password@localhost:5432/agent_platform?sslmode=disable")
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect as app_api: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	for _, schema := range []string{"control", "execution"} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`, schema,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query schema %s: %v", schema, err)
		}
		if !exists {
			t.Fatalf("schema %q is missing; run make migrate", schema)
		}
	}

	var apiCanCreate bool
	err = pool.QueryRow(ctx, `SELECT has_schema_privilege('app_api', 'control', 'CREATE')`).Scan(&apiCanCreate)
	if err != nil {
		t.Fatalf("query app_api privileges: %v", err)
	}
	if apiCanCreate {
		t.Fatal("app_api must not have CREATE on control; DDL belongs to migration roles")
	}

	var orchestratorCanWriteControl bool
	err = pool.QueryRow(ctx,
		`SELECT has_table_privilege('app_orchestrator', 'control.organizations', 'INSERT')`,
	).Scan(&orchestratorCanWriteControl)
	if err != nil {
		// The table does not exist until Phase 03; grant checks start with the first table.
		t.Logf("control tables not present yet: %v", err)
	} else if orchestratorCanWriteControl {
		t.Fatal("app_orchestrator must not write control tables")
	}
}

func TestNATSJetStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	url := envOr("ORCHESTRATOR_NATS_URL", nats.DefaultURL)
	conn, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		t.Fatalf("connect to NATS: %v", err)
	}
	defer conn.Close()

	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatalf("jetstream context: %v", err)
	}

	info, err := js.AccountInfo(ctx)
	if err != nil {
		t.Fatalf("jetstream account info: %v", err)
	}
	t.Logf("jetstream ready: %d streams, %d consumers", info.Streams, info.Consumers)
}

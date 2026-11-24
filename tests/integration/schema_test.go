//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/agent-platform/agent-platform/internal/outbox"
	"github.com/agent-platform/agent-platform/internal/platform/postgres"
)

func databaseURL(env, fallback string) string {
	if value := os.Getenv(env); value != "" {
		return value
	}
	return fallback
}

func connectPool(t *testing.T, env, fallback string) *pgxpool.Pool {
	t.Helper()
	// The suite starts many short-lived pools; under a loaded Docker Desktop a ping can take longer than the
	// old 10s budget even though PostgreSQL is healthy.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL(env, fallback))
	if err != nil {
		t.Fatalf("connect %s: %v", env, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping %s: %v", env, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func adminPool(t *testing.T) *pgxpool.Pool {
	return connectPool(t, "POSTGRES_DATABASE_URL",
		"postgres://postgres:postgres@localhost:5432/agent_platform?sslmode=disable")
}

func apiPool(t *testing.T) *pgxpool.Pool {
	return connectPool(t, "API_DATABASE_URL",
		"postgres://app_api:dev_api_password@localhost:5432/agent_platform?sslmode=disable")
}

func orchestratorPool(t *testing.T) *pgxpool.Pool {
	return connectPool(t, "ORCHESTRATOR_DATABASE_URL",
		"postgres://app_orchestrator:dev_orchestrator_password@localhost:5432/agent_platform?sslmode=disable")
}

type seed struct {
	orgID             string
	projectID         string
	principalID       string
	agentID           string
	agentVersionID    string
	workflowID        string
	workflowVersionID string
	executionID       string
	taskID            string
	siblingTaskID     string
	attemptID         string
	workerID          string
	requestID         string
	eventID           string
	leaseID           string
}

func digest(letter string) string {
	return "sha256:" + strings.Repeat(letter, 64)
}

func seedExecution(t *testing.T, admin *pgxpool.Pool) seed {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	s := seed{
		orgID:             "org_" + suffix,
		projectID:         "proj_" + suffix,
		principalID:       "user_" + suffix,
		agentID:           "agt_" + suffix,
		agentVersionID:    "agtv_" + suffix,
		workflowID:        "wf_" + suffix,
		workflowVersionID: "wfv_" + suffix,
		executionID:       "exec_" + suffix,
		taskID:            "task_" + suffix + "a",
		siblingTaskID:     "task_" + suffix + "b",
		attemptID:         "att_" + suffix,
		workerID:          "wrk_" + suffix,
		requestID:         "req-" + suffix,
		eventID:           "evt_" + suffix,
		leaseID:           uuid.NewString(),
	}

	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO control.organizations (id, name, slug) VALUES ($1, 'Test Org', $2)`,
			[]any{s.orgID, "test-" + suffix[:16]}},
		{`INSERT INTO control.projects (id, org_id, name, slug) VALUES ($1, $2, 'Project', 'project')`,
			[]any{s.projectID, s.orgID}},
		{`INSERT INTO control.memberships (org_id, principal_id, role) VALUES ($1, $2, 'owner')`,
			[]any{s.orgID, s.principalID}},
		{`INSERT INTO control.agents (id, org_id, project_id, name, latest_version) VALUES ($1, $2, $3, 'researcher', 1)`,
			[]any{s.agentID, s.orgID, s.projectID}},
		{`INSERT INTO control.agent_versions
			(id, agent_id, org_id, project_id, version, status, digest, spec, runtime_abi, created_by)
		  VALUES ($1, $2, $3, $4, 1, 'draft', $5, '{"model":"echo-1"}', 1, $6)`,
			[]any{s.agentVersionID, s.agentID, s.orgID, s.projectID, digest("a"), s.principalID}},
		{`INSERT INTO control.workflows (id, org_id, project_id, name, latest_version) VALUES ($1, $2, $3, 'research', 1)`,
			[]any{s.workflowID, s.orgID, s.projectID}},
		{`INSERT INTO control.workflow_versions
			(id, workflow_id, org_id, project_id, version, status, digest, graph, created_by)
		  VALUES ($1, $2, $3, $4, 1, 'draft', $5, '{"nodes":[{"id":"research"},{"id":"publish"}],"edges":[]}', $6)`,
			[]any{s.workflowVersionID, s.workflowID, s.orgID, s.projectID, digest("b"), s.principalID}},
		{`INSERT INTO execution.executions
			(id, org_id, project_id, workflow_id, workflow_version_id, request_id, request_digest,
			 input, input_digest, created_by)
		  VALUES ($1, $2, $3, $4, $5, $6, $7, '{"topic":"x"}', $8, $9)`,
			[]any{s.executionID, s.orgID, s.projectID, s.workflowID, s.workflowVersionID,
				s.requestID, digest("c"), digest("d"), s.principalID}},
		{`INSERT INTO execution.workers
			(id, boot_id, pool, max_concurrency, reported_available_slots, reserved_slots,
			 binary_version, protocol_version, runtime_abis, runtime_digests)
		  VALUES ($1, $2, 'seed', 4, 4, 0, 'dev', 1, '{1}', ARRAY['sha256:' || repeat('e', 64)])`,
			[]any{s.workerID, uuid.NewString()}},
		{`INSERT INTO execution.execution_tasks
			(id, execution_id, org_id, node_id, node_type, agent_version_id, worker_pool, max_attempts)
		  VALUES ($1, $2, $3, 'research', 'agent', $4, 'seed', 2)`,
			[]any{s.taskID, s.executionID, s.orgID, s.agentVersionID}},
		{`INSERT INTO execution.execution_tasks
			(id, execution_id, org_id, node_id, node_type, agent_version_id, worker_pool, max_attempts)
		  VALUES ($1, $2, $3, 'publish', 'agent', $4, 'seed', 1)`,
			[]any{s.siblingTaskID, s.executionID, s.orgID, s.agentVersionID}},
		{`INSERT INTO execution.task_dependencies (execution_id, task_id, depends_on_task_id, org_id)
		  VALUES ($1, $2, $3, $4)`,
			[]any{s.executionID, s.siblingTaskID, s.taskID, s.orgID}},
		{`UPDATE execution.execution_tasks
		  SET state = 'assigned', attempt_count = 1, owner_worker_id = $2,
		      lease_id = $3, lease_expires_at = now() + interval '30 seconds'
		  WHERE id = $1`,
			[]any{s.taskID, s.workerID, s.leaseID}},
		{`INSERT INTO execution.task_attempts
			(id, task_id, execution_id, org_id, attempt_number, worker_id, lease_id, accepted_at, result_inline)
		  VALUES ($1, $2, $3, $4, 1, $5, $6, now(), '{"ok":true}')`,
			[]any{s.attemptID, s.taskID, s.executionID, s.orgID, s.workerID, s.leaseID}},
		{`UPDATE execution.execution_tasks SET latest_attempt_id = $2 WHERE id = $1`,
			[]any{s.taskID, s.attemptID}},
		{`INSERT INTO execution.execution_events
			(event_id, execution_id, org_id, seq, event_type, entity_type, entity_id, actor_type, actor_id, payload)
		  VALUES ($1, $2, $3, 1, 'execution.created', 'execution', $2, 'system', 'orchestrator', '{}')`,
			[]any{s.eventID, s.executionID, s.orgID}},
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt.sql)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM execution.executions WHERE org_id = $1`, s.orgID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM control.workflow_versions WHERE org_id = $1`, s.orgID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM control.workflows WHERE org_id = $1`, s.orgID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM control.agent_versions WHERE org_id = $1`, s.orgID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM control.agents WHERE org_id = $1`, s.orgID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM control.memberships WHERE org_id = $1`, s.orgID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM control.idempotency_keys WHERE org_id = $1`, s.orgID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM control.projects WHERE org_id = $1`, s.orgID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM execution.workers WHERE id = $1`, s.workerID)
		_, _ = admin.Exec(cleanupCtx, `DELETE FROM control.organizations WHERE id = $1`, s.orgID)
	})

	return s
}

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestRLSAndOrgDiscovery(t *testing.T) {
	admin := adminPool(t)
	api := apiPool(t)
	s := seedExecution(t, admin)
	ctx := context.Background()

	t.Run("tenant setting scopes reads", func(t *testing.T) {
		tx, err := api.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, s.orgID); err != nil {
			t.Fatal(err)
		}
		var projects, executions int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM control.projects`).Scan(&projects); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM execution.executions`).Scan(&executions); err != nil {
			t.Fatal(err)
		}
		if projects != 1 || executions != 1 {
			t.Fatalf("tenant scope returned projects=%d executions=%d, want 1/1", projects, executions)
		}
	})

	t.Run("no tenant setting returns no rows", func(t *testing.T) {
		tx, err := api.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var projects int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM control.projects`).Scan(&projects); err != nil {
			t.Fatal(err)
		}
		if projects != 0 {
			t.Fatalf("query without tenant returned %d projects", projects)
		}
	})

	t.Run("cross tenant returns no rows", func(t *testing.T) {
		tx, err := api.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', 'org_01JQ9A0B1C2D3E4F5G6H7J8K', true)`); err != nil {
			t.Fatal(err)
		}
		var projects int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM control.projects`).Scan(&projects); err != nil {
			t.Fatal(err)
		}
		if projects != 0 {
			t.Fatalf("cross-tenant query returned %d projects", projects)
		}
	})

	t.Run("organisation discovery returns only the principal", func(t *testing.T) {
		tx, err := api.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT set_config('app.principal_id', $1, true)`, s.principalID); err != nil {
			t.Fatal(err)
		}
		var orgs int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM control.list_orgs_for_principal()`).Scan(&orgs); err != nil {
			t.Fatal(err)
		}
		if orgs != 1 {
			t.Fatalf("principal discovered %d organisations, want 1", orgs)
		}
	})

	t.Run("api cannot write execution or read outbox", func(t *testing.T) {
		_, err := api.Exec(ctx, `SELECT count(*) FROM execution.outbox`)
		if code := pgErrorCode(err); code != "42501" {
			t.Fatalf("outbox read error = %v (code %s), want permission denied", err, code)
		}
		_, err = api.Exec(ctx, `UPDATE execution.executions SET state = 'running' WHERE id = $1`, s.executionID)
		if code := pgErrorCode(err); code != "42501" {
			t.Fatalf("execution write error = %v (code %s), want permission denied", err, code)
		}
	})
}

func TestConstraintsAndImmutability(t *testing.T) {
	admin := adminPool(t)
	orchestrator := orchestratorPool(t)
	s := seedExecution(t, admin)
	ctx := context.Background()

	t.Run("duplicate idempotency key", func(t *testing.T) {
		_, err := admin.Exec(ctx, `INSERT INTO control.idempotency_keys
			(org_id, key, principal_id, method, path, request_hash, expires_at)
			VALUES ($1, 'key-1', $2, 'POST', '/v1/test', $3, now() + interval '1 day')`,
			s.orgID, s.principalID, digest("f"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = admin.Exec(ctx, `INSERT INTO control.idempotency_keys
			(org_id, key, principal_id, method, path, request_hash, expires_at)
			VALUES ($1, 'key-1', $2, 'POST', '/v1/test', $3, now() + interval '1 day')`,
			s.orgID, s.principalID, digest("f"))
		if code := pgErrorCode(err); code != "23505" {
			t.Fatalf("duplicate key error = %v (code %s), want 23505", err, code)
		}
	})

	t.Run("duplicate execution request id", func(t *testing.T) {
		_, err := admin.Exec(ctx, `INSERT INTO execution.executions
			(id, org_id, project_id, workflow_id, workflow_version_id, request_id, request_digest, input, input_digest, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, '{}', $8, $9)`,
			"exec_"+strings.ReplaceAll(uuid.NewString(), "-", ""), s.orgID, s.projectID, s.workflowID,
			s.workflowVersionID, s.requestID, digest("c"), digest("d"), s.principalID)
		if code := pgErrorCode(err); code != "23505" {
			t.Fatalf("duplicate request error = %v (code %s), want 23505", err, code)
		}
	})

	t.Run("assigned task requires a lease", func(t *testing.T) {
		_, err := admin.Exec(ctx, `INSERT INTO execution.execution_tasks
			(id, execution_id, org_id, node_id, node_type, agent_version_id, state)
			VALUES ($1, $2, $3, 'no_lease', 'agent', $4, 'assigned')`,
			"task_"+strings.ReplaceAll(uuid.NewString(), "-", ""), s.executionID, s.orgID, s.agentVersionID)
		if code := pgErrorCode(err); code != "23514" {
			t.Fatalf("lease check error = %v (code %s), want 23514", err, code)
		}
	})

	t.Run("one open attempt per task", func(t *testing.T) {
		_, err := admin.Exec(ctx, `INSERT INTO execution.task_attempts
			(id, task_id, execution_id, org_id, attempt_number, worker_id, lease_id)
			VALUES ($1, $2, $3, $4, 2, $5, $6)`,
			"att_"+strings.ReplaceAll(uuid.NewString(), "-", ""), s.taskID, s.executionID, s.orgID,
			s.workerID, uuid.NewString())
		if code := pgErrorCode(err); code != "23505" {
			t.Fatalf("open attempt error = %v (code %s), want 23505", err, code)
		}
	})

	t.Run("terminal execution requires ended_at", func(t *testing.T) {
		_, err := admin.Exec(ctx, `UPDATE execution.executions SET state = 'succeeded' WHERE id = $1`, s.executionID)
		if code := pgErrorCode(err); code != "23514" {
			t.Fatalf("terminal check error = %v (code %s), want 23514", err, code)
		}
	})

	t.Run("version content is immutable", func(t *testing.T) {
		_, err := admin.Exec(ctx, `UPDATE control.workflow_versions
			SET graph = '{"nodes":[],"edges":[]}' WHERE id = $1`, s.workflowVersionID)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("content update error = %v, want immutability rejection", err)
		}
		_, err = admin.Exec(ctx, `UPDATE control.workflow_versions
			SET status = 'published', published_at = now() WHERE id = $1`, s.workflowVersionID)
		if err != nil {
			t.Fatalf("publish transition must be allowed: %v", err)
		}
		_, err = admin.Exec(ctx, `UPDATE control.workflow_versions
			SET status = 'published', published_at = now() WHERE id = $1`, s.workflowVersionID)
		if err == nil {
			t.Fatal("publishing twice must be rejected")
		}
	})

	t.Run("events are append-only", func(t *testing.T) {
		_, err := admin.Exec(ctx, `UPDATE execution.execution_events SET reason = 'edited' WHERE event_id = $1`, s.eventID)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("event update error = %v, want append-only rejection", err)
		}
	})

	t.Run("closed attempts are immutable", func(t *testing.T) {
		_, err := orchestrator.Exec(ctx, `UPDATE execution.task_attempts
			SET outcome = 'succeeded', finished_at = now() WHERE id = $1`, s.attemptID)
		if err != nil {
			t.Fatalf("closing an attempt must succeed: %v", err)
		}
		_, err = orchestrator.Exec(ctx, `UPDATE execution.task_attempts SET error_code = 'CHANGED' WHERE id = $1`, s.attemptID)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("closed attempt update error = %v, want immutability rejection", err)
		}
		_, err = orchestrator.Exec(ctx, `UPDATE execution.task_attempts SET result_inline = NULL WHERE id = $1`, s.attemptID)
		if err != nil {
			t.Fatalf("result_inline redaction must be allowed: %v", err)
		}
	})
}

type fakePublisher struct {
	messages []outbox.Message
	err      error
}

func (f *fakePublisher) Publish(_ context.Context, msg outbox.Message) error {
	if f.err != nil {
		return f.err
	}
	f.messages = append(f.messages, msg)
	return nil
}

func TestOutboxContract(t *testing.T) {
	admin := adminPool(t)
	orchestrator := orchestratorPool(t)
	s := seedExecution(t, admin)
	ctx := context.Background()

	newMessage := func(t *testing.T, subject string) outbox.Message {
		t.Helper()
		msg, err := outbox.NewMessage(s.orgID, s.projectID, subject, "execution.created", &s.executionID,
			map[string]any{"seq": 1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}

	msg1 := newMessage(t, "events.org."+s.orgID+".execution."+s.executionID+".created")
	msg2 := newMessage(t, "events.org."+s.orgID+".execution."+s.executionID+".changed")

	err := postgres.WithTx(ctx, orchestrator, func(tx pgx.Tx) error {
		store := outbox.NewStore(orchestrator)
		if err := store.Insert(ctx, tx, msg1); err != nil {
			return err
		}
		return store.Insert(ctx, tx, msg2)
	})
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
	// Keep the live publisher out of this org's rows until a subtest explicitly makes them due.
	if _, err := orchestrator.Exec(ctx,
		`UPDATE execution.outbox SET available_at = now() + interval '1 hour' WHERE org_id = $1`, s.orgID); err != nil {
		t.Fatal(err)
	}
	makeDue := func(t *testing.T, tx pgx.Tx) {
		t.Helper()
		if _, err := tx.Exec(ctx,
			`UPDATE execution.outbox SET available_at = now() WHERE org_id = $1 AND published_at IS NULL`, s.orgID); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("skip locked", func(t *testing.T) {
		tx1, err := orchestrator.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx1.Rollback(ctx) }()
		makeDue(t, tx1)
		tx2, err := orchestrator.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx2.Rollback(ctx) }()

		store := outbox.NewStore(orchestrator)
		first, err := store.ClaimForOrg(ctx, tx1, s.orgID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(first) != 2 {
			t.Fatalf("first claim returned %d messages, want 2", len(first))
		}
		second, err := store.ClaimForOrg(ctx, tx2, s.orgID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(second) != 0 {
			t.Fatalf("second claim returned %d locked messages, want 0", len(second))
		}
	})

	t.Run("failure reschedules", func(t *testing.T) {
		tx, err := orchestrator.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		makeDue(t, tx)

		store := outbox.NewStore(orchestrator)
		claimed, err := store.ClaimForOrg(ctx, tx, s.orgID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(claimed) != 2 {
			t.Fatalf("claim returned %d messages, want 2", len(claimed))
		}
		publisher := &fakePublisher{err: errors.New("broker unavailable")}
		for _, message := range claimed {
			if err := publisher.Publish(ctx, message); err == nil {
				t.Fatal("the fake publisher was configured to fail")
			}
			if err := store.MarkFailed(ctx, tx, message.ID, time.Millisecond, "broker unavailable"); err != nil {
				t.Fatal(err)
			}
		}

		var attempts int
		var lastError pgtype.Text
		var publishedAt pgtype.Timestamptz
		if err := tx.QueryRow(ctx, `SELECT attempts, last_error, published_at
			FROM execution.outbox WHERE message_id = $1`, msg1.MessageID).Scan(&attempts, &lastError, &publishedAt); err != nil {
			t.Fatal(err)
		}
		// A live publisher may have claimed the row once before this transaction; the contract under test is
		// that the failed publish is recorded and the row remains unpublished.
		if attempts < 1 || !lastError.Valid || publishedAt.Valid {
			t.Fatalf("failed outbox row: attempts=%d last_error=%v published_at=%v", attempts, lastError, publishedAt)
		}
	})

	t.Run("success marks published", func(t *testing.T) {
		tx, err := orchestrator.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		makeDue(t, tx)

		store := outbox.NewStore(orchestrator)
		claimed, err := store.ClaimForOrg(ctx, tx, s.orgID, 10)
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		publisher := &fakePublisher{}
		for _, message := range claimed {
			if err := publisher.Publish(ctx, message); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkPublished(ctx, tx, message.ID); err != nil {
				t.Fatal(err)
			}
		}
		if len(claimed) != 2 || len(publisher.messages) != 2 {
			t.Fatalf("published=%d delivered=%d, want 2/2", len(claimed), len(publisher.messages))
		}
		var unpublished int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM execution.outbox
			WHERE org_id = $1 AND published_at IS NULL`, s.orgID).Scan(&unpublished); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if unpublished != 0 {
			t.Fatalf("%d outbox rows remain unpublished", unpublished)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

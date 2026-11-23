//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/messaging"
	"github.com/agent-platform/agent-platform/internal/worker/control"
	"github.com/agent-platform/agent-platform/internal/worker/delivery"
)

type testWorker struct {
	id      string
	pool    string
	control *control.Client
	js      jetstream.JetStream
	nc      *nats.Conn
	cancel  context.CancelFunc
}

func startTestWorker(t *testing.T, suffix, pool string) *testWorker {
	t.Helper()
	nc, err := nats.Connect(natsURL(t), nats.Name("test-worker-"+suffix))
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	workerID := "wrk_msg_" + suffix
	ctx, cancel := context.WithCancel(context.Background())
	controlClient := control.New(control.Config{
		Address:         orchestratorGRPCAddr(t),
		Insecure:        true,
		WorkerID:        workerID,
		Pool:            pool,
		MaxConcurrency:  4,
		BinaryVersion:   "test",
		RuntimeABIs:     []int32{1},
		HeartbeatPeriod: time.Second,
		ReconnectDelay:  500 * time.Millisecond,
	}, zap.NewNop())
	go func() { _ = controlClient.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for !controlClient.Connected() {
		if time.Now().After(deadline) {
			cancel()
			nc.Close()
			t.Fatal("test worker did not register")
		}
		time.Sleep(50 * time.Millisecond)
	}

	worker := &testWorker{id: workerID, pool: pool, control: controlClient, js: js, nc: nc, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		time.Sleep(100 * time.Millisecond)
		background, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		admin := adminPool(t)
		if _, err := admin.Exec(background, `DELETE FROM execution.workers WHERE id = $1`, workerID); err != nil {
			t.Logf("delete test worker: %v", err)
		}
		nc.Close()
	})
	return worker
}

func (w *testWorker) startConsumer(t *testing.T, handler messaging.Handler, configure func(*messaging.ConsumerConfig)) {
	t.Helper()
	config := messaging.DefaultConsumerConfig(w.id, 4)
	if configure != nil {
		configure(&config)
	}
	consumer, err := messaging.NewConsumer(context.Background(), w.js, config, handler, zap.NewNop())
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	t.Cleanup(func() {
		consumer.Stop()
		background, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = w.js.DeleteConsumer(background, messaging.StreamTaskDispatch, config.Durable)
	})
}

type fixtureExecution struct {
	orgID       string
	projectID   string
	workflowID  string
	executionID string
	taskIDs     []string
}

func createFixtureExecution(t *testing.T, client *apiClient, suffix, pool string, twoNodes bool) fixtureExecution {
	t.Helper()
	org := client.request(http.MethodPost, "/v1/orgs", map[string]any{
		"name": "Messaging Org", "slug": "messaging-" + suffix,
	}, nil, http.StatusCreated)
	orgID := asString(t, org, "id")
	admin := adminPool(t)
	t.Cleanup(func() { cleanupAPITestOrg(t, admin, orgID) })

	project := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects", map[string]any{
		"name": "Messaging", "slug": "messaging",
	}, nil, http.StatusCreated)
	projectID := asString(t, project, "id")

	agent := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/agents", map[string]any{
		"name": "worker-agent", "description": "messaging delivery test",
		"version": map[string]any{"spec": map[string]any{
			"model": map[string]any{
				"provider": "echo", "name": "echo-1", "credential_ref": "default",
			},
			"instructions": "Return the input.",
			"runtime": map[string]any{
				"abi_version": 1, "image_digest": "sha256:" + strings.Repeat("a", 64),
			},
			"limits":     map[string]any{"max_turns": 4, "timeout_seconds": 120},
			"checkpoint": map[string]any{"mode": "on_node_end", "require_checkpoint": false},
		}},
	}, nil, http.StatusCreated)
	agentID := asString(t, agent, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/agents/"+agentID+"/versions/1:publish",
		nil, nil, http.StatusOK)

	nodes := []any{map[string]any{
		"id": "plan", "type": "agent",
		"agent":       map[string]any{"agent_id": agentID, "version": 1},
		"worker_pool": pool, "timeout_seconds": 60,
	}}
	edges := []any{}
	if twoNodes {
		nodes = append([]any{map[string]any{
			"id": "collect", "type": "tool",
			"tool":            map[string]any{"tool_id": "tool_web_search", "version": 1},
			"worker_pool":     pool,
			"timeout_seconds": 60,
		}}, nodes...)
		edges = append(edges, map[string]any{"from": "collect", "to": "plan"})
	}

	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{
			"name": "delivery-flow",
			"version": map[string]any{"graph": map[string]any{
				"nodes":        nodes,
				"edges":        edges,
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
			"input": map[string]any{"topic": "durable delivery"},
		}, map[string]string{"Idempotency-Key": "msg-" + suffix}, http.StatusAccepted)
	executionID := asString(t, execution, "id")

	fixture := fixtureExecution{orgID: orgID, projectID: projectID, workflowID: workflowID, executionID: executionID}
	rows, err := admin.Query(context.Background(),
		`SELECT id FROM execution.execution_tasks WHERE execution_id = $1 ORDER BY created_at`, executionID)
	if err != nil {
		t.Fatalf("query fixture tasks: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			t.Fatalf("scan fixture task: %v", err)
		}
		fixture.taskIDs = append(fixture.taskIDs, taskID)
	}
	return fixture
}

func waitExecutionState(t *testing.T, client *apiClient, fixture fixtureExecution, want string) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	path := "/v1/orgs/" + fixture.orgID + "/projects/" + fixture.projectID + "/executions/" + fixture.executionID
	for time.Now().Before(deadline) {
		execution := client.request(http.MethodGet, path, nil, nil, http.StatusOK)
		state, _ := execution["state"].(string)
		if state == want {
			return
		}
		if state == "failed" || state == "cancelled" {
			t.Fatalf("execution %s ended %s", fixture.executionID, state)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("execution %s did not reach %s", fixture.executionID, want)
}

type crashOnceHandler struct {
	inner     messaging.Handler
	crashed   atomic.Bool
	delivered atomic.Int64
}

func (h *crashOnceHandler) Handle(ctx context.Context, msg jetstream.Msg) error {
	h.delivered.Add(1)
	if h.crashed.CompareAndSwap(false, true) {
		if err := h.inner.Handle(ctx, msg); err != nil {
			return err
		}
		// The result is durably committed; crash before the broker acknowledgement.
		return errors.New("simulated crash before ack")
	}
	return h.inner.Handle(ctx, msg)
}

func TestMessagingRedeliveryAndDuplicateSuppression(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("msg_" + suffix)
	pool := "test-" + suffix

	worker := startTestWorker(t, suffix, pool)
	handler := &crashOnceHandler{inner: &delivery.Handler{
		Client: worker.control, WorkerID: worker.id, Execute: delivery.Echo, Log: zap.NewNop(),
	}}
	worker.startConsumer(t, handler, func(cfg *messaging.ConsumerConfig) {
		cfg.MaxDeliver = 5
		cfg.AckWait = 2 * time.Second
		cfg.BackOff = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	})

	fixture := createFixtureExecution(t, client, suffix, pool, true)
	waitExecutionState(t, client, fixture, "succeeded")

	if handler.delivered.Load() < 3 {
		t.Fatalf("expected redelivery of the first task plus the second task, deliveries=%d", handler.delivered.Load())
	}
	if !handler.crashed.Load() {
		t.Fatal("the simulated crash path never ran")
	}

	admin := adminPool(t)
	ctx := context.Background()
	var executionState string
	if err := admin.QueryRow(ctx, `SELECT state FROM execution.executions WHERE id = $1`, fixture.executionID).
		Scan(&executionState); err != nil {
		t.Fatal(err)
	}
	if executionState != "succeeded" {
		t.Fatalf("execution state = %s, want succeeded", executionState)
	}
	var attempts int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM execution.task_attempts WHERE execution_id = $1`, fixture.executionID).
		Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != len(fixture.taskIDs) {
		t.Fatalf("attempts = %d, want one per task (%d); redelivery created a duplicate logical attempt",
			attempts, len(fixture.taskIDs))
	}
	var duplicateCompletions int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT attempt.task_id, count(*) AS completions
			FROM execution.execution_events event
			JOIN execution.task_attempts attempt ON attempt.id = event.entity_id
			WHERE event.execution_id = $1 AND event.event_type = 'task.attempt_finished'
			GROUP BY attempt.task_id
			HAVING count(*) > 1
		) duplicates`, fixture.executionID).Scan(&duplicateCompletions); err != nil {
		t.Fatal(err)
	}
	if duplicateCompletions != 0 {
		t.Fatalf("redelivery duplicated %d attempt-finished events", duplicateCompletions)
	}
	var succeededTasks int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM execution.execution_tasks
		WHERE execution_id = $1 AND state = 'succeeded'`, fixture.executionID).Scan(&succeededTasks); err != nil {
		t.Fatal(err)
	}
	if succeededTasks != len(fixture.taskIDs) {
		t.Fatalf("succeeded tasks = %d, want %d", succeededTasks, len(fixture.taskIDs))
	}

	// The broker must have nothing left unacknowledged or undelivered for the test worker.
	stream, err := worker.js.Stream(ctx, messaging.StreamTaskDispatch)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := stream.Consumer(ctx, "worker-"+worker.id)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info, err := consumer.Info(ctx)
		if err == nil && info.NumAckPending == 0 && info.NumPending == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("dispatch consumer still has unacknowledged or pending messages")
}

type poisonSwitchHandler struct {
	good messaging.Handler
	bad  atomic.Bool
}

func (h *poisonSwitchHandler) Handle(ctx context.Context, msg jetstream.Msg) error {
	if h.bad.Load() {
		return messaging.Permanent(errors.New("simulated poison message"))
	}
	return h.good.Handle(ctx, msg)
}

func TestMessagingDeadLetterAndRedrive(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("dlq_" + suffix)
	pool := "dlq-" + suffix

	worker := startTestWorker(t, suffix, pool)
	handler := &poisonSwitchHandler{good: &delivery.Handler{
		Client: worker.control, WorkerID: worker.id, Execute: delivery.Echo, Log: zap.NewNop(),
	}}
	handler.bad.Store(true)
	worker.startConsumer(t, handler, func(cfg *messaging.ConsumerConfig) {
		cfg.MaxDeliver = 2
		cfg.AckWait = 2 * time.Second
		cfg.BackOff = []time.Duration{100 * time.Millisecond}
	})

	fixture := createFixtureExecution(t, client, suffix, pool, false)
	ctx := context.Background()
	dlqStream, err := worker.js.Stream(ctx, messaging.StreamTaskDLQ)
	if err != nil {
		t.Fatal(err)
	}

	// Inspect only this worker's dead letters. The stream retains messages for seven days, so a stale message
	// from another worker or test must not satisfy the wait.
	inspectorName := "inspect-" + suffix
	inspector, err := dlqStream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       inspectorName,
		FilterSubject: messaging.DLQSubject(worker.id),
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		t.Fatalf("inspect DLQ: %v", err)
	}
	t.Cleanup(func() {
		background, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = dlqStream.DeleteConsumer(background, inspectorName)
		_ = dlqStream.DeleteConsumer(background, "redrive-"+worker.id)
	})
	var envelope messaging.DLQEnvelope
	var sequence uint64
	found := false
	deadline := time.Now().Add(20 * time.Second)
	for !found {
		batch, err := inspector.FetchNoWait(1)
		if err != nil {
			t.Fatalf("fetch DLQ: %v", err)
		}
		for message := range batch.Messages() {
			if err := json.Unmarshal(message.Data(), &envelope); err != nil {
				t.Fatalf("decode DLQ envelope: %v", err)
			}
			meta, err := message.Metadata()
			if err == nil {
				sequence = meta.Sequence.Stream
			}
			found = true
		}
		if !found {
			if time.Now().After(deadline) {
				t.Fatal("dispatch was not dead-lettered")
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if envelope.OriginalSubject != "tasks.dispatch."+worker.id {
		t.Fatalf("original subject = %q", envelope.OriginalSubject)
	}
	if envelope.ExecutionID != fixture.executionID {
		t.Fatalf("DLQ execution_id = %q, want %q", envelope.ExecutionID, fixture.executionID)
	}
	if envelope.Error == "" || envelope.Deliveries < 1 || len(envelope.Payload) == 0 {
		t.Fatalf("DLQ envelope is missing failure context: %+v", envelope)
	}

	handler.bad.Store(false)
	redriven, err := messaging.RedriveDLQ(ctx, worker.js, worker.id, 10, zap.NewNop())
	if err != nil {
		t.Fatalf("redrive: %v", err)
	}
	if redriven < 1 {
		t.Fatalf("redriven = %d, want at least one", redriven)
	}
	waitExecutionState(t, client, fixture, "succeeded")

	admin := adminPool(t)
	var succeededAttempts int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM execution.task_attempts
		WHERE execution_id = $1 AND outcome = 'succeeded'`, fixture.executionID).Scan(&succeededAttempts); err != nil {
		t.Fatal(err)
	}
	if succeededAttempts != 1 {
		t.Fatalf("succeeded attempts = %d, want 1", succeededAttempts)
	}
	if sequence > 0 {
		if err := dlqStream.DeleteMsg(ctx, sequence); err != nil {
			t.Logf("delete DLQ test message: %v", err)
		}
	}
}

func natsURL(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("ORCHESTRATOR_NATS_URL"); value != "" {
		return value
	}
	return "nats://localhost:4222"
}

func orchestratorGRPCAddr(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("ORCHESTRATOR_GRPC_ADDR"); value != "" {
		return value
	}
	return "localhost:9090"
}

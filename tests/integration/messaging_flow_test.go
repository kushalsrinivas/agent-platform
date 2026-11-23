//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/messaging"
	"github.com/agent-platform/agent-platform/internal/worker/control"
	"github.com/agent-platform/agent-platform/internal/worker/delivery"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

type testWorker struct {
	id        string
	pool      string
	capacity  int
	control   *control.Client
	js        jetstream.JetStream
	nc        *nats.Conn
	cancel    context.CancelFunc
	done      <-chan struct{}
	mu        sync.Mutex
	consumers []*messaging.Consumer
	crashed   bool
}

func startTestWorker(t *testing.T, suffix, pool string) *testWorker {
	return startTestWorkerWithCapacity(t, suffix, pool, 4)
}

func startTestWorkerWithCapacity(t *testing.T, suffix, pool string, capacity int) *testWorker {
	return startTestWorkerNamed(t, "wrk_msg_"+suffix, pool, capacity)
}

func startTestWorkerNamed(t *testing.T, workerID, pool string, capacity int) *testWorker {
	t.Helper()
	if capacity < 1 {
		capacity = 1
	}
	nc, err := nats.Connect(natsURL(t), nats.Name("test-worker-"+workerID))
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	controlClient := control.New(control.Config{
		Address:         orchestratorGRPCAddr(t),
		Insecure:        true,
		WorkerID:        workerID,
		Pool:            pool,
		Runtime:         "python",
		RuntimeVersion:  "test-1.0.0",
		Capabilities:    []string{"agent_execution", "tool_execution"},
		CPUCores:        2,
		MemoryMB:        2048,
		MaxConcurrency:  capacity,
		BinaryVersion:   "test",
		RuntimeABIs:     []int32{1},
		HeartbeatPeriod: time.Second,
		ReconnectDelay:  500 * time.Millisecond,
	}, zap.NewNop())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = controlClient.Run(ctx)
	}()

	deadline := time.Now().Add(20 * time.Second)
	for !controlClient.Connected() {
		if time.Now().After(deadline) {
			cancel()
			nc.Close()
			t.Fatal("test worker did not register")
		}
		time.Sleep(50 * time.Millisecond)
	}

	worker := &testWorker{
		id: workerID, pool: pool, capacity: capacity, control: controlClient,
		js: js, nc: nc, cancel: cancel, done: done,
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Logf("test worker control loop did not stop")
		}
		background, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		admin := adminPool(t)
		// The worker may still own an assigned test task; fixtures are cleaned up in LIFO order, so detach
		// tasks first rather than depending on the org cleanup having run.
		if _, err := admin.Exec(background,
			`DELETE FROM execution.execution_tasks WHERE owner_worker_id = $1`, workerID); err != nil {
			t.Logf("delete test worker tasks: %v", err)
		}
		if _, err := admin.Exec(background, `DELETE FROM execution.workers WHERE id = $1`, workerID); err != nil {
			t.Logf("delete test worker: %v", err)
		}
		nc.Close()
	})
	return worker
}

// crash stops heartbeats and message consumption without touching the database, which is what a killed worker
// process leaves behind. Recovery must take over from the durable lease.
func (w *testWorker) crash() {
	w.mu.Lock()
	if w.crashed {
		w.mu.Unlock()
		return
	}
	w.crashed = true
	consumers := append([]*messaging.Consumer(nil), w.consumers...)
	w.mu.Unlock()
	for _, consumer := range consumers {
		consumer.Stop()
	}
	w.cancel()
}

func (w *testWorker) startConsumer(t *testing.T, handler messaging.Handler, configure func(*messaging.ConsumerConfig)) {
	t.Helper()
	config := messaging.DefaultConsumerConfig(w.id, w.capacity)
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
	w.mu.Lock()
	w.consumers = append(w.consumers, consumer)
	w.mu.Unlock()
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

func createWorkerProject(t *testing.T, client *apiClient, suffix string) (string, string, string) {
	return createWorkerProjectWithTools(t, client, suffix, nil)
}

func createWorkerProjectWithTools(t *testing.T, client *apiClient, suffix string,
	tools []any) (string, string, string) {
	t.Helper()
	org := client.request(http.MethodPost, "/v1/orgs", map[string]any{
		"name": "Worker Manager Org", "slug": "worker-manager-" + suffix,
	}, nil, http.StatusCreated)
	orgID := asString(t, org, "id")
	admin := adminPool(t)
	t.Cleanup(func() { cleanupAPITestOrg(t, admin, orgID) })

	project := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects", map[string]any{
		"name": "Worker Manager", "slug": "worker-manager",
	}, nil, http.StatusCreated)
	projectID := asString(t, project, "id")

	spec := map[string]any{
		"model": map[string]any{
			"provider": "echo", "name": "echo-1", "credential_ref": "default",
		},
		"instructions": "Return the input.",
		"runtime": map[string]any{
			"abi_version": 1, "image_digest": "sha256:" + strings.Repeat("a", 64),
		},
		"limits":     map[string]any{"max_turns": 4, "timeout_seconds": 120},
		"checkpoint": map[string]any{"mode": "on_node_end", "require_checkpoint": false},
	}
	if len(tools) > 0 {
		spec["tools"] = tools
	}
	agent := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/agents", map[string]any{
		"name": "worker-agent", "description": "worker manager test",
		"version": map[string]any{"spec": spec},
	}, nil, http.StatusCreated)
	agentID := asString(t, agent, "id")
	client.request(http.MethodPost,
		"/v1/orgs/"+orgID+"/projects/"+projectID+"/agents/"+agentID+"/versions/1:publish",
		nil, nil, http.StatusOK)
	return orgID, projectID, agentID
}

func loadTaskIDs(t *testing.T, executionID string) []string {
	t.Helper()
	admin := adminPool(t)
	rows, err := admin.Query(context.Background(),
		`SELECT id FROM execution.execution_tasks WHERE execution_id = $1 ORDER BY created_at, node_id`, executionID)
	if err != nil {
		t.Fatalf("query fixture tasks: %v", err)
	}
	defer rows.Close()
	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			t.Fatalf("scan fixture task: %v", err)
		}
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs
}

func createFixtureExecution(t *testing.T, client *apiClient, suffix, pool string, twoNodes bool) fixtureExecution {
	t.Helper()
	orgID, projectID, agentID := createWorkerProject(t, client, suffix)
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

	return fixtureExecution{
		orgID: orgID, projectID: projectID, workflowID: workflowID, executionID: executionID,
		taskIDs: loadTaskIDs(t, executionID),
	}
}

func waitExecutionState(t *testing.T, client *apiClient, fixture fixtureExecution, want string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
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

type schedulerOptions struct {
	Pool              string
	NodeCount         int
	Independent       bool
	Priority          int32
	MaxAttempts       int
	RetryOn           []any
	OutputSchema      map[string]any
	SideEffectClass   string
	IdempotencyKey    string
	CheckpointMode    string
	RequireCheckpoint bool
	DeadlineAt        *time.Time
}

func createSchedulerFixture(t *testing.T, client *apiClient, suffix string, opts schedulerOptions) fixtureExecution {
	t.Helper()
	orgID, projectID, agentID := createWorkerProject(t, client, suffix)
	if opts.NodeCount < 1 {
		opts.NodeCount = 1
	}
	nodes := make([]any, 0, opts.NodeCount)
	edges := make([]any, 0, opts.NodeCount)
	for index := 1; index <= opts.NodeCount; index++ {
		nodeID := fmt.Sprintf("task_%d", index)
		node := map[string]any{
			"id": nodeID, "type": "agent",
			"agent":       map[string]any{"agent_id": agentID, "version": 1},
			"worker_pool": opts.Pool, "timeout_seconds": 60,
		}
		if opts.MaxAttempts > 1 {
			retry := map[string]any{"max_attempts": opts.MaxAttempts}
			if len(opts.RetryOn) > 0 {
				retry["retry_on"] = opts.RetryOn
			}
			node["retry"] = retry
		}
		if opts.OutputSchema != nil {
			node["output_schema"] = opts.OutputSchema
		}
		if opts.SideEffectClass != "" {
			node["side_effect_class"] = opts.SideEffectClass
		}
		if opts.IdempotencyKey != "" {
			node["idempotency_key"] = opts.IdempotencyKey
		}
		if opts.CheckpointMode != "" {
			node["checkpoint_mode"] = opts.CheckpointMode
		}
		if opts.RequireCheckpoint {
			node["require_checkpoint"] = true
		}
		nodes = append(nodes, node)
		if !opts.Independent && index > 1 {
			edges = append(edges, map[string]any{"from": fmt.Sprintf("task_%d", index-1), "to": nodeID})
		}
	}
	workflow := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/workflows",
		map[string]any{
			"name": "scheduler-flow",
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

	executionRequest := map[string]any{
		"workflow_id": workflowID, "workflow_version": 1,
		"input": map[string]any{"topic": "scheduler"}, "priority": opts.Priority,
	}
	if opts.DeadlineAt != nil {
		executionRequest["deadline_at"] = opts.DeadlineAt.UTC().Format(time.RFC3339Nano)
	}
	execution := client.request(http.MethodPost, "/v1/orgs/"+orgID+"/projects/"+projectID+"/executions",
		executionRequest, map[string]string{"Idempotency-Key": "sched-" + suffix}, http.StatusAccepted)
	executionID := asString(t, execution, "id")
	return fixtureExecution{
		orgID: orgID, projectID: projectID, workflowID: workflowID, executionID: executionID,
		taskIDs: loadTaskIDs(t, executionID),
	}
}

func submitExtraExecution(t *testing.T, client *apiClient, fixture fixtureExecution, key string, priority int32) fixtureExecution {
	t.Helper()
	execution := client.request(http.MethodPost,
		"/v1/orgs/"+fixture.orgID+"/projects/"+fixture.projectID+"/executions",
		map[string]any{
			"workflow_id": fixture.workflowID, "workflow_version": 1,
			"input": map[string]any{"topic": "scheduler"}, "priority": priority,
		}, map[string]string{"Idempotency-Key": key}, http.StatusAccepted)
	executionID := asString(t, execution, "id")
	return fixtureExecution{
		orgID: fixture.orgID, projectID: fixture.projectID, workflowID: fixture.workflowID,
		executionID: executionID, taskIDs: loadTaskIDs(t, executionID),
	}
}

func TestWorkerManagerDistributesTasks(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("wm_dist_" + suffix)
	pool := "wm-dist-" + suffix

	workerA := startTestWorkerWithCapacity(t, suffix+"a", pool, 1)
	workerB := startTestWorkerWithCapacity(t, suffix+"b", pool, 1)
	handled := make(chan string, 4)
	startConsumer := func(worker *testWorker) {
		execute := func(ctx context.Context, dispatch *contracts.TaskDispatch) (delivery.ExecutionOutcome, error) {
			handled <- worker.id + "|" + dispatch.TaskID
			time.Sleep(2 * time.Second)
			return delivery.Echo(ctx, dispatch)
		}
		worker.startConsumer(t, &delivery.Handler{
			Client: worker.control, WorkerID: worker.id, Execute: execute, Log: zap.NewNop(),
		}, nil)
	}
	startConsumer(workerA)
	startConsumer(workerB)

	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 2, Independent: true,
	})
	waitExecutionState(t, client, fixture, "succeeded")

	handledBy := map[string]string{}
	for index := 0; index < 2; index++ {
		select {
		case value := <-handled:
			parts := strings.SplitN(value, "|", 2)
			handledBy[parts[1]] = parts[0]
		case <-time.After(20 * time.Second):
			t.Fatal("both workers did not report a task")
		}
	}
	if len(handledBy) != 2 {
		t.Fatalf("handled tasks = %v", handledBy)
	}
	workers := map[string]struct{}{}
	for _, workerID := range handledBy {
		workers[workerID] = struct{}{}
	}
	if len(workers) != 2 {
		t.Fatalf("expected both workers to receive work, got %v", handledBy)
	}
}

func TestWorkerManagerPriorityOrder(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("wm_prio_" + suffix)
	pool := "wm-prio-" + suffix

	normal := createSchedulerFixture(t, client, suffix, schedulerOptions{Pool: pool, NodeCount: 1})
	high := submitExtraExecution(t, client, normal, "high-"+suffix, 100)
	admin := adminPool(t)
	rows, err := admin.Query(context.Background(), `
		SELECT e.id, e.priority, t.id, t.priority, t.state, COALESCE(t.worker_pool, '')
		FROM execution.executions e JOIN execution.execution_tasks t ON t.execution_id = e.id
		WHERE e.id = ANY($1) ORDER BY e.priority`, []string{normal.executionID, high.executionID})
	if err == nil {
		for rows.Next() {
			var executionID, taskID, state, workerPool string
			var executionPriority, taskPriority int
			if err := rows.Scan(&executionID, &executionPriority, &taskID, &taskPriority, &state, &workerPool); err == nil {
				t.Logf("priority fixture execution=%s priority=%d task=%s task_priority=%d state=%s pool=%s",
					executionID, executionPriority, taskID, taskPriority, state, workerPool)
				if executionID == high.executionID && taskPriority != 100 {
					t.Fatalf("high-priority task has priority %d", taskPriority)
				}
				if workerPool != pool {
					t.Fatalf("task pool = %q, want %q", workerPool, pool)
				}
			}
		}
		rows.Close()
	}
	startTestWorkerWithCapacity(t, suffix, pool, 1)

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		var executionID string
		err := admin.QueryRow(context.Background(), `
			SELECT execution_id FROM execution.execution_tasks
			WHERE execution_id = ANY($1) AND state = 'assigned'
			ORDER BY created_at LIMIT 1`, []string{normal.executionID, high.executionID}).Scan(&executionID)
		if err == nil {
			if executionID != high.executionID {
				rows, queryErr := admin.Query(context.Background(), `
					SELECT t.id, t.priority, t.state, e.org_id, e.state, COALESCE(t.worker_pool, ''),
					       COALESCE(t.owner_worker_id, ''),
					       row_number() OVER (PARTITION BY e.org_id ORDER BY t.priority DESC, t.created_at, t.id) AS tenant_rank,
					       t.priority + LEAST(FLOOR(EXTRACT(EPOCH FROM (now() - t.created_at)) / 60)::integer, 200) AS effective
					FROM execution.execution_tasks t
					JOIN execution.executions e ON e.id = t.execution_id
					WHERE e.id = ANY($1) ORDER BY tenant_rank, effective DESC`,
					[]string{normal.executionID, high.executionID})
				if queryErr == nil {
					for rows.Next() {
						var taskID, taskState, orgID, executionState, workerPool, owner string
						var taskPriority, rank, effective int
						if err := rows.Scan(&taskID, &taskPriority, &taskState, &orgID, &executionState, &workerPool, &owner, &rank, &effective); err == nil {
							t.Logf("rank=%d effective=%d priority=%d task=%s task_state=%s pool=%s owner=%s org=%s execution_state=%s",
								rank, effective, taskPriority, taskID, taskState, workerPool, owner, orgID, executionState)
						}
					}
					rows.Close()
				}
				workerRows, workerErr := admin.Query(context.Background(), `
					SELECT id, status, max_concurrency, reserved_slots, reported_available_slots, pool
					FROM execution.workers ORDER BY id`)
				if workerErr == nil {
					for workerRows.Next() {
						var workerID, status, workerPool string
						var maxConcurrency, reserved, reported int
						if err := workerRows.Scan(&workerID, &status, &maxConcurrency, &reserved, &reported, &workerPool); err == nil {
							t.Logf("worker=%s status=%s pool=%s slots=%d/%d reported=%d",
								workerID, status, workerPool, reserved, maxConcurrency, reported)
						}
					}
					workerRows.Close()
				}
				t.Fatalf("assigned execution = %s, want high-priority %s", executionID, high.executionID)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("no task was assigned")
}

func TestWorkerManagerTenantFairness(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("wm_fair_" + suffix)
	pool := "wm-fair-" + suffix

	first := createSchedulerFixture(t, client, suffix+"a", schedulerOptions{
		Pool: pool, NodeCount: 2, Independent: true,
	})
	second := createSchedulerFixture(t, client, suffix+"b", schedulerOptions{
		Pool: pool, NodeCount: 2, Independent: true,
	})
	startTestWorkerWithCapacity(t, suffix+"a", pool, 1)
	startTestWorkerWithCapacity(t, suffix+"b", pool, 1)
	admin := adminPool(t)

	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := admin.Query(context.Background(), `
			SELECT DISTINCT e.org_id
			FROM execution.execution_tasks t
			JOIN execution.executions e ON e.id = t.execution_id
			WHERE t.execution_id = ANY($1) AND t.state = 'assigned'`,
			[]string{first.executionID, second.executionID})
		if err != nil {
			t.Fatal(err)
		}
		var orgIDs []string
		for rows.Next() {
			var orgID string
			if err := rows.Scan(&orgID); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			orgIDs = append(orgIDs, orgID)
		}
		rows.Close()
		if len(orgIDs) == 2 {
			if orgIDs[0] == orgIDs[1] {
				t.Fatalf("both assignments came from one tenant: %v", orgIDs)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("two tenants were not both assigned work")
}

func TestWorkerManagerLeaseRenewal(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("wm_lease_" + suffix)
	pool := "wm-lease-" + suffix

	worker := startTestWorkerWithCapacity(t, suffix, pool, 1)
	worker.startConsumer(t, &delivery.Handler{
		Client: worker.control, WorkerID: worker.id, Log: zap.NewNop(),
		Execute: func(ctx context.Context, dispatch *contracts.TaskDispatch) (delivery.ExecutionOutcome, error) {
			time.Sleep(3 * time.Second)
			return delivery.Echo(ctx, dispatch)
		},
	}, nil)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{Pool: pool, NodeCount: 1})
	admin := adminPool(t)

	var before time.Time
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		err := admin.QueryRow(context.Background(), `
			SELECT lease_expires_at FROM execution.execution_tasks
			WHERE id = $1 AND state = 'running'`, fixture.taskIDs[0]).Scan(&before)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if before.IsZero() {
		t.Fatal("task never started")
	}
	time.Sleep(2 * time.Second)
	var after time.Time
	if err := admin.QueryRow(context.Background(), `
		SELECT lease_expires_at FROM execution.execution_tasks WHERE id = $1`,
		fixture.taskIDs[0]).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.After(before) {
		t.Fatalf("lease was not renewed: before=%s after=%s", before, after)
	}
	waitExecutionState(t, client, fixture, "succeeded")
}

func TestWorkerManagerStaleWorkerCannotCommit(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("wm_fence_" + suffix)
	pool := "wm-fence-" + suffix

	workerA := startTestWorkerWithCapacity(t, suffix+"a", pool, 1)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1, MaxAttempts: 2,
	})
	admin := adminPool(t)

	var taskID, attemptID, leaseID string
	var attemptNumber int
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		err := admin.QueryRow(context.Background(), `
			SELECT t.id, a.id, a.lease_id, a.attempt_number
			FROM execution.execution_tasks t
			JOIN execution.task_attempts a ON a.task_id = t.id
			WHERE t.execution_id = $1 AND t.state = 'assigned' AND a.finished_at IS NULL`,
			fixture.executionID).Scan(&taskID, &attemptID, &leaseID, &attemptNumber)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if taskID == "" {
		t.Fatal("task was never assigned to the first worker")
	}

	workerA.cancel()
	time.Sleep(500 * time.Millisecond)
	if _, err := admin.Exec(context.Background(), `
		UPDATE execution.workers SET status = 'unhealthy', last_heartbeat_at = now() - interval '30 seconds'
		WHERE id = $1`, workerA.id); err != nil {
		t.Fatal(err)
	}
	// Recovery may already have reaped the terminated worker's attempt during the disconnect; only expire a
	// lease that is still active.
	if _, err := admin.Exec(context.Background(), `
		UPDATE execution.execution_tasks SET lease_expires_at = now() - interval '1 second'
		WHERE id = $1 AND state IN ('assigned', 'running')`, taskID); err != nil {
		t.Fatal(err)
	}

	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		var outcome string
		err := admin.QueryRow(context.Background(), `
			SELECT t.state, a.outcome FROM execution.execution_tasks t
			JOIN execution.task_attempts a ON a.id = $2
			WHERE t.id = $1`, taskID, attemptID).Scan(&state, &outcome)
		if err == nil && state == "pending" && outcome == "lease_expired" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	workerB := startTestWorkerWithCapacity(t, suffix+"b", pool, 1)
	workerB.startConsumer(t, &delivery.Handler{
		Client: workerB.control, WorkerID: workerB.id, Execute: delivery.Echo, Log: zap.NewNop(),
	}, nil)
	waitExecutionState(t, client, fixture, "succeeded")

	if _, err := admin.Exec(context.Background(), `
		UPDATE execution.workers SET status = 'available', last_heartbeat_at = now() WHERE id = $1`,
		workerA.id); err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient(orchestratorGRPCAddr(t),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.AppendToOutgoingContext(context.Background(), control.WorkerIDMetadataKey, workerA.id)
	output, err := structpb.NewStruct(map[string]any{"stale": true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := workerv1.NewWorkerControlClient(conn).ReportTaskResult(ctx,
		&workerv1.ReportTaskResultRequest{
			TaskId: taskID, AttemptId: attemptID, LeaseId: leaseID, AttemptNumber: int32(attemptNumber), //nolint:gosec // bounded by max_attempts.
			Outcome: workerv1.TaskOutcome_TASK_OUTCOME_SUCCEEDED, Output: output,
		})
	if err != nil {
		t.Fatalf("stale result was not answered: %v", err)
	}
	if response.GetStatus() != workerv1.ResultStatus_RESULT_STATUS_STALE_LEASE {
		t.Fatalf("stale result status = %s, want STALE_LEASE", response.GetStatus())
	}

	var succeeded, expired int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE outcome = 'succeeded'),
		       count(*) FILTER (WHERE outcome = 'lease_expired')
		FROM execution.task_attempts WHERE execution_id = $1`, fixture.executionID).
		Scan(&succeeded, &expired); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || expired != 1 {
		t.Fatalf("attempt outcomes succeeded=%d lease_expired=%d, want 1/1", succeeded, expired)
	}
	var taskState, executionState string
	if err := admin.QueryRow(context.Background(), `
		SELECT t.state, e.state FROM execution.execution_tasks t
		JOIN execution.executions e ON e.id = t.execution_id WHERE t.id = $1`, taskID).
		Scan(&taskState, &executionState); err != nil {
		t.Fatal(err)
	}
	if taskState != "succeeded" || executionState != "succeeded" {
		t.Fatalf("stale result changed state: task=%s execution=%s", taskState, executionState)
	}
}

func TestWorkerManagerDrainStopsAssignments(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("wm_drain_" + suffix)
	pool := "wm-drain-" + suffix

	draining := startTestWorkerWithCapacity(t, suffix+"a", pool, 1)
	available := startTestWorkerWithCapacity(t, suffix+"b", pool, 1)
	available.startConsumer(t, &delivery.Handler{
		Client: available.control, WorkerID: available.id, Execute: delivery.Echo, Log: zap.NewNop(),
	}, nil)
	draining.control.BeginDrain()
	admin := adminPool(t)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := admin.QueryRow(context.Background(), `
			SELECT status FROM execution.workers WHERE id = $1`, draining.id).Scan(&status); err == nil && status == "draining" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{Pool: pool, NodeCount: 1})
	waitExecutionState(t, client, fixture, "succeeded")
	var assignedWorker string
	if err := admin.QueryRow(context.Background(), `
		SELECT worker_id FROM execution.task_attempts WHERE execution_id = $1`,
		fixture.executionID).Scan(&assignedWorker); err != nil {
		t.Fatal(err)
	}
	if assignedWorker != available.id {
		t.Fatalf("task assigned to %s; draining worker %s must not receive work", assignedWorker, draining.id)
	}
}

func TestWorkerManagerBootChangeReclaimsAttempts(t *testing.T) {
	client := newAPIClient(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	client.token = client.devToken("wm_boot_" + suffix)
	pool := "wm-boot-" + suffix

	original := startTestWorkerWithCapacity(t, suffix+"a", pool, 1)
	fixture := createSchedulerFixture(t, client, suffix, schedulerOptions{
		Pool: pool, NodeCount: 1, MaxAttempts: 2,
	})
	admin := adminPool(t)

	var oldAttemptID string
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		err := admin.QueryRow(context.Background(), `
			SELECT a.id FROM execution.execution_tasks t
			JOIN execution.task_attempts a ON a.task_id = t.id
			WHERE t.execution_id = $1 AND a.finished_at IS NULL`, fixture.executionID).Scan(&oldAttemptID)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if oldAttemptID == "" {
		t.Fatal("original worker never received an attempt")
	}
	t.Logf("fixture task=%s old attempt=%s execution=%s", fixture.taskIDs[0], oldAttemptID, fixture.executionID)

	// If the boot changes before the dispatch outbox row is published, the attempt is correctly reclaimed as
	// incomplete and its number may be reused. Wait for publication so this test exercises the lease_expired path.
	publishedDeadline := time.Now().Add(10 * time.Second)
	for {
		var published int
		if err := admin.QueryRow(context.Background(), `
			SELECT count(*) FROM execution.outbox
			WHERE task_id = $1 AND attempt_id = $2 AND message_type = 'task.dispatch'
			  AND published_at IS NOT NULL`, fixture.taskIDs[0], oldAttemptID).Scan(&published); err != nil {
			t.Fatal(err)
		}
		if published > 0 {
			break
		}
		if time.Now().After(publishedDeadline) {
			t.Fatal("task dispatch was not published before the boot change")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Register a replacement process with the same worker ID and a new boot ID.
	_ = startTestWorkerNamed(t, original.id, pool, 1)

	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var outcome string
		var attemptNumber int
		err := admin.QueryRow(context.Background(), `
			SELECT outcome, attempt_number FROM execution.task_attempts WHERE id = $1`,
			oldAttemptID).Scan(&outcome, &attemptNumber)
		if err == nil && outcome == "lease_expired" && attemptNumber == 1 {
			var nextAttempts int
			if err := admin.QueryRow(context.Background(), `
				SELECT count(*) FROM execution.task_attempts
				WHERE execution_id = $1 AND attempt_number = 2`, fixture.executionID).Scan(&nextAttempts); err != nil {
				t.Fatal(err)
			}
			if nextAttempts == 1 {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	rows, err := admin.Query(context.Background(), `
		SELECT a.attempt_number, COALESCE(a.outcome, 'open'), t.state, t.attempt_count,
		       t.max_attempts, COALESCE(t.owner_worker_id, ''), t.next_attempt_at
		FROM execution.execution_tasks t
		LEFT JOIN execution.task_attempts a ON a.task_id = t.id
		WHERE t.execution_id = $1 ORDER BY a.attempt_number`, fixture.executionID)
	if err == nil {
		for rows.Next() {
			var number int
			var outcome, state, owner string
			var attemptCount, maxAttempts int
			var nextAttempt *time.Time
			if err := rows.Scan(&number, &outcome, &state, &attemptCount, &maxAttempts, &owner, &nextAttempt); err == nil {
				t.Logf("attempt=%d outcome=%s task=%s count=%d max=%d owner=%s next=%v",
					number, outcome, state, attemptCount, maxAttempts, owner, nextAttempt)
			}
		}
		rows.Close()
	}
	t.Fatal("new boot did not reclaim and redispatch the previous attempt")
}

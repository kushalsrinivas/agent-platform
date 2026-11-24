// Package control holds one worker <-> orchestrator session: registration, heartbeats, fencing and drain.
package control

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/platform/resources"
)

// WorkerIDMetadataKey is the local-development worker identity header. Production mTLS binds it to the certificate.
const WorkerIDMetadataKey = "x-worker-id"

// Config describes one worker process.
type Config struct {
	Address         string
	Insecure        bool
	WorkerID        string
	Pool            string
	Runtime         string
	RuntimeVersion  string
	Capabilities    []string
	CPUCores        float64
	MemoryMB        int64
	MaxConcurrency  int
	BinaryVersion   string
	RuntimeABIs     []int32
	RuntimeDigests  []string
	HeartbeatPeriod time.Duration
	ReconnectDelay  time.Duration
	// ControlToken authenticates worker-control RPCs. Empty keeps local development token-free.
	ControlToken string
}

type activeAttempt struct {
	taskID    string
	attemptID string
	leaseID   string
}

// Client owns the control stream and exposes only the operations the delivery loop needs.
type Client struct {
	cfg     Config
	log     *zap.Logger
	sampler *resources.Sampler

	mu        sync.Mutex
	conn      *grpc.ClientConn
	client    workerv1.WorkerControlClient
	stream    workerv1.WorkerControl_ConnectClient
	sessionID string
	connected bool
	active    map[string]activeAttempt

	sendMu sync.Mutex

	draining  atomic.Bool
	drainCh   chan struct{}
	drainOnce sync.Once
	stopCh    chan struct{}
	stopOnce  sync.Once

	cancelMu   sync.Mutex
	cancelWait map[string]chan struct{}
	cancelMark map[string]struct{}
}

// New builds a worker control client.
func New(cfg Config, log *zap.Logger) *Client {
	if cfg.HeartbeatPeriod <= 0 {
		cfg.HeartbeatPeriod = 5 * time.Second
	}
	if cfg.ReconnectDelay <= 0 {
		cfg.ReconnectDelay = 2 * time.Second
	}
	if cfg.MaxConcurrency < 1 {
		cfg.MaxConcurrency = 1
	}
	if cfg.Runtime == "" {
		cfg.Runtime = "python"
	}
	if cfg.RuntimeVersion == "" {
		cfg.RuntimeVersion = "unknown"
	}
	if len(cfg.Capabilities) == 0 {
		cfg.Capabilities = []string{"agent_execution", "tool_execution"}
	}
	if cfg.CPUCores <= 0 {
		cfg.CPUCores = 1
	}
	if cfg.MemoryMB <= 0 {
		cfg.MemoryMB = 1024
	}
	return &Client{
		cfg: cfg, log: log, sampler: resources.New(), active: map[string]activeAttempt{},
		drainCh: make(chan struct{}), stopCh: make(chan struct{}),
		cancelWait: map[string]chan struct{}{}, cancelMark: map[string]struct{}{},
	}
}

// Run keeps a session open until the context is cancelled or Shutdown is called. A broken stream reconnects;
// dispatch delivery then fails transiently and the broker redelivers rather than executing without a control plane.
func (c *Client) Run(ctx context.Context) error {
	c.log.Info("worker control loop started", zap.String("orchestrator", c.cfg.Address))
	for ctx.Err() == nil && !c.stopped() {
		if err := c.session(ctx); err != nil && ctx.Err() == nil && !c.stopped() {
			c.log.Warn("worker session ended", zap.Error(err), zap.Duration("retry_in", c.cfg.ReconnectDelay))
		}
		select {
		case <-ctx.Done():
		case <-c.stopCh:
		case <-time.After(c.cfg.ReconnectDelay):
		}
	}
	c.close()
	c.log.Info("worker control loop stopped")
	return nil
}

func (c *Client) stopped() bool {
	select {
	case <-c.stopCh:
		return true
	default:
		return false
	}
}

func (c *Client) session(ctx context.Context) error {
	c.mu.Lock()
	conn := c.conn
	client := c.client
	c.mu.Unlock()
	if conn == nil {
		newConn, err := c.dial()
		if err != nil {
			return err
		}
		client = workerv1.NewWorkerControlClient(newConn)
		c.mu.Lock()
		c.conn, c.client = newConn, client
		c.mu.Unlock()
	}

	stream, err := client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()
	helloAvailable := c.cfg.MaxConcurrency
	if c.draining.Load() {
		// A reconnecting draining worker must not be offered work for the brief window before its first heartbeat.
		helloAvailable = 0
	}
	if err := stream.Send(&workerv1.WorkerMessage{Body: &workerv1.WorkerMessage_Hello{Hello: &workerv1.Hello{
		WorkerId:        c.cfg.WorkerID,
		BootId:          newBootID(),
		Pool:            c.cfg.Pool,
		Capacity:        &workerv1.Capacity{MaxConcurrency: clampInt32(c.cfg.MaxConcurrency), AvailableSlots: clampInt32(helloAvailable)},
		RuntimeAbis:     c.cfg.RuntimeABIs,
		RuntimeDigests:  c.cfg.RuntimeDigests,
		BinaryVersion:   c.cfg.BinaryVersion,
		ProtocolVersion: 1,
		Runtime:         c.cfg.Runtime,
		RuntimeVersion:  c.cfg.RuntimeVersion,
		Capabilities:    c.cfg.Capabilities,
		Resources: &workerv1.WorkerResources{
			CpuCores: c.cfg.CPUCores,
			MemoryMb: c.cfg.MemoryMB,
		},
	}}}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive hello ack: %w", err)
	}
	ack := first.GetHelloAck()
	if ack == nil {
		return errors.New("orchestrator did not acknowledge hello")
	}

	c.mu.Lock()
	c.stream = stream
	c.sessionID = ack.GetSessionId()
	c.connected = true
	c.mu.Unlock()
	c.log.Info("worker registered",
		zap.String("worker_id", c.cfg.WorkerID),
		zap.String("session_id", ack.GetSessionId()),
		zap.String("runtime", c.cfg.Runtime),
		zap.String("runtime_version", c.cfg.RuntimeVersion),
		zap.Strings("capabilities", c.cfg.Capabilities),
		zap.Duration("lease_ttl", time.Duration(ack.GetLeaseTtlMs())*time.Millisecond),
	)

	defer func() {
		c.mu.Lock()
		c.connected = false
		c.stream = nil
		c.sessionID = ""
		c.mu.Unlock()
	}()

	readDone := make(chan error, 1)
	go func() { readDone <- c.read(stream) }()

	ticker := time.NewTicker(c.cfg.HeartbeatPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			c.goodbye(stream, "shutdown")
			return nil
		case <-c.stopCh:
			c.goodbye(stream, "shutdown")
			return nil
		case err := <-readDone:
			return err
		case <-ticker.C:
			if err := c.sendHeartbeat(stream); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		}
	}
}

func (c *Client) read(stream workerv1.WorkerControl_ConnectClient) error {
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		switch body := message.Body.(type) {
		case *workerv1.OrchestratorMessage_CancelTask:
			c.log.Warn("task cancellation requested",
				zap.String("task_id", body.CancelTask.GetTaskId()),
				zap.String("attempt_id", body.CancelTask.GetAttemptId()),
				zap.String("reason", body.CancelTask.GetReason()),
				zap.Int32("cancel_epoch", body.CancelTask.GetCancelEpoch()),
			)
			c.signalCancel(body.CancelTask.GetTaskId(), body.CancelTask.GetAttemptId(),
				body.CancelTask.GetLeaseId())
		case *workerv1.OrchestratorMessage_Drain:
			c.log.Warn("worker drain requested",
				zap.String("reason", body.Drain.GetReason()),
				zap.Int32("grace_seconds", body.Drain.GetGracePeriodSeconds()),
			)
			c.BeginDrain()
		case *workerv1.OrchestratorMessage_Error:
			c.log.Warn("orchestrator control error",
				zap.String("code", body.Error.GetCode()),
				zap.String("message", body.Error.GetMessage()),
				zap.Bool("retryable", body.Error.GetRetryable()),
			)
		default:
			c.log.Debug("unknown orchestrator message ignored")
		}
	}
}

// BeginDrain stops accepting new work and reports the draining status immediately. In-flight attempts keep their
// leases because heartbeats continue until Shutdown.
func (c *Client) BeginDrain() {
	c.drainOnce.Do(func() {
		c.draining.Store(true)
		close(c.drainCh)
	})
	c.mu.Lock()
	stream := c.stream
	c.mu.Unlock()
	if stream != nil {
		if err := c.sendHeartbeat(stream); err != nil {
			c.log.Warn("could not report draining status", zap.Error(err))
		}
	}
}

// Draining reports whether new dispatches must be rejected.
func (c *Client) Draining() bool { return c.draining.Load() }

// DrainCh closes when a drain is requested by the control plane.
func (c *Client) DrainCh() <-chan struct{} { return c.drainCh }

// Shutdown ends the control session after in-flight results have been reported.
func (c *Client) Shutdown() { c.stopOnce.Do(func() { close(c.stopCh) }) }

// Started records the attempt as active and sends task_accepted. It must succeed before execution begins.
func (c *Client) Started(_ context.Context, taskID, attemptID, leaseID string) error {
	c.mu.Lock()
	stream := c.stream
	if stream == nil || !c.connected {
		c.mu.Unlock()
		return errors.New("control stream is not connected")
	}
	c.active[taskID] = activeAttempt{taskID: taskID, attemptID: attemptID, leaseID: leaseID}
	c.mu.Unlock()

	err := c.send(stream, &workerv1.WorkerMessage{Body: &workerv1.WorkerMessage_TaskAccepted{
		TaskAccepted: &workerv1.TaskAccepted{
			TaskId: taskID, AttemptId: attemptID, LeaseId: leaseID, StartedAt: timestamppb.Now(),
		},
	}})
	if err != nil {
		c.Done(taskID)
		return fmt.Errorf("send task accepted: %w", err)
	}
	return nil
}

// Rejected declines a dispatch without executing it. The orchestrator returns the task to pending.
func (c *Client) Rejected(_ context.Context, taskID, attemptID, leaseID string,
	reason workerv1.TaskRejectionReason, message string) error {
	c.mu.Lock()
	stream := c.stream
	c.mu.Unlock()
	if stream == nil {
		return errors.New("control stream is not connected")
	}
	return c.send(stream, &workerv1.WorkerMessage{Body: &workerv1.WorkerMessage_TaskRejected{
		TaskRejected: &workerv1.TaskRejected{
			TaskId: taskID, AttemptId: attemptID, LeaseId: leaseID, Reason: reason, Message: message,
		},
	}})
}

// Done removes an attempt from the heartbeat set and forgets a cancellation for it.
func (c *Client) Done(taskID string) {
	c.mu.Lock()
	active, ok := c.active[taskID]
	delete(c.active, taskID)
	c.mu.Unlock()
	if ok {
		c.ClearCancellation(taskID, active.attemptID, active.leaseID)
	}
}

func cancelKey(taskID, attemptID, leaseID string) string {
	return taskID + "|" + attemptID + "|" + leaseID
}

func (c *Client) signalCancel(taskID, attemptID, leaseID string) {
	key := cancelKey(taskID, attemptID, leaseID)
	c.cancelMu.Lock()
	if _, exists := c.cancelMark[key]; exists {
		c.cancelMu.Unlock()
		return
	}
	c.cancelMark[key] = struct{}{}
	waiter := c.cancelWait[key]
	delete(c.cancelWait, key)
	c.cancelMu.Unlock()
	if waiter != nil {
		close(waiter)
	}
}

// Cancellation returns a channel closed when the orchestrator cancels this exact attempt. Registering before the
// execution starts also catches a cancellation that arrived with the dispatch.
func (c *Client) Cancellation(taskID, attemptID, leaseID string) (<-chan struct{}, func()) {
	key := cancelKey(taskID, attemptID, leaseID)
	ch := make(chan struct{})
	c.cancelMu.Lock()
	if _, cancelled := c.cancelMark[key]; cancelled {
		close(ch)
	} else {
		c.cancelWait[key] = ch
	}
	c.cancelMu.Unlock()
	return ch, func() { c.ClearCancellation(taskID, attemptID, leaseID) }
}

// ClearCancellation forgets the attempt's marker and waiter.
func (c *Client) ClearCancellation(taskID, attemptID, leaseID string) {
	key := cancelKey(taskID, attemptID, leaseID)
	c.cancelMu.Lock()
	delete(c.cancelMark, key)
	delete(c.cancelWait, key)
	c.cancelMu.Unlock()
}

// ActiveCount is the number of attempts this process still owns.
func (c *Client) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active)
}

// ReportTaskResult adds the worker identity and performs the fenced result RPC.
func (c *Client) ReportTaskResult(ctx context.Context,
	req *workerv1.ReportTaskResultRequest) (*workerv1.ReportTaskResultResponse, error) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return nil, errors.New("orchestrator client is not connected")
	}
	ctx = metadata.AppendToOutgoingContext(ctx, WorkerIDMetadataKey, c.cfg.WorkerID)
	return client.ReportTaskResult(ctx, req)
}

// Connected reports whether a session is registered.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Check is the readiness probe: a draining worker is alive but no longer ready for new work.
func (c *Client) Check(_ context.Context) error {
	if c.Draining() {
		return errors.New("worker is draining")
	}
	if !c.Connected() {
		return errors.New("worker control stream is not connected")
	}
	return nil
}

func (c *Client) sendHeartbeat(stream workerv1.WorkerControl_ConnectClient) error {
	usage := c.sampler.Sample()
	c.mu.Lock()
	renewals := make([]*workerv1.ActiveAttempt, 0, len(c.active))
	for _, active := range c.active {
		renewals = append(renewals, &workerv1.ActiveAttempt{
			TaskId: active.taskID, AttemptId: active.attemptID, LeaseId: active.leaseID,
		})
	}
	available := c.cfg.MaxConcurrency - len(c.active)
	c.mu.Unlock()
	if available < 0 {
		available = 0
	}
	status := workerv1.WorkerStatus_WORKER_STATUS_AVAILABLE
	if c.draining.Load() {
		status = workerv1.WorkerStatus_WORKER_STATUS_DRAINING
	} else if available == 0 {
		status = workerv1.WorkerStatus_WORKER_STATUS_BUSY
	}
	return c.send(stream, &workerv1.WorkerMessage{Body: &workerv1.WorkerMessage_Heartbeat{
		Heartbeat: &workerv1.Heartbeat{
			SentAt:          timestamppb.Now(),
			ActiveAttempts:  renewals,
			Capacity:        &workerv1.Capacity{MaxConcurrency: clampInt32(c.cfg.MaxConcurrency), AvailableSlots: clampInt32(available)},
			CpuUsagePercent: usage.CPUPercent,
			MemoryUsedMb:    usage.MemoryMB,
			Status:          status,
			RuntimeVersion:  c.cfg.RuntimeVersion,
		},
	}})
}

func (c *Client) send(stream workerv1.WorkerControl_ConnectClient, message *workerv1.WorkerMessage) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return stream.Send(message)
}

func (c *Client) goodbye(stream workerv1.WorkerControl_ConnectClient, reason string) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_ = stream.Send(&workerv1.WorkerMessage{Body: &workerv1.WorkerMessage_Goodbye{
		Goodbye: &workerv1.Goodbye{Reason: reason},
	}})
}

func (c *Client) dial() (*grpc.ClientConn, error) {
	var creds credentials.TransportCredentials
	if c.cfg.Insecure {
		creds = insecure.NewCredentials()
	} else {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	conn, err := grpc.NewClient(c.cfg.Address,
		grpc.WithTransportCredentials(creds),
		grpc.WithUnaryInterceptor(c.authUnary),
		grpc.WithStreamInterceptor(c.authStream),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(36<<20), grpc.MaxCallSendMsgSize(36<<20)))
	if err != nil {
		return nil, fmt.Errorf("dial orchestrator %s: %w", c.cfg.Address, err)
	}
	return conn, nil
}

func (c *Client) authUnary(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn,
	invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if c.cfg.ControlToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-worker-token", c.cfg.ControlToken)
	}
	return invoker(ctx, method, req, reply, conn, opts...)
}

func (c *Client) authStream(ctx context.Context, desc *grpc.StreamDesc, conn *grpc.ClientConn,
	method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	if c.cfg.ControlToken != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-worker-token", c.cfg.ControlToken)
	}
	return streamer(ctx, desc, conn, method, opts...)
}

func (c *Client) close() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.client = nil
	c.connected = false
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func newBootID() string {
	return uuid.NewString()
}

func clampInt32(value int) int32 {
	if value < 0 {
		return 0
	}
	if value > 1<<31-1 {
		return 1<<31 - 1
	}
	return int32(value)
}

// GetTaskContext fetches the pinned, immutable context for a live attempt.
func (c *Client) GetTaskContext(ctx context.Context, taskID, attemptID, leaseID string) (*workerv1.GetTaskContextResponse, error) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return nil, errors.New("orchestrator client is not connected")
	}
	ctx = metadata.AppendToOutgoingContext(ctx, WorkerIDMetadataKey, c.cfg.WorkerID)
	return client.GetTaskContext(ctx, &workerv1.GetTaskContextRequest{
		TaskId: taskID, AttemptId: attemptID, LeaseId: leaseID,
	})
}

// GetTaskCredentials fetches short-lived, task-scoped credentials. The store is not wired yet, so callers must
// treat an empty result as a configuration failure rather than an empty provider key.
func (c *Client) GetTaskCredentials(ctx context.Context, taskID, attemptID, leaseID string,
	names []string) (*workerv1.GetTaskCredentialsResponse, error) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return nil, errors.New("orchestrator client is not connected")
	}
	ctx = metadata.AppendToOutgoingContext(ctx, WorkerIDMetadataKey, c.cfg.WorkerID)
	return client.GetTaskCredentials(ctx, &workerv1.GetTaskCredentialsRequest{
		TaskId: taskID, AttemptId: attemptID, LeaseId: leaseID, Names: names,
	})
}

// PutCheckpoint uploads one checkpoint produced by the runtime.
func (c *Client) PutCheckpoint(ctx context.Context,
	req *workerv1.PutCheckpointRequest) (*workerv1.PutCheckpointResponse, error) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return nil, errors.New("orchestrator client is not connected")
	}
	ctx = metadata.AppendToOutgoingContext(ctx, WorkerIDMetadataKey, c.cfg.WorkerID)
	return client.PutCheckpoint(ctx, req)
}

// Progress reports advisory attempt telemetry. It never transitions state and failures are ignored by the
// orchestrator.
func (c *Client) Progress(_ context.Context, taskID, attemptID, leaseID, message string, step int64) error {
	c.mu.Lock()
	stream := c.stream
	c.mu.Unlock()
	if stream == nil {
		return errors.New("control stream is not connected")
	}
	return c.send(stream, &workerv1.WorkerMessage{Body: &workerv1.WorkerMessage_TaskProgress{
		TaskProgress: &workerv1.TaskProgress{
			TaskId: taskID, AttemptId: attemptID, LeaseId: leaseID,
			Step: step, Message: message, ReportedAt: timestamppb.Now(),
		},
	}})
}

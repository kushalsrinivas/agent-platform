// Package control holds one worker <-> orchestrator session: registration, heartbeats and fenced result calls.
package control

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
)

// WorkerIDMetadataKey is the local-development worker identity header. Production mTLS binds it to the certificate.
const WorkerIDMetadataKey = "x-worker-id"

// Config describes one worker process.
type Config struct {
	Address         string
	Insecure        bool
	WorkerID        string
	Pool            string
	MaxConcurrency  int
	BinaryVersion   string
	RuntimeABIs     []int32
	RuntimeDigests  []string
	HeartbeatPeriod time.Duration
	ReconnectDelay  time.Duration
}

type activeAttempt struct {
	taskID    string
	attemptID string
	leaseID   string
}

// Client owns the control stream and exposes only the operations the delivery loop needs.
type Client struct {
	cfg Config
	log *zap.Logger

	mu        sync.Mutex
	conn      *grpc.ClientConn
	client    workerv1.WorkerControlClient
	stream    workerv1.WorkerControl_ConnectClient
	sessionID string
	connected bool
	active    map[string]activeAttempt

	sendMu sync.Mutex
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
	return &Client{cfg: cfg, log: log, active: map[string]activeAttempt{}}
}

// Run keeps a session open until the context is cancelled. A broken stream reconnects; dispatch delivery then
// fails transiently and the broker redelivers rather than the worker executing without a live control plane.
func (c *Client) Run(ctx context.Context) error {
	c.log.Info("worker control loop started", zap.String("orchestrator", c.cfg.Address))
	for ctx.Err() == nil {
		if err := c.session(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn("worker session ended", zap.Error(err), zap.Duration("retry_in", c.cfg.ReconnectDelay))
		}
		select {
		case <-ctx.Done():
		case <-time.After(c.cfg.ReconnectDelay):
		}
	}
	c.close()
	c.log.Info("worker control loop stopped")
	return nil
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
	if err := stream.Send(&workerv1.WorkerMessage{Body: &workerv1.WorkerMessage_Hello{Hello: &workerv1.Hello{
		WorkerId:        c.cfg.WorkerID,
		BootId:          newBootID(),
		Pool:            c.cfg.Pool,
		Capacity:        &workerv1.Capacity{MaxConcurrency: clampInt32(c.cfg.MaxConcurrency), AvailableSlots: clampInt32(c.cfg.MaxConcurrency)},
		RuntimeAbis:     c.cfg.RuntimeABIs,
		RuntimeDigests:  c.cfg.RuntimeDigests,
		BinaryVersion:   c.cfg.BinaryVersion,
		ProtocolVersion: 1,
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
			)
		case *workerv1.OrchestratorMessage_Drain:
			c.log.Warn("worker drain requested",
				zap.String("reason", body.Drain.GetReason()),
				zap.Int32("grace_seconds", body.Drain.GetGracePeriodSeconds()),
			)
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

// Done removes an attempt from the heartbeat set.
func (c *Client) Done(taskID string) {
	c.mu.Lock()
	delete(c.active, taskID)
	c.mu.Unlock()
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

// Connected reports whether a session is registered and ready to accept work.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Check is the readiness probe for the worker session.
func (c *Client) Check(_ context.Context) error {
	if !c.Connected() {
		return errors.New("worker control stream is not connected")
	}
	return nil
}

func (c *Client) sendHeartbeat(stream workerv1.WorkerControl_ConnectClient) error {
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
	return c.send(stream, &workerv1.WorkerMessage{Body: &workerv1.WorkerMessage_Heartbeat{
		Heartbeat: &workerv1.Heartbeat{
			SentAt:         timestamppb.Now(),
			ActiveAttempts: renewals,
			Capacity: &workerv1.Capacity{
				MaxConcurrency: clampInt32(c.cfg.MaxConcurrency), AvailableSlots: clampInt32(available),
			},
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
	conn, err := grpc.NewClient(c.cfg.Address, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial orchestrator %s: %w", c.cfg.Address, err)
	}
	return conn, nil
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

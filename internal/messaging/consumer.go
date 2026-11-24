package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Handler processes one delivered message. Returning nil authorizes the broker acknowledgement: the handler must
// have durably committed whatever it promised before returning nil.
type Handler interface {
	Handle(ctx context.Context, msg jetstream.Msg) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, msg jetstream.Msg) error

// Handle calls the function.
func (f HandlerFunc) Handle(ctx context.Context, msg jetstream.Msg) error { return f(ctx, msg) }

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks a failure that must go straight to the DLQ; retrying cannot help.
func Permanent(err error) error {
	return &permanentError{err: err}
}

// IsPermanent reports whether the failure must bypass retries and go straight to the DLQ.
func IsPermanent(err error) bool {
	var target *permanentError
	return errors.As(err, &target)
}

// ConsumerConfig describes one worker's durable pull consumer.
type ConsumerConfig struct {
	WorkerID      string
	Durable       string
	Stream        string
	FilterSubject string
	MaxDeliver    int
	AckWait       time.Duration
	MaxAckPending int
	BackOff       []time.Duration
	DLQSubject    string
}

// DefaultConsumerConfig builds the per-worker consumer. Capacity bounds in-flight messages.
func DefaultConsumerConfig(workerID string, capacity int) ConsumerConfig {
	if capacity < 1 {
		capacity = 1
	}
	return ConsumerConfig{
		WorkerID:      workerID,
		Durable:       "worker-" + safeName(workerID),
		Stream:        StreamTaskDispatch,
		FilterSubject: "tasks.dispatch." + workerID,
		MaxDeliver:    5,
		AckWait:       60 * time.Second,
		MaxAckPending: capacity,
		// NATS derives the effective ack wait from the first backoff, so the broker retry curve starts at the
		// ack window. Explicit NakWithDelay uses the same curve.
		BackOff:    []time.Duration{60 * time.Second, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute},
		DLQSubject: DLQSubject(workerID),
	}
}

type consumerStats struct {
	received     uint64
	processed    uint64
	failed       uint64
	deadLettered uint64
	lastError    string
	lastErrorAt  time.Time
}

// Stats is the operator-facing consumer snapshot.
type Stats struct {
	Received     uint64     `json:"received"`
	Processed    uint64     `json:"processed"`
	Failed       uint64     `json:"failed"`
	DeadLettered uint64     `json:"dead_lettered"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
}

// Consumer is a durable delivery loop. A broker ack happens only after Handle returns nil.
type Consumer struct {
	cfg      ConsumerConfig
	handler  Handler
	js       jetstream.JetStream
	consumer jetstream.Consumer
	log      *zap.Logger

	mu         sync.Mutex
	stats      consumerStats
	ctx        context.Context
	cancel     context.CancelFunc
	consumeCtx jetstream.ConsumeContext
	stopOnce   sync.Once
	inFlight   sync.WaitGroup
}

// NewConsumer creates or updates the durable pull consumer and validates its delivery policy.
func NewConsumer(ctx context.Context, js jetstream.JetStream, cfg ConsumerConfig, handler Handler,
	log *zap.Logger) (*Consumer, error) {
	if handler == nil {
		return nil, errors.New("messaging: consumer handler is required")
	}
	if strings.TrimSpace(cfg.WorkerID) == "" || strings.ContainsAny(cfg.WorkerID, ".*> \t\r\n/\\") {
		return nil, fmt.Errorf("messaging: worker_id %q is not a valid NATS subject token", cfg.WorkerID)
	}
	if cfg.Stream == "" {
		cfg.Stream = StreamTaskDispatch
	}
	if cfg.FilterSubject == "" {
		cfg.FilterSubject = "tasks.dispatch." + cfg.WorkerID
	}
	if cfg.Durable == "" {
		cfg.Durable = "worker-" + safeName(cfg.WorkerID)
	}
	if cfg.MaxDeliver <= 0 {
		cfg.MaxDeliver = 5
	}
	if cfg.AckWait <= 0 {
		cfg.AckWait = 60 * time.Second
	}
	if cfg.MaxAckPending <= 0 {
		cfg.MaxAckPending = 1
	}
	if len(cfg.BackOff) == 0 {
		cfg.BackOff = []time.Duration{60 * time.Second, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute}
	}
	if cfg.DLQSubject == "" {
		cfg.DLQSubject = DLQSubject(cfg.WorkerID)
	}

	stream, err := js.Stream(ctx, cfg.Stream)
	if err != nil {
		return nil, fmt.Errorf("messaging: load stream %s: %w", cfg.Stream, err)
	}
	// Recreate the worker's own durable consumer so a stale broker-side configuration (for example an old
	// ack_wait from a previous deployment) cannot weaken delivery semantics. TASK_DISPATCH is a work-queue
	// stream: acknowledged messages are gone, unacknowledged messages remain and are redelivered.
	if err := stream.DeleteConsumer(ctx, cfg.Durable); err != nil && !errors.Is(err, jetstream.ErrConsumerNotFound) {
		return nil, fmt.Errorf("messaging: reset consumer %s: %w", cfg.Durable, err)
	}
	natsConsumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       cfg.Durable,
		Description:   "Durable task delivery for " + cfg.WorkerID,
		FilterSubject: cfg.FilterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       cfg.AckWait,
		MaxDeliver:    cfg.MaxDeliver,
		MaxAckPending: cfg.MaxAckPending,
		BackOff:       cfg.BackOff,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("messaging: create consumer %s: %w", cfg.Durable, err)
	}

	return &Consumer{cfg: cfg, handler: handler, js: js, consumer: natsConsumer, log: log}, nil
}

// Start begins the pull loop. It returns immediately; Stop drains and terminates it.
func (c *Consumer) Start(parent context.Context) error {
	c.mu.Lock()
	if c.consumeCtx != nil {
		c.mu.Unlock()
		return errors.New("messaging: consumer already started")
	}
	c.ctx, c.cancel = context.WithCancel(parent)
	ctx := c.ctx
	c.mu.Unlock()

	// NATS invokes this callback serially. Dispatch each message to its own goroutine so the worker capacity
	// (which is also MaxAckPending) is real: a supervisor waiting on delegated children must not block the
	// child dispatch that would unblock it. ACKs still happen only after Handle returns.
	consumeCtx, err := c.consumer.Consume(func(msg jetstream.Msg) {
		c.inFlight.Add(1)
		go func() {
			defer c.inFlight.Done()
			c.handle(ctx, msg)
		}()
	}, jetstream.PullMaxMessages(c.cfg.MaxAckPending))
	if err != nil {
		return fmt.Errorf("messaging: consume %s: %w", c.cfg.Durable, err)
	}
	c.mu.Lock()
	c.consumeCtx = consumeCtx
	c.mu.Unlock()

	c.log.Info("durable consumer started",
		zap.String("stream", c.cfg.Stream),
		zap.String("durable", c.cfg.Durable),
		zap.String("filter", c.cfg.FilterSubject),
		zap.Int("max_deliver", c.cfg.MaxDeliver),
		zap.Int("max_ack_pending", c.cfg.MaxAckPending),
		zap.Duration("ack_wait", c.cfg.AckWait),
	)
	return nil
}

// Stop drains buffered messages and waits for the loop to close.
func (c *Consumer) Stop() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		cancel := c.cancel
		consumeCtx := c.consumeCtx
		c.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if consumeCtx != nil {
			consumeCtx.Drain()
			select {
			case <-consumeCtx.Closed():
			case <-time.After(10 * time.Second):
				consumeCtx.Stop()
			}
		}
		done := make(chan struct{})
		go func() {
			c.inFlight.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			c.log.Warn("consumer stop timed out waiting for in-flight handlers",
				zap.String("durable", c.cfg.Durable))
		}
		c.log.Info("durable consumer stopped", zap.String("durable", c.cfg.Durable))
	})
}

func (c *Consumer) handle(ctx context.Context, msg jetstream.Msg) {
	if encoded := msg.Headers().Get("traceparent"); encoded != "" {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": encoded})
	}
	ctx, span := otel.Tracer("messaging").Start(ctx, "nats.consume",
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination", msg.Subject()),
			attribute.String("messaging.consumer", c.cfg.Durable),
		))
	defer span.End()
	c.mu.Lock()
	c.stats.received++
	c.mu.Unlock()

	meta, err := msg.Metadata()
	if err != nil {
		c.recordFailure(fmt.Errorf("message metadata: %w", err))
		_ = msg.NakWithDelay(time.Second)
		return
	}

	if err := c.handler.Handle(ctx, msg); err != nil {
		c.recordFailure(err)
		c.log.Warn("task delivery failed",
			zap.String("durable", c.cfg.Durable),
			zap.String("subject", msg.Subject()),
			zap.Uint64("deliveries", meta.NumDelivered),
			zap.Bool("permanent", IsPermanent(err)),
			zap.Error(err),
		)
		if IsPermanent(err) || meta.NumDelivered >= uint64(c.cfg.MaxDeliver) { //nolint:gosec // MaxDeliver is validated positive.
			if dlqErr := PublishDLQ(ctx, c.js, c.cfg.DLQSubject, c.cfg.WorkerID, msg, meta.NumDelivered, err); dlqErr != nil {
				c.log.Error("could not publish to DLQ; message will be retried",
					zap.String("durable", c.cfg.Durable), zap.Error(dlqErr))
				_ = msg.NakWithDelay(c.backoff(meta.NumDelivered))
				return
			}
			if ackErr := msg.Ack(); ackErr != nil {
				c.log.Error("could not acknowledge dead-lettered message", zap.Error(ackErr))
				return
			}
			c.mu.Lock()
			c.stats.deadLettered++
			c.mu.Unlock()
			return
		}
		_ = msg.NakWithDelay(c.backoff(meta.NumDelivered))
		return
	}

	if err := msg.Ack(); err != nil {
		c.log.Error("could not acknowledge delivered message", zap.Error(err))
		return
	}
	c.mu.Lock()
	c.stats.processed++
	c.mu.Unlock()
}

func (c *Consumer) recordFailure(err error) {
	now := time.Now().UTC()
	c.mu.Lock()
	c.stats.failed++
	c.stats.lastError = err.Error()
	c.stats.lastErrorAt = now
	c.mu.Unlock()
}

func (c *Consumer) backoff(deliveries uint64) time.Duration {
	index := int(deliveries) - 1 //nolint:gosec // delivery counts are bounded by MaxDeliver.
	if index < 0 {
		index = 0
	}
	if index < 0 {
		index = 0
	}
	if index >= len(c.cfg.BackOff) {
		index = len(c.cfg.BackOff) - 1
	}
	return c.cfg.BackOff[index]
}

// Stats returns the in-process delivery counters.
func (c *Consumer) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := Stats{
		Received:     c.stats.received,
		Processed:    c.stats.processed,
		Failed:       c.stats.failed,
		DeadLettered: c.stats.deadLettered,
		LastError:    c.stats.lastError,
	}
	if !c.stats.lastErrorAt.IsZero() {
		value := c.stats.lastErrorAt
		stats.LastErrorAt = &value
	}
	return stats
}

// Info returns broker-side counters for monitoring.
func (c *Consumer) Info(ctx context.Context) (*jetstream.ConsumerInfo, error) {
	return c.consumer.Info(ctx)
}

// Config returns the effective consumer configuration.
func (c *Consumer) Config() ConsumerConfig { return c.cfg }

func safeName(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	return builder.String()
}

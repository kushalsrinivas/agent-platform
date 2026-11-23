package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
)

// RedriveDLQ republishes up to limit dead-lettered messages for one worker to their original subjects and
// acknowledges them only after the republish succeeds. It is operator-controlled and idempotent at the consumer
// level: duplicates are still handled by the worker's normal fencing.
func RedriveDLQ(ctx context.Context, js jetstream.JetStream, workerID string, limit int, log *zap.Logger) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, errors.New("messaging: redrive limit must be between 1 and 100")
	}
	if workerID == "" {
		return 0, errors.New("messaging: worker_id is required")
	}
	stream, err := js.Stream(ctx, StreamTaskDLQ)
	if err != nil {
		return 0, fmt.Errorf("messaging: load dlq stream: %w", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "redrive-" + safeName(workerID),
		Description:   "Operator-controlled redrive of dead-lettered dispatch messages",
		FilterSubject: DLQSubject(workerID),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    5,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
	if err != nil {
		return 0, fmt.Errorf("messaging: create redrive consumer: %w", err)
	}
	batch, err := consumer.Fetch(limit, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		return 0, fmt.Errorf("messaging: fetch dlq: %w", err)
	}

	redriven := 0
	for msg := range batch.Messages() {
		var envelope DLQEnvelope
		if err := json.Unmarshal(msg.Data(), &envelope); err != nil {
			return redriven, fmt.Errorf("messaging: decode dlq message: %w", err)
		}
		if envelope.OriginalSubject == "" || len(envelope.Payload) == 0 {
			return redriven, errors.New("messaging: dlq message has no original subject or payload")
		}
		natsMessage := nats.NewMsg(envelope.OriginalSubject)
		natsMessage.Data = envelope.Payload
		natsMessage.Header.Set(jetstream.MsgIDHeader, idgen.New("redrive"))
		natsMessage.Header.Set("Content-Type", "application/json")
		for key, values := range envelope.Headers {
			for _, value := range values {
				natsMessage.Header.Add(key, value)
			}
		}
		if _, err := js.PublishMsg(ctx, natsMessage); err != nil {
			return redriven, fmt.Errorf("messaging: republish %s: %w", envelope.OriginalSubject, err)
		}
		if err := msg.Ack(); err != nil {
			return redriven, fmt.Errorf("messaging: acknowledge redriven dlq message: %w", err)
		}
		redriven++
		log.Info("dead-lettered message redriven",
			zap.String("worker_id", workerID),
			zap.String("subject", envelope.OriginalSubject),
			zap.String("execution_id", envelope.ExecutionID),
			zap.Uint64("deliveries", envelope.Deliveries),
		)
	}
	if err := batch.Error(); err != nil {
		return redriven, fmt.Errorf("messaging: dlq batch: %w", err)
	}
	return redriven, nil
}

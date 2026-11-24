package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/outbox"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

// BuildEnvelope wraps a durable outbox row in the versioned message envelope. The outbox payload is the
// type-specific payload; consumers only ever see a complete envelope.
func BuildEnvelope(msg outbox.Message) ([]byte, error) {
	if msg.ProjectID == "" {
		return nil, fmt.Errorf("messaging: outbox message %s has no project_id", msg.MessageID)
	}
	if len(msg.Payload) == 0 {
		return nil, fmt.Errorf("messaging: outbox message %s has no payload", msg.MessageID)
	}
	createdAt := msg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	correlationID := msg.CorrelationID
	if correlationID == nil {
		correlationID = msg.ExecutionID
	}
	orgID := msg.OrgID
	projectID := msg.ProjectID
	envelope := contracts.EventEnvelope{
		MessageID:     msg.MessageID,
		MessageType:   msg.MessageType,
		SchemaVersion: msg.SchemaVersion,
		TenantID:      &orgID,
		ProjectID:     &projectID,
		ExecutionID:   msg.ExecutionID,
		TaskID:        msg.TaskID,
		AttemptID:     msg.AttemptID,
		WorkerID:      msg.WorkerID,
		Timestamp:     createdAt.UTC().Format(time.RFC3339Nano),
		CorrelationID: correlationID,
		CausationID:   msg.CausationID,
		Traceparent:   msg.Traceparent,
		Payload:       msg.Payload,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("messaging: marshal envelope: %w", err)
	}
	// Validate known event types before they reach the broker. Unknown future types are allowed through.
	if _, known := contracts.SupportedEventTypes[msg.MessageType]; known {
		if _, err := contracts.ParseEvent(data); err != nil {
			return nil, fmt.Errorf("messaging: outbox message %s violates its event contract: %w", msg.MessageID, err)
		}
	}
	return data, nil
}

// NATSPublisher implements outbox.Publisher over JetStream. MessageID is the broker deduplication key.
type NATSPublisher struct {
	js  jetstream.JetStream
	log *zap.Logger
}

// NewNATSPublisher builds a publisher over JetStream.
func NewNATSPublisher(js jetstream.JetStream, log *zap.Logger) *NATSPublisher {
	return &NATSPublisher{js: js, log: log}
}

// Publish sends one outbox row as a versioned envelope and requires a stream acknowledgement.
func (p *NATSPublisher) Publish(ctx context.Context, message outbox.Message) error {
	if message.Traceparent != nil {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": *message.Traceparent})
	}
	ctx, span := otel.Tracer("messaging").Start(ctx, "nats.publish",
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination", message.Subject),
			attribute.String("messaging.message_type", message.MessageType),
		))
	defer span.End()
	data, err := BuildEnvelope(message)
	if err != nil {
		return err
	}

	natsMessage := nats.NewMsg(message.Subject)
	natsMessage.Data = data
	natsMessage.Header.Set(jetstream.MsgIDHeader, message.MessageID)
	natsMessage.Header.Set("Content-Type", "application/json")
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	for key, value := range carrier {
		natsMessage.Header.Set(key, value)
	}
	if len(message.Headers) > 0 {
		var headers map[string]string
		if err := json.Unmarshal(message.Headers, &headers); err == nil {
			for key, value := range headers {
				natsMessage.Header.Set(key, value)
			}
		}
	}

	ack, err := p.js.PublishMsg(ctx, natsMessage)
	if err != nil {
		return fmt.Errorf("messaging: publish %s: %w", message.Subject, err)
	}
	if ack == nil || ack.Stream == "" {
		return fmt.Errorf("messaging: publish %s returned no stream acknowledgement", message.Subject)
	}
	if ack.Duplicate {
		p.log.Debug("nats deduplicated outbox publish",
			zap.String("message_id", message.MessageID),
			zap.String("stream", ack.Stream),
		)
	}
	p.log.Debug("outbox message published",
		zap.String("message_id", message.MessageID),
		zap.String("message_type", message.MessageType),
		zap.String("subject", message.Subject),
		zap.String("stream", ack.Stream),
		zap.Uint64("sequence", ack.Sequence),
	)
	return nil
}

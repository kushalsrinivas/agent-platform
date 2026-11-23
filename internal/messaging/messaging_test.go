package messaging

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/agent-platform/agent-platform/internal/outbox"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

func TestBuildEnvelopeWrapsPayloadAndContext(t *testing.T) {
	executionID := "exec_01JQ9A0B1C2D3E4F5G6H7J8K"
	msg, err := outbox.NewMessage("org_01JQ9A0B1C2D3E4F5G6H7J8K", "proj_01JQ9A0B1C2D3E4F5G6H7J8K",
		"events.org.org_01JQ9A0B1C2D3E4F5G6H7J8K.execution."+executionID+".created",
		contracts.EventExecutionCreated, &executionID,
		contracts.ExecutionCreatedPayload{
			Seq: 1, WorkflowVersionID: "wfv_01JQ9A0B1C2D3E4F5G6H7J8K", State: "pending", StateVersion: 1,
			Counts: contracts.ExecutionCounts{Total: 1, Pending: 1},
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := BuildEnvelope(msg)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := contracts.ParseEvent(data)
	if err != nil {
		t.Fatalf("envelope does not parse: %v", err)
	}
	if parsed.Envelope.MessageID != msg.MessageID || parsed.Envelope.MessageType != contracts.EventExecutionCreated {
		t.Fatalf("unexpected envelope: %+v", parsed.Envelope)
	}
	if parsed.Envelope.TenantID == nil || *parsed.Envelope.TenantID != msg.OrgID {
		t.Fatalf("tenant was not preserved: %+v", parsed.Envelope)
	}
}

func TestBuildEnvelopeRejectsKnownTypeWithInvalidPayload(t *testing.T) {
	executionID := "exec_01JQ9A0B1C2D3E4F5G6H7J8K"
	msg, err := outbox.NewMessage("org_01JQ9A0B1C2D3E4F5G6H7J8K", "proj_01JQ9A0B1C2D3E4F5G6H7J8K",
		"events.org.org_01JQ9A0B1C2D3E4F5G6H7J8K.execution."+executionID+".created",
		contracts.EventExecutionCreated, &executionID, map[string]any{"state": "not-a-state"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildEnvelope(msg); err == nil || !strings.Contains(err.Error(), "event contract") {
		t.Fatalf("expected a contract violation, got %v", err)
	}
}

func TestBuildEnvelopeAllowsUnknownFutureType(t *testing.T) {
	msg := outbox.Message{
		MessageID: "msg_01JQ9A0B1C2D3E4F5G6H7J8K", Subject: "events.future", MessageType: "future.event",
		SchemaVersion: 1, OrgID: "org_01JQ9A0B1C2D3E4F5G6H7J8K", ProjectID: "proj_01JQ9A0B1C2D3E4F5G6H7J8K",
		ExecutionID: ptr("exec_01JQ9A0B1C2D3E4F5G6H7J8K"), Payload: json.RawMessage(`{"ok":true}`),
		CreatedAt: time.Now().UTC(),
	}
	if _, err := BuildEnvelope(msg); err != nil {
		t.Fatalf("unknown types must pass through: %v", err)
	}
}

func TestConsumerConfigDefaults(t *testing.T) {
	cfg := DefaultConsumerConfig("wrk_test_01", 3)
	if cfg.Durable != "worker-wrk_test_01" || cfg.FilterSubject != "tasks.dispatch.wrk_test_01" {
		t.Fatalf("unexpected consumer target: %+v", cfg)
	}
	if cfg.MaxDeliver != 5 || cfg.AckWait != 60*time.Second || cfg.MaxAckPending != 3 {
		t.Fatalf("unexpected delivery policy: %+v", cfg)
	}
	if len(cfg.BackOff) != 5 || cfg.DLQSubject != "tasks.dlq.wrk_test_01" {
		t.Fatalf("unexpected retry policy: %+v", cfg)
	}
	if cfg.BackOff[0] < cfg.AckWait {
		t.Fatalf("first backoff %s is below the ack wait %s; NATS would shrink the ack window", cfg.BackOff[0], cfg.AckWait)
	}
}

func TestConsumerRejectsInvalidWorkerID(t *testing.T) {
	handler := HandlerFunc(func(context.Context, jetstream.Msg) error { return nil })
	if _, err := NewConsumer(context.Background(), nil, ConsumerConfig{WorkerID: "bad.worker"}, handler,
		zap.NewNop()); err == nil {
		t.Fatal("expected an invalid worker id error")
	}
}

func TestRedriveDiscipline(t *testing.T) {
	if _, err := RedriveDLQ(context.Background(), nil, "wrk_test_01", 0, zap.NewNop()); err == nil {
		t.Fatal("expected limit validation")
	}
	if _, err := RedriveDLQ(context.Background(), nil, "", 5, zap.NewNop()); err == nil {
		t.Fatal("expected worker validation")
	}
}

func ptr[T any](value T) *T { return &value }

package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/agent-platform/agent-platform/internal/platform/idgen"
)

// DLQEnvelope preserves the original message plus the operator context needed to investigate and redrive it.
type DLQEnvelope struct {
	OriginalSubject   string              `json:"original_subject"`
	OriginalMessageID string              `json:"original_message_id,omitempty"`
	TenantID          string              `json:"tenant_id,omitempty"`
	ProjectID         string              `json:"project_id,omitempty"`
	ExecutionID       string              `json:"execution_id,omitempty"`
	TaskID            string              `json:"task_id,omitempty"`
	AttemptID         string              `json:"attempt_id,omitempty"`
	WorkerID          string              `json:"worker_id,omitempty"`
	Deliveries        uint64              `json:"deliveries"`
	Error             string              `json:"error"`
	FailedAt          time.Time           `json:"failed_at"`
	Payload           json.RawMessage     `json:"payload"`
	Headers           map[string][]string `json:"headers,omitempty"`
}

// DLQSubject returns the dead-letter subject for one worker.
func DLQSubject(workerID string) string {
	return "tasks.dlq." + workerID
}

// PublishDLQ records a failed delivery. The original message is acknowledged by the caller only after this publish
// succeeds; otherwise the message is redelivered and cannot be lost.
func PublishDLQ(ctx context.Context, js jetstream.JetStream, subject string, workerID string,
	original jetstream.Msg, deliveries uint64, cause error) error {
	var envelope struct {
		MessageID   string `json:"message_id"`
		TenantID    string `json:"tenant_id"`
		ProjectID   string `json:"project_id"`
		ExecutionID string `json:"execution_id"`
		TaskID      string `json:"task_id"`
		AttemptID   string `json:"attempt_id"`
	}
	_ = json.Unmarshal(original.Data(), &envelope)

	dlq := DLQEnvelope{
		OriginalSubject:   original.Subject(),
		OriginalMessageID: envelope.MessageID,
		TenantID:          envelope.TenantID,
		ProjectID:         envelope.ProjectID,
		ExecutionID:       envelope.ExecutionID,
		TaskID:            envelope.TaskID,
		AttemptID:         envelope.AttemptID,
		WorkerID:          workerID,
		Deliveries:        deliveries,
		Error:             cause.Error(),
		FailedAt:          time.Now().UTC(),
		Payload:           original.Data(),
		Headers:           original.Headers(),
	}
	data, err := json.Marshal(dlq)
	if err != nil {
		return fmt.Errorf("messaging: marshal dlq: %w", err)
	}

	message := nats.NewMsg(subject)
	message.Data = data
	message.Header.Set(jetstream.MsgIDHeader, idgen.New("dlq"))
	message.Header.Set("Content-Type", "application/json")

	ack, err := js.PublishMsg(ctx, message)
	if err != nil {
		return fmt.Errorf("messaging: publish dlq: %w", err)
	}
	if ack == nil || ack.Stream == "" {
		return fmt.Errorf("messaging: dlq publish returned no stream acknowledgement")
	}
	return nil
}

package outbox

import (
	"encoding/json"
	"testing"
)

func TestNewMessage(t *testing.T) {
	executionID := "exec_01JQ9A0B1C2D3E4F5G6H7J8K"
	msg, err := NewMessage(
		"org_01JQ9A0B1C2D3E4F5G6H7J8K",
		"events.org.org_01JQ9A0B1C2D3E4F5G6H7J8K.execution.exec_01JQ9A0B1C2D3E4F5G6H7J8K.created",
		"execution.created",
		&executionID,
		map[string]any{"seq": 1},
		map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("new message must be valid: %v", err)
	}
	if msg.MessageID[:4] != "msg_" {
		t.Fatalf("unexpected message id %q", msg.MessageID)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["seq"] != float64(1) {
		t.Fatalf("unexpected payload %v", payload)
	}
}

func TestNewMessageRejectsNonObjectPayload(t *testing.T) {
	if _, err := NewMessage("org_01JQ9A0B1C2D3E4F5G6H7J8K", "subj", "t", nil, []int{1, 2}, nil); err == nil {
		t.Fatal("array payload must be rejected")
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	cases := []Message{
		{},
		{ID: "not-a-uuid"},
		{ID: "f8feee36-f7de-4ddf-9ab0-ecf56f29c776", MessageID: "bad"},
	}
	for i, msg := range cases {
		if err := msg.Validate(); err == nil {
			t.Fatalf("case %d must be rejected", i)
		}
	}
}

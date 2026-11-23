package execution

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
)

func TestSplitCommandHonoursQuotes(t *testing.T) {
	parts, err := splitCommand(`"/Volumes/Some Disk/python" -m agent_runtime serve --socket /tmp/rt.sock`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/Volumes/Some Disk/python", "-m", "agent_runtime", "serve", "--socket", "/tmp/rt.sock"}
	if len(parts) != len(want) {
		t.Fatalf("parts = %v", parts)
	}
	for index := range want {
		if parts[index] != want[index] {
			t.Fatalf("parts[%d] = %q, want %q", index, parts[index], want[index])
		}
	}
	if _, err := splitCommand(`"unterminated`); err == nil {
		t.Fatal("unterminated quote must be rejected")
	}
}

func TestValidateAgainstSchema(t *testing.T) {
	schema, err := structpb.NewStruct(map[string]any{
		"type": "object", "required": []any{"report"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAgainstSchema(map[string]any{"report": "ok"}, schema); err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}
	if err := validateAgainstSchema(map[string]any{"other": "x"}, schema); err == nil {
		t.Fatal("output missing a required field must be rejected")
	}
	if err := validateAgainstSchema(map[string]any{"anything": true}, nil); err != nil {
		t.Fatalf("missing schema must accept any object: %v", err)
	}
}

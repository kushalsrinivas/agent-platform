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

func TestRuntimeEnvironmentDoesNotInheritControlPlaneSecrets(t *testing.T) {
	t.Setenv("WORKER_CONTROL_TOKEN", "should-not-leak")
	t.Setenv("NATS_PASSWORD", "should-not-leak")
	t.Setenv("PATH", "/usr/bin")
	env := runtimeEnvironment([]string{"AGENT_MCP_ALLOW_STDIO=true"})
	for _, entry := range env {
		if entry == "WORKER_CONTROL_TOKEN=should-not-leak" || entry == "NATS_PASSWORD=should-not-leak" {
			t.Fatalf("runtime environment leaked control-plane secret: %q", entry)
		}
	}
	if env[0] != "HOME=/tmp" {
		t.Fatalf("runtime environment must use an ephemeral home, got %q", env[0])
	}
}

func TestCredentialNamesReadsModelAndMCPReferences(t *testing.T) {
	spec, err := structpb.NewStruct(map[string]any{
		"model": map[string]any{"credential_ref": "provider_key"},
		"mcp_servers": []any{
			map[string]any{"name": "a", "auth_credential_ref": "mcp_token"},
			map[string]any{"name": "b", "auth_credential_ref": "provider_key"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	names := credentialNames(spec)
	if len(names) != 2 || names[0] != "mcp_token" || names[1] != "provider_key" {
		t.Fatalf("credential names = %v", names)
	}
}

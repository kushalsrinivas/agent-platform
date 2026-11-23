package agents

import (
	"strings"
	"testing"
)

func validSpec() AgentSpec {
	return AgentSpec{
		Model: Model{
			Provider:      "echo",
			Name:          "echo-1",
			CredentialRef: "default",
		},
		Instructions: "You are a research assistant.",
		Tools: []ToolBinding{
			{ToolID: "tool_web_search", Version: 1},
			{ToolID: "tool_artifact_write", Version: 1},
		},
		Runtime: Runtime{ABIVersion: 1, ImageDigest: "sha256:" + strings.Repeat("a", 64)},
		Limits:  &Limits{MaxTurns: intPtr(8), TimeoutSeconds: intPtr(300)},
		Checkpoint: &Checkpoint{
			Mode:              "on_node_end",
			RequireCheckpoint: boolPtr(false),
		},
	}
}

func TestAgentSpecValid(t *testing.T) {
	spec := validSpec()
	if err := spec.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	digest, err := spec.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("unexpected digest %q", digest)
	}
	again, err := validSpec().Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != again {
		t.Fatalf("digest is not stable: %s != %s", digest, again)
	}
}

func TestAgentSpecRejections(t *testing.T) {
	cases := map[string]func(*AgentSpec){
		"tag image":       func(s *AgentSpec) { s.Runtime.ImageDigest = "latest" },
		"no instructions": func(s *AgentSpec) { s.Instructions = "" },
		"duplicate tool":  func(s *AgentSpec) { s.Tools = append(s.Tools, s.Tools[0]) },
		"bad turn limit":  func(s *AgentSpec) { s.Limits.MaxTurns = intPtr(0) },
		"checkpoint none": func(s *AgentSpec) {
			s.Checkpoint = &Checkpoint{Mode: "none", RequireCheckpoint: boolPtr(true)}
		},
	}
	for name, mutate := range cases {
		spec := validSpec()
		mutate(&spec)
		if err := spec.Validate(); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func intPtr(value int) *int    { return &value }
func boolPtr(value bool) *bool { return &value }

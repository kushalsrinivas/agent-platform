package workflows

import (
	"strings"
	"testing"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

func TestEmbeddedSchemas(t *testing.T) {
	valid := map[string]any{"type": "object", "required": []any{"topic"}}
	if err := compileSchema("input", valid); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	if err := compileSchema("input", map[string]any{"type": "nope"}); err == nil {
		t.Fatal("invalid schema accepted")
	}
	if err := compileSchema("input", map[string]any{"$ref": "#/missing"}); err == nil {
		t.Fatal("unresolved ref accepted")
	}

	graph := contracts.WorkflowGraph{
		Nodes: []contracts.WorkflowNode{{
			ID: "a", Type: contracts.NodeTypeTool,
			Tool:         &contracts.ToolRef{ToolID: "tool_a", Version: 1},
			OutputSchema: &map[string]any{"type": "not-a-type"},
		}},
		Edges: []contracts.WorkflowEdge{},
	}
	if err := validateEmbeddedSchemas(&graph); err == nil || !strings.Contains(err.Error(), "output_schema") {
		t.Fatalf("invalid node output schema = %v", err)
	}
}

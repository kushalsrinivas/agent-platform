package contracts

import (
	"strings"
	"testing"
)

func TestGraphEngineNodeTypesValidate(t *testing.T) {
	graph := WorkflowGraph{
		Nodes: []WorkflowNode{
			{ID: "start", Type: NodeTypeStart},
			{ID: "score", Type: NodeTypeCondition, Config: &NodeConfig{Expression: ptrString("input.score >= 80")}},
			{ID: "approve", Type: NodeTypeTransform, Config: &NodeConfig{Mapping: &map[string]string{
				"decision": "'approved'", "report": "outputs.research.report",
			}}},
			{ID: "research", Type: NodeTypeTool, Tool: &ToolRef{ToolID: "tool_web_search", Version: 1}},
			{ID: "join", Type: NodeTypeJoin, Config: &NodeConfig{Join: &JoinPolicy{Mode: "all"}}},
			{ID: "pause", Type: NodeTypeDelay, Config: &NodeConfig{Delay: &DelayConfig{Seconds: ptrInt(1)}}},
			{ID: "review", Type: NodeTypeApproval, Config: &NodeConfig{Approval: &ApprovalConfig{}}},
			{ID: "end", Type: NodeTypeEnd},
		},
		Edges: []WorkflowEdge{
			{From: "start", To: "score"},
			{From: "score", To: "research"},
			{From: "research", To: "approve"},
			{From: "approve", To: "join"},
			{From: "pause", To: "join"},
			{From: "review", To: "join"},
			{From: "join", To: "end"},
			{From: "start", To: "pause"},
			{From: "start", To: "review"},
		},
	}
	if err := graph.Validate(); err != nil {
		t.Fatalf("graph should validate: %v", err)
	}
}

func TestGraphEngineRejectsUnsafeShapes(t *testing.T) {
	cases := []struct {
		name  string
		graph WorkflowGraph
		want  string
	}{
		{
			name: "cycle",
			graph: WorkflowGraph{
				Nodes: []WorkflowNode{{ID: "a", Type: NodeTypeStart}, {ID: "b", Type: NodeTypeParallel}},
				Edges: []WorkflowEdge{{From: "a", To: "b"}, {From: "b", To: "a"}},
			},
			want: "cycle",
		},
		{
			name: "bad condition",
			graph: WorkflowGraph{
				Nodes: []WorkflowNode{{ID: "a", Type: NodeTypeStart}, {ID: "b", Type: NodeTypeCondition,
					Config: &NodeConfig{Expression: ptrString("input.score >")}}},
				Edges: []WorkflowEdge{{From: "a", To: "b"}},
			},
			want: "expression",
		},
		{
			name: "start has incoming",
			graph: WorkflowGraph{
				Nodes: []WorkflowNode{{ID: "a", Type: NodeTypeStart}, {ID: "b", Type: NodeTypeParallel},
					{ID: "end", Type: NodeTypeEnd}},
				Edges: []WorkflowEdge{{From: "a", To: "b"}, {From: "b", To: "a"}, {From: "b", To: "end"}},
			},
			want: "start node",
		},
		{
			name: "approval all policy requires approvers",
			graph: WorkflowGraph{
				Nodes: []WorkflowNode{{ID: "a", Type: NodeTypeStart},
					{ID: "review", Type: NodeTypeApproval, Config: &NodeConfig{Approval: &ApprovalConfig{
						Policy: ptrString("all"),
					}}},
					{ID: "end", Type: NodeTypeEnd}},
				Edges: []WorkflowEdge{{From: "a", To: "review"}, {From: "review", To: "end"}},
			},
			want: "approvers",
		},
		{
			name: "loop requires config",
			graph: WorkflowGraph{
				Nodes: []WorkflowNode{{ID: "a", Type: NodeTypeStart}, {ID: "loop", Type: NodeTypeLoop},
					{ID: "end", Type: NodeTypeEnd}},
				Edges: []WorkflowEdge{{From: "a", To: "loop"}, {From: "loop", To: "end"}},
			},
			want: "config",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.graph.Validate()
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want containing %q", err, testCase.want)
			}
		})
	}
}

func ptrString(value string) *string { return &value }
func ptrInt(value int) *int          { return &value }

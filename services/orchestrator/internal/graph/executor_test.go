package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

func TestConditionAndTransformExecutors(t *testing.T) {
	registry := NewRegistry()
	condition, ok := registry.Get(contracts.NodeTypeCondition)
	if !ok {
		t.Fatal("condition executor missing")
	}
	config := &contracts.NodeConfig{Expression: stringPtr("input.score >= 80")}
	output, err := condition.Execute(context.Background(), NodeInput{
		RootInput: map[string]any{"score": 92}, Config: config, Now: time.Now(),
	})
	if err != nil || output.Output["result"] != true {
		t.Fatalf("condition output=%v err=%v", output.Output, err)
	}

	transform, _ := registry.Get(contracts.NodeTypeTransform)
	mapping := map[string]string{"decision": "'approved'", "report": "outputs.research.report"}
	output, err = transform.Execute(context.Background(), NodeInput{
		RootInput: map[string]any{}, Outputs: map[string]map[string]any{
			"research": {"report": "durable"},
		}, Config: &contracts.NodeConfig{Mapping: &mapping}, Now: time.Now(),
	})
	if err != nil || output.Output["decision"] != "approved" || output.Output["report"] != "durable" {
		t.Fatalf("transform output=%v err=%v", output.Output, err)
	}
}

func TestDelayAndApprovalKeepDurableState(t *testing.T) {
	registry := NewRegistry()
	delay, _ := registry.Get(contracts.NodeTypeDelay)
	now := time.Now().UTC()
	result, err := delay.Execute(context.Background(), NodeInput{
		Config: &contracts.NodeConfig{Delay: &contracts.DelayConfig{Seconds: intPtr(30)}},
		Now:    now, State: map[string]any{},
	})
	if err != nil || result.Deferred == nil || result.Deferred.State["ready_at"] == nil {
		t.Fatalf("delay deferred=%v err=%v", result.Deferred, err)
	}
	if !result.Deferred.ReadyAt.After(now) {
		t.Fatalf("delay readyAt=%v, want after %v", result.Deferred.ReadyAt, now)
	}

	approval, _ := registry.Get(contracts.NodeTypeApproval)
	pending, err := approval.Execute(context.Background(), NodeInput{Now: now, State: map[string]any{}})
	if err != nil || pending.Deferred == nil || pending.Deferred.State["status"] != "pending" {
		t.Fatalf("approval pending=%v err=%v", pending.Deferred, err)
	}
	approved, err := approval.Execute(context.Background(), NodeInput{
		Now: now, State: map[string]any{"status": "approved", "by": "operator"},
	})
	if err != nil || approved.Output["approved"] != true {
		t.Fatalf("approval approved=%v err=%v", approved.Output, err)
	}
}

func TestWorkerNodesRequireDispatcher(t *testing.T) {
	registry := NewRegistry()
	executor, _ := registry.Get(contracts.NodeTypeAgent)
	_, err := executor.Execute(context.Background(), NodeInput{})
	if !errors.Is(err, ErrWorkerRequired) {
		t.Fatalf("error=%v, want ErrWorkerRequired", err)
	}
}

func stringPtr(value string) *string { return &value }
func intPtr(value int) *int          { return &value }

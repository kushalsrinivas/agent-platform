package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeveloperSurface(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "apk_test" {
			t.Errorf("missing api key on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/organizations/org_1/projects/proj_1/agents":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"agt_1","org_id":"org_1","project_id":"proj_1","name":"a","latest_version":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/organizations/org_1/projects/proj_1/agents/agt_1/versions/1:publish":
			_, _ = w.Write([]byte(`{"id":"agtv_1","agent_id":"agt_1","version":1,"status":"published","digest":"sha256:x","spec":{},"runtime_abi":1,"created_by":"u","created_at":"2026-01-01T00:00:00Z"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/organizations/org_1/projects/proj_1/workflows:validate":
			_, _ = w.Write([]byte(`{"valid":true,"nodes":1,"edges":0}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/organizations/org_1/projects/proj_1/executions":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("execution submission must carry an idempotency key")
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"exec_1","org_id":"org_1","project_id":"proj_1","workflow_id":"wf_1","workflow_version_id":"wfv_1","workflow_version":1,"state":"succeeded","state_version":2,"priority":0,"counts":{"total":1,"succeeded":1},"input_digest":"sha256:x","created_by":"u","created_at":"2026-01-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/organizations/org_1/projects/proj_1/executions/exec_1/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("id: 1\nevent: task.state_changed\ndata: {\"seq\":1,\"event_id\":\"ev_1\",\"event_type\":\"task.state_changed\",\"entity_type\":\"task\",\"entity_id\":\"task_1\",\"actor_type\":\"system\",\"actor_id\":\"orchestrator\",\"payload\":{},\"created_at\":\"2026-01-01T00:00:00Z\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/organizations/org_1/workers":
			_, _ = w.Write([]byte(`{"items":[{"id":"wrk_1","pool":"default","status":"available","max_concurrency":2,"available_slots":2,"reserved_slots":0,"runtime":"python","runtime_version":"1","binary_version":"dev","cpu_cores":2,"memory_mb":1024,"capabilities":["agent_execution"],"last_heartbeat_at":"2026-01-01T00:00:00Z"}],"next_cursor":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"missing"}}`))
		}
	}))
	defer server.Close()

	api := NewWithConfig(Config{BaseURL: server.URL, APIKey: "apk_test", OrgID: "org_1", ProjectID: "proj_1"})
	ctx := context.Background()
	agent, err := api.CreateAgent(ctx, "", "a", "", map[string]any{"model": map[string]any{"provider": "echo"}})
	if err != nil || agent.ID != "agt_1" {
		t.Fatalf("agent=%v err=%v", agent, err)
	}
	if _, err := api.DeployAgent(ctx, "", agent.ID, 1); err != nil {
		t.Fatal(err)
	}
	if result, err := api.ValidateWorkflow(ctx, "", map[string]any{"nodes": []any{}, "edges": []any{}}); err != nil || !result.Valid {
		t.Fatalf("validation=%v err=%v", result, err)
	}
	execution, err := api.ExecuteWorkflow(ctx, "", "wf_1", 1, map[string]any{"topic": "x"}, ExecuteOptions{})
	if err != nil || execution.State != "succeeded" {
		t.Fatalf("execution=%v err=%v", execution, err)
	}
	workers, err := api.ListWorkers(ctx, "", "")
	if err != nil || len(workers) != 1 {
		t.Fatalf("workers=%v err=%v", workers, err)
	}
	var streamed int
	if err := api.StreamEvents(ctx, "", "exec_1", 0, func(_ Event) error {
		streamed++
		return nil
	}); err != nil || streamed != 1 {
		t.Fatalf("streamed=%d err=%v", streamed, err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(`{"ok":true}`), &body)
}

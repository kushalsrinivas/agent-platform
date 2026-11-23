package contracts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const repoRoot = "../.."

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "packages", "test-fixtures", rel)) //nolint:gosec // repository fixture
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return data
}

func compileSchema(t *testing.T, rel string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "packages", "schemas", rel)) //nolint:gosec // repository schema
	if err != nil {
		t.Fatalf("read schema %s: %v", rel, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse schema %s: %v", rel, err)
	}
	object, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("schema %s is not an object", rel)
	}
	id, ok := object["$id"].(string)
	if !ok {
		t.Fatalf("schema %s has no $id", rel)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(id, doc); err != nil {
		t.Fatalf("register schema %s: %v", rel, err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatalf("compile schema %s: %v", rel, err)
	}
	return schema
}

func validateSchema(t *testing.T, schema *jsonschema.Schema, data []byte) error {
	t.Helper()
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse instance: %v", err)
	}
	err = schema.Validate(instance)
	if err == nil {
		return nil
	}
	return &ValidationError{Problems: []string{err.Error()}}
}

func TestWorkflowFixtureMatchesSchema(t *testing.T) {
	schema := compileSchema(t, "workflow.schema.json")
	if err := validateSchema(t, schema, readFixture(t, "workflows/research.json")); err != nil {
		t.Fatalf("research workflow must satisfy the JSON Schema: %v", err)
	}
	for _, name := range []string{"invalid-agent-version.json"} {
		if err := validateSchema(t, schema, readFixture(t, "workflows/"+name)); err == nil {
			t.Fatalf("%s must be rejected by the JSON Schema", name)
		}
	}
}

func TestWorkflowSemanticValidation(t *testing.T) {
	valid, err := ParseWorkflowDocument(readFixture(t, "workflows/research.json"))
	if err != nil {
		t.Fatalf("research workflow must be valid: %v", err)
	}
	if len(valid.Nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(valid.Nodes))
	}

	cases := map[string]string{
		"invalid-cycle.json":        "cycle",
		"invalid-unknown-edge.json": "unknown",
		"invalid-input-from.json":   "upstream",
		"invalid-side-effect.json":  "idempotency_key",
	}
	for name, want := range cases {
		_, err := ParseWorkflowDocument(readFixture(t, "workflows/"+name))
		if err == nil {
			t.Fatalf("%s must be rejected", name)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s error %q does not mention %q", name, err, want)
		}
	}
}

func TestWorkflowCanonicalDigestsMatchPython(t *testing.T) {
	doc, err := ParseWorkflowDocument(readFixture(t, "workflows/research.json"))
	if err != nil {
		t.Fatal(err)
	}

	var golden struct {
		Workflow       string `json:"workflow"`
		GraphSHA256    string `json:"graph_sha256"`
		DocumentSHA256 string `json:"document_sha256"`
	}
	if err := json.Unmarshal(readFixture(t, "workflow.digests.json"), &golden); err != nil {
		t.Fatalf("read golden digests: %v", err)
	}

	graphDigest, err := doc.GraphDigest()
	if err != nil {
		t.Fatal(err)
	}
	documentDigest, err := doc.DocumentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if graphDigest != golden.GraphSHA256 {
		t.Fatalf("graph digest = %s, want %s", graphDigest, golden.GraphSHA256)
	}
	if documentDigest != golden.DocumentSHA256 {
		t.Fatalf("document digest = %s, want %s", documentDigest, golden.DocumentSHA256)
	}

	canonical, err := doc.CanonicalDocument()
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := ParseWorkflowDocument(canonical)
	if err != nil {
		t.Fatalf("canonical document must parse back: %v", err)
	}
	again, err := roundTripped.DocumentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if again != documentDigest {
		t.Fatalf("round-tripped digest = %s, want %s", again, documentDigest)
	}
}

func TestWorkflowRejectsUnknownFields(t *testing.T) {
	data := readFixture(t, "workflows/research.json")
	doc, err := ParseWorkflowDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	mutated, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseWorkflowDocument(mutated); err == nil {
		t.Fatal("unknown top-level fields must be rejected")
	}
}

func TestTaskContracts(t *testing.T) {
	dispatchSchema := compileSchema(t, "task-dispatch.schema.json")
	resultSchema := compileSchema(t, "task-result.schema.json")

	dispatchData := readFixture(t, "tasks/dispatch.json")
	if err := validateSchema(t, dispatchSchema, dispatchData); err != nil {
		t.Fatalf("dispatch fixture schema: %v", err)
	}
	dispatch, err := ParseTaskDispatch(dispatchData)
	if err != nil {
		t.Fatalf("dispatch fixture: %v", err)
	}
	if dispatch.NodeType != NodeTypeTool || dispatch.ToolVersionID == nil {
		t.Fatalf("unexpected dispatch: %+v", dispatch)
	}

	for _, name := range []string{"tasks/result-success.json", "tasks/result-failure.json"} {
		data := readFixture(t, name)
		if err := validateSchema(t, resultSchema, data); err != nil {
			t.Fatalf("%s schema: %v", name, err)
		}
		if _, err := ParseTaskResult(data); err != nil {
			t.Fatalf("%s contract: %v", name, err)
		}
	}

	invalid := map[string]any{
		"schema_version": 1,
		"task_id":        "task_01JQ9A0B1C2D3E4F5G6H7J8K",
		"attempt_id":     "att_01JQ9A0B1C2D3E4F5G6H7J8K",
		"lease_id":       "8e4f3a2b-1c9d-4e8f-a0b1-c2d3e4f5a6b7",
		"outcome":        "succeeded",
	}
	invalidData, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSchema(t, resultSchema, invalidData); err == nil {
		t.Fatal("a successful result without output must fail the schema")
	}
	if _, err := ParseTaskResult(invalidData); err == nil {
		t.Fatal("a successful result without output must fail typed validation")
	}
}

func TestEventContracts(t *testing.T) {
	envelopeSchema := compileSchema(t, "../../docs/api/envelope.schema.json")
	payloadSchemas := map[string]*jsonschema.Schema{
		EventExecutionCreated:      compileSchema(t, "events/execution.created.schema.json"),
		EventExecutionStateChanged: compileSchema(t, "events/execution.state_changed.schema.json"),
		EventTaskStateChanged:      compileSchema(t, "events/task.state_changed.schema.json"),
		EventTaskAttemptFinished:   compileSchema(t, "events/task.attempt_finished.schema.json"),
		EventTaskDispatch:          compileSchema(t, "events/task.dispatch.schema.json"),
	}

	for _, name := range []string{
		"events/execution.created.json",
		"events/execution.state_changed.json",
		"events/task.state_changed.json",
		"events/task.attempt_finished.json",
		"events/task.dispatch.json",
	} {
		data := readFixture(t, name)
		if err := validateSchema(t, envelopeSchema, data); err != nil {
			t.Fatalf("%s envelope schema: %v", name, err)
		}

		var envelope struct {
			MessageType string          `json:"message_type"`
			Payload     json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatal(err)
		}
		payloadSchema, ok := payloadSchemas[envelope.MessageType]
		if !ok {
			t.Fatalf("%s has unsupported message type %q", name, envelope.MessageType)
		}
		if err := validateSchema(t, payloadSchema, envelope.Payload); err != nil {
			t.Fatalf("%s payload schema: %v", name, err)
		}

		parsed, err := ParseEvent(data)
		if err != nil {
			t.Fatalf("%s typed event: %v", name, err)
		}
		if parsed.Envelope.MessageType != envelope.MessageType {
			t.Fatalf("%s parsed type %q", name, parsed.Envelope.MessageType)
		}
	}
}

func TestDigestIsStableAcrossKeyOrder(t *testing.T) {
	first := []byte(`{"b":2,"a":1}`)
	second := []byte(`{"a":1,"b":2}`)
	firstDigest, err := DigestJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := DigestJSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("digest depends on key order: %s != %s", firstDigest, secondDigest)
	}
}

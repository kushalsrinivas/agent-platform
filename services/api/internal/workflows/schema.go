package workflows

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

// validateEmbeddedSchemas compiles the workflow input schema and every node output schema. A malformed schema is a
// publication error, not an execution-time surprise.
func validateEmbeddedSchemas(graph *contracts.WorkflowGraph) error {
	if graph.InputSchema != nil {
		if err := compileSchema("workflow input_schema", *graph.InputSchema); err != nil {
			return err
		}
	}
	for _, node := range graph.Nodes {
		if node.InputSchema != nil {
			if err := compileSchema(fmt.Sprintf("node %q input_schema", node.ID), *node.InputSchema); err != nil {
				return err
			}
		}
		if node.OutputSchema != nil {
			if err := compileSchema(fmt.Sprintf("node %q output_schema", node.ID), *node.OutputSchema); err != nil {
				return err
			}
		}
	}
	return nil
}

func compileSchema(label string, schemaMap map[string]any) error {
	raw, err := json.Marshal(schemaMap)
	if err != nil {
		return fmt.Errorf("%s is not serialisable", label)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%s is not valid JSON", label)
	}
	const schemaID = "urn:agent-platform:embedded-schema"
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(schemaID, document); err != nil {
		return fmt.Errorf("%s is invalid: %w", label, err)
	}
	if _, err := compiler.Compile(schemaID); err != nil {
		return fmt.Errorf("%s is invalid: %w", label, err)
	}
	return nil
}

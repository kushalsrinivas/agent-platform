package contracts

import (
	"fmt"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types/ref"
)

// Expression variables available to edge conditions, condition nodes, transforms, subworkflow input mappings and
// loop exit conditions. The language is CEL: non-Turing-complete, compiled at publish time and evaluated in the
// orchestrator; arbitrary Go/Python is never executed.
const (
	ExprInput     = "input"
	ExprOutputs   = "outputs"
	ExprParams    = "params"
	ExprExecution = "execution"
	ExprIteration = "iteration"
	ExprPrevious  = "previous"
	ExprNode      = "node"
	ExprNodeInput = "node_input"
)

var (
	expressionEnvOnce sync.Once
	expressionEnv     *cel.Env
	expressionEnvErr  error
)

// ExpressionEnv returns the shared CEL environment.
func ExpressionEnv() (*cel.Env, error) {
	expressionEnvOnce.Do(func() {
		expressionEnv, expressionEnvErr = cel.NewEnv(
			cel.Variable(ExprInput, cel.DynType),
			cel.Variable(ExprOutputs, cel.DynType),
			cel.Variable(ExprParams, cel.DynType),
			cel.Variable(ExprExecution, cel.DynType),
			cel.Variable(ExprIteration, cel.IntType),
			cel.Variable(ExprPrevious, cel.DynType),
			cel.Variable(ExprNode, cel.DynType),
			cel.Variable(ExprNodeInput, cel.DynType),
		)
	})
	return expressionEnv, expressionEnvErr
}

// CompileExpression compiles a validated CEL expression.
func CompileExpression(expression string) (cel.Program, error) {
	env, err := ExpressionEnv()
	if err != nil {
		return nil, err
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("invalid expression: %w", issues.Err())
	}
	program, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("invalid expression: %w", err)
	}
	return program, nil
}

// CompileCondition compiles an expression that must return a boolean.
func CompileCondition(expression string) (cel.Program, error) {
	env, err := ExpressionEnv()
	if err != nil {
		return nil, err
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("invalid expression: %w", issues.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("condition must return bool, got %s", ast.OutputType())
	}
	program, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("invalid expression: %w", err)
	}
	return program, nil
}

// EvaluateExpression runs a compiled expression with the engine's variable context.
func EvaluateExpression(program cel.Program, variables map[string]any) (any, error) {
	if program == nil {
		return nil, fmt.Errorf("expression is not compiled")
	}
	value, _, err := program.Eval(variables)
	if err != nil {
		return nil, fmt.Errorf("expression evaluation failed: %w", err)
	}
	return expressionNative(value), nil
}

// EvaluateCondition runs a compiled boolean expression.
func EvaluateCondition(program cel.Program, variables map[string]any) (bool, error) {
	value, err := EvaluateExpression(program, variables)
	if err != nil {
		return false, err
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("condition returned %T, want bool", value)
	}
	return boolean, nil
}

func expressionNative(value ref.Val) any {
	switch typed := value.Value().(type) {
	case map[ref.Val]ref.Val:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[fmt.Sprint(expressionNative(key))] = expressionNative(item)
		}
		return result
	case []ref.Val:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, expressionNative(item))
		}
		return result
	default:
		return typed
	}
}

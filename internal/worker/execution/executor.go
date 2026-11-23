// Package execution spawns one Python runtime process per attempt, executes the pinned agent over the runtime
// ABI and maps the result into the delivery outcome. The process is the sandbox boundary: no database, NATS or
// object-store credentials ever reach it, and cancellation is enforced by terminating the process group.
package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	runtimev1 "github.com/agent-platform/agent-platform/gen/runtime/v1"
	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/worker/control"
	"github.com/agent-platform/agent-platform/internal/worker/delivery"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

const maxRuntimeTail = 8 << 10

// Config controls runtime process lifecycle.
type Config struct {
	// CommandTemplate must contain "{socket}"; it is replaced with the per-attempt socket path.
	CommandTemplate string
	RuntimeABI      int32
	RuntimeVersion  string
	StartupTimeout  time.Duration
	KillGrace       time.Duration
}

// Executor runs attempts through the Python runtime.
type Executor struct {
	client *control.Client
	cfg    Config
	log    *zap.Logger
}

// NewExecutor builds the runtime executor.
func NewExecutor(client *control.Client, cfg Config, log *zap.Logger) *Executor {
	if cfg.RuntimeABI < 1 {
		cfg.RuntimeABI = 1
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 15 * time.Second
	}
	if cfg.KillGrace <= 0 {
		cfg.KillGrace = 5 * time.Second
	}
	return &Executor{client: client, cfg: cfg, log: log}
}

// Execute runs one attempt in a fresh runtime process.
func (e *Executor) Execute(ctx context.Context, dispatch *contracts.TaskDispatch) (delivery.ExecutionOutcome, error) {
	if e.cfg.CommandTemplate == "" || !strings.Contains(e.cfg.CommandTemplate, "{socket}") {
		return delivery.ExecutionOutcome{}, errors.New("runtime command template must contain {socket}")
	}
	taskCtx, err := e.client.GetTaskContext(ctx, dispatch.TaskID, dispatch.AttemptID, dispatch.LeaseID)
	if err != nil {
		return delivery.ExecutionOutcome{}, fmt.Errorf("load task context: %w", err)
	}

	workDir, err := os.MkdirTemp("", "agent-runtime-")
	if err != nil {
		return delivery.ExecutionOutcome{}, err
	}
	defer func() { _ = os.RemoveAll(workDir) }()
	socketPath := filepath.Join(workDir, "runtime.sock")

	command := strings.ReplaceAll(e.cfg.CommandTemplate, "{socket}", socketPath)
	parts, err := splitCommand(command)
	if err != nil {
		return delivery.ExecutionOutcome{}, err
	}
	if len(parts) == 0 {
		return delivery.ExecutionOutcome{}, errors.New("runtime command template is empty")
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...) //nolint:gosec // runtime command is operator configuration.
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	tail := &tailBuffer{}
	// Python emits one JSON event per line (task_started, model_request_started, ...); forward it to the worker
	// log stream while keeping the last bytes for failure diagnostics.
	cmd.Stdout = io.MultiWriter(tail, os.Stdout)
	cmd.Stderr = io.MultiWriter(tail, os.Stderr)
	if err := cmd.Start(); err != nil {
		return delivery.ExecutionOutcome{}, fmt.Errorf("start runtime process: %w", err)
	}

	processDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(processDone)
	}()
	// The process group is the sandbox: on any exit path the whole group is terminated.
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-processDone:
		case <-time.After(e.cfg.KillGrace):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-processDone
		}
	}()

	if err := waitForSocket(socketPath, e.cfg.StartupTimeout, processDone); err != nil {
		return delivery.ExecutionOutcome{}, fmt.Errorf("runtime process did not start: %w: %s",
			err, strings.TrimSpace(tail.String()))
	}

	conn, err := grpc.NewClient("unix:"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return delivery.ExecutionOutcome{}, fmt.Errorf("dial runtime socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	e.log.Debug("runtime process started",
		zap.String("task_id", dispatch.TaskID),
		zap.String("attempt_id", dispatch.AttemptID),
		zap.String("socket", socketPath),
	)

	agentContext := &runtimev1.AgentTaskContext{
		OrgId:           dispatch.OrgID,
		ProjectId:       dispatch.ProjectID,
		ExecutionId:     dispatch.ExecutionID,
		TaskId:          dispatch.TaskID,
		AttemptId:       dispatch.AttemptID,
		NodeId:          dispatch.NodeID,
		AgentVersionId:  taskCtx.GetAgentVersionId(),
		VersionSpec:     taskCtx.GetVersionSpec(),
		ExecutionInput:  taskCtx.GetExecutionInput(),
		NodeParams:      taskCtx.GetNodeParams(),
		UpstreamOutputs: taskCtx.GetUpstreamOutputs(),
		Deadline:        taskCtx.GetDeadline(),
		Traceparent:     taskCtx.GetTraceparent(),
	}
	if dispatch.NodeType == contracts.NodeTypeTool {
		toolID, toolVersion, err := splitToolVersion(dispatch.ToolVersionID)
		if err != nil {
			return delivery.ExecutionOutcome{}, err
		}
		arguments := map[string]any{}
		if taskCtx.GetNodeParams() != nil {
			arguments = taskCtx.GetNodeParams().AsMap()
		}
		if len(arguments) == 0 && taskCtx.GetExecutionInput() != nil {
			arguments = map[string]any{"input": taskCtx.GetExecutionInput().AsMap()}
		}
		response, err := runtimev1.NewToolRuntimeClient(conn).ExecuteTool(ctx, &runtimev1.ExecuteToolRequest{
			Context: &runtimev1.ToolTaskContext{
				OrgId:       dispatch.OrgID,
				ProjectId:   dispatch.ProjectID,
				ExecutionId: dispatch.ExecutionID,
				TaskId:      dispatch.TaskID,
				AttemptId:   dispatch.AttemptID,
				NodeId:      dispatch.NodeID,
				ToolId:      toolID,
				ToolVersion: toolVersion,
				Arguments:   mustStruct(arguments),
				Deadline:    taskCtx.GetDeadline(),
				Traceparent: taskCtx.GetTraceparent(),
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return delivery.ExecutionOutcome{}, fmt.Errorf("runtime call interrupted: %w", ctx.Err())
			}
			return delivery.ExecutionOutcome{}, fmt.Errorf("tool runtime call failed: %w: %s",
				err, strings.TrimSpace(tail.String()))
		}
		return e.mapToolResponse(taskCtx, response)
	}

	response, err := runtimev1.NewAgentRuntimeClient(conn).ExecuteNode(ctx, &runtimev1.ExecuteNodeRequest{
		Context:    agentContext,
		RuntimeAbi: e.cfg.RuntimeABI,
	})
	if err != nil {
		// A cancelled context is the worker stopping or the task deadline; either way the lease owns recovery.
		if ctx.Err() != nil {
			return delivery.ExecutionOutcome{}, fmt.Errorf("runtime call interrupted: %w", ctx.Err())
		}
		return delivery.ExecutionOutcome{}, fmt.Errorf("runtime call failed: %w: %s",
			err, strings.TrimSpace(tail.String()))
	}

	return e.mapResponse(taskCtx, response)
}

func (e *Executor) mapToolResponse(taskCtx *workerv1.GetTaskContextResponse,
	response *runtimev1.ExecuteToolResponse) (delivery.ExecutionOutcome, error) {
	outcome := delivery.ExecutionOutcome{RuntimeVersion: response.GetRuntimeVersion()}
	if response.GetMetrics() != nil {
		outcome.Metrics = &workerv1.AttemptMetrics{
			DurationMs: response.GetMetrics().GetDurationMs(),
			Extra:      response.GetMetrics().GetExtra(),
		}
	}
	switch response.GetOutcome() {
	case runtimev1.RuntimeOutcome_RUNTIME_OUTCOME_SUCCEEDED:
		if response.GetOutput() != nil {
			outcome.Output = response.GetOutput().AsMap()
		}
		if err := validateAgainstSchema(outcome.Output, taskCtx.GetNodeOutputSchema()); err != nil {
			outcome.Output = nil
			outcome.Failure = &workerv1.TaskFailure{
				Code: "RESULT_SCHEMA_INVALID", Message: truncate(err.Error(), 512),
				Classification: workerv1.FailureClassification_FAILURE_CLASSIFICATION_PERMANENT,
			}
		}
	case runtimev1.RuntimeOutcome_RUNTIME_OUTCOME_CANCELLED:
		outcome.Failure = &workerv1.TaskFailure{
			Code: "TOOL_CANCELLED", Message: "tool execution was cancelled",
			Classification: workerv1.FailureClassification_FAILURE_CLASSIFICATION_CANCELLED,
		}
	default:
		outcome.Failure = failureFromRuntime(response.GetFailure(), "TOOL_FAILED",
			workerv1.FailureClassification_FAILURE_CLASSIFICATION_WORKER_ERROR)
	}
	return outcome, nil
}

func splitToolVersion(value *string) (string, int32, error) {
	if value == nil || *value == "" {
		return "", 0, errors.New("tool task has no tool version id")
	}
	toolID, rawVersion, found := strings.Cut(*value, ":")
	if !found {
		return toolID, 1, nil
	}
	version, err := strconv.Atoi(rawVersion)
	if err != nil || version < 1 {
		return "", 0, fmt.Errorf("tool version id %q is invalid", *value)
	}
	return toolID, int32(version), nil //nolint:gosec // tool versions are bounded by the control plane.
}

func mustStruct(value map[string]any) *structpb.Struct {
	converted, err := structpb.NewStruct(value)
	if err != nil {
		return &structpb.Struct{}
	}
	return converted
}

func (e *Executor) mapResponse(taskCtx *workerv1.GetTaskContextResponse,
	response *runtimev1.ExecuteNodeResponse) (delivery.ExecutionOutcome, error) {
	outcome := delivery.ExecutionOutcome{RuntimeVersion: response.GetRuntimeVersion()}
	if response.GetMetrics() != nil {
		metrics := &workerv1.AttemptMetrics{
			DurationMs: response.GetMetrics().GetDurationMs(),
			TokensIn:   response.GetMetrics().GetTokensIn(),
			TokensOut:  response.GetMetrics().GetTokensOut(),
			Steps:      response.GetMetrics().GetSteps(),
			Extra:      response.GetMetrics().GetExtra(),
		}
		outcome.Metrics = metrics
	}
	if len(response.GetCheckpoint()) > 0 {
		outcome.Checkpoint = response.GetCheckpoint()
	}

	switch response.GetOutcome() {
	case runtimev1.RuntimeOutcome_RUNTIME_OUTCOME_SUCCEEDED:
		output := response.GetOutput()
		if output != nil {
			outcome.Output = output.AsMap()
		}
		if err := validateAgainstSchema(outcome.Output, taskCtx.GetNodeOutputSchema()); err != nil {
			e.log.Warn("runtime output rejected by node schema",
				zap.String("task_id", taskCtx.GetNodeId()),
				zap.Error(err),
			)
			outcome.Output = nil
			outcome.Failure = &workerv1.TaskFailure{
				Code: "RESULT_SCHEMA_INVALID", Message: truncate(err.Error(), 512),
				Classification: workerv1.FailureClassification_FAILURE_CLASSIFICATION_PERMANENT,
			}
		}
		return outcome, nil
	case runtimev1.RuntimeOutcome_RUNTIME_OUTCOME_CANCELLED:
		// Execution-level cancellation is not wired yet, so a runtime cancellation is recorded as a worker
		// cancellation failure rather than a task cancellation.
		outcome.Failure = failureFromRuntime(response.GetFailure(), "RUNTIME_CANCELLED",
			workerv1.FailureClassification_FAILURE_CLASSIFICATION_CANCELLED)
		return outcome, nil
	default:
		failure := response.GetFailure()
		if failure == nil {
			outcome.Failure = &workerv1.TaskFailure{
				Code: "RUNTIME_FAILED", Message: "runtime returned a failure without detail",
				Classification: workerv1.FailureClassification_FAILURE_CLASSIFICATION_WORKER_ERROR,
			}
			return outcome, nil
		}
		outcome.Failure = failureFromRuntime(failure, "RUNTIME_FAILED",
			workerv1.FailureClassification_FAILURE_CLASSIFICATION_WORKER_ERROR)
		return outcome, nil
	}
}

func failureFromRuntime(failure *runtimev1.RuntimeFailure, defaultCode string,
	defaultClassification workerv1.FailureClassification) *workerv1.TaskFailure {
	code := defaultCode
	if failure.GetCode() != "" {
		code = failure.GetCode()
	}
	classification := defaultClassification
	switch failure.GetClassification() {
	case runtimev1.RuntimeFailureClassification_RUNTIME_FAILURE_CLASSIFICATION_TRANSIENT:
		classification = workerv1.FailureClassification_FAILURE_CLASSIFICATION_TRANSIENT
	case runtimev1.RuntimeFailureClassification_RUNTIME_FAILURE_CLASSIFICATION_PERMANENT:
		classification = workerv1.FailureClassification_FAILURE_CLASSIFICATION_PERMANENT
	case runtimev1.RuntimeFailureClassification_RUNTIME_FAILURE_CLASSIFICATION_TIMEOUT:
		classification = workerv1.FailureClassification_FAILURE_CLASSIFICATION_TIMEOUT
	case runtimev1.RuntimeFailureClassification_RUNTIME_FAILURE_CLASSIFICATION_CANCELLED:
		classification = workerv1.FailureClassification_FAILURE_CLASSIFICATION_CANCELLED
	case runtimev1.RuntimeFailureClassification_RUNTIME_FAILURE_CLASSIFICATION_RUNTIME_ERROR:
		classification = workerv1.FailureClassification_FAILURE_CLASSIFICATION_WORKER_ERROR
	}
	return &workerv1.TaskFailure{
		Code:            code,
		Message:         truncate(failure.GetMessage(), 4096),
		Classification:  classification,
		EffectUncertain: failure.GetEffectUncertain(),
	}
}

// validateAgainstSchema checks the runtime output against the pinned node output schema before the result is
// reported, so an invalid output can never be committed as a task success.
func validateAgainstSchema(output map[string]any, schema *structpb.Struct) error {
	if schema == nil {
		return nil
	}
	schemaJSON, err := json.Marshal(schema.AsMap())
	if err != nil {
		return fmt.Errorf("node output_schema is not serialisable")
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return fmt.Errorf("node output_schema is invalid")
	}
	const schemaID = "urn:agent-platform:node:output"
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(schemaID, document); err != nil {
		return fmt.Errorf("node output_schema is invalid")
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return fmt.Errorf("node output_schema is invalid")
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(mustJSON(output)))
	if err != nil {
		return fmt.Errorf("runtime output is not valid JSON")
	}
	return compiled.Validate(instance)
}

func mustJSON(value map[string]any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

// splitCommand splits a command template on whitespace while honouring single and double quotes. Quote-aware
// splitting keeps executable paths that contain spaces executable without introducing a shell.
func splitCommand(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	inToken := false
	for _, char := range input {
		switch {
		case quote != 0:
			if char == quote {
				quote = 0
				continue
			}
			current.WriteRune(char)
		case char == '"' || char == '\'':
			quote = char
			inToken = true
		case char == ' ' || char == '\t' || char == '\n':
			if inToken {
				args = append(args, current.String())
				current.Reset()
				inToken = false
			}
		default:
			current.WriteRune(char)
			inToken = true
		}
	}
	if quote != 0 {
		return nil, errors.New("runtime command template has an unterminated quote")
	}
	if inToken {
		args = append(args, current.String())
	}
	return args, nil
}

func waitForSocket(path string, timeout time.Duration, processDone <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		select {
		case <-processDone:
			return errors.New("runtime process exited before creating its socket")
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("socket %s was not created within %s", path, timeout)
}

// tailBuffer keeps the last maxRuntimeTail bytes of runtime process output so failures carry a diagnostic
// without retaining prompts or full logs.
type tailBuffer struct {
	mu     sync.Mutex
	buffer []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buffer = append(t.buffer, p...)
	if len(t.buffer) > maxRuntimeTail {
		t.buffer = t.buffer[len(t.buffer)-maxRuntimeTail:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buffer)
}

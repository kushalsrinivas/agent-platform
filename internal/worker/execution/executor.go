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
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
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
	// Env is appended to the runtime process environment (tests and operator overrides).
	Env []string
	// Sandbox limits are enforced by prlimit on the child process. RequireSandbox fails closed if prlimit is
	// unavailable instead of silently running without limits.
	MemoryMB       int64
	CPUSeconds     int
	ProcessLimit   int
	FileSizeMB     int64
	OpenFiles      int
	RequireSandbox bool
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
	if cfg.MemoryMB <= 0 {
		cfg.MemoryMB = 1024
	}
	if cfg.CPUSeconds <= 0 {
		cfg.CPUSeconds = 300
	}
	if cfg.ProcessLimit <= 0 {
		cfg.ProcessLimit = 256
	}
	if cfg.FileSizeMB <= 0 {
		cfg.FileSizeMB = 64
	}
	if cfg.OpenFiles <= 0 {
		cfg.OpenFiles = 1024
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
	checkpointDir := filepath.Join(workDir, "checkpoints")
	if err := os.MkdirAll(checkpointDir, 0o700); err != nil {
		return delivery.ExecutionOutcome{}, fmt.Errorf("create checkpoint directory: %w", err)
	}

	command := strings.ReplaceAll(e.cfg.CommandTemplate, "{socket}", socketPath)
	parts, err := splitCommand(command)
	if err != nil {
		return delivery.ExecutionOutcome{}, err
	}
	if len(parts) == 0 {
		return delivery.ExecutionOutcome{}, errors.New("runtime command template is empty")
	}
	program, args, err := e.sandboxCommand(parts)
	if err != nil {
		return delivery.ExecutionOutcome{}, err
	}
	credentials := map[string]string{}
	if names := credentialNames(taskCtx.GetVersionSpec()); len(names) > 0 {
		response, credErr := e.client.GetTaskCredentials(ctx, dispatch.TaskID, dispatch.AttemptID, dispatch.LeaseID, names)
		if credErr != nil {
			e.log.Warn("task credentials unavailable", zap.String("task_id", dispatch.TaskID), zap.Error(credErr))
		} else {
			credentials = response.GetValues()
		}
	}
	cmd := exec.CommandContext(ctx, program, args...) //nolint:gosec // runtime command is operator configuration.
	cmd.Dir = workDir
	cmd.Env = runtimeEnvironment(e.cfg.Env)
	cmd.Env = append(cmd.Env, "AGENT_RUNTIME_CHECKPOINT_DIR="+checkpointDir)
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

	conn, err := grpc.NewClient("unix:"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(36<<20), grpc.MaxCallSendMsgSize(36<<20)))
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
		NodeInput:       taskCtx.GetNodeInput(),
		UpstreamOutputs: taskCtx.GetUpstreamOutputs(),
		Deadline:        taskCtx.GetDeadline(),
		Traceparent:     taskCtx.GetTraceparent(),
		LeaseId:         dispatch.LeaseID,
		Credentials:     credentials,
	}
	if dispatch.NodeType == contracts.NodeTypeTool {
		toolID, toolVersion, err := splitToolVersion(dispatch.ToolVersionID)
		if err != nil {
			return delivery.ExecutionOutcome{}, err
		}
		arguments := map[string]any{}
		if taskCtx.GetNodeInput() != nil {
			arguments = taskCtx.GetNodeInput().AsMap()
		}
		if len(arguments) == 0 && taskCtx.GetNodeParams() != nil {
			arguments = taskCtx.GetNodeParams().AsMap()
		}
		if len(arguments) == 0 && taskCtx.GetExecutionInput() != nil {
			arguments = map[string]any{"input": taskCtx.GetExecutionInput().AsMap()}
		}
		response, err := runtimev1.NewToolRuntimeClient(conn).ExecuteTool(ctx, &runtimev1.ExecuteToolRequest{
			Context: &runtimev1.ToolTaskContext{
				OrgId:          dispatch.OrgID,
				ProjectId:      dispatch.ProjectID,
				ExecutionId:    dispatch.ExecutionID,
				TaskId:         dispatch.TaskID,
				AttemptId:      dispatch.AttemptID,
				NodeId:         dispatch.NodeID,
				ToolId:         toolID,
				ToolVersion:    toolVersion,
				Arguments:      mustStruct(arguments),
				Deadline:       taskCtx.GetDeadline(),
				Traceparent:    taskCtx.GetTraceparent(),
				IdempotencyKey: taskCtx.GetIdempotencyKey(),
				NodeInput:      taskCtx.GetNodeInput(),
				Credentials:    credentials,
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

	uploader := startCheckpointUploader(ctx, e.client, dispatch, checkpointDir,
		e.cfg.RuntimeABI, e.cfg.RuntimeVersion, e.log)
	response, err := runtimev1.NewAgentRuntimeClient(conn).ExecuteNode(ctx, &runtimev1.ExecuteNodeRequest{
		Context:                 agentContext,
		ResumeCheckpoint:        taskCtx.GetResumeCheckpointBytes(),
		CheckpointSchemaVersion: taskCtx.GetResumeCheckpointSchemaVersion(),
		RuntimeAbi:              e.cfg.RuntimeABI,
	})
	drainErr := uploader.StopAndDrain(ctx)
	if err != nil {
		// A cancelled context is the worker stopping or the task deadline; either way the lease owns recovery.
		if ctx.Err() != nil {
			return delivery.ExecutionOutcome{}, fmt.Errorf("runtime call interrupted: %w", ctx.Err())
		}
		return delivery.ExecutionOutcome{}, fmt.Errorf("runtime call failed: %w: %s",
			err, strings.TrimSpace(tail.String()))
	}
	outcome, mappingErr := e.mapResponse(taskCtx, response)
	if mappingErr != nil {
		return outcome, mappingErr
	}
	if drainErr != nil && outcome.Failure == nil {
		// Never report a success whose checkpoint was not durably recorded.
		return delivery.ExecutionOutcome{}, fmt.Errorf("checkpoint upload failed: %w", drainErr)
	}
	return outcome, nil
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
				Classification: workerv1.FailureClassification_FAILURE_CLASSIFICATION_RESULT_INVALID,
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

// sandboxCommand wraps the runtime process with OS resource limits. The worker image is non-root and drops all
// capabilities, so this is the per-task second layer behind the container/network boundary.
func (e *Executor) sandboxCommand(parts []string) (string, []string, error) {
	prlimit, err := exec.LookPath("prlimit")
	if err != nil {
		if e.cfg.RequireSandbox {
			return "", nil, errors.New("prlimit is required by WORKER_REQUIRE_SANDBOX")
		}
		return parts[0], parts[1:], nil
	}
	args := []string{
		fmt.Sprintf("--as=%d", e.cfg.MemoryMB<<20),
		fmt.Sprintf("--cpu=%d", e.cfg.CPUSeconds),
		fmt.Sprintf("--nproc=%d", e.cfg.ProcessLimit),
		fmt.Sprintf("--fsize=%d", e.cfg.FileSizeMB<<20),
		fmt.Sprintf("--nofile=%d", e.cfg.OpenFiles),
		"--core=0",
		"--",
	}
	args = append(args, parts...)
	return prlimit, args, nil
}

// runtimeEnvironment deliberately does not inherit the worker environment. The runtime receives only the
// filesystem/Python variables it needs, never worker identity, NATS URLs or control-plane tokens.
func runtimeEnvironment(extra []string) []string {
	keys := []string{
		"PATH", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR", "VIRTUAL_ENV", "PYTHONPATH",
		"PYTHONUNBUFFERED", "PYTHONDONTWRITEBYTECODE", "PYTHONHASHSEED",
		"WORKER_ORCHESTRATOR_GRPC_ADDR", "ORCHESTRATOR_GRPC_ADDR",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_SERVICE_NAME", "OTEL_SDK_DISABLED", "OTEL_TRACES_SAMPLER_ARG",
		"OTEL_RESOURCE_ATTRIBUTES",
	}
	env := []string{"HOME=/tmp", "TMPDIR=/tmp"}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env, extra...)
	for _, key := range []string{"AGENT_MCP_ALLOW_PRIVATE_NETWORKS", "AGENT_MCP_ALLOW_STDIO"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func credentialNames(spec *structpb.Struct) []string {
	if spec == nil {
		return nil
	}
	values := spec.AsMap()
	names := map[string]bool{}
	if model, ok := values["model"].(map[string]any); ok {
		if ref, ok := model["credential_ref"].(string); ok && strings.TrimSpace(ref) != "" {
			names[ref] = true
		}
	}
	if servers, ok := values["mcp_servers"].([]any); ok {
		for _, raw := range servers {
			server, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if ref, ok := server["auth_credential_ref"].(string); ok && strings.TrimSpace(ref) != "" {
				names[ref] = true
			}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
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
				Classification: workerv1.FailureClassification_FAILURE_CLASSIFICATION_RESULT_INVALID,
			}
		}
		return outcome, nil
	case runtimev1.RuntimeOutcome_RUNTIME_OUTCOME_CANCELLED:
		outcome.Failure = failureFromRuntime(response.GetFailure(), "CANCELLED",
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

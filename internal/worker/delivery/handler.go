// Package delivery consumes committed task dispatches and reports fenced results. A broker acknowledgement is
// only sent after the orchestrator has committed the result.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/messaging"
	"github.com/agent-platform/agent-platform/internal/worker/control"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

// ExecutionOutcome is one completed attempt execution. Failure carries the runtime's sanitised classification;
// a non-nil error from the executor itself means the attempt could not run (infrastructure), not that the agent
// failed.
type ExecutionOutcome struct {
	Output         map[string]any
	Failure        *workerv1.TaskFailure
	Metrics        *workerv1.AttemptMetrics
	RuntimeVersion string
	Checkpoint     []byte
}

// Executor runs one accepted attempt. Returning an error means the attempt never produced a result; the handler
// then either reports a classified failure or, on worker shutdown, leaves the message for redelivery.
type Executor func(ctx context.Context, dispatch *contracts.TaskDispatch) (ExecutionOutcome, error)

// Handler processes one task.dispatch message. Returning nil authorises the broker acknowledgement.
type Handler struct {
	Client   *control.Client
	WorkerID string
	Execute  Executor
	Log      *zap.Logger
}

// Handle implements messaging.Handler.
func (h *Handler) Handle(ctx context.Context, msg jetstream.Msg) error {
	parsed, err := contracts.ParseEvent(msg.Data())
	if err != nil {
		// A malformed dispatch can never succeed; preserve it in the DLQ.
		return messaging.Permanent(fmt.Errorf("invalid message: %w", err))
	}
	if parsed.Envelope.MessageType != contracts.EventTaskDispatch {
		h.Log.Debug("ignoring non-dispatch message", zap.String("message_type", parsed.Envelope.MessageType))
		return nil
	}
	payload, ok := parsed.Payload.(*contracts.TaskDispatchPayload)
	if !ok {
		return messaging.Permanent(errors.New("task.dispatch payload has an unexpected type"))
	}
	if parsed.Envelope.WorkerID == nil || *parsed.Envelope.WorkerID != h.WorkerID {
		return messaging.Permanent(fmt.Errorf("dispatch targets worker %v, not %s",
			parsed.Envelope.WorkerID, h.WorkerID))
	}
	if parsed.Envelope.TenantID == nil || parsed.Envelope.ProjectID == nil ||
		parsed.Envelope.ExecutionID == nil || parsed.Envelope.TaskID == nil || parsed.Envelope.AttemptID == nil {
		return messaging.Permanent(errors.New("dispatch envelope is missing tenant context"))
	}
	dispatch := &contracts.TaskDispatch{
		SchemaVersion:  parsed.Envelope.SchemaVersion,
		OrgID:          *parsed.Envelope.TenantID,
		ProjectID:      *parsed.Envelope.ProjectID,
		ExecutionID:    *parsed.Envelope.ExecutionID,
		TaskID:         *parsed.Envelope.TaskID,
		AttemptID:      *parsed.Envelope.AttemptID,
		LeaseID:        payload.LeaseID,
		AttemptNumber:  payload.AttemptNumber,
		NodeID:         payload.NodeID,
		NodeType:       payload.NodeType,
		AgentVersionID: payload.AgentVersionID,
		ToolVersionID:  payload.ToolVersionID,
		SpecDigest:     payload.SpecDigest,
		Deadline:       payload.Deadline,
		LeaseExpiresAt: payload.LeaseExpiresAt,
	}
	if err := dispatch.Validate(); err != nil {
		return messaging.Permanent(fmt.Errorf("invalid dispatch: %w", err))
	}

	deadline, err := time.Parse(time.RFC3339Nano, dispatch.Deadline)
	if err != nil {
		return messaging.Permanent(fmt.Errorf("invalid deadline: %w", err))
	}
	deadlineCtx, deadlineCancel := context.WithDeadline(ctx, deadline)
	defer deadlineCancel()
	runCtx, cancelRun := context.WithCancel(deadlineCtx)
	defer cancelRun()

	cancelCh, unsubscribe := h.Client.Cancellation(dispatch.TaskID, dispatch.AttemptID, dispatch.LeaseID)
	defer unsubscribe()
	var cancelledByOrchestrator atomic.Bool
	go func() {
		select {
		case <-cancelCh:
			cancelledByOrchestrator.Store(true)
			h.Log.Warn("cancelling attempt on orchestrator request",
				zap.String("task_id", dispatch.TaskID),
				zap.String("attempt_id", dispatch.AttemptID),
			)
			cancelRun()
		case <-runCtx.Done():
		}
	}()

	if h.Client.Draining() {
		if err := h.Client.Rejected(ctx, dispatch.TaskID, dispatch.AttemptID, dispatch.LeaseID,
			workerv1.TaskRejectionReason_TASK_REJECTION_REASON_SHUTTING_DOWN,
			"worker is draining"); err != nil {
			return fmt.Errorf("reject dispatch while draining: %w", err)
		}
		return nil
	}

	// Extend the broker ack deadline while the attempt runs. Leases are renewed by the control heartbeat; this
	// prevents a long task from being redelivered mid-execution with the same lease.
	progressDone := make(chan struct{})
	progressStopped := make(chan struct{})
	go func() {
		defer close(progressStopped)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-progressDone:
				return
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if err := msg.InProgress(); err != nil {
					h.Log.Warn("could not extend message ack deadline",
						zap.String("task_id", dispatch.TaskID), zap.Error(err))
				}
			}
		}
	}()
	defer func() {
		close(progressDone)
		<-progressStopped
	}()

	if err := h.Client.Started(runCtx, dispatch.TaskID, dispatch.AttemptID, dispatch.LeaseID); err != nil {
		// No live control plane means no execution: redeliver rather than run unmanaged work.
		return fmt.Errorf("accept task: %w", err)
	}
	defer h.Client.Done(dispatch.TaskID)
	h.Log.Info("task started",
		zap.String("task_id", dispatch.TaskID),
		zap.String("attempt_id", dispatch.AttemptID),
		zap.String("execution_id", dispatch.ExecutionID),
		zap.String("node_id", dispatch.NodeID),
	)

	startedAt := time.Now().UTC()
	outcome, executeErr := h.Execute(runCtx, dispatch)
	finishedAt := time.Now().UTC()
	duration := finishedAt.Sub(startedAt).Milliseconds()

	// A cancellation requested by the orchestrator is durable and must be reported, not left for redelivery. Any
	// other cancelled context is the worker shutting down: leave the message to the lease/recovery mechanism.
	if errors.Is(runCtx.Err(), context.Canceled) && executeErr != nil && !cancelledByOrchestrator.Load() {
		return fmt.Errorf("attempt interrupted: %w", executeErr)
	}

	request := &workerv1.ReportTaskResultRequest{
		TaskId:        dispatch.TaskID,
		AttemptId:     dispatch.AttemptID,
		LeaseId:       dispatch.LeaseID,
		AttemptNumber: int32(dispatch.AttemptNumber), //nolint:gosec // bounded by max_attempts.
		StartedAt:     timestamppb.New(startedAt),
		FinishedAt:    timestamppb.New(finishedAt),
	}
	if outcome.Metrics != nil {
		request.Metrics = outcome.Metrics
	} else {
		request.Metrics = &workerv1.AttemptMetrics{DurationMs: duration}
	}
	if outcome.RuntimeVersion != "" {
		request.RuntimeVersion = outcome.RuntimeVersion
	}

	if cancelledByOrchestrator.Load() {
		request.Outcome = workerv1.TaskOutcome_TASK_OUTCOME_CANCELLED
		request.Failure = &workerv1.TaskFailure{
			Code: "CANCELLED", Message: "execution cancellation requested",
			Classification:  workerv1.FailureClassification_FAILURE_CLASSIFICATION_CANCELLED,
			EffectUncertain: outcome.Failure != nil && outcome.Failure.GetEffectUncertain(),
		}
	} else if executeErr != nil {
		request.Outcome = workerv1.TaskOutcome_TASK_OUTCOME_FAILED
		request.Failure = failureFor(runCtx, executeErr)
	} else if outcome.Failure != nil {
		request.Outcome = workerv1.TaskOutcome_TASK_OUTCOME_FAILED
		request.Failure = outcome.Failure
		h.Log.Warn("agent attempt failed",
			zap.String("task_id", dispatch.TaskID),
			zap.String("attempt_id", dispatch.AttemptID),
			zap.String("code", outcome.Failure.GetCode()),
			zap.String("classification", outcome.Failure.GetClassification().String()),
			zap.String("message", outcome.Failure.GetMessage()),
		)
	} else {
		output := outcome.Output
		if output == nil {
			output = map[string]any{}
		}
		structOutput, err := structpb.NewStruct(output)
		if err != nil {
			request.Outcome = workerv1.TaskOutcome_TASK_OUTCOME_FAILED
			request.Failure = &workerv1.TaskFailure{
				Code: "RESULT_INVALID", Message: "output is not a JSON object",
				Classification: workerv1.FailureClassification_FAILURE_CLASSIFICATION_PERMANENT,
			}
		} else {
			request.Outcome = workerv1.TaskOutcome_TASK_OUTCOME_SUCCEEDED
			request.Output = structOutput
		}
	}
	if len(outcome.Checkpoint) > 0 {
		// Durable checkpoint upload is deferred with the object-store integration; log that state exists so
		// the gap is visible in operations.
		h.Log.Debug("attempt produced checkpoint state; durable upload is deferred",
			zap.String("task_id", dispatch.TaskID),
			zap.Int("checkpoint_bytes", len(outcome.Checkpoint)),
		)
	}

	// Reporting must still work after the execution deadline cancels runCtx; the result is the proof of the timeout.
	reportCtx, reportCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer reportCancel()
	response, err := h.Client.ReportTaskResult(reportCtx, request)
	if err != nil {
		return fmt.Errorf("report result: %w", err)
	}
	switch response.GetStatus() {
	case workerv1.ResultStatus_RESULT_STATUS_ACCEPTED:
		h.Log.Info("task result committed",
			zap.String("task_id", dispatch.TaskID),
			zap.String("attempt_id", dispatch.AttemptID),
			zap.String("execution_id", dispatch.ExecutionID),
			zap.Int64("duration_ms", duration),
		)
		return nil
	case workerv1.ResultStatus_RESULT_STATUS_ALREADY_FINISHED,
		workerv1.ResultStatus_RESULT_STATUS_STALE_LEASE:
		// The durable state already moved on; acknowledging without repeating the operation is the correct recovery.
		h.Log.Info("task result superseded; acknowledging duplicate",
			zap.String("task_id", dispatch.TaskID),
			zap.String("attempt_id", dispatch.AttemptID),
			zap.String("status", response.GetStatus().String()),
			zap.String("message", response.GetMessage()),
		)
		return nil
	case workerv1.ResultStatus_RESULT_STATUS_VALIDATION_FAILED:
		return messaging.Permanent(fmt.Errorf("result rejected: %s", response.GetMessage()))
	default:
		return fmt.Errorf("result was not committed: %s", response.GetMessage())
	}
}

func failureFor(ctx context.Context, err error) *workerv1.TaskFailure {
	classification := workerv1.FailureClassification_FAILURE_CLASSIFICATION_TRANSIENT
	code := "TASK_EXECUTION_FAILED"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		classification = workerv1.FailureClassification_FAILURE_CLASSIFICATION_TIMEOUT
		code = "TASK_TIMEOUT"
	}
	message := err.Error()
	if len(message) > 4096 {
		message = message[:4096]
	}
	return &workerv1.TaskFailure{Code: code, Message: message, Classification: classification}
}

// Echo is the deterministic offline executor: it proves delivery, fencing and acknowledgement without a runtime
// process. The Python runtime executor replaces it in deployment.
func Echo(_ context.Context, dispatch *contracts.TaskDispatch) (ExecutionOutcome, error) {
	return ExecutionOutcome{Output: map[string]any{
		"status":         "succeeded",
		"node_id":        dispatch.NodeID,
		"attempt_number": dispatch.AttemptNumber,
	}}, nil
}

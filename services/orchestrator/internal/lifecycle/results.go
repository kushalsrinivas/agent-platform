package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/internal/platform/metrics"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

// ResultStatus is the fenced outcome of a worker result report.
type ResultStatus string

// Result report statuses.
const (
	ResultAccepted         ResultStatus = "accepted"
	ResultAlreadyFinished  ResultStatus = "already_finished"
	ResultStaleLease       ResultStatus = "stale_lease"
	ResultValidationFailed ResultStatus = "validation_failed"
	ResultRetryLater       ResultStatus = "retry_later"
)

// ResultResponse tells the worker whether it may acknowledge the broker message.
type ResultResponse struct {
	Status    ResultStatus
	TaskState string
	Message   string
}

// ErrStaleLease means the caller no longer owns the attempt.
var ErrStaleLease = errors.New("lifecycle: stale lease")

// AcceptTask applies T3: assigned -> running, the execution's first acceptance moves it pending -> running.
// It is idempotent for a duplicate acceptance with the same lease.
func (s *Service) AcceptTask(ctx context.Context, workerID, taskID, attemptID, leaseID string) error {
	if workerID == "" || taskID == "" || attemptID == "" || leaseID == "" {
		return errors.New("lifecycle: worker, task, attempt and lease are required")
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecutionForTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		t, err := s.lockTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		a, err := s.lockAttempt(ctx, tx, attemptID)
		if err != nil {
			return err
		}
		if a.TaskID != t.ID || a.ExecutionID != e.ID || a.WorkerID != workerID || a.LeaseID != leaseID {
			return ErrStaleLease
		}
		if t.OwnerWorkerID == nil || *t.OwnerWorkerID != workerID || t.LeaseID == nil || *t.LeaseID != leaseID {
			return ErrStaleLease
		}
		if t.State == "running" {
			return nil
		}
		if t.State != "assigned" {
			return ErrStaleLease
		}
		if t.LeaseExpired {
			return ErrStaleLease
		}

		if _, err := tx.Exec(ctx, `
			UPDATE execution.task_attempts
			SET accepted_at = COALESCE(accepted_at, now()), started_at = COALESCE(started_at, now())
			WHERE id = $1`, a.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE execution.execution_tasks
			SET state = 'running', state_version = state_version + 1,
			    started_at = COALESCE(started_at, now()), updated_at = now()
			WHERE id = $1 AND state = 'assigned'`, t.ID); err != nil {
			return err
		}

		from, to := "assigned", "running"
		taskIDValue, nodeID, attemptIDValue, version := t.ID, t.NodeID, a.ID, t.StateVersion+1
		if err := s.appendEvent(ctx, tx, e, eventSpec{
			MessageType: contracts.EventTaskStateChanged,
			EntityType:  "task",
			EntityID:    taskIDValue,
			From:        &from,
			To:          &to,
			ActorType:   "worker",
			ActorID:     workerID,
			Subject:     taskSubject(e, taskIDValue, "state_changed"),
			TaskID:      &taskIDValue,
			AttemptID:   &attemptIDValue,
			WorkerID:    &workerID,
			Payload: func(seq int64) any {
				return contracts.TaskStateChangedPayload{
					Seq: seq, TaskID: taskIDValue, NodeID: nodeID,
					From: from, To: to, StateVersion: version, AttemptID: &attemptIDValue,
				}
			},
		}); err != nil {
			return err
		}
		if e.State == "pending" {
			return s.transitionExecution(ctx, tx, e, "running", "worker_accepted", nil)
		}
		return nil
	})
}

// RejectTask applies T4/T5 when a worker declines a dispatch before executing it.
func (s *Service) RejectTask(ctx context.Context, workerID, taskID, attemptID, leaseID, reason, message string) error {
	if workerID == "" || taskID == "" || attemptID == "" || leaseID == "" {
		return errors.New("lifecycle: worker, task, attempt and lease are required")
	}
	if reason == "" {
		reason = "WORKER_REJECTED"
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecutionForTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		t, err := s.lockTask(ctx, tx, taskID)
		if err != nil {
			return err
		}
		a, err := s.lockAttempt(ctx, tx, attemptID)
		if err != nil {
			return err
		}
		if a.Outcome != nil {
			return nil
		}
		if a.TaskID != t.ID || a.ExecutionID != e.ID || a.WorkerID != workerID || a.LeaseID != leaseID ||
			t.State != "assigned" || t.OwnerWorkerID == nil || *t.OwnerWorkerID != workerID ||
			t.LeaseID == nil || *t.LeaseID != leaseID || t.LeaseExpired {
			return ErrStaleLease
		}

		if _, err := tx.Exec(ctx, `
			UPDATE execution.task_attempts
			SET outcome = 'failed', finished_at = now(), error_code = $2, error_message = $3
			WHERE id = $1`, a.ID, reason, message); err != nil {
			return err
		}
		if err := s.releaseWorkerSlot(ctx, tx, workerID); err != nil {
			return err
		}

		policy := taskRetryPolicy(t)
		// A pre-execution rejection is not a consumed attempt: the worker never ran the node. Only permanent
		// protocol/config rejections end the task.
		transientRejection := reason == "WORKER_BUSY" || reason == "WORKER_SHUTTING_DOWN" ||
			reason == "ATTEMPT_NOT_OPEN"
		retry := (transientRejection || t.AttemptCount < policy.MaxAttempts) &&
			reason != "INVALID_PAYLOAD" && reason != "UNSUPPORTED_RUNTIME" &&
			e.State != "failing" && e.State != "cancel_requested"
		var nextAttempt *time.Time
		if retry {
			metrics.RetryTotal.Inc()
			next := time.Now().UTC().Add(retryDelay(policy, t.AttemptCount))
			nextAttempt = &next
			if _, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = 'pending', state_version = state_version + 1, owner_worker_id = NULL,
				    lease_id = NULL, lease_expires_at = NULL, next_attempt_at = $2,
				    attempt_count = CASE WHEN $3 THEN GREATEST(attempt_count - 1, 0) ELSE attempt_count END,
				    updated_at = now()
				WHERE id = $1`, t.ID, nextAttempt, transientRejection); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = 'failed', state_version = state_version + 1, ended_at = now(),
				    owner_worker_id = NULL, lease_id = NULL, lease_expires_at = NULL,
				    last_failure_code = $2, dead_lettered_at = COALESCE(dead_lettered_at, now()),
				    updated_at = now()
				WHERE id = $1`, t.ID, reason); err != nil {
				return err
			}
		}

		to := "pending"
		if !retry {
			to = "failed"
		}
		if err := s.appendAttemptFinished(ctx, tx, e, t, a, "failed", reason); err != nil {
			return err
		}
		from := "assigned"
		taskIDValue, nodeID, attemptIDValue, version := t.ID, t.NodeID, a.ID, t.StateVersion+1
		if err := s.appendEvent(ctx, tx, e, eventSpec{
			MessageType: contracts.EventTaskStateChanged,
			EntityType:  "task",
			EntityID:    taskIDValue,
			From:        &from,
			To:          &to,
			ActorType:   "worker",
			ActorID:     workerID,
			Subject:     taskSubject(e, taskIDValue, "state_changed"),
			TaskID:      &taskIDValue,
			AttemptID:   &attemptIDValue,
			WorkerID:    &workerID,
			Payload: func(seq int64) any {
				return contracts.TaskStateChangedPayload{
					Seq: seq, TaskID: taskIDValue, NodeID: nodeID,
					From: from, To: to, StateVersion: version, AttemptID: &attemptIDValue, Reason: &reason,
				}
			},
		}); err != nil {
			return err
		}
		if !retry {
			return s.failFast(ctx, tx, e, reason)
		}
		return nil
	})
}

// ReportTaskResult applies T6/T7/T8 for one fenced attempt. The transaction commits before this returns, which is
// what authorises the worker to acknowledge the broker message.
func (s *Service) ReportTaskResult(ctx context.Context, workerID string,
	req *workerv1.ReportTaskResultRequest) (ResultResponse, error) {
	if workerID == "" {
		return ResultResponse{Status: ResultValidationFailed, Message: "worker identity is required"}, nil
	}
	result, err := resultContract(req)
	if err != nil {
		return ResultResponse{Status: ResultValidationFailed, Message: err.Error()}, nil
	}

	response := ResultResponse{Status: ResultRetryLater, Message: "result was not committed"}
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		e, err := s.lockExecutionForTask(ctx, tx, req.TaskId)
		if err != nil {
			if isNoRows(err) {
				response = ResultResponse{Status: ResultStaleLease, Message: "unknown task"}
				return nil
			}
			return err
		}
		t, err := s.lockTask(ctx, tx, req.TaskId)
		if err != nil {
			if isNoRows(err) {
				response = ResultResponse{Status: ResultStaleLease, Message: "unknown task"}
				return nil
			}
			return err
		}
		a, err := s.lockAttempt(ctx, tx, req.AttemptId)
		if err != nil {
			if isNoRows(err) {
				response = ResultResponse{Status: ResultStaleLease, Message: "unknown attempt"}
				return nil
			}
			return err
		}
		response.TaskState = t.State

		if a.TaskID != t.ID || a.ExecutionID != e.ID {
			response = ResultResponse{Status: ResultValidationFailed, Message: "attempt does not belong to task"}
			return nil
		}
		if req.AttemptNumber != 0 && int(req.AttemptNumber) != a.AttemptNumber {
			response = ResultResponse{Status: ResultValidationFailed, Message: "attempt generation does not match"}
			return nil
		}
		if a.Outcome != nil {
			if a.LeaseID == req.LeaseId && a.WorkerID == workerID {
				if *a.Outcome == "lease_expired" || *a.Outcome == "incomplete" {
					// The orchestrator reclaimed this attempt; a late success is stale and must not be applied.
					response = ResultResponse{Status: ResultStaleLease, Message: "attempt lease was already reclaimed"}
					return nil
				}
				response = ResultResponse{
					Status: ResultAlreadyFinished, TaskState: t.State,
					Message: "attempt was already finalised; safe to acknowledge",
				}
				return nil
			}
			response = ResultResponse{Status: ResultStaleLease, Message: "attempt is closed under another lease"}
			return nil
		}
		if t.OwnerWorkerID == nil || *t.OwnerWorkerID != workerID ||
			t.LeaseID == nil || *t.LeaseID != req.LeaseId {
			response = ResultResponse{Status: ResultStaleLease, Message: "lease no longer owns the task"}
			return nil
		}
		if t.State != "assigned" && t.State != "running" {
			response = ResultResponse{Status: ResultStaleLease, Message: "task is not active"}
			return nil
		}
		if t.LeaseExpired {
			response = ResultResponse{Status: ResultStaleLease, Message: "lease expired"}
			return nil
		}

		// A fast worker can report before the stream acceptance is processed; apply T3 inline and continue.
		if t.State == "assigned" {
			if _, err := tx.Exec(ctx, `
				UPDATE execution.task_attempts
				SET accepted_at = COALESCE(accepted_at, now()), started_at = COALESCE(started_at, now())
				WHERE id = $1`, a.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = 'running', state_version = state_version + 1,
				    started_at = COALESCE(started_at, now()), updated_at = now()
				WHERE id = $1 AND state = 'assigned'`, t.ID); err != nil {
				return err
			}
			from, to := "assigned", "running"
			taskIDValue, nodeID, attemptIDValue, version := t.ID, t.NodeID, a.ID, t.StateVersion+1
			if err := s.appendEvent(ctx, tx, e, eventSpec{
				MessageType: contracts.EventTaskStateChanged,
				EntityType:  "task",
				EntityID:    taskIDValue,
				From:        &from,
				To:          &to,
				ActorType:   "worker",
				ActorID:     workerID,
				Subject:     taskSubject(e, taskIDValue, "state_changed"),
				TaskID:      &taskIDValue,
				AttemptID:   &attemptIDValue,
				WorkerID:    &workerID,
				Payload: func(seq int64) any {
					return contracts.TaskStateChangedPayload{
						Seq: seq, TaskID: taskIDValue, NodeID: nodeID,
						From: from, To: to, StateVersion: version, AttemptID: &attemptIDValue,
					}
				},
			}); err != nil {
				return err
			}
			if e.State == "pending" {
				if err := s.transitionExecution(ctx, tx, e, "running", "worker_accepted", nil); err != nil {
					return err
				}
			}
			t.State = "running"
			t.StateVersion++
		}

		// Cancellation is durable and wins a race with a worker that finished just after the request. The work
		// may have happened, but the task must not be recorded as a success of a cancelled execution.
		if e.State == "cancel_requested" && result.Outcome != contracts.TaskOutcomeCancelled {
			effectUncertain := result.Failure != nil && result.Failure.EffectUncertain
			result.Outcome = contracts.TaskOutcomeCancelled
			result.Failure = &contracts.TaskFailure{
				Code: "CANCELLED", Message: "execution cancellation requested",
				Classification: contracts.FailureCancelled, EffectUncertain: effectUncertain,
			}
		}
		if err := s.closeAttemptWithResult(ctx, tx, t, a, result, req); err != nil {
			return err
		}
		if err := s.releaseWorkerSlot(ctx, tx, workerID); err != nil {
			return err
		}

		from := t.State
		to := "succeeded"
		var nextAttempt *time.Time
		retry := false
		failureCode := ""
		retryPolicy := taskRetryPolicy(t)
		if result.Outcome != contracts.TaskOutcomeSucceeded {
			failureCode = "TASK_FAILED"
			if result.Failure != nil && result.Failure.Code != "" {
				failureCode = result.Failure.Code
			}
			if result.Outcome == contracts.TaskOutcomeCancelled {
				to = "cancelled"
			} else if result.Failure != nil && t.AttemptCount < retryPolicy.MaxAttempts &&
				retryableFailure(retryPolicy, t, result.Failure) &&
				e.State != "failing" && e.State != "cancel_requested" {
				to = "pending"
				retry = true
				next := time.Now().UTC().Add(retryDelay(retryPolicy, t.AttemptCount))
				nextAttempt = &next
			} else {
				to = "failed"
			}
		}

		switch to {
		case "succeeded":
			_, err = tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = 'succeeded', state_version = state_version + 1, ended_at = now(),
				    owner_worker_id = NULL, lease_id = NULL, lease_expires_at = NULL, updated_at = now()
				WHERE id = $1`, t.ID)
		case "pending":
			_, err = tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = 'pending', state_version = state_version + 1, owner_worker_id = NULL,
				    lease_id = NULL, lease_expires_at = NULL, next_attempt_at = $2, updated_at = now()
				WHERE id = $1`, t.ID, nextAttempt)
		default:
			var failureCodePtr *string
			if failureCode != "" {
				failureCodePtr = &failureCode
			}
			_, err = tx.Exec(ctx, `
				UPDATE execution.execution_tasks
				SET state = $2, state_version = state_version + 1, ended_at = now(),
				    owner_worker_id = NULL, lease_id = NULL, lease_expires_at = NULL,
				    last_failure_code = COALESCE($3, last_failure_code),
				    dead_lettered_at = CASE WHEN $2 = 'failed' THEN COALESCE(dead_lettered_at, now()) ELSE dead_lettered_at END,
				    updated_at = now()
				WHERE id = $1`, t.ID, to, failureCodePtr)
		}
		if err != nil {
			return err
		}
		if retry && failureCode != "" {
			if _, err := tx.Exec(ctx, `
				UPDATE execution.execution_tasks SET last_failure_code = $2 WHERE id = $1`, t.ID, failureCode); err != nil {
				return err
			}
		}

		attemptOutcome := "succeeded"
		if to != "succeeded" {
			attemptOutcome = "failed"
			if to == "cancelled" {
				attemptOutcome = "cancelled"
			}
		}
		if err := s.appendAttemptFinished(ctx, tx, e, t, a, attemptOutcome, failureCode); err != nil {
			return err
		}
		reason := failureCode
		if reason == "" {
			reason = "result_succeeded"
		}
		taskIDValue, nodeID, attemptIDValue, version := t.ID, t.NodeID, a.ID, t.StateVersion+1
		if err := s.appendEvent(ctx, tx, e, eventSpec{
			MessageType: contracts.EventTaskStateChanged,
			EntityType:  "task",
			EntityID:    taskIDValue,
			From:        &from,
			To:          &to,
			ActorType:   "worker",
			ActorID:     workerID,
			Subject:     taskSubject(e, taskIDValue, "state_changed"),
			TaskID:      &taskIDValue,
			AttemptID:   &attemptIDValue,
			WorkerID:    &workerID,
			Payload: func(seq int64) any {
				return contracts.TaskStateChangedPayload{
					Seq: seq, TaskID: taskIDValue, NodeID: nodeID,
					From: from, To: to, StateVersion: version,
					AttemptID: &attemptIDValue, Reason: &reason,
				}
			},
		}); err != nil {
			return err
		}

		if to == "failed" || to == "cancelled" {
			if err := s.failFast(ctx, tx, e, failureCode); err != nil {
				return err
			}
		} else if !retry {
			if err := s.evaluateCompletion(ctx, tx, e); err != nil {
				return err
			}
		}

		response = ResultResponse{Status: ResultAccepted, TaskState: to, Message: "result committed"}
		return nil
	})
	if err != nil {
		return ResultResponse{}, err
	}
	return response, nil
}

func (s *Service) closeAttemptWithResult(ctx context.Context, tx pgx.Tx, t *task, a *attempt,
	result *contracts.TaskResult, req *workerv1.ReportTaskResultRequest) error {
	attemptOutcome := "succeeded"
	switch result.Outcome {
	case contracts.TaskOutcomeFailed:
		attemptOutcome = "failed"
	case contracts.TaskOutcomeCancelled:
		attemptOutcome = "cancelled"
	}
	var errorCode, errorMessage *string
	effectUncertain := false
	if result.Failure != nil {
		code, message := result.Failure.Code, result.Failure.Message
		errorCode, errorMessage = &code, &message
		effectUncertain = result.Failure.EffectUncertain
	}
	var resultInline any
	if result.Output != nil {
		resultInline = *result.Output
	}
	metricValues := map[string]any{}
	if result.Metrics != nil {
		if result.Metrics.DurationMS != nil {
			metricValues["duration_ms"] = *result.Metrics.DurationMS
		}
		if result.Metrics.TokensIn != nil {
			metricValues["tokens_in"] = *result.Metrics.TokensIn
		}
		if result.Metrics.TokensOut != nil {
			metricValues["tokens_out"] = *result.Metrics.TokensOut
		}
		if result.Metrics.Steps != nil {
			metricValues["steps"] = *result.Metrics.Steps
		}
		if result.Metrics.Extra != nil {
			metricValues["extra"] = *result.Metrics.Extra
		}
	}
	var runtimeVersion *string
	if req.RuntimeVersion != "" {
		value := req.RuntimeVersion
		runtimeVersion = &value
	}
	_, err := tx.Exec(ctx, `
		UPDATE execution.task_attempts
		SET outcome = $2, finished_at = now(), started_at = COALESCE(started_at, now()),
		    error_code = $3, error_message = $4, result_inline = $5, metrics = $6,
		    runtime_version = $7, effect_uncertain = $8, outcome_unknown = false
		WHERE id = $1`,
		a.ID, attemptOutcome, errorCode, errorMessage, resultInline, metricValues, runtimeVersion, effectUncertain)
	if err != nil {
		return err
	}
	provider, model := "unknown", "unknown"
	if t.Provider != nil && *t.Provider != "" {
		provider = *t.Provider
	}
	if t.Model != nil && *t.Model != "" {
		model = *t.Model
	}
	_ = model
	if result.Metrics != nil {
		if result.Metrics.TokensIn != nil {
			metrics.AITokensTotal.WithLabelValues(provider, "input").Add(float64(*result.Metrics.TokensIn))
		}
		if result.Metrics.TokensOut != nil {
			metrics.AITokensTotal.WithLabelValues(provider, "output").Add(float64(*result.Metrics.TokensOut))
		}
		if result.Metrics.DurationMS != nil && *result.Metrics.DurationMS > 0 {
			metrics.ModelLatency.WithLabelValues(provider).Observe(float64(*result.Metrics.DurationMS) / 1000)
		}
	}
	if result.Failure != nil {
		metrics.ProviderErrors.WithLabelValues(provider, result.Failure.Code).Inc()
	}
	if t.NodeType == "tool" {
		tool := "tool"
		if t.ToolVersionID != nil && *t.ToolVersionID != "" {
			tool = *t.ToolVersionID
		}
		metrics.ToolCallsTotal.WithLabelValues(tool).Inc()
	}
	return nil
}

func (s *Service) appendAttemptFinished(ctx context.Context, tx pgx.Tx, e *execution, t *task, a *attempt,
	outcome, errorCode string) error {
	var codePtr *string
	if errorCode != "" {
		code := errorCode
		codePtr = &code
	}
	attemptID := a.ID
	workerID := a.WorkerID
	number := a.AttemptNumber
	return s.appendEvent(ctx, tx, e, eventSpec{
		MessageType: contracts.EventTaskAttemptFinished,
		EntityType:  "attempt",
		EntityID:    attemptID,
		From:        ptr("running"),
		To:          ptr(outcome),
		ActorType:   "worker",
		ActorID:     workerID,
		Subject:     taskSubject(e, t.ID, "attempt_finished"),
		TaskID:      ptr(t.ID),
		AttemptID:   &attemptID,
		WorkerID:    &workerID,
		Payload: func(seq int64) any {
			return contracts.TaskAttemptFinishedPayload{
				Seq: seq, AttemptID: attemptID, AttemptNumber: number, WorkerID: workerID,
				Outcome: outcome, ErrorCode: codePtr,
			}
		},
	})
}

func resultContract(req *workerv1.ReportTaskResultRequest) (*contracts.TaskResult, error) {
	if req == nil {
		return nil, errors.New("result is required")
	}
	result := &contracts.TaskResult{
		SchemaVersion: 1,
		TaskID:        req.TaskId,
		AttemptID:     req.AttemptId,
		LeaseID:       req.LeaseId,
	}
	switch req.Outcome {
	case workerv1.TaskOutcome_TASK_OUTCOME_SUCCEEDED:
		result.Outcome = contracts.TaskOutcomeSucceeded
	case workerv1.TaskOutcome_TASK_OUTCOME_FAILED:
		result.Outcome = contracts.TaskOutcomeFailed
	case workerv1.TaskOutcome_TASK_OUTCOME_CANCELLED:
		result.Outcome = contracts.TaskOutcomeCancelled
	default:
		return nil, errors.New("outcome must be succeeded, failed or cancelled")
	}
	if req.Output != nil {
		output := req.Output.AsMap()
		result.Output = &output
	}
	if req.Failure != nil {
		classification, err := failureClassification(req.Failure.Classification)
		if err != nil {
			return nil, err
		}
		result.Failure = &contracts.TaskFailure{
			Code:            req.Failure.Code,
			Message:         req.Failure.Message,
			Classification:  classification,
			EffectUncertain: req.Failure.EffectUncertain,
		}
	}
	if req.Metrics != nil {
		metrics := &contracts.TaskMetrics{}
		if req.Metrics.DurationMs > 0 {
			value := req.Metrics.DurationMs
			metrics.DurationMS = &value
		}
		if req.Metrics.TokensIn > 0 {
			value := req.Metrics.TokensIn
			metrics.TokensIn = &value
		}
		if req.Metrics.TokensOut > 0 {
			value := req.Metrics.TokensOut
			metrics.TokensOut = &value
		}
		if req.Metrics.Steps > 0 {
			value := int(req.Metrics.Steps)
			metrics.Steps = &value
		}
		if req.Metrics.Extra != nil {
			extra := req.Metrics.Extra.AsMap()
			metrics.Extra = &extra
		}
		result.Metrics = metrics
	}
	if req.RuntimeVersion != "" {
		value := req.RuntimeVersion
		result.RuntimeVersion = &value
	}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	// The canonical contract must also survive JSON encoding; the output is stored as jsonb.
	if result.Output != nil {
		raw, err := json.Marshal(*result.Output)
		if err != nil || len(raw) > 65536 {
			return nil, fmt.Errorf("output must be a JSON object of at most 64 KiB")
		}
	}
	return result, nil
}

func failureClassification(value workerv1.FailureClassification) (contracts.FailureClassification, error) {
	switch value {
	case workerv1.FailureClassification_FAILURE_CLASSIFICATION_TRANSIENT:
		return contracts.FailureTransient, nil
	case workerv1.FailureClassification_FAILURE_CLASSIFICATION_PERMANENT:
		return contracts.FailurePermanent, nil
	case workerv1.FailureClassification_FAILURE_CLASSIFICATION_TIMEOUT:
		return contracts.FailureTimeout, nil
	case workerv1.FailureClassification_FAILURE_CLASSIFICATION_CANCELLED:
		return contracts.FailureCancelled, nil
	case workerv1.FailureClassification_FAILURE_CLASSIFICATION_WORKER_ERROR:
		return contracts.FailureWorkerError, nil
	case workerv1.FailureClassification_FAILURE_CLASSIFICATION_RESULT_INVALID:
		return contracts.FailureResultInvalid, nil
	default:
		return contracts.FailureWorkerError, nil
	}
}

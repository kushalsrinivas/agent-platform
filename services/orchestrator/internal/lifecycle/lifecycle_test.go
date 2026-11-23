package lifecycle

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	workerv1 "github.com/agent-platform/agent-platform/gen/worker/v1"
	"github.com/agent-platform/agent-platform/packages/contracts"
)

func TestValidateWorkerID(t *testing.T) {
	valid := []string{"wrk_worker_01", "wrk_test_001", "wrk_a2345678"}
	for _, value := range valid {
		if err := validateWorkerID(value); err != nil {
			t.Fatalf("%q should be valid: %v", value, err)
		}
	}
	invalid := []string{"", "worker-01", "wrk_short", "wrk_bad.id", "wrk_has space", "xrk_worker_01"}
	for _, value := range invalid {
		if err := validateWorkerID(value); err == nil {
			t.Fatalf("%q should be rejected", value)
		}
	}
}

func TestRetryBackoffBounds(t *testing.T) {
	for attempt := 1; attempt <= 8; attempt++ {
		delay := retryBackoff(attempt)
		if delay < 500*time.Millisecond || delay > 90*time.Second {
			t.Fatalf("attempt %d backoff %s out of bounds", attempt, delay)
		}
	}
}

func TestTerminalState(t *testing.T) {
	for _, state := range []string{"succeeded", "failed", "cancelled"} {
		if !terminalState(state) {
			t.Fatalf("%s must be terminal", state)
		}
	}
	for _, state := range []string{"pending", "running", "failing", "cancel_requested"} {
		if terminalState(state) {
			t.Fatalf("%s must not be terminal", state)
		}
	}
}

func TestResultContractValidation(t *testing.T) {
	output, err := structpb.NewStruct(map[string]any{"answer": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	base := func() *workerv1.ReportTaskResultRequest {
		return &workerv1.ReportTaskResultRequest{
			TaskId:    "task_01JQ9A0B1C2D3E4F5G6H7J8K",
			AttemptId: "att_01JQ9A0B1C2D3E4F5G6H7J8K",
			LeaseId:   "3f9c2f7a-5f36-4a4b-9d31-6b0e2f1a8c44",
			Outcome:   workerv1.TaskOutcome_TASK_OUTCOME_SUCCEEDED,
			Output:    output,
		}
	}

	if _, err := resultContract(base()); err != nil {
		t.Fatalf("valid success rejected: %v", err)
	}

	missingOutput := base()
	missingOutput.Output = nil
	if _, err := resultContract(missingOutput); err == nil {
		t.Fatal("a success without output must be rejected")
	}

	failed := base()
	failed.Outcome = workerv1.TaskOutcome_TASK_OUTCOME_FAILED
	failed.Output = nil
	failed.Failure = &workerv1.TaskFailure{
		Code: "PROVIDER_ERROR", Message: "upstream failed",
		Classification: workerv1.FailureClassification_FAILURE_CLASSIFICATION_TRANSIENT,
	}
	result, err := resultContract(failed)
	if err != nil {
		t.Fatalf("valid failure rejected: %v", err)
	}
	if result.Failure.Classification != contracts.FailureTransient {
		t.Fatalf("classification = %s, want transient", result.Failure.Classification)
	}

	noFailure := base()
	noFailure.Outcome = workerv1.TaskOutcome_TASK_OUTCOME_FAILED
	noFailure.Output = nil
	if _, err := resultContract(noFailure); err == nil {
		t.Fatal("a failure without failure detail must be rejected")
	}

	unknown := base()
	unknown.Outcome = workerv1.TaskOutcome_TASK_OUTCOME_UNSPECIFIED
	if _, err := resultContract(unknown); err == nil {
		t.Fatal("an unspecified outcome must be rejected")
	}
}

func TestFailureClassificationDefaultsToWorkerError(t *testing.T) {
	value, err := failureClassification(workerv1.FailureClassification_FAILURE_CLASSIFICATION_UNSPECIFIED)
	if err != nil {
		t.Fatal(err)
	}
	if value != contracts.FailureWorkerError {
		t.Fatalf("classification = %s, want worker_error", value)
	}
}

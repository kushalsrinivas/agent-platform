package lifecycle

import (
	"testing"
	"time"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

func TestRetryPolicyDefaultsAndOverrides(t *testing.T) {
	task := &task{MaxAttempts: 2, RetryPolicy: []byte(`{"retry_on":["timeout","result_invalid"],"backoff_base_ms":200,"backoff_max_ms":500}`)}
	policy := taskRetryPolicy(task)
	if policy.MaxAttempts != 2 || policy.BackoffBase != 200*time.Millisecond || policy.BackoffMax != 500*time.Millisecond {
		t.Fatalf("unexpected policy: %+v", policy)
	}
	if len(policy.RetryOn) != 2 || policy.RetryOn[0] != "timeout" {
		t.Fatalf("retry_on = %v", policy.RetryOn)
	}
	delay := retryDelay(policy, 4)
	if delay < 250*time.Millisecond || delay > 500*time.Millisecond {
		t.Fatalf("delay = %v, want 250ms..500ms", delay)
	}
}

func TestRetryableFailureHonoursClassificationAndSideEffects(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 3, RetryOn: []string{"transient", "timeout", "worker_lost"}}
	readOnly := &task{SideEffectClass: "read_only"}
	if !retryableFailure(policy, readOnly, &contracts.TaskFailure{Classification: contracts.FailureTransient}) {
		t.Fatal("transient failure on a read-only task must retry")
	}
	if retryableFailure(policy, readOnly, &contracts.TaskFailure{Classification: contracts.FailurePermanent}) {
		t.Fatal("permanent failure must not retry")
	}
	if retryableFailure(policy, readOnly, &contracts.TaskFailure{Classification: contracts.FailureCancelled}) {
		t.Fatal("cancelled work must not retry")
	}

	nonIdempotent := &task{SideEffectClass: "non_idempotent"}
	uncertain := &contracts.TaskFailure{Classification: contracts.FailureTimeout, EffectUncertain: true}
	if retryableFailure(policy, nonIdempotent, uncertain) {
		t.Fatal("an uncertain non-idempotent side effect must not retry")
	}
	keyed := &task{SideEffectClass: "non_idempotent", IdempotencyKey: ptr("order-42")}
	if !retryableFailure(policy, keyed, uncertain) {
		t.Fatal("an idempotency-keyed side effect may retry")
	}
}

func TestResultInvalidRetryIsConfigurable(t *testing.T) {
	base := RetryPolicy{MaxAttempts: 2, RetryOn: []string{"transient"}}
	failure := &contracts.TaskFailure{Classification: contracts.FailureResultInvalid}
	if retryableFailure(base, &task{SideEffectClass: "read_only"}, failure) {
		t.Fatal("result_invalid must not retry by default")
	}
	configured := RetryPolicy{MaxAttempts: 2, RetryOn: []string{"result_invalid"}}
	if !retryableFailure(configured, &task{SideEffectClass: "read_only"}, failure) {
		t.Fatal("result_invalid retry should work when configured")
	}
}

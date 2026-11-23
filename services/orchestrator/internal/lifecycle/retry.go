package lifecycle

import (
	"encoding/json"
	"math/rand"
	"time"

	"github.com/agent-platform/agent-platform/packages/contracts"
)

// RetryPolicy is the effective, frozen policy of one task.
type RetryPolicy struct {
	MaxAttempts int
	RetryOn     []string
	BackoffBase time.Duration
	BackoffMax  time.Duration
}

var defaultRetryOn = []string{"transient", "timeout", "worker_lost"}

type retryPolicyDocument struct {
	MaxAttempts   *int      `json:"max_attempts"`
	RetryOn       *[]string `json:"retry_on"`
	BackoffBaseMS *int      `json:"backoff_base_ms"`
	BackoffMaxMS  *int      `json:"backoff_max_ms"`
}

// taskRetryPolicy combines the frozen node policy with the materialised attempt budget.
func taskRetryPolicy(t *task) RetryPolicy {
	policy := RetryPolicy{MaxAttempts: t.MaxAttempts, RetryOn: defaultRetryOn, BackoffBase: time.Second, BackoffMax: time.Minute}
	if len(t.RetryPolicy) > 0 {
		var document retryPolicyDocument
		if err := json.Unmarshal(t.RetryPolicy, &document); err == nil {
			if document.RetryOn != nil && len(*document.RetryOn) > 0 {
				policy.RetryOn = append([]string(nil), (*document.RetryOn)...)
			}
			if document.BackoffBaseMS != nil && *document.BackoffBaseMS >= 100 {
				policy.BackoffBase = time.Duration(*document.BackoffBaseMS) * time.Millisecond
			}
			if document.BackoffMaxMS != nil && *document.BackoffMaxMS >= 100 {
				policy.BackoffMax = time.Duration(*document.BackoffMaxMS) * time.Millisecond
			}
		}
	}
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	if policy.MaxAttempts > 10 {
		policy.MaxAttempts = 10
	}
	if policy.BackoffMax < policy.BackoffBase {
		policy.BackoffMax = policy.BackoffBase
	}
	return policy
}

// retryableFailure decides whether a classified failure may start another attempt. A retry is never allowed to
// repeat an external side effect whose outcome is unknown: only read-only or idempotency-keyed work is safe.
func retryableFailure(policy RetryPolicy, t *task, failure *contracts.TaskFailure) bool {
	if failure == nil {
		return false
	}
	if failure.Classification == contracts.FailureCancelled || failure.Classification == contracts.FailurePermanent {
		return false
	}
	if failure.EffectUncertain && !retrySafeAfterUncertainEffect(t) {
		return false
	}
	reason := retryReason(failure.Classification)
	if reason == "" {
		return false
	}
	for _, allowed := range policy.RetryOn {
		if allowed == "any" || allowed == reason {
			return true
		}
	}
	return false
}

// retryableWorkerLoss applies the same policy when a lease expires. A started attempt may have performed work;
// the side-effect class decides whether replaying it is safe.
func retryableWorkerLoss(policy RetryPolicy, t *task, started bool) bool {
	if started && !retrySafeAfterUncertainEffect(t) && t.SideEffectClass == string(contracts.SideEffectNonIdempotent) {
		return false
	}
	for _, allowed := range policy.RetryOn {
		if allowed == "any" || allowed == "worker_lost" {
			return true
		}
	}
	return false
}

func retrySafeAfterUncertainEffect(t *task) bool {
	if t.SideEffectClass == string(contracts.SideEffectReadOnly) {
		return true
	}
	return t.IdempotencyKey != nil && *t.IdempotencyKey != ""
}

func retryReason(classification contracts.FailureClassification) string {
	switch classification {
	case contracts.FailureTransient:
		return "transient"
	case contracts.FailureTimeout:
		return "timeout"
	case contracts.FailureWorkerError:
		return "worker_lost"
	case contracts.FailureResultInvalid:
		return "result_invalid"
	default:
		return ""
	}
}

// retryDelay is exponential with jitter, bounded by the policy. attemptCount is the number of attempts already
// made, so the first backoff applies after attempt 1.
func retryDelay(policy RetryPolicy, attemptCount int) time.Duration {
	base := policy.BackoffBase
	if base <= 0 {
		base = time.Second
	}
	maxDelay := policy.BackoffMax
	if maxDelay <= 0 {
		maxDelay = time.Minute
	}
	delay := base
	for i := 1; i < attemptCount && delay < maxDelay; i++ {
		delay *= 2
	}
	if delay > maxDelay {
		delay = maxDelay
	}
	jitter := 0.5 + 0.5*rand.Float64() //nolint:gosec // retry jitter is not a security decision.
	return time.Duration(float64(delay) * jitter)
}

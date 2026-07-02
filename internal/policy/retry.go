package policy

import "fmt"

// RetryConfig holds the retry rules. The Assignment Engine no longer owns these numbers.
type RetryConfig struct {
	MaxAttempts       int  // total attempts allowed for one Assignment (>=1)
	DisableFlakyRetry bool // when true, transient/flaky failures are NOT retried (zero value = retry)
}

// RetryPolicy decides whether a failed attempt should be retried. Deterministic and stateless: the
// attempt count is supplied by the caller (it is persisted Assignment state, S3), never held here.
type RetryPolicy struct{ cfg RetryConfig }

// RetryDecision is the observable answer.
type RetryDecision struct {
	Retry  bool
	Reason string
}

// Decide reports whether to retry after `attempts` have been made, given whether the failure was
// transient. Non-transient failures are never retried; transient ones are retried while attempts
// remain below the cap.
func (p RetryPolicy) Decide(attempts int, transient bool) RetryDecision {
	if !transient || p.cfg.DisableFlakyRetry {
		return RetryDecision{Retry: false, Reason: "non-transient failure or flaky-retry disabled"}
	}
	if attempts < p.cfg.MaxAttempts {
		return RetryDecision{Retry: true, Reason: fmt.Sprintf("attempt %d < max %d", attempts, p.cfg.MaxAttempts)}
	}
	return RetryDecision{Retry: false, Reason: fmt.Sprintf("attempts exhausted (%d/%d)", attempts, p.cfg.MaxAttempts)}
}

// ShouldRetry adapts RetryPolicy to the Assignment Engine's RetryDecider port (the engine treats a
// recoverable failure as transient at its boundary; unrecoverable failures call fail() directly).
func (p RetryPolicy) ShouldRetry(attempts int) bool {
	return p.Decide(attempts, true).Retry
}

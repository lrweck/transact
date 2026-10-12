// Package transactretry builds retry policies for transact.
// New returns transact.Options, so it plugs into any Manager:
//
//	m := transactpgx.New(pool, transactretry.New(transactretry.WithMaxAttempts(5)))
//	m.Run(ctx, fn) // retries on failure
package transactretry

import (
	"time"

	randv2 "math/rand/v2"

	"github.com/lrweck/transact"
)

// RetryOption configures New.
type RetryOption func(*policy)

type policy struct {
	maxAttempts   int
	perTryTimeout time.Duration
	totalTimeout  time.Duration
	retryIf       func(error) bool
	backoff       Backoff
	jitterMin     time.Duration
	jitterMax     time.Duration
}

// WithMaxAttempts sets the total number of tries, including the first one.
// Values below 1 mean 1, a single try with no retry. Default is 1.
func WithMaxAttempts(n int) RetryOption {
	return func(o *policy) { o.maxAttempts = n }
}

// WithPerTryTimeout bounds each try's begin and commit. It does not bound
// the callback: queries use your context. Zero or less means no limit.
func WithPerTryTimeout(d time.Duration) RetryOption {
	return func(o *policy) { o.perTryTimeout = d }
}

// WithTotalTimeout bounds the whole operation, all tries together.
// Zero or less means no limit.
func WithTotalTimeout(d time.Duration) RetryOption {
	return func(o *policy) { o.totalTimeout = d }
}

// WithRetryIf sets when a failed try is worth retrying. Nil means every
// error is retried. Returning false stops and returns the error.
func WithRetryIf(f func(error) bool) RetryOption {
	return func(o *policy) { o.retryIf = f }
}

// Backoff returns how long to wait before the next try.
// n is the number of failed tries so far, starting at 1.
type Backoff func(failedTries int) time.Duration

// ConstantBackoff waits the same time before every retry.
func ConstantBackoff(d time.Duration) Backoff {
	return func(int) time.Duration { return d }
}

// LinearBackoff waits base, then 2*base, 3*base, and so on.
func LinearBackoff(base time.Duration) Backoff {
	return func(n int) time.Duration { return time.Duration(n) * base }
}

// ExponentialBackoff waits base, then 2*base, 4*base, and so on.
// It saturates instead of overflowing on huge try counts.
func ExponentialBackoff(base time.Duration) Backoff {
	return func(n int) time.Duration {
		d := base
		for i := 1; i < n; i++ {
			d *= 2
			if d < 0 || d > maxBackoff {
				return maxBackoff
			}
		}
		return d
	}
}

const maxBackoff = time.Duration(1<<63 - 1)

// WithBackoff sets the wait strategy between tries. Default is no wait.
func WithBackoff(b Backoff) RetryOption {
	return func(o *policy) { o.backoff = b }
}

// WithJitter randomizes the wait between tries into [min, max].
// Without a backoff strategy the wait is uniform in [min, max].
// With a strategy, the strategy value caps the range: the wait is uniform
// in [min, strategy], clamped to [min, max]. A strategy below min waits min.
// Negative values count as zero. A max below min counts as min.
func WithJitter(min, max time.Duration) RetryOption {
	return func(o *policy) { o.jitterMin, o.jitterMax = min, max }
}

// New builds transact.Options that enable retries with the given knobs.
func New(opts ...RetryOption) transact.Options {
	var p policy
	for _, fn := range opts {
		fn(&p)
	}
	return transact.WithRetry(transact.RetryPolicy{
		MaxAttempts:   p.maxAttempts,
		PerTryTimeout: p.perTryTimeout,
		TotalTimeout:  p.totalTimeout,
		RetryIf:       p.retryIf,
		Wait:          composeWait(p.backoff, p.jitterMin, p.jitterMax),
	})
}

// composeWait merges a strategy with jitter into one wait func.
// It returns nil when there is no wait at all.
func composeWait(b Backoff, lo, hi time.Duration) func(int) time.Duration {
	if hi <= 0 {
		return b
	}
	if lo < 0 {
		lo = 0
	}
	if hi < lo {
		hi = lo
	}
	return func(failed int) time.Duration {
		top := hi
		if b != nil {
			d := b(failed)
			if d < 0 {
				d = 0
			}
			top = min(hi, max(d, lo))
		}
		if top <= lo {
			return lo
		}
		return lo + time.Duration(randv2.Int64N(int64(top-lo)+1))
	}
}

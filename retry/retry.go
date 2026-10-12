// Package transactretry retries failed transactions.
package transactretry

import (
	"context"
	"fmt"
	"time"

	randv2 "math/rand/v2"

	"github.com/lrweck/transact"
)

// Option configures Run.
type Option func(*options)

type options struct {
	maxAttempts   int
	perTryTimeout time.Duration
	totalTimeout  time.Duration
	retryIf       func(error) bool
	backoff       Backoff
	jitterMin     time.Duration
	jitterMax     time.Duration
	txOpts        []transact.Option
}

// WithMaxAttempts sets the total number of tries, including the first one.
// Values below 1 mean 1, a single try with no retry. Default is 1.
func WithMaxAttempts(n int) Option {
	return func(o *options) { o.maxAttempts = n }
}

// WithPerTryTimeout bounds each try. The callback gets a context that
// expires after this time. Zero or less means no limit.
func WithPerTryTimeout(d time.Duration) Option {
	return func(o *options) { o.perTryTimeout = d }
}

// WithTotalTimeout bounds the whole operation, all tries together.
// Zero or less means no limit.
func WithTotalTimeout(d time.Duration) Option {
	return func(o *options) { o.totalTimeout = d }
}

// WithRetryIf sets when a failed try is worth retrying. Nil means every
// error is retried. Returning false stops and returns the error.
func WithRetryIf(f func(error) bool) Option {
	return func(o *options) { o.retryIf = f }
}

// WithTxOptions passes transaction options to every try.
func WithTxOptions(opts ...transact.Option) Option {
	return func(o *options) { o.txOpts = append(o.txOpts, opts...) }
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
func WithBackoff(b Backoff) Option {
	return func(o *options) { o.backoff = b }
}

// WithJitter randomizes the wait between tries into [min, max].
// Without a backoff strategy the wait is uniform in [min, max].
// With a strategy, the strategy value caps the range: the wait is uniform
// in [min, strategy], clamped to [min, max]. A strategy below min waits min.
// Negative values count as zero. A max below min counts as min.
func WithJitter(min, max time.Duration) Option {
	return func(o *options) { o.jitterMin, o.jitterMax = min, max }
}

// Run starts a transaction, calls fn, and commits, retrying on failure.
// Each try runs through transact.Run, so a failed try is rolled back before
// the next one starts.
//
// Fn gets the try context and the transaction. Use that context for queries
// so WithPerTryTimeout can stop a slow try.
//
// A panic in fn rolls back and panics again without retry. When the context
// is done, Run stops and returns the context error. Otherwise it returns the
// last error wrapped with the try count, so errors.Is and errors.As work.
func Run[T transact.Tx](ctx context.Context, b transact.Beginner[T], fn func(context.Context, T) error, opts ...Option) error {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	max := o.maxAttempts
	if max < 1 {
		max = 1
	}
	if o.totalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.totalTimeout)
		defer cancel()
	}
	var err error
	tries := 0
	for tries < max {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if tries == 0 {
				return ctxErr
			}
			return fmt.Errorf("transact: gave up after %d attempt(s): %w", tries, ctxErr)
		}
		tries++
		err = tryOnce(ctx, b, fn, o.perTryTimeout, o.txOpts)
		if err == nil {
			return nil
		}
		if tries == max {
			break
		}
		if o.retryIf != nil && !o.retryIf(err) {
			break
		}
		if werr := sleepCtx(ctx, waitFor(&o, tries)); werr != nil {
			return fmt.Errorf("transact: gave up after %d attempt(s): %w", tries, werr)
		}
	}
	return fmt.Errorf("transact: gave up after %d attempt(s): %w", tries, err)
}

// waitFor computes the wait before the next try. failed is the number of
// failed tries so far, starting at 1.
func waitFor(o *options, failed int) time.Duration {
	var d time.Duration
	if o.backoff != nil {
		d = o.backoff(failed)
	}
	if d < 0 {
		d = 0
	}
	if o.jitterMax <= 0 {
		return d
	}
	lo, hi := o.jitterMin, o.jitterMax
	if lo < 0 {
		lo = 0
	}
	if hi < lo {
		hi = lo
	}
	top := hi
	if o.backoff != nil {
		top = min(hi, max(d, lo))
	}
	if top <= lo {
		return lo
	}
	return lo + time.Duration(randv2.Int64N(int64(top-lo)+1))
}

// sleepCtx waits d or returns early when ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func tryOnce[T transact.Tx](ctx context.Context, b transact.Beginner[T], fn func(context.Context, T) error, timeout time.Duration, txOpts []transact.Option) (err error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return transact.Run(ctx, b, func(tx T) error { return fn(ctx, tx) }, txOpts...)
}

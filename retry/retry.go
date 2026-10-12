// Package transactretry retries failed transactions.
package transactretry

import (
	"context"
	"fmt"
	"time"

	"github.com/lrweck/transact"
)

// Option configures Run.
type Option func(*options)

type options struct {
	maxAttempts   int
	perTryTimeout time.Duration
	totalTimeout  time.Duration
	retryIf       func(error) bool
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
	}
	return fmt.Errorf("transact: gave up after %d attempt(s): %w", tries, err)
}

func tryOnce[T transact.Tx](ctx context.Context, b transact.Beginner[T], fn func(context.Context, T) error, timeout time.Duration, txOpts []transact.Option) (err error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return transact.Run(ctx, b, func(tx T) error { return fn(ctx, tx) }, txOpts...)
}

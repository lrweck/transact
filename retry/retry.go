// Package transactretry retries failed transactions.
package transactretry

import (
	"context"
	"fmt"
	"time"

	"github.com/lrweck/transact"
)

// Policy controls how Run retries a failed transaction.
type Policy struct {
	// MaxAttempts is the total number of tries, including the first one.
	// Values below 1 mean 1, a single try with no retry.
	MaxAttempts int
	// PerTryTimeout bounds each try. The callback gets a context that
	// expires after this time. Zero or less means no limit.
	PerTryTimeout time.Duration
	// TotalTimeout bounds the whole operation, all tries together.
	// Zero or less means no limit.
	TotalTimeout time.Duration
	// RetryIf reports whether the error from a failed try is worth
	// retrying. Nil means every error is retried. Returning false stops
	// and returns the error.
	RetryIf func(error) bool
}

// Run starts a transaction, calls fn, and commits, retrying on failure.
// Each try runs through transact.Run, so a failed try is rolled back before
// the next one starts.
//
// Fn gets the try context and the transaction. Use that context for queries
// so PerTryTimeout can stop a slow try.
//
// A panic in fn rolls back and panics again without retry. When the context
// is done, Run stops and returns the context error. Otherwise it returns the
// last error wrapped with the try count, so errors.Is and errors.As work.
func Run[T transact.Tx](ctx context.Context, b transact.Beginner[T], fn func(context.Context, T) error, p Policy, txOpts ...transact.Option) error {
	max := p.MaxAttempts
	if max < 1 {
		max = 1
	}
	if p.TotalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.TotalTimeout)
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
		err = tryOnce(ctx, b, fn, p.PerTryTimeout, txOpts)
		if err == nil {
			return nil
		}
		if tries == max {
			break
		}
		if p.RetryIf != nil && !p.RetryIf(err) {
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

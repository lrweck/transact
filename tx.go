package transact

import (
	"context"
	"fmt"
	"time"
)

// Tx is the smallest transaction handle. It can commit or roll back.
type Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Beginner starts transactions. A pool adapter implements it, and so does a
// transaction that supports savepoints.
type Beginner[T Tx] interface {
	Begin(ctx context.Context, opts Options) (T, error)
}

// Run starts a transaction, calls fn with it, then commits.
// It rolls back when fn returns an error and also when fn panics.
// A panic still panics again after the rollback.
// When the options carry a retry policy (WithRetry), a failed try is rolled
// back and tried again. See RetryPolicy for details.
func Run[T Tx](ctx context.Context, b Beginner[T], fn func(T) error, opts ...Options) error {
	o := JoinOptions(opts...)
	if o.retry == nil {
		return runOnce(ctx, b, fn, o)
	}
	return runRetry(ctx, b, fn, o)
}

func runOnce[T Tx](ctx context.Context, b Beginner[T], fn func(T) error, o Options) (err error) {
	tx, err := b.Begin(ctx, o)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// runRetry tries runOnce again on failure. A panic rolls back and panics
// again without retry. When the context is done, it stops and returns the
// context error. Otherwise it returns the last error wrapped with the try
// count, so errors.Is and errors.As work.
func runRetry[T Tx](ctx context.Context, b Beginner[T], fn func(T) error, o Options) error {
	p := o.retry
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
		err = tryOnce(ctx, b, fn, o, p.PerTryTimeout)
		if err == nil {
			return nil
		}
		if tries == max {
			break
		}
		if p.RetryIf != nil && !p.RetryIf(err) {
			break
		}
		if werr := sleepCtx(ctx, wait(p, tries)); werr != nil {
			return fmt.Errorf("transact: gave up after %d attempt(s): %w", tries, werr)
		}
	}
	return fmt.Errorf("transact: gave up after %d attempt(s): %w", tries, err)
}

// tryOnce runs one try with an optional timeout on its begin and commit.
// The callback keeps using your context for queries.
func tryOnce[T Tx](ctx context.Context, b Beginner[T], fn func(T) error, o Options, timeout time.Duration) (err error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return runOnce(ctx, b, fn, o)
}

// wait computes the hold before the next try. failed is the number of
// failed tries so far, starting at 1.
func wait(p *RetryPolicy, failed int) time.Duration {
	if p.Wait == nil {
		return 0
	}
	if d := p.Wait(failed); d > 0 {
		return d
	}
	return 0
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

// Manager holds a Beginner and default options used by every Run.
//
// Manager is itself a Beginner, so it works anywhere a Beginner is needed,
// like transact.Run.
type Manager[T Tx] struct {
	b        Beginner[T]
	defaults Options
}

// New returns a Manager. The options are defaults, joined into one.
// Options passed to Run override the defaults for that call.
func New[T Tx](b Beginner[T], opts ...Options) *Manager[T] {
	return &Manager[T]{b: b, defaults: JoinOptions(opts...)}
}

// Begin starts a transaction with the manager defaults plus opts.
// Properties set in opts win. See JoinOptions for details.
func (m *Manager[T]) Begin(ctx context.Context, opts Options) (T, error) {
	return m.b.Begin(ctx, JoinOptions(m.defaults, opts))
}

// Run starts a transaction with the manager defaults plus opts, calls fn,
// then commits. It rolls back on error or panic. See Run for details.
func (m *Manager[T]) Run(ctx context.Context, fn func(T) error, opts ...Options) error {
	return Run(ctx, m, fn, opts...)
}

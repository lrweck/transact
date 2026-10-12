package transact

import (
	"context"
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
func Run[T Tx](ctx context.Context, b Beginner[T], fn func(T) error, opts ...Option) (err error) {
	tx, err := b.Begin(ctx, buildOptions(opts))
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

// Manager holds a Beginner and default options used by every Run.
//
// Manager is itself a Beginner, so it works anywhere a Beginner is needed,
// like transact.Run or the retry module.
type Manager[T Tx] struct {
	b        Beginner[T]
	defaults []Option
}

// New returns a Manager. The options are defaults. Options passed to Run
// replace the defaults for that call.
func New[T Tx](b Beginner[T], opts ...Option) *Manager[T] {
	return &Manager[T]{b: b, defaults: opts}
}

// Begin starts a transaction with the manager defaults. Fields set in opts
// win for IsoLevel and Deferrable. ReadOnly is on when either side sets it.
func (m *Manager[T]) Begin(ctx context.Context, opts Options) (T, error) {
	base := buildOptions(m.defaults)
	if opts.IsoLevel != LevelDefault {
		base.IsoLevel = opts.IsoLevel
	}
	if opts.Deferrable != DeferrableDefault {
		base.Deferrable = opts.Deferrable
	}
	base.ReadOnly = base.ReadOnly || opts.ReadOnly
	return m.b.Begin(ctx, base)
}

// Run starts a transaction with the manager defaults plus opts, calls fn,
// then commits. It rolls back on error or panic. See Run for details.
func (m *Manager[T]) Run(ctx context.Context, fn func(T) error, opts ...Option) error {
	return Run(ctx, m, fn, opts...)
}

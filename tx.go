package transact

import (
	"context"
	"slices"
)

// Tx is the minimal transaction handle. Driver transactions implement it through their adapter.
type Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Beginner starts transactions. A pool adapter implements it, and so does a Tx for subtransactions.
type Beginner[T Tx] interface {
	Begin(ctx context.Context, opts Options) (T, error)
}

// Run begins a transaction, calls fn with it, then commits. It rolls back if fn returns an error or panics.
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

// Manager holds a Beginner and default options applied to every Run.
type Manager[T Tx] struct {
	b        Beginner[T]
	defaults []Option
}

// New returns a Manager. opts are defaults; per-call options given to Run override them.
func New[T Tx](b Beginner[T], opts ...Option) *Manager[T] {
	return &Manager[T]{b: b, defaults: opts}
}

func (m *Manager[T]) Run(ctx context.Context, fn func(T) error, opts ...Option) error {
	return Run(ctx, m.b, fn, slices.Concat(m.defaults, opts)...)
}

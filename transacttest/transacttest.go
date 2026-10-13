// Package transacttest checks that a transact adapter behaves correctly.
// Call Conformance from the adapter's tests, against a real database.
package transacttest

import (
	"context"
	"errors"
	"testing"

	"github.com/lrweck/transact"
)

// Store connects an adapter to a table with one integer column, named id.
type Store[T transact.Tx] struct {
	// Beginner starts transactions on the database under test.
	Beginner transact.Beginner[T]
	// Reset empties the table. Conformance calls it before each check.
	Reset func(ctx context.Context) error
	// Insert adds a row with the given id through tx.
	Insert func(ctx context.Context, tx T, id int) error
	// Count returns the number of rows, read through tx.
	Count func(ctx context.Context, tx T) (int, error)
}

// Conformance runs the checks every adapter must pass:
// commit, rollback on error, rollback on panic, and savepoints.
// The transaction type T must also nest, meaning T implements
// transact.Beginner[T] (every adapter in this module does).
func Conformance[T transact.Tx](t *testing.T, s Store[T]) {
	t.Helper()

	t.Run("commits", func(t *testing.T) {
		reset(t, s)
		run(t, s, func(tx T) error { return s.Insert(t.Context(), tx, 1) })
		if n := count(t, s); n != 1 {
			t.Fatalf("rows = %d, want 1", n)
		}
	})

	t.Run("rolls back on error", func(t *testing.T) {
		reset(t, s)
		boom := errors.New("boom")
		err := transact.Run(t.Context(), s.Beginner, func(tx T) error {
			if err := s.Insert(t.Context(), tx, 1); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		if n := count(t, s); n != 0 {
			t.Fatalf("rows = %d, want 0 after rollback", n)
		}
	})

	t.Run("rolls back on panic and panics again", func(t *testing.T) {
		reset(t, s)
		func() {
			defer func() {
				if r := recover(); r != "kaboom" {
					t.Fatalf("recover = %v, want kaboom", r)
				}
			}()
			_ = transact.Run(t.Context(), s.Beginner, func(tx T) error {
				if err := s.Insert(t.Context(), tx, 1); err != nil {
					return err
				}
				panic("kaboom")
			})
			t.Fatal("expected panic")
		}()
		if n := count(t, s); n != 0 {
			t.Fatalf("rows = %d, want 0 after panic rollback", n)
		}
	})

	t.Run("failed savepoint keeps outer work", func(t *testing.T) {
		reset(t, s)
		boom := errors.New("boom")
		run(t, s, func(tx T) error {
			if err := s.Insert(t.Context(), tx, 1); err != nil {
				return err
			}
			if err := transact.Run(t.Context(), nester(t, tx), func(sub T) error {
				if err := s.Insert(t.Context(), sub, 2); err != nil {
					return err
				}
				return boom
			}); !errors.Is(err, boom) {
				t.Fatalf("sub err = %v, want boom", err)
			}
			return nil
		})
		if n := count(t, s); n != 1 {
			t.Fatalf("rows = %d, want 1 (only the outer insert)", n)
		}
	})

	t.Run("successful savepoint persists", func(t *testing.T) {
		reset(t, s)
		run(t, s, func(tx T) error {
			if err := s.Insert(t.Context(), tx, 1); err != nil {
				return err
			}
			return transact.Run(t.Context(), nester(t, tx), func(sub T) error {
				return s.Insert(t.Context(), sub, 2)
			})
		})
		if n := count(t, s); n != 2 {
			t.Fatalf("rows = %d, want 2", n)
		}
	})

	t.Run("savepoint rejects transaction options", func(t *testing.T) {
		reset(t, s)
		run(t, s, func(tx T) error {
			err := transact.Run(t.Context(), nester(t, tx), func(T) error { return nil },
				transact.WithReadOnly())
			if !errors.Is(err, transact.ErrSavepointOptions) {
				t.Fatalf("err = %v, want ErrSavepointOptions", err)
			}
			return nil
		})
	})
}

func nester[T transact.Tx](t *testing.T, tx T) transact.Beginner[T] {
	t.Helper()
	b, ok := any(tx).(transact.Beginner[T])
	if !ok {
		t.Fatalf("%T cannot nest: it does not implement transact.Beginner", tx)
	}
	return b
}

func reset[T transact.Tx](t *testing.T, s Store[T]) {
	t.Helper()
	if err := s.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func run[T transact.Tx](t *testing.T, s Store[T], fn func(T) error) {
	t.Helper()
	if err := transact.Run(t.Context(), s.Beginner, fn); err != nil {
		t.Fatal(err)
	}
}

func count[T transact.Tx](t *testing.T, s Store[T]) int {
	t.Helper()
	var n int
	run(t, s, func(tx T) error {
		var err error
		n, err = s.Count(t.Context(), tx)
		return err
	})
	return n
}

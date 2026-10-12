package transactretry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lrweck/transact"
)

type fakeTx struct{}

func (fakeTx) Commit(context.Context) error   { return nil }
func (fakeTx) Rollback(context.Context) error { return nil }

type fakeBeginner struct {
	begins   int
	opts     transact.Options
	beginErr error
}

func (b *fakeBeginner) Begin(_ context.Context, o transact.Options) (fakeTx, error) {
	if b.beginErr != nil {
		return fakeTx{}, b.beginErr
	}
	b.begins++
	b.opts = o
	return fakeTx{}, nil
}

func TestRunSucceedsFirstTry(t *testing.T) {
	b := &fakeBeginner{}
	err := Run(context.Background(), b, func(context.Context, fakeTx) error { return nil }, WithMaxAttempts(3))
	if err != nil {
		t.Fatal(err)
	}
	if b.begins != 1 {
		t.Fatalf("begins = %d, want 1", b.begins)
	}
}

func TestRunRetriesThenSucceeds(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	left := 2
	err := Run(context.Background(), b, func(context.Context, fakeTx) error {
		if left > 0 {
			left--
			return boom
		}
		return nil
	}, WithMaxAttempts(3))
	if err != nil {
		t.Fatal(err)
	}
	if b.begins != 3 {
		t.Fatalf("begins = %d, want 3", b.begins)
	}
}

func TestRunGivesUpWrappingLastError(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := Run(context.Background(), b, func(context.Context, fakeTx) error { return boom }, WithMaxAttempts(2))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped boom", err)
	}
	if b.begins != 2 {
		t.Fatalf("begins = %d, want 2", b.begins)
	}
}

func TestRunStopsWhenRetryIfSaysNo(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := Run(context.Background(), b, func(context.Context, fakeTx) error { return boom },
		WithMaxAttempts(5), WithRetryIf(func(error) bool { return false }))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 1 {
		t.Fatalf("begins = %d, want 1", b.begins)
	}
}

func TestRunDefaultsToOneTry(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := Run(context.Background(), b, func(context.Context, fakeTx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 1 {
		t.Fatalf("begins = %d, want 1", b.begins)
	}
}

func TestRunPassesTxOptions(t *testing.T) {
	b := &fakeBeginner{}
	err := Run(context.Background(), b, func(context.Context, fakeTx) error { return nil },
		WithMaxAttempts(1), WithTxOptions(transact.WithIsoLevel(transact.LevelSerializable)))
	if err != nil {
		t.Fatal(err)
	}
	if b.opts.IsoLevel != transact.LevelSerializable {
		t.Fatalf("opts = %+v, want serializable", b.opts)
	}
}

func TestRunTryContextHasDeadline(t *testing.T) {
	b := &fakeBeginner{}
	err := Run(context.Background(), b, func(ctx context.Context, _ fakeTx) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("try context has no deadline")
		}
		return nil
	}, WithMaxAttempts(1), WithPerTryTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunPerTryTimeoutFires(t *testing.T) {
	b := &fakeBeginner{}
	err := Run(context.Background(), b, func(ctx context.Context, _ fakeTx) error {
		<-ctx.Done()
		return ctx.Err()
	}, WithMaxAttempts(2), WithPerTryTimeout(10*time.Millisecond), WithRetryIf(func(error) bool { return true }))
	if err == nil {
		t.Fatal("expected error")
	}
	if b.begins != 2 {
		t.Fatalf("begins = %d, want 2", b.begins)
	}
}

func TestRunTotalTimeoutSetsDeadline(t *testing.T) {
	b := &fakeBeginner{}
	err := Run(context.Background(), b, func(ctx context.Context, _ fakeTx) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("context has no deadline")
		}
		return nil
	}, WithMaxAttempts(1), WithTotalTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunStopsWhenContextDiesBetweenTries(t *testing.T) {
	b := &fakeBeginner{}
	ctx, cancel := context.WithCancel(context.Background())
	boom := errors.New("boom")
	first := true
	err := Run(ctx, b, func(context.Context, fakeTx) error {
		if first {
			first = false
			cancel()
		}
		return boom
	}, WithMaxAttempts(5))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if b.begins != 1 {
		t.Fatalf("begins = %d, want 1", b.begins)
	}
}

func TestRunCanceledContextSkipsFn(t *testing.T) {
	b := &fakeBeginner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := Run(ctx, b, func(context.Context, fakeTx) error { called = true; return nil }, WithMaxAttempts(3))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("fn was called with a canceled context")
	}
	if b.begins != 0 {
		t.Fatalf("begins = %d, want 0", b.begins)
	}
}

func TestRunPanicPropagatesWithoutRetry(t *testing.T) {
	b := &fakeBeginner{}
	defer func() {
		if r := recover(); r != "kaboom" {
			t.Fatalf("recover = %v, want kaboom", r)
		}
		if b.begins != 1 {
			t.Fatalf("begins = %d, want 1", b.begins)
		}
	}()
	_ = Run(context.Background(), b, func(context.Context, fakeTx) error { panic("kaboom") }, WithMaxAttempts(3))
	t.Fatal("expected panic")
}

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

func TestManagerRunUsesDefaults(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	m := New(b, WithMaxAttempts(3))
	err := m.Run(context.Background(), func(context.Context, fakeTx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 3 {
		t.Fatalf("begins = %d, want 3", b.begins)
	}
}

func TestManagerCallOptsOverrideDefaults(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	m := New(b, WithMaxAttempts(1))
	err := m.Run(context.Background(), func(context.Context, fakeTx) error { return boom }, WithMaxAttempts(3))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 3 {
		t.Fatalf("begins = %d, want 3", b.begins)
	}
}

func TestManagerMergesTxOptions(t *testing.T) {
	b := &fakeBeginner{}
	m := New(b, WithTxOptions(transact.WithIsoLevel(transact.LevelSerializable)))
	if err := m.Run(context.Background(), func(context.Context, fakeTx) error { return nil },
		WithTxOptions(transact.WithReadOnly())); err != nil {
		t.Fatal(err)
	}
	if b.opts.IsoLevel != transact.LevelSerializable || !b.opts.ReadOnly {
		t.Fatalf("opts = %+v, want serializable + read-only", b.opts)
	}
}

func TestBackoffStrategies(t *testing.T) {
	if got := ConstantBackoff(100 * time.Millisecond)(1); got != 100*time.Millisecond {
		t.Fatalf("constant(1) = %v", got)
	}
	if got := ConstantBackoff(100 * time.Millisecond)(5); got != 100*time.Millisecond {
		t.Fatalf("constant(5) = %v", got)
	}
	for n, want := range map[int]time.Duration{1: 100 * time.Millisecond, 2: 200 * time.Millisecond, 3: 300 * time.Millisecond} {
		if got := LinearBackoff(100 * time.Millisecond)(n); got != want {
			t.Fatalf("linear(%d) = %v, want %v", n, got, want)
		}
	}
	for n, want := range map[int]time.Duration{1: 100 * time.Millisecond, 2: 200 * time.Millisecond, 3: 400 * time.Millisecond, 4: 800 * time.Millisecond} {
		if got := ExponentialBackoff(100 * time.Millisecond)(n); got != want {
			t.Fatalf("exponential(%d) = %v, want %v", n, got, want)
		}
	}
	if got := ExponentialBackoff(time.Hour)(100); got <= 0 {
		t.Fatalf("exponential(100) = %v, want positive saturation", got)
	}
}

func TestWaitForJitterBounds(t *testing.T) {
	check := func(o options, failed int, lo, hi time.Duration) {
		t.Helper()
		for i := 0; i < 200; i++ {
			if got := waitFor(&o, failed); got < lo || got > hi {
				t.Fatalf("waitFor = %v, want in [%v, %v]", got, lo, hi)
			}
		}
	}
	strategy := ConstantBackoff(10 * time.Millisecond)
	check(options{backoff: strategy, jitterMin: 5 * time.Millisecond, jitterMax: 15 * time.Millisecond}, 1, 5*time.Millisecond, 10*time.Millisecond)
	check(options{backoff: ConstantBackoff(time.Hour), jitterMin: 5 * time.Millisecond, jitterMax: 15 * time.Millisecond}, 1, 5*time.Millisecond, 15*time.Millisecond)
	check(options{backoff: ConstantBackoff(time.Millisecond), jitterMin: 5 * time.Millisecond, jitterMax: 15 * time.Millisecond}, 1, 5*time.Millisecond, 5*time.Millisecond)
	check(options{jitterMin: 5 * time.Millisecond, jitterMax: 15 * time.Millisecond}, 1, 5*time.Millisecond, 15*time.Millisecond)
	if got := waitFor(&options{}, 1); got != 0 {
		t.Fatalf("waitFor without backoff or jitter = %v, want 0", got)
	}
	if got := waitFor(&options{backoff: ConstantBackoff(-time.Second)}, 1); got != 0 {
		t.Fatalf("waitFor with negative backoff = %v, want 0", got)
	}
}

func apply(opts ...Option) options {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

func TestJitterOptionClamps(t *testing.T) {
	o := apply(WithJitter(-5*time.Millisecond, 15*time.Millisecond), WithBackoff(ConstantBackoff(10*time.Millisecond)))
	for i := 0; i < 100; i++ {
		if got := waitFor(&o, 1); got < 0 || got > 10*time.Millisecond {
			t.Fatalf("waitFor = %v, want in [0, 10ms]", got)
		}
	}
	o = apply(WithJitter(15*time.Millisecond, 5*time.Millisecond))
	if got := waitFor(&o, 1); got != 15*time.Millisecond {
		t.Fatalf("waitFor with max < min = %v, want 15ms", got)
	}
}

func TestRunSleepsBetweenTries(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	start := time.Now()
	err := Run(context.Background(), b, func(context.Context, fakeTx) error { return boom },
		WithMaxAttempts(2), WithBackoff(ConstantBackoff(20*time.Millisecond)))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 2 {
		t.Fatalf("begins = %d, want 2", b.begins)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("elapsed = %v, want at least 20ms", elapsed)
	}
}

func TestRunSleepInterruptedByCancel(t *testing.T) {
	b := &fakeBeginner{}
	ctx, cancel := context.WithCancel(context.Background())
	boom := errors.New("boom")
	start := time.Now()
	err := Run(ctx, b, func(context.Context, fakeTx) error {
		cancel()
		return boom
	}, WithMaxAttempts(5), WithBackoff(ConstantBackoff(time.Hour)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if b.begins != 1 {
		t.Fatalf("begins = %d, want 1", b.begins)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("elapsed = %v, want a fast return", elapsed)
	}
}

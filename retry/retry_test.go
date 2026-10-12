package transactretry

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lrweck/transact"
)

type fakeTx struct{}

func (fakeTx) Commit(context.Context) error   { return nil }
func (fakeTx) Rollback(context.Context) error { return nil }

type fakeBeginner struct {
	begins   int
	deadline bool
}

func (b *fakeBeginner) Begin(ctx context.Context, _ transact.Options) (fakeTx, error) {
	_, b.deadline = ctx.Deadline()
	b.begins++
	return fakeTx{}, nil
}

func TestNewRunsThroughCoreRun(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	left := 2
	err := transact.Run(context.Background(), b, func(fakeTx) error {
		if left > 0 {
			left--
			return boom
		}
		return nil
	}, New(WithMaxAttempts(3)))
	if err != nil {
		t.Fatal(err)
	}
	if b.begins != 3 {
		t.Fatalf("begins = %d, want 3", b.begins)
	}
}

func TestNewGivesUpWrappingLastError(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := transact.Run(context.Background(), b, func(fakeTx) error { return boom },
		New(WithMaxAttempts(2)))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped boom", err)
	}
}

func TestNewStopsWhenRetryIfSaysNo(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := transact.Run(context.Background(), b, func(fakeTx) error { return boom },
		New(WithMaxAttempts(5), WithRetryIf(func(error) bool { return false })))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 1 {
		t.Fatalf("begins = %d, want 1", b.begins)
	}
}

func TestNewRetriesWhenAnyMatcherSaysYes(t *testing.T) {
	no := func(error) bool { return false }
	yes := func(error) bool { return true }
	for _, opts := range [][]RetryOption{
		{WithRetryIf(no), WithRetryIf(yes)},
		{WithRetryIf(yes), WithRetryIf(no)},
	} {
		b := &fakeBeginner{}
		boom := errors.New("boom")
		err := transact.Run(context.Background(), b, func(fakeTx) error { return boom },
			New(append([]RetryOption{WithMaxAttempts(2)}, opts...)...))
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		if b.begins != 2 {
			t.Fatalf("begins = %d, want 2", b.begins)
		}
	}
}

func TestNewIgnoresNilMatcher(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := transact.Run(context.Background(), b, func(fakeTx) error { return boom },
		New(WithMaxAttempts(2), WithRetryIf(nil)))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 2 {
		t.Fatalf("begins = %d, want 2 (nil matcher ignored, retries all)", b.begins)
	}
}

func TestNewDefaultsToOneTry(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := transact.Run(context.Background(), b, func(fakeTx) error { return boom }, New())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 1 {
		t.Fatalf("begins = %d, want 1", b.begins)
	}
}

func TestNewSetsPerTryAndTotalDeadlines(t *testing.T) {
	b := &fakeBeginner{}
	err := transact.Run(context.Background(), b, func(fakeTx) error { return nil },
		New(WithMaxAttempts(1), WithPerTryTimeout(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if !b.deadline {
		t.Fatal("begin got no deadline with PerTryTimeout")
	}
	b = &fakeBeginner{}
	err = transact.Run(context.Background(), b, func(fakeTx) error { return nil },
		New(WithMaxAttempts(1), WithTotalTimeout(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if !b.deadline {
		t.Fatal("begin got no deadline with TotalTimeout")
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

func TestComposeWaitBounds(t *testing.T) {
	check := func(w func(int) time.Duration, lo, hi time.Duration) {
		t.Helper()
		for range 200 {
			if got := w(1); got < lo || got > hi {
				t.Fatalf("wait = %v, want in [%v, %v]", got, lo, hi)
			}
		}
	}
	check(composeWait(ConstantBackoff(10*time.Millisecond), 5*time.Millisecond, 15*time.Millisecond), 5*time.Millisecond, 10*time.Millisecond)
	check(composeWait(ConstantBackoff(time.Hour), 5*time.Millisecond, 15*time.Millisecond), 5*time.Millisecond, 15*time.Millisecond)
	check(composeWait(ConstantBackoff(time.Millisecond), 5*time.Millisecond, 15*time.Millisecond), 5*time.Millisecond, 5*time.Millisecond)
	check(composeWait(nil, 5*time.Millisecond, 15*time.Millisecond), 5*time.Millisecond, 15*time.Millisecond)
	if got := composeWait(nil, 0, 0); got != nil {
		t.Fatal("composeWait without backoff or jitter should be nil")
	}
	if got := composeWait(ConstantBackoff(10*time.Millisecond), 0, 0); got(1) != 10*time.Millisecond {
		t.Fatalf("composeWait without jitter = %v, want strategy value", got(1))
	}
}

func TestComposeWaitClamps(t *testing.T) {
	w := composeWait(ConstantBackoff(10*time.Millisecond), -5*time.Millisecond, 15*time.Millisecond)
	for range 100 {
		if got := w(1); got < 0 || got > 10*time.Millisecond {
			t.Fatalf("wait = %v, want in [0, 10ms]", got)
		}
	}
	w = composeWait(nil, 15*time.Millisecond, 5*time.Millisecond)
	if got := w(1); got != 15*time.Millisecond {
		t.Fatalf("wait with max < min = %v, want 15ms", got)
	}
	w = composeWait(ConstantBackoff(-time.Second), 0, 0)
	if w == nil {
		t.Fatal("strategy without jitter should pass through")
	}
	w = composeWait(ConstantBackoff(-time.Second), 5*time.Millisecond, 15*time.Millisecond)
	if got := w(1); got != 5*time.Millisecond {
		t.Fatalf("wait with negative strategy = %v, want 5ms floor", got)
	}
}

func TestNewWithJitterEndToEnd(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := transact.Run(context.Background(), b, func(fakeTx) error { return boom },
		New(WithMaxAttempts(2),
			WithBackoff(ConstantBackoff(time.Millisecond)),
			WithJitter(5*time.Millisecond, 15*time.Millisecond)))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if b.begins != 2 {
		t.Fatalf("begins = %d, want 2", b.begins)
	}
}

func TestNewSleepsBetweenTries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &fakeBeginner{}
		boom := errors.New("boom")
		start := time.Now()
		err := transact.Run(context.Background(), b, func(fakeTx) error { return boom },
			New(WithMaxAttempts(2), WithBackoff(ConstantBackoff(20*time.Millisecond))))
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		if b.begins != 2 {
			t.Fatalf("begins = %d, want 2", b.begins)
		}
		if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
			t.Fatalf("elapsed = %v, want at least 20ms", elapsed)
		}
	})
}

func TestNewSleepInterruptedByCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &fakeBeginner{}
		ctx, cancel := context.WithCancel(context.Background())
		boom := errors.New("boom")
		start := time.Now()
		err := transact.Run(ctx, b, func(fakeTx) error {
			cancel()
			return boom
		}, New(WithMaxAttempts(5), WithBackoff(ConstantBackoff(time.Hour))))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if b.begins != 1 {
			t.Fatalf("begins = %d, want 1", b.begins)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("elapsed = %v, want a fast return", elapsed)
		}
	})
}

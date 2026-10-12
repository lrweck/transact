package transact

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

type fakeTx struct {
	log       *[]string
	commitErr error
}

func (f fakeTx) Commit(context.Context) error {
	*f.log = append(*f.log, "commit")
	return f.commitErr
}
func (f fakeTx) Rollback(context.Context) error { *f.log = append(*f.log, "rollback"); return nil }

type fakeBeginner struct {
	log      []string
	opts     Options
	beginTx  fakeTx
	beginErr error
	deadline bool
}

func (b *fakeBeginner) Begin(ctx context.Context, o Options) (fakeTx, error) {
	if b.beginErr != nil {
		return fakeTx{}, b.beginErr
	}
	_, b.deadline = ctx.Deadline()
	b.opts = o
	b.log = append(b.log, "begin")
	if b.beginTx.log == nil {
		b.beginTx.log = &b.log
	}
	return b.beginTx, nil
}

func countBegins(log []string) int {
	n := 0
	for _, e := range log {
		if e == "begin" {
			n++
		}
	}
	return n
}

func TestRunCommitsOnSuccess(t *testing.T) {
	b := &fakeBeginner{}
	err := Run(context.Background(), b, func(fakeTx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"begin", "commit"}; !slices.Equal(b.log, want) {
		t.Fatalf("log = %v, want %v", b.log, want)
	}
}

func TestRunRollsBackOnError(t *testing.T) {
	b := &fakeBeginner{}
	want := errors.New("boom")
	err := Run(context.Background(), b, func(fakeTx) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if exp := []string{"begin", "rollback"}; !slices.Equal(b.log, exp) {
		t.Fatalf("log = %v, want %v", b.log, exp)
	}
}

func TestRunRollsBackOnPanicAndRepanics(t *testing.T) {
	b := &fakeBeginner{}
	defer func() {
		if r := recover(); r != "kaboom" {
			t.Fatalf("recover = %v, want kaboom", r)
		}
		if exp := []string{"begin", "rollback"}; !slices.Equal(b.log, exp) {
			t.Fatalf("log = %v, want %v", b.log, exp)
		}
	}()
	_ = Run(context.Background(), b, func(fakeTx) error { panic("kaboom") })
	t.Fatal("expected panic")
}

type spTx struct{ log *[]string }

func (s spTx) Commit(context.Context) error   { *s.log = append(*s.log, "commit"); return nil }
func (s spTx) Rollback(context.Context) error { *s.log = append(*s.log, "rollback"); return nil }
func (s spTx) Begin(context.Context, Options) (spTx, error) {
	*s.log = append(*s.log, "sub-begin")
	return s, nil
}

func TestRunNestsOnTx(t *testing.T) {
	var log []string
	root := spTx{&log}
	err := Run(context.Background(), beginFn(func() (spTx, error) { log = append(log, "begin"); return root, nil }), func(tx spTx) error {
		return Run(context.Background(), tx, func(spTx) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	if exp := []string{"begin", "sub-begin", "commit", "commit"}; !slices.Equal(log, exp) {
		t.Fatalf("log = %v, want %v", log, exp)
	}
}

type beginFn func() (spTx, error)

func (f beginFn) Begin(context.Context, Options) (spTx, error) { return f() }

func TestRunReturnsBeginErrorWithoutCallingFn(t *testing.T) {
	b := &fakeBeginner{beginErr: errors.New("no conn")}
	called := false
	err := Run(context.Background(), b, func(fakeTx) error { called = true; return nil })
	if err == nil || err.Error() != "no conn" {
		t.Fatalf("err = %v, want no conn", err)
	}
	if called {
		t.Fatal("fn called after Begin error")
	}
	if len(b.log) != 0 {
		t.Fatalf("log = %v, want empty", b.log)
	}
}

func TestRunReturnsCommitError(t *testing.T) {
	b := &fakeBeginner{}
	want := errors.New("commit failed")
	b.beginTx = fakeTx{log: &b.log, commitErr: want}
	err := Run(context.Background(), b, func(fakeTx) error { return nil })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestManagerAppliesDefaultsThenCallOptions(t *testing.T) {
	b := &fakeBeginner{}
	m := New[fakeTx](b, WithIsoLevel(LevelSerializable))
	_ = m.Run(context.Background(), func(fakeTx) error { return nil }, WithReadOnly())
	if b.opts.IsoLevel() != LevelSerializable || !b.opts.ReadOnly() {
		t.Fatalf("opts = %+v, want serializable + read-only", b.opts)
	}
}

func TestManagerIsABeginner(t *testing.T) {
	var _ Beginner[fakeTx] = New[fakeTx](&fakeBeginner{})
	b := &fakeBeginner{}
	m := New[fakeTx](b)
	if err := Run(context.Background(), m, func(fakeTx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if want := []string{"begin", "commit"}; !slices.Equal(b.log, want) {
		t.Fatalf("log = %v, want %v", b.log, want)
	}
}

func TestManagerBeginMergesDefaults(t *testing.T) {
	b := &fakeBeginner{}
	m := New[fakeTx](b, WithIsoLevel(LevelSerializable), WithReadOnly(), WithDeferrable(Deferred))
	if _, err := m.Begin(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if b.opts.IsoLevel() != LevelSerializable || !b.opts.ReadOnly() || b.opts.Deferrable() != Deferred {
		t.Fatalf("opts = %+v, want defaults", b.opts)
	}
	if _, err := m.Begin(context.Background(), JoinOptions(WithIsoLevel(LevelReadCommitted), WithDeferrable(NotDeferrable))); err != nil {
		t.Fatal(err)
	}
	if b.opts.IsoLevel() != LevelReadCommitted || !b.opts.ReadOnly() || b.opts.Deferrable() != NotDeferrable {
		t.Fatalf("opts = %+v, want override + defaults", b.opts)
	}
}

func TestManagerBeginMergesRetry(t *testing.T) {
	b := &fakeBeginner{}
	ra := RetryPolicy{MaxAttempts: 2}
	rb := RetryPolicy{MaxAttempts: 5}
	m := New[fakeTx](b, WithRetry(ra))
	if _, err := m.Begin(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if p, ok := b.opts.Retry(); !ok || p.MaxAttempts != 2 {
		t.Fatalf("opts retry = %+v, %v, want ra", p, ok)
	}
	if _, err := m.Begin(context.Background(), WithRetry(rb)); err != nil {
		t.Fatal(err)
	}
	if p, _ := b.opts.Retry(); p.MaxAttempts != 5 {
		t.Fatalf("opts retry = %+v, want rb", p)
	}
}

func TestRunRetrySucceedsAfterFailures(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	left := 2
	err := Run(context.Background(), b, func(fakeTx) error {
		if left > 0 {
			left--
			return boom
		}
		return nil
	}, WithRetry(RetryPolicy{MaxAttempts: 3}))
	if err != nil {
		t.Fatal(err)
	}
	if n := countBegins(b.log); n != 3 {
		t.Fatalf("begins = %d, want 3 (log %v)", n, b.log)
	}
}

func TestRunRetryGivesUpWrappingLastError(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := Run(context.Background(), b, func(fakeTx) error { return boom },
		WithRetry(RetryPolicy{MaxAttempts: 2}))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped boom", err)
	}
	if n := countBegins(b.log); n != 2 {
		t.Fatalf("begins = %d, want 2 (log %v)", n, b.log)
	}
}

func TestRunRetryStopsWhenRetryIfSaysNo(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := Run(context.Background(), b, func(fakeTx) error { return boom },
		WithRetry(RetryPolicy{MaxAttempts: 5, RetryIf: func(error) bool { return false }}))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := countBegins(b.log); n != 1 {
		t.Fatalf("begins = %d, want 1 (log %v)", n, b.log)
	}
}

func TestRunRetryZeroPolicyMeansOneTry(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	err := Run(context.Background(), b, func(fakeTx) error { return boom }, WithRetry(RetryPolicy{}))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := countBegins(b.log); n != 1 {
		t.Fatalf("begins = %d, want 1 (log %v)", n, b.log)
	}
}

func TestRunRetrySetsPerTryAndTotalDeadlines(t *testing.T) {
	b := &fakeBeginner{}
	err := Run(context.Background(), b, func(fakeTx) error { return nil },
		WithRetry(RetryPolicy{MaxAttempts: 1, PerTryTimeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	if !b.deadline {
		t.Fatal("begin got no deadline with PerTryTimeout")
	}
	b = &fakeBeginner{}
	err = Run(context.Background(), b, func(fakeTx) error { return nil },
		WithRetry(RetryPolicy{MaxAttempts: 1, TotalTimeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	if !b.deadline {
		t.Fatal("begin got no deadline with TotalTimeout")
	}
}

type blockingBeginner struct{ begins int }

func (b *blockingBeginner) Begin(ctx context.Context, _ Options) (fakeTx, error) {
	b.begins++
	<-ctx.Done()
	return fakeTx{}, ctx.Err()
}

func TestRunRetryPerTryTimeoutFires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &blockingBeginner{}
		err := Run(context.Background(), b, func(fakeTx) error { return nil },
			WithRetry(RetryPolicy{MaxAttempts: 2, PerTryTimeout: 10 * time.Millisecond}))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want context.DeadlineExceeded", err)
		}
		if b.begins != 2 {
			t.Fatalf("begins = %d, want 2", b.begins)
		}
	})
}

func TestRunRetryNegativeWaitMeansNoWait(t *testing.T) {
	b := &fakeBeginner{}
	boom := errors.New("boom")
	start := time.Now()
	err := Run(context.Background(), b, func(fakeTx) error { return boom },
		WithRetry(RetryPolicy{MaxAttempts: 2, Wait: func(int) time.Duration { return -time.Second }}))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("elapsed = %v, want a fast return", elapsed)
	}
}

func TestRunRetrySleepsBetweenTries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &fakeBeginner{}
		boom := errors.New("boom")
		start := time.Now()
		err := Run(context.Background(), b, func(fakeTx) error { return boom },
			WithRetry(RetryPolicy{MaxAttempts: 2, Wait: func(int) time.Duration { return 20 * time.Millisecond }}))
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
			t.Fatalf("elapsed = %v, want at least 20ms", elapsed)
		}
	})
}

func TestRunRetryStopsWhenContextDiesBetweenTries(t *testing.T) {
	b := &fakeBeginner{}
	ctx, cancel := context.WithCancel(context.Background())
	boom := errors.New("boom")
	first := true
	err := Run(ctx, b, func(fakeTx) error {
		if first {
			first = false
			cancel()
		}
		return boom
	}, WithRetry(RetryPolicy{MaxAttempts: 5}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := countBegins(b.log); n != 1 {
		t.Fatalf("begins = %d, want 1 (log %v)", n, b.log)
	}
}

func TestRunRetryCanceledContextSkipsFn(t *testing.T) {
	b := &fakeBeginner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := Run(ctx, b, func(fakeTx) error { called = true; return nil },
		WithRetry(RetryPolicy{MaxAttempts: 3}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("fn was called with a canceled context")
	}
}

func TestRunRetrySleepInterruptedByCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &fakeBeginner{}
		ctx, cancel := context.WithCancel(context.Background())
		boom := errors.New("boom")
		start := time.Now()
		err := Run(ctx, b, func(fakeTx) error {
			cancel()
			return boom
		}, WithRetry(RetryPolicy{MaxAttempts: 5, Wait: func(int) time.Duration { return time.Hour }}))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if n := countBegins(b.log); n != 1 {
			t.Fatalf("begins = %d, want 1 (log %v)", n, b.log)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("elapsed = %v, want a fast return", elapsed)
		}
	})
}

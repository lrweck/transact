package transact

import (
	"context"
	"errors"
	"slices"
	"testing"
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
}

func (b *fakeBeginner) Begin(_ context.Context, o Options) (fakeTx, error) {
	if b.beginErr != nil {
		return fakeTx{}, b.beginErr
	}
	b.opts = o
	b.log = append(b.log, "begin")
	if b.beginTx.log == nil {
		b.beginTx.log = &b.log
	}
	return b.beginTx, nil
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
	if b.opts.IsoLevel != LevelSerializable || !b.opts.ReadOnly {
		t.Fatalf("opts = %+v, want serializable + read-only", b.opts)
	}
}

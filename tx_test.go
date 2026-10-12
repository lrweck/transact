package transact

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type fakeTx struct{ log *[]string }

func (f fakeTx) Commit(context.Context) error   { *f.log = append(*f.log, "commit"); return nil }
func (f fakeTx) Rollback(context.Context) error { *f.log = append(*f.log, "rollback"); return nil }

type fakeBeginner struct {
	log  []string
	opts Options
}

func (b *fakeBeginner) Begin(_ context.Context, o Options) (fakeTx, error) {
	b.opts = o
	b.log = append(b.log, "begin")
	return fakeTx{&b.log}, nil
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

func TestManagerAppliesDefaultsThenCallOptions(t *testing.T) {
	b := &fakeBeginner{}
	m := New[fakeTx](b, WithIsoLevel(LevelSerializable))
	_ = m.Run(context.Background(), func(fakeTx) error { return nil }, WithReadOnly())
	if b.opts.IsoLevel != LevelSerializable || !b.opts.ReadOnly {
		t.Fatalf("opts = %+v, want serializable + read-only", b.opts)
	}
}

package transactpgx

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lrweck/transact"
)

func TestTxOptionsMapping(t *testing.T) {
	tests := []struct {
		name string
		in   transact.Options
		want pgx.TxOptions
	}{
		{"default", transact.Options{}, pgx.TxOptions{}},
		{"read uncommitted", transact.Options{IsoLevel: transact.LevelReadUncommitted}, pgx.TxOptions{IsoLevel: pgx.ReadUncommitted}},
		{"read committed", transact.Options{IsoLevel: transact.LevelReadCommitted}, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}},
		{"repeatable read", transact.Options{IsoLevel: transact.LevelRepeatableRead}, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}},
		{"serializable", transact.Options{IsoLevel: transact.LevelSerializable}, pgx.TxOptions{IsoLevel: pgx.Serializable}},
		{"read only", transact.Options{ReadOnly: true}, pgx.TxOptions{AccessMode: pgx.ReadOnly}},
		{"deferrable", transact.Options{Deferrable: transact.Deferred}, pgx.TxOptions{DeferrableMode: pgx.Deferrable}},
		{"not deferrable", transact.Options{Deferrable: transact.NotDeferrable}, pgx.TxOptions{DeferrableMode: pgx.NotDeferrable}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := txOptions(tt.in); got != tt.want {
				t.Fatalf("txOptions = %+v, want %+v", got, tt.want)
			}
		})
	}
}

type fakeInnerTx struct {
	pgx.Tx
	log       *[]string
	sub       pgx.Tx
	beginErr  error
	commitErr error
}

func (f *fakeInnerTx) Begin(context.Context) (pgx.Tx, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	*f.log = append(*f.log, "sub-begin")
	if f.sub != nil {
		return f.sub, nil
	}
	return &fakeInnerTx{log: f.log}, nil
}

func (f *fakeInnerTx) Commit(context.Context) error {
	*f.log = append(*f.log, "commit")
	return f.commitErr
}

func (f *fakeInnerTx) Rollback(context.Context) error {
	*f.log = append(*f.log, "rollback")
	return nil
}

type fakePool struct {
	opts pgx.TxOptions
	tx   pgx.Tx
	err  error
}

func (p *fakePool) BeginTx(_ context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	p.opts = opts
	if p.err != nil {
		return nil, p.err
	}
	return p.tx, nil
}

func TestBeginnerBeginPassesOptions(t *testing.T) {
	var log []string
	pool := &fakePool{tx: &fakeInnerTx{log: &log}}
	b := beginner{pool}
	tx, err := b.Begin(context.Background(), transact.Options{
		IsoLevel: transact.LevelSerializable,
		ReadOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx.Tx == nil {
		t.Fatal("tx is not wrapped")
	}
	if pool.opts.IsoLevel != pgx.Serializable || pool.opts.AccessMode != pgx.ReadOnly {
		t.Fatalf("pool opts = %+v", pool.opts)
	}
}

func TestBeginnerBeginError(t *testing.T) {
	pool := &fakePool{err: errors.New("no conn")}
	b := beginner{pool}
	if _, err := b.Begin(context.Background(), transact.Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestTxBeginSavepoint(t *testing.T) {
	var log []string
	sub := &fakeInnerTx{log: &log}
	tx := Tx{&fakeInnerTx{log: &log, sub: sub}}
	got, err := tx.Begin(context.Background(), transact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tx != sub {
		t.Fatal("Begin did not return the savepoint tx")
	}
}

func TestTxBeginError(t *testing.T) {
	var log []string
	tx := Tx{&fakeInnerTx{log: &log, beginErr: errors.New("sp failed")}}
	if _, err := tx.Begin(context.Background(), transact.Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestManagerRunCommitsAndRollsBack(t *testing.T) {
	var log []string
	pool := &fakePool{tx: &fakeInnerTx{log: &log}}
	m := New(pool)
	if err := m.Run(context.Background(), func(Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := m.Run(context.Background(), func(Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	want := []string{"commit", "rollback"}
	found := log[len(log)-2:]
	if found[0] != want[0] || found[1] != want[1] {
		t.Fatalf("log tail = %v, want %v (full log %v)", found, want, log)
	}
}

func TestSubCommitsAndRollsBack(t *testing.T) {
	var log []string
	sub := &fakeInnerTx{log: &log}
	tx := Tx{&fakeInnerTx{log: &log, sub: sub}}
	if err := tx.Sub(context.Background(), func(Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := tx.Sub(context.Background(), func(Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

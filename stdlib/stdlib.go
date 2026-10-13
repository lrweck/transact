package transactstdlib

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"

	"github.com/lrweck/transact"
)

// DB starts database/sql transactions. It is implemented by *sql.DB.
type DB interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Tx is a database/sql transaction. It embeds *sql.Tx, so all query methods
// are available. When it holds a savepoint, Commit releases it and Rollback
// rolls back to it instead of ending the whole transaction.
type Tx struct {
	*sql.Tx
	sp string
}

var savepointSeq atomic.Uint64

// Begin starts a savepoint inside the transaction. It returns
// transact.ErrSavepointOptions if opts set isolation, read-only, or deferrable,
// since a savepoint cannot change them.
func (t Tx) Begin(ctx context.Context, opts transact.Options) (Tx, error) {
	if opts.IsoLevel() != transact.LevelDefault || opts.ReadOnly() || opts.Deferrable() != transact.DeferrableDefault {
		return Tx{}, transact.ErrSavepointOptions
	}
	name := fmt.Sprintf("transact_sp_%d", savepointSeq.Add(1))
	if _, err := t.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return Tx{}, err
	}
	return Tx{Tx: t.Tx, sp: name}, nil
}

// Commit commits the transaction. When the Tx holds a savepoint, it only
// releases the savepoint and the outer transaction keeps going.
//
// Limit: a root commit goes through *sql.Tx.Commit, which takes no context.
// Once Commit starts, ctx cannot cancel it. Use ctx to stop the work before
// Commit, not during it.
func (t Tx) Commit(ctx context.Context) error {
	if t.sp != "" {
		_, err := t.ExecContext(ctx, "RELEASE SAVEPOINT "+t.sp)
		return err
	}
	return t.Tx.Commit()
}

// Rollback rolls back the transaction. When the Tx holds a savepoint, it
// only rolls back to the savepoint and the outer transaction keeps going.
func (t Tx) Rollback(ctx context.Context) error {
	if t.sp != "" {
		_, err := t.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+t.sp)
		return err
	}
	return t.Tx.Rollback()
}

// Sub runs fn inside a savepoint. When fn fails, only its work is rolled
// back and the error is returned. The outer transaction keeps going.
func (t Tx) Sub(ctx context.Context, fn func(Tx) error, opts ...transact.Options) error {
	return transact.Run(ctx, t, fn, opts...)
}

// Manager is a transact.Manager for database/sql. Embed it in your repository to get Run.
type Manager = transact.Manager[Tx]

// New returns a Manager for db. The options are defaults for every Run.
func New(db DB, opts ...transact.Options) *Manager {
	return transact.New(beginner{db}, opts...)
}

type beginner struct{ db DB }

func (b beginner) Begin(ctx context.Context, o transact.Options) (Tx, error) {
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: isolation(o.IsoLevel()),
		ReadOnly:  o.ReadOnly(),
	})
	if err != nil {
		return Tx{}, err
	}
	return Tx{Tx: tx}, nil
}

func isolation(l transact.IsoLevel) sql.IsolationLevel {
	switch l {
	case transact.LevelReadUncommitted:
		return sql.LevelReadUncommitted
	case transact.LevelReadCommitted:
		return sql.LevelReadCommitted
	case transact.LevelRepeatableRead:
		return sql.LevelRepeatableRead
	case transact.LevelSerializable:
		return sql.LevelSerializable
	}
	return sql.LevelDefault
}

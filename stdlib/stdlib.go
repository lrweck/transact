package transactstdlib

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"

	"github.com/lweck/transact"
)

// DB is implemented by *sql.DB.
type DB interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Tx wraps *sql.Tx. When sp is set, the Tx is a savepoint inside the outer transaction.
type Tx struct {
	*sql.Tx
	sp string
}

var savepointSeq atomic.Uint64

// Begin starts a savepoint. Commit releases it and Rollback rolls back to it.
func (t Tx) Begin(ctx context.Context, _ transact.Options) (Tx, error) {
	name := fmt.Sprintf("transact_sp_%d", savepointSeq.Add(1))
	if _, err := t.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return Tx{}, err
	}
	return Tx{Tx: t.Tx, sp: name}, nil
}

func (t Tx) Commit(ctx context.Context) error {
	if t.sp != "" {
		_, err := t.ExecContext(ctx, "RELEASE SAVEPOINT "+t.sp)
		return err
	}
	return t.Tx.Commit()
}

func (t Tx) Rollback(ctx context.Context) error {
	if t.sp != "" {
		_, err := t.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+t.sp)
		return err
	}
	return t.Tx.Rollback()
}

// Sub runs fn inside a savepoint.
func (t Tx) Sub(ctx context.Context, fn func(Tx) error, opts ...transact.Option) error {
	return transact.Run(ctx, t, fn, opts...)
}

// Manager is a transact.Manager bound to database/sql.
type Manager = transact.Manager[Tx]

// New returns a Manager for db.
func New(db DB, opts ...transact.Option) *Manager {
	return transact.New[Tx](beginner{db}, opts...)
}

type beginner struct{ db DB }

func (b beginner) Begin(ctx context.Context, o transact.Options) (Tx, error) {
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: isolation(o.IsoLevel),
		ReadOnly:  o.ReadOnly,
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

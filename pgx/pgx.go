package transactpgx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lrweck/transact"
)

// Pool starts pgx transactions. It is implemented by *pgxpool.Pool and *pgx.Conn.
type Pool interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// Tx is a pgx transaction. It embeds pgx.Tx, so all query methods are available.
// Its Begin starts a savepoint, so transactions can nest.
type Tx struct{ pgx.Tx }

// Begin starts a savepoint inside the transaction. Options are ignored because
// isolation and access mode cannot change inside a savepoint.
func (t Tx) Begin(ctx context.Context, _ transact.Options) (Tx, error) {
	sub, err := t.Tx.Begin(ctx)
	if err != nil {
		return Tx{}, err
	}
	return Tx{sub}, nil
}

// Sub runs fn inside a savepoint. When fn fails, only its work is rolled
// back and the error is returned. The outer transaction keeps going.
func (t Tx) Sub(ctx context.Context, fn func(Tx) error, opts ...transact.Option) error {
	return transact.Run(ctx, t, fn, opts...)
}

// Manager is a transact.Manager for pgx. Embed it in your repository to get Run.
type Manager = transact.Manager[Tx]

// New returns a Manager for pool. The options are defaults for every Run.
func New(pool Pool, opts ...transact.Option) *Manager {
	return transact.New[Tx](beginner{pool}, opts...)
}

type beginner struct{ pool Pool }

func (b beginner) Begin(ctx context.Context, o transact.Options) (Tx, error) {
	tx, err := b.pool.BeginTx(ctx, txOptions(o))
	if err != nil {
		return Tx{}, err
	}
	return Tx{tx}, nil
}

func txOptions(o transact.Options) pgx.TxOptions {
	var t pgx.TxOptions
	switch o.IsoLevel {
	case transact.LevelReadUncommitted:
		t.IsoLevel = pgx.ReadUncommitted
	case transact.LevelReadCommitted:
		t.IsoLevel = pgx.ReadCommitted
	case transact.LevelRepeatableRead:
		t.IsoLevel = pgx.RepeatableRead
	case transact.LevelSerializable:
		t.IsoLevel = pgx.Serializable
	}
	if o.ReadOnly {
		t.AccessMode = pgx.ReadOnly
	}
	switch o.Deferrable {
	case transact.Deferred:
		t.DeferrableMode = pgx.Deferrable
	case transact.NotDeferrable:
		t.DeferrableMode = pgx.NotDeferrable
	}
	return t
}

//go:build integration

package integration

import (
	"context"
	"testing"

	transactpgx "github.com/lrweck/transact/pgx"
	"github.com/lrweck/transact/transacttest"
)

func TestPostgresConformance(t *testing.T) {
	transacttest.Conformance(t, transacttest.Store[transactpgx.Tx]{
		Beginner: transactpgx.New(pgPool),
		Reset: func(ctx context.Context) error {
			_, err := pgPool.Exec(ctx, "DELETE FROM conf")
			return err
		},
		Insert: func(ctx context.Context, tx transactpgx.Tx, id int) error {
			_, err := tx.Exec(ctx, "INSERT INTO conf (id) VALUES ($1)", id)
			return err
		},
		Count: func(ctx context.Context, tx transactpgx.Tx) (int, error) {
			var n int
			err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM conf").Scan(&n)
			return n, err
		},
	})
}

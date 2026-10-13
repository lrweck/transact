//go:build integration

package integration

import (
	"context"
	"testing"

	transactstdlib "github.com/lrweck/transact/stdlib"
	"github.com/lrweck/transact/transacttest"
)

func TestMySQLConformance(t *testing.T) {
	transacttest.Conformance(t, transacttest.Store[transactstdlib.Tx]{
		Beginner: transactstdlib.New(myDB),
		Reset: func(ctx context.Context) error {
			_, err := myDB.ExecContext(ctx, "DELETE FROM conf")
			return err
		},
		Insert: func(ctx context.Context, tx transactstdlib.Tx, id int) error {
			_, err := tx.ExecContext(ctx, "INSERT INTO conf (id) VALUES (?)", id)
			return err
		},
		Count: func(ctx context.Context, tx transactstdlib.Tx) (int, error) {
			var n int
			err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conf").Scan(&n)
			return n, err
		},
	})
}

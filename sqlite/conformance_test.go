package transactsqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lrweck/transact/transacttest"
)

func TestConformance(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "conformance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE conf (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	transacttest.Conformance(t, transacttest.Store[Tx]{
		Beginner: New(db),
		Reset: func(ctx context.Context) error {
			_, err := db.ExecContext(ctx, "DELETE FROM conf")
			return err
		},
		Insert: func(ctx context.Context, tx Tx, id int) error {
			_, err := tx.ExecContext(ctx, "INSERT INTO conf (id) VALUES (?)", id)
			return err
		},
		Count: func(ctx context.Context, tx Tx) (int, error) {
			var n int
			err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conf").Scan(&n)
			return n, err
		},
	})
}

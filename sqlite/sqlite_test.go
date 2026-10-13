package transactsqlite

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSubRollbackKeepsOuterWork(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t (v INTEGER)"); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	m := New(db)
	boom := errors.New("boom")
	err = m.Run(ctx, func(tx Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO t (v) VALUES (1)"); err != nil {
			return err
		}
		_ = tx.Sub(ctx, func(sub Tx) error {
			if _, err := sub.ExecContext(ctx, "INSERT INTO t (v) VALUES (2)"); err != nil {
				return err
			}
			return boom
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []int
	rows, err := db.Query("SELECT v FROM t ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("rows = %v, want [1]", got)
	}
}

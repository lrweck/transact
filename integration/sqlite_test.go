//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	transactretry "github.com/lrweck/transact/retry"
	transactsqlite "github.com/lrweck/transact/sqlite"
	sqlite "modernc.org/sqlite"
)

func openFileDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := transactsqlite.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("CREATE TABLE kv (k VARCHAR(64) PRIMARY KEY, v INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSQLiteSubRollbackKeepsOuter(t *testing.T) {
	db := openFileDB(t, "sub.db")
	m := transactsqlite.New(db)
	ctx := context.Background()
	err := m.Run(ctx, func(tx transactsqlite.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO kv (k, v) VALUES ('a', 1)"); err != nil {
			return err
		}
		if serr := tx.Sub(ctx, func(sub transactsqlite.Tx) error {
			if _, err := sub.ExecContext(ctx, "INSERT INTO kv (k, v) VALUES ('b', 2)"); err != nil {
				return err
			}
			return errors.New("boom")
		}); serr == nil {
			t.Error("sub should have failed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM kv").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (only 'a')", n)
	}
}

func isBusy(err error, _ int) bool {
	var sqlErr *sqlite.Error
	if errors.As(err, &sqlErr) && sqlErr.Code()&0xff == 5 {
		return true
	}
	return strings.Contains(err.Error(), "BUSY")
}

func TestSQLiteRetryOnBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	dbW, err := transactsqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dbW.Close()
	if _, err := dbW.Exec("CREATE TABLE kv (k VARCHAR(64) PRIMARY KEY, v INTEGER NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	txW, err := dbW.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txW.Exec("INSERT INTO kv (k, v) VALUES ('w', 0)"); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(time.Second, func() { _ = txW.Commit() })

	db2, err := sql.Open("sqlite", "file:"+path+"?_txlock=immediate&_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	// Note: contention fails at BEGIN, before fn runs. Count attempts in the
	// matcher: it sees every failed try, including Begin failures.
	ctx := context.Background()
	maxTry := 0
	m2 := transactsqlite.New(db2)
	err = m2.Run(ctx, func(tx transactsqlite.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO kv (k, v) VALUES ('r', 1)")
		return err
	}, transactretry.New(
		transactretry.WithMaxAttempts(15),
		transactretry.WithRetryIf(func(err error, try int) bool {
			if try > maxTry {
				maxTry = try
			}
			return isBusy(err, try)
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if maxTry < 1 {
		t.Fatalf("maxTry = %d, want at least 1 failed try before success", maxTry)
	}
	var v int
	if err := db2.QueryRow("SELECT v FROM kv WHERE k = 'r'").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("v = %d, want 1", v)
	}
}

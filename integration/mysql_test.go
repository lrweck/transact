//go:build integration

package integration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	transactretry "github.com/lrweck/transact/retry"
	transactstdlib "github.com/lrweck/transact/stdlib"
)

func isDeadlock(err error, _ int) bool {
	var myErr *mysqlDriver.MySQLError
	return errors.As(err, &myErr) && myErr.Number == 1213
}

func TestMySQLRunCommits(t *testing.T) {
	cleanMy(t)
	ctx := context.Background()
	m := transactstdlib.New(myDB)
	err := m.Run(ctx, func(tx transactstdlib.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO kv (k, v) VALUES ('a', 1)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := myDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM kv").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestMySQLSubRollbackKeepsOuter(t *testing.T) {
	cleanMy(t)
	ctx := context.Background()
	m := transactstdlib.New(myDB)
	err := m.Run(ctx, func(tx transactstdlib.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO kv (k, v) VALUES ('a', 1)"); err != nil {
			return err
		}
		if serr := tx.Sub(ctx, func(sub transactstdlib.Tx) error {
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
	if err := myDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM kv").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (only 'a')", n)
	}
}

func TestMySQLRetryOnDeadlock(t *testing.T) {
	cleanMy(t)
	ctx := context.Background()
	if _, err := myDB.ExecContext(ctx, "INSERT INTO acct (id, bal) VALUES (1, 100), (2, 200)"); err != nil {
		t.Fatal(err)
	}
	m := transactstdlib.New(myDB)
	var deadlocks atomic.Int32

	// Two transactions update the same two rows in opposite order.
	// The channel order forces a lock cycle, so InnoDB kills one with 1213.
	// Either side can be the victim; both run under retry and converge.
	runTx := func(first, second int, held, wanted chan struct{}) error {
		firstTry := true
		return m.Run(ctx, func(tx transactstdlib.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE acct SET bal = bal - 1 WHERE id = ?", first); err != nil {
				return err
			}
			if firstTry {
				firstTry = false
				held <- struct{}{}
				if err := waitFor(wanted, 30*time.Second); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, "UPDATE acct SET bal = bal - 1 WHERE id = ?", second)
			return err
		}, transactretry.New(
			transactretry.WithMaxAttempts(5),
			transactretry.WithRetryIf(func(err error, try int) bool {
				if isDeadlock(err, try) {
					deadlocks.Add(1)
					return true
				}
				return false
			}),
		))
	}

	s1 := make(chan struct{}, 1)
	s2 := make(chan struct{}, 1)
	done := make(chan error, 2)
	go func() { done <- runTx(1, 2, s1, s2) }()
	go func() { done <- runTx(2, 1, s2, s1) }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("timed out waiting for deadlock test")
		}
	}

	if deadlocks.Load() < 1 {
		t.Fatalf("deadlocks = %d, want at least 1 retried victim", deadlocks.Load())
	}
	bals := map[int]int{}
	rows, err := myDB.QueryContext(ctx, "SELECT id, bal FROM acct ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, bal int
		if err := rows.Scan(&id, &bal); err != nil {
			t.Fatal(err)
		}
		bals[id] = bal
	}
	if len(bals) != 2 || bals[1] != 98 || bals[2] != 198 {
		t.Fatalf("bals = %v, want map[1:98 2:198]", bals)
	}
}

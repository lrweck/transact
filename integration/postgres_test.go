//go:build integration

package integration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lrweck/transact"
	transactpgx "github.com/lrweck/transact/pgx"
	transactretry "github.com/lrweck/transact/retry"
)

func isSerialization(err error, _ int) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

func TestPostgresRunCommits(t *testing.T) {
	cleanPG(t)
	ctx := context.Background()
	m := transactpgx.New(pgPool)
	err := m.Run(ctx, func(tx transactpgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO kv (k, v) VALUES ('a', 1)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pgPool.QueryRow(ctx, "SELECT COUNT(*) FROM kv").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestPostgresSubRollbackKeepsOuter(t *testing.T) {
	cleanPG(t)
	ctx := context.Background()
	m := transactpgx.New(pgPool)
	err := m.Run(ctx, func(tx transactpgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO kv (k, v) VALUES ('a', 1)"); err != nil {
			return err
		}
		if serr := tx.Sub(ctx, func(sub transactpgx.Tx) error {
			if _, err := sub.Exec(ctx, "INSERT INTO kv (k, v) VALUES ('b', 2)"); err != nil {
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
	if err := pgPool.QueryRow(ctx, "SELECT COUNT(*) FROM kv").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (only 'a')", n)
	}
}

func TestPostgresRetryOnSerializationFailure(t *testing.T) {
	cleanPG(t)
	ctx := context.Background()
	if _, err := pgPool.Exec(ctx, "INSERT INTO acct (id, bal) VALUES (1, 100)"); err != nil {
		t.Fatal(err)
	}
	m := transactpgx.New(pgPool)
	serializable := transact.JoinOptions(transact.WithIsoLevel(transact.LevelSerializable))

	readSeen := make(chan struct{})
	g2done := make(chan struct{})
	var tries atomic.Int32
	g1done := make(chan struct{})
	var g1err error
	go func() {
		defer close(g1done)
		g1err = m.Run(ctx, func(tx transactpgx.Tx) error {
			n := tries.Add(1)
			var bal int
			if err := tx.QueryRow(ctx, "SELECT bal FROM acct WHERE id = 1").Scan(&bal); err != nil {
				return err
			}
			if n == 1 {
				close(readSeen)
				if err := waitFor(g2done, 30*time.Second); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, "UPDATE acct SET bal = bal - 10 WHERE id = 1")
			return err
		}, serializable, transactretry.New(
			transactretry.WithMaxAttempts(3),
			transactretry.WithRetryIf(isSerialization),
		))
	}()

	recv(t, readSeen, "g1 read")
	err := m.Run(ctx, func(tx transactpgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE acct SET bal = bal - 10 WHERE id = 1")
		return err
	}, serializable)
	if err != nil {
		t.Fatal(err)
	}
	close(g2done)

	recv(t, g1done, "g1 finish")
	if g1err != nil {
		t.Fatal(g1err)
	}
	if tries.Load() != 2 {
		t.Fatalf("tries = %d, want 2 (one serialization failure)", tries.Load())
	}
	var bal int
	if err := pgPool.QueryRow(ctx, "SELECT bal FROM acct WHERE id = 1").Scan(&bal); err != nil {
		t.Fatal(err)
	}
	if bal != 80 {
		t.Fatalf("bal = %d, want 80", bal)
	}
}

func TestPostgresRetrySubComposition(t *testing.T) {
	cleanPG(t)
	ctx := context.Background()
	m := transactpgx.New(pgPool)
	boom := errors.New("boom")
	try := 0
	err := m.Run(ctx, func(tx transactpgx.Tx) error {
		try++
		if _, err := tx.Exec(ctx, "INSERT INTO kv (k, v) VALUES ('t', $1)", try); err != nil {
			return err
		}
		if err := tx.Sub(ctx, func(sub transactpgx.Tx) error {
			_, err := sub.Exec(ctx, "INSERT INTO kv (k, v) VALUES ('s', $1)", try)
			return err
		}); err != nil {
			return err
		}
		if try == 1 {
			return boom
		}
		return nil
	}, transactretry.New(
		transactretry.WithMaxAttempts(2),
		transactretry.WithRetryIf(func(err error, _ int) bool { return errors.Is(err, boom) }),
	))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := pgPool.Query(ctx, "SELECT k, v FROM kv ORDER BY k")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		got[k] = v
	}
	if len(got) != 2 || got["t"] != 2 || got["s"] != 2 {
		t.Fatalf("rows = %v, want map[s:2 t:2]", got)
	}
}

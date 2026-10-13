//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

var (
	pgPool *pgxpool.Pool
	myDB   *sql.DB
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgCtr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("transact"),
		postgres.WithUsername("tester"),
		postgres.WithPassword("secret"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres: %v", err)
	}
	myCtr, err := mysql.Run(ctx, "mysql:8.0.36",
		mysql.WithDatabase("transact"),
		mysql.WithUsername("tester"),
		mysql.WithPassword("secret"),
	)
	if err != nil {
		log.Fatalf("start mysql: %v", err)
	}

	code := 1
	defer func() {
		if err := testcontainers.TerminateContainer(pgCtr); err != nil {
			log.Printf("terminate postgres: %v", err)
		}
		if err := testcontainers.TerminateContainer(myCtr); err != nil {
			log.Printf("terminate mysql: %v", err)
		}
		os.Exit(code)
	}()

	pgConn, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("postgres conn string: %v", err)
	}
	pgPool, err = pgxpool.New(ctx, pgConn)
	if err != nil {
		log.Fatalf("pgxpool: %v", err)
	}
	defer pgPool.Close()

	myDSN, err := myCtr.ConnectionString(ctx, "parseTime=true")
	if err != nil {
		log.Fatalf("mysql dsn: %v", err)
	}
	myDB, err = sql.Open("mysql", myDSN)
	if err != nil {
		log.Fatalf("mysql open: %v", err)
	}
	defer myDB.Close()

	execSchema(ctx)
	code = m.Run()
}

func execSchema(ctx context.Context) {
	for _, q := range []string{
		"CREATE TABLE IF NOT EXISTS kv (k VARCHAR(64) PRIMARY KEY, v INTEGER NOT NULL)",
		"CREATE TABLE IF NOT EXISTS acct (id INTEGER PRIMARY KEY, bal INTEGER NOT NULL)",
	} {
		if _, err := pgPool.Exec(ctx, q); err != nil {
			log.Fatalf("pg schema: %v", err)
		}
		if _, err := myDB.ExecContext(ctx, q); err != nil {
			log.Fatalf("mysql schema: %v", err)
		}
	}
}

func cleanPG(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	if _, err := pgPool.Exec(ctx, "DELETE FROM kv"); err != nil {
		t.Fatal(err)
	}
	if _, err := pgPool.Exec(ctx, "DELETE FROM acct"); err != nil {
		t.Fatal(err)
	}
}

func cleanMy(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	if _, err := myDB.ExecContext(ctx, "DELETE FROM kv"); err != nil {
		t.Fatal(err)
	}
	if _, err := myDB.ExecContext(ctx, "DELETE FROM acct"); err != nil {
		t.Fatal(err)
	}
}

// recv waits for a signal with a timeout so a broken test fails instead of hanging.
// Only call it from the test goroutine. In spawned goroutines use waitFor,
// since testing.T must not fail from another goroutine.
func recv(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	if err := waitFor(ch, 30*time.Second); err != nil {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitFor(ch <-chan struct{}, d time.Duration) error {
	select {
	case <-ch:
		return nil
	case <-time.After(d):
		return errors.New("timed out")
	}
}

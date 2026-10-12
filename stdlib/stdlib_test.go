package transactstdlib

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/lrweck/transact"
)

func TestIsolationMapping(t *testing.T) {
	tests := []struct {
		name string
		in   transact.IsoLevel
		want sql.IsolationLevel
	}{
		{"default", transact.LevelDefault, sql.LevelDefault},
		{"read uncommitted", transact.LevelReadUncommitted, sql.LevelReadUncommitted},
		{"read committed", transact.LevelReadCommitted, sql.LevelReadCommitted},
		{"repeatable read", transact.LevelRepeatableRead, sql.LevelRepeatableRead},
		{"serializable", transact.LevelSerializable, sql.LevelSerializable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isolation(tt.in); got != tt.want {
				t.Fatalf("isolation = %v, want %v", got, tt.want)
			}
		})
	}
}

var registerFakeOnce sync.Once

func openFakeDB(c *fakeConn) *sql.DB {
	registerFakeOnce.Do(func() { sql.Register("transactfake", &fakeDriver{}) })
	fakeDriverConn = c
	db, err := sql.Open("transactfake", "")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

type fakeDriver struct{}

var fakeDriverConn *fakeConn

func (d *fakeDriver) Open(string) (driver.Conn, error) { return fakeDriverConn, nil }

type fakeConn struct {
	execs      []string
	execErr    error
	beginErr   error
	txOpts     driver.TxOptions
	committed  bool
	rolledBack bool
}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return &fakeStmt{}, nil }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	return &fakeDriverTx{conn: c}, nil
}
func (c *fakeConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.txOpts = opts
	return c.Begin()
}
func (c *fakeConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.execs = append(c.execs, query)
	if c.execErr != nil {
		return nil, c.execErr
	}
	return fakeResult{}, nil
}

type fakeStmt struct{}

func (s *fakeStmt) Close() error                               { return nil }
func (s *fakeStmt) NumInput() int                              { return -1 }
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) { return fakeResult{}, nil }
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error)  { return nil, errors.New("no rows") }

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 1, nil }

type fakeDriverTx struct{ conn *fakeConn }

func (t *fakeDriverTx) Commit() error   { t.conn.committed = true; return nil }
func (t *fakeDriverTx) Rollback() error { t.conn.rolledBack = true; return nil }

func mustBegin(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestBeginnerBeginPassesOptions(t *testing.T) {
	c := &fakeConn{}
	db := openFakeDB(c)
	defer db.Close()
	tx, err := beginner{db}.Begin(context.Background(), transact.Options{
		IsoLevel: transact.LevelSerializable,
		ReadOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if tx.Tx == nil || tx.sp != "" {
		t.Fatal("expected a root tx without savepoint")
	}
	if c.txOpts.Isolation != driver.IsolationLevel(sql.LevelSerializable) || !c.txOpts.ReadOnly {
		t.Fatalf("tx opts = %+v", c.txOpts)
	}
}

func TestBeginnerBeginError(t *testing.T) {
	c := &fakeConn{beginErr: errors.New("no conn")}
	db := openFakeDB(c)
	defer db.Close()
	b := beginner{db}
	if _, err := b.Begin(context.Background(), transact.Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestSavepointCommitReleases(t *testing.T) {
	c := &fakeConn{}
	db := openFakeDB(c)
	defer db.Close()
	tx := Tx{Tx: mustBegin(t, db)}
	sub, err := tx.Begin(context.Background(), transact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sub.sp == "" {
		t.Fatal("sub has no savepoint name")
	}
	if err := sub.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.execs) != 2 || !strings.HasPrefix(c.execs[0], "SAVEPOINT ") || !strings.HasPrefix(c.execs[1], "RELEASE SAVEPOINT ") {
		t.Fatalf("execs = %v", c.execs)
	}
}

func TestSavepointRollbackTo(t *testing.T) {
	c := &fakeConn{}
	db := openFakeDB(c)
	defer db.Close()
	tx := Tx{Tx: mustBegin(t, db)}
	sub, err := tx.Begin(context.Background(), transact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.execs) != 2 || !strings.HasPrefix(c.execs[1], "ROLLBACK TO SAVEPOINT ") {
		t.Fatalf("execs = %v", c.execs)
	}
}

func TestSavepointBeginError(t *testing.T) {
	c := &fakeConn{execErr: errors.New("locked")}
	db := openFakeDB(c)
	defer db.Close()
	tx := Tx{Tx: mustBegin(t, db)}
	if _, err := tx.Begin(context.Background(), transact.Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestSavepointNamesAreUnique(t *testing.T) {
	c := &fakeConn{}
	db := openFakeDB(c)
	defer db.Close()
	tx := Tx{Tx: mustBegin(t, db)}
	a, err := tx.Begin(context.Background(), transact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := tx.Begin(context.Background(), transact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if a.sp == b.sp {
		t.Fatalf("duplicate savepoint name %q", a.sp)
	}
}

func TestRootCommitAndRollback(t *testing.T) {
	c := &fakeConn{}
	db := openFakeDB(c)
	defer db.Close()
	root := Tx{Tx: mustBegin(t, db)}
	if err := root.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.committed {
		t.Fatal("driver tx was not committed")
	}
	c2 := &fakeConn{}
	db2 := openFakeDB(c2)
	defer db2.Close()
	raw, err := db2.Begin()
	if err != nil {
		t.Fatal(err)
	}
	root2 := Tx{Tx: raw}
	if err := root2.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c2.rolledBack {
		t.Fatal("driver tx was not rolled back")
	}
}

func TestManagerRunCommitsAndRollsBack(t *testing.T) {
	c := &fakeConn{}
	db := openFakeDB(c)
	defer db.Close()
	m := New(db)
	if err := m.Run(context.Background(), func(Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !c.committed {
		t.Fatal("run did not commit")
	}
	boom := errors.New("boom")
	c2 := &fakeConn{}
	db2 := openFakeDB(c2)
	defer db2.Close()
	m2 := New(db2)
	if err := m2.Run(context.Background(), func(Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if !c2.rolledBack {
		t.Fatal("run did not roll back")
	}
}

func TestSubKeepsOuterTxOnError(t *testing.T) {
	c := &fakeConn{}
	db := openFakeDB(c)
	defer db.Close()
	m := New(db)
	boom := errors.New("boom")
	err := m.Run(context.Background(), func(tx Tx) error {
		if err := tx.Sub(context.Background(), func(Tx) error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("sub err = %v, want boom", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c.committed {
		t.Fatal("outer tx was not committed")
	}
	var sawSavepoint, sawRollbackTo bool
	for _, q := range c.execs {
		if strings.HasPrefix(q, "SAVEPOINT ") {
			sawSavepoint = true
		}
		if strings.HasPrefix(q, "ROLLBACK TO SAVEPOINT ") {
			sawRollbackTo = true
		}
	}
	if !sawSavepoint || !sawRollbackTo {
		t.Fatalf("execs = %v, want SAVEPOINT and ROLLBACK TO", c.execs)
	}
}

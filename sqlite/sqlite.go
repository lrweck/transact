package transactsqlite

import (
	"database/sql"
	"net/url"

	_ "modernc.org/sqlite"

	"github.com/lrweck/transact"
	transactstdlib "github.com/lrweck/transact/stdlib"
)

// Tx is a SQLite transaction or savepoint. It has the same methods as
// transactstdlib.Tx.
type Tx = transactstdlib.Tx

// Manager is a transact.Manager for SQLite. Embed it in your repository to get Run.
type Manager = transactstdlib.Manager

// New returns a Manager for db. The options are defaults for every Run.
func New(db *sql.DB, opts ...transact.Option) *Manager {
	return transactstdlib.New(db, opts...)
}

// Open opens a SQLite file and returns its *sql.DB. Every connection uses WAL
// mode, a 5s busy timeout, and foreign keys. Transactions start as
// BEGIN IMMEDIATE, so concurrent writers wait instead of failing with SQLITE_BUSY.
func Open(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate")
	return sql.Open("sqlite", "file:"+path+"?"+q.Encode())
}

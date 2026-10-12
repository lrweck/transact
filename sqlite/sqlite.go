package transactsqlite

import (
	"database/sql"
	"net/url"

	_ "modernc.org/sqlite"

	"github.com/lweck/transact"
	transactstdlib "github.com/lweck/transact/stdlib"
)

// Tx is a SQLite transaction or savepoint.
type Tx = transactstdlib.Tx

// Manager is a transact.Manager bound to SQLite.
type Manager = transactstdlib.Manager

// New returns a Manager for db.
func New(db *sql.DB, opts ...transact.Option) *Manager {
	return transactstdlib.New(db, opts...)
}

// Open opens a SQLite file with WAL, busy_timeout and foreign keys on every connection.
// Transactions begin as BEGIN IMMEDIATE so concurrent writers queue instead of failing with SQLITE_BUSY.
func Open(path string) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate")
	return sql.Open("sqlite", "file:"+path+"?"+q.Encode())
}

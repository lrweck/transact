# transact

Transactions for Go database code. The callback gets the driver's native transaction, so any query layer works: sqlc, raw SQL, pgx batches, or your own builder.

## Modules

| Module | Import | Depends on |
| --- | --- | --- |
| core | `github.com/lrweck/transact` | nothing |
| pgx | `github.com/lrweck/transact/pgx` | `pgx/v5` |
| database/sql | `github.com/lrweck/transact/stdlib` | nothing (works with any `database/sql` driver) |
| SQLite | `github.com/lrweck/transact/sqlite` | `modernc.org/sqlite` |
| retry | `github.com/lrweck/transact/retry` | nothing (core only) |
| integration | `github.com/lrweck/transact/integration` | testcontainers (tests only, tag `integration`) |

Install only the one you use.

## Usage

### PostgreSQL with pgx

```go
type Repo struct {
    *transactpgx.Manager
    q *gen.Queries
}

func NewRepo(pool *pgxpool.Pool) *Repo {
    return &Repo{
        Manager: transactpgx.New(pool, transact.WithIsoLevel(transact.LevelReadCommitted)), // default for this repo
        q:       gen.New(pool),
    }
}

func (r *Repo) Transfer(ctx context.Context, from, to int64, v int) error {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second) // timeouts come from the context
    defer cancel()

    return r.Run(ctx, func(tx transactpgx.Tx) error {
        q := gen.New(tx)
        if err := debit(ctx, q, from, v); err != nil {
            return err
        }
        return tx.Sub(ctx, func(sub transactpgx.Tx) error { // savepoint
            return credit(ctx, gen.New(sub), to, v)
        })
    }, transact.WithIsoLevel(transact.LevelSerializable)) // per-call override
}
```

### MySQL (or Postgres) with database/sql

```go
import (
    _ "github.com/go-sql-driver/mysql" // or pgx/stdlib, modernc.org/sqlite
    transactstdlib "github.com/lrweck/transact/stdlib"
)

type Repo struct {
    *transactstdlib.Manager
    q *gen.Queries
}

func NewRepo(db *sql.DB) *Repo {
    return &Repo{
        Manager: transactstdlib.New(db),
        q:       gen.New(db),
    }
}

func (r *Repo) Transfer(ctx context.Context, from, to string, amount int) error {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    return r.Run(ctx, func(tx transactstdlib.Tx) error {
        q := gen.New(tx) // *sql.Tx satisfies sqlc and all query methods
        if err := debit(ctx, q, from, amount); err != nil {
            return err
        }
        return tx.Sub(ctx, func(sub transactstdlib.Tx) error {
            return credit(ctx, gen.New(sub), to, amount)
        })
    })
}
```

`Run` begins a transaction, calls the callback, then commits. It rolls back if the callback returns an error or panics.

Options are values. Options set on `New` are defaults; options passed to `Run` override them for that call. `JoinOptions` merges sets by hand — properties set later win, so an explicit default overrides an earlier value:

```go
opts := transact.JoinOptions(
    transact.WithIsoLevel(transact.LevelSerializable),
    transact.WithReadOnly(),
)
```

`Manager` is itself a `Beginner`, so you can pass it anywhere a `Beginner` works, like `transact.Run` or another `Manager`.

## Subtransactions

`tx.Sub` runs a callback inside a savepoint. If the callback fails, only its work is rolled back and the outer transaction continues. Supported on PostgreSQL, MySQL (InnoDB), and SQLite.

A savepoint can't change isolation, read-only, or deferrable mode. Passing any of them to `Sub` returns `transact.ErrSavepointOptions` before anything runs.

## SQLite

```go
db, err := transactsqlite.Open("app.db") // WAL, busy_timeout, foreign_keys, BEGIN IMMEDIATE
m := transactsqlite.New(db)
```

`Open` returns a `*sql.DB` so you can call `Close` and pass it to sqlc.

## Retry

```go
import transactretry "github.com/lrweck/transact/retry"

m := transactpgx.New(pool,
    transactretry.New(
        transactretry.WithMaxAttempts(5),
        transactretry.WithBeginCommitTimeout(2*time.Second),  // bounds each try's begin and commit
        transactretry.WithTotalTimeout(30*time.Second),  // all tries together
        transactretry.WithRetryIf(func(err error, try int) bool { return isSerializationError(err) }),
        transactretry.WithBackoff(transactretry.ExponentialBackoff(100*time.Millisecond)),
        transactretry.WithJitter(100*time.Millisecond, 2*time.Second),
    ),
    transact.WithIsoLevel(transact.LevelSerializable),
)

m.Run(ctx, func(tx transactpgx.Tx) error { ... }) // retries on failure
```

`retry.New` returns `transact.Options`, so it goes next to the other options. Each try runs through `transact.Run`: a failed try is rolled back before the next one starts. `BeginCommitTimeout` bounds begin and commit, not the callback — queries use your context. A panic rolls back and panics again without retry.

## Writing an adapter

An adapter implements `transact.Beginner[T]` for your pool, where `T` has `Commit(ctx)` and `Rollback(ctx)`. For nesting, `T` must also implement `Begin(ctx, transact.Options) (T, error)`. See `pgx/pgx.go` for the smallest example.

Check your adapter with `transacttest.Conformance`. It runs the contract every adapter must meet: commit, rollback on error and on panic, savepoints keeping or dropping their work, and savepoints rejecting options. You give it a `Store` with four small functions (reset, insert, count) against a table with one integer column:

```go
transacttest.Conformance(t, transacttest.Store[transactstdlib.Tx]{
    Beginner: transactstdlib.New(db),
    Reset:    func(ctx context.Context) error { _, err := db.ExecContext(ctx, "DELETE FROM conf"); return err },
    Insert:   func(ctx context.Context, tx transactstdlib.Tx, id int) error { _, err := tx.ExecContext(ctx, "INSERT INTO conf (id) VALUES (?)", id); return err },
    Count:    func(ctx context.Context, tx transactstdlib.Tx) (int, error) { var n int; err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conf").Scan(&n); return n, err },
})
```

The suite runs against a real database, so put it in your adapter's integration tests.

## Design notes

The retry loop lives in the core `Run`, not in the `retry` module. A `Beginner` wraps one `Begin` call, and retry has to wrap the whole `Run` (begin, callback, commit, rollback). So the core owns the loop, and `retry` only builds the policy that `Run` reads. The `retry` module stays a small constructor with no database dependencies.

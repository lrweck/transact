# transact

Transactions for Go database code. The callback gets the driver's native transaction, so any query layer works: sqlc, raw SQL, pgx batches, or your own builder.

## Modules

| Module | Import | Depends on |
| --- | --- | --- |
| core | `github.com/lrweck/transact` | nothing |
| pgx | `github.com/lrweck/transact/pgx` | `pgx/v5` |
| database/sql | `github.com/lrweck/transact/stdlib` | nothing (works with any `database/sql` driver) |
| SQLite | `github.com/lrweck/transact/sqlite` | `modernc.org/sqlite` |

Install only the one you use.

## Usage

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

`Run` begins a transaction, calls the callback, then commits. It rolls back if the callback returns an error or panics.

Options set on `New` are defaults. Options passed to `Run` override them.

## Subtransactions

`tx.Sub` runs a callback inside a savepoint. If the callback fails, only its work is rolled back and the outer transaction continues. Supported on PostgreSQL, MySQL (InnoDB), and SQLite. `Sub` ignores isolation and read-only options, since those can't change inside a savepoint.

## SQLite

```go
db, err := transactsqlite.Open("app.db") // WAL, busy_timeout, foreign_keys, BEGIN IMMEDIATE
m := transactsqlite.New(db)
```

`Open` returns a `*sql.DB` so you can call `Close` and pass it to sqlc.

## Writing an adapter

An adapter implements `transact.Beginner[T]` for your pool, where `T` has `Commit(ctx)` and `Rollback(ctx)`. For nesting, `T` must also implement `Begin(ctx, transact.Options) (T, error)`. See `pgx/pgx.go` for the smallest example.

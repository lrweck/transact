# transact

### Tired of transactions that just don't stick?

Commit. Rollback. Savepoints. Retries. **All of it, in one callback.**

transact is the transaction layer your database code has been begging for. Hand it a pool, hand it a function, and walk away. It begins the transaction, runs your queries, commits on success, and rolls back the instant something goes wrong. Panic in the middle? Rolled back. Serialization failure? Retried. A nested piece of work fails? Only that piece is undone.

**It works with EVERYTHING.** sqlc. Raw SQL. pgx batches. Your homemade query builder from 2019. If it can run on the transaction, it runs inside transact. No glue. No adapters for your query layer. Just the native transaction, handed to you, warm.

---

## Modules

Install only what you use. Nothing is bundled. Nothing sneaks in.

| Module | Import | Depends on |
| --- | --- | --- |
| core | `github.com/lrweck/transact` | nothing. Zero. Nada. |
| pgx | `github.com/lrweck/transact/pgx` | `pgx/v5` |
| database/sql | `github.com/lrweck/transact/stdlib` | nothing (any `database/sql` driver: MySQL, Postgres, SQLite, you name it) |
| SQLite | `github.com/lrweck/transact/sqlite` | `modernc.org/sqlite` |
| retry | `github.com/lrweck/transact/retry` | nothing (core only) |
| integration | `github.com/lrweck/transact/integration` | testcontainers (tests only, tag `integration`) |

---

## See it work

### PostgreSQL with pgx

```go
type Repo struct {
    *transactpgx.Manager
    q *gen.Queries
}

func NewRepo(pool *pgxpool.Pool) *Repo {
    return &Repo{
        Manager: transactpgx.New(pool, transact.WithIsoLevel(transact.LevelReadCommitted)), // your default, set once
        q:       gen.New(pool),
    }
}

func (r *Repo) Transfer(ctx context.Context, from, to int64, v int) error {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second) // timeouts? Your context. Obviously.
    defer cancel()

    return r.Run(ctx, func(tx transactpgx.Tx) error {
        q := gen.New(tx)
        if err := debit(ctx, q, from, v); err != nil {
            return err // rolled back. Automatically. Before you finish your coffee.
        }
        return tx.Sub(ctx, func(sub transactpgx.Tx) error { // a savepoint, for the brave
            return credit(ctx, gen.New(sub), to, v)
        })
    }, transact.WithIsoLevel(transact.LevelSerializable)) // per-call override. Because you're worth it.
}
```

### MySQL (or any database/sql driver) with database/sql

```go
import (
    _ "github.com/go-sql-driver/mysql" // or pgx/stdlib, or modernc.org/sqlite
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
        q := gen.New(tx) // *sql.Tx slides right into sqlc. No questions asked.
        if err := debit(ctx, q, from, amount); err != nil {
            return err
        }
        return tx.Sub(ctx, func(sub transactstdlib.Tx) error {
            return credit(ctx, gen.New(sub), to, amount)
        })
    })
}
```

That's it. No ceremony. No boilerplate. No "just one more wrapper."

---

## Options that actually stick

Options are plain values. Set them once on `New`. Override them per call. Done.

```go
opts := transact.JoinOptions(
    transact.WithIsoLevel(transact.LevelSerializable),
    transact.WithReadOnly(),
)
```

Later options win, property by property, like `encoding/json/v2` (no, really). An explicit default overrides an earlier value, so you can always say "actually, no" and mean it.

`Manager` is itself a `Beginner`. Drop it anywhere a `Beginner` goes: `transact.Run`, another `Manager`, your tests, your dreams.

---

## Subtransactions: undo the little mistake, keep the big win

`tx.Sub` runs a callback inside a savepoint. If it fails, **only its work disappears**. The outer transaction keeps going, unbothered.

Works on **PostgreSQL, MySQL (InnoDB), and SQLite**. Savepoints are the one thing you don't have to hope for.

A savepoint can't change isolation, read-only, or deferrable mode, because the database says so. Try it anyway and you get `transact.ErrSavepointOptions` back before a single byte hits the wire.

---

## SQLite: one writer at a time, and it actually waits its turn

```go
db, err := transactsqlite.Open("app.db") // WAL, busy_timeout, foreign_keys, BEGIN IMMEDIATE. All of it. Pre-set.
m := transactsqlite.New(db)
```

`Open` hands you a `*sql.DB`, so `Close` works and sqlc takes it without complaint. A second writer waits up to 5 seconds for its turn instead of crashing into `SQLITE_BUSY` on the spot. Add retry and it gets even more patience.

---

## Retry: the second chance your transaction deserves

```go
import transactretry "github.com/lrweck/transact/retry"

m := transactpgx.New(pool,
    transactretry.New(
        transactretry.WithMaxAttempts(5),
        transactretry.WithBeginCommitTimeout(2*time.Second), // begin + commit get their own clock
        transactretry.WithTotalTimeout(30*time.Second),      // the whole operation, all tries, one deadline
        transactretry.WithRetryIf(func(err error, try int) bool {
            log.Printf("try %d failed: %v", try, err)        // log every failure. Be the hero of your on-call rotation.
            return isSerializationError(err)
        }),
        transactretry.WithBackoff(transactretry.ExponentialBackoff(100*time.Millisecond)),
        transactretry.WithJitter(100*time.Millisecond, 2*time.Second),
    ),
    transact.WithIsoLevel(transact.LevelSerializable),
)

m.Run(ctx, func(tx transactpgx.Tx) error { ... }) // fails? Tries again. Clean slate every time.
```

- Every try goes through `transact.Run`. A failed try is rolled back before the next one starts. No half-written mess follows you around.
- Waits are cancelable. Your context stops the sleep dead, mid-nap.
- A panic rolls back and panics again. Retry doesn't hide bugs. It just keeps the database tidy while you fix them.

`BeginCommitTimeout` bounds begin and commit. Queries use your context. Yes, you're in charge. That's the point.

---

## Writing an adapter (and proving it works)

An adapter implements `transact.Beginner[T]` for your pool, where `T` has `Commit(ctx)` and `Rollback(ctx)`. For subtransactions, `T` also implements `Begin(ctx, transact.Options) (T, error)`. The smallest example is `pgx/pgx.go`.

Then run `transacttest.Conformance` against it. It checks the whole contract against a real database: commit, rollback on error, rollback on panic, savepoints keeping or dropping their work, and savepoints rejecting options. You give it four small functions and a table with one integer column:

```go
transacttest.Conformance(t, transacttest.Store[transactstdlib.Tx]{
    Beginner: transactstdlib.New(db),
    Reset:    func(ctx context.Context) error { _, err := db.ExecContext(ctx, "DELETE FROM conf"); return err },
    Insert:   func(ctx context.Context, tx transactstdlib.Tx, id int) error { _, err := tx.ExecContext(ctx, "INSERT INTO conf (id) VALUES (?)", id); return err },
    Count:    func(ctx context.Context, tx transactstdlib.Tx) (int, error) { var n int; err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conf").Scan(&n); return n, err },
})
```

Passes? Your adapter is done. Fails? You just found the bug before your users did.

---

### Ready to stop losing transactions?

```sh
go get github.com/lrweck/transact
```

Pick your adapter. Write your callback. **Let it run.**

*Transactions: now with 100% more rollback.*

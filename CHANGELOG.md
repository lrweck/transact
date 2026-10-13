# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Each module in this repo is versioned on its own tag:

| Module | Tag |
| --- | --- |
| core (`github.com/lrweck/transact`) | `v1.0.0` |
| pgx (`github.com/lrweck/transact/pgx`) | `pgx/v1.0.0` |
| database/sql (`github.com/lrweck/transact/stdlib`) | `stdlib/v1.0.0` |
| SQLite (`github.com/lrweck/transact/sqlite`) | `sqlite/v1.0.0` |
| retry (`github.com/lrweck/transact/retry`) | `retry/v1.0.0` |

The `integration` module is test-only and is not tagged.

## [1.0.0] - 2026-10-12

First stable release. From here on, the public API follows semver: breaking changes need a major version bump.

### Added

**Core (`transact`)**

- `Run` starts a transaction, calls your callback with it, and commits. It rolls back when the callback returns an error or panics, and re-panics after the rollback.
- `Beginner[T]`, the interface every adapter implements, and `Manager[T]`, which holds a `Beginner` plus default options. `Manager` is itself a `Beginner`, so it can be passed anywhere one is expected.
- Options are values: `WithIsoLevel`, `WithReadOnly`, `WithDeferrable`, `WithRetry`. `JoinOptions` merges them, with later options winning property by property.
- `ErrSavepointOptions`, returned when a subtransaction is given isolation, read-only, or deferrable options.
- `RetryPolicy`, the policy type that `Run` reads when `WithRetry` is set. The `retry` module builds it.

**pgx (`transact/pgx`)**

- `New(pool, opts...)` returns a `Manager` for `*pgxpool.Pool` or `*pgx.Conn`.
- `Tx` embeds `pgx.Tx`, so every query method works. `Tx.Sub` runs a callback inside a savepoint.
- Isolation, read-only, and deferrable options map to `pgx.TxOptions`.

**database/sql (`transact/stdlib`)**

- `New(db, opts...)` returns a `Manager` for any `*sql.DB`. This covers MySQL, Postgres through `database/sql`, SQLite, and any other `database/sql` driver.
- `Tx.Sub` runs a callback inside a savepoint. Savepoint names are unique per process.

**SQLite (`transact/sqlite`)**

- `Open(path)` returns a `*sql.DB` with WAL mode, `busy_timeout`, foreign keys on, and `BEGIN IMMEDIATE` for every transaction.
- `New(db, opts...)` returns a `Manager`.

**Retry (`transact/retry`)**

- `New(opts...)` returns a `transact.Options` that enables retries. Options: `WithMaxAttempts`, `WithBeginCommitTimeout`, `WithTotalTimeout`, `WithRetryIf`, `WithBackoff`, `WithJitter`.
- Backoff strategies: `ConstantBackoff`, `LinearBackoff`, `ExponentialBackoff`.
- `WithRetryIf` receives the error and the number of the try that failed, so it can log every attempt.
- Waits stop immediately when the context is done.

**Conformance (`transact/transacttest`)**

- `Conformance` runs the contract every adapter must meet against a real database: commit, rollback on error, rollback on panic, savepoints keeping or dropping their work, and savepoints rejecting options.

**Testing and CI**

- Unit tests with 100% statement coverage in every module that has code, run with `-race`.
- Integration tests against real Postgres 16 and MySQL 8.0 through testcontainers, behind the `integration` build tag.
- A single GitHub Actions job runs gofmt, vet across all modules, the unit tests, and the integration tests.

### Notes and known limits

- **`BeginCommitTimeout` bounds begin and commit only.** Queries use the context you pass in. This is by design: one context controls the work, and the timeout controls the transaction boundaries.
- **The database/sql root commit takes no context.** `*sql.Tx.Commit` cannot be canceled once it starts. Stop the work with the context before the commit, not during it.
- **A failed `Commit` is followed by a `Rollback` call.** The rollback error is ignored. Drivers treat a rollback after commit as a no-op error, so no data is affected.
- **SQLite pragmas are fixed by `Open`.** To change them, build the DSN yourself and call `New` on the resulting `*sql.DB`.
- **Subtransactions need PostgreSQL, MySQL (InnoDB), or SQLite.** Other databases may not support savepoints.

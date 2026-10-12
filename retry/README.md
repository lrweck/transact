# transact/retry

Builds retry policies for transact. `New` returns `transact.Options`, so it plugs into any `Manager`:

```go
import transactretry "github.com/lrweck/transact/retry"

m := transactpgx.New(pool,
    transactretry.New(
        transactretry.WithMaxAttempts(5),
        transactretry.WithPerTryTimeout(2*time.Second),
        transactretry.WithTotalTimeout(30*time.Second),
        transactretry.WithRetryIf(func(err error) bool { return isTransient(err) }),
        transactretry.WithBackoff(transactretry.ExponentialBackoff(100*time.Millisecond)),
        transactretry.WithJitter(100*time.Millisecond, 2*time.Second),
    ),
    transact.WithIsoLevel(transact.LevelSerializable),
)

m.Run(ctx, func(tx transactpgx.Tx) error { ... }) // retries on failure
```

Each try runs through `transact.Run`, so a failed try is rolled back before the next one starts.

## Options

| RetryOption | Meaning |
| --- | --- |
| `WithMaxAttempts` | Total tries, including the first. Below 1 means 1 (no retry). Default is 1. |
| `WithPerTryTimeout` | Bounds each try's begin and commit, not the callback: queries use your context. Zero means none. |
| `WithTotalTimeout` | Timeout for all tries together. Zero means none. |
| `WithRetryIf` | Sets the matcher for retryable errors. The last call wins. The matcher gets the error and the 1-based failed try number. Nil retries every error. |
| `WithBackoff` | Wait strategy between tries. Default is no wait. |
| `WithJitter(min, max)` | Randomizes the wait into `[min, max]`. |

## Backoff

Three strategies: `ConstantBackoff`, `LinearBackoff` (base, 2x, 3x, ...), and `ExponentialBackoff` (base, 2x, 4x, ...). The wait happens between tries and stops early when the context is done.

`WithJitter(min, max)` randomizes the wait into `[min, max]`. Without a strategy the wait is uniform in `[min, max]`. With a strategy, the strategy value caps the range: uniform in `[min, strategy]`, clamped to `[min, max]`. A strategy below `min` waits `min`.

A panic rolls back and panics again without retry. When the context is done, `Run` stops and returns the context error. Otherwise it returns the last error wrapped with the try count, so `errors.Is` and `errors.As` still work.

## RetryIf

`RetryIf` runs on the try's error, after the rollback and before the backoff sleep. It gets the error and the 1-based number of the try that just failed, so it can log the attempt. Returning false stops and returns the error. Panics and context errors never reach it.

One matcher with plain `||` covers every case:

```go
transactretry.WithRetryIf(func(err error, try int) bool {
    log.Printf("try %d failed: %v", try, err)
    return isSerialization(err) || isDeadlock(err) || isBusy(err)
})
```

Write one small helper per transient error and OR them:

```go
isSerialization := func(err error) bool {
    var pgErr *pgconn.PgError // already a dependency if you use pgx
    return errors.As(err, &pgErr) && pgErr.Code == "40001"
}
isDeadlock := func(err error) bool {
    var pgErr *pgconn.PgError
    return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}
isBusy := func(err error) bool {
    return strings.Contains(err.Error(), "SQLITE_BUSY")
}
```

Use `errors.As`/`errors.Is` inside matchers: the error arrives wrapped with the try count. The library ships no matchers on purpose — they would drag driver dependencies into this module. Your app already depends on its driver, so the matchers live there.

Set `RetryIf` in production. Retrying every error wastes tries on errors that never go away, like constraint violations. Retry transient errors: serialization failures, deadlocks, and `SQLITE_BUSY`.

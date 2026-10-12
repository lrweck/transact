# transact/retry

Retries failed transactions. Each try runs through `transact.Run`, so a failed try is rolled back before the next one starts.

```go
import transactretry "github.com/lrweck/transact/retry"

err := transactretry.Run(ctx, manager, func(ctx context.Context, tx transactpgx.Tx) error {
    return gen.New(tx).Insert(ctx, ...)
},
    transactretry.WithMaxAttempts(5),
    transactretry.WithPerTryTimeout(2*time.Second),
    transactretry.WithTotalTimeout(30*time.Second),
    transactretry.WithRetryIf(func(err error) bool { return isTransient(err) }),
    transactretry.WithBackoff(transactretry.ExponentialBackoff(100*time.Millisecond)),
    transactretry.WithJitter(100*time.Millisecond, 2*time.Second),
    transactretry.WithTxOptions(transact.WithIsoLevel(transact.LevelSerializable)),
)
```

## Policy

| Option | Meaning |
| --- | --- |
| `WithMaxAttempts` | Total tries, including the first. Below 1 means 1 (no retry). Default is 1. |
| `WithPerTryTimeout` | Each try gets a context with this timeout. Zero means none. Use the context the callback gets, or the timeout only covers begin and commit. |
| `WithTotalTimeout` | Timeout for all tries together. Zero means none. |
| `WithRetryIf` | Return false to stop retrying an error. Default retries every error. |
| `WithBackoff` | Wait strategy between tries. Default is no wait. |
| `WithJitter(min, max)` | Randomizes the wait into `[min, max]`. |
| `WithTxOptions` | Transaction options for every try. |

A panic rolls back and panics again without retry. When the context is done, `Run` stops and returns the context error. Otherwise it returns the last error wrapped with the try count, so `errors.Is` and `errors.As` still work.

## Backoff

Three strategies: `ConstantBackoff`, `LinearBackoff` (base, 2x, 3x, ...), and `ExponentialBackoff` (base, 2x, 4x, ...). The wait happens between tries and stops early when the context is done.

`WithJitter(min, max)` randomizes the wait into `[min, max]`. Without a strategy the wait is uniform in `[min, max]`. With a strategy, the strategy value caps the range: uniform in `[min, strategy]`, clamped to `[min, max]`. A strategy below `min` waits `min`.

Set `RetryIf` in production. Retrying every error wastes tries on errors that never go away, like constraint violations. Retry transient errors: serialization failures, deadlocks, and `SQLITE_BUSY`.

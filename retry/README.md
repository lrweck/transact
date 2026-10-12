# transact/retry

Retries failed transactions. Each try runs through `transact.Run`, so a failed try is rolled back before the next one starts.

```go
import transactretry "github.com/lrweck/transact/retry"

err := transactretry.Run(ctx, manager, func(ctx context.Context, tx transactpgx.Tx) error {
    return gen.New(tx).Insert(ctx, ...)
}, transactretry.Policy{
    MaxAttempts:   5,
    PerTryTimeout: 2 * time.Second,
    TotalTimeout:  30 * time.Second,
    RetryIf:       func(err error) bool { return isTransient(err) },
}, transact.WithIsoLevel(transact.LevelSerializable))
```

## Policy

| Field | Meaning |
| --- | --- |
| `MaxAttempts` | Total tries, including the first. Below 1 means 1 (no retry). |
| `PerTryTimeout` | Each try gets a context with this timeout. Zero means none. Use the context the callback gets, or the timeout only covers begin and commit. |
| `TotalTimeout` | Timeout for all tries together. Zero means none. |
| `RetryIf` | Return false to stop retrying an error. Nil retries every error. |

A panic rolls back and panics again without retry. When the context is done, `Run` stops and returns the context error. Otherwise it returns the last error wrapped with the try count, so `errors.Is` and `errors.As` still work.

Set `RetryIf` in production. Retrying every error wastes tries on errors that never go away, like constraint violations. Retry transient errors: serialization failures, deadlocks, and `SQLITE_BUSY`.

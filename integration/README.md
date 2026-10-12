# transact/integration

Integration tests against real databases with testcontainers. Optional module: install it only to run these tests.

```sh
cd integration
go test -tags integration -timeout 10m ./...
```

Needs Docker. Postgres and MySQL run in containers (images `postgres:16-alpine` and `mysql:8.0.36`, pulled on first run). SQLite uses temp files, no container.

## What it covers

| Test | Postgres (pgx) | MySQL (stdlib) | SQLite |
| --- | --- | --- | --- |
| Run commits | yes | yes | — (unit-tested) |
| Sub rollback keeps outer work | yes | yes | yes |
| Retry on transient error | 40001 serialization failure | 1213 deadlock | BUSY on locked DB |
| Retry + Sub composition | yes | — | — |

## Retry vs subtransactions

Retry works on whole root transactions; subtransactions work inside one try. A failed sub rolls back to its savepoint and the outer transaction goes on — retry never hears about it. A failed outer transaction rolls the whole thing back and the next try starts clean: savepoints die with their transaction.

Per database:

- **Postgres**: serialization failures (40001) usually surface at COMMIT, after all the work. Retry re-runs the whole transaction. Savepoints behave the same inside every try.
- **MySQL (InnoDB)**: a deadlock (1213) kills the victim's whole transaction. Retry re-runs it from scratch. `ROLLBACK TO SAVEPOINT` leaves later savepoints usable, which is all this library needs.
- **SQLite**: one writer at a time. With `BEGIN IMMEDIATE` (this library's `Open`), a second writer blocks up to `busy_timeout`, then fails with BUSY. Retry waits it out. Note the failure can happen at BEGIN, before the callback runs — retry covers that too.

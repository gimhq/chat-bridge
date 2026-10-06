# SQLite with hand-written SQL and embedded migrations

- **date**: 2026-10-03
- **scope**: `internal/store`
- **sunset**: none; review if the bridge ever needs a server database or several writers

## Decision

`/pma-go` defaults to sqlc with pgx for data access and goose for migrations. chat-bridge uses
SQLite through `modernc.org/sqlite`, SQL written by hand in `internal/store`, and migrations kept
as numbered SQL strings in the binary (`internal/store/store.go`, `docs/storage.md` §8).

## Why

The bridge is a single self-hosted process whose whole state lives in one data directory. SQLite
needs no second service, and the modernc driver keeps the default build free of cgo, which the
prebuilt release binaries depend on. pgx does not apply to SQLite. The schema is one file and the
queries rely on SQLite features (FTS5 with trigram tokens, `json_extract`, row values) and on
dynamic filters, so generated query code would cover little of it. Embedded migrations run at
startup inside the same binary, so an upgrade is replacing the executable; goose would add a
second tool and a migrations directory to ship for the same result.

## Revisit

If a deployment needs PostgreSQL or the migration count grows past what one file holds, move to
sqlc and goose and delete this record.

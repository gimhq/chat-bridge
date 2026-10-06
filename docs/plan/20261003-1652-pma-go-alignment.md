# 20261003-1652-pma-go-alignment Align with the pma-go baseline: lint to green, comments, API spec sync

- **status**: completed
- **createdAt**: 2026-10-03 16:52
- **approvedAt**: 2026-10-03 17:20
- **relatedTask**: 20261003-1652-pma-go-alignment

## Context

Baseline, measured in `golangci/golangci-lint:v2.14.0` (Go 1.27.1) on `cbd7283`:

| Gate | Result |
|---|---|
| `gofmt -l .` | clean |
| `go vet -tags goolm ./...` | clean |
| `go test -tags goolm -cover ./...` | pass; core 70.1%, server 74.9%, store 73.9%, config 88.1%, remote 71.6%, connector 50.6%, telegram 29.3%, whatsapp 24.8%, matrix 13.2% |
| `go mod tidy` | no diff |
| `golangci-lint run` | **139 issues**: revive 100, gosec 18, errcheck 10, staticcheck 9, errorlint 1, ineffassign 1 |

Lint findings by kind:

- revive `exported` (about 90): exported methods without a doc comment, almost all adapter
  interface implementations in `adapter/fake`, `adapters/remote`, `adapters/connector`,
  `adapters/telegram`, `adapters/matrix`.
- revive `unused-parameter` (7), `redefines-builtin-id` (3: `cap`, `close`).
- errcheck (10): unchecked `Close` / `os.Remove` in `connector/matrix.go` and `remote_test.go`.
- gosec (18): G115 uint64→int64 in `whatsapp/convert.go` (5), G118 background context in
  goroutines (4), G120 unbounded multipart parsing in `server/handlers.go` (2), G304/G703 file
  paths (4), G705, G117, G101.
- staticcheck (9): SA1019 deprecated mautrix / gotd calls (6), QF1008 (3).

Comments:

- Four comments reference `docs/chat-api-spec.md`, which does not exist (the spec is
  `docs/api.md`): `internal/server/server.go:1`, `internal/model/model.go:2`,
  `internal/core/core.go:24`, `internal/adapter/adapter.go:16`.

Spec versus implementation (`docs/api.md`, `docs/storage.md`):

| Spec | State |
|---|---|
| `api.md` header and §12 | describe a migration from a WhatsApp-only API that this repository never shipped |
| `storage.md` §10 | same: "what the current implementation must change" lists a schema that is not in the code |
| §3.2 `contact.request`, `chat.invite`, `chat.join_request`, `call.reject` | not declared by any adapter; `RequestAnswerer` has no capability by design |
| §3.5 `send.poll` | referenced, not defined anywhere |
| §4.3 `PATCH chats`: "the rest reach the platform" | only `name` reaches the platform; `muted` and `archived` are bridge-local; the error names capability `chat.update`, absent from §3.2 |
| §2 `?raw=1` "on message reads" | honored on `GET /messages/{msg}` only |
| §4.5 upload `kind` | form field ignored |
| §5 Matrix `sso` flow | not implemented; §4.1 lists `password` and `token` |
| §10 `config.token_accounts` | not implemented; one global token |
| §4.7a | numbered before §4.7 |
| `storage.md` §7 retention | only event pruning, upload expiry and request expiry run; `retention.*` and `media.max_gb` keys do not exist. This is the open part of task `20260913-0530-api-framework-completion`, claimed by `worker-a/session-chatbridge-01` |
| `chat-bridge.example.yaml`, `.env.example` | lack `persons.auto_link_by_phone`; `.env.example` lacks `CHATBRIDGE_ADAPTERS_TELEGRAM_BRIDGEV2` |

pma-go baseline:

| Item | Baseline | Repository |
|---|---|---|
| Go | 1.27+, `go` and `toolchain` directives | `go 1.26.0`, no `toolchain`; `golang:1.26-alpine` in the Dockerfile |
| `.golangci.yml` | starter: explicit linter list with `gocritic`, test-scoped exclusions, timeout | `default: standard` plus revive, gosec, errorlint, misspell |
| Gates | adds `go mod tidy` with no diff and coverage | neither is in the documented gate |
| Task runner | `Taskfile.yml` | none; the gate is a one-line command in README and AGENTS |
| Data access, migrations, CLI | sqlc + pgx, goose, Cobra (defaults) | modernc SQLite with hand-written SQL, embedded migrations, stdlib `flag`; not recorded in `docs/decisions/` |
| HTTP | health and readiness, timeouts where appropriate | `/healthz` only; `ReadHeaderTimeout` only |
| File size | usually under 800 lines | `connector/host.go` 1001, `server/handlers.go` 867 |
| Dependencies | latest stable | nine direct modules have newer versions (mautrix 0.31.0, whatsmeow, gotd 0.162.0, sqlite 1.60.1, …) |

Logging (slog), config layering (koanf + validator), graceful shutdown, router layout, error
model and constant-time token comparison already match the baseline.

## Proposal

Four phases, each leaving every gate green and committed separately.

1. **Lint to green and comments.** Add doc comments to the exported methods; fix the four stale
   spec references; rename shadowed builtins and unused parameters; check or explicitly discard
   the unchecked errors; replace the deprecated calls where the replacement is a rename
   (`GetUserLogins`, `MemberMap`, `Duration`, in-place encrypt/decrypt); bound multipart parsing
   in the two upload handlers; guard the uint64→int64 conversions. Findings that are false
   positives (generated paths, test fixtures, deliberate background contexts) get a scoped
   `//nolint` with the reason, or a path-scoped exclusion for tests.
2. **API spec sync.** Make `api.md` and `storage.md` state what the server does: drop the
   migration sections, the undeclared capabilities, `send.poll` and the `sso` flow; correct the `PATCH chats` wording and renumber §4.7a. Implement the two
   small gaps instead of removing them: `?raw=1` on message lists, and `kind` on upload. Add the
   missing keys to the example config and `.env.example`. Tests first for the two behaviors.
3. **Baseline tooling.** Go 1.27 with a `toolchain` directive and the matching Dockerfile image;
   `.golangci.yml` on the pma-go starter (keeping errorlint); `Taskfile.yml` with `check`
   mirroring the gates including the tidy check; `/readyz` that pings the store; decision records
   for SQLite/hand-written SQL/embedded migrations and stdlib `flag`.
4. **Dependency bumps**, as their own `chore(deps)` commit, after reading the mautrix, whatsmeow
   and gotd release notes.

Not in this plan: retention and GC (`storage.md` §7), because the task that owns it is claimed
by another session; splitting `host.go` and `handlers.go`; raising adapter test coverage.

## Risks

- Phase 1 touches adapter code paths that only run against live platforms (deprecated mautrix
  and gotd calls, in-place media encryption). Unit tests cover conversions, not live sends.
- Go 1.27 changes the toolchain for every build, including the release workflow, which reads
  `go.mod`.
- New linters (`gocritic`) will report further findings that have to be fixed in the same phase.
- Dependency bumps on unofficial protocol libraries can change behavior; they are isolated in
  phase 4 so they can be reverted alone.
- Removing spec sections is a contract change for any consumer written against them; none exists
  in this repository besides the web UI, which uses none of the removed items.

## Scope

Phase 1: about 25 Go files, mostly comment-only. Phase 2: `docs/api.md`, `docs/storage.md`,
`internal/server`, `internal/core`, example config, plus tests. Phase 3: `go.mod`, `Dockerfile`,
`.golangci.yml`, `Taskfile.yml`, `internal/server`, `docs/decisions/`, README and AGENTS.
Phase 4: `go.mod`, `go.sum` and whatever the upgrades break.

## Alternatives

- Relax the linter instead of adding comments (disable revive `exported`): fewer edits, but it
  leaves the baseline unmet and the adapter implementations undocumented.
- Implement the unbuilt spec items (token scoping, SSO login, request capabilities) instead of
  removing them: larger, and nothing consumes them today.

## Annotations

- `api.md` §10 `token_accounts` is not removed in phase 2: plan `20261003-1654-scoped-tokens`
  replaces it with scoped tokens.
- Approved 2026-10-03 with the proposal's defaults: unbuilt spec items are removed, retention stays with its own task. Phase 4 (dependency bumps) was not approved with the rest and moved to task `20261003-1806-dependency-bumps`.
- Upload `kind` was removed from the spec instead of implemented: the send request's `content.type` already carries it.

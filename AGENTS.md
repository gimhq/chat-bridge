## Project Development

This repository follows the PMA workflow. The actual rules live in the `/pma`
skill and the stack skills below — do not duplicate them here. If a rule in
this file ever conflicts with `/pma`, treat `/pma` as the source of truth and
update this file.

### Skill stack

- `/pma` — workflow control, three-phase gate, task and plan tracking
- `/pma-go` — implementation baseline for Go services

### Triggers

Any feature, bug fix, refactor, planning, progress tracking, or multi-agent
execution goes through `/pma` (investigate → proposal → implement). Ceremony
is tiered by complexity per `/pma` *Task Tiers*: only trivial changes take
the fast path; everything else waits for explicit approval such as `proceed`.

### Project-specific facts

- Primary language / runtime: Go 1.26 (`go` directive in `go.mod`; whatsmeow's floor)
- Database / storage: SQLite via `modernc.org/sqlite` (`<data_dir>/chatbridge.db`), content-addressed media under `<data_dir>/media/`, see `docs/storage.md`
- Platform adapters: WhatsApp (whatsmeow), Telegram (gotd), Matrix (mautrix) in-process behind `internal/adapter`; out-of-process adapters over WebSocket + JSON-RPC via `internal/adapters/remote` (`docs/adapter-protocol.md`). Several adapter instances may serve one platform; every account is bound to one (`accounts.adapter`).
- Config: defaults < `chat-bridge.yaml` (`-config` / `CHATBRIDGE_CONFIG` / `./chat-bridge.yaml`) < `CHATBRIDGE_<SECTION>_<KEY>` env, see `docs/api.md` §2.2
- Dev URL routing: not used; single HTTP service on `:8080`
- Deployment target: distroless static nonroot container (`Dockerfile`, `compose.yaml`)
- Quality-gate command: `test -z "$(gofmt -l .)" && go vet -tags goolm ./... && golangci-lint run && go test -tags goolm ./... && go build -tags goolm ./...` (the `goolm` tag is mandatory: it selects the pure-Go olm backend for Matrix E2EE; `.golangci.yml` carries it too)
- Fast path: enabled (default)

### Local divergences

Any deliberate deviation from a skill rule (Hard Lock relaxation, alternative
library, non-default layout) is recorded in `docs/decisions/<YYYY-MM-DD>-<slug>.md`
with a sunset date. Do not silently override skill rules in this file.

### Documentation entry points

- Tasks: `docs/task/index.md`
- Plans: `docs/plan/index.md`
- Decisions: `docs/decisions/`
- Architecture: `docs/architecture.md`
- Changelog: `docs/changelog.md`
- HTTP API: `docs/api.md`
- Adapter protocol: `docs/adapter-protocol.md`
- Storage design: `docs/storage.md`

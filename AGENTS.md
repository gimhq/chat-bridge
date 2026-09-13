## Project Development

This repository follows the PMA workflow. The actual rules live in the `/pma`
skill and the stack skills below — do not duplicate them here. If a rule in
this file ever conflicts with `/pma`, treat `/pma` as the source of truth and
update this file.

### Skill stack

- `/pma` — workflow control, three-phase gate, task and plan tracking
- `/pma-go` — implementation baseline for Go services
- `/pma-web` — implementation baseline for the embedded management UI in `web/`

### Triggers

Any feature, bug fix, refactor, planning, progress tracking, or multi-agent
execution goes through `/pma` (investigate → proposal → implement). Ceremony
is tiered by complexity per `/pma` *Task Tiers*: only trivial changes take
the fast path; everything else waits for explicit approval such as `proceed`.

### Project-specific facts

- Primary language / runtime: Go 1.26 (`go` directive in `go.mod`; whatsmeow's floor)
- Database / storage: SQLite via `modernc.org/sqlite` (`<data_dir>/chatbridge.db`), content-addressed media under `<data_dir>/media/`, see `docs/storage.md`
- Platform adapters: WhatsApp (whatsmeow), Telegram (gotd), Matrix (mautrix) in-process behind `internal/adapter`; Signal is mautrix-signal's bridgev2 connector hosted by `internal/adapters/connector` (virtual Matrix side; build tag `signal`, cgo + libsignal, see `docs/adapter-protocol.md` §11); out-of-process adapters over WebSocket + JSON-RPC via `internal/adapters/remote` (`docs/adapter-protocol.md`). Several adapter instances may serve one platform; every account is bound to one (`accounts.adapter`).
- Config: defaults < `chat-bridge.yaml` (`-config` / `CHATBRIDGE_CONFIG` / `./chat-bridge.yaml`) < `CHATBRIDGE_<SECTION>_<KEY>` env, see `docs/api.md` §2.2
- Dev URL routing: production is one HTTP service on `:8080` (`/v1`, `/ui/`); nsl is used only for the web UI dev loop (`bunx nsl route chat-bridge:/v1 8080`, then `bun run dev` in `web/` → `http://chat-bridge.localhost:3355/ui/`)
- Deployment target: alpine nonroot container (`Dockerfile`, `compose.yaml`); the image is a cgo binary (`-tags goolm,signal`) linking libsignal built in the Dockerfile's `libsignal` stage
- Quality-gate command: `test -z "$(gofmt -l .)" && go vet -tags goolm ./... && golangci-lint run && go test -tags goolm ./... && go build -tags goolm ./...` (the `goolm` tag is mandatory: it selects the pure-Go olm backend for Matrix E2EE; `.golangci.yml` carries it too). Second line when `.tmp/libsignal/libsignal_ffi.a` exists (`scripts/build-libsignal.sh`), skipped otherwise: `CGO_LDFLAGS="-L$PWD/.tmp/libsignal" go vet -tags goolm,signal ./... && CGO_LDFLAGS="-L$PWD/.tmp/libsignal" go build -tags goolm,signal ./...`; `docker build --target test .` always covers both tags. Web UI gate, in `web/`: `bun run lint && bun run typecheck && bun run test && bun run build` (the build writes `internal/webui/dist`, embedded by the Go binary); `web/go.mod` is a stub that keeps `web/node_modules` out of `./...`
- Fast path: enabled (default)

### Local divergences

Any deliberate deviation from a skill rule (Hard Lock relaxation, alternative
library, non-default layout) is recorded in `docs/decisions/<YYYY-MM-DD>-<slug>.md`
with a sunset date. Do not silently override skill rules in this file.

Current records: `docs/decisions/2026-09-13-web-typescript-6.md` (the UI pins TypeScript 6.0 until typescript-eslint supports TypeScript 7).

### Documentation entry points

- Tasks: `docs/task/index.md`
- Plans: `docs/plan/index.md`
- Decisions: `docs/decisions/`
- Architecture: `docs/architecture.md`
- Changelog: `docs/changelog.md`
- HTTP API: `docs/api.md`
- Adapter protocol: `docs/adapter-protocol.md`
- Storage design: `docs/storage.md`

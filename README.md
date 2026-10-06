# chat-bridge

A self-hosted Go service that owns WhatsApp, Telegram, Matrix, and Signal sessions, stores every
message and attachment in SQLite, and exposes one platform-agnostic HTTP API (REST, long-poll, SSE,
webhooks). Consumers such as a personal assistant or a Matrix bridge talk to the API instead of to
each chat platform. Platforms are adapters: three ship in-process (whatsmeow, gotd, mautrix-go),
Signal is mautrix-signal's bridgev2 connector hosted behind the same contract (any other bridgev2
network can be added the same way), and any other language can add one over WebSocket + JSON-RPC
(`docs/adapter-protocol.md`).

## Quick start

```bash
cp .env.example .env            # set CHATBRIDGE_SERVER_TOKEN (at least 16 characters)
docker compose up -d --build    # API on http://127.0.0.1:8080, data in ./data
```

Configuration can also come from a file: copy `chat-bridge.example.yaml` to `chat-bridge.yaml`.
Environment variables (`CHATBRIDGE_<SECTION>_<KEY>`) override the file.

Run locally without Docker:

```bash
go run -tags goolm ./cmd/chat-bridge -config chat-bridge.yaml
```

`-tags goolm` selects the pure-Go olm implementation for Matrix end-to-end encryption (the
default backend needs cgo and libolm); every `go build`, `go test`, and `go vet` in this
repository carries it.

`-tags signal` adds the Signal adapter. It links libsignal (Rust) through cgo, so it needs
`libsignal_ffi.a`, a C++ toolchain and zlib: the Docker build produces all of it (the `libsignal`
stage clones mautrix-signal and runs its `build-rust.sh`); on a developer machine
`scripts/build-libsignal.sh` builds the library in a throwaway container into `.tmp/libsignal/`,
then `CGO_LDFLAGS="-L$PWD/.tmp/libsignal" go build -tags goolm,signal ./...`. Without the tag the
binary stays pure Go and Signal is simply absent from `GET /platforms`.

`-tags tgbridge` adds a second Telegram implementation: mautrix-telegram's bridgev2 connector,
registered as instance `bridgev2` next to the gotd adapter (`local`) when
`adapters.telegram.bridgev2` is `true` (`CHATBRIDGE_ADAPTERS_TELEGRAM_BRIDGEV2`). It needs cgo for
the connector's bundled libwebp (a C compiler, no extra library) and adds about 36 MB to the
binary. With both instances registered, `POST /accounts` for `telegram` must name the `adapter`;
existing accounts stay on the instance they are bound to.

Prebuilt binaries for Linux, macOS and Windows (amd64, arm64) are attached to each
[GitHub release](https://github.com/gimhq/chat-bridge/releases). They are pure Go with the web UI
embedded and without Signal or the bridgev2 Telegram instance; use the container image for those. Pushing a `v*` tag runs
`.github/workflows/release.yml`, which runs the gates, builds the archives and publishes the release.

First account, WhatsApp via QR:

```bash
T="Authorization: Bearer $CHATBRIDGE_SERVER_TOKEN"; B=http://127.0.0.1:8080/v1
curl -s -H "$T" -H 'Content-Type: application/json' -d '{"id":"wa-main","platform":"whatsapp"}' $B/accounts
curl -s -H "$T" -H 'Content-Type: application/json' -d '{"flow":"qr"}' $B/accounts/wa-main/login   # render display.data as a QR
curl -s -H "$T" "$B/events?wait=30"                                                                   # account.status → connected, then message.new …
```

Every platform follows the same shape: `POST /accounts` → `POST /accounts/{a}/login` (QR, code,
password… described by the step machine) → chats, messages, media, contacts under
`/accounts/{a}/…` → events on `/events`. Telegram needs `api_id`/`api_hash` once for the whole server
(`CHATBRIDGE_ADAPTERS_TELEGRAM_API_ID` / `_API_HASH`, or `adapters.telegram` in the config; accounts
inherit them and only a different application needs them per account), Matrix a `homeserver`. Matrix
rooms are end-to-end encrypted transparently; `POST /accounts/{a}/keys/verify` with the
account's recovery key cross-signs the bridge device and restores the key backup. Signal links
as a secondary device: `{"flow":"qr"}` returns the `sgnl://linkdevice` URI to scan.

## Tokens

`server.token` is the admin token. Each other consumer gets its own scoped token, limited to the
persons, contacts and chats it lists: it can read, search and send only there, and every
management route answers `403` (`docs/api.md` §4.11). `"read_only": true` in the scope leaves
reading and searching only; `POST …/chats/resolve` lets a token start a chat with a contact it lists.

```bash
curl -s -H "$T" -H 'Content-Type: application/json' $B/tokens \
  -d '{"name":"family-assistant","scope":{"contacts":[{"account_id":"wa-main","user_id":"8613800000000@s.whatsapp.net"}]}}'
# → {"id":"tok_…","token":"cbt_…", …}   the secret is shown once
```

## Web UI

The binary serves a management UI at `http://127.0.0.1:8080/ui/` (`/` redirects there). Sign in
with `server.token`; the token stays in the browser's local storage. It covers accounts (create,
login by QR / code / password, reconnect, logout, delete, profile), chats (list, archive, mute,
create and rename groups, search), the message timeline (media, replies, reactions, sending text
and files, older history), contacts (alias, block), requests (accept invites, reject calls, with a
toast when one arrives), persons (one human's contacts across accounts: automatic grouping by
phone, suggestions, merged timeline, merge and unlink), a live event log, and system status with
scoped tokens (create with a scope picker and a read-only switch, edit, revoke) and webhooks.

The UI is built from `web/` (React, Vite, TanStack Router and Query, shadcn/ui on Base UI) into
`internal/webui/dist` and embedded with `go:embed`; the Docker build does this in a bun stage.
Without a build, `/ui/` answers with instructions and the API works as usual.

```bash
cd web && bun install && bun run build && cd .. && go build -tags goolm ./cmd/chat-bridge
```

Development with live reload uses nsl so UI and API share one origin:

```bash
bunx nsl route chat-bridge:/v1 8080     # the Go server, already running on :8080
cd web && bun run dev                    # http://chat-bridge.localhost:3355/ui/
```

## Layout

```
cmd/chat-bridge/        entrypoint
internal/core/          accounts, login machine, event log, media policy, webhooks
internal/store/         SQLite (schema in docs/storage.md)
internal/media/         content-addressed blobs
internal/server/        HTTP API (docs/api.md)
internal/adapter/       the adapter contract (+ fake for tests)
internal/adapters/
  base/                 shared scaffolding for in-process adapters
  whatsapp/ telegram/ matrix/
  matrixcontent/        Matrix event content <-> model.Content (shared by matrix and connector)
  connector/            host for mautrix bridgev2 network connectors (virtual Matrix side)
  signal/               Signal = hosted mautrix-signal connector (build tag `signal`, cgo)
  tgbridge/             Telegram instance `bridgev2` = hosted mautrix-telegram connector (build tag `tgbridge`, cgo)
  remote/               WebSocket + JSON-RPC host for out-of-process adapters
internal/webui/         go:embed of the built UI (dist/ is generated)
web/                    management UI source (bun; see Web UI)
scripts/                build-libsignal.sh (libsignal_ffi.a for local `-tags signal` builds)
docs/                   specs, architecture, PMA task/plan tracking
```

## Quality gates

```bash
test -z "$(gofmt -l .)" && go vet -tags goolm ./... && golangci-lint run && go test -tags goolm -cover ./... && go build -tags goolm ./... && go mod tidy && git diff --exit-code go.mod go.sum
# with libsignal available (see above); skipped when .tmp/libsignal/libsignal_ffi.a is absent
CGO_LDFLAGS="-L$PWD/.tmp/libsignal" go vet -tags goolm,signal ./... && CGO_LDFLAGS="-L$PWD/.tmp/libsignal" go build -tags goolm,signal ./...
# bridgev2 Telegram instance (a C compiler is enough)
go vet -tags goolm,tgbridge ./... && go test -tags goolm,tgbridge ./internal/adapters/tgbridge/ && go build -tags goolm,tgbridge ./...
```

Web UI gates (in `web/`):

```bash
bun run lint && bun run typecheck && bun run test && bun run build
```

`docker build --target test .` runs `go vet` and the unit tests with both tags in the build toolchain.
`golangci-lint` (config in `.golangci.yml`) is expected on the developer machine; it is not part
of the Docker build. `task check` (`Taskfile.yml`) runs the Go gates in order, `task web` the UI ones.

`.github/workflows/ci.yml` runs all of the above on every push to `main` and every pull request:
the web UI gate, the Go gates with the `tgbridge` line, and the Docker `test` stage for `signal`.

## Documentation

- [HTTP API](docs/api.md)
- [Adapter protocol](docs/adapter-protocol.md)
- [Storage design](docs/storage.md)
- [Architecture](docs/architecture.md)
- [WhatsApp feasibility and plan](docs/whatsapp-feasibility.md)

## License

[Apache-2.0](LICENSE)

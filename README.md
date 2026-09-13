# chat-bridge

A self-hosted Go service that owns WhatsApp, Telegram, and Matrix sessions, stores every message and
attachment in SQLite, and exposes one platform-agnostic HTTP API (REST, long-poll, SSE, webhooks).
Consumers such as a personal assistant or a Matrix bridge talk to the API instead of to each chat
platform. Platforms are adapters: three ship in-process (whatsmeow, gotd, mautrix-go), and any
other language can add one over WebSocket + JSON-RPC (`docs/adapter-protocol.md`).

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

First account, WhatsApp via QR:

```bash
T="Authorization: Bearer $CHATBRIDGE_SERVER_TOKEN"; B=http://127.0.0.1:8080/v1
curl -s -H "$T" -H 'Content-Type: application/json' -d '{"id":"wa-main","platform":"whatsapp"}' $B/accounts
curl -s -H "$T" -H 'Content-Type: application/json' -d '{"flow":"qr"}' $B/accounts/wa-main/login   # render display.data as a QR
curl -s -H "$T" "$B/events?wait=30"                                                                   # account.status → connected, then message.new …
```

Every platform follows the same shape: `POST /accounts` → `POST /accounts/{a}/login` (QR, code,
password… described by the step machine) → chats, messages, media, contacts under
`/accounts/{a}/…` → events on `/events`. Telegram needs `api_id`/`api_hash` (once, under
`adapters.telegram` in the config, or per account in `config`), Matrix a `homeserver`. Matrix
rooms are end-to-end encrypted transparently; `POST /accounts/{a}/keys/verify` with the
account's recovery key cross-signs the bridge device and restores the key backup.

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
  remote/               WebSocket + JSON-RPC host for out-of-process adapters
docs/                   specs, architecture, PMA task/plan tracking
```

## Quality gates

```bash
test -z "$(gofmt -l .)" && go vet -tags goolm ./... && golangci-lint run && go test -tags goolm ./... && go build -tags goolm ./...
```

`docker build --target test .` runs `go vet` and the unit tests in the build toolchain.
`golangci-lint` (config in `.golangci.yml`) is expected on the developer machine; it is not part
of the Docker build.

## Documentation

- [HTTP API](docs/api.md)
- [Adapter protocol](docs/adapter-protocol.md)
- [Storage design](docs/storage.md)
- [Architecture](docs/architecture.md)
- [WhatsApp feasibility and plan](docs/whatsapp-feasibility.md)

## License

[Apache-2.0](LICENSE)

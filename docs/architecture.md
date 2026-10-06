# Architecture

```
WhatsApp / Telegram / Matrix <-> in-process adapters --\
Signal (mautrix bridgev2    <-> connector host --------+-> core <-> HTTP API (/v1) + long-poll + SSE + webhooks <-> consumers
  network connector, cgo)       (virtual Matrix side)  |     |
any platform, any language  <-> remote adapters -------/     |
        (WebSocket + JSON-RPC on /adapter/v1)       SQLite (chatbridge.db) + media/<sha256> + accounts/<id>/
```

Per-account adapter state under `accounts/<id>/`: WhatsApp `whatsmeow.db`; Telegram
`session.json`, `updates.json` (pts/qts for gap recovery), `peers.json` (access hashes); Matrix
`session.json` (token, sync cursor, pickle key) and `crypto.db` (olm account, megolm sessions,
device keys, room state — opened with modernc, `-tags goolm`); hosted bridgev2 connectors
(Signal) `bridgev2.db` (the bridge's own users, logins, portals, ghosts, messages, plus the
connector's tables such as signalmeow's).

The core is the only owner of consumer-visible state. Adapters are interchangeable executors:
each is identified by `platform/instance`, every account is bound to exactly one instance, and an
adapter leaving or returning changes account status but never account data.

## Components

| Package | Role |
|---|---|
| `cmd/chat-bridge` | Entrypoint: loads config, opens store and media, registers adapters, serves HTTP |
| `internal/config` | koanf config: defaults < YAML file < `CHATBRIDGE_*` env, validated at startup |
| `internal/server` | Chi router, bearer-token auth on `/v1` (admin token or scoped token), request handlers |
| `internal/core` | Account lifecycle, message/chat/media orchestration, event fan-out, scoped-token allowlists |
| `internal/adapter` | Contract between core and platform implementations (`fake` for tests) |
| `internal/adapters/{whatsapp,telegram,matrix}` | Platform adapters (whatsmeow, gotd, mautrix-go); `base` holds shared helpers |
| `internal/adapters/matrixcontent` | Matrix event content ↔ `model.Content`, shared by the Matrix adapter and the connector host |
| `internal/adapters/connector` | Hosts mautrix bridgev2 network connectors: implements bridgev2's Matrix side as a virtual homeserver feeding the sink (`docs/adapter-protocol.md` §11) |
| `internal/adapters/signal` | Signal = hosted mautrix-signal connector; build tag `signal` (cgo, libsignal) |
| `internal/adapters/remote` | Hosts out-of-process adapters: WebSocket + JSON-RPC 2.0 on `/adapter/v1`, media PUT/GET |
| `internal/store` | SQLite persistence: accounts, chats, contacts, messages, events, webhooks, media index |
| `internal/media` | Content-addressed blob storage |
| `internal/model` | Shared API/domain types |
| `internal/webui` | `go:embed` of the built management UI, served at `/ui/` (`internal/server/ui.go`) |
| `web/` | Management UI source: React, Vite, TanStack Router and Query, shadcn/ui on Base UI; a plain consumer of `/v1` |

## Data flow

- Inbound: adapter → `Sink.Events` → one SQLite transaction per batch (rows + `events` log) →
  long-poll/SSE wake-up and webhook worker. Attachments are rows first; bytes arrive later
  (`Sink.PutMedia`, auto-download policy in `media.auto_download`).
- Outbound: HTTP → core validates capability and resolves uploads → adapter sends → core stores the
  sent message and emits `message.new`. Replies with the same `client_id` return the stored row.
- Login: a platform-agnostic step machine (`input` / `display` / `done` / `failed`) driven by the
  adapter, recorded as `account.login_step` events.
- Scoped tokens: the server puts the token id in the request context; every core read and write
  resolves it to an allowlist of contacts and chats and filters in SQL (`api.md` §4.11). A context
  without a token id (admin token, adapters, background workers) is unrestricted.
- Requests: adapter `request` events (invites, join requests, calls) → `requests` rows keyed by the
  adapter's stable key → `request.new` / `request.updated`; answers go back through
  `RequestAnswerer`, and the GC loop expires pending ones.

## Details

- HTTP API: [api.md](api.md)
- Adapter contract: [adapter-protocol.md](adapter-protocol.md)
- Storage layout, schema, retention: [storage.md](storage.md)
- Platform decision and roadmap: [whatsapp-feasibility.md](whatsapp-feasibility.md)

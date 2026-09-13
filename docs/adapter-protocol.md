# Adapter Protocol

How a platform implementation plugs into the bridge core. Companion to `api.md`
(consumer-facing) and `storage.md`. Status: draft v1, 2026-09-12.

## 1. Architecture

```
consumers (assistant, Matrix bridge, CLI)
        │  HTTP + events   (api.md)
        ▼
┌─────────────────────────────────────────────┐
│ bridge core (Go)                            │
│  HTTP API · store · event log · persons     │
│  webhook worker · media GC · login machine  │
└──────┬──────────────────────┬───────────────┘
       │ Go interface         │ WebSocket + JSON-RPC 2.0  (this document)
       ▼                      ▼
 in-process adapters                        out-of-process adapters
 whatsapp (whatsmeow) · telegram (gotd)      any platform in any language:
 matrix (mautrix-go)                         wechat · a second whatsapp host · …
 hosted mautrix bridgev2 connectors (§11):
 signal (libsignal, cgo) · …
```

The core owns everything a consumer sees. An adapter owns exactly one thing: talking to its
platform. It keeps its own session files, knows nothing about persons, tags, webhooks, or
retention, and never touches `chatbridge.db`.

Two ways to attach, one contract:

| | In-process | Out-of-process |
|---|---|---|
| Language | Go | any |
| Transport | `adapter.Adapter` interface (§10) | WebSocket, JSON-RPC 2.0, one connection per adapter process |
| Lifecycle | compiled in, started by core | separate process or container, connects to core, restarts independently |
| When | the best library is Go (whatsmeow) | the best library is not (Telethon, matrix-js-sdk, Baileys), or it must be isolated |

The JSON-RPC surface is a 1:1 encoding of the Go interface; the core wraps a remote adapter in a
struct that implements `adapter.Adapter`, so the rest of the core cannot tell them apart.

## 2. Transport

- Adapter connects to `ws://<core>/adapter/v1` (or `wss://`). The core never dials out; adapters
  may live anywhere that can reach the core.
- Header `Authorization: Bearer <ADAPTER_TOKEN>`. One shared adapter token per core (config
  `server.adapter_token`; the endpoint is disabled until it is set), distinct from consumer
  tokens. `server.public_url` sets the base of the media URLs handed out; otherwise they are
  derived from the request (`put_url`) or relative (`message.send` attachment `url`).
- JSON-RPC 2.0 text frames, both directions, on the same connection. Every request gets a
  response; notifications are not used, so delivery is always acknowledged.
- One connection serves all accounts of that platform. Every method carries `account_id`.
- Ping every 20 s from the core; a connection with no pong for 60 s is dropped and its accounts
  go `disconnected` with `error.code = adapter_offline`.
- Frames over 1 MiB are rejected; media bytes go over HTTP (§7).
- Newline-delimited JSON over stdio is accepted as an alternative for adapters the core spawns
  itself (`adapters: [{platform, cmd, args}]` in core config); same messages, same semantics.

## 3. Handshake

First request on the connection, adapter → core:

```json
{"jsonrpc": "2.0", "id": 1, "method": "hello", "params": {
  "protocol": 1,
  "platform": "telegram",
  "instance": "eu-host",
  "adapter": {"name": "tg-telethon", "version": "0.3.0"},
  "capabilities": ["send.text", "send.media", "message.edit", "message.delete", "message.reaction", "message.reply", "message.thread", "message.history", "chat.read", "chat.typing", "chat.resolve", "chat.members", "receipts", "format.markdown", "format.html", "self.update", "contact.alias"],
  "login_flows": [
    {"id": "phone", "name": "Phone number + code"},
    {"id": "qr", "name": "Scan QR from Telegram app"}
  ],
  "config_schema": {
    "type": "object",
    "required": ["api_id", "api_hash"],
    "properties": {
      "api_id":   {"type": "integer"},
      "api_hash": {"type": "string", "x-secret": true},
      "device_name": {"type": "string", "default": "bridge"}
    }
  },
  "device_schema": {"type": "object", "properties": {"dc": {"type": "integer"}, "layer": {"type": "integer"}}}
}}
```

Core replies with the accounts this adapter must serve:

```json
{"jsonrpc": "2.0", "id": 1, "result": {
  "core_version": "0.4.0",
  "accounts": [
    {"id": "tg-main", "config": {"api_id": 12345, "api_hash": "…", "device_name": "bridge"}, "data_dir": "/data/accounts/tg-main", "status": "unpaired"}
  ],
  "media": {"put_url": "http://core:8080/adapter/v1/media", "get_url": "http://core:8080/adapter/v1/media/{id}"}
}}
```

- `protocol` mismatch → core closes with code 4001.
- `instance` names this adapter among others of the same platform (default `remote`; the
  in-process adapters are `local`). Several instances may serve one platform at once — e.g. two
  WhatsApp hosts on different networks — and each account is bound to exactly one of them
  (`POST /accounts {"adapter": ...}`, `PATCH /accounts/{a} {"adapter": ...}`). A second connection
  with the same `platform/instance` gets 4002. The hello reply and `account.add` only cover the
  accounts bound to this instance; unbound accounts are bound automatically when the instance is
  the only one for its platform.
- `capabilities`, `login_flows`, and schemas are what `GET /platforms` and `Account.capabilities`
  publish. The core validates `POST /accounts` config against `config_schema` and redacts
  `x-secret` fields on read.
- `data_dir` is a path the core has created for the adapter's private state. For an adapter in
  another container the operator mounts the core's `data/accounts/` at the same path; if the
  adapter cannot see it, it may keep state elsewhere — the core does not care, but
  `DELETE /accounts/{a}` then cannot wipe it.
- After hello, the adapter must call `status` for every account (§5.1); the core keeps them
  `connecting` until it does.
- The core also sends `account.add` for every account listed in the hello reply (the same code
  path a newly created account takes); treat it as idempotent.

## 4. Core → adapter methods

All take `{"account_id": "…", ...}`. Errors use JSON-RPC `error.code`:

| code | meaning | maps to API |
|---|---|---|
| -32601 | method not implemented | `422 unsupported` |
| 1001 | `not_connected` | `409 account_not_ready` |
| 1002 | `invalid_target` — unknown chat / user / message | `404 not_found` |
| 1003 | `invalid_input` | `400 invalid_request` |
| 1004 | `unsupported` — capability advertised but not for this case (e.g. edit a media caption) | `422 unsupported` |
| 1005 | `rate_limited`, `data.retry_after_s` | `429` |
| 1006 | `platform_error`, `data.platform_code`, `data.message` | `502` |

| Method | Params | Result | Capability |
|---|---|---|---|
| `account.add` | `{account_id, config, data_dir}` | `{}` | |
| `account.remove` | `{account_id}` | `{}` — log out if logged in, delete `data_dir` contents | |
| `account.reconnect` | `{account_id}` | `{}` | |
| `account.update_config` | `{account_id, config}` | `{}` | |
| `login.start` | `{account_id, flow}` | LoginStep | |
| `login.submit` | `{account_id, fields}` | LoginStep | |
| `login.refresh` | `{account_id}` | LoginStep — current step, new QR if rotated | |
| `login.cancel` | `{account_id}` | `{}` | |
| `logout` | `{account_id}` | `{}` | |
| `self.update` | `{account_id, name?, bio?, avatar_media_id?}` | Contact | `self.update` |
| `chat.resolve` | `{account_id, handle}` | `{chat_id, kind, user_id?}` | `chat.resolve` |
| `chat.get` | `{account_id, chat_id}` | Chat with `participants` | |
| `chat.create` | `{account_id, kind, name, members}` | Chat | `chat.create` |
| `chat.update` | `{account_id, chat_id, muted?, archived?, name?}` | Chat | |
| `chat.mark_read` | `{account_id, chat_id, up_to?}` | `{}` | `chat.read` |
| `chat.typing` | `{account_id, chat_id, state}` | `{}` | `chat.typing` |
| `chat.backfill` | `{account_id, chat_id, before: {ts, message_id}, limit}` | `{messages: [Message], more: bool}` | `message.history` |
| `contact.get` | `{account_id, user_id}` | Contact | |
| `contact.list` | `{account_id}` | `{contacts: [Contact]}` — full address book, used at first connect and on demand | |
| `contact.set_alias` | `{account_id, user_id, alias}` | Contact | `contact.alias` |
| `contact.block` | `{account_id, user_id, blocked}` | Contact | |
| `message.send` | `{account_id, chat_id, client_id, content, reply_to?, thread_id?, mentions?}` | Message (`id` set, `status: sent` or `pending`) | `send.*` |
| `message.edit` | `{account_id, chat_id, message_id, content}` | Message | `message.edit` |
| `message.delete` | `{account_id, chat_id, message_id}` | `{}` | `message.delete` |
| `message.react` | `{account_id, chat_id, message_id, emoji, remove}` | `{}` | `message.reaction` |
| `media.fetch` | `{account_id, media_id, remote_ref}` | `{}` after the adapter has PUT the bytes (§7); an error marks the attachment `failed` | |
| `keys.status` | `{account_id}` | `KeyStatus` (api.md §4.7a) | `keys.manage` |
| `keys.verify` | `{account_id, recovery_key}` | `KeyVerifyResult` | `keys.manage` |
| `keys.export` | `{account_id, passphrase}` | `{data}` (base64 of the key file) | `keys.manage` |
| `keys.import` | `{account_id, passphrase, data}` (base64) | `{sessions_imported}` | `keys.manage` |
| `request.answer` | `{account_id, request_id, platform_ref, action: "accept"\|"reject", alias?, reason?}` | `{}` | `contact.request` etc. |

`message.send` content arrives exactly as the consumer posted it (§3.5 of the API spec), with
each attachment already expanded to `{media_id, mime, size, file_name, sha256}`; the adapter
fetches bytes from `media.get_url`. `format: markdown` is the common subset; an adapter converts
to its platform's markup or strips it — it never returns `unsupported` for markdown.

## 5. Adapter → core methods

### 5.1 `status`

```json
{"method": "status", "params": {"account_id": "tg-main", "status": "connected", "self": { Contact }, "device": {"dc": 2, "layer": 195}, "error": null}}
```

Sent on every transition. `error` is `{code, message}` with `code` from: `logged_out_remotely`,
`banned`, `session_expired`, `network`, `adapter_error`. `self` is required with `connected`.

### 5.2 `login.step`

`{"account_id", "step": LoginStep}` — pushed when a step changes on its own (QR rotated, user
scanned, code expired), so the core does not need to poll `login.refresh`.

### 5.3 `events`

The only bulk channel. Adapter batches whatever it has (max 100, max 1 MiB), core responds when
all are durably stored, then the adapter may drop them. Ordering inside a batch and across batches
on one connection is preserved per account.

```json
{"jsonrpc": "2.0", "id": 77, "method": "events", "params": {"account_id": "tg-main", "events": [
  {"kind": "message", "message": { Message, "raw": {…} }, "chat": { Chat, optional }, "sender": { Contact, optional }},
  {"kind": "message_update", "chat_id": "…", "message_id": "…", "content": {…}, "edited_at": "…"},
  {"kind": "message_delete", "chat_id": "…", "message_id": "…", "deleted_at": "…"},
  {"kind": "reaction", "chat_id": "…", "message_id": "…", "sender_id": "…", "emoji": "👍", "removed": false, "ts": "…"},
  {"kind": "receipt", "chat_id": "…", "message_ids": ["…"], "user_id": "…", "receipt": "read", "ts": "…"},
  {"kind": "chat", "chat": { Chat }},
  {"kind": "member", "chat_id": "…", "user_id": "…", "chat_name": "…", "role": "member", "left": false},
  {"kind": "contact", "contact": { Contact }},
  {"kind": "request", "request": { Request minus id, "platform_ref": {…} }},
  {"kind": "request_update", "platform_ref": {…}, "state": "accepted"},
  {"kind": "typing", "chat_id": "…", "user_id": "…", "state": "typing"},
  {"kind": "presence", "user_id": "…", "state": "online", "last_seen": null},
  {"kind": "platform_event", "platform_type": "updateBotStopped", "chat_id": null, "user_id": "…", "raw": {…}}
]}}
```

The `events` result is `{"count": n}` once every event is committed. Unknown `kind`s are stored as
`platform_event` with the original kind as `platform_type`.

Rules for the adapter:

- `message.id` is the platform id; `message.sender.name` may be omitted — the core resolves it.
  The `chat` and `sender` side-objects are optional hints so the core can create rows for a chat
  or contact it has never seen without a round-trip; send them the first time and whenever they
  change.
- Attachments in `message.content.attachments[]` carry `state: pending` or `remote` plus
  `remote_ref`; bytes come separately (§7). Never inline bytes.
- Messages the adapter itself sent via `message.send` are **not** re-emitted as `message`
  events; messages from another device of the same account are, with `from_me: true`.
- Timestamps are RFC 3339 with the platform's value; the core sets `received_at`.
- Unknown platform things go out as `platform_event`, never dropped.
- Messages replayed from history (WhatsApp history sync, Telegram dialog list, Matrix initial
  timeline) carry `"backfill": true`. The core stores them idempotently but does not bump unread
  counters or auto-download their media; the `chat` hint's `unread_count` is taken as the
  platform's authoritative value instead. Contacts that arrive incrementally after first login
  (WhatsApp app-state sync, push-name chunks) are re-emitted as `contact` batches when the
  platform signals the sync finished.

### 5.4 `media.ready` / `media.failed`

`{"account_id", "media_id", "sha256", "size", "mime", "width?", "height?", "duration_ms?"}` after
a background download finished and the bytes were PUT (§7); `media.failed` with `{media_id, error}`.

## 6. Login flow contract

The adapter drives the machine; the core only relays steps and stores the outcome.

```
core → login.start {flow}           adapter returns step (display | input)
core → login.submit {fields}        adapter returns next step
adapter → login.step                adapter pushes unsolicited changes (QR rotation, scan detected)
adapter returns / pushes done       core stores self, login record; adapter then sends status connected
adapter returns / pushes failed     core surfaces the error; owner may login.start again
core → login.cancel                 adapter aborts; account back to unpaired
```

LoginStep shape is exactly §5 of the API spec. Field `type` and display `type` are closed enums;
an adapter needing something new proposes a protocol bump, it does not invent a value.

## 7. Media transfer

Bytes never cross the WebSocket.

**Inbound (platform → core):** the adapter streams the file to
`PUT {put_url}` (`/adapter/v1/media`) with headers `Authorization: Bearer <ADAPTER_TOKEN>`, `X-Account-Id`,
`X-Media-Id` (the platform id it used in the event), `Content-Type`, optional `X-File-Name`,
`X-Width`, `X-Height`, `X-Duration-Ms`. The core hashes while writing, dedupes by SHA-256, and
responds `{"media_id", "sha256", "size"}`; the attachment flips to `ready` and consumers get
`message.updated`. The adapter may PUT before or after emitting the message event; the core
matches on `(account_id, media_id)`. If a chunked transfer breaks, the adapter re-PUTs from the
start; there is no resume.

**Outbound (core → platform):** `message.send` carries a top-level `attachments` array
(`{media_id, mime, size, file_name, sha256, url}`) next to `content`; the adapter `GET`s each
`url` (`/adapter/v1/media/{id}`, relative unless `server.public_url` is set) with the adapter
token, streams to the platform, and must not cache beyond the request.

**Auto-download policy** is the core's: `media.fetch` is called for attachments the account's
config says to download; everything else stays `remote` until a consumer asks. Adapters must
therefore emit every attachment with a `remote_ref` that stays valid as long as the platform
allows, and say so in `raw` if it expires (WhatsApp media keys do not; Telegram file references
do, and the adapter re-resolves them on `media.fetch`).

## 8. Reconnection and delivery guarantees

- Every `events` request is acknowledged only after the batch is committed with its `events` rows.
  An adapter that does not get a response (connection dropped) resends the batch after reconnect
  and re-hello; the core's `INSERT … ON CONFLICT DO NOTHING` on `(account_id, chat_id, id)` makes
  message replays idempotent, and reactions / receipts / members are upserts.
- The core never replays core → adapter requests. A `message.send` that lost its response is
  reported to the consumer as `502 platform_error` with `details.unknown_outcome: true`; the
  consumer's `client_id` retry is what makes it safe (the adapter must treat a repeated
  `client_id` for the same chat as a no-op and return the original message if it still knows it).
- While no adapter is connected for a platform, its accounts read `disconnected` /
  `adapter_offline`; writes return `409`; reads keep working from the store.

## 9. Versioning

- `protocol` is an integer. Additive changes (new optional field, new event kind, new method)
  keep the number; the core ignores unknown fields and event kinds it does not know become
  `platform_event`. Removals or semantic changes bump it, and the core supports the previous
  number for two releases.
- Capability strings are the extension point for behaviour; a new one is added to the API spec
  §3.2 first.

## 10. Go interface (in-process adapters)

```go
package adapter

type Adapter interface {
    Info() Info                                   // platform, capabilities, login flows, schemas
    Start(ctx context.Context, sink Sink) error   // called once; adapter emits through sink
    AddAccount(ctx context.Context, id string, cfg json.RawMessage, dataDir string) error
    RemoveAccount(ctx context.Context, id string) error
    Reconnect(ctx context.Context, id string) error

    LoginStart(ctx context.Context, id, flow string) (LoginStep, error)
    LoginSubmit(ctx context.Context, id string, fields map[string]string) (LoginStep, error)
    LoginRefresh(ctx context.Context, id string) (LoginStep, error)
    LoginCancel(ctx context.Context, id string) error
    Logout(ctx context.Context, id string) error

    ResolveChat(ctx context.Context, id, handle string) (ResolvedChat, error)
    GetChat(ctx context.Context, id, chatID string) (Chat, error)
    Backfill(ctx context.Context, id, chatID string, before Cursor, limit int) ([]Message, bool, error)
    ListContacts(ctx context.Context, id string) ([]Contact, error)

    SendMessage(ctx context.Context, id string, req SendRequest) (Message, error)
    EditMessage(ctx context.Context, id, chatID, msgID string, c Content) (Message, error)
    DeleteMessage(ctx context.Context, id, chatID, msgID string) error
    React(ctx context.Context, id, chatID, msgID, emoji string, remove bool) error
    MarkRead(ctx context.Context, id, chatID, upTo string) error
    Typing(ctx context.Context, id, chatID string, state string) error
    FetchMedia(ctx context.Context, id string, mediaID string, ref json.RawMessage, w io.Writer) (MediaInfo, error)
    AnswerRequest(ctx context.Context, id string, ref json.RawMessage, action string, opts AnswerOpts) error
}

// Sink is what the core hands the adapter; every method is durable when it returns.
type Sink interface {
    Status(ctx context.Context, id string, st Status) error
    LoginStep(ctx context.Context, id string, step LoginStep) error
    Events(ctx context.Context, id string, evs []Event) error
    PutMedia(ctx context.Context, id, mediaID string, meta MediaMeta, r io.Reader) (MediaInfo, error)
}
```

Optional capabilities are separate interfaces the core type-asserts (`Editor`, `Reactor`,
`Resolver`, `Backfiller`, `SelfUpdater`, `AliasSetter`, `RequestAnswerer`, `ChatCreator`, `KeyManager`);
`Info().Capabilities` must agree with what is implemented, and the core checks at startup.

The remote-adapter shim implements `Adapter` by forwarding to JSON-RPC and implements `Sink`
handling on the receiving side; it is the only place the wire format exists in the core.

In-process adapters live under `internal/adapters/<platform>/` and share
`internal/adapters/base`: an account registry (`base.Accounts`), a per-account reporter over the
sink with timeouts (`base.Reporter`), the login-flow state (`base.Login` with `Start`, `Current`,
`Update`, `SetStep`, `Cancel`), step constructors (`Input`, `Display`, `Failed`), and
`PlatformErr`. The built-in `whatsapp` (whatsmeow), `telegram` (gotd/td), and `matrix`
(mautrix-go) adapters are the reference implementations; a new platform is one package that fills
the same `Adapter` methods and is registered in `cmd/chat-bridge/main.go`. Platforms that already
have a mautrix bridgev2 connector need no adapter code at all: the `connector` host wraps them (§11).

## 11. Hosting a mautrix bridgev2 connector

`maunium.net/go/mautrix/bridgev2` splits a Matrix bridge into a *network connector* (the platform
side: login, chats, messages) and a *Matrix connector* (the homeserver side). chat-bridge
implements the homeserver side as a **virtual Matrix** in `internal/adapters/connector`, so any
bridgev2 network connector runs unmodified as an in-process adapter:

```go
// internal/adapters/signal/signal.go (build tag `signal`)
connector.New(log, "signal", func() bridgev2.NetworkConnector { return &sigconn.SignalConnector{} })
```

One bridgev2 `Bridge` runs per account, with its own database `accounts/<id>/bridgev2.db`
(modernc SQLite through `dbutil`) holding the bridge's users, logins, portals, ghosts, messages
and reactions. The account is the bridge user `@<account>:chat-bridge`; its single `UserLogin` is
the platform session. Mapping:

| bridgev2 | chat-bridge |
|---|---|
| portal (`PortalKey.ID`), room `!…:chat-bridge` | chat `id` = portal id; `kind` from the portal's room type (DM → `direct`, else `group`) |
| ghost (`@g_<base64url(user id)>:chat-bridge`) | contact / sender `id` = network user id; display name → `names.profile`, `tel:` identifiers → `phone` |
| `Message.ID` (+ `#<part id>` for extra parts) | message `id`; `ReplyTo` / `ThreadRoot` → `reply_to` / `thread_id` |
| ghost intent `SendMessage` (`m.room.message`, `m.sticker`, `m.reaction`, redaction, `m.replace`) | `message`, `reaction`, `message_delete`, `message_update` events; `from_me` when the sender is the login's own user id |
| `SendState` member / name, `CreateRoom` | `member`, `chat` events (with participants) |
| `MarkRead`, `MarkTyping` | `receipt` (read), `typing` |
| `UploadMedia` (attachments the connector downloaded during conversion) | `Sink.PutMedia`; the content URI `mxc://chat-bridge/<media_id>` names the stored media, so the message arrives with the attachment already `ready` |
| `BatchSend` (backfill) | the same conversion with `backfill: true` |
| `SendBridgeStatus` (`CONNECTED`, `TRANSIENT_DISCONNECT`, `BAD_CREDENTIALS` / `LOGGED_OUT`, `UNKNOWN_ERROR`) | account status `connected`, `disconnected`, `unpaired`, `error` |
| `LoginProcess` steps `user_input`, `display_and_wait` (qr / code), `complete` | `input` (field types phone / password / code / url / text), `display`, `done`; `cookies`, `client_http`, `webauthn` become `failed` (`unsupported`) |
| `NetworkAPI.HandleMatrixMessage` and the optional `Edit` / `Redaction` / `Reaction` / `ReadReceipt` / `Typing` / `IdentifierResolving` / `ContactListing` interfaces | `message.send` (a synthetic Matrix event from the account's user, resolved by `SendMessageStatus`), `message.edit`, `message.delete`, `message.react`, `chat.read`, `chat.typing`, `chat.resolve`, `contacts.list`; capabilities are derived from which interfaces the connected login implements |

Outbound attachments are served back to the connector through `DownloadMedia` from the send
request's `MediaSource`. The connector's YAML config is loaded from its own example config and
overlaid with the account's `config.network` (a YAML string or an object with the same keys, e.g.
`device_name` for Signal). Everything the virtual homeserver cannot answer (`GetEvent`, power
levels beyond the bot, room tags) is a no-op.

Adding another bridgev2 network (Meta, Slack, Discord, Bluesky, …) is a package like
`internal/adapters/signal`: import the connector, call `connector.New`, register it in
`cmd/chat-bridge`. Only Signal needs cgo (libsignal); the rest are pure Go.

## 12. Minimal adapter walk-through (TypeScript, Matrix)

```ts
const ws = new WebSocket("ws://core:8080/adapter/v1", { headers: { Authorization: `Bearer ${TOKEN}` } });
const rpc = new JsonRpc(ws);                       // any JSON-RPC 2.0 peer library

const { accounts, media } = await rpc.call("hello", {
  protocol: 1, platform: "matrix", adapter: { name: "mx-js", version: "0.1.0" },
  capabilities: ["send.text", "send.media", "message.reply", "message.thread", "message.edit",
                 "message.delete", "message.reaction", "message.history", "chat.read", "chat.typing",
                 "chat.members", "chat.invite", "receipts", "format.html", "format.markdown", "self.update"],
  login_flows: [{ id: "password", name: "Username and password" }, { id: "token", name: "Access token" }],
  config_schema: { type: "object", required: ["homeserver"], properties: { homeserver: { type: "string" } } },
});

for (const acc of accounts) clients.set(acc.id, await openMatrix(acc));   // restore session from acc.data_dir

rpc.handle("login.start", async ({ account_id, flow }) => flow === "password"
  ? { flow, step: "input", input: { fields: [{ name: "user", type: "text" }, { name: "password", type: "password" }] } }
  : { flow, step: "input", input: { fields: [{ name: "token", type: "password" }] } });

rpc.handle("login.submit", async ({ account_id, fields }) => {
  const client = await loginMatrix(accounts[account_id], fields);       // throws → {step:"failed"}
  clients.set(account_id, client);
  await rpc.call("status", { account_id, status: "connected", self: toContact(client.me) });
  return { flow: "password", step: "done", self: toContact(client.me) };
});

rpc.handle("message.send", async ({ account_id, chat_id, client_id, content, reply_to, thread_id }) => {
  const client = clients.get(account_id);
  const body = await toMatrixContent(content, media.get_url, TOKEN);    // fetch attachments, upload to homeserver
  if (reply_to) body["m.relates_to"] = { "m.in_reply_to": { event_id: reply_to } };
  const { event_id } = await client.sendEvent(chat_id, "m.room.message", body, client_id /* txnId */);
  return { id: event_id, chat_id, from_me: true, timestamp: new Date().toISOString(), content, status: "sent", client_id };
});

client.on("Room.timeline", (ev, room) => {
  if (ev.getSender() === client.getUserId() && sentByUs.has(ev.getId())) return;   // our own send, not re-emitted
  queue.push({ kind: "message", message: toMessage(ev), chat: toChat(room), sender: toContact(room.getMember(ev.getSender())) });
});
setInterval(async () => { if (queue.length) { const batch = queue.splice(0, 100); await rpc.call("events", { account_id, events: batch }); } }, 200);
```

An adapter is complete when it passes the conformance list below; everything else is
platform-specific.

## 13. Conformance checklist

- [ ] `hello` with truthful capabilities; every advertised capability has a working method
- [ ] `status` for each account after hello and on every transition, `self` present when connected
- [ ] at least one login flow reaching `done`; `login.cancel` returns to `unpaired`; QR rotation via `login.step`
- [ ] inbound text, one media type, reply, and a `system` message emitted via `events` with `chat` + `sender` hints on first sight
- [ ] media PUT with dedupe; `media.fetch` for a `remote` attachment
- [ ] `message.send` text and media; repeated `client_id` is a no-op
- [ ] resend of an unacknowledged `events` batch after reconnect produces no duplicates
- [ ] unknown platform events surface as `platform_event`
- [ ] `account.remove` leaves nothing in `data_dir`

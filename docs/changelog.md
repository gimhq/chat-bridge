# Changelog

## 2026-09-13 06:40 [progress]

Task `20260913-0530-api-framework-completion` Phase A (plan of the same id): platform-backed endpoints the spec promised.

- Adapter contract: `ChatCreator` (`chat.create`), `ChatUpdater` (rename, no capability), `Backfiller` (`message.history`), `SelfUpdater` (`self.update`), `Blocker` (no capability); wire methods `chat.create`, `chat.update`, `chat.backfill`, `self.update` (with `avatar_url`), `contact.block` in the remote shim
- Core / API: `POST /accounts/{a}/chats` (201, `chat.new`), `PATCH …/chats/{c} {name}` reaches the platform, `GET …/messages?backfill=1` tops a short page up from platform history (stored as history, cursor kept while the platform has more; silently ignored without `message.history`), `GET …/messages/search` (`q`, `chat`, cursor), `PATCH …/self` (name / bio / uploaded avatar → `Account.self`), `PATCH …/contacts/{u} {blocked}`
- Store: schema v3 — `messages_fts` (FTS5 trigram, external content, triggers, rebuilt on migration); terms under three characters use LIKE so short CJK queries work
- Adapters: WhatsApp (create/rename group, blocklist, push name / about / profile photo), Telegram (create group, rename chat/channel, block, profile + photo, `messages.getHistory` backfill), Matrix (create/rename room, `m.ignored_user_list`, display name + avatar, `/messages` backfill with E2EE decryption); Signal (hosted) unchanged
- Tests: core (create/rename/self/block, backfill paging, search), store (FTS paths, rename, block), server routes, remote end-to-end for the new RPCs; the ingest test now waits for the contact sync it raced against
- Docs: `api.md` §4.4 backfill/search semantics, `adapter-protocol.md` §4 wire rows and §10 interface list, `storage.md` FTS tokenizer and migration table

## 2026-09-13 03:30 [progress]

Task `20260913-0252-bridgev2-host-signal` (plan of the same id) completed: mautrix bridgev2 network connectors run as chat-bridge adapters; Signal is the first.

- `internal/adapters/connector`: hosts any `bridgev2.NetworkConnector` behind `adapter.Adapter` by implementing bridgev2's Matrix side (`MatrixConnector` / `MatrixAPI`) as a virtual homeserver — portals → chats, ghosts → contacts, ghost intents' Matrix events → `message` / `message_update` / `message_delete` / `reaction` / `receipt` / `typing` / `member` / `chat` events, `UploadMedia` → `Sink.PutMedia`, `BatchSend` → backfill, bridge states → account statuses; one bridge per account on `accounts/<id>/bridgev2.db` (modernc); login steps mapped onto the step machine (`user_input` → `input`, `display_and_wait` → `display` with `Wait` pushed via `login.step`, `complete` → `done`); sends are synthetic Matrix events resolved by `SendMessageStatus`; edit / delete / react / read / typing / resolve / contacts map to the optional `*HandlingNetworkAPI` interfaces and the advertised capabilities follow them (probe client + connected logins)
- `internal/adapters/matrixcontent`: Matrix content ↔ `model.Content` moved out of the Matrix adapter and shared with the host (`Convert`, `Build`)
- `internal/adapters/signal` (build tag `signal`): mautrix-signal v0.2608.0's connector hosted in-process; `cmd/chat-bridge` registers it when built with the tag; connector settings come from the account's `config.network`
- Build: Dockerfile gains a `libsignal` Rust stage (clones mautrix-signal, `build-rust.sh`), the Go stage is `CGO_ENABLED=1` with `-tags goolm,signal`, the runtime image moves from distroless-static to alpine (musl, libstdc++, zlib); `scripts/build-libsignal.sh` produces `libsignal_ffi.a` in `.tmp/libsignal/` for local tagged builds; the pure-Go gate (`-tags goolm`) is unchanged and a second tagged gate line is skipped when the library is absent
- Docs: `adapter-protocol.md` §11 "Hosting a mautrix bridgev2 connector" (walk-through and checklist renumbered to §12/§13), `api.md` §4.1 Signal config and §7 Signal column, README, AGENTS, architecture
- Tests: fake bridgev2 connector round-trip (login, inbound message with chat/contact hints, duplicate suppression, send, chat info, restart from `bridgev2.db`, removal) plus media, reaction, receipt, typing, edit and delete conversions

## 2026-09-13 02:25 [progress]

Task `20260913-0151-telegram-matrix-hardening` (plan of the same id) completed: Telegram and Matrix adapters hardened.

- Telegram: application credentials move to `adapters.telegram.api_id/api_hash` (`CHATBRIDGE_ADAPTERS_TELEGRAM_*`), per-account `config` only overrides; updates state (`updates.json`) and peer access hashes (`peers.json`) persisted per account with coalesced atomic writes so restarts recover missed updates and resolve known peers; message formatting both ways (entities ↔ markdown subset, `format.markdown` capability)
- Matrix: end-to-end encryption via mautrix `cryptohelper` on a modernc SQLite `crypto.db` (pure Go, `-tags goolm`), pickle key stored in `session.json`; encrypted rooms decrypt/encrypt transparently, encrypted attachments are fetched and decrypted, undecryptable events surface as `unsupported` + `platform.event decrypt_failed`; direct chats are named from the peer's member display name
- Key management (`keys.manage`): `GET /accounts/{a}/keys`, `POST …/keys/verify` (recovery key → cross-sign device + restore key backup), `POST …/keys/export`, `POST …/keys/import`; generic `adapter.KeyManager` with remote `keys.*` RPCs
- Build: `goolm` tag carried by Dockerfile, `.golangci.yml`, README and AGENTS gate

## 2026-09-13 01:47 [progress]

Unified chat bridge: core, storage, HTTP API, three in-process adapters, remote adapter host.

- Specs: `docs/api.md` (consumer HTTP API), `docs/storage.md` (SQLite layout, write paths, retention), `docs/adapter-protocol.md` (adapter contract, in-process Go interface and WebSocket + JSON-RPC wire format)
- Core: accounts with a platform-agnostic login step machine, event log with cursors (long-poll, SSE, HMAC-signed webhooks with backoff), media policy (auto-download by content type, content-addressed blobs, lazy fetch), backfill handling (history replay does not count unread or trigger downloads), read-time sender-name resolution
- Adapters: `whatsapp` (whatsmeow: QR/pairing-code login, full message/media/group/receipt mapping, history sync, incremental contact sync), `telegram` (gotd: phone+code+2FA and QR login, dialogs, media, reactions), `matrix` (mautrix-go: password/token login, unencrypted rooms, edits/redactions/reactions/threads), shared `base` scaffolding
- Remote adapters: `/adapter/v1` WebSocket host (`server.adapter_token`), hello handshake, per-platform instances (`platform/instance`), account binding and rebinding, media PUT/GET
- Config: defaults < YAML file < `CHATBRIDGE_<SECTION>_<KEY>`; `server.token_file`, `server.public_url`
- Tests: store, core (login, ingest, send, media, backfill, instances, webhooks), server, remote end-to-end, adapter conversions

## 2026-09-13 00:56 [progress]

Bootstrapped the repository as a PMA-managed project (stack: `/pma-go`).

- Initialized git on `main`
- Added `AGENTS.md` (with `CLAUDE.md` symlinked to it), `docs/task/`, `docs/plan/`, `docs/architecture.md`, and this changelog
- Repository hygiene: extended `.gitignore` (secrets, local `chat-bridge.yaml`, logs, coverage, IDE/OS files), added `.gitattributes`, `.editorconfig`, `README.md`, and an Apache-2.0 `LICENSE` matching `gimhq/matrix-bridge`

# Changelog

## 2026-09-13 20:37 [progress]

Task `20260913-2034-ignore-call-requests`: calls are ignored instead of rejected.

- Rejecting a call from the bridge hung up on every device of the account (WhatsApp `RejectCall`, Telegram `phone.discardCall`, Matrix `m.call.reject`), so the owner could not answer on the phone
- New action `ignore` and state `ignored`: `POST /accounts/{a}/requests/{id}/ignore` closes the request in the bridge only, without contacting the platform or needing a connected account; the adapter's later "call ended" update does not overwrite it
- `actions` is now `["ignore"]` for calls and `["accept", "reject", "ignore"]` for other pending requests; `POST …/reject` still works for calls and is documented as hanging up everywhere
- Web UI: the request list offers 忽略 (no reject button for calls) and the incoming-call toast's action is 忽略
- Tests: store, core (ignore never calls the adapter), server route, Vitest request list, Playwright smoke (the call is ignored and no `request.answer` reaches the adapter; the toast offers ignore); `api.md` §4.8 updated

## 2026-09-13 20:32 [progress]

Task `20260913-0530-api-framework-completion` Phase B (plan of the same id): Persons, a bridge-local identity over contacts on several accounts (`api.md` §3.8, §4.7).

- Store: schema v5 — `persons`, `person_links` (a contact belongs to at most one person), `person_unlinks` (pairs the owner split), `contacts.phone_norm` (digits only, at least 7, indexed, backfilled); `person_id` joined at read time on Contact, direct Chat (chat id is the counterpart, or the counterpart is the other member for Matrix rooms) and message `sender`
- Core / API: `GET|POST /persons`, `GET|PATCH|DELETE /persons/{p}`, `POST /persons/{p}/links`, `DELETE /persons/{p}/links/{account}/{user}`, `POST /persons/{p}/merge`, `GET /persons/{p}/chats`, `GET /persons/{p}/messages?scope=direct|all` (merged across accounts, cursor paged), `GET /persons/suggest`; `person=` on `/accounts/{a}/chats`, `/events` and `/events/stream`; `person.updated` (with `deleted` / `merged_into`) plus `contact.updated` for contacts whose `person_id` changed; linking a contact that belongs to another person is `409`
- Auto-link by phone (`persons.auto_link_by_phone`, default on): whenever a contact row changes (contact events, message sender hints, address-book sync), other accounts' non-self contacts with the same phone are matched; one person involved → join it, none → create one with all of them, several → leave to suggestions; split pairs are never re-joined
- Web UI: "人" page (search, tag filter, phone-match suggestions with one-click grouping, create), person detail (linked identities with unlink, direct-chat channels, notes, merged timeline with direct / all scope, edit, merge, delete), "link to person" and a person column on the contacts page, "view this person" in the chat header
- Tests: store (phone normalisation, split pairs, suggestions, Matrix-style DM person, person messages paging, merge, delete), core (auto-link across two accounts, split pair, conflict, merge, person event filter, delete events), server routes, config env override; Vitest 39 tests; Playwright smoke 14/14 with a new check that two accounts sharing a phone become one person, show a merged timeline and unlink from the UI

## 2026-09-13 19:53 [progress]

Task `20260913-1903-web-ui-and-requests` (plan of the same id): embedded management web UI; requests for invites and calls.

- Requests (`api.md` §4.8): schema v4 `requests` keyed by the adapter's stable key; `Request` carries `actions` (`["reject"]` for calls, `["accept","reject"]` otherwise); `GET /accounts/{a}/requests[/{id}]`, `POST …/accept|reject` (`409` unless pending, `400` accepting a call); `request.new` / `request.updated`; pending requests expire in the GC loop; `AccountStats.requests_pending`
- Adapter contract: event kind `request` (re-emitting a key updates names or resolves the request; a first sighting that is already terminal is ignored; a later `created_at` reopens), optional `RequestAnswerer`, wire method `request.answer`
- Adapters: WhatsApp incoming calls (reject) and group invite messages (accept joins, reject dismisses locally); Telegram incoming calls (discard as busy) and join requests to owned groups (approve / dismiss); Matrix room invites (join / leave; invite state no longer creates chat rows) and `m.call.invite` (`m.call.reject`, `m.call.hangup` for VoIP v0); hosted Signal has no request source
- Web UI in `web/` (React 19, Vite 8, TanStack Router and Query, Tailwind v4, shadcn/ui base-nova on Base UI), built into `internal/webui/dist`, embedded with `go:embed` and served at `/ui/` with SPA fallback, immutable asset caching and a strict CSP; pages for accounts (create from the platform config schema, login wizard, reconnect / logout / delete, profile), chats (list, archive, mute, create and rename groups, search), the timeline (media via authorized blob fetch, replies, reactions, retract, send text and files, older history), contacts (alias, block, open chat), requests (accept / reject, toast with a reject action for calls), live events and system (status, platforms, webhooks); the event stream is read with `fetch` from the current cursor so the token never goes into a URL
- Build and tooling: Dockerfile bun stage builds the UI before `go build`; `web/go.mod` stub keeps `web/node_modules` out of `./...`; TypeScript pinned to 6.0 for typescript-eslint (`docs/decisions/2026-09-13-web-typescript-6.md`); nsl for the web dev loop only; web gate added to AGENTS and README
- Fixed during verification: shadcn init wired `cn` to an unrelated npm package named `cn`; ESLint autofix escaped quotes inside Tailwind arbitrary variants (button icons lost their size) and dropped spaces in mixed JSX text; the live stream replayed retained history and re-toasted old requests on every load
- Tests: Go store / core / server / remote request lifecycle and adapter conversion tests; Vitest 35 tests (45% statements overall, 81% for `shared/lib`); Playwright smoke in a sibling container against the real binary with a fake remote adapter: 13/13 checks (sign-in, accounts, chat send and mark read, reject call and accept invite, live toast, events, system, dark theme and deep links, create account with QR login, compact icons, no console errors)
- Docker: `docker build --target test .` (Go tests with `goolm,signal`) and the full image build pass; the 161 MB runtime image serves `/ui/` with its assets and lists whatsapp, telegram, matrix and signal under `/v1/platforms`

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

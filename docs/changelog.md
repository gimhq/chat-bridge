# Changelog

## 2026-10-06 06:11 [progress]

Task `20261003-1745-tokens-web-ui` (plan of the same id): the management UI manages scoped tokens.

- System page: a tokens card lists name, scope summary, read-only badge, creation and last use; create and edit in a dialog with a read-only switch, persons as toggles, and contacts and chats picked per account with search; the secret is shown once with a copy button; revoke asks for confirmation
- Sign-in screen: a scoped token gets "管理界面需要管理员令牌" instead of "服务器返回 403"
- Tests: Vitest for the card (list, revoke, create from the pickers, edit) and the sign-in message
- Delivery checks on the whole change set: a database created by the v0.1.0 binary upgrades to schema 2 with its rows intact; token create, scope enforcement, read-only, scope edit and revoke exercised over HTTP against the built binary; a browser run (Playwright 1.63, Chromium) drives the card end to end against the embedded UI, 8 steps, no console errors. The run found two buttons with the same accessible name (a contact and its direct chat are both titled by the contact); the labels now name the kind
- `bun run test` crashes in the coverage merge when vitest runs on the bun runtime (an image without Node); with Node, as on the CI runner, 46 tests pass

## 2026-10-03 17:46 [progress]

Task `20261003-1744-token-read-only-and-resolve` (plan of the same id): the user's answers to the open questions of the scoped-token plan.

- `scope.read_only`: a read-only token gets `403 forbidden` on send, edit, delete, react, typing, read receipts, upload and `chats/resolve` (`Core.allowWrite`); reads are unchanged
- `POST /accounts/{a}/chats/resolve` is open to scoped tokens for contacts in their scope: the handle must be the contact's user id, stored handle or stored phone, the platform must answer with that contact, and the resulting direct chat is stored so the token can use it. Other handles answer `404` before the adapter is asked
- Confirmed, no change: a group is visible only when it is listed; filtering is by chat
- Web UI for tokens is task `20261003-1745-tokens-web-ui`, to be done later
- Docs: `api.md` §2.1 and §4.11, README. Tests: `TestReadOnlyToken`, `TestScopedTokenStartsChat`, route allowlist

## 2026-10-03 17:37 [progress]

Task `20261003-1654-scoped-tokens` (plan of the same id): scoped API tokens.

- `server.token` stays the admin token; additional tokens live in the new `tokens` table (migration 2, SHA-256 of the secret only) and are managed through `GET/POST /tokens`, `PATCH/DELETE /tokens/{id}`; `GET /tokens/self` tells any token what it is
- A scope lists persons, contacts and chats; it resolves on every request to allowed contacts and chats (direct chats of the allowed contacts plus the listed chats). Chats, messages, search, contacts, persons, media, events and the account list are limited to it in SQL; out-of-scope objects answer `404`, management routes `403 forbidden`
- `internal/server`: `adminOnly` wraps every route a scoped token must not reach; `TestScopedTokenRouteAllowlist` walks the router and fails on a route without a decision
- Decisions taken with the proposal's defaults: a contact's messages in groups stay hidden unless the group is listed; no read-only switch; `chats/resolve` is admin-only; webhooks are admin-only; no UI page yet
- Docs: `api.md` §2, §2.1, §4.11, §10; `storage.md` §2, §8; README, AGENTS, architecture
- Tests: store (token CRUD, `Only` filters, event scope), server (in-scope and out-of-scope access per route family, events, media, persons, revocation, route walk); removing the search or event filter or one `adminOnly` makes them fail

Task `20261003-1652-pma-go-alignment` (plan of the same id), phases 1 to 3: the repository meets the `/pma-go` baseline.

- Lint: `golangci-lint run` went from 139 issues to 0. Doc comments on about 90 adapter methods (`adapter/fake`, `adapters/remote`, `adapters/connector`, `adapters/matrix`, `adapters/telegram`); four comments pointed at a missing `docs/chat-api-spec.md`, now `docs/api.md`; unchecked `Close`/`Remove` errors, shadowed builtins and unused parameters fixed; deprecated calls replaced (`GetUserLogins`, `MemberMap` with a fallback to the old list, in-place attachment encryption and decryption, gotd `Duration`); WhatsApp file lengths clamp instead of overflowing; deliberate cases carry a scoped `//nolint` with the reason
- `.golangci.yml` follows the starter (explicit linter list with `gocritic`, test-scoped exclusions for gosec and errcheck, timeout), keeping `errorlint`
- Spec sync: `api.md` loses the WhatsApp-only migration section, the four request capabilities no adapter declared, `send.poll`, the Matrix `sso` flow and the upload `kind` field (the send request's `content.type` already says it); `PATCH chats` documents that only `name` reaches the platform; Keys moves from §4.7a to §4.12; `storage.md` §7 separates the retention that runs from the planned part and drops the stale §10
- New behavior: `?raw=1` on `GET …/chats/{chat}/messages`; `GET /readyz` pings the database
- Tooling: Go 1.27 with a `toolchain` directive (`go.mod`, Dockerfile); `Taskfile.yml` with `check`; the gate gains `go mod tidy` with no diff and `-cover`; decision records for SQLite with embedded migrations and for stdlib `flag`; example config and `.env.example` list `persons.auto_link_by_phone` and `adapters.telegram.bridgev2`
- Not done: phase 4 (dependency bumps) is task `20261003-1806-dependency-bumps`; retention and GC stay with task `20260913-0530-api-framework-completion`

Task `20261003-1646-ci-workflow`: `.github/workflows/ci.yml` runs on push to `main` and on pull requests.

- Jobs: web UI gate; Go gate (gofmt, vet, golangci-lint v2.14.0, tests with coverage, build, tidy check, `tgbridge` line); Docker `test` stage with a layer cache, the only check of the `signal` tag. Actions are pinned to commit SHAs
- Server tests waited on a background contact sync by luck and failed about one run in eight; `env.connected` now waits for the synced contact (60 consecutive runs pass)

## 2026-09-14 02:45 [progress]

Task `20260914-0206-contact-detail` (plan of the same id): a detail page for every contact.

- API: `GET /accounts/{a}/contacts/{user}/chats` (direct chats with them and the chats they are a current member of), `GET /accounts/{a}/contacts/{user}/messages?scope=direct|all` (the conversation, or also their messages in groups), `from=` on `GET /accounts/{a}/requests`; `api.md` §4.6 and §4.8
- UI `/accounts/{a}/contacts/{user}`: profile (all names, phone, handle, email, bio, id), cross-platform card (the person and its other identities, or link to a person), shared chats, requests from them, message timeline with 私聊 / 含群聊; actions message, alias, view person, block
- Entry points: contact names in the contacts table, 查看联系人 in direct chat headers, sender names in messages, linked identities on the person page
- Tests: store, core and server for the new endpoints; Vitest for the detail page; Playwright smoke 7/7 against a scripted remote adapter

## 2026-09-14 02:10 [progress]

Task `20260914-0209-schema-reset`: the database schema starts over from one base version.

- `internal/store` keeps a single migration, the final schema of former versions 1-6 (accounts with `adapter`, contacts with `phone_norm`, trigram FTS, requests, persons, sender and membership indexes) without the backfills; databases created by earlier builds report a newer schema and must be deleted
- `storage.md` §2 DDL completed (missing `accounts.adapter`, `contacts.phone_norm`, indexes) and §8 describes the reset

Task `20260914-0206-contact-detail` proposed: a contact detail page with shared chats, messages across chats and requests.

## 2026-09-14 01:45 [progress]

Task `20260913-2319-telegram-bridgev2-connector` (plan of the same id): mautrix-telegram's bridgev2 connector runs as a second Telegram instance next to the gotd adapter.

- Connector host: `WithInstance` (adapter instance id), `WithNetworkDefaults` (server-wide connector YAML between the example config and the account's `config.network`), optional `WithMaxFileSize`; the fixed 100 MB cap is gone (connector capabilities decide, `SetMaxFileSize` gets an unbounded value), uploads above an explicit cap fail with `bridgev2.ErrMediaTooLarge`, and `UploadMediaStream` streams the connector's temp file into the sink instead of buffering it
- `internal/adapters/tgbridge` (build tag `tgbridge`, cgo for mautrix-telegram's bundled libwebp only): platform `telegram`, instance `bridgev2`, `api_id` / `api_hash` from `adapters.telegram.*`, animated stickers unconverted; dependency `go.mau.fi/mautrix-telegram v0.2608.0` (its own gotd fork, no conflict with `github.com/gotd/td`)
- Config `adapters.telegram.bridgev2` (default `false`) registers the instance, so `POST /accounts` without `adapter` keeps working for single-instance deployments; `cmd/chat-bridge` registers tagged adapters from `adapters_<tag>.go` `init()` functions (`adapters_default.go` removed)
- Build: Dockerfile and `.golangci.yml` carry `tgbridge`; gate line for the tag in `AGENTS.md` and README; release binaries unchanged (pure Go)
- Verified: host unit tests (instance, config precedence, upload cap and streaming; race-clean), tagged vet and a real-connector test (initialises against the virtual homeserver), pure-Go gate (gofmt, vet, tests, build), `docker build --target test .` with `goolm,signal,tgbridge`; smoke run with the flag on: `GET /platforms` lists `telegram` instances `bridgev2` and `local`, `POST /accounts` without `adapter` answers 400, a `bridgev2` account's `qr` login returns `tg://login?token=…`; stripped binary 102.6 MB (66.5 MB without the tag). End-to-end messaging needs a real Telegram login
- Docs: `adapter-protocol.md` §11 (options, Telegram), `api.md` §2.2 and platform table, `chat-bridge.example.yaml`, README

## 2026-09-13 23:40 [progress]

Task `20260913-2243-whatsapp-lid-and-new-types` (plan of the same id): WhatsApp addresses users by LID, like mautrix-whatsapp; newer message types are mapped.

- Adapter contract: event kind `identity` (`user_id` → `new_id`) and optional `IdentityResolver` / wire method `identity.resolve`; the fake and the remote shim implement both
- Store: `ReID` moves a user and the direct chat with them to a new id in one transaction (contact merged with local alias, blocked flag and person link kept; chat merged, duplicate messages dropped, unread summed, last message recomputed; senders, members, reactions, receipts, mentions, system notices, requests, self id rewritten) and skips ids it never stored; schema v6 indexes `messages(account_id, sender_id)` and `chat_members(account_id, user_id)`
- Core: `identity` events re-ID inside the ingest transaction and emit `chat.updated` (with `merged_from`) and `contact.updated`, then auto-link; after every `connected` status the core hands the account's stored user and direct-chat ids to `identity.resolve` and re-IDs the answer before the contact sync
- WhatsApp: every emitted user and DM id resolves to the LID when whatsmeow's LID map knows it (event alternates first, the account's own LID for itself), including history sync, receipts, typing, presence, members, group notices, mentions, calls, invites, push names, `ResolveChat`, `GetChat` and send results; the first phone→LID sighting in a session emits `identity`; contacts are one per user with the phone from the phone form and names merged from both whatsmeow rows (LID-only rows no longer dropped); `CanonicalIDs` answers through `GetManyLIDsForPNs` and re-emits stored LID users with the phone number the LID map knows (contacts stored before the mapping arrived had none)
- WhatsApp message types: HD dual uploads are stored under the parent id (a duplicate when the parent exists), motion-photo children, album headers and history bundles are skipped, templates / highly structured / interactive business messages become text, `messageHistoryNotice` becomes system `history_shared`, `MASK_LINKED_DEVICES` placeholders become system `primary_device_only`; UI labels for both kinds
- Unnamed direct chats take their counterpart's contact name when read (chat list, chat, person chats, `chat.new` / `chat.updated`); the name is not stored, so it follows the contact
- Verified on the dev account: 89 phone ids re-IDed at connect, 82 of 108 chats and all senders except WhatsApp's `0@s.whatsapp.net` are LIDs, `235978975346820@lid` carries +66995618240 and the address-book name; `api.md` (system kinds, `chat.updated`), `adapter-protocol.md` (§4, §5.3, §10), `storage.md` (schema v6) updated

## 2026-09-13 22:45 [progress]

Task `20260913-2243-telegram-default-app` closed: built-in Telegram credentials were dropped before commit at the user's request.

- Credentials stay server-wide configuration (`CHATBRIDGE_ADAPTERS_TELEGRAM_API_ID|HASH` or `adapters.telegram.*`), inherited by every account; `api.md` §2.2 and platform table, `chat-bridge.example.yaml`, README say so explicitly

Task `20260913-2243-whatsapp-lid-and-new-types` opened: LID chats and senders are not resolved to phone numbers, and album / HD-image / template / linked-device placeholder messages render as unsupported (findings in the task file).

## 2026-09-13 22:40 [progress]

Task `20260913-2234-release-workflow`: GitHub release workflow.

- `.github/workflows/release.yml`: on a `v*` tag, web UI gate and Go gate, then pure-Go binaries (`-tags goolm`, no cgo, embedded UI) for linux / darwin / windows × amd64 / arm64 as `.tar.gz` / `.zip` with `checksums.txt` in a GitHub release (prerelease when the tag has `-`); `workflow_dispatch` builds the archives as artifacts only
- Signal is not in the release binaries (cgo + libsignal); it remains in the container image
- Vite dev server accepts any host (`allowedHosts: true`) so nsl can publish it under non-`localhost` domains
- README: Releases section

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

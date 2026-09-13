# 20260913-0151-telegram-matrix-hardening Harden the Telegram and Matrix adapters

- **status**: completed
- **createdAt**: 2026-09-13 01:51
- **approvedAt**: 2026-09-13 02:13
- **relatedTask**: 20260913-0151-telegram-matrix-hardening

## Context

Both adapters compile, pass conversion tests, and were smoke-tested up to the login step, but
neither has run against a real account. Investigation of the current code and the libraries:

Telegram (`internal/adapters/telegram`, gotd/td v0.161.0):

- `api_id` / `api_hash` are required in every account's `config` (`adapter.go` `AddAccount`).
  They identify the *application*, not the account, so they belong in the server config; the
  account config should only override them.
- `updates.New(updates.Config{Handler: dispatcher})` runs with the default in-memory
  `StateStorage`: pts/qts/seq/date are lost on restart, so updates missed while offline are never
  fetched via `getDifference`. `updates.StateStorage` is a 10-method interface (verified in
  `telegram/updates/state_storage.go`); a JSON file per account satisfies it.
- `peers.Options{}.Build(api)` uses `InmemoryStorage`: access hashes vanish on restart, so
  `resolvePeer` for a user who is not in the last 100 dialogs fails until they write again.
  `peers.Storage` (Save/Find/SavePhone/FindPhone/GetContactsHash/SaveContactsHash) is small.
- Text formatting is dropped both ways: inbound `Entities` are ignored except mentions; outbound
  `format: markdown` is sent as plain text. gotd ships `telegram/message/html` (HTML → entities)
  and `styling`; the reverse (entities → markdown) is a ~60-line walk over offsets.
- Capabilities declared: no `format.markdown`, no `message.thread`.

Matrix (`internal/adapters/matrix`, mautrix-go v0.30.0):

- `m.room.encrypted` events are stored as `content.type: unsupported` (`events.go` `onEncrypted`)
  and encrypted attachments are marked `failed`. Element creates DMs encrypted by default, so the
  adapter is unusable for most private chats.
- mautrix's `crypto/cryptohelper` is usable in the pure-Go build. It imports
  `go.mau.fi/util/dbutil/litestream` (which registers `github.com/mattn/go-sqlite3`), but under
  `CGO_ENABLED=0` that driver compiles to a stub and is only touched when a file *path* is passed;
  passing a `*dbutil.Database` built with `dbutil.NewWithDB(<modernc sql.DB>, "sqlite3")` keeps
  everything on modernc. Verified with a throwaway program: `CGO_ENABLED=0 go run -tags goolm`
  constructs the helper and creates its tables; without `-tags goolm` the build fails because the
  default olm backend is libolm (cgo). The helper manages the crypto store, the state store, key
  upload, megolm sharing, and key requests.
- `mautrix.Client.Crypto` is a 5-method interface (`Encrypt`, `Decrypt`, `WaitForSession`,
  `RequestSession`, `Init`); `SendMessageEvent` encrypts automatically when it is set and the
  state store says the room is encrypted; `DefaultSyncer` needs `OnSync(mach.ProcessSyncResponse)`,
  `OnEvent(cli.StateStoreSyncHandler)`, and `OnEventType(StateMember, mach.HandleMemberEvent)`.
- Direct chats have no name until `GET /chats/{id}`: `chatHint` only sets kind; member events
  carry the other user's display name but are not used for the chat name.

Shared: `internal/config` maps `CHATBRIDGE_<SECTION>_<KEY>` by splitting on the first
underscore, so a third level (`adapters.telegram.api_id`) needs an explicit rule. Dockerfile,
README and AGENTS quality gates must carry `-tags goolm`.

## Proposal

Telegram

1. **Adapter-level credentials.** New config section:
   ```yaml
   adapters:
     telegram:
       api_id: 12345
       api_hash: "…"      # secret
   ```
   Env: `CHATBRIDGE_ADAPTERS_TELEGRAM_API_ID` / `_API_HASH` (env mapping: when the section is
   `adapters`, the next token is the platform). `telegram.New(log, telegram.Defaults{AppID,
   AppHash})`; `AddAccount` fills missing account values from the defaults; `config_schema` drops
   them from `required`. `GET /platforms` unchanged.
2. **Persistent state.** `internal/adapters/telegram/storage.go`: `fileState` implementing
   `updates.StateStorage` and `filePeers` implementing `peers.Storage`, both a JSON document
   written atomically (temp + rename) to `accounts/<id>/updates.json` and `peers.json`, with
   writes coalesced (dirty flag, flush at most every 2 s and on stop). Wired into
   `updates.Config{Storage: …}` and `peers.Options{Storage: …}`.
3. **Formatting.** `format.go`: `entitiesToMarkdown(text, entities)` for inbound (bold, italic,
   strike, code, pre, text links, mention names; `format: "markdown"` only when at least one
   entity applies) and `markdownToHTML` for outbound fed to `html.String(...)` so `Text`,
   captions and `Edit` honour `format: markdown`. Declare `format.markdown`.

Matrix

4. **E2EE with mautrix `cryptohelper`.** `internal/adapters/matrix/crypto.go` (~60 lines):
   open `accounts/<id>/crypto.db` with modernc, wrap it in `dbutil.NewWithDB(db, "sqlite3")`,
   `cryptohelper.NewCryptoHelper(cli, pickleKey, db)`, `helper.Init(ctx)`, `cli.Crypto = helper`.
   Pickle key: 32 random bytes generated at first login, stored in `session.json`. The helper
   registers the syncer hooks, uploads device keys, shares megolm sessions, and requests missing
   keys; `AllowUnverifiedDevices` is left at its default (true) and `DecryptErrorCallback` emits a
   `platform.event`. Decrypted events reach the existing `onMessage` (the helper rewrites the
   event type before dispatch); encrypted attachments keep the `file` JSON in `remote_ref` and
   `FetchMedia` decrypts with `attachment.Decrypt` after download. Logout closes the helper and
   deletes `crypto.db`. Build: `-tags goolm` in Dockerfile `go build`/`go test`, README, AGENTS
   gate.
5. **Direct-chat names.** In `onMember`, when the room is direct and the member is not us, emit a
   `chat` hint with `Name: displayname`; `loadDirect` also seeds `roomKind` so the first message
   of a DM already carries `kind: direct`.

6. **Matrix key management** (E2EE is unusable in practice without it: a fresh login cannot read
   history, other clients show the bridge device as unverified, and keys are lost on
   re-login). Generic surface, adapter-optional interface `adapter.KeyManager` (capability
   `keys.manage`; WhatsApp/Telegram answer `422 unsupported`):
   - `GET /accounts/{a}/keys` → `{device_id, fingerprint, cross_signed, backup: {version,
     enabled}, sessions}` (`mach.OwnIdentity`, `ResolveTrust` of the own device,
     `KeyBackupVersion`, inbound session count).
   - `POST /accounts/{a}/keys/verify {recovery_key}` → fetch cross-signing keys from SSSS with
     the recovery key (`FetchCrossSigningKeysFromSSSS`), cross-sign our device
     (`SignOwnDevice`) so every other client trusts it, then verify and restore the server-side
     key backup (`GetAndVerifyLatestKeyBackupVersion` + `DownloadAndStoreLatestKeyBackup`) so
     pre-login history decrypts. Response `{cross_signed, backup_version, sessions_imported}`.
     Recovery key is used once and never stored.
   - `POST /accounts/{a}/keys/export {passphrase}` → Element-compatible encrypted key file
     (`crypto.ExportKeys`); `POST /accounts/{a}/keys/import` (multipart `file` + `passphrase`)
     → `mach.ImportKeys`. Moves sessions between deployments and gives an off-site backup.
   - Automatic: with a verified backup version, sessions the bridge creates or receives are
     uploaded to the backup (confirm during implementation which part mautrix does on its own).
   Wire format for remote adapters: `keys.status`, `keys.verify`, `keys.export`, `keys.import`
   RPCs, added to `adapter-protocol.md` §4 with the same shapes.

Docs: `docs/api.md` §2.2 (new config keys) and §4.1 config table (Telegram keys now optional),
§7 mapping (Matrix E2EE supported, Telegram markdown), `docs/architecture.md` (crypto.db under
the account dir), `chat-bridge.example.yaml`, `.env.example`, changelog.

Tests (RED first): config env mapping for `adapters.*`; Telegram `fileState`/`filePeers`
round-trip and coalescing; `entitiesToMarkdown` / `markdownToHTML`; Matrix
crypto database opening and helper construction on modernc (temp dir), encrypted-attachment
`remote_ref` round-trip, and `onMember` DM naming; existing suites unchanged.

## Risks

- E2EE correctness now rests on mautrix's own helper (the same code its bridges ship); our
  surface is the database wiring and the decrypt-failure path. Still untested against a
  homeserver until a real account is used.
- dbutil runs its SQL upgrade scripts with dialect `sqlite3` against modernc; the scripts are
  plain SQLite and modernc is a full port, but a mismatch would surface at `Init`. Covered by
  the round-trip test opening a real temp database.
- `-tags goolm` becomes mandatory for every `go build/vet/test` that touches the matrix package
  (the probe showed the untagged build fails outright under `CGO_ENABLED=0`, and with cgo it
  would need libolm installed). Mitigation: put the tag in the Dockerfile, README, and the
  AGENTS quality-gate command, and add `-tags goolm` to `.golangci.yml` `run.build-tags`.
- Telegram state file: a crash between `SetPts` and the flush loses ≤2 s of pts; the gaps
  manager then re-fetches that window on the next start (safe, at worst duplicates that the
  store dedupes).
- `adapters.telegram.api_hash` in the YAML is a secret in a file; same posture as
  `server.token`, documented.

## Scope

- `internal/config/config.go`, `config_test.go` — `adapters.telegram.*`, env mapping
- `cmd/chat-bridge/main.go` — pass defaults, build tag docs
- `internal/adapters/telegram/{adapter.go, events.go, send.go}` + new `storage.go`,
  `format.go`, tests
- `internal/adapters/matrix/{adapter.go, events.go, send.go}` + new `crypto.go`, `keys.go`, tests
- `internal/adapter/adapter.go` (`KeyManager`, `CapKeys`), `internal/core/keys.go`,
  `internal/server/handlers.go` (`/accounts/{a}/keys*`), `internal/adapters/remote/adapter.go`
  (RPC forwarding)
- `Dockerfile`, `README.md`, `AGENTS.md`, `chat-bridge.example.yaml`, `.env.example`
- `docs/api.md`, `docs/architecture.md`, `docs/changelog.md`
- New dependencies: none beyond mautrix subpackages already in `go.sum` (`crypto`,
  `sqlstatestore`, `go.mau.fi/util/dbutil`); `go mod tidy` may promote them to direct.

Roughly 1 300 lines of Go including tests; two to three working sessions.

## Alternatives

- Hand-written helper on `crypto.OlmMachine`: ~200 lines duplicating what `cryptohelper` does;
  only justified if the helper had required cgo, which the probe disproved. Rejected.
- Telegram state through `gotd/contrib` (bbolt/pebble storages): proven, but adds a dependency
  and a second database engine for a few hundred bytes of state. Rejected in favour of JSON files.
- Skip Matrix E2EE and document "unencrypted rooms only": simplest, but excludes ordinary DMs
  on every mainstream client, which defeats the point of the adapter. Not recommended.
- Forum topics as `thread_id` and Telegram `message.history` backfill: real gaps, but orthogonal;
  proposed as follow-up tasks rather than widening this one.
- Bridge-wide secrets at rest (a `security.master_key` from file/env, AES-GCM envelopes for Matrix
  and Telegram `session.json`, per-account pickle keys derived by HKDF, `x-secret` account config
  fields in `chatbridge.db`, a `rekey` command; WhatsApp's `whatsmeow.db` stays plaintext unless
  the filesystem is encrypted): valuable and related, but it cuts across every adapter, the
  store, and the CLI. Proposed as its own task right after this one rather than folded in.

## Annotations

(none yet)

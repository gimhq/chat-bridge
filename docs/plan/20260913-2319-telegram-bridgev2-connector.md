# 20260913-2319-telegram-bridgev2-connector Host mautrix-telegram's bridgev2 connector alongside the gotd adapter

- **status**: completed
- **createdAt**: 2026-09-13 23:19
- **approvedAt**: 2026-09-14 00:05
- **relatedTask**: 20260913-2319-telegram-bridgev2-connector

## Context

Findings (mautrix-telegram v0.2608.0, the latest on the module proxy; mautrix-go v0.30.0):

- mautrix-telegram is a Go bridgev2 bridge. `pkg/connector` exposes `TelegramConnector` /
  `TelegramClient`, same shape as `sigconn.SignalConnector`, so `connector.New(...)` hosts it with
  no host rewrite. It requires `maunium.net/go/mautrix v0.30.0` (ours) and `go.mau.fi/util v0.10.0`
  (ours is newer), so the module graph resolves without upgrades.
- **No gotd conflict**: it ships its own gotd fork under `go.mau.fi/mautrix-telegram/pkg/gotd/...`
  (different import path), so it links next to our `github.com/gotd/td v0.161.0`. The cost is a
  second copy of the generated `tg` schema in the binary.
- **cgo is required, but no external library**: `handlematrix.go` imports `go.mau.fi/webp`, which
  bundles libwebp 1.6.0 C sources (`#cgo LDFLAGS: -lm`). `CGO_ENABLED=0` fails; with cgo it builds
  here with the local gcc. A stripped test binary importing only the connector is 46.5 MB (it
  includes mautrix/bridgev2 code chat-bridge already links); the current stripped chat-bridge is
  66.5 MB, so an estimated +30–40 MB (not measured on the combined binary).
- Login flows: `phone` (user_input phone → 2FA code → password), `qr` (display_and_wait qr, then an
  optional password input), `bot_token`, `manual`. All map onto the host's existing step
  conversion (`applyStep` / `waitStep`), including input after a display step.
- Config: `api_id` / `api_hash` / `device_info` / `animated_sticker` / `sync` / `takeout` / `proxy`
  in the connector YAML. The embedded example carries a placeholder `api_id: 12345`, so without an
  override logins fail at Telegram. The host today only overlays `config.network` per account; the
  server-wide `adapters.telegram.api_id|api_hash` are not applied.
- Matrix-side usage is within what `virtualMatrix` implements: `GenerateContentURI`,
  `ParseContentURI`, `GhostIntent`, `ServerName`, `UploadMediaStream`, and the real
  `commands.Processor` (`Init` type-asserts it; the host already passes it). Direct media is only
  used when the Matrix side calls `SetUseDirectMedia`, which the host never does, so media is
  uploaded eagerly through the sink as with Signal. `MatrixAPIWithArbitraryRoomState` is only for
  the image-pack command.
- Sticker conversion shells out to `lottieconverter` / `ffmpeg` when present; neither is in the
  image, so animated stickers arrive as gzipped lottie (`video/lottie+json`) unless the target is
  set to `disable`/`png`.
- Core coexistence: adapters are keyed `platform/instance` (`internal/core/core.go`
  `registryKey`). The host sets no `Info.Instance`, so a second `telegram` adapter would default
  to `local` and **overwrite** the gotd adapter in the registry. With a distinct instance, existing
  accounts stay bound to `local` (bound at an earlier single-instance start), and the web UI already
  offers an instance select when a platform has several instances.
- **Behaviour change**: with two instances, `POST /accounts {platform: "telegram"}` without
  `adapter` returns 400 (`internal/core/accounts.go` `CreateAccount`, "several adapters"), and any
  still-unbound Telegram account goes to `account_not_ready`.
- Release workflow builds `CGO_ENABLED=0 -tags goolm`; a tagged package stays out of it. The
  Dockerfile is already cgo (`-tags goolm,signal`).
- IDs differ between the two implementations (bridgev2 portal/user/message ids vs the gotd
  adapter's), so an account cannot move between instances without a new login and a fresh history;
  `PATCH {adapter}` already only allows moving unpaired accounts. Telegram allows several sessions,
  so the same phone number can be logged in on both instances at once.
- License: mautrix-telegram is AGPL-3.0, like mautrix-signal already linked.
- Storage split (answering "can we reuse its database"): bridgev2's `message` table holds only id
  mapping and metadata (`id`, `part_id`, `mxid`, room, sender, timestamp, edit count, reply ids,
  `metadata` JSON) — no body; a real bridge keeps bodies in the homeserver, which in our design is
  `chatbridge.db`. So bridgev2.db cannot serve the API, and the overlap with our store is limited to
  chat/contact names. Connector state lives next to it (`telegram_user_state` / `channel_state`
  pts, `telegram_access_hash`, `telegram_username`, `telegram_phone_number`, `telegram_file` MXC
  cache, `telegram_topic`; Signal: `signalmeow_*`).
- Sharing one database is technically possible: every bridgev2 table is keyed by `bridge_id`, the
  host sets no `dbutil` owner (the owner check is skipped), and the connector stores use their own
  version tables (`telegram_version`, `signalmeow_version`); no table name collides with ours
  (`message`/`reaction` vs `messages`/`reactions`). Blockers: `telegram_file` caches MXC URIs per
  Telegram file location without an account column, so a second account would reuse another
  account's `media` row; `DELETE /accounts` could no longer `rm -rf` the account directory; merging
  into `chatbridge.db` breaks `docs/storage.md` §1 ("adapters never write to chatbridge.db") and
  mixes two migration systems on our single-writer connection.
- The `manual` login flow takes "Session data JSON" (auth key + DC), so an account on the gotd
  instance could be moved to `bridgev2` without a new login; the same auth key must not run on
  both instances at once.
- Media size: the host ignores the size argument of `UploadMediaStream` and buffers the whole file
  in memory before `Sink.PutMedia`. mautrix's real Matrix side returns `bridgev2.ErrMediaTooLarge`
  above the upload limit, which Telegram's converter turns into a "Too large file <name>" notice.
  The fixed `maxFileSize` (100 MB) is only handed to `SetMaxFileSize`, which Telegram uses for
  backfill downloads; its outbound capability cap is 2 GB.

## Proposal

1. Host options in `internal/adapters/connector/host.go`:
   - `WithInstance(id)` → `Info.Instance`, so the hosted adapter registers as `telegram/bridgev2`.
   - `WithNetworkDefaults(yaml string)` → overlaid between the connector's example config and the
     account's `config.network` in `loadNetworkConfig`.
   - No host-wide size cap: the `maxFileSize` constant goes away. Platform caps already come from
     each connector's capabilities (Signal 100 MB, Telegram 2 GB; bridgev2 rejects oversized
     outbound media in `portal.go`). `SetMaxFileSize` receives a large value so Telegram backfill
     (`min(maxFileSize, 2000 MiB)`) is not starved. An optional `WithMaxFileSize(n)` lets a
     platform package set a lower cap; above it `UploadMedia` / `UploadMediaStream` return
     `bridgev2.ErrMediaTooLarge`. `UploadMediaStream` streams through a temp file into
     `Sink.PutMedia` instead of buffering in memory.
   - Tests: instance in `Info`, defaults overlay precedence (example < defaults < account),
     oversized upload rejected with `ErrMediaTooLarge`.
   - Storage stays one `bridgev2.db` per account (see Context: shared database is rejected).
2. `internal/adapters/tgbridge` (build tag `tgbridge`):
   `New(log, apiID, apiHash)` = `connector.New(log, "telegram", factory, WithInstance("bridgev2"),
   WithProbe(&tgconn.TelegramClient{}), WithNetworkDefaults(<api_id, api_hash, animated_sticker.target: disable>))`.
3. Registration: `cmd/chat-bridge` switches `extraAdapters` from the `signal` / `!signal` file pair
   to one tagged file per extra adapter that appends in `init()`, so the two tags combine freely.
   The Telegram bridgev2 instance registers only when `adapters.telegram.bridgev2: true` (default
   `false`, env `CHATBRIDGE_ADAPTERS_TELEGRAM_BRIDGEV2`), which keeps `POST /accounts` without
   `adapter` working for existing deployments.
4. Build: Dockerfile builds `-tags goolm,signal,tgbridge`; `.golangci.yml` build tags; quality gate
   gains `go vet -tags goolm,tgbridge ./... && go build -tags goolm,tgbridge ./...` (runnable here,
   gcc only). Release workflow unchanged (pure Go, tag absent).
5. Docs: `adapter-protocol.md` §11 (second hosted connector, instance option), `api.md` (platform
   table row for `telegram/bridgev2`, config key), `chat-bridge.example.yaml`, README build tags,
   `AGENTS.md` gate, changelog.
6. Verification: host unit tests; tagged vet/build; Docker `--target test`; smoke run with the flag
   on — `GET /platforms` shows both instances, creating a `bridgev2` account and starting `qr`
   login returns a `tg://login?token=` QR. End-to-end messaging needs the user's Telegram account.

## Risks

- Binary and image grow by an estimated 30–40 MB (second `tg` schema, webp C code).
- The tagged build needs cgo; hosts building the pure-Go binary do not get the instance.
- Two databases per hosted account, as with Signal: `bridgev2.db` (id mapping, connector state)
  plus chat-bridge's store (content). Names of chats and contacts exist in both.
- Large Telegram files (up to 2 GB) are stored eagerly through the sink up to the per-platform
  limit; the core's `media.auto_download_max_mb` does not apply to hosted connectors today.
- mautrix-telegram's defaults assume a Matrix bridge (`sync.create_limit: 15` portals at login,
  member sync limits, takeout); unsynced chats get a portal when their first message arrives, and
  history backfill stays off as with Signal.
- bridgev2 and mautrix-telegram move monthly; pin versions and upgrade them together with mautrix-go.

## Scope

- New: `internal/adapters/tgbridge/tgbridge.go`, `cmd/chat-bridge/adapters_tgbridge.go`
- Touched: `internal/adapters/connector/host.go` (+ `host_test.go`),
  `cmd/chat-bridge/adapters_signal.go`, `cmd/chat-bridge/adapters_default.go`,
  `cmd/chat-bridge/main.go`, `internal/config` (one bool), `Dockerfile`, `.golangci.yml`,
  `AGENTS.md`, `README.md`, `chat-bridge.example.yaml`, `docs/adapter-protocol.md`, `docs/api.md`,
  `docs/changelog.md`
- Dependency: `go.mau.fi/mautrix-telegram v0.2608.0` (latest, verified with `go list -m -versions`)
- Roughly 150 lines of Go plus docs and build changes; one working session

## Alternatives

- Separate platform id `telegram-bridgev2`: no API behaviour change and no config flag, but the two
  implementations of one network show up as unrelated platforms and accounts cannot be moved
  between them. Rejected in favour of instances, which the core and UI already model.
- Always register the instance (no flag): simpler, but breaks `POST /accounts` without `adapter`
  for existing Telegram users of the Docker image.
- Replace the gotd adapter with the connector: loses the native adapter's capabilities that the
  host does not map (contact send, chat create, history, presence, self update, markdown). Out of
  scope; coexistence is what was asked.
- Out-of-process via `/adapter/v1`: keeps the main binary small but adds a second binary and a Go
  remote client. Not needed while cgo is accepted in the image.
- Make our schema compatible with bridgev2 (one storage for hosted accounts, Signal included):
  `bridgev2.Bridge.DB` is the concrete `*database.Database` with hard-coded SQL against its own
  tables (32 upgrade files in v0.30.0), so the bridge cannot write into our tables; the only form is
  bridgev2's tables inside `chatbridge.db` with our core reading `portal` / `ghost` / `message` for
  hosted accounts and keeping bodies in a side table. The core would then have two storage paths
  (WhatsApp, gotd Telegram and Matrix stay on ours), our migrations would depend on mautrix's
  monthly schema changes, and ingest would span two migration owners on one writer connection. The
  saving is id mapping and names. Going all the way (every platform a bridgev2 connector, bridgev2
  tables as the core schema) is adopting bridgev2 as the core, which was rejected in
  `20260913-0252-bridgev2-host-signal`. Rejected unless that decision is reopened.

## Annotations

- 2026-09-13 — user: upload limits may differ per platform → `WithMaxFileSize` added to step 1.
- 2026-09-13 — user: do not force 100 MB → no host-wide cap; connector capabilities decide.
- 2026-09-13 — user asked whether Signal can reuse bridgev2's schema, or our schema can be made
  compatible with it → see the schema-compatibility alternative.
- 2026-09-13 — user asked whether bridgev2's database can be reused; findings in Context. Open:
  whether "reuse" means sharing/merging storage (not recommended) or importing an existing gotd
  session through the `manual` flow (optional follow-up).
- 2026-09-14 — user: no gotd session import; storage stays one `bridgev2.db` per account.
- 2026-09-14 01:50 — completed. Verified: host and config unit tests (race), tagged vet and the
  real-connector test, pure-Go gate, `docker build --target test .` with `goolm,signal,tgbridge`,
  smoke run (both instances listed, unbound create 400, `bridgev2` QR login returns
  `tg://login?token=`); stripped binary 102.6 MB. The login flow id is `bot`, not `bot_token`.
  golangci-lint is not installed locally; 2.13.2 in a container reports pre-existing issues across
  the repo, none in `tgbridge` / `cmd`; the new `UploadMediaStream` defers follow the file's
  existing unchecked-`defer` style. End-to-end messaging needs a real Telegram login.

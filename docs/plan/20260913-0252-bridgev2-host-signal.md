# 20260913-0252-bridgev2-host-signal Host mautrix bridgev2 network connectors; spike with Signal

- **status**: completed
- **createdAt**: 2026-09-13 02:52
- **approvedAt**: 2026-09-13 03:02
- **relatedTask**: 20260913-0252-bridgev2-host-signal

## Context

Decisions recorded with the user: keep chat-bridge's architecture (platform-agnostic core + HTTP
API, adapters underneath), align with `bridgev2` rather than adopt it as the core, and **cgo is
acceptable** for the main binary — so Signal is hosted in-process, not as a sidecar.

Findings (mautrix-go v0.30.0, mautrix-signal v0.2608.0):

- `bridgev2.NetworkConnector` (Init/Start/GetName/GetCapabilities/GetConfig/LoadUserLogin/
  GetLoginFlows/CreateLogin) plus `NetworkAPI` (Connect/Disconnect/IsLoggedIn/LogoutRemote/
  IsThisUser/GetChatInfo/GetUserInfo/GetCapabilities/HandleMatrixMessage) and ~30 optional
  interfaces (edits, reactions, redactions, read receipts, typing, identifier resolving, contact
  listing, backfill, …) are the same shape as our `adapter.Adapter` + optional interfaces.
- `bridgev2.LoginProcess` steps: `user_input`, `display_and_wait` (qr / code / emoji / nothing),
  `cookies`, `client_http`, `webauthn`, `complete` — a superset of our `input` / `display` / `done`.
- A connector needs a `*bridgev2.Bridge`: `bridgev2.NewBridge(bridgeID, db *dbutil.Database, log,
  cfg *bridgeconfig.BridgeConfig, matrix MatrixConnector, network NetworkConnector, …)`; the bridge
  owns its own tables (`bridgev2/database`: users, user logins, portals, ghosts, messages, reactions,
  backfill queue) on a `dbutil.Database` — modernc works, as shown by the E2EE store.
- The Matrix side is two interfaces: `MatrixConnector` (20 methods: ghost MXID formatting, intents,
  bridge/message status, content URIs, members/power levels, deterministic IDs, `BatchSend`) and
  `MatrixAPI` (22 methods per intent: SendMessage/SendState/MarkRead/MarkTyping/Upload/Download
  media, profile, CreateRoom/EnsureJoined/EnsureInvited, GetEvent). mautrix's real implementation
  (`bridgev2/matrix`, appservice + homeserver) is ~5 000 lines; a virtual one that only feeds our
  core needs no appservice, no crypto, no provisioning: estimated 700–900 lines.
- Remote events arrive through `bridgev2.RemoteEvent` on portals and end up as `MatrixAPI.SendMessage`
  calls from ghost intents with `event.Content` (`m.room.message`, `m.reaction`, redactions, state
  for room name/avatar/members). Our matrix adapter already converts `event.MessageEventContent`
  to `model.Content` (`internal/adapters/matrix/events.go` `convertContent`); it moves to a shared
  package so both use it.
- `mautrix-signal`'s connector depends on `pkg/libsignalgo`, cgo bindings to Rust `libsignal`.
  The Rust sources are a git submodule, not part of the Go module; mautrix builds them in a
  `rust:1-alpine` stage (`build-rust.sh`) and links `libsignal_ffi.a`. With cgo accepted, the
  main binary links it directly; the image moves from distroless-static to alpine (musl, matching
  mautrix's own build), and the developer box needs a prebuilt `libsignal_ffi.a` (this container
  has gcc but no Rust; the library is produced once in a sibling container and cached under the
  project's `.tmp/`).

## Proposal

Phase A — generic host (pure Go, in-process capable):

1. `internal/adapters/matrixcontent`: move `convertContent` / media-content helpers out of the
   matrix adapter so the host reuses them (matrix adapter keeps its behaviour).
2. `internal/adapters/bridgev2`: `New(log, connector bridgev2.NetworkConnector, opts)` returns an
   `adapter.Adapter`. Per account: one bridgev2 `User` (`@<account>:chat-bridge`) and one
   `UserLogin`; database `accounts/<id>/bridgev2.db` (modernc via `dbutil.NewWithDB`).
   - `virtualMatrix` implements `MatrixConnector`: ghost MXIDs `@<network-user>:chat-bridge`,
     `GhostIntent`/`BotIntent`/`NewUserIntent` return `virtualIntent`s; `SendBridgeStatus` →
     `Sink.Status` (bridgev2 `status.BridgeState` → our statuses: `CONNECTED`→connected,
     `TRANSIENT_DISCONNECT`→disconnected, `BAD_CREDENTIALS`/`LOGGED_OUT`→unpaired,
     `UNKNOWN_ERROR`→error); deterministic room ids `!<portal-key>:chat-bridge` become chat ids;
     `BatchSend` (backfill) fans out to `Backfill: true` message events.
   - `virtualIntent` implements `MatrixAPI`: `SendMessage` converts `event.Content` → adapter
     events (message / reaction / message_delete / message_update via `m.new_content`),
     `SendState` (room name, avatar, member, topic) → chat / member / contact events, `MarkRead`
     → receipt, `MarkTyping` → typing, `UploadMedia` → `Sink.PutMedia` (content URI
     `mxc://chat-bridge/<media_id>`), `DownloadMedia` → `MediaSource` / blob store,
     `CreateRoom` → chat hint, profile setters → contact events. Everything else is a no-op with
     a debug log.
   - `adapter.Adapter` methods: `LoginStart` = `CreateLogin` + `Start`, `LoginSubmit` =
     `SubmitUserInput`, `LoginRefresh` = `Wait` for display_and_wait (run in a goroutine, pushed via
     `Sink.LoginStep`), `Logout` = `UserLogin.Logout`; `SendMessage` builds a `MatrixMessage` from
     `model.Content` (text/media/location) and calls `HandleMatrixMessage`; edit/delete/react/read/
     typing/resolve/contacts map to the optional `*HandlingNetworkAPI` interfaces when present;
     `FetchMedia` uses the portal message's media through `DirectMediableNetwork` or the stored
     `event.Content` (`remote_ref` = the original content JSON). Capabilities are derived from which
     optional interfaces the connector implements (+ `GetCapabilities`).
   - Verified with a fake `NetworkConnector` in tests (login round-trip, inbound message with
     media, reaction, receipt, send, capability derivation, status mapping).
3. `GET /platforms` shows the connector's `GetName()` as platform id (e.g. `signal`); config
   schema is generated from the connector's `GetConfig()` example (opaque YAML string field
   `network` for now).

Phase B — Signal in-process (cgo):

4. `internal/adapters/signal` (build tag `signal`): `New(log)` = the bridgev2 host wrapping
   `mautrix-signal/pkg/connector`; registered in `cmd/chat-bridge/main.go` behind the same tag so
   `go build -tags goolm` alone still produces a cgo-free binary for hosts without libsignal, and
   `-tags goolm,signal` produces the full one. `go.mau.fi/mautrix-signal` becomes a normal
   dependency of the module (only the tagged package imports it).
5. Build: the Dockerfile gains a `rust:1-alpine` stage that clones `mautrix-signal` at the pinned
   tag with its `libsignal` submodule and runs `build-rust.sh`; the Go stage becomes
   `CGO_ENABLED=1` with `build-base` and `LIBRARY_PATH` pointing at `libsignal_ffi.a`, building
   `-tags goolm,signal`; the runtime stage becomes `alpine` (ca-certificates, nonroot user)
   instead of distroless-static. A `scripts/build-libsignal.sh` runs the same rust stage in a
   sibling container and drops `libsignal_ffi.a` into `.tmp/libsignal/` for local
   `go build/test -tags goolm,signal`. The quality gate keeps `-tags goolm` (pure Go, always
   runnable) and adds a second line with `signal` that is skipped when the library is absent.
6. Docs: `adapter-protocol.md` §11 "Hosting a bridgev2 connector", `api.md` §7 adds a Signal
   column (E2EE always, phone handles, QR linking) and §4.1 its config keys, README (build tags,
   libsignal), `AGENTS.md` (gate), `docs/architecture.md`, changelog.

Out of scope now (was in the sidecar variant): a Go client for the remote protocol.

## Risks

- `bridgev2` is a moving target (its docs directory is literally `unorganized-docs`); minor
  version bumps of mautrix may change `MatrixAPI`. Mitigation: the virtual Matrix side is one
  package with compile-time interface checks; pin mautrix and upgrade deliberately.
- Some connector behaviour assumes a real homeserver (power levels, invites, `GetEvent` for
  replies). The virtual side answers from the bridgev2 database; gaps show up as no-ops with logs.
- Signal build is heavy (Rust compile of libsignal ~10–20 min, needs a sibling container with
  network) and the image grows (alpine + glibc-free musl binary linking a ~40 MB static
  library). Mitigation: phase A is fully testable without it; the `signal` build tag keeps every
  other build cgo-free; the rust stage is cached by Docker layer.
- Moving the runtime image off distroless-static widens the attack surface slightly (a shell
  exists in alpine). Accepted; the container still runs as a non-root user.
- Two message models in one process (bridgev2's portal/message tables plus ours) double-store
  messages for hosted networks. Accepted for the spike; retention for the bridgev2 DB is the
  connector's own.
- No real Signal account available in this environment: phase B is verified up to "sidecar
  connects, platform `signal` appears, QR login step returns a linking URL"; end-to-end messaging
  needs the user's phone.

## Scope

- New: `internal/adapters/matrixcontent`, `internal/adapters/bridgev2` (+ tests),
  `internal/adapters/signal` (build tag `signal`), `scripts/build-libsignal.sh`
- Touched: `internal/adapters/matrix/events.go` (use shared content conversion),
  `cmd/chat-bridge/main.go` (tagged registration), `Dockerfile`, `compose.yaml`, `.golangci.yml`,
  `README.md`, `AGENTS.md`, `docs/adapter-protocol.md`, `docs/api.md`, `docs/architecture.md`,
  `docs/changelog.md`
- Dependencies: `maunium.net/go/mautrix/bridgev2` (already in go.sum); `go.mau.fi/mautrix-signal`
  v0.2608.0 (latest at pkg.go.dev, verified) added to the module, imported only by the tagged
  package
- Roughly 1 500 lines of Go for phase A, 250 for phase B plus build scripts; three working sessions

## Alternatives

- Adopt `bridgev2` as the core and run a homeserver: rejected by the user; Matrix-centric.
- Write a native Signal adapter on `signalmeow` directly (skipping bridgev2): duplicates
  mautrix-signal's connector logic for no gain. Rejected.
- Signal as an out-of-process sidecar over `/adapter/v1` (the previous draft): keeps the core
  image cgo-free but adds a second binary, a Go remote client, and a second deployment unit.
  Superseded by the user's decision to accept cgo.
- Spike the host with a pure-Go connector first (Slack / Bluesky) and Signal second: same phase A
  work; Signal is what the user asked for, so phase B targets Signal and the pure-Go networks come
  for free afterwards.

## Annotations

- 2026-09-13 03:45 — completed. Package named `internal/adapters/connector` (not `bridgev2`, to
  avoid shadowing the mautrix package). Capabilities are derived from an optional probe client
  (`connector.WithProbe`) plus connected logins, since bridgev2 declares them per login. Verified:
  fake-connector round-trip and event-conversion tests (race-clean), pure-Go gate, Docker build
  (`--target test` with `-tags goolm,signal` and the runtime image), and a smoke run of the image:
  `GET /platforms` lists `signal`, `POST /accounts` + `qr` login returns a `sgnl://linkdevice`
  URI from Signal's provisioning socket. `scripts/build-libsignal.sh` is syntax-checked only: this
  container lacks zlib development files, so the local tagged build cannot link here. End-to-end
  Signal messaging still needs a real phone to scan the code.

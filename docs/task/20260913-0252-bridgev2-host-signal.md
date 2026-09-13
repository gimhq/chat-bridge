# 20260913-0252-bridgev2-host-signal Host mautrix bridgev2 network connectors; spike with Signal

- **status**: completed
- **priority**: P2
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-13 02:52

## Description

Align chat-bridge with mautrix-go's `bridgev2` so its network connectors (Signal, Meta, Slack,
Discord, …) can run as chat-bridge adapters without changing our architecture: a generic
`bridgev2` host implements bridgev2's Matrix side (`MatrixConnector` / `MatrixAPI`) as a virtual
homeserver that maps portals → chats, ghosts → contacts, Matrix events → adapter events, and
drives `NetworkAPI` for outbound traffic. Spike it with `mautrix-signal`'s connector.

Acceptance:

- `internal/adapters/connector` hosts any `bridgev2.NetworkConnector` behind `adapter.Adapter`;
  login flows, inbound messages/reactions/receipts/typing, chat and contact info, and sends round-trip
  through the virtual Matrix side, verified with a fake connector in unit tests.
- Login steps map onto our step machine: `user_input` → `input`, `display_and_wait` (qr / code /
  emoji) → `display`, `complete` → `done`; `cookies`, `client_http`, `webauthn` are reported as
  `unsupported` steps rather than dropped.
- Signal runs in-process (cgo + libsignal, build tag `signal`): the Dockerfile builds libsignal in
  a Rust stage and links it into the main binary; builds without the tag stay pure Go. (Revised
  from the sidecar variant after the user accepted cgo.)
- Documented in `docs/adapter-protocol.md` (how to host a bridgev2 connector) and `docs/api.md` §7.

## ActiveForm

Hosting bridgev2 network connectors; spiking Signal

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Plan: `docs/plan/20260913-0252-bridgev2-host-signal.md`.

- complete: bridgev2 host + Signal (in-process, cgo) delivered; Docker build and QR login smoke verified

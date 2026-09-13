# Changelog

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

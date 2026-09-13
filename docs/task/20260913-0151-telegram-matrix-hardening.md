# 20260913-0151-telegram-matrix-hardening Harden the Telegram and Matrix adapters

- **status**: completed
- **priority**: P1
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-13 01:51

## Description

Bring the Telegram and Matrix adapters from "first cut" to daily-use quality:

- Telegram: application credentials (`api_id` / `api_hash`) configurable once at the adapter level instead of per account; updates state and peer access hashes persisted so restarts recover missed updates and can address peers without reloading dialogs; message formatting (entities <-> markdown subset).
- Matrix: end-to-end encryption (decrypt `m.room.encrypted`, encrypt sends in encrypted rooms, encrypted attachments) in the pure-Go build; direct-chat names from member display names.

Acceptance:

- `CHATBRIDGE_ADAPTERS_TELEGRAM_API_ID/API_HASH` (or the YAML equivalent) is enough to create a Telegram account with an empty `config`; per-account values still override.
- A Telegram account restarted after being offline receives the updates it missed (gaps manager state survives restarts); `chat.resolve` / send to a previously seen user works right after restart without `loadDialogs`.
- Telegram inbound bold/italic/code/links arrive as `format: markdown`; outbound `format: markdown` renders on Telegram.
- Matrix: a message received in an encrypted room is stored as text/media, not `unsupported`; a message sent to an encrypted room is encrypted; an encrypted image can be fetched; the build stays `CGO_ENABLED=0` (`-tags goolm`).
- Matrix direct chats get a name without an explicit `GET /chats/{id}`.
- Unit tests for the new conversions and storages; existing suites stay green.

## ActiveForm

Hardening the Telegram and Matrix adapters

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Plan: `docs/plan/20260913-0151-telegram-matrix-hardening.md`.

- complete: Focused and full suites passed with -tags goolm; smoke-tested adapter-level Telegram credentials and keys endpoints.

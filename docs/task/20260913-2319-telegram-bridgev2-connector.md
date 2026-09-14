# 20260913-2319-telegram-bridgev2-connector Host mautrix-telegram's bridgev2 connector alongside the gotd adapter

- **status**: completed
- **priority**: P2
- **owner**: worker-a/session-chatbridge-02
- **createdAt**: 2026-09-13 23:19

## Description

Investigate running `go.mau.fi/mautrix-telegram/pkg/connector` through the bridgev2 host
(`internal/adapters/connector`), the same way Signal is hosted, as a second Telegram adapter that
coexists with the gotd-based `internal/adapters/telegram`.

Acceptance (implementation phase, after approval):

- `GET /platforms` lists `telegram` with two instances: `local` (gotd) and `bridgev2`.
- Existing Telegram accounts keep working on `local`; `POST /accounts` without `adapter` keeps
  working unless the operator enables the second instance.
- An account bound to `bridgev2` completes phone and QR login and exchanges messages.

## ActiveForm

Investigating a bridgev2-hosted Telegram adapter

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

- Plan: `docs/plan/20260913-2319-telegram-bridgev2-connector.md`

- complete: implemented and verified; see plan annotations

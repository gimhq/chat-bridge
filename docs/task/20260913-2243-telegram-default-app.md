# 20260913-2243-telegram-default-app Built-in Telegram application credentials

- **status**: closed
- **priority**: P2
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-13 22:43

## Description

The user first asked for fixed Telegram `api_id` / `api_hash` values in the code, then withdrew it:
the credentials belong in the server-wide configuration (`adapters.telegram.*`, usually
`CHATBRIDGE_ADAPTERS_TELEGRAM_API_ID|HASH`), which every Telegram account already inherits, so
nothing is typed per account and nothing is committed.

Outcome:

- No built-in credentials; the config defaults are unchanged.
- `api.md` §2.2 and the platform table, `chat-bridge.example.yaml` and README state that the
  credentials are set once for the server through the environment or the config file.
- The dev instance reads them from a gitignored environment file under `.tmp/dev/`.

## ActiveForm

Documenting server-wide Telegram credentials

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

- close: built-in defaults reverted before commit at the user's request; docs clarified instead

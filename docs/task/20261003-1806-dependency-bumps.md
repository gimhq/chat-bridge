# 20261003-1806-dependency-bumps Bump direct Go dependencies to their latest releases

- **status**: pending
- **priority**: P2
- **owner**: (unassigned)
- **createdAt**: 2026-10-03 18:06

## Description

Phase 4 of plan `20261003-1652-pma-go-alignment`, split out because it was not approved with the
rest. `go list -u -m all` on 2026-10-03 reports newer versions for nine direct modules:
`maunium.net/go/mautrix` 0.30.0 → 0.31.0, `go.mau.fi/mautrix-signal` and
`go.mau.fi/mautrix-telegram` 0.2608.0 → 0.2609.0, `go.mau.fi/whatsmeow` (2026-09-29),
`github.com/gotd/td` 0.161.0 → 0.162.0, `go.mau.fi/util` → 0.10.1, `modernc.org/sqlite`
1.58.0 → 1.60.1, `github.com/knadh/koanf/v2` → 2.3.7, `github.com/go-playground/validator/v10`
→ 10.30.5.

Acceptance:

- Each bump lands in its own `chore(deps)` change after reading the release notes.
- All gates pass, including `docker build --target test .` (the `signal` and `tgbridge` tags and
  `MAUTRIX_SIGNAL_VERSION` in the Dockerfile move together with mautrix-signal).
- A login and a message round trip are checked against a real account for WhatsApp, Telegram and
  Matrix, because unit tests do not cover the live protocols.

## ActiveForm

Bumping Go dependencies

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

(none)

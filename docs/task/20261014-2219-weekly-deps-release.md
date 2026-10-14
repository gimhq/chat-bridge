# 20261014-2219-weekly-deps-release Weekly upstream dependency update and patch release

- **status**: in_progress
- **priority**: P2
- **owner**: gimhq-a3/a4cf95
- **createdAt**: 2026-10-14 22:19

## Description

The platform libraries (whatsmeow, mautrix-signal, mautrix-telegram, mautrix-go, gotd) move every
week, and WhatsApp in particular rejects clients that fall behind. Following them by hand
(task `20261003-1806-dependency-bumps`) left them a month behind.

Acceptance:

- A scheduled workflow (weekly, plus manual dispatch) moves every direct Go dependency to its
  latest version, keeps `MAUTRIX_SIGNAL_VERSION` in step with `go.mau.fi/mautrix-signal`, and
  runs the CI gates on the result.
- When nothing changed it stops. When the gates pass it fast-forwards `main`, tags the next patch
  version and runs the release workflow. When they fail, the bump stays on `deps/weekly` for a
  person to fix and the run is red.
- README documents it.

## ActiveForm

Adding the weekly dependency workflow

## Dependencies

- **blocked by**: 20261003-1806-dependency-bumps
- **blocks**: (none)

## Notes

Full tier (CI config, dependencies, release). Plan: `docs/plan/20261014-2219-weekly-deps-release.md`.

# 20261003-1652-pma-go-alignment Align with the pma-go baseline: lint to green, comments, API spec sync

- **status**: completed
- **priority**: P1
- **owner**: worker-a/session-ci-01
- **createdAt**: 2026-10-03 16:52

## Description

An audit against `/pma` and `/pma-go` found that the documented quality gate does not pass
(`golangci-lint run` reports 139 issues, about 90 of them missing doc comments on exported
methods), that code comments point at a spec file that no longer exists, that `docs/api.md` and
`docs/storage.md` describe behavior the implementation does not have, and that several baseline
items (Go version, lint config, task runner, tidy gate, decision records) are missing.

Acceptance:

- `golangci-lint run` reports no issues with the repository config; every exported symbol has a
  doc comment; no comment references a missing document.
- `docs/api.md` and `docs/storage.md` describe what the server does, with every remaining gap
  either implemented or removed from the spec.
- The pma-go baseline items in the plan are in place or recorded in `docs/decisions/`.
- All gates in `AGENTS.md` pass before and after.

## ActiveForm

Aligning the repository with the pma-go baseline

## Dependencies

- **blocked by**: (none)
- **blocks**: 20261003-1646-ci-workflow (CI would be red until lint passes)

## Notes

Full tier. Plan: `docs/plan/20261003-1652-pma-go-alignment.md`.

- complete: Phases 1-3 done, golangci-lint 0 issues, all Go gates pass; phase 4 moved to 20261003-1806-dependency-bumps.

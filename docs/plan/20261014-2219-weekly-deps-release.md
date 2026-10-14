# 20261014-2219-weekly-deps-release Weekly upstream dependency update and patch release

- **status**: implementing
- **createdAt**: 2026-10-14 22:19
- **approvedAt**: 2026-10-14 22:19
- **relatedTask**: 20261014-2219-weekly-deps-release

## Context

- `ci.yml` runs the web gate, the Go gates with the `tgbridge` line, and the Docker `test` stage,
  the only check of the `signal` tag, on push to `main` and on pull requests.
- `release.yml` builds and publishes on a `v*` tag push or a manual run; it releases when
  `github.ref_type` is `tag`, so dispatching it on a tag ref releases too.
- A push or tag made with `GITHUB_TOKEN` starts no workflow; `workflow_dispatch` is the
  exception. `main` is not protected.
- The Dockerfile builds libsignal from `MAUTRIX_SIGNAL_VERSION`, which must match the Go module.

## Proposal

New `.github/workflows/deps.yml`, Mondays 04:00 UTC and `workflow_dispatch`:

1. `bump`: `go get` every direct module at `@latest`, `go mod tidy`, set
   `MAUTRIX_SIGNAL_VERSION` in the Dockerfile and `scripts/build-libsignal.sh` to the resolved
   mautrix-signal version. No diff: stop. Otherwise commit with the list of version changes and
   force-push to `deps/weekly`.
2. `ci`: call `ci.yml` on that commit. `ci.yml` gains `workflow_call` with a `ref` input used by
   checkout and its concurrency group, so a push to `main` cannot cancel it.
3. `release`: push the commit to `main` (fast-forward only; a moved `main` fails the run), tag the
   latest stable `v*` tag plus one patch, push the tag, delete `deps/weekly`, and dispatch
   `release.yml` on the tag.

## Risks

- A green gate is not a live-protocol check; a release can still break a login. The weekly
  cadence and patch versions keep a bad release cheap to roll forward or back.
- An upstream break leaves the run red until someone fixes `deps/weekly`; nothing is released.
- The web UI dependencies are not part of this; they do not talk to the platforms.

## Scope

`.github/workflows/deps.yml` (new), `.github/workflows/ci.yml` (`workflow_call`), README.

## Alternatives

- Dependabot or Renovate: one PR per module and no release; needs a person every week.
- Open a PR instead of pushing to `main`: PRs opened with `GITHUB_TOKEN` get no CI run, and the
  goal is an unattended release.

## Annotations

(none)

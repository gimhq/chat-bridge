# 20260913-2234-release-workflow Publish on GitHub; release binaries from a workflow

- **status**: completed
- **priority**: P2
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-13 22:34

## Description

The user asked to publish the repository at `github.com/gimhq/chat-bridge` and to add a GitHub
Actions workflow that releases binaries.

Acceptance:

- `.github/workflows/release.yml`: a `v*` tag runs the web UI gate and the Go gate (gofmt, vet,
  tests), builds pure-Go binaries (`-tags goolm`, `CGO_ENABLED=0`) with the embedded UI for
  linux, darwin and windows on amd64 and arm64, and publishes them as a GitHub release with
  `checksums.txt`; tags containing `-` become prereleases. A manual run builds the same archives
  as workflow artifacts without releasing.
- Archives carry the binary, `LICENSE`, `README.md` and `chat-bridge.example.yaml`
  (`.tar.gz`, `.zip` for windows); the version comes from the tag through `main.version`.
- Nothing internal is published: git history scanned for credentials and internal host names.
- README documents releases.

## ActiveForm

Adding the release workflow and publishing on GitHub

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Standard tier; the user's message is the go-ahead. Release binaries leave out Signal: it needs cgo
and libsignal, which do not cross-compile from one runner; Signal stays in the container image.
All six targets were cross-compiled locally with `CGO_ENABLED=0`.

- complete: workflow added and checked with actionlint; six targets cross-compiled locally

# 20261003-1744-token-read-only-and-resolve Scoped tokens: read-only switch; start a chat with an allowed contact

- **status**: completed
- **priority**: P1
- **owner**: worker-a/session-ci-01
- **createdAt**: 2026-10-03 17:44

## Description

Follow-up to `20261003-1654-scoped-tokens`, from the user's answers to its open questions:

1. Groups stay invisible unless the group is listed; filtering is by chat. Already the behavior.
2. A token must be restrictable to reading: no sending, editing, deleting, reacting, typing, read
   receipts, uploads, or starting chats.
3. A web UI for tokens is wanted, later: task `20261003-1745-tokens-web-ui`.
4. A scoped token may start a conversation with a contact in its scope.

Acceptance:

- `scope.read_only` (default `false`) makes every route that acts on the platform answer
  `403 forbidden`; reads are unchanged.
- `POST /accounts/{a}/chats/resolve` works for a scoped token when the handle names an allowed
  contact; the resolved direct chat becomes reachable for that token. Any other handle answers
  `404` without a platform lookup.
- Server tests cover both, and the route allowlist test knows the new route.
- `docs/api.md` §4.11 describes both.

## ActiveForm

Adding the read-only switch and scoped chat resolution

## Dependencies

- **blocked by**: (none)
- **blocks**: 20261003-1745-tokens-web-ui

## Notes

Full tier; approval was given with the request. Plan: `docs/plan/20261003-1744-token-read-only-and-resolve.md`.

- complete: Server, core and store suites pass (server 20 runs); lint clean.

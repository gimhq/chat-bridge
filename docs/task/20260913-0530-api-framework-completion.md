# 20260913-0530-api-framework-completion Complete the bridge API framework against api.md

- **status**: in_progress
- **priority**: P1
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-13 05:30

## Description

`docs/api.md` and `docs/storage.md` describe a surface the implementation only partly delivers.
Close the gap so the consumer API is complete and platform-agnostic before any consumer (Matrix
appservice bridge, assistant) is built on it. Gaps found in the audit (spec section → state):

- §4.3 `POST /accounts/{a}/chats` (`chat.create`), `PATCH … {name}` reaching the platform — missing
- §4.4 `GET /accounts/{a}/messages/search`, `backfill=1` history from the platform (`message.history`) — missing
- §4.1 `PATCH /accounts/{a}/self` (`self.update`), §4.6 `PATCH contacts {blocked}` — stubs only
- §3.8 / §4.7 Persons (local identity overlay, auto-link by phone, merge, suggest, cross-account views, `person_id`, `person=` filters, `person.updated`) — missing
- §4.8 Requests (contact requests, chat invites, join requests, calls; `request.*` events; `request.answer`) — missing
- storage.md §7 retention/GC (`retention.*` keys, `media.max_gb`, raw payloads, receipts, short-lived event types, media purge by age/size, ephemeral expiry → `expired`) — only upload expiry and event pruning exist

Acceptance:

- Every endpoint in `api.md` §4 exists and behaves as documented, or the spec is amended where the
  audit shows the design was wrong; `GET /platforms` advertises the new capabilities only where an
  adapter implements them.
- New adapter interfaces (`ChatCreator`, `ChatUpdater`, `Backfiller`, `SelfUpdater`, `Blocker`,
  `RequestAnswerer`) exist in Go and on the JSON-RPC wire, with at least one built-in adapter
  implementing each; remote adapters gain the matching RPC methods.
- Persons and Requests are persisted (schema v3), emit their events, and are covered by store,
  core and server tests.
- Retention runs every step of storage.md §7 with config keys, covered by tests with a fake clock.
- Docs synced: `api.md`, `adapter-protocol.md`, `storage.md`, README, changelog.

## ActiveForm

Completing the bridge API framework

## Dependencies

- **blocked by**: (none)
- **blocks**: future Matrix appservice bridge (consumer of this API)

## Notes

Plan: `docs/plan/20260913-0530-api-framework-completion.md`.

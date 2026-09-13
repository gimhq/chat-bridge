# 20260913-2034-ignore-call-requests Ignore calls instead of rejecting them

- **status**: completed
- **priority**: P1
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-13 20:34

## Description

Rejecting an incoming call from the bridge ends the ring on every device of the account
(WhatsApp `RejectCall`, Telegram `phone.discardCall`, Matrix `m.call.reject`), so the owner
cannot pick the call up on the phone. The user asked for calls to be ignored instead.

Acceptance:

- New request action `ignore` and state `ignored`: the request stops being pending in the bridge
  and the platform is not contacted, so other devices keep ringing.
- Calls advertise `actions: ["ignore"]`; other pending requests `["accept", "reject", "ignore"]`.
  `POST …/reject` still works for calls but is documented as hanging up everywhere.
- `POST /accounts/{a}/requests/{id}/ignore`; no adapter call, no connected account needed.
- Web UI: the call toast and the request list offer 忽略 for calls; no reject button for calls.
- Tests (store, core, server, web, browser smoke) and `api.md` §4.8 updated; changelog.

## ActiveForm

Ignoring calls instead of rejecting them

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Follow-up to `20260913-1903-web-ui-and-requests`. Standard tier; the user's message is the go-ahead.

- complete: calls are ignored locally; Go, web gates and browser smoke 14/14 verified

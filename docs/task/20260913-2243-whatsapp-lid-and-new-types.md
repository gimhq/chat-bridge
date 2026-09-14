# 20260913-2243-whatsapp-lid-and-new-types WhatsApp: resolve LIDs to phone numbers; map newer message types

- **status**: completed
- **priority**: P1
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-13 22:43

## Description

A live WhatsApp account in the dev instance shows chats and senders as `<n>@lid` that are not
tied to the saved contact, and several messages render as unsupported.

Findings (dev account `aaa`, 31 078 messages):

- 62 of 108 chats and 29 959 message senders are LIDs; every one of them has a phone mapping in
  whatsmeow's `whatsmeow_lid_map` (e.g. `235978975346820@lid` → `66995618240`, saved as 向日葵).
  The adapter only swaps to the phone JID when the event itself carries `SenderAlt` /
  `RecipientAlt` / `PnJID`; history-sync messages and many live events do not, and the LID store is
  never consulted. `syncContacts` also drops LID-only contact rows.
- Unsupported rows: `associatedChildMessage` 263 (HD copy of an image already sent,
  `HD_IMAGE_DUAL_UPLOAD`), `albumMessage` 141 (album header, the images arrive separately),
  `templateMessage` 4 and `interactiveMessage` 2 (business messages with text),
  `messageHistoryNotice` 3, `messageHistoryBundle` 1, and `placeholderMessage` 1 of type
  `MASK_LINKED_DEVICES` — the phone withheld that message from linked devices, which WhatsApp
  presents as "may be on an old version".

Acceptance (proposed, awaiting approval):

- Every JID the adapter emits (chat ids, senders, receipts, typing, members, system actors,
  mentions, call creators, push names, contacts) resolves `@lid` to the phone JID through
  `Store.LIDs` when a mapping exists; outgoing calls accept either form.
- Existing LID rows in a store are merged into the phone form on start (chats, members, messages,
  receipts, reactions, contacts) or the account is re-synced; decided in the proposal.
- `associatedChildMessage` is unwrapped (HD image replaces nothing, so it is dropped when it only
  duplicates its parent); `albumMessage` produces no timeline row; `templateMessage` /
  `interactiveMessage` become text; history notices become system rows;
  `MASK_LINKED_DEVICES` becomes a readable notice and is re-requested from the phone.
- Tests for the conversions and the LID resolution.

## ActiveForm

Resolving WhatsApp LIDs and mapping newer message types

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Investigation done against `.tmp/dev/data` (bridge DB and `accounts/aaa/whatsmeow.db`).
- complete: LID is the user id (mautrix-whatsapp model), identity re-ID in core and store, newer message types mapped; Go and web gates plus the dev account verified

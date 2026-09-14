# 20260913-2243-whatsapp-lid-and-new-types WhatsApp: one identity per user (LID); newer message types

- **status**: completed
- **createdAt**: 2026-09-13 23:10
- **approvedAt**: 2026-09-13 23:10
- **relatedTask**: 20260913-2243-whatsapp-lid-and-new-types

## Context

The user asked to fix the WhatsApp logic and to follow mautrix-whatsapp, which does not show the
problem. Findings in the task file; mautrix-whatsapp (`pkg/connector/{events,id,lidmigrate,
userinfo,handlewhatsapp,backfill}.go`, `pkg/msgconv/wa-*.go`) was read for reference.

How mautrix-whatsapp handles it:

- LID is the canonical user and DM identity (`pickLID`, `maybeConvertJIDToLID`): a phone JID is
  replaced by its LID whenever whatsmeow's LID map knows one (`Store.LIDs.GetLIDForPN`), using
  `SenderAlt` / `RecipientAlt` first. The phone number stays an attribute.
- Existing phone-keyed DM portals are re-IDed to the LID at start (`migrateToLIDDMs`), and again
  on the fly when an event shows a mapping that was not known before (`reIDPhoneDMToLIDIfNeeded`).
- Contact info is keyed by both forms in whatsmeow; the ghost of one form is synced with the other
  (`syncAltGhostWithInfo`).
- HD dual uploads become an edit of the parent (live) or unwrap (backfill); motion photos are
  ignored; albums, templates, interactive messages, history notices and linked-device placeholders
  have their own converters.

Dev data (`.tmp/dev`, account `aaa`): 62 LID DMs, 21 phone DMs (20 with a LID mapping, none of
them duplicated yet), own identity split between `66927707777@s.whatsapp.net` (DM sends) and
`67014777372709@lid` (groups); all 17 HD and 246 motion-photo children have their parent stored.

## Proposal

**Adapter contract**

1. Event kind `identity` (`Event.UserID` = old id, `Event.NewID` = canonical id): the platform
   now addresses this user or direct chat by another id. Wire kind `identity` with `user_id` and
   `new_id`.
2. Optional interface `IdentityResolver{ CanonicalIDs(ctx, account, ids []string)
   (map[string]string, error) }` returning only ids that changed; wire method `identity.resolve`.

**Core**

3. `store.ReID(account, old, new)` in one transaction, no-op when nothing references `old`:
   contacts (merge into an existing row with `mergeContact`, local alias kept, person link moved
   unless the new row is already linked, unlink pairs rewritten), direct chat (insert or merge the
   chat row, move members and messages, dropping messages whose id already exists in the target,
   unread summed, last message kept, old row deleted), `messages.sender_id`, quoted ids inside
   `mentions` and system `content`, `chat_members.user_id`, `reactions.sender_id`,
   `receipts.user_id`, `requests.from_id|chat_id`, `accounts.self_id`. Returns whether anything
   changed.
4. Sink: `identity` events call `ReID`; when it changed rows the core emits `contact.updated` for
   the new contact and `chat.updated` for the target chat (with `merged_from` on the payload
   envelope data) and runs auto-link. On `connected` the core collects the account's distinct
   contact, direct-chat and sender ids and, when the adapter implements `IdentityResolver`, re-IDs
   every id it maps (before the contact sync).

**WhatsApp adapter**

5. `acc.userJID(ctx, jid, alt)` → LID when known (alt first, then `GetLIDForPN`), own phone →
   own LID; `acc.chatJID(ctx, source)` for DMs as in mautrix. Every emitted id goes through them:
   messages (chat, sender, mentions), reactions, receipts, typing, presence, members, group system
   actors and targets, calls, invites, push names, contacts, undecryptable, history sync
   conversations, `ResolveChat`, `GetChat`, `SendMessage` result.
6. When a phone JID resolves to a LID for the first time in a session, the adapter emits
   `identity{old: phone, new: lid}` ahead of the converted events.
7. Contacts: one contact per canonical id; `phone` from the phone form; names merged from the
   whatsmeow contact rows of both forms (address-book names live under the phone key, push names
   often under the LID). `ListContacts` / `syncContacts` include LID-only rows.
8. `CanonicalIDs` maps `@s.whatsapp.net` ids through `GetManyLIDsForPNs`.
9. Message types: `associatedChildMessage` with `HD_IMAGE_DUAL_UPLOAD` / `HD_VIDEO_DUAL_UPLOAD`
   is stored under the parent id (dropped as a duplicate when the parent exists), `MOTION_PHOTO`
   children are ignored; `albumMessage` and `messageHistoryBundle` produce nothing;
   `templateMessage` / `highlyStructuredMessage` / `interactiveMessage` become text (title, body,
   buttons, footer); `messageHistoryNotice` becomes system `history_shared` (actor, targets,
   value = message count); `placeholderMessage` `MASK_LINKED_DEVICES` becomes system
   `primary_device_only`.

**Docs and UI**

10. `api.md` system kinds, `adapter-protocol.md` event kind and method, UI labels for the two
    system kinds, changelog.

## Risks

- Re-ID rewrites ids clients may have cached; they receive `chat.updated` / `contact.updated`
  but an old chat id stops resolving (404).
- The JSON rewrite of `mentions` / system content matches the quoted id string only.
- Verified with unit tests and the dev account; no second live account to watch a mapping appear
  mid-session.

## Scope

- `internal/adapter/{adapter.go,fake}`, `internal/adapters/remote/{wire,adapter}.go`
- `internal/store/reid.go` (new), `internal/core/{sink.go,identity.go}`
- `internal/adapters/whatsapp/{identity.go (new),convert.go,events.go,send.go,adapter.go,manage.go,requests.go}` and tests
- `web/src/shared/lib/format.ts`, docs listed above

## Alternatives

- Phone number as canonical id (the earlier proposal): matches the address book, but WhatsApp is
  moving to LIDs, some users have no visible phone, and mautrix-whatsapp went the other way.
- Wiping and re-pairing instead of re-ID: loses history beyond what history sync resends and does
  not handle mappings learned later.

## Annotations

- 2026-09-13 23:40 — implemented as proposed. Findings and deviations:
  - The first dev run re-IDed 89 ids at about 150 ms each inside one transaction (several scans of
    `messages` per id). Added schema v6 (sender and member indexes), an indexed "is this id stored
    at all" check before any rewrite, and one combined scan for mentions and notices. An id that
    only appears in mentions or notices is therefore not rewritten.
  - `GetPlaceholderMessage().GetType()` is `MASK_LINKED_DEVICES` (0) on messages without a
    placeholder; caught by the adapter test and guarded with a nil check.
  - The wire also rejects `identity` events without `user_id` / `new_id` (`-32602`).
  - The UI shows `chat.name || chat.id`, and WhatsApp DMs rarely carry a name, so the reported
    chat still showed the LID after the merge. Reads now fill an unnamed direct chat's name from
    the counterpart's contact (`store.NameDirectChats`: chat list, chat, person chats, chat
    events) without storing it.
  - Five DMs still showed a LID: whatsmeow has no address-book or push name for them, and their
    contacts were stored without a phone by the old conversion. `CanonicalIDs` now also re-emits
    stored LID users with the phone number from the LID map, so they read as their number.
  - Verified: Go gate (gofmt, vet, all tests, build), web lint / typecheck / tests; store, core,
    remote and WhatsApp adapter tests for the new paths (whatsmeow device store in a temp SQLite);
    the dev account after restart (see changelog). Not verified: a LID mapping learned mid-session
    on a live account, and Signal / Telegram / Matrix are untouched.

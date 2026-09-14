# 20260914-0206-contact-detail Contact detail page: everything about one contact

- **status**: completed
- **createdAt**: 2026-09-14 02:06
- **approvedAt**: 2026-09-14 02:15
- **relatedTask**: 20260914-0206-contact-detail

## Context

Findings:

- UI: contacts are a table with a "…" menu (message, alias, block). No row opens anything. The
  chat header links to `/persons/{p}` only when the chat has a `person_id`; message sender names
  are plain text; group participants are only counted. `LinkContactDialog` is not used anywhere.
- Persons already have a detail page (links, direct-chat channels, notes, merged timeline with
  `direct | all` scope), built on `GET /persons/{p}`, `/persons/{p}/chats`, `/persons/{p}/messages`.
- Dev data: one WhatsApp account, 127 contacts, 0 persons — nothing is reachable today.
- API for one contact: only `GET|PATCH /accounts/{a}/contacts/{user}`. There is no way to list the
  chats shared with a contact or their messages across chats; `GET /accounts/{a}/requests` has no
  sender filter. Store: `messages(account_id, sender_id)` and `chat_members(account_id, user_id)`
  are indexed since schema v6; `personDirectChats` already encodes "direct chat with this user"
  (chat id is the user, or the user is a member of a direct room for Matrix).

## Proposal

**API (additive)**

1. `GET /accounts/{a}/contacts/{user}/chats` → `{chats: [Chat]}`: the direct chat(s) with the user
   and the groups where they are a current member, most recent first, names filled like the chat
   list.
2. `GET /accounts/{a}/contacts/{user}/messages?scope=direct|all&cursor&limit` →
   `{messages, next_cursor}`: `direct` is the whole conversation in the direct chat(s); `all` adds
   their own messages in groups. Same cursor format as person messages.
3. `GET /accounts/{a}/requests?from={user}`: requests whose sender is the user.
4. Store `ContactChats`, `ContactMessages` share the direct-chat condition with the person queries;
   404 for an unknown contact. Tests in store, core and server; `api.md` §4.7 / §4.8.

**UI**

5. Route `/accounts/$accountId/contacts/$userId` (the contacts route becomes a folder: `index` list
   plus the detail). Page layout like the person page:
   - Header: display name, badges (self, address book, blocked), phone / handle; actions: message,
     set alias, block / unblock, link to a person (existing `LinkContactDialog`) or view person.
   - Profile card: alias, profile, first / last, username, phone, email, bio, id, updated.
   - Cross-platform card: the person and its other linked identities (each opens that contact), or
     an empty state with "link to a person".
   - Chats card: direct chat and shared groups with last activity, each opening the chat.
   - Timeline card: tabs 私聊 / 含群聊 over contact messages, message rendering reused, each
     message links to its chat.
   - Requests card (only when there are any): calls and invites from them with state.
6. Entry points: contacts table rows (name opens the detail), direct chat header "查看联系人"
   (alongside "查看此人" when linked), sender names in group messages, linked identities on the
   person page.
7. Queries and invalidation: `contact`, `contactChats`, `contactMessages` keys, refreshed by
   `contact.updated`, `message.new`, `chat.updated`, `request.*` for that account.

**Verification**

8. Go gate; web gate; Vitest for the detail page pieces (profile, chats card, timeline scope);
   browser smoke against the recovered dev data (open a contact from the table, from a chat
   header, from a group sender; link to a new person and see the cross-platform card).

## Risks

- Direct chat detection for Matrix relies on member rows (same rule as persons).
- A contact with thousands of group messages pages 50 at a time; no per-chat grouping.
- Sender names in history stored before contacts existed resolve at read time; links use the
  stored sender id, which is the canonical id after the WhatsApp re-ID.

## Scope

- `internal/store/{persons.go,contacts.go or contact_views.go}`, `internal/core/chats.go`,
  `internal/server/{server.go,handlers.go}`, tests, `docs/api.md`
- `web/src/app/routes/accounts/$accountId/contacts/{index,$userId}.tsx`,
  `web/src/features/contacts/{contacts-page,contact-detail}.tsx`,
  `web/src/features/chats/{chat-view,message-item}.tsx`, `web/src/features/persons/links-card.tsx`,
  `web/src/shared/api/{queries,types}.ts`, tests

## Alternatives

- Frontend only: show the profile and the direct chat from existing endpoints. No shared groups,
  no messages across chats, no requests — not "everything related".
- Create a Person implicitly when opening a contact and reuse the person page: writes data on
  every click and pollutes the persons list.
- A side sheet over the contacts table instead of a route: no deep link and too narrow for a
  timeline.

## Annotations

- 2026-09-14 02:45 — implemented as proposed. Notes:
  - Store: `ContactChats` / `ContactMessages` in `contact_views.go`; the person queries now share
    `chatsWithLast` and `pageMessages` with them. `RequestFilter.From`.
  - UI query keys nest under the keys events already refresh (`contacts`, `chats`, `requests`);
    only the contact timeline needed a new prefix, added to `message.*` invalidations.
  - The alias dialog and "open chat" moved out of the contacts page (`alias-dialog.tsx`,
    `use-open-chat.ts`) to be shared with the detail page. `renderWithClient` now provides a bare
    router context so components with `Link` render in unit tests.
  - The dev data was deleted at the user's request, so the browser smoke ran against a fresh
    binary with a scripted remote adapter (contacts, a DM, a group, a call) instead.
  - Verified: Go gate (gofmt, vet, all tests, build); web lint, typecheck, 41 tests, build;
    Playwright smoke 7/7 (contacts table → detail; profile, shared chats, requests; timeline scope;
    link to a new person; person page → contact; DM header → contact; group sender → contact).

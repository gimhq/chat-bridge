# 20260914-0206-contact-detail Contact detail page: everything about one contact

- **status**: completed
- **priority**: P1
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-14 02:06

## Description

The user cannot open the details of a person in the web UI: select someone and see everything
related to them. Only Persons (`/persons/{p}`) have a detail page, and with a single account no
person exists (auto-link needs the same phone on two accounts), so contacts have no detail view
at all. `LinkContactDialog` exists but no page uses it.

Acceptance (proposed, awaiting approval):

- A contact detail page per account and user, reachable from the contacts table, the direct chat
  header, a message's sender name, and a person's linked identities.
- It shows the profile (all names, phone, handle, email, bio, flags, id), the person it belongs to
  with the other platform identities (or linking to a person), the direct chat and the groups
  shared with them, a message timeline (direct conversation, or also their messages in groups),
  and the requests (calls, invites) from them; actions: message, alias, block, link / unlink.
- API: contact chats, contact messages and a `from` filter on requests, with store / core / server
  tests; `api.md` updated; Vitest for the new components; browser smoke.

## ActiveForm

Building the contact detail page

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Plan: `docs/plan/20260914-0206-contact-detail.md`.
- complete: contact detail page with shared chats, cross-chat timeline, requests and person link; entry points from contacts, DM header, group senders and person links; Go and web gates plus browser smoke 7/7

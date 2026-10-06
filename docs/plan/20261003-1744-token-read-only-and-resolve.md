# 20261003-1744-token-read-only-and-resolve Scoped tokens: read-only switch; start a chat with an allowed contact

- **status**: completed
- **createdAt**: 2026-10-03 17:44
- **approvedAt**: 2026-10-03 17:44
- **relatedTask**: 20261003-1744-token-read-only-and-resolve

## Context

- A scoped token (`internal/core/tokens.go`) resolves to allowed contacts and chats; inside that
  set it can read and write alike.
- Allowed chats are the listed chats plus the direct chats the store already holds for the allowed
  contacts (`store.DirectChats`). A contact the account never talked to has no chat row, so the
  token cannot reach it, and `POST …/chats/resolve` is admin-only.
- `Core.ResolveChat` asks the adapter and stores nothing. A resolve reaches the platform (WhatsApp
  checks whether a number is registered), so a scoped token must not be able to probe arbitrary
  handles through the owner's account.

## Proposal

**Read-only.** `scope.read_only` (boolean, default `false`). One helper, `Core.allowWrite`,
answers `403 forbidden` for a read-only token; it guards `Send`, `Edit`, `Delete`, `React`,
`Typing`, `MarkRead` (it sends a read receipt), `Upload` and `ResolveChat`. Reads, including
`backfill=1` and `POST /media/{id}/fetch`, stay available.

**Starting a chat.** `POST /accounts/{a}/chats/resolve` leaves the admin-only set. For a scoped
token:

1. The handle must name an allowed contact of that account: its user id, or the `handle` or
   `phone` the store holds for it. Anything else answers `404` before the adapter is asked.
2. The adapter resolves the handle; the result must be a direct chat whose `user_id` is that
   allowed contact, else `404`.
3. The chat is stored as a direct chat with the contact as a member (`storeChatInfo`), so the
   next scope resolution includes it and the token can send to it.

The admin token's resolve is unchanged.

## Risks

- A scope edit that sets `read_only` takes effect on the next request, like every scope change.
- Step 3 creates a chat row before any message exists; `chat.new` is emitted for it.
- Matrix resolves a user id by creating a direct room, so a scoped resolve there has a visible
  effect on the platform; it is still limited to allowed contacts and blocked for read-only tokens.

## Scope

`internal/model` (one field), `internal/core/tokens.go`, `internal/core/chats.go`,
`internal/core/messages.go`, `internal/core/media.go`, `internal/server/server.go`, server tests,
`docs/api.md`, README, changelog.

## Alternatives

- A permission list (`["read", "send", "react", …]`) instead of one switch: more precise, but the
  request was for reading versus sending.
- Letting a scoped token send straight to `chat_id = user_id` without resolving: works where the
  direct chat id is the user id (WhatsApp, Telegram, Signal) but not on Matrix.

## Annotations

- Approved with the request (2026-10-03): the user asked for the read-only switch and for
  starting chats, and confirmed that groups are visible only when listed.

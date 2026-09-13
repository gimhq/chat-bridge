# 20260913-1903-web-ui-and-requests Embedded web UI; requests (invites, calls)

- **status**: completed
- **priority**: P1
- **owner**: worker-a/session-chatbridge-01
- **createdAt**: 2026-09-13 19:03

## Description

Two deliverables the user asked for together:

1. An embedded management web UI served by the chat-bridge binary at `/ui/`: view and manage
   accounts (create, login wizard, reconnect, logout, delete, profile), chats (list, flags, create
   group, rename), a chat timeline (media, replies, reactions, send text and media, older history,
   search), contacts (alias, block), requests, live events, and system status / webhooks. It is a
   plain consumer of `/v1` and reuses `server.token`.
2. Requests (`api.md` §4.8), scoped to what the user named: pending invites are listed and can be
   accepted or rejected; incoming calls are listed and can be rejected.

Acceptance:

- `GET /accounts/{a}/requests`, `GET …/requests/{id}`, `POST …/requests/{id}/accept|reject`
  behave as documented; `request.new` / `request.updated` events are emitted; calls reject only;
  answering a non-pending request is `409`; pending requests past `expires_at` become `expired`.
- Adapter contract: event kind `request`, optional `RequestAnswerer`, wire method `request.answer`.
- Mappings: WhatsApp incoming calls (reject) and group invite messages (accept joins); Telegram
  incoming calls (reject) and join requests to owned groups (approve / dismiss); Matrix room
  invites (join / leave) and `m.call.invite` (reject). Signal (hosted) has no request source.
- The UI builds with `bun run build` into `web/dist`, is embedded with `go:embed`, and passes
  `lint`, `typecheck`, `test`, `build`; the Docker image contains it.
- Docs: `api.md` §4.8 and §6, `adapter-protocol.md`, `storage.md` (schema v4), README (UI),
  AGENTS gate, changelog.

## ActiveForm

Building the web UI and request handling

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Plan: `docs/plan/20260913-1903-web-ui-and-requests.md`. Supersedes Phase C of
`20260913-0530-api-framework-completion` for the request kinds listed above; contact requests
(WeChat-style) stay unimplemented because no current adapter has them.

- complete: web UI at /ui/ and requests (invites, calls) delivered; Go, web and Docker gates plus Playwright smoke 13/13 verified

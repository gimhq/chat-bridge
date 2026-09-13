# 20260913-1903-web-ui-and-requests Embedded web UI; requests (invites, calls)

- **status**: completed
- **createdAt**: 2026-09-13 19:03
- **approvedAt**: 2026-09-13 19:03
- **relatedTask**: 20260913-1903-web-ui-and-requests

## Context

The user approved an embedded web UI ("开始处理") and asked in the same message for invite and call
handling: calls can be rejected, invites are shown. The two open questions from the proposal were
not answered; the recommended defaults apply: React + Vite (`/pma-web` baseline) rather than a
build-less page, and the existing `server.token` rather than a separate read-only token.

Findings:

- `api.md` §4.8 already specifies Requests (`contact_request`, `chat_invite`, `join_request`,
  `call`; `pending|accepted|rejected|expired`). Nothing is implemented: no table, model, events,
  endpoints, adapter event kind, or `request.answer`.
- WhatsApp: `events.CallOffer` / `CallTerminate` already become call messages; `cli.RejectCall`
  exists. Group invites arrive as `GroupInviteMessage` (group JID, code, expiration, name,
  caption) and `cli.JoinGroupWithInvite` accepts them. There is no platform "decline".
- Telegram: `UpdatePhoneCall` carries `PhoneCallRequested` / `PhoneCallDiscarded`;
  `phone.discardCall` rejects. `UpdatePendingJoinRequests` lists recent requesters of owned groups;
  `messages.hideChatJoinRequest{approved}` answers. Users are added to groups directly, so there
  is no invite to accept.
- Matrix: invites are `m.room.member` with `membership: invite` for the own user in the sync
  `invite` section (`event.SourceInvite`), with stripped state such as `m.room.name`;
  `JoinRoomByID` / `LeaveRoom` answer. Calls are `m.call.invite` (`call_id`, `lifetime`, SDP);
  `m.call.reject` declines; cryptohelper re-dispatches decrypted call events.
  Today `onMember` also turns invite-section member events into chat/member rows for rooms the
  account has not joined; that must stop.
- Media and SSE need `Authorization`; `<img src>` and `EventSource` cannot send headers. The UI
  fetches media as blobs and reads SSE through `fetch` streaming, so no token-in-URL is added.
- nsl mounts paths: `nsl route chat-bridge:/v1 8080` (no strip) plus `nsl run -n chat-bridge vite`
  gives one origin for UI (`/ui/`) and API (`/v1`) in dev. `AGENTS.md` currently says nsl is not
  used; this task starts using it for the web dev loop only.
- Registry (verified 2026-09-13): vite 8.3.0, @tanstack/react-router 1.170.36, shadcn 4.21.0,
  typescript 7.0.2; bun 1.4.0 and node 24 are installed.

## Proposal

**Backend — requests**

1. Model `Request {id, account_id, kind, state, from{id,name}, chat{id,name,kind}, message,
   call{kind}, actions[], created_at, expires_at, answered_at, raw}`; `actions` tells a client what
   it may do (`call` → `["reject"]`, others → `["accept","reject"]`, none once answered).
2. Store schema v4: `requests` keyed `req_<uuid>`, unique `(account_id, platform_key)` so an
   adapter can re-emit the same request with new state or names; `platform_ref` kept private.
   List newest first with cursor, filters `kind`, `state`.
3. Adapter contract: event kind `request` with `adapter.Request{Key, Kind, State, From, Chat,
   Message, CallKind, PlatformRef, CreatedAt, ExpiresAt}`; interface `RequestAnswerer`
   `AnswerRequest(ctx, account, RequestAnswer{Kind, Key, ChatID, FromID, PlatformRef, Action,
   Reason})`. Core ingest upserts: new row → `request.new`; state or visible field changed →
   `request.updated`. Wire: event kind `request`, method `request.answer`.
4. Core: `ListRequests`, `GetRequest`, `AnswerRequest` (connected account, pending only → `409`,
   calls reject only → `400`, adapter call, row → `accepted|rejected`, `request.updated`); GC
   step expires pending rows past `expires_at`.
5. Server: the four §4.8 routes.
6. Adapters: WhatsApp calls (key `call:<id>`, expiry 2 min fallback, `CallTerminate` → expired,
   reject = `RejectCall`) and invite messages (key `invite:<group>:<code>`, accept =
   `JoinGroupWithInvite`, reject local only); Telegram calls (`call:<id>`, reject =
   `phone.discardCall` busy) and join requests (`join:<chat>:<user>`, approve / dismiss via
   `hideChatJoinRequest`); Matrix invites (`invite:<room>`, name from stripped state, own join →
   accepted, own leave → rejected, answer = join / leave) and calls (`call:<call_id>`, lifetime
   expiry, hangup / answer / reject → expired, reject = `m.call.reject`). Fake adapter records
   answers for tests.

**Frontend — `web/` (single app, `/pma-web` baseline)**

7. React 19, TypeScript 7, Vite 8 (`base: '/ui/'`), TanStack Router (file routes under
   `src/app/routes`, basepath `/ui`) and Query, Tailwind v4, shadcn `base-nova` on
   `@base-ui/react`, lucide-react, ESLint `@antfu/eslint-config`, Vitest. No Zustand: the only
   client state is the token (localStorage) and route params.
8. `shared/lib/http.ts`: typed fetch on `/v1` with bearer token and the API error envelope;
   `shared/lib/sse.ts`: fetch-stream SSE parser with `Last-Event-ID` resume; `shared/api/types.ts`
   mirrors `internal/model`. Events invalidate the matching queries (message → chat timeline and
   chat list, account status → accounts, request → requests).
9. Pages: token gate; Accounts (list, create with platform config from `/platforms`, login wizard
   rendering input / display(qr, code, url, text) / done / failed steps, reconnect, logout, delete,
   self with name / bio edit); Chats per account (list with unread and last message, mute /
   archive, create group, rename); Chat (timeline with attachments fetched as blobs, reply quote,
   reactions, system / call messages, send text and file, load older with `backfill=1`, search);
   Contacts (search, alias, block); Requests (pending first, accept / reject per `actions`, live);
   Events (live SSE log with type filter); System (`/v1/status`, platforms and capabilities,
   webhooks list / create / delete).
10. Serving: package `web` embeds `all:dist` (a `.gitkeep` keeps the directory present in clean
    clones); the server mounts `/ui/*` with SPA fallback and long cache for hashed assets, and
    `/` redirects to `/ui/`. Without a build `/ui/` answers a short "run bun run build" page.
11. Build: Dockerfile gains an `oven/bun` stage building `web/dist` before the Go stage; dev uses
    nsl as above (`bun run dev` in `web/`, Go server routed with `nsl route`).

**Verification and docs**

12. Go: store, core, server, remote tests for requests; adapter unit tests for the conversions that
    are pure functions (WhatsApp invite message, Matrix call SDP kind). Web: Vitest for http, SSE
    parser, login-step helpers, message rendering helpers; `lint`, `typecheck`, `build`; manual
    smoke against a running server with the fake data in tmux.
13. Docs: `api.md` §4.8 (actions, per-platform kinds) and §6, `adapter-protocol.md` (event kind,
    method, interface), `storage.md` (schema v4), README (web UI, dev loop), AGENTS (web gate, nsl
    for web dev), changelog.

## Risks

- No live accounts: call rejection and invite acceptance are verified against fakes only.
- Telegram call rejection needs the call's access hash from `PhoneCallRequested`; answering after a
  restart works only while the row still carries the ref (kept in `platform_ref`).
- WhatsApp invite "reject" is local bookkeeping because the platform has no decline; the UI shows it
  as dismissed.
- Coverage target 80% for the web app is ambitious for page components; logic modules are tested,
  pages are smoke-tested, and the achieved number is reported as is.
- Binary grows by the UI bundle (estimated a few hundred KB gzipped).
- Size: about 1 200 lines of Go plus tests, about 3 000 lines of TypeScript including shadcn output.

## Scope

- New: `internal/core/requests.go`, `internal/store/requests.go`, `internal/server` request
  handlers, `internal/adapters/{whatsapp,telegram,matrix}/requests.go`, `web/` (app, `embed.go`),
  server UI mount
- Touched: `internal/adapter/adapter.go`, `internal/adapter/fake`, `internal/model/model.go`,
  `internal/core/{sink,media}.go`, `internal/store/store.go`, `internal/adapters/remote/{wire,adapter}.go`,
  adapter event wiring files, `Dockerfile`, `.gitignore`, `.dockerignore`, docs listed above
- Dependencies: web toolchain listed in step 7 at latest stable; no new Go dependencies

## Alternatives

- Build-less single HTML page: smaller, but no component library, no typed API, hard to extend;
  the user accepted the recommended stack.
- Token in query string for media and SSE: simpler client, but leaks the token into logs and
  history; blob fetch and fetch-stream SSE avoid it.
- Serving the UI from a separate container: extra deployment unit for no benefit.

## Annotations

- 2026-09-13 20:00 — implemented. Deviations and findings:
  - Vitest 5.0.0 (latest at the registry) instead of the baseline's 4; TypeScript pinned to 6.0.3
    because typescript-eslint rejects TypeScript 7 (decision record in `docs/decisions/`).
  - shadcn `init` with preset `nova` on base `base` (the CLI has no `base-nova` preset name); its
    generated `cn` import pointed at an unrelated npm package and was replaced.
  - `web/go.mod` stub added: `flatted` ships a `.go` file that the root `./...` pattern picked up.
  - The UI starts the event stream at `events_cursor` from `/v1/status`; without it the server
    replays retained history.
  - Coverage is 45% of statements (81% in `shared/lib`), below the 80% target. Accounts login,
    requests, message rendering, composer and token gate have component tests; contacts, system,
    events and the shell are covered by the Playwright smoke only.
  - Verified: Go gate at the repo root, web gate (lint, typecheck, test, build), Playwright smoke
    13/13 against the real binary with a fake remote adapter. Real platform calls (WhatsApp call
    reject, invite join, Telegram call discard, Matrix join / reject) are verified with unit tests
    and fakes only; no live accounts in this environment.
  - Docker: the `test` target and the full image build pass; the runtime image serves `/ui/` and
    its assets.

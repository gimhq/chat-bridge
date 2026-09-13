# 20260913-0530-api-framework-completion Complete the bridge API framework against api.md

- **status**: draft
- **createdAt**: 2026-09-13 05:30
- **approvedAt**: (pending)
- **relatedTask**: 20260913-0530-api-framework-completion

## Context

Audit of `docs/api.md` §3–§6 and `docs/storage.md` §7 against `internal/server` (routes),
`internal/core`, `internal/store` (schema v2) and `internal/adapter` (interfaces):

| Spec | Implemented | Missing |
|---|---|---|
| Accounts, login, platforms, status, keys | all | `PATCH /self` returns `unsupported` unconditionally (no `SelfUpdater`) |
| Chats | list (kind/archived/tag), get, resolve, read, typing, patch (muted/archived/tags) | `POST /chats` (`chat.create`), `PATCH {name}` to the platform, `person=` filter |
| Messages | list (cursor/before/after), get (`raw`), send, edit, delete, reactions | `messages/search`, `backfill=1` platform history, `Backfiller` interface |
| Media | upload, get (Range via `ServeFile`), meta, fetch | — |
| Contacts | list, get, patch `alias` | patch `blocked` (`Blocker`) |
| Persons §3.8 / §4.7 | — | everything: tables, CRUD, links, merge, suggest, auto-link, `person_id` on contact/sender/chat, `/persons/{p}/chats|messages`, `person=` on events/chats, `person.updated` |
| Requests §4.8 | — | everything: table, model, endpoints, `request.new/updated`, adapter event kind, `RequestAnswerer` / `request.answer` |
| Events §6 | 13 of 16 types, long-poll, SSE, webhooks with backoff/pause/gap | `person.updated`, `request.new`, `request.updated` |
| Retention storage.md §7 | expired uploads (1 h), events by `events.retention_days` | `retention.{messages,raw,events,receipts,media}_days`, `media.max_gb`, short retention for typing/presence/platform events, media purge (`purged` state keeps `remote_ref`), ephemeral expiry → `content.type: expired` + `message.updated` |
| Adapter wire §4 | all listed methods except below | `chat.create`, `chat.update {name}`, `chat.backfill`, `self.update`, `contact.block`, `request.answer` |

Adapters: WhatsApp (whatsmeow) can create/rename groups, block, set push name/status, join group
invites, reject calls; Telegram (gotd) can create groups, rename, block, update profile, fetch
history, answer join requests; Matrix can create/rename rooms, ignore users, set profile, fetch
`/messages`, accept/reject invites. Signal (hosted) inherits what bridgev2 exposes: identifier
resolving and contact list only; the rest stays `unsupported` for it.

## Proposal

Four phases, each ending with the quality gate and a commit so partial delivery is usable.

**Phase A — platform-backed endpoints (adapter interfaces + core + server + wire)**

1. `adapter.ChatCreator` (`CreateChat(kind, name, members) → Chat`, cap `chat.create`) and
   `adapter.ChatUpdater` (`UpdateChat(chatID, name)`): WhatsApp (`CreateGroup`, `SetGroupName`),
   Telegram (`messages.createChat`, `editChatTitle`), Matrix (`CreateRoom`, `m.room.name`).
   `POST /accounts/{a}/chats` (201, emits `chat.new`), `PATCH … {name}` forwards when the cap
   exists, else `422 unsupported`. Wire: `chat.create`, `chat.update`.
2. `adapter.Backfiller` (`Backfill(chatID, before {ts, id}, limit) → ([]Message, more)`, cap
   `message.history`): Telegram (`messages.getHistory`), Matrix (`/messages` backwards). Core:
   `GET …/messages?backfill=1` fills from the platform when the local page is short, stores rows
   with `Backfill: true` (no unread/no auto-download, as today), then answers from the store. Wire:
   `chat.backfill`.
3. `adapter.SelfUpdater` (`UpdateSelf(name?, bio?, avatar MediaSource?) → Contact`, cap
   `self.update`): WhatsApp (push name, about, profile picture), Telegram (`account.updateProfile`,
   photo), Matrix (displayname, avatar). `PATCH /accounts/{a}/self` updates `Account.self`, emits
   `account.status`. Wire: `self.update`.
4. `adapter.Blocker` (`Block(userID, blocked)`, no new cap — every built-in adapter has it):
   WhatsApp (`UpdateBlocklist`), Telegram (`contacts.block/unblock`), Matrix (`m.ignored_user_list`).
   `PATCH contacts {blocked}`, `contact.updated`. Wire: `contact.block`.
5. `GET /accounts/{a}/messages/search?q&chat&cursor&limit`: SQLite FTS5 virtual table
   `messages_fts(text)` kept by triggers (schema v3, backfilled on migration); ranked by recency.

**Phase B — Persons (bridge-local overlay)**

6. Schema v3: `persons(id, name, tags, notes, created_at, updated_at)`,
   `person_links(person_id, account_id, user_id, source, linked_at, UNIQUE(account_id,user_id))`,
   `person_unlinks(account_id, user_id, other_account_id, other_user_id)` (auto-link suppression).
7. Core: CRUD, `links` add/remove, `merge`, `suggest` (unlinked contacts sharing an E.164 phone),
   auto-link on contact upsert when `persons.auto_link_by_phone` (config, default true), derived
   `channels`, `GET /persons/{p}/chats` and `/messages` (merged by timestamp, `scope=direct|all`),
   `person_id` decoration on Contact, Sender, direct Chat; `person=` filter on `/events` and
   `/accounts/{a}/chats`; `person.updated` event (with `deleted: true`).
8. Server: the nine `/persons` routes of §4.7.

**Phase C — Requests**

9. Schema v3: `requests(id, account_id, kind, state, from_id, chat_id, message, platform_ref,
   created_at, expires_at, answered_at, raw)`. Model `Request`; events `request.new` /
   `request.updated`; `GET/…/requests`, `GET …/{id}`, `POST …/accept|reject`.
10. Adapter side: `adapter.Event` kind `request` (`Request` payload with `PlatformRef`), interface
    `adapter.RequestAnswerer` (`AnswerRequest(ref, action, opts)`), wire `request.answer`.
    Mappings: Matrix room invites (`chat_invite`: join / leave), WhatsApp incoming calls (`call`,
    reject only, auto-expire on call end) and group-invite messages (`chat_invite` via
    `JoinGroupWithInvite`), Telegram chat join requests on owned groups (`join_request`).
    Expiry sweep in the GC loop (`expires_at` → `expired`, `request.updated`).

**Phase D — retention and ephemeral messages (storage.md §7)**

11. Config: `retention.{messages_days: 0, raw_days: 7, events_days: 7, receipts_days: 30,
    media_days: 90}`, `media.max_gb: 20`; `events.retention_days` kept as a deprecated alias.
12. GC loop steps (each its own transaction, bounded batches): raw payloads, receipts rows, events
    (`chat.typing` / `presence` 1 h, `platform.event` 24 h, others `events_days`), media by last
    access then oldest-first over `max_gb` (`state: purged`, `remote_ref` kept, `message.updated`),
    optional message purge when `messages_days > 0`, ephemeral messages at `expires_at`
    (`content.type: expired`, attachments purged, `message.updated`), request expiry (Phase C).
13. Tests with an injected clock; `docs/storage.md` §7 and §2 (schema v3) synced.

**Docs, each phase**: `api.md` (mark behaviour, `person_id`, search), `adapter-protocol.md` §4/§10
(new methods and interfaces, `request` event kind), `storage.md`, README capability list, changelog.

## Risks

- No live platform accounts here: Phase A/C platform calls are verified by compile + unit tests
  with fakes; real behaviour (group creation, blocking, call rejection) needs the user's accounts.
  Mitigation: keep each platform call a thin wrapper around one library method.
- FTS5 in modernc SQLite: supported, but the migration rebuilds the index over all messages once;
  bounded by batch inserts inside the schema-v3 transaction.
- Persons auto-link by phone can be wrong for shared numbers; links are removable and suppressed
  per pair afterwards, as the spec says. Default stays `true` per spec; config can turn it off.
- Requests widen adapter events; remote adapters written against the current wire keep working
  (new kind is additive, `request.answer` is optional and capability-gated).
- Retention now deletes data by default (raw payloads 7 d, media 90 d); documented defaults match
  the spec that was already published.
- Size: about 3 500 lines of Go plus tests across four phases; Phases B and C are independent of A
  and can be reordered or dropped on request.

## Scope

- New: `internal/core/{persons,requests,retention}.go` (+ tests), `internal/store/{persons,requests,search}.go`
  (+ tests, schema v3), server handlers for chats create, search, persons, requests, self, blocked
- Touched: `internal/adapter/adapter.go` (interfaces, event kind, capabilities), `internal/model`
  (Person, Request, `person_id` fields, `expired` content), `internal/adapters/{whatsapp,telegram,matrix}`
  (new interface methods), `internal/adapters/remote/{wire,adapter,hub}.go` (new RPCs),
  `internal/adapters/connector` (advertise `contacts` only; no new caps), `internal/config`,
  `internal/core/{chats,messages,media,events,sink}.go`, docs listed above
- Dependencies: none new (FTS5 ships in modernc.org/sqlite)

## Alternatives

- Ship only Phase A and D (platform-backed endpoints and retention) and drop Persons/Requests from
  the spec: smaller, but the API would lose the cross-platform identity feature the user asked for
  at design time and the "answerable events" lifecycle; keep them, order them last.
- Search with `LIKE` instead of FTS5: no migration and no index rebuild, but unusable beyond a few
  hundred thousand rows; FTS5 costs one virtual table.
- Persons as a consumer-side concern (outside the bridge): contradicts §3.8, which was chosen
  precisely so every consumer sees the same links.

## Annotations

(none yet)

# Bridge Storage Design

Companion to `api.md`. Status: draft v1, 2026-09-12.

## 1. Layout on disk

```
<storage.data_dir>/           # default ./data, see api.md §2.2
  chatbridge.db               # everything the API serves: accounts, chats, contacts, messages, events, media index
  media/<aa>/<sha256>         # content-addressed bytes, <aa> = first two hex chars
  media/tmp/                  # in-flight writes before rename
  accounts/<account_id>/      # adapter-private session state (whatsmeow.db, telegram session.json, matrix session.json)
```

- One SQLite file for the bridge, WAL mode, `busy_timeout=5000`, a single writer connection
  (`SetMaxOpenConns(1)` on the write pool, a separate read pool). Personal-scale traffic (tens of
  thousands of messages a month) is far below SQLite's comfort zone; there is no reason for a server
  database.
- Adapter state is opaque to the bridge and lives in its own directory so `DELETE /accounts/{a}`
  is "delete rows where account_id = ? + rm -rf the directory". Adapters never write to `chatbridge.db`
  directly; they go through the store API (§5).
- Backup is `sqlite3 chatbridge.db ".backup …"` plus `rsync media/`. Nothing else holds state.

## 2. Schema

Types are SQLite affinities. `*_id` columns are platform-native strings. Times are unix seconds
unless named `*_ms`. `json` columns hold canonical JSON as text.

```sql
CREATE TABLE accounts (
  id            TEXT PRIMARY KEY,
  platform      TEXT NOT NULL,
  status        TEXT NOT NULL,             -- unpaired|logging_in|connecting|connected|disconnected|error
  self_id       TEXT,                      -- the contacts row with is_self = 1; profile lives there, not here
  config        TEXT NOT NULL DEFAULT '{}',-- json, adapter-specific, secrets included (file is 0600)
  device        TEXT NOT NULL DEFAULT '{}',-- json, adapter-reported session detail, refreshed on connect
  login_flow    TEXT,                      -- how the session was established
  login_ident   TEXT,                      -- phone / username / user id the owner entered
  login_at      INTEGER,
  error         TEXT,                      -- json {code,message} or NULL
  created_at    INTEGER NOT NULL,
  connected_at  INTEGER,
  adapter       TEXT NOT NULL DEFAULT ''     -- adapter instance serving the account ('' = not yet bound)
);

CREATE TABLE contacts (
  account_id    TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  id            TEXT NOT NULL,
  handle        TEXT,
  phone         TEXT,                      -- E.164 when known
  email         TEXT,
  names         TEXT NOT NULL DEFAULT '{}',-- json {alias,profile,username,first,last}, platform-sourced
  alias_local   TEXT,                      -- owner alias stored by the bridge when the platform cannot
  avatar_media  TEXT,
  is_self       INTEGER NOT NULL DEFAULT 0,
  is_contact    INTEGER NOT NULL DEFAULT 0,
  blocked       INTEGER NOT NULL DEFAULT 0,
  bio           TEXT,
  raw           TEXT,
  updated_at    INTEGER NOT NULL,
  phone_norm    TEXT,                      -- phone digits only, at least 7; drives person auto-link
  PRIMARY KEY (account_id, id)
);
CREATE INDEX contacts_phone ON contacts(phone) WHERE phone IS NOT NULL;
CREATE INDEX contacts_phone_norm ON contacts(phone_norm) WHERE phone_norm IS NOT NULL;

CREATE TABLE persons (
  id          TEXT PRIMARY KEY,             -- per_<ulid>
  name        TEXT,
  tags        TEXT NOT NULL DEFAULT '[]',   -- json array
  notes       TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE person_links (
  account_id  TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  person_id   TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
  source      TEXT NOT NULL,                -- manual|phone
  linked_at   INTEGER NOT NULL,
  PRIMARY KEY (account_id, user_id),        -- a contact belongs to at most one person
  FOREIGN KEY (account_id, user_id) REFERENCES contacts(account_id, id) ON DELETE CASCADE
);
CREATE INDEX person_links_person ON person_links(person_id);

CREATE TABLE person_unlinks (                -- pairs the owner split; auto-link must not re-join them
  account_id TEXT NOT NULL, user_id TEXT NOT NULL,
  other_account_id TEXT NOT NULL, other_user_id TEXT NOT NULL,
  PRIMARY KEY (account_id, user_id, other_account_id, other_user_id)
);

CREATE TABLE chats (
  account_id        TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  id                TEXT NOT NULL,
  kind              TEXT NOT NULL,          -- direct|group|channel|self
  name              TEXT,
  avatar_media      TEXT,
  unread_count      INTEGER NOT NULL DEFAULT 0,
  last_message_ts   INTEGER,
  last_message_id   TEXT,
  muted             INTEGER NOT NULL DEFAULT 0,
  archived          INTEGER NOT NULL DEFAULT 0,
  tags              TEXT NOT NULL DEFAULT '[]', -- json array, bridge-local
  pinned_ids        TEXT NOT NULL DEFAULT '[]',
  ephemeral_ttl_s   INTEGER,
  raw               TEXT,
  updated_at        INTEGER NOT NULL,
  PRIMARY KEY (account_id, id)
);
CREATE INDEX chats_recent ON chats(account_id, archived, last_message_ts DESC);

CREATE TABLE chat_members (
  account_id  TEXT NOT NULL,
  chat_id     TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  chat_name   TEXT,                         -- per-chat nickname
  role        TEXT NOT NULL DEFAULT 'member',
  joined_at   INTEGER,
  left_at     INTEGER,                      -- NULL while a member; kept after leaving for name resolution
  PRIMARY KEY (account_id, chat_id, user_id),
  FOREIGN KEY (account_id, chat_id) REFERENCES chats(account_id, id) ON DELETE CASCADE
);
CREATE INDEX chat_members_user ON chat_members(account_id, user_id);  -- memberships of a user (identity changes)

CREATE TABLE messages (
  seq           INTEGER PRIMARY KEY,        -- rowid; insertion order, tiebreaker and internal FK
  account_id    TEXT NOT NULL,
  chat_id       TEXT NOT NULL,
  id            TEXT NOT NULL,              -- platform message id
  sender_id     TEXT NOT NULL,
  sender_name   TEXT,                       -- snapshot at receive time
  from_me       INTEGER NOT NULL,
  ts            INTEGER NOT NULL,           -- platform timestamp
  received_at   INTEGER NOT NULL,
  type          TEXT NOT NULL,              -- content.type, denormalised for filtering
  text          TEXT,                       -- content.text, denormalised for FTS
  content       TEXT NOT NULL,              -- json, full content object minus attachment bytes
  reply_to      TEXT,
  thread_id     TEXT,
  mentions      TEXT NOT NULL DEFAULT '[]',
  forwarded     INTEGER NOT NULL DEFAULT 0,
  ephemeral     TEXT,                       -- json {expires_at,view_once} or NULL
  status        TEXT,                       -- outbound only: sent|delivered|read|failed
  client_id     TEXT,
  edited_at     INTEGER,
  deleted_at    INTEGER,
  UNIQUE (account_id, chat_id, id),
  FOREIGN KEY (account_id, chat_id) REFERENCES chats(account_id, id) ON DELETE CASCADE
);
CREATE INDEX messages_timeline ON messages(account_id, chat_id, ts DESC, seq DESC);
CREATE INDEX messages_by_id ON messages(account_id, id);
CREATE INDEX messages_sender ON messages(account_id, sender_id);             -- a user's messages (identity changes)
CREATE UNIQUE INDEX messages_client_id ON messages(account_id, chat_id, client_id) WHERE client_id IS NOT NULL;
CREATE INDEX messages_ephemeral ON messages(json_extract(ephemeral,'$.expires_at')) WHERE ephemeral IS NOT NULL;

CREATE VIRTUAL TABLE messages_fts USING fts5(text, content='messages', content_rowid='seq', tokenize='trigram');
-- plus the three standard external-content triggers (insert/delete/update) on messages.text.
-- trigram (not unicode61) so CJK text, which has no word boundaries, is searchable by substring;
-- search terms shorter than three characters fall back to LIKE.

CREATE TABLE message_versions (              -- previous bodies after an edit
  message_seq INTEGER NOT NULL REFERENCES messages(seq) ON DELETE CASCADE,
  edited_at   INTEGER NOT NULL,
  content     TEXT NOT NULL,
  PRIMARY KEY (message_seq, edited_at)
);

CREATE TABLE reactions (
  message_seq INTEGER NOT NULL REFERENCES messages(seq) ON DELETE CASCADE,
  sender_id   TEXT NOT NULL,
  emoji       TEXT NOT NULL,
  ts          INTEGER NOT NULL,
  PRIMARY KEY (message_seq, sender_id, emoji)
);

CREATE TABLE receipts (                      -- per-user delivered/read; rolled up into messages.status
  message_seq INTEGER NOT NULL REFERENCES messages(seq) ON DELETE CASCADE,
  user_id     TEXT NOT NULL,
  kind        TEXT NOT NULL,                -- delivered|read
  ts          INTEGER NOT NULL,
  PRIMARY KEY (message_seq, user_id, kind)
);

CREATE TABLE media (
  id            TEXT PRIMARY KEY,           -- platform media/message id, or upl_<uuid> for uploads
  account_id    TEXT NOT NULL,
  message_seq   INTEGER REFERENCES messages(seq) ON DELETE SET NULL,  -- NULL for unreferenced uploads
  sha256        TEXT,                       -- NULL while state != ready
  mime          TEXT NOT NULL,
  size          INTEGER,
  file_name     TEXT,
  width         INTEGER, height INTEGER, duration_ms INTEGER,
  thumbnail_id  TEXT,
  state         TEXT NOT NULL,              -- ready|pending|failed|remote|purged
  remote_ref    TEXT,                       -- json, adapter's handle for a later fetch
  created_at    INTEGER NOT NULL,
  expires_at    INTEGER,                    -- uploads: created_at + 1h until referenced
  last_access   INTEGER
);
CREATE INDEX media_sha ON media(sha256);
CREATE INDEX media_message ON media(message_seq);
CREATE INDEX media_gc  ON media(state, last_access);

CREATE TABLE raw_payloads (                  -- ?raw=1; separate so the hot table stays small
  message_seq INTEGER PRIMARY KEY REFERENCES messages(seq) ON DELETE CASCADE,
  raw         TEXT NOT NULL,
  created_at  INTEGER NOT NULL
);

CREATE TABLE requests (
  id           TEXT PRIMARY KEY,             -- req_<uuid>
  account_id   TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  platform_key TEXT NOT NULL,                -- adapter's stable key; re-emits update the same row
  kind TEXT NOT NULL, state TEXT NOT NULL,
  from_id TEXT, from_name TEXT, chat_id TEXT, chat_name TEXT, chat_kind TEXT,
  message TEXT, call_kind TEXT,
  platform_ref TEXT,                         -- json, what the adapter needs to answer it
  raw TEXT,
  created_at INTEGER NOT NULL, expires_at INTEGER, answered_at INTEGER, updated_at INTEGER NOT NULL,
  UNIQUE (account_id, platform_key)
);
CREATE INDEX requests_open ON requests(account_id, state, created_at DESC);
CREATE INDEX requests_expiry ON requests(expires_at) WHERE state = 'pending';

CREATE TABLE events (
  id          INTEGER PRIMARY KEY,          -- the cursor
  account_id  TEXT,
  type        TEXT NOT NULL,
  ts_ms       INTEGER NOT NULL,
  data        TEXT NOT NULL                 -- json, full object as delivered
);
CREATE INDEX events_type ON events(type, id);

CREATE TABLE webhooks (
  id          TEXT PRIMARY KEY,
  url         TEXT NOT NULL,
  secret      TEXT NOT NULL,
  account_id  TEXT, types TEXT,             -- filters, NULL = all
  cursor      INTEGER NOT NULL DEFAULT 0,   -- last event id acknowledged with 2xx
  failures    INTEGER NOT NULL DEFAULT 0,
  next_try    INTEGER, paused_at INTEGER,
  created_at  INTEGER NOT NULL
);

CREATE TABLE schema_version (version INTEGER NOT NULL);
```

## 3. Keys and identity

- The **external** key of a message is `(account_id, chat_id, id)`; platform ids are only unique
  within an account, and on WhatsApp only within a chat. The **internal** key is `seq` (rowid):
  stable, small, and what reactions / receipts / media / versions point at, so a platform id
  change (Matrix `$local` → `$server` during send) touches one row.
- `GET /messages/{id}` without a chat resolves by `(account_id, id)`; if that hits several rows
  the newest `seq` wins. Well-behaved platforms never collide.
- Media ids stay global in the API (`/media/{id}`), but rows are account-scoped. Uploads get
  `upl_<uuid>`; adapter-fetched media reuse the platform id the message carries.
- Contacts are `(account_id, id)`. A user seen in a group but never in the address book still gets
  a row (`is_contact = 0`) so names resolve.
- `person_id` on Contact, Chat (direct), and `sender` is a read-time join on `person_links`;
  nothing on `messages` is denormalised, so relinking is instant and history follows. Cross-account
  reads (`/persons/{p}/messages`) are `WHERE (account_id, sender_id) IN (links)` (scope `all`) or
  `WHERE (account_id, chat_id) IN (direct chats)` (scope `direct`), ordered by `(ts, seq)` with the
  same cursor as a single chat.
- Auto-link by phone runs when a contact row gains or changes `phone`: find other contacts on other
  accounts with the same E.164, not in `person_unlinks` against this one; if exactly one Person is
  involved, join it, if none, create one with `source = phone`, if several, do nothing and leave it
  to `/persons/suggest`.
- The account's own profile is the `contacts` row `(account_id, accounts.self_id)` with
  `is_self = 1`. `GET /accounts/{a}` joins it as `self`; `stats` are `COUNT(*)` over `chats`,
  `messages`, open `requests`, `MAX(messages.received_at) WHERE from_me = 0`, and `MAX(events.id)`
  for the account, computed on read (cheap at this scale, cache 5 s if it ever is not).

## 4. Write paths

Every mutation is one transaction that writes state **and** appends the matching `events` rows.
Consumers therefore never see an event for state that is not readable, and never miss an event for
state that is.

| Cause | Rows touched | Events |
|---|---|---|
| Inbound message | `messages` insert (`INSERT … ON CONFLICT(account_id,chat_id,id) DO NOTHING`), `chats` upsert (`last_message_*`, `unread_count += 1` unless `from_me`), `chat_members` upsert of sender, `contacts` upsert if names changed, `media` insert (`pending` or `remote`), `raw_payloads` insert | `message.new`, plus `chat.new` if the chat row was created, `contact.updated` if names changed |
| Backfilled message (`backfill: true`) | as inbound, but `unread_count` is not incremented (the chat hint's counter is written instead) and attachments stay `remote` | `message.new`, `chat.new` / `chat.updated` |
| API send | adapter sends first; on success insert `messages` with `status = sent`, `from_me = 1`; `media` row for an upload is re-pointed (`message_seq` set, `expires_at` cleared) | `message.new` (with `client_id`) |
| Duplicate `client_id` | none; returns the existing row | none |
| Media download done | `media` → `ready`, `sha256`, `size`; bytes written to `media/<aa>/<sha>` before the row flips (write temp, fsync, rename) | `message.updated` |
| Edit (in or out) | old `content` → `message_versions`; `messages.content/text/edited_at` updated | `message.updated` |
| Delete / revoke | `deleted_at` set, `content = {"type":"deleted"}`, `text = NULL`; media refs stay, bytes GC'd if orphaned | `message.deleted` |
| Reaction | `reactions` upsert / delete | `message.reaction` |
| Receipt | `receipts` upsert; `messages.status` = max over receipts for outbound | `message.receipt` (and `message.updated` if `status` changed) |
| Ephemeral expiry (job) | `content = {"type":"expired"}`, `text = NULL`, media → `purged` | `message.updated` |
| Chat / member / contact change | the row | `chat.updated` / `contact.updated` |
| Person create / link / unlink / merge | `persons`, `person_links`, `person_unlinks` | `person.updated`, plus `contact.updated` for each contact whose `person_id` changed |
| Request lifecycle | `requests` row | `request.new` / `request.updated` |
| Typing / presence | **nothing** | event only, `events` row retained 1 h |

There is no offline send queue. If the account is not `connected`, `POST …/messages` returns
`409 account_not_ready` and nothing is stored. This keeps `pending` meaningful only for adapters
that genuinely ack asynchronously, and keeps the platform id the primary identity.

## 5. Store API (Go)

Adapters see one interface; the HTTP layer sees one reader. Sketch, not final:

```go
type Store interface {
    UpsertChat(ctx, Chat) (created bool, err error)
    UpsertContact(ctx, Contact) (changed bool, err error)
    UpsertMember(ctx, Member) error
    InsertMessage(ctx, Message, raw json.RawMessage) (seq int64, inserted bool, err error)
    UpdateMessage(ctx, seq int64, fn func(*Message)) error
    SetReaction(ctx, seq int64, senderID, emoji string, remove bool) error
    SetReceipt(ctx, seq int64, userID, kind string, at time.Time) error
    UpsertMedia(ctx, Media) error
    UpsertRequest(ctx, Request) error
    Emit(ctx, Event) error
    Tx(ctx, func(Store) error) error
}
```

`Tx` is how an adapter groups "insert message + upsert chat + emit" atomically; each method
outside a `Tx` runs in its own.

Reads are cursor-paginated. A message cursor encodes `(ts, seq)` of the last row as base64url
JSON; the query is `WHERE (ts, seq) < (?, ?) ORDER BY ts DESC, seq DESC LIMIT ?`. Sorting on
platform `ts` (not `received_at`) means backfilled history lands in the right place; `seq` breaks
ties deterministically.

## 6. Media bytes

- Content-addressed by SHA-256. Two messages with the same photo share one file; `media` rows are
  the references. The bytes are removed when the last row referencing the hash is `purged` or
  deleted.
- Write path: stream to `media/tmp/<uuid>`, hash while writing, fsync, rename to
  `media/<aa>/<sha>`. If the target exists, discard the temp file.
- `GET /media/{id}` serves with `Content-Type` from the row, supports `Range`, sets `ETag` to the
  hash, and bumps `last_access` at most once per hour per row.
- Adapters decide `ready` vs `remote` per config: `media.auto_download` default `image, voice,
  sticker` with `media.auto_download_max_mb = 16`; larger or other kinds stay `remote` until
  `POST /media/{id}/fetch`. `remote_ref` holds whatever the adapter needs (WhatsApp media keys and
  URL; Telegram file id; Matrix mxc URI).
- Uploads live under the same table with `expires_at`; the GC job deletes unreferenced ones past
  expiry.

## 7. Retention and GC

One background job every 10 minutes, each step its own transaction, bounded batch size:

| What | Default | Config key |
|---|---|---|
| Messages, chats, contacts, reactions | forever | `retention.messages_days` (0 = forever) |
| `raw_payloads` | 7 days | `retention.raw_days` |
| `events` | 7 days | `retention.events_days` |
| `events` of type `chat.typing`, `presence` | 1 hour | fixed |
| `events` of type `platform.event` | 24 hours | fixed |
| `receipts` rows (rollup stays on the message) | 30 days | `retention.receipts_days` |
| Media bytes, by last access | 90 days, or oldest-first when `media/` exceeds `media.max_gb` (default 20) | `retention.media_days`, `media.max_gb` |
| Unreferenced uploads | 1 hour | fixed |
| Ephemeral messages | at `expires_at` | platform-driven |

Purging media flips `state` to `purged` and keeps `remote_ref`, so a later `POST /media/{id}/fetch`
can bring it back if the platform still has it. Message rows are never physically deleted by
retention unless `retention.messages_days > 0`; deleting an account is the only hard delete.

Webhook delivery reads `events` by `cursor`; a webhook paused for longer than
`retention.events_days` resumes from the oldest surviving event and the response includes
`gap: true`.

## 8. Migrations

`schema_version` holds one integer. Migrations are numbered SQL strings embedded in the binary
(`internal/store/store.go`), applied in order inside a transaction at startup; the process refuses
to start on a database newer than it knows. No down migrations. Adapter-private databases are
versioned by their own libraries.

While chat-bridge is in development there is no data to carry forward: the schema was reset to a
single base version, and a database created by an earlier build (version above 1) is refused;
delete the data directory. Once a database is deployed, schema changes append migrations again.

| Version | Change |
|---|---|
| 1 | base schema (§2): accounts with instance binding, contacts with `phone_norm`, chats, members, messages with trigram FTS5, reactions, receipts, media, raw payloads, events, webhooks, requests, persons; indexes on senders and memberships for identity changes |

## 9. Sizing

Personal use, one account, 200 messages/day: about 75 k `messages` rows/year at roughly 1 KB each
including `content`, 75 MB/year before media. `raw_payloads` at 7 days stays under 10 MB.
`events` at 7 days and ~5 events per message stays under 20 MB. Media dominates; the 20 GB cap
with LRU purge is the only real limit. FTS5 roughly doubles the text footprint.

## 10. What the current implementation must change

| Current (`internal/store`) | New |
|---|---|
| `messages` PK `(chat_jid, id)`, no `account_id` | `seq` rowid + unique `(account_id, chat_id, id)` |
| `type`, `text`, `push_name`, `media_id` columns | `type` + `text` kept as denormalised; body in `content` json; `sender_name`; media links from `media.message_seq` |
| `ListChats` derived with `GROUP BY` at read time | materialised `chats` row maintained on write |
| `media.path` column | derived from `sha256`; `state`, `remote_ref`, dimensions added |
| webhook POST inline from `handleMessage`, no retry | `events` append in the same tx; delivery worker reads by cursor |
| `whatsmeow.db` in `data/` | `data/accounts/<id>/whatsmeow.db` |

# Platform-Agnostic Chat Bridge API

Status: draft v1, 2026-09-12. Replaces the WhatsApp-only surface in `api.md` (no compatibility kept); §12 maps the current implementation onto it.

## 1. Purpose

One HTTP API that fronts any messaging network: WhatsApp, Telegram, Matrix, WeChat, Signal, ...
A consumer (assistant, Matrix bridge, CLI) talks to the bridge only; it never learns platform
libraries. Each platform is an **adapter** that implements the same contract and declares what it
cannot do through **capabilities**, so consumers degrade instead of guessing.

Design rules:

- Platform-native identifiers are passed through, never re-encoded. They are opaque strings to the
  consumer and are always scoped by an account.
- One message model. Platform quirks land in `content.type = "unsupported"` or in `raw`, never in
  new top-level fields.
- Read paths and event delivery are identical for every platform. Only login differs, and that is
  expressed as a generic step machine (§5).
- Everything that can fail on the network side returns a `platform_error`; the bridge never
  invents success.
- Cross-platform identity is a local overlay (§3.8). It groups and labels; it never routes.
  Sending always targets one account and one chat.

## 2. Conventions

| Item | Rule |
|---|---|
| Base path | `/v1` |
| Auth | `Authorization: Bearer <token>` on every `/v1/*` route. `/healthz` is open. |
| Encoding | JSON request and response bodies, `Content-Type: application/json`. Media upload is multipart. |
| Time | RFC 3339 UTC strings (`2026-09-12T13:05:00Z`). |
| IDs | `account_id` is bridge-assigned (`[a-z0-9_-]{1,64}`). `chat_id`, `user_id`, `message_id` are platform-native, percent-encoded when used in a path. |
| Pagination | `?cursor=&limit=` on every list. Response carries `next_cursor` (absent when exhausted). `limit` defaults to 50, max 500. Cursors are opaque. |
| Idempotency | Every write that creates a message accepts `client_id`. Replaying the same `client_id` on the same chat returns the original message with `200` instead of `201`. |
| Raw passthrough | `?raw=1` on message reads adds `raw`, the adapter's untouched platform payload. Off by default; large. |

### 2.1 Errors

```json
{"error": {"code": "unsupported", "message": "telegram cannot edit media captions", "details": {"capability": "message.edit"}}}
```

| HTTP | `code` | When |
|---|---|---|
| 400 | `invalid_request` | malformed body, bad field, unknown enum value |
| 401 | `unauthorized` | missing or wrong bearer token |
| 404 | `not_found` | account, chat, message, or media does not exist |
| 409 | `account_not_ready` | account is not `connected` (see §4.1) |
| 409 | `conflict` | login already in progress, account id taken |
| 413 | `too_large` | upload exceeds the configured cap |
| 422 | `unsupported` | platform or adapter lacks the capability; `details.capability` names it |
| 429 | `rate_limited` | bridge or platform throttling; `Retry-After` header set |
| 502 | `platform_error` | the platform rejected or timed out; `details.platform_code` when available |
| 500 | `internal` | bridge bug |

### 2.2 Configuration (reference implementation)

Three layers, later wins: built-in defaults → one optional YAML file → `CHATBRIDGE_*` environment
variables. The file is chosen by `-config <path>`, else `$CHATBRIDGE_CONFIG`, else
`./chat-bridge.yaml` when it exists (see `chat-bridge.example.yaml`). Every key `section.key`
maps to `CHATBRIDGE_<SECTION>_<KEY>`; list values are comma separated in the environment.

| Key | Env | Default | Notes |
|---|---|---|---|
| `server.addr` | `CHATBRIDGE_SERVER_ADDR` | `:8080` | listen address |
| `server.token` | `CHATBRIDGE_SERVER_TOKEN` | required | consumer bearer token, at least 16 characters |
| `server.token_file` | `CHATBRIDGE_SERVER_TOKEN_FILE` | — | read the token from a file instead (trailing whitespace trimmed) |
| `server.adapter_token` | `CHATBRIDGE_SERVER_ADAPTER_TOKEN` | — | enables `/adapter/v1` for out-of-process adapters (`adapter-protocol.md`); at least 16 characters, must differ from `server.token` |
| `server.public_url` | `CHATBRIDGE_SERVER_PUBLIC_URL` | — | externally reachable base URL, used in media URLs given to remote adapters |
| `storage.data_dir` | `CHATBRIDGE_STORAGE_DATA_DIR` | `./data` | made absolute against the working directory; created on start |
| `media.auto_download` | `CHATBRIDGE_MEDIA_AUTO_DOWNLOAD` | `image,voice,sticker` | content types fetched eagerly; others stay `remote` |
| `media.auto_download_max_mb` | `CHATBRIDGE_MEDIA_AUTO_DOWNLOAD_MAX_MB` | `16` | larger attachments stay `remote` |
| `media.max_upload_mb` | `CHATBRIDGE_MEDIA_MAX_UPLOAD_MB` | `64` | cap for `POST /accounts/{a}/media` |
| `events.retention_days` | `CHATBRIDGE_EVENTS_RETENTION_DAYS` | `7` | event log retention |
| `log.level` | `CHATBRIDGE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `persons.auto_link_by_phone` | `CHATBRIDGE_PERSONS_AUTO_LINK_BY_PHONE` | `true` | join contacts on different accounts that share a phone into one Person (§3.8) |
| `adapters.telegram.api_id` | `CHATBRIDGE_ADAPTERS_TELEGRAM_API_ID` | — | Telegram application id (my.telegram.org), inherited by every Telegram account |
| `adapters.telegram.api_hash` | `CHATBRIDGE_ADAPTERS_TELEGRAM_API_HASH` | — | Telegram application hash (secret); an account's own `config` may override both |

Data directory layout (`storage.md` §1):

```
<data_dir>/
  chatbridge.db           # accounts, chats, contacts, messages, events, media index
  media/<aa>/<sha256>     # attachment bytes, content-addressed
  accounts/<account_id>/  # adapter-private session state (whatsmeow.db, Telegram session.json, Matrix session.json)
```

Per-account platform settings (`api_id`, `homeserver`, …) are not in this file: they are passed
as `config` on `POST /accounts` and stored in the database (§4.1).

## 3. Resources

### 3.1 Account

One logged-in identity on one platform. A bridge process hosts many.

```json
{
  "id": "wa-main",
  "platform": "whatsapp",
  "adapter": "local",
  "status": "connected",
  "self": {
    "id": "8613800000000@s.whatsapp.net",
    "name": "Deyang",
    "names": {"alias": null, "profile": "Deyang", "username": null, "first": null, "last": null},
    "handle": "+8613800000000",
    "phone": "+8613800000000",
    "email": null,
    "avatar": {"media_id": "avatar_8613800000000"},
    "bio": "Hey there! I am using WhatsApp.",
    "is_self": true,
    "is_contact": true,
    "blocked": false,
    "updated_at": "2026-09-12T10:01:30Z",
    "raw": {}
  },
  "login": {"flow": "phone", "identifier": "+8613800000000", "at": "2026-09-12T10:01:30Z"},
  "capabilities": ["send.text", "send.media", "message.reply", "message.reaction", "message.delete", "chat.read", "chat.typing", "chat.resolve", "format.markdown"],
  "config": {"device_name": "bridge", "media": {"auto_download": ["image", "voice", "sticker"], "auto_download_max_mb": 16}},
  "device": {"platform_version": "2.3000.1", "device_id": "8613800000000:42@s.whatsapp.net", "session_since": "2026-09-01T08:00:00Z", "last_sync": "2026-09-12T13:05:00Z"},
  "stats": {"chats": 42, "messages": 18320, "requests_pending": 1, "last_inbound_at": "2026-09-12T13:05:00Z", "last_event_id": "0000000000012345"},
  "error": null,
  "created_at": "2026-09-12T10:00:00Z",
  "connected_at": "2026-09-12T10:01:30Z"
}
```

`status`: `unpaired` → `logging_in` → `connecting` → `connected`; `disconnected` (will retry) and
`error` (needs operator; `error` field explains) are terminal until acted on.

`platform`: `whatsapp`, `telegram`, `matrix`, `wechat`, `signal`, ... Adapters register the string.
`adapter` is the instance of that platform's adapter the account is bound to. Several adapters may
serve one platform at the same time (the built-in whatsmeow one is `local`; out-of-process ones
pick their own id, e.g. `eu-host`); every account belongs to exactly one, and all traffic for it is
routed there. When a platform has a single adapter the binding is implicit.

`self` is the account's own profile and reuses the Contact shape, so name/avatar/handle/bio
follow the same rules as everyone else; it is `null` while `unpaired`. `self.phone` is E.164 when
the platform exposes it (WhatsApp, Telegram, Signal always; WeChat when bound; Matrix only via
3PID), `self.email` likewise; `self.handle` is whichever of those the platform treats as primary.
`login` records how the session was established (`flow` from §5, the `identifier` the owner
entered — phone, username, or Matrix user id — and when) so an operator can re-pair without
guessing. `config` is what the owner
set, with secret fields (`api_hash`, tokens, passwords) replaced by `"***"`. `device` is
adapter-specific read-only detail about the session (schema published under `GET /platforms`);
`stats` are derived counts. The list endpoint omits `config`, `device`, and `stats`.

### 3.2 Capabilities

Strings an account advertises. A request that needs an absent capability fails with `422 unsupported`.

| Capability | Meaning |
|---|---|
| `send.text` | plain text out |
| `send.media` | image/video/audio/voice/file out |
| `send.location` | location content out |
| `send.contact` | contact card out |
| `message.reply` | `reply_to` honoured |
| `message.thread` | `thread_id` honoured (Matrix threads, Telegram topics) |
| `message.edit` | `PATCH` a sent message |
| `message.delete` | unsend for everyone |
| `message.reaction` | add or remove an emoji reaction |
| `message.history` | adapter can backfill history from the platform, not only from the local store |
| `chat.read` | mark-as-read reaches the platform |
| `chat.typing` | typing indicator out |
| `chat.resolve` | phone / username / alias → `chat_id` |
| `chat.create` | create group / room |
| `chat.members` | list and change participants |
| `self.update` | own profile name / bio / avatar can be changed |
| `contact.alias` | owner-set alias written back to the platform or bridge store |
| `keys.manage` | end-to-end encryption key management (§4.7a): device identity, cross-signing, key backup, export/import |
| `contact.request` | friend requests exist and can be accepted/rejected |
| `chat.invite` | group/room invites can be accepted/rejected |
| `chat.join_request` | join requests to owned groups can be approved |
| `call.reject` | incoming calls can be declined (accepting is never supported) |
| `presence` | online/offline events |
| `receipts` | delivered/read receipts in |
| `format.markdown` | bridge renders the common markdown subset (§3.5) |
| `format.html` | adapter accepts HTML directly (Matrix) |

### 3.3 Chat

```json
{
  "id": "120363012345678901@g.us",
  "account_id": "wa-main",
  "kind": "group",
  "name": "Family",
  "avatar": {"media_id": "avatar_120363012345678901"},
  "unread_count": 3,
  "last_message_at": "2026-09-12T13:05:00Z",
  "last_message": {"id": "3EB0C767D26A1D8E5C7A", "sender": {"id": "8613800000000@s.whatsapp.net", "name": "Alice"}, "from_me": false, "timestamp": "2026-09-12T13:05:00Z", "content": {"type": "text", "text": "see you at 6"}},
  "muted": false,
  "archived": false,
  "tags": ["family"],
  "person_id": null,
  "pinned_message_ids": [],
  "ephemeral_ttl_s": null,
  "participants": [{"id": "8613800000000@s.whatsapp.net", "name": "Alice", "chat_name": "Ali", "role": "admin"}],
  "raw": {}
}
```

`kind`: `direct`, `group`, `channel` (broadcast, one-way), `self` (notes-to-self / saved messages).
`participants` is present only on `GET /chats/{id}` and only when `chat.members` is available.
`participants[].name` is the resolved name (§3.7); `chat_name` is the per-chat nickname if the
platform has one (WeChat 群昵称, Matrix per-room displayname), else absent.
`ephemeral_ttl_s` is the chat-level disappearing-message timer when set. `tags` are bridge-local
labels (§3.8); `person_id` is set on `direct` chats when the counterpart is linked to a Person.

### 3.4 Message

```json
{
  "id": "3EB0C767D26A1D8E5C7A",
  "account_id": "wa-main",
  "chat_id": "8613800000000@s.whatsapp.net",
  "sender": {"id": "8613800000000@s.whatsapp.net", "name": "Alice", "chat_name": null, "person_id": "per_01J7Q0X5R2K3M4N5P6Q7R8S9T0"},
  "from_me": false,
  "timestamp": "2026-09-12T13:05:00Z",
  "content": {
    "type": "image",
    "text": "look at this",
    "attachments": [{
      "media_id": "3EB0C767D26A1D8E5C7A",
      "mime": "image/jpeg",
      "size": 183221,
      "file_name": "IMG_0123.jpg",
      "width": 1280, "height": 960,
      "duration_ms": null,
      "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
      "url": "/v1/media/3EB0C767D26A1D8E5C7A",
      "thumbnail_media_id": null,
      "state": "ready"
    }]
  },
  "reply_to": "3EB0C767D26A1D8E5C79",
  "thread_id": null,
  "mentions": ["8613800000001@s.whatsapp.net"],
  "forwarded": false,
  "ephemeral": null,
  "edited_at": null,
  "deleted_at": null,
  "reactions": [{"emoji": "👍", "sender_id": "8613800000001@s.whatsapp.net"}],
  "status": "delivered",
  "client_id": null,
  "raw": {}
}
```

`status` (outbound only): `pending`, `sent`, `delivered`, `read`, `failed`. Inbound messages omit it.
`sender.name` follows the resolution order in §3.7 and is a snapshot at receive time; look the
contact up for the current value. `ephemeral` is `{"expires_at": "2026-09-19T13:05:00Z", "view_once": false}` for
disappearing or view-once messages; the bridge keeps the row and clears attachments at expiry
(`message.updated` with `content.type: "expired"`).
A deleted message keeps its row with `deleted_at` set and `content` replaced by `{"type": "deleted"}`.
Reactions are not messages; they are a field here and an event (§6).

### 3.5 Content

Exactly one `type`. `text` is the body or caption. `format` is `plain` (default), `markdown`, or
`html` and only applies to `text`. The markdown subset every adapter must render or strip is:
`**bold**`, `_italic_`, `~~strike~~`, `` `code` ``, fenced code blocks, `- ` lists, `> ` quotes,
and `[label](url)`. Nothing else is guaranteed.

| `type` | Extra fields | Notes |
|---|---|---|
| `text` | | |
| `image`, `video`, `audio`, `voice`, `file`, `sticker` | `attachments[]` | `voice` is push-to-talk; `file` is any document. One attachment per message except `image` albums where the platform groups them. |
| `location` | `location: {lat, lon, name?, address?, live_until?}` | |
| `contact` | `contacts: [{name, phones[], emails[], vcard?}]` | |
| `poll` | `poll: {question, options[], multi, closed}` | read-only unless `send.poll` |
| `call` | `call: {kind: "voice" \| "video", state: "missed" \| "declined" \| "ended" \| "ringing", duration_s?}` | one message per call, updated as state changes |
| `payment` | `payment: {kind: "transfer" \| "red_packet" \| "invoice", amount?, currency?, note?, state}` | WeChat 转账/红包, Telegram invoices; amounts absent when the platform hides them |
| `system` | `system: {kind, actor?, targets[]?, value?}` | in-timeline notices; `kind` enum below |
| `deleted` | | body redacted by sender |
| `expired` | | disappearing / view-once message whose body is gone |
| `unsupported` | `unsupported: {platform_type}` | adapter could not map it; `raw` has the payload |

`system.kind`: `created`, `member_joined`, `member_left`, `member_added`, `member_removed`,
`role_changed`, `name_changed`, `avatar_changed`, `description_changed`, `pinned`, `unpinned`,
`ephemeral_changed` (`value` = seconds or 0), `encryption_changed`, `other` (`value` = platform
text). `actor` is who did it, `targets` who it was done to. Membership kinds also update the Chat
(`chat.updated`), so a consumer that only tracks state can ignore system messages.

Attachment:

```json
{
  "media_id": "3EB0C767D26A1D8E5C7A",
  "mime": "image/jpeg",
  "size": 183221,
  "file_name": "IMG_0123.jpg",
  "width": 1280, "height": 960,
  "duration_ms": null,
  "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
  "url": "/v1/media/3EB0C767D26A1D8E5C7A",
  "thumbnail_media_id": null,
  "state": "ready"
}
```

`state`: `ready` (bytes on disk), `pending` (adapter still downloading), `failed`, `remote`
(bridge chose not to download; `url` is absent, use `POST /media/{id}/fetch`).

### 3.6 Contact

```json
{
  "id": "8613800000000@s.whatsapp.net",
  "name": "Alice W",
  "names": {"alias": "Alice W", "profile": "alice 🌸", "username": "alicew", "first": "Alice", "last": "Wang"},
  "handle": "+8613800000000",
  "phone": "+8613800000000",
  "email": null,
  "avatar": {"media_id": "avatar_8613800000000"},
  "is_self": false,
  "is_contact": true,
  "blocked": false,
  "person_id": "per_01J7Q0X5R2K3M4N5P6Q7R8S9T0",
  "bio": "hi",
  "updated_at": "2026-09-12T13:05:00Z",
  "raw": {}
}
```

`handle` is the human identifier on that platform: phone (WhatsApp, Signal, Telegram),
`@username` (Telegram), `@user:server` (Matrix), wxid (WeChat). `phone` (E.164) and `email` are
set whenever the platform reveals them, independent of `handle`. `is_contact` says whether the
account's address book has this user; `names` is split so consumers can choose (§3.7).

### 3.7 Name resolution

Every platform has several names for one person. The bridge exposes all of them and also one
resolved `name`, so consumers that do not care get a sensible label without logic.

| `names.*` | Who sets it | WhatsApp | Telegram | Matrix | WeChat |
|---|---|---|---|---|---|---|
| `alias` | the account owner, private | address-book name (from phone sync) | contact first/last as saved | none (bridge-local only) | 备注 remark |
| `profile` | the user, public | push name | first + last | displayname | 昵称 |
| `username` | the user, public, unique | none | `@username` | localpart | 微信号 |
| `first` / `last` | the user | none | yes | none | none |
| `chat_name` (on participant / sender) | the user, per chat | none | none | per-room displayname | 群昵称 |

Resolution for `name` (contact) and `sender.name` (message): `alias` → `chat_name` (only in that
chat) → `profile` → `username` → `handle` → `id`. Empty strings count as absent.

`alias` is writable when the account has `contact.alias` (WeChat, Telegram, Matrix via bridge-local
store): `PATCH /accounts/{a}/contacts/{user} {"alias": "Alice W"}`. Bridge-local aliases survive
re-login and are marked `names.alias_source: "local"`.

Any change to a contact's names or avatar emits `contact.updated`. Because names change, the bridge
stores `sender.name` on each message as seen at the time and does not rewrite history.

### 3.8 Person (local identity overlay)

Platform ids are unique only inside an account. The same human on WhatsApp, Telegram, and Matrix
is three unrelated Contacts. A **Person** is a bridge-local record that groups them so a consumer
can classify, filter, and read across platforms. It is never sent to any platform and never
changes how messages are delivered: a reply still goes to the account and chat the message came
from.

```json
{
  "id": "per_01J7Q0X5R2K3M4N5P6Q7R8S9T0",
  "name": "Alice Wang",
  "tags": ["work", "vendor"],
  "notes": "PM at Acme, prefers Telegram",
  "links": [
    {"account_id": "wa-main", "user_id": "8613800000000@s.whatsapp.net", "source": "manual", "linked_at": "2026-09-12T13:05:00Z"},
    {"account_id": "tg-main", "user_id": "123456789", "source": "phone", "linked_at": "2026-09-12T13:05:00Z"}
  ],
  "channels": [
    {"account_id": "wa-main", "user_id": "8613800000000@s.whatsapp.net", "chat_id": "8613800000000@s.whatsapp.net", "name": "Alice", "last_message_at": "2026-09-12T13:05:00Z"},
    {"account_id": "tg-main", "user_id": "123456789", "chat_id": "123456789", "name": "alicew", "last_message_at": "2026-09-10T09:00:00Z"}
  ],
  "created_at": "2026-09-12T13:05:00Z",
  "updated_at": "2026-09-12T13:05:00Z"
}
```

- `links` are the Contacts that make up the person; each Contact belongs to at most one Person.
  Each link also carries the contact's current `platform`, resolved `name`, `handle` and `phone`
  for display.
  `source` is `manual` or `phone` (auto-linked because two contacts on different accounts share an
  E.164 `phone`; controlled by bridge config `persons.auto_link_by_phone`, default `true`).
  Automatic links can be removed like manual ones and are not re-created for that pair: removing
  a contact records it as split from every contact that stays in the person. Auto-linking runs
  whenever a contact row changes: other accounts' non-self contacts with the same phone (digits
  only, at least seven) are looked up; if exactly one Person is among them the contact joins it,
  if none is, a Person named after the contact is created with all of them, if several are,
  nothing happens and `/persons/suggest` lists them.
- `channels` is derived: the direct chat on each linked account, if one exists, with the platform
  name and last activity, so a consumer choosing where to reply picks one explicitly.
- `name` is a local label; it does **not** enter the `sender.name` resolution (§3.7), which stays
  per platform. Consumers that want the person view read `person_id`.
- `tags` are free-form strings, bridge-local, also available on Chat for groups and channels that
  have no person. `GET /persons?tag=` and `GET /accounts/{a}/chats?tag=` filter on them.
- Deleting a Person unlinks its contacts; contacts and messages are untouched.

## 4. Endpoints

All paths below are relative to `/v1`. `{a}` is an `account_id`.

### 4.1 Accounts

| Method | Path | Body | Result |
|---|---|---|---|
| GET | `/accounts` | | `{accounts: [Account]}` |
| POST | `/accounts` | `{id, platform, adapter?, config?}` | 201 Account in `unpaired`; `adapter` is required when the platform has more than one connected adapter (`400` lists them). With an explicit `adapter` the account may be created before that adapter connects (status `error` / `adapter_offline` until it does). |
| GET | `/accounts/{a}` | | Account |
| DELETE | `/accounts/{a}` | | logs out, deletes local state; 204 |
| PATCH | `/accounts/{a}` | `{config?, adapter?}` | Account; secret fields may be omitted to keep the old value. Connection-affecting keys take effect on next `reconnect`. `adapter` moves an `unpaired` account to another instance of the same platform (`409` while logged in). |
| PATCH | `/accounts/{a}/self` | `{name?, bio?, avatar_media_id?}` | Account; needs `self.update` |
| POST | `/accounts/{a}/logout` | | Account in `unpaired` |
| POST | `/accounts/{a}/reconnect` | | Account; forces a reconnect from `disconnected`/`error` |
| GET | `/platforms` | | `{platforms: [{id, name, capabilities, login_flows, instances: [{id, name, version, remote, capabilities}]}]}` — one entry per platform, `instances` lists every connected adapter of it |

`config` is adapter-specific; its JSON Schema is published under `GET /platforms`. Keys of the
built-in adapters:

| Platform | Required | Optional | Login flows |
|---|---|---|---|
| `whatsapp` | — | `device_name` | `qr`, `phone` (pairing code) |
| `telegram` | — (`api_id` / `api_hash` come from `adapters.telegram.*`; either may be overridden per account) | `device_name` | `phone` (code, then 2FA password if enabled), `qr` (also asks for the 2FA password) |
| `matrix` | `homeserver` (base URL) | `device_name` | `password` (user + password), `token` (user + access token). Rooms are end-to-end encrypted transparently; see §4.7a |
| `signal` | — | `network`: mautrix-signal connector settings as a YAML string or an object (`device_name`, `displayname_template`, `sync_contacts_on_startup`, …; defaults from the connector's example config) | `qr` (link chat-bridge as a secondary device: scan the `sgnl://linkdevice` URI). Only present in builds with `-tags signal`; see `adapter-protocol.md` §11 |

### 4.2 Login (see §5)

| Method | Path | Body | Result |
|---|---|---|---|
| POST | `/accounts/{a}/login` | `{flow}` | LoginStep |
| GET | `/accounts/{a}/login` | | current LoginStep (refreshes rotating QR) |
| POST | `/accounts/{a}/login/submit` | `{fields: {...}}` | next LoginStep |
| DELETE | `/accounts/{a}/login` | | cancel; 204 |

### 4.3 Chats

| Method | Path | Body / Query | Result |
|---|---|---|---|
| GET | `/accounts/{a}/chats` | `cursor, limit, kind?, archived?, tag?, person?` | `{chats: [Chat], next_cursor}` by recent activity |
| GET | `/accounts/{a}/chats/{chat}` | | Chat with `participants` |
| POST | `/accounts/{a}/chats/resolve` | `{handle}` | `{chat_id, kind, user_id?}`; needs `chat.resolve` |
| POST | `/accounts/{a}/chats` | `{kind: "group", name, members[]}` | 201 Chat; needs `chat.create` |
| POST | `/accounts/{a}/chats/{chat}/read` | `{up_to?: message_id}` | 204; needs `chat.read` |
| POST | `/accounts/{a}/chats/{chat}/typing` | `{state: "typing" \| "paused"}` | 204; needs `chat.typing` |
| PATCH | `/accounts/{a}/chats/{chat}` | `{muted?, archived?, name?, tags?}` | Chat; `tags` is bridge-local, the rest reach the platform |

`resolve` is how a consumer sends to a phone number or username without knowing platform ID
formats: `{"handle": "+8613800000000"}` → `{"chat_id": "8613800000000@s.whatsapp.net", "kind": "direct"}`.

### 4.4 Messages

| Method | Path | Body / Query | Result |
|---|---|---|---|
| GET | `/accounts/{a}/chats/{chat}/messages` | `cursor, limit, before?, after?, backfill=1?` | `{messages: [Message], next_cursor}` newest first |
| POST | `/accounts/{a}/chats/{chat}/messages` | SendRequest | 201 Message (`status: pending` or `sent`) |
| GET | `/accounts/{a}/messages/{msg}` | `raw=1?` | Message |
| PATCH | `/accounts/{a}/messages/{msg}` | `{content: {type: "text", text}}` | Message; needs `message.edit` |
| DELETE | `/accounts/{a}/messages/{msg}` | | Message with `deleted_at`; needs `message.delete` |
| PUT | `/accounts/{a}/messages/{msg}/reactions/{emoji}` | | 204; needs `message.reaction` |
| DELETE | `/accounts/{a}/messages/{msg}/reactions/{emoji}` | | 204 |
| GET | `/accounts/{a}/messages/search` | `q, chat?, cursor, limit` | `{messages, next_cursor}`; local store only |

`backfill=1` asks the adapter to pull older history from the platform when the local page is
short: the missing rows are fetched (older than the page's oldest row, or than the cursor row),
stored as history (no unread count, no auto-download), and the page is re-read. `next_cursor` is
returned while the platform reports more, even when the local store is exhausted. Needs
`message.history`, otherwise silently ignored. `search` matches every whitespace-separated term
against message text (substring, case-insensitive); results are newest first.

SendRequest:

```json
{
  "client_id": "7f0d3a2e-…",
  "content": {
    "type": "image",
    "text": "caption",
    "format": "markdown",
    "attachments": [{"media_id": "upl_9f8e…"}]
  },
  "reply_to": "3EB0C767D26A1D8E5C79",
  "thread_id": null,
  "mentions": ["8613800000001@s.whatsapp.net"]
}
```

`content` uses the same shape as §3.5; attachments reference a previously uploaded `media_id`.
Only `text`, media types, `location`, and `contact` are sendable.

### 4.5 Media

| Method | Path | Body / Query | Result |
|---|---|---|---|
| POST | `/accounts/{a}/media` | multipart `file`, optional `kind` | 201 Attachment with `state: ready`, `media_id` prefixed `upl_`. Unreferenced uploads expire after 1 h. |
| GET | `/media/{id}` | | bytes, original `Content-Type`, `Content-Disposition`; supports `Range` |
| GET | `/media/{id}/meta` | | Attachment |
| POST | `/media/{id}/fetch` | | starts download for `state: remote`; returns Attachment `pending` |

Media IDs are global (not account-scoped) because the consumer already got them from a message.

### 4.6 Contacts

| Method | Path | Query | Result |
|---|---|---|---|
| GET | `/accounts/{a}/contacts` | `cursor, limit, q?` | `{contacts: [Contact], next_cursor}` |
| GET | `/accounts/{a}/contacts/{user}` | | Contact |
| PATCH | `/accounts/{a}/contacts/{user}` | `{alias?, blocked?}` | Contact; `alias` needs `contact.alias` |

### 4.7a Keys (end-to-end encryption)

Only platforms with client-side encryption expose these (Matrix today; capability `keys.manage`,
others answer `422 unsupported`). Messages in encrypted rooms are decrypted and encrypted by the
bridge without any call here; these endpoints establish trust and durability of the keys.

| Method | Path | Body | Result |
|---|---|---|---|
| GET | `/accounts/{a}/keys` | | `{device_id, fingerprint, cross_signed, backup: {version, enabled}, sessions}` |
| POST | `/accounts/{a}/keys/verify` | `{recovery_key}` | `{cross_signed, backup_version, sessions_imported}` — cross-signs this device with the account's recovery key (other clients then show it as verified) and restores the server-side key backup so history from before the login decrypts. The recovery key is used once and not stored. |
| POST | `/accounts/{a}/keys/export` | `{passphrase}` | encrypted key file (`application/octet-stream`, Element-compatible) |
| POST | `/accounts/{a}/keys/import` | multipart `file`, `passphrase` | `{sessions_imported}` |

An event that cannot be decrypted is stored as `content.type: unsupported` with
`platform_type: m.room.encrypted` plus a `platform.event` (`decrypt_failed`) carrying the reason;
once the key arrives (backup restore, key request answered) new events decrypt normally.

### 4.7 Persons

Not account-scoped. See §3.8.

| Method | Path | Body / Query | Result |
|---|---|---|---|
| GET | `/persons` | `cursor, limit, tag?, q?` | `{persons: [Person], next_cursor}`; `q` matches name, notes, linked contact names and handles |
| POST | `/persons` | `{name?, tags?, notes?, links: [{account_id, user_id}]}` | 201 Person; a contact already linked elsewhere → `409 conflict` |
| GET | `/persons/{p}` | | Person |
| PATCH | `/persons/{p}` | `{name?, tags?, notes?}` | Person |
| DELETE | `/persons/{p}` | | unlinks all; 204 |
| POST | `/persons/{p}/links` | `{account_id, user_id}` | Person |
| DELETE | `/persons/{p}/links/{a}/{user}` | | Person |
| POST | `/persons/{p}/merge` | `{from: ["per_…"]}` | Person; links and tags moved, sources deleted |
| GET | `/persons/{p}/chats` | | `{chats: [Chat]}` — direct chats across accounts, same as `channels` but full objects |
| GET | `/persons/{p}/messages` | `cursor, limit, scope=direct\|all` | `{messages, next_cursor}` merged by timestamp across accounts; `direct` = their direct chats, `all` = also their messages in groups |
| GET | `/persons/suggest` | | `{suggestions: [{contacts: [{account_id, user_id, name, phone}], reason: "phone"}]}` — unlinked contacts that share a phone, for a UI to confirm when auto-link is off |

`GET /events` and `GET /events/stream` accept `person=` to filter to that person's own
`person.updated` events and to events on a linked account whose sender, user, chat, or subject id is
a linked contact (`404` for an unknown person). `person_id` is omitted on Contact, Chat and sender
when the contact is not linked. Deleting or merging emits `person.updated` with
`{"id": "per_…", "deleted": true}` (plus `"merged_into"` for merges) and `contact.updated` for every
contact whose `person_id` changed.

### 4.8 Requests

Anything the platform is waiting on the owner to answer: friend requests, group/room invites, join
requests to owned groups, incoming calls. They are not messages because they have a lifecycle and
an answer.

| Method | Path | Body / Query | Result |
|---|---|---|---|
| GET | `/accounts/{a}/requests` | `cursor, limit, kind?, state?` | `{requests: [Request], next_cursor}` |
| GET | `/accounts/{a}/requests/{id}` | | Request |
| POST | `/accounts/{a}/requests/{id}/accept` | `{reason?}` | Request `accepted`; `409` unless `pending`, `400` for calls |
| POST | `/accounts/{a}/requests/{id}/reject` | `{reason?}` | Request `rejected`; `409` unless `pending`. For a call this hangs up on every device of the account, which is why calls do not offer it |
| POST | `/accounts/{a}/requests/{id}/ignore` | | Request `ignored`; `409` unless `pending`. Local only: the platform is not contacted, so other devices keep ringing or keep the invite |

```json
{
  "id": "req_01J…",
  "account_id": "wx-main",
  "kind": "contact_request",
  "state": "pending",
  "from": {"id": "wxid_abc", "name": "Bob"},
  "chat": null,
  "message": "Hi, I'm Bob from the meetup",
  "call": null,
  "actions": ["accept", "reject", "ignore"],
  "created_at": "2026-09-12T13:05:00Z",
  "expires_at": "2026-09-19T13:05:00Z",
  "answered_at": null,
  "raw": {}
}
```

`kind`: `contact_request`, `chat_invite` (`chat` set, `from` is the inviter), `join_request`
(`chat` is the owned group, `from` the applicant), `call` (`chat` is the direct chat; only
`reject` is valid, and it auto-expires when the call ends). `state`: `pending`, `accepted`, `rejected`,
`ignored`, `expired`. Accepting a `chat_invite` yields a `chat.new` event.

`actions` lists the answers a client should offer: `["ignore"]` for calls,
`["accept", "reject", "ignore"]` for other pending requests, `[]` once answered, ignored or
expired. Ignoring never reaches the platform and needs no connected account; it only takes the
request off the bridge's pending list. `call` is `{kind: "voice" | "video"}` on
call requests. The platform can resolve a request on its own (answered on the phone, call hung
up), which emits `request.updated` with the new state. Pending requests past `expires_at` become
`expired`. Answering a request needs the account connected (`409 account_not_ready`) and an
adapter that answers requests (`422 unsupported` otherwise). `GET …/requests` filters with
`kind` and `state`.

| Platform | Kinds | accept | reject (ignore is always local) |
|---|---|---|---|
| WhatsApp | `call` (incoming voice call) | — | rejects the call |
| WhatsApp | `chat_invite` (group invite message) | joins the group | dismisses locally (WhatsApp has no decline) |
| Telegram | `call` | — | discards the call as busy |
| Telegram | `join_request` (to a group or channel you own) | approves | dismisses |
| Matrix | `chat_invite` (room invite) | joins the room | leaves (declines) |
| Matrix | `call` (`m.call.invite`) | — | sends `m.call.reject` (`m.call.hangup` for VoIP v0) |
| Signal (hosted) | none | | |

### 4.9 Events (see §6)

| Method | Path | Query | Result |
|---|---|---|---|
| GET | `/events` | `cursor, limit, account?, types?, wait?` | `{events: [Event], next_cursor}`; `wait` seconds long-polls when empty (max 60) |
| GET | `/events/stream` | `cursor?, account?, types?` | `text/event-stream`; honours `Last-Event-ID` |
| GET | `/webhooks` / `POST` / `DELETE /webhooks/{id}` | `{url, secret, account?, types?}` | Webhook subscriptions |

### 4.10 Meta

| Method | Path | Result |
|---|---|---|
| GET | `/healthz` | `{"status":"ok"}`, no auth |
| GET | `/v1/status` | `{version, uptime_s, accounts: [{id, platform, status}], events_cursor}` |

## 5. Login flows

Login is the only part of the contract that differs per platform, so it is modelled as a step
machine driven by the adapter. The consumer never hardcodes "QR" or "OTP"; it renders whatever step
it gets.

```
POST /accounts/{a}/login {"flow":"qr"}         -> step display(qr)
GET  /accounts/{a}/login                       -> step display(qr)   (new code after rotation)
...user scans...
GET  /accounts/{a}/login                       -> step done
```

```
POST /accounts/{a}/login {"flow":"phone"}      -> step input(phone)
POST /accounts/{a}/login/submit {"fields":{"phone":"+8613800000000"}}  -> step display(code "XXXX-XXXX")   (WhatsApp)
                                                                       -> step input(otp)                   (Telegram)
POST /accounts/{a}/login/submit {"fields":{"otp":"12345"}}             -> step input(password) | done
```

LoginStep:

```json
{"flow": "phone", "step": "input", "input": {"fields": [{"name": "otp", "type": "code", "label": "SMS code", "pattern": "^[0-9]{5}$"}]}}
{"flow": "qr", "step": "display", "display": {"type": "qr", "data": "2@AbCdEf…,base64pubkey==,base64adv==", "expires_at": "2026-09-12T10:01:50Z"}}
{"flow": "phone", "step": "display", "display": {"type": "code", "data": "ABCD-EFGH", "expires_at": "2026-09-12T10:04:30Z"}}
{"flow": "sso", "step": "display", "display": {"type": "url", "data": "https://matrix.example.org/_matrix/client/v3/login/sso/redirect?redirectUrl=…", "expires_at": "2026-09-12T10:11:30Z"}}
{"flow": "phone", "step": "done", "self": {"id": "8613800000000@s.whatsapp.net", "name": "Deyang", "handle": "+8613800000000", "phone": "+8613800000000"}}
{"flow": "phone", "step": "failed", "error": {"code": "platform_error", "message": "invalid code"}}
```

Field `type`: `text`, `phone`, `password`, `code`, `url`. Display `type`: `qr`, `code`, `url`, `text`.

Known flows per platform:

| Platform | Flows |
|---|---|
| whatsapp | `qr`, `phone` (pairing code) |
| telegram | `phone` (OTP, optional 2FA password), `qr` |
| matrix | `password` (user + password), `sso` (URL, then token), `token` (paste an access token) |
| wechat | `qr` |
| signal | `qr` (link device) |

`done.self` is the full Contact (§3.6); the example is abbreviated. The same object is then
available as `Account.self`, and `Account.login` records the flow and identifier used.

Every step transition also emits an `account.login_step` event so a UI can subscribe instead of poll.

## 6. Events

Envelope:

```json
{
  "id": "0000000000012345",
  "type": "message.new",
  "account_id": "wa-main",
  "timestamp": "2026-09-12T13:05:01Z",
  "data": { }
}
```

`id` is a monotonically increasing cursor across all accounts; consumers persist the last one they
handled and resume with `?cursor=`. The bridge keeps at least 7 days of events.

| `type` | `data` |
|---|---|
| `message.new` | Message. Includes `from_me: true` messages sent from another device of the same account. Messages sent through this API are also emitted, with `client_id` set, so a consumer can dedupe. |
| `message.updated` | Message (after edit, status change, or attachment `state` change) |
| `message.deleted` | `{chat_id, message_id, deleted_at}` |
| `message.reaction` | `{chat_id, message_id, sender_id, emoji, removed: bool}` |
| `message.receipt` | `{chat_id, message_ids[], user_id, kind: "delivered" \| "read"}` |
| `chat.new` | Chat |
| `chat.updated` | Chat (name, avatar, membership, mute/archive) |
| `chat.typing` | `{chat_id, user_id, state}` |
| `presence` | `{user_id, state: "online" \| "offline", last_seen?}` |
| `contact.updated` | Contact (names, avatar, blocked, is_contact, person_id) |
| `person.updated` | Person (created, renamed, tagged, links changed; `data.deleted: true` on delete) |
| `request.new` | Request |
| `request.updated` | Request (state changed, including by the owner on another device) |
| `account.status` | Account. `error.code` distinguishes `logged_out_remotely`, `banned`, `session_expired`, `network` so a consumer knows whether to re-login or just wait. |
| `account.login_step` | LoginStep |
| `platform.event` | `{platform_type, chat_id?, user_id?, message_id?, raw}` |

### 6.1 What becomes what

Rule of thumb for an adapter meeting a platform event it has to place:

1. Shows up in the chat timeline for a human → a Message. Human-authored → normal content type;
   platform-generated → `content.type: system` (or `call` / `payment`).
2. Changes state the consumer may hold (chat name, members, contact name, pin) → the matching
   `*.updated` event with the full object, in addition to any timeline message.
3. Needs an answer from the owner → a Request (§4.8) plus `request.new`.
4. Ephemeral and not worth storing (typing, presence, receipts) → dedicated event, not logged to
   the message store.
5. Nothing above fits → `platform.event`. Never drop silently. These are excluded from `/events`
   unless the consumer asks with `types=platform.event`, and they are retained for 24 h only.

Live-location updates and poll votes are `message.updated` on the original message with the new
`location` / `poll` payload.

### 6.2 Webhooks

`POST <url>` with body `{"events": [Event, ...]}` (batched, ordered, max 100). Headers:
`X-ChatBridge-Delivery: <uuid>`, `X-ChatBridge-Signature: sha256=<hex HMAC of body with secret>`,
`X-ChatBridge-Cursor: <last event id in batch>`. Non-2xx or timeout (10 s) retries with exponential
backoff for 24 h, then the subscription is paused (`GET /webhooks` shows `paused_at`). Events are
never dropped from the log; a paused consumer catches up with `GET /events?cursor=`.

### 6.3 SSE

`GET /v1/events/stream` emits `id: <cursor>` and `event: <type>` per event, `data:` is the
envelope. A `: keepalive` comment every 15 s.

## 7. Per-platform mapping

| Concept | WhatsApp | Telegram | Matrix | Signal | WeChat |
|---|---|---|---|---|---|
| `chat_id` | JID (`…@s.whatsapp.net`, `…@g.us`) | numeric peer id (`-100…` for supergroups) | room id `!x:server` | peer ACI UUID (direct), group identifier (base64) | wxid / `…@chatroom` |
| `user_id` | JID | numeric user id | `@user:server` | ACI UUID (`PNI:<uuid>` before the ACI is known) | wxid |
| `message_id` | WA message id | `chat_id:msg_id` | event id `$…` | `<sender uuid>|<timestamp ms>` | msg id |
| `handle` | `+phone` | `+phone` or `@username` | `@user:server` | `+phone` (when the contact shares it) | wxid |
| `kind: channel` | broadcast / newsletter | channel | none (use room with read-only power levels) | none (announcement groups are groups) | official account |
| `thread_id` | none | forum topic id | thread root event id | none | none |
| edit | no | yes | yes (`m.replace`) | yes | no |
| delete | yes (revoke, time-limited) | yes | yes (redaction) | yes (remote delete) | yes (recall, 2 min) |
| reactions | yes | yes | yes (`m.reaction`) | yes | no |
| `format` | limited markdown | markdown subset ↔ entities (bold, italic, strike, code, pre, links, mentions) | HTML | HTML (bold, italic, strike, monospace, spoiler) | plain only |
| receipts | delivered + read | read (private only) | read | delivered + read | none |
| history backfill | no (on-device only) | yes | yes | no (a linked device only receives new traffic) | no |
| end-to-end encryption | always (protocol) | secret chats not supported | yes (megolm; `keys.manage`) | always (protocol; keys live in `bridgev2.db`) | always (protocol) |
| owner alias | phone address book (read-only) | contact name (read/write) | bridge-local | phone contacts synced on connect (read-only) | 备注 (read/write) |
| per-chat nickname | none | none | room displayname | none | 群昵称 |
| contact request | none (anyone can message) | none | none | message requests (accepted in the Signal app) | yes (must accept) |
| chat invite | added directly, or invite link | invite link / added | `m.room.member invite` | added directly, or group link | invited, must confirm for >N members |
| incoming call | event; reject only | event; reject only | `m.call.*`; reject only | not bridged | event; reject only |
| payment content | none | invoice / Stars | none | none (payments not bridged) | 转账 / 红包 |

WeChat has no supported client protocol; any adapter there is best-effort and must advertise a
narrow capability list.

## 8. Multi-account & routing

A single bridge process may host several accounts across platforms. Every chat, message, and
contact carries `account_id`; the event log is shared and filterable by `account`. Nothing in the
API assumes one account or one platform per process.

## 9. Storage expectations

Full design in `storage.md`. The contract the API relies on:

- Messages, chats, contacts, requests, media metadata, and the event log are persisted locally, so
  every read works while the platform is disconnected.
- State changes and their events are written in one transaction; an event never references
  unreadable state and no state change lacks its event.
- Media bytes are content-addressed by SHA-256, stored once, and may be purged by retention; a
  purged attachment reports `state: remote` and can be refetched.
- History beyond what the adapter delivers is not promised; `message.history` says whether more
  can be pulled.

## 10. Security

- One bearer token per consumer; tokens may be scoped to accounts (`config.token_accounts`).
- Webhook bodies are HMAC-signed; consumers must verify.
- Message bodies are never logged at `info` or above.
- Media is served only to bearer-authenticated callers; no public URLs.

## 11. Adding a platform

The consumer API never changes when a platform is added. A platform is an adapter that speaks the
adapter protocol (`adapter-protocol.md`): in-process Go (whatsmeow, gotd, mautrix-go), a hosted
mautrix bridgev2 network connector (Signal today; `adapter-protocol.md` §11), or any language over
a WebSocket JSON-RPC connection. The adapter declares capabilities, login flows, and a config
schema at connect time, and the core publishes them under `GET /platforms`. Nothing in this
document is specific to a language or a process boundary.

## 12. Migration from the current WhatsApp API

| Current | New |
|---|---|
| `GET /v1/status` | `GET /v1/accounts/{a}` (+ `GET /v1/status` for process-level) |
| `POST /v1/pair/code {phone}` | `POST /v1/accounts/{a}/login {flow:"phone"}` then `/login/submit {fields:{phone}}` |
| QR in `/v1/status.qr_code` | `POST /v1/accounts/{a}/login {flow:"qr"}` then `GET /v1/accounts/{a}/login` |
| `POST /v1/logout` | `POST /v1/accounts/{a}/logout` |
| `POST /v1/messages/text {to,text}` | `POST /v1/accounts/{a}/chats/resolve {handle}` once, then `POST …/chats/{chat}/messages {content:{type:"text",text}}` |
| `POST /v1/messages/media` multipart | `POST /v1/accounts/{a}/media` then `POST …/chats/{chat}/messages` with `attachments[{media_id}]` |
| `GET /v1/messages?chat=` | `GET /v1/accounts/{a}/chats/{chat}/messages` |
| `GET /v1/messages/{id}` | `GET /v1/accounts/{a}/messages/{id}` |
| `GET /v1/chats` | `GET /v1/accounts/{a}/chats` |
| `GET /v1/media/{id}` | unchanged; `?meta=1` becomes `/media/{id}/meta` |
| `CHATBRIDGE_WEBHOOK_URL` | `POST /v1/webhooks` (or keep the env var as a bootstrap subscription) |
| message `chat_jid`, `sender_jid`, `push_name`, `type`, `text`, `media_id` | `chat_id`, `sender.id`, `sender.name`, `content.type`, `content.text`, `content.attachments[0].media_id` |
| message `type: reaction` / `protocol` | reaction → `message.reaction` event; protocol → `content.type: system` or `unsupported` |

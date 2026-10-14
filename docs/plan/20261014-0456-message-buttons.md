# 20261014-0456-message-buttons Inline buttons on sent messages, and button presses as events

- **status**: completed
- **createdAt**: 2026-10-14 04:56
- **approvedAt**: 2026-10-14 05:10
- **relatedTask**: 20261014-0446-message-buttons

## Context

- **Capabilities are per adapter, not per account.** `adapter.Info().Capabilities` is what
  `GET /accounts/{a}` shows (`core/accounts.go` `accountView`) and what every check uses
  (`ad.Info().Has(...)` and `requireCap` in `core/chats.go`, `messages.go`, `accounts.go`,
  `keys.go`; about 15 sites). A Telegram bot account and a user account share one adapter, so a
  bot needs per-account capabilities.
- **Telegram adapter** (`internal/adapters/telegram`, gotd v0.161.0): login flows `phone` and
  `qr`; `online()` starts `updates.Manager.Run` without `IsBot` and calls `loadDialogs`
  (`messages.getDialogs`, not allowed for bots). The core calls `ListContacts` on connect
  (`contacts.getContacts`, not allowed). gotd has `Auth().Bot(token)`,
  `updates.AuthOptions{IsBot}`, `Builder.Markup(...)` (also carried by `Edit(id)` into
  `messages.editMessage`), `UpdateBotCallbackQuery` and `MessagesSetBotCallbackAnswer`.
- **Messages.** `Content` is stored as JSON (`messages.content`), so a new field needs no
  migration. `EditMessage(ctx, id, chatID, msgID, content)` is the edit contract; the core only
  accepts `type: text` edits. Inbound edits arrive as `message_update` carrying the full
  `Content`, which replaces the stored one.
- **Events.** `sink.ingest` maps adapter events to log events; typing/presence go to the log
  only, like the press will. A scoped token sees an event type only if it is listed in
  `store.scopeByChatID` / `scopeByChat`.
- **Remote adapters** send `content` as `model.Content` JSON in `message.send` / `message.edit`
  (`remote/adapter.go`), so `buttons` travels without wire changes; the press needs a new event
  kind and the answer a new core → adapter method.
- **Housekeeping found during investigation:** commit `81555ba` left `docs/plan/index.md` empty
  and added an empty `docs/plan/20261003-1745-tokens-web-ui.md`. The index is restored from
  `cbd7283` with the three October plans appended; the empty file is removed (that task carried
  its acceptance in the task file).

## Proposal

### 1. Model and adapter contract

```go
// model
type Button struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}
// Content gains: Buttons [][]Button `json:"buttons,omitempty"`
const EvMessageButton = "message.button"

// adapter
const CapButtons = "message.buttons"
const EvButton = "button"          // Event gains Data, PressID
type AccountCapabilities interface { // optional: per-account override of Info().Capabilities
	AccountCapabilities(id string) []string // nil = use Info()
}
type PressAnswerer interface {
	AnswerPress(ctx context.Context, id, pressID, text string, alert bool) error
}
```

Core gets one helper, `c.can(ad, accountID, capability)`, used by every existing check and by
`accountView`, replacing `ad.Info().Has` / `requireCap`. Behavior is unchanged for adapters that
do not implement `AccountCapabilities`.

### 2. Send and edit (core)

- Validation for `content.buttons`: allowed on `text`, `image`, `video`, `audio`, `file`
  (others → `400 invalid_input`); every row non-empty; each button has non-empty `text` and
  exactly one of `data` (≤ 64 bytes) or `url` (`http`, `https` or `tg` scheme). Platform limits
  beyond that (buttons per row, total) come back from the adapter as `invalid_input`.
- Buttons without `message.buttons` on the account → `422 unsupported`.
- `PATCH /accounts/{a}/messages/{msg}` body becomes `{content?, buttons?}`; at least one is
  required. `buttons` absent leaves the keyboard, `[]` removes it, a layout replaces it. A text
  change needs `message.edit`, `buttons` needs `message.buttons`. The core passes the adapter the
  full resulting content (stored text when only buttons change, stored buttons when only text
  changes), because Telegram drops the keyboard on an edit without `reply_markup`. Edits of media
  messages are allowed when only `buttons` is given (caption untouched). The stored message and
  `message.updated` carry the new keyboard; `edited_at` is set only when the text changed.

### 3. Presses (core)

- `EvButton` → log event `message.button`
  `{chat_id, message_id, user_id, sender: {id, name}, data, press_id, pressed_at}`; the sender
  hint is upserted as a contact like a message sender, and its stored name is used. Nothing goes
  to the message store. Added to `scopeByChatID`.
- An in-memory press table keyed by `(account, press_id)`: chat id, state
  (`pending|answered|expired`). A 10 s timer calls `AnswerPress(…, "", false)` and marks it
  expired; finished entries are dropped after 10 min.
- `POST /accounts/{a}/presses/{press_id}/answer` `{text?, alert?}`: `allowWrite` (read-only →
  `403`), `allowChat` on the press's chat (else `404`), unknown press → `404`, answered → `409
  press_answered`, expired → `409 press_expired`, account not connected → `409
  account_not_ready`. Open to scoped tokens; the allowlist test gains the route.
- Presses do not survive a restart: a press from before a restart answers `404`; the
  10 s window makes this rare and the client spinner stops on its own.

### 4. Telegram bot accounts (gotd adapter)

- New login flow `bot`: one secret field `token`; `Auth().Bot(ctx, token)`, then `online()`.
- The account knows it is a bot from `self.Bot` (also on restart, after `Auth().Status`), plus
  a marker file `bot` in the account dir so capabilities are right before the first connect.
- For bots: updates manager with `IsBot: true`; no `loadDialogs`; `ListContacts` returns the
  empty list without an API call; `ResolveChat` only resolves `@username` (phones →
  `invalid_target`); no `Backfill`, `MarkRead`, `CreateChat`, `UpdateSelf`, call/join-request
  handling.
- Bot capabilities: `send.text`, `send.media`, `send.location`, `send.contact`, `message.reply`,
  `message.edit`, `message.delete`, `message.reaction`, `chat.typing`, `chat.resolve`,
  `format.markdown`, `message.buttons`. Not: `message.history`, `chat.read`, `chat.members`,
  `chat.create`, `presence`, `receipts`, `self.update`. User accounts keep today's set (no
  `message.buttons`).
- Send/edit: `content.buttons` → `tg.ReplyInlineMarkup` (`KeyboardButtonCallback` /
  `KeyboardButtonURL`) through `Builder.Markup`; `[]` on edit → no `reply_markup`, which removes the keyboard (an empty inline markup is rejected by Telegram).
- Inbound: `m.ReplyMarkup` of type `ReplyInlineMarkup` → `content.buttons` (callback and URL
  buttons; other kinds skipped). This also makes our own edits echo back correctly.
- `UpdateBotCallbackQuery` → `EvButton` with `press_id` = query id, `message_id` =
  `<chat>:<msg>`, `data` = the bytes as a string, sender hint from the update entities.
  `UpdateInlineBotCallbackQuery` (inline-mode messages, no chat) is ignored.
- `AnswerPress` → `messages.setBotCallbackAnswer{query_id, message, alert}`.

### 5. Remote adapters (`docs/adapter-protocol.md`, `internal/adapters/remote`)

- `content.buttons` documented in `message.send` / `message.edit` (no code change: content is
  passed through).
- `events` gains kind `button` (`chat_id, message_id, user_id, sender?, data, press_id, at`).
- New core → adapter method `press.answer {account_id, press_id, text, alert}`; the remote
  adapter implements `PressAnswerer`. Per-account capabilities are not added to the wire: a
  remote adapter declares `message.buttons` for all its accounts.

### 6. Web UI

- `message-item.tsx` renders `content.buttons` as a read-only grid under the bubble: `url`
  buttons are links (`target="_blank" rel="noopener noreferrer"`), `data` buttons are disabled.
- Types (`Button`, `Content.buttons`), the event log shows `message.button` (type filter list,
  if any), query invalidation ignores it. Vitest for the rendering.

### 7. Docs

`docs/api.md` §3.2 (capability), §3.4/§3.5 (`buttons`), §4.4 (send/PATCH), new §4.x Presses
(including the multi-consumer rule: the owner of the message answers, others ignore), §6 event
table, §7 platform table (Telegram bot row), §5 login flow `bot`; `docs/adapter-protocol.md`;
README capability list; changelog.

### Tests (RED first)

- `core`/`server` with the fake adapter (gains `AccountCapabilities`, `AnswerPress`, buttons):
  send with buttons (stored, returned), validation errors, `422` without the capability; PATCH
  buttons-only / `[]` / text-only keeps keyboard, `message.updated` emitted; press event payload
  and two independent cursors; answer once → `409 press_answered`; timer → auto-answer and `409
  press_expired` (timer duration injectable for the test); scoped token sees presses only in
  scope and answers only there; read-only token → `403`; allowlist test updated.
- `store`: `message.button` filtered by chat for a scope.
- `telegram`: unit tests for markup ↔ `[][]Button` conversion, callback-query → event
  conversion, bot capability set.
- Web: Vitest for buttons in `message-item`.

### Real-platform verification

Needs a bot token from @BotFather and a Telegram client to tap buttons, which I cannot do:
I will log in with the token you provide (through the API; the token is not printed), send to a
group the bot is in, and ask you to tap; then verify events, answers, the 10 s auto-answer,
keyboard edits, and grep the log for `BOT_METHOD_INVALID`. WhatsApp/Matrix/Signal `422` is
covered by tests (none declares the capability).

## Risks

- Changing every capability check to the per-account helper touches many call sites; it is
  mechanical and existing tests cover the checks.
- Bot accounts cannot see chats until a message arrives or they send to one; a bot cannot DM a
  user who has not started it (`invalid_target` / platform error). Documented.
- In groups with privacy mode on, the bot receives only commands, replies to it and presses.
  Enough for the BKD flow; documented.
- `data` from other bots may not be UTF-8; it is exposed as a string with invalid bytes
  replaced. Only inbound display is affected.
- Press state is in memory (see §3).

## Scope

`internal/model`, `internal/adapter` (+ `fake`), `internal/core` (capability helper, messages,
sink, new `presses.go`), `internal/store/events.go`, `internal/server` (route, PATCH body,
tests), `internal/adapters/telegram`, `internal/adapters/remote`, `web/src` (types, message
item, events), `docs/api.md`, `docs/adapter-protocol.md`, README, changelog. No schema
migration. Roughly 25 files.

## Alternatives

- **Press as a Request with a lifecycle** (`requests` table): durable and listable, but presses
  are frequent, short-lived and meaningless to the bridge; the event plus answer route matches
  the Telegram model and costs no storage. Chosen: event.
- **Matrix fallback** (numbered text plus reactions): consumers own their fallback; not done.
- **Adapter-side 10 s timer**: each adapter would re-implement it; the core timer keeps one
  behavior for in-process and remote adapters. Chosen: core.
- **Separate `EditButtons` adapter method** instead of full content to `EditMessage`: two code
  paths in every adapter; passing the full resulting content keeps one.
- **Persisting presses** to answer `409` after a restart: not worth a table for a 10 s window.

## Annotations

- Approved with "开始处理" (2026-10-14).

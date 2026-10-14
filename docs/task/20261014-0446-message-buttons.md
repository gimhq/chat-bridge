# 20261014-0446-message-buttons Inline buttons on sent messages, and button presses as events

- **status**: completed
- **priority**: P1
- **owner**: worker-a/session-ci-01
- **createdAt**: 2026-10-14 04:46

## Description

A consumer can send text and media, but cannot ask a question the recipient answers with one tap.
The first consumer that needs this is BKD (`bkhq/bkd`), a kanban board for AI coding agents: when
an agent needs a human decision, BKD posts the question with one button per option, the user taps
one, confirms, and BKD records the answer. Today BKD does this by talking to the Telegram Bot API
directly, which allows only one process to receive a bot's updates — so several BKD instances
cannot share one bot. Routing through chat-bridge removes that limit, because the event log lets
every consumer read every event with its own cursor; each BKD then claims only presses on messages
it sent itself. Without buttons, BKD has to fall back to "reply with A or B".

Add inline buttons to the message model, a button-press event, an answer route for presses, and
implement them for Telegram bot accounts. Other platforms do not advertise the capability and
consumers degrade, as for every other capability.

### Scope

1. **Telegram bot login (local gotd adapter).** A `bot` login flow that takes a bot token
   (`auth.importBotAuthorization`, gotd `Auth().Bot`). Buttons need a bot: a user account cannot
   attach an inline keyboard. A bot account advertises only what the Bot API side of MTProto
   allows; methods bots may not call (dialog list, contact list, history backfill, …) must not be
   attempted, and the matching capabilities (`message.history`, `chat.resolve` by phone, …) are
   absent. Chats a bot account knows are built from incoming updates and from chats it was sent
   to. The `tgbridge` instance's existing `bot` flow does not need to gain buttons in this task.

2. **Model and capability.**
   - Capability `message.buttons`: the account can attach inline buttons and reports presses.
   - `content.buttons` on SendRequest and Message: rows of buttons,
     `[[{"text": "A", "data": "d:ev1:0"}, {"text": "Docs", "url": "https://…"}], …]`. A button has
     `text` and exactly one of `data` (opaque to the bridge, at most 64 bytes, delivered back on
     press) or `url` (opened by the client, no event). Allowed on `text` content and on media
     captions. Sending buttons to an account without the capability answers `422 unsupported`.
   - Inbound messages that carry buttons (another bot's keyboard) expose them read-only in
     `content.buttons`, with `data` present so a consumer could show them; pressing them on
     behalf of the account is out of scope.

3. **Editing buttons.** `PATCH /accounts/{a}/messages/{msg}` accepts `buttons` next to
   `content`: a new layout replaces the keyboard, `[]` removes it, absent leaves it unchanged.
   Allowed with or without a text change (Telegram `messages.editMessage` with `reply_markup`).
   Needs `message.buttons`; changing text still needs `message.edit`. The stored Message and
   `message.updated` reflect the new keyboard.

4. **Press event.** `message.button` with
   `{"chat_id", "message_id", "user_id", "sender": {id, name}, "data", "press_id", "pressed_at"}`.
   It goes into the event log like every other event (so `/events`, SSE, webhooks and per-consumer
   cursors all see it; retained like the rest of the log) but not into the message store. Presses
   on `url` buttons do not exist on the wire. A press on a message the bridge does not know still
   produces the event, with the ids as received.

5. **Answering a press.** `POST /accounts/{a}/presses/{press_id}/answer` with
   `{"text"?: string, "alert"?: bool}` (Telegram `messages.setBotCallbackAnswer`). One answer per
   press: a second call answers `409`. If no consumer answers within 10 s, the bridge answers
   with no text so the user's client stops its spinner, and a later answer call gets `409`
   (`press_expired`). With several consumers reading the same chat, the one that owns the
   message answers; the rest must ignore the press — the spec says so.

6. **Tokens.** A scoped token sees `message.button` only for chats in its scope, and may answer
   presses only there. A `read_only` token sees presses but cannot answer them and cannot send
   or edit buttons (`403`).

7. **Out-of-process adapters.** `docs/adapter-protocol.md` gains the same fields: `buttons` in
   `message.send` / `message.edit`, a press notification from adapter to core, and an
   `press.answer` call from core to adapter, so a remote adapter can implement the capability.

8. **Web UI.** The message timeline renders buttons under a message (read-only; tapping a `url`
   button opens it), and the event log shows `message.button`. Sending buttons from the UI is not
   needed.

### Acceptance

- A Telegram account logged in with the `bot` flow reaches `connected`, advertises
  `message.buttons`, and does not call methods bots are not allowed to call (no
  `BOT_METHOD_INVALID` in the log after connect, a send, and a press).
- Sending a text message with two rows of `data` buttons and one `url` button to a group the bot
  is in shows the keyboard in the Telegram client; the returned Message carries `content.buttons`.
- Tapping a `data` button produces exactly one `message.button` event with the original `data`,
  the right `message_id`, `user_id` and `sender`; it is readable by two consumers with separate
  cursors.
- Answering the press shows the text (toast, or alert with `alert: true`) in the client; a second
  answer gets `409`; an unanswered press is answered automatically after 10 s and its spinner
  stops.
- `PATCH` with `buttons: []` removes the keyboard; with a new layout replaces it; both emit
  `message.updated`.
- Sending buttons on a WhatsApp, Matrix or Signal account answers `422 unsupported`.
- Scoped and read-only tokens behave as in scope item 6, covered by server tests and by the route
  allowlist test.
- `docs/api.md` (§3.2, §3.4/§3.5, §4.4, a new §4.x for presses, §6 event table, §7 platform table)
  and `docs/adapter-protocol.md` describe all of the above.
- Go and web UI quality gates pass.

## ActiveForm

Adding inline buttons and button-press events

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Full tier (public API, auth scope, adapter contract). Plan: `docs/plan/20261014-0456-message-buttons.md`.

Requested on 2026-10-14 by the BKD side (decision events answered from chat, shared by several
BKD instances). How BKD will use it, so the contract fits:

- BKD sends one message per open decision with one `data` button per option plus `Close`, and
  stores the returned `message_id`. `data` is `<action>:<eventId>:<optionIndex>:<optionsHash>`,
  well under 64 bytes.
- It reads `/events?types=message.button,message.new&wait=30` with its own cursor, handles a press
  only when `message_id` is one it stored, answers it, and edits the message's buttons (confirm
  step, then `buttons: []` once decided). Presses on other instances' messages are ignored and
  left for their owner to answer.
- Replies (`message.new` with `reply_to` on a stored message) remain the way to answer in free
  text; nothing new is needed for that.

Points to settle in the plan:

- Telegram bots in groups only receive messages addressed to them unless privacy mode is off;
  replies to the bot's own messages and button presses are always delivered, which is all BKD
  needs. A bot cannot open a direct chat; the user has to start it first.
- Whether `message.button` should also be offered as a Request-like object with a lifecycle
  instead of an event plus an answer route. The proposal above keeps it an event because presses
  are frequent, short-lived and have no meaning to the bridge.
- Whether Matrix should map buttons onto a fallback (numbered text plus reactions). Proposed: no;
  consumers own their fallback.
- progress (2026-10-14): implemented; Go gate (lint, tests, `tgbridge`), race run, `docker build --target test` and web gates pass. Open: the real-platform acceptance with a bot (keyboard in the client, press, answer, 10 s auto-answer, no `BOT_METHOD_INVALID`) needs a bot token and a person to tap.
- progress (2026-10-14): real-bot acceptance in a test group: `bot` login reaches `connected` with the bot capability set (also after a restart); send with three data rows and a URL row returns `content.buttons`; each tap gives one `message.button` with the right data, ids and sender; immediate answer 204 then `409 press_answered`; unanswered presses auto-answered after 10 s, then `409 press_expired`; keyboard replace, text-only edit keeping the keyboard, and `[]` removal each emit `message.updated`; no `BOT_METHOD_INVALID`, no WARN/ERROR. Pending: the user's confirmation of what the client showed (alert text, spinner stop, keyboard changes).
- progress (2026-10-14): second real-bot run, watcher armed before sending, all server-side checks pass: press A answered at once (204, then `409 press_answered`), press B auto-answered after 10 s (`409 press_expired`), replace / text-only edit / removal each emit `message.updated` (3), no `BOT_METHOD_INVALID`, no WARN/ERROR.
- progress (2026-10-14): the auto-answer timeout is configurable (`events.press_timeout_s`, default 10 s, 1–60); timed on the real bot: answer 10.1 s after the press at 10 s, 3.1 s at 3 s; the user confirmed the client behavior.

- complete: Go gate, race run, docker test stage and web gates pass; real-bot acceptance passed and confirmed by the user.

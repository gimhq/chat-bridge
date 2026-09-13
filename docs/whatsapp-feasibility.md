# WhatsApp Bridge for a Personal Work Assistant: Feasibility and Plan

Date: 2026-09-12. Status: proposal, nothing built yet.

## 1. Goal

A self-hosted bridge that lets the owner talk to a Claude-backed assistant from
WhatsApp: ask questions, summarize, draft replies, set reminders, and later reach
work tools (mail, calendar, files) through MCP. Single user. No public bot.

## 2. Feasibility: the two ways in

There are only two ways to put a program on WhatsApp. Both work technically; each
carries a different, non-technical risk.

### 2a. Official WhatsApp Business Platform (Cloud API)

How it works: a dedicated business phone number owned by a Meta Business account.
Messages arrive by webhook, replies go out through the Graph API. The owner would
message their own business number from their personal WhatsApp.

Findings (verified 2026-09-12):

- Meta prohibits general-purpose LLM chatbots on the Business Platform. New
  accounts since 2025-10-15, all accounts since 2026-01-15. The wording targets
  "open-domain assistants where the AI is the core product". A private
  single-user assistant is exactly that shape, even if nobody else can reach it.
  Enforcement against a one-user bot is unknown, but the account would be in
  breach from day one.
- Service messages (free-form replies inside the 24-hour window) are free until
  2026-09-30. From 2026-10-01 Meta bills every one per message at utility rates.
  Meta's own pricing page states this. So a chatty assistant becomes a metered
  cost the month after launch.
- Requires Meta Business verification, a number not registered on consumer
  WhatsApp, and a public HTTPS webhook.

Verdict: policy-non-compliant for this use case and soon metered. Not
recommended.

### 2b. Linked-device protocol (unofficial libraries)

How it works: the bridge pairs as a "linked device" to a real WhatsApp account,
the same way WhatsApp Web does. Two mature open-source implementations:

| Library | Language | Activity (2026-09) | Notes |
|---|---|---|---|
| whatsmeow (`go.mau.fi/whatsmeow`) | Go | commits 2026-09-09, 7.3k stars | Powers mautrix-whatsapp and Beeper. Requires Go 1.26. No release tags; consumed by commit. |
| Baileys (`@whiskeysockets/baileys`) | TypeScript | last commit 2026-08-04, v7.0.0-rc14 (2026-07-29), 11k stars | Runs on Bun/Node. QR and pairing-code login. Still a release candidate. |

Findings:

- Both violate WhatsApp's Terms of Service. The maintainers say so. There is no
  appeal path if a number is banned.
- Meta ran detection waves against unofficial clients through 2025 and 2026.
  Documented bans hit low-volume, reply-only accounts as well as spammers.
  Reported signals: datacenter or VPS IP ranges, robotic timing, reconnect
  churn, proactive messaging to non-contacts, low reply ratio, user reports.
- Reply-only usage on a number that only ever talks to the owner minimizes every
  one of those signals except the IP one.
- Existing projects prove the shape works: `dsebastien/whatsapp-claude-agent`
  (Baileys + Claude Agent SDK, TypeScript, whitelist by phone number) and
  several similar bridges.

Verdict: technically straightforward, one-evening prototype. The real risk is
losing the paired number.

### 2c. Decision

Use the linked-device route with these hard rules:

1. Pair a dedicated secondary number, never the primary work number. If it gets
   banned, nothing of value is lost.
2. Reply-only. The bridge never initiates a chat; reminders go only to the owner
   who has already messaged it.
3. Whitelist the owner's number. Everyone else is ignored silently.
4. Run it from a home or office connection when possible, not a VPS. If it must
   run on the `station` host, accept the elevated risk and keep rule 1.
5. Keep the session alive; avoid restart loops that reconnect every few seconds.

Revisit the official API only if Meta later carves out an exception for private
assistants.

## 3. Architecture (decided 2026-09-12)

The bridge is a standalone Go service built on whatsmeow. It owns the WhatsApp
session, stores every message and attachment, and exposes one HTTP API. The AI
assistant is a separate consumer of that API, so the WhatsApp side can stay
stable while the assistant logic iterates.

```
WhatsApp <-> whatsmeow <-> bridge (Go) <-> HTTP API + webhook <-> assistant (any language)
                              |
                         SQLite + media/
```

- Language: Go 1.26 (whatsmeow's floor; the local `go` auto-downloads it).
- WhatsApp: `go.mau.fi/whatsmeow`, pinned by commit in `go.mod`.
- HTTP: stdlib `net/http` + Chi. Bearer token on every `/v1` route.
- Storage: two SQLite files under `storage.data_dir`: `whatsmeow.db` (device
  keys, managed by whatsmeow) and `chatbridge.db` (messages, media index).
  Attachments are downloaded on receipt to `media/<message-id>`.
- Config: koanf, defaults then `CHATBRIDGE_*` env, validated at startup.
- Layout follows the local Go baseline: `cmd/chat-bridge`, `internal/{core,store,server,adapter,adapters/whatsapp}` (see `adapter-protocol.md`).
- Build and run: Docker only. Multi-stage `Dockerfile` (golang:1.26-alpine
  builder, distroless static nonroot runtime, `--target test` runs the unit
  tests) plus `compose.yaml` with `./data` mounted at `/data`.
- API reference: `docs/api.md`.

## 4. Scope by phase

### Phase 1: bridge core (done 2026-09-12)

- Pair by QR (exposed through `GET /v1/status`) or by phone pairing code.
- Persist device state; whatsmeow reconnects on drop.
- Store every inbound and outbound message with type, text or caption, sender,
  and push name. Download image, video, audio, document, and sticker
  attachments and index them.
- Send text and media (image, video, audio, voice note, document).
- Read API: chats, messages by chat with cursor pagination, single message,
  media bytes or metadata.
- Webhook POST per stored inbound message.
- Verified: unit tests on config, store, handlers, and message classification
  (coverage 81 to 84 percent on those packages); lint clean; live run connects
  to WhatsApp and produces a QR code. Real send and receive still needs a
  paired number.

### Phase 2: bridge hardening (1 to 2 days)

- Pair a dedicated number and run an end-to-end test: text, image, document,
  voice note in both directions.
- History sync: persist the backlog WhatsApp pushes after pairing.
- Reply-to (quoted messages) and reactions on send.
- Webhook retry with backoff; contact and group name resolution for chat lists.
- Runbook: tmux service, backup of `storage.data_dir`, re-pair procedure.

### Phase 3: assistant on top of the API (2 to 3 days)

- A separate process (Bun or Go) subscribes to the webhook, applies the owner
  whitelist, and answers through `POST /v1/messages/text`.
- Claude via the Anthropic SDK, model `claude-opus-5`, cached system prompt,
  per-chat history read back from `GET /v1/messages`.
- Images and PDFs fetched from `GET /v1/media/{id}` and passed to Claude as
  image or document blocks. Voice notes transcribed first.
- Reminders and web search as in the original plan.

### Phase 4: work tools (open-ended)

- MCP servers for mail, calendar, and files. Gmail and Google Drive connectors
  exist in this environment but are not authorized yet.
- Every tool that writes or sends requires an explicit confirmation reply from
  the owner before execution.

## 5. Cost

Anthropic API list prices, Claude Opus 5: $5 per million input tokens, $25 per
million output tokens. Cached system prompt reads are cheaper.

| Usage | Approx. monthly API cost |
|---|---|
| 30 turns/day, 3k in / 500 out per turn | about $50 |
| 100 turns/day, same shape | about $170 |

WhatsApp side costs nothing on the linked-device route. A second SIM or eSIM
for the dedicated number is the only fixed cost.

## 6. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Dedicated number banned | Medium | Rules in 2c. Keep a pairing runbook so a new number is live in 10 minutes. |
| Baileys protocol break after a WhatsApp update | Medium | Pin the version, watch the repo, keep whatsmeow as fallback. |
| Prompt injection via forwarded messages | Low now, higher in Phase 3 | Whitelist plus confirmation gate on write tools. |
| Secrets in chat logs | Low | Store history locally only; never log message bodies at info level. |

## 7. Open questions for the owner

1. Is there a spare number (SIM or eSIM) to dedicate to this?
2. Where will it run: home network, office, or the `station` host?
3. Which work tools matter first for Phase 3: mail, calendar, files, or a
   ticketing system?

## Sources

- Meta non-template message pricing: https://developers.facebook.com/documentation/business-messaging/whatsapp/pricing/non-template-messages
- General-purpose chatbot policy explainer: https://respond.io/blog/whatsapp-general-purpose-chatbots-ban
- Same policy, Alibaba Cloud summary: https://www.alibabacloud.com/help/en/chatapp/use-cases/whatsapp-ai-policy-2026-guide
- Unofficial client ban signals: https://achiya-automation.com/en/blog/whatsapp-spam-detection-2026/
- whatsmeow: https://github.com/tulir/whatsmeow
- Baileys: https://github.com/WhiskeySockets/Baileys
- Reference bridge: https://github.com/dsebastien/whatsapp-claude-agent

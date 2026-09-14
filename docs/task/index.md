# chat-bridge - Task List

> Updated: 2026-09-13

## Usage

Each task is a single line linking to its detail file. All detailed information lives in `docs/task/<timestamp>-<feature-slug>.md`.

### Format

- [ ] [**20260907-1428-add-endpoint Add endpoint**](20260907-1428-add-endpoint.md) `P1`

### Status Markers

| Marker | Meaning |
|--------|---------|
| `[ ]`  | Pending |
| `[-]`  | In progress |
| `[x]`  | Completed |
| `[~]`  | Closed / Won't do |
| `[d]`  | Deleted detail file; index entry retained |

### Priority: P0 (blocking) > P1 (high) > P2 (medium) > P3 (low)

### Rules

- Only update the checkbox marker; never delete the line or change its other content. If the detail file is deleted, mark the entry `[d]`.
- Record change history and deletion reasons in `docs/changelog.md`; update affected dependency and plan references.
- New tasks append to the end.
- See each `<timestamp>-<feature-slug>.md` for full details, except `[d]` entries whose files have been deleted; consult `docs/changelog.md` for their history.

---

## Tasks
- [x] [**20260913-0151-telegram-matrix-hardening Harden the Telegram and Matrix adapters**](20260913-0151-telegram-matrix-hardening.md) `P1`
- [x] [**20260913-0252-bridgev2-host-signal Host mautrix bridgev2 network connectors; spike with Signal**](20260913-0252-bridgev2-host-signal.md) `P2`
- [-] [**20260913-0530-api-framework-completion Complete the bridge API framework against api.md**](20260913-0530-api-framework-completion.md) `P1`
- [x] [**20260913-1903-web-ui-and-requests Embedded web UI; requests (invites, calls)**](20260913-1903-web-ui-and-requests.md) `P1`
- [x] [**20260913-2034-ignore-call-requests Ignore calls instead of rejecting them**](20260913-2034-ignore-call-requests.md) `P1`
- [x] [**20260913-2234-release-workflow Publish on GitHub; release binaries from a workflow**](20260913-2234-release-workflow.md) `P2`
- [~] [**20260913-2243-telegram-default-app Built-in Telegram application credentials**](20260913-2243-telegram-default-app.md) `P2`
- [x] [**20260913-2243-whatsapp-lid-and-new-types WhatsApp: resolve LIDs to phone numbers; map newer message types**](20260913-2243-whatsapp-lid-and-new-types.md) `P1`
- [x] [**20260913-2319-telegram-bridgev2-connector Host mautrix-telegram's bridgev2 connector alongside the gotd adapter**](20260913-2319-telegram-bridgev2-connector.md) `P2`
- [x] [**20260914-0206-contact-detail Contact detail page: everything about one contact**](20260914-0206-contact-detail.md) `P1`
- [x] [**20260914-0209-schema-reset Reset the database schema to one base version**](20260914-0209-schema-reset.md) `P2`

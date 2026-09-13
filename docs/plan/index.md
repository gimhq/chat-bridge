# chat-bridge - Plan Index

> Updated: 2026-09-13

## Usage

Each plan is a single line linking to its detail file. All detailed information lives in `docs/plan/<timestamp>-<feature-slug>.md`.

### Format

- [ ] [**20260907-1440-add-endpoint Add endpoint**](20260907-1440-add-endpoint.md) `YYYY-MM-DD`

### Status Markers

| Marker | Meaning |
|--------|---------|
| `[ ]`  | Draft / Pending review |
| `[-]`  | Approved / Implementing |
| `[x]`  | Completed |
| `[~]`  | Rejected / Abandoned |
| `[d]`  | Deleted detail file; index entry retained |

### Rules

- Only update the checkbox marker; never delete the line or change its other content. If the detail file is deleted, mark the entry `[d]`.
- Record change history and deletion reasons in `docs/changelog.md`; update affected task and plan references.
- New plans append to the end.
- See each `<timestamp>-<feature-slug>.md` for full details, except `[d]` entries whose files have been deleted; consult `docs/changelog.md` for their history.

---

## Plans
- [x] [**20260913-0151-telegram-matrix-hardening Harden the Telegram and Matrix adapters**](20260913-0151-telegram-matrix-hardening.md) `2026-09-13`
- [x] [**20260913-0252-bridgev2-host-signal Host mautrix bridgev2 network connectors; spike with Signal**](20260913-0252-bridgev2-host-signal.md) `2026-09-13`

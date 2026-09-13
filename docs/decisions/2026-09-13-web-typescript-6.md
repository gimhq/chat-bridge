# Web UI pins TypeScript 6.0

- **date**: 2026-09-13
- **scope**: `web/` (the embedded management UI)
- **sunset**: when typescript-eslint supports TypeScript 7 (tracking issue typescript-eslint#10940); review by 2027-03-31

## Decision

`/pma-web` defaults to TypeScript 7. The UI pins `typescript` to the latest 6.0 release instead.

## Why

`@antfu/eslint-config` runs typescript-eslint 8.70, which loads the TypeScript compiler API and
refuses to start on TypeScript 7 (`typescript-eslint does not support TS 7.0`, peer range
`>=4.8.4 <6.1.0`). TypeScript 7 does not expose the programmatic API yet. The baseline allows a
TypeScript 6 pin for projects whose tooling embeds TypeScript, with the reason recorded. A
side-by-side install (TS 7 for `tsc`, TS 6 for the linter) was not chosen because both packages
ship a `tsc` binary and the lint gate is the only consumer that needs the old API.

## Revisit

Upgrade to TypeScript 7 and delete this record once typescript-eslint accepts it; `bun run lint`
and `bun run typecheck` must both pass afterwards.

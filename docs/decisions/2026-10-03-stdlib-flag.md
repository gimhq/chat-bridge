# Standard-library flag instead of Cobra

- **date**: 2026-10-03
- **scope**: `cmd/chat-bridge`
- **sunset**: when the binary gains a second subcommand

## Decision

`/pma-go` defaults to Cobra with pflag for command-line parsing. chat-bridge parses its one flag,
`-config`, with the standard `flag` package, and has no flag layer in the configuration
(defaults, then the YAML file, then `CHATBRIDGE_*` environment variables).

## Why

The binary has one mode, running the server, and every setting is already reachable through the
file or the environment, which is how the container image is configured. A command framework
would add a dependency and a flag layer with nothing to put in them.

## Revisit

Adopt Cobra when a second subcommand appears (for example an offline maintenance command), and
add flags as the fourth configuration layer at the same time.

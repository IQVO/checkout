# ADR 0005: A read-only MCP server

## Status

Accepted (2026-10-25).

## Context

The fleet exposes bounded contexts to AI clients through the Model Context Protocol as an
additive inbound adapter (`.claude/rules/fleet/no-auth-and-mcp.md`): the official Go SDK,
Streamable HTTP only, a separate `cmd/mcp` binary, nothing in the domain or application
depending on it (`TestMCPAdapterDependencyRule`). A checkout session moves money.

## Decision

- `cmd/mcp` serves ONE tool, `get_checkout_session`, over the same `GetSession` use case
  the REST adapter uses (`internal/adapters/inbound/mcp`). It is read-only (annotated
  `ReadOnlyHint`) and the binary wires only the Postgres session repository: no outbox, no
  relay, no policy, no Kafka, no sibling gateway. Opening and confirming a session stay on
  REST because they authorize and capture a shopper's money.
- Errors keep the REST slugs (`not-found`); anything else is logged and reported as a
  generic `internal-error` so infrastructure details never reach the model.
- No authentication, consistent with the fleet decision of 2026-09-11. The view carries
  references only (`shopperId`, `shipToAddressId`) and never the PSP token.

## Consequences

- A second deployable that shares the database of `cmd/api` and runs the idempotent
  embedded migrations on start, like every MCP binary of the fleet.
- A write tool would need its own ADR first.

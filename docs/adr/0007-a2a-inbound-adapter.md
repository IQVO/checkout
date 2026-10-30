# ADR 0007: A2A inbound adapter

## Status

Accepted (2026-10-30).

## Context

The fleet gives every bounded context three transports with no overlap: Kafka and
CloudEvents carry FACTS (ADR 0004), MCP carries an agent's TOOLS (ADR 0005), and the
Agent2Agent protocol (A2A) carries the DELEGATION of a goal between agents. A2A is never a
data-sync channel. This context already exposes a read-only MCP surface (ADR 0005): one
tool, `get_checkout_session`. Opening and confirming a session authorize and capture a
shopper's money and stay on REST only.

## Decision

- `cmd/a2a` is a third deployable beside `cmd/api` and `cmd/mcp`, over the SAME `GetSession`
  use case (`internal/adapters/inbound/a2a`, built on `a2aproject/a2a-go/v2` v2.7.0). It is a
  message-only agent (no Task lifecycle): the skill is a transactional read.
- ONE curated, goal-shaped skill, `checkout_session_status` ("where does a checkout session
  stand"), derived from the MCP tool `get_checkout_session`. **Write gate:** a skill may
  change state only if the MCP surface already offers that write. The MCP charter is
  read-only, so this agent is read-only. It never widens what the context exposes: no
  open-session, no confirm. No write use case, no outbox, no relay, no policy, no Kafka, no
  sibling gateway is wired (`TestA2AAdapterIsReadOnlyAndHasNoOutboundClient`, an allowlist
  sensor over the adapter and `cmd/a2a`). There is no outbound A2A client either.
- Input contract: `{"skill":"<id>","arguments":{...}}` as a Data part (or a Text part holding
  the same JSON). Decoding is strict: unknown fields and trailing data are refused.
- Domain rejections and bad requests are ANSWERED in-band as RFC 7807-shaped `problem`s in the
  message envelope, never as JSON-RPC errors, with the REST adapter's classification: 404
  `not-found` (unknown, malformed or over-long `sessionId`), 400 `invalid-argument` /
  `unknown-skill` / `malformed-skill-request`, 500 `internal-error` (logged with the trace
  id; the detail text is adapter-owned so infrastructure details never reach the caller). The
  409, 422, 502 and 503 classes of the REST adapter belong to writes and sibling calls this
  agent does not make.
- The answer carries references only (`shopperId`, `shipToAddressId`), never the PSP token.
- Each skill run is an OTel span `a2a.skill <id>` with the attribute `a2a.skill`, like
  `mcp.tool <name>`.
- Endpoints: `A2A_ADDR` (default `:8093`); JSON-RPC at BOTH `/` and `/a2a`; the Agent Card at
  `/.well-known/agent-card.json`; `GET /healthz`. `A2A_AGENT_URL` is mandatory: the absolute
  http(s) URL advertised on the card, refused at boot otherwise (the chart defaults it to the
  in-cluster Service DNS and `a2a.agentUrl` overrides it).
- No authentication, consistent with the fleet decision of 2026-09-11 (ADR 0005): the card
  declares no security schemes on purpose. Real Postgres only; the binary runs the idempotent
  embedded migrations at boot like `cmd/mcp` and has no in-memory mode.
- Chart: `a2a-deployment/service/route.yaml`, `a2a:` values block (default OFF, route a
  separate switch), `checkout.a2aFullname`; the gateway path is `/api/a2a/checkout`, prefix
  rewritten to `/`. Dockerfile: `cmd/*` is built by loop, `/app/a2a` ships in the image.

## Consequences

- A third deployable that shares the database of `cmd/api`. It never writes, so it cannot
  put anything in the outbox; the CloudEvents of this context still leave only from `cmd/api`.
- A write skill (for example confirming a session) needs its own ADR and an MCP write first.
- `TestA2AAdapterDependencyRule` keeps the adapter additive: only application and domain
  below it, nothing depends on it. Re-adopting auth is a user decision plus an ADR.
- Related: [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md),
  [ADR 0005](./0005-mcp-server.md).

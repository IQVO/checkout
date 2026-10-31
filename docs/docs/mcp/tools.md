---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
description: The Model Context Protocol server of Checkout - transport, endpoints and the one read-only tool, captured from a live tools/list.
---

# MCP tools

`cmd/mcp` is a read-only Model Context Protocol server
([ADR 0005](/docs/adr/0005-mcp-server)). It is a second driving adapter over
the SAME `GetSession` use case the REST adapter uses
(`internal/adapters/inbound/mcp`). Opening and confirming a session move a
shopper's money and stay on REST, so no write verb is reachable over MCP: the
binary is wired from the read repository only (`inbound/mcp.NewDeps`) and
`TestMCPAdapterDependencyRule` keeps the adapter off every outbound adapter.

| Property | Value |
| --- | --- |
| Transport | Streamable HTTP only (official `modelcontextprotocol/go-sdk`) |
| Endpoints | `POST /mcp` and `POST /` (both mounted), `GET /healthz` |
| Port | `:8090` (`MCP_ADDR`) |
| Server info | name `checkout-mcp`, version `1.0.0`, protocol `2025-06-18` accepted in the captured handshake |
| Gateway | `/api/mcp/checkout`, rewritten to `/mcp` (when `mcp.route.enabled=true`); the local cluster leaves the component off |
| Authentication | none, fleet-wide |
| Dependencies | Postgres only; no Kafka, no sibling, no outbox, no relay |

## Tools

One tool, read-only. A live `tools/list` against `cmd/mcp` built from this
branch returned exactly one entry:

| Tool | Input | Annotations | Description |
| --- | --- | --- | --- |
| `get_checkout_session` | `sessionId` (string, required): the checkout session id, for example `sess-3f9c1a2b4d5e`; 1 to 128 characters | `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false` | Read a checkout session by id: its state, the cart and shopper references, the numbered lines and shipment groups, the binding total and currency, when the price quote expires, the payment authorization id, the placed order id, the re-quoted offers of a `REQUOTE_REQUIRED` session and the failure code of a `FAILED` one. `PLACING` means the place-order outcome is unknown. |

The tool takes no other field and `additionalProperties` is false. There is no
write tool, no list tool and no search tool.

### Output

The result carries both `content[0].text` (the JSON as text) and
`structuredContent` with this shape (from the live `outputSchema`):

| Field | Type | Present |
| --- | --- | --- |
| `sessionId`, `state`, `cartId`, `shopperId`, `shipToAddressId`, `priceQuoteId`, `currency` | string | always |
| `cartVersion`, `totalMinor`, `version` | integer | always |
| `quoteExpiresAt` | string, RFC 3339 UTC | always |
| `lines[]` | `{lineNo, catalogItemId, quantity}` | always |
| `groups[]` | `{groupNo, offerId, tier, lineNos[], amountMinor}` | always |
| `authorizationId`, `orderId`, `failureCode` | string | only when set |
| `requotedOffers[]` | `{offerId, groupNo, tier, deliverByEarliest, deliverByLatest, expiresAt}` | only on `REQUOTE_REQUIRED` |

### Real calls

`tools/call` with `{"name":"get_checkout_session","arguments":{"sessionId":"sess-3b5fcbd5535a"}}`
returned (`structuredContent`):

```json
{"authorizationId":"auth-1","cartId":"cart-demo","cartVersion":3,"currency":"USD","groups":[{"amountMinor":4400,"groupNo":1,"lineNos":[1,2],"offerId":"offer-1","tier":"NEXT_DAY"}],"lines":[{"catalogItemId":"sku-100","lineNo":1,"quantity":2},{"catalogItemId":"sku-200","lineNo":2,"quantity":1}],"orderId":"ord-1","priceQuoteId":"quote-1","quoteExpiresAt":"2026-10-31T14:58:39Z","sessionId":"sess-3b5fcbd5535a","shipToAddressId":"addr-1","shopperId":"shopper-1","state":"COMPLETED","totalMinor":4400,"version":4}
```

An unknown (or over-long) session id is a tool-level error, not a transport
failure:

```json
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"not-found: checkout session sess-nope: not found"}],"isError":true}}
```

The error text is `<slug>: <detail>` with the same slug vocabulary as the REST
problem types; anything unexpected is logged and reported as
`internal-error: an unexpected internal error occurred` so infrastructure
details never reach the model.

### Reproducing the capture

```bash
MCP_ADDR=127.0.0.1:18090 DATABASE_URL='postgres://...' ./mcp
curl -si localhost:18090/mcp -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"docs","version":"1"}}}'
```

The `initialize` answer carries an `Mcp-Session-Id` header and capabilities
`{"logging":{},"tools":{"listChanged":true}}`; send it back on every later
request, after a `notifications/initialized` notification, then `tools/list` and
`tools/call`. Responses arrive as `text/event-stream` (`event: message`). The
server's `instructions` string tells a client that nothing here changes data and
that a session in `PLACING` means the outcome is unknown and a resolver is
replaying it.

## A2A, the sibling surface

`cmd/a2a` exposes the same read as one skill, `checkout_session_status`; see the
[API overview](/docs/api-reference#endpoints) and
[ADR 0007](/docs/adr/0007-a2a-inbound-adapter).

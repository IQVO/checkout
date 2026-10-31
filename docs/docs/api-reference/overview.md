---
id: overview
title: API overview
sidebar_label: API overview
slug: /api-reference
sidebar_position: 1
description: The interfaces of Checkout - REST, events, MCP and A2A - with the endpoint table and the contract conventions.
---

# API overview

:::info Implemented by cmd/api, cmd/mcp and cmd/a2a
`cmd/api` serves the REST routes below (`internal/adapters/inbound/http`) on
`HTTP_ADDR` (default `:8080`). The contracts are `apis/openapi.yaml` (OpenAPI
3.0.3, version 0.1.0) and `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0),
pinned by [ADR 0001](/docs/adr/0001-checkout-bounded-context) and
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue). The router
registers exactly the three session routes plus `/healthz` and `/readyz`; the
spec lists the three session routes and not the two probes. See the
[Quickstart](/docs/overview/quickstart) for real responses.
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. Server named in the spec: `http://localhost:8080`; through the cluster gateway the route is `/api/checkout` with the prefix stripped (Kong `:8000` on the local cluster). |
| Events | `apis/asyncapi.yaml` | Seven published events on two topics, none consumed. Summarised on the [event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0005](/docs/adr/0005-mcp-server), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` and `/` (port 8090; through Kong `/api/mcp/checkout`, rewritten to `/mcp`). One read-only tool, `get_checkout_session`; no write tool. No auth, by fleet decision. See [MCP tools](/docs/mcp/tools). |
| A2A | [ADR 0007](/docs/adr/0007-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (port 8093; through Kong `/api/a2a/checkout`, rewritten to `/`), the Agent Card at `/.well-known/agent-card.json` and `GET /healthz`. One read-only skill, `checkout_session_status` (`sessionId`), no write skill. A rejection is answered in the message envelope as a problem (400, 404 or 500), never as a JSON-RPC error. No auth, by fleet decision. |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| CheckoutSessions | `POST /checkout-sessions` | `openCheckoutSession` | `201`, the session `OPEN` with its binding total; `Idempotency-Key` required |
| CheckoutSessions | `GET /checkout-sessions/{sessionId}` | `getCheckoutSession` | `200`, the session; `404 not-found` |
| CheckoutSessions | `POST /checkout-sessions/{sessionId}:confirm` | `confirmCheckoutSession` | `200` with the session in a definitive state (`COMPLETED`, `DECLINED`, `REQUOTE_REQUIRED`, `FAILED`); `202` while an outcome is unknown (`AUTHORIZING` or `PLACING`); `Idempotency-Key` required |
| Health | `GET /healthz` | none (not in the spec) | `200 {"status":"ok"}`, liveness only |
| Health | `GET /readyz` | none (not in the spec) | `200 {"status":"ready"}`, or `503` while draining or while Postgres or Kafka is unreachable |

## Conventions in the contract

- **Session state** is one of `OPEN`, `AUTHORIZING`, `DECLINED`, `PLACING`,
  `REQUOTE_REQUIRED`, `COMPLETED`, `EXPIRED`, `FAILED`. Money is an integer in
  minor units with an ISO currency; the delivery tier is `SAME_DAY`,
  `NEXT_DAY`, `TWO_DAY` or `STANDARD`. `authorizationId`, `orderId` and
  `failureCode` are `null` until they exist; `requotedOffers` appears only on a
  `REQUOTE_REQUIRED` session. `version` grows by one per change.
- **`Idempotency-Key`.** Required on both POST routes
  (`internal/adapters/inbound/http/idempotency.go`), 1 to 128 characters,
  otherwise `400 idempotency-key-required`. The first response below 500 is
  stored per (route, key) for 24 h. An equal request replays it with the header
  `Idempotent-Replayed: true`; the same key with a different method, path, query
  or body is `422 idempotency-key-reused`. A 5xx is never stored.
- **`202` is not an error.** It means the authorization or the place-order
  outcome is unknown; the session is saved and a policy resolves it. Poll
  `GET /checkout-sessions/{sessionId}`.
- **Errors** are RFC 9457 `application/problem+json` with
  `type = /problems/<slug>` (a relative URI), `title`, `status`, `detail` and
  `instance` (the request path). The slugs the code can answer:
  `malformed-request` (400), `idempotency-key-required` (400), `not-found`
  (404), `cart-not-checkout-ready`, `session-closed`, `authorization-mismatch`,
  `placement-in-doubt`, `void-pending`, `illegal-transition`,
  `concurrent-update` (all 409), `idempotency-key-reused`, `validation-failed`,
  `price-quote-expired`, `offer-not-quotable`, `price-quote-rejected` (all 422),
  `upstream-contract-violation` (502), `shopping-cart-unavailable`,
  `delivery-promise-unavailable`, `pricing-unavailable`,
  `customer-payments-unavailable`, `customer-ordering-unavailable`,
  `dependency-unavailable` (the `/readyz` answer) (all 503) and `internal-error`
  (500, the message is never leaked). Remedies are on
  [Troubleshooting](/docs/operations/troubleshooting#rest-errors-by-slug).
- **Strict bodies.** Unknown fields, trailing data, an empty body and a body
  over 64 KiB are `400 malformed-request`; a NUL character in the path, query or
  body is the same uniform client error.
- **Auth**: none. The fleet reverted REST, MCP and A2A auth on 2026-09-11, and
  `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns.
- **PII**: none. A session holds `shopperId` and `shipToAddressId` references;
  the payment-method token is kept only while `AUTHORIZING` and is never
  returned by any interface.
- **Events** are CloudEvents 1.0, structured mode, with a stable `id` across
  redelivery; see the [event catalogue](/docs/api-reference/events).

---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: What the Checkout bounded context owns and does not own, its subdomain class and area, its main capabilities and where to read next.
---

# Checkout

`checkout` is a **Supporting** bounded context of the **Storefront** area of
the warehouse-systems fleet. It is the purchase saga: one transaction across
price, payment and the retail network's promise confirmation. A shopper's
cart becomes a **checkout session**; the saga authorizes the payment first
(compensatable: it can be voided), then places the order with
`customer-ordering` (the pivot that cannot be undone from here), and leaves the
retriable steps to events. While the place-order outcome is unknown the session
sits in `PLACING`, the **semantic lock**: there is no void and no new attempt
until a definitive answer arrives.

The context holds a `shopperId` and a `shipToAddressId` reference and never PII
(no address, name or e-mail); the stored placement request is PII-free so it
can be replayed byte for byte under the same `Idempotency-Key`. Nothing in the
domain knows the network's wire names: the only place that does is the
Anti-Corruption Layer to customer-ordering
([ADR 0003](/docs/adr/0003-synchronous-contracts-and-the-anti-corruption-layer)).

:::info What runs on develop
Three binaries from one image. `cmd/api` is the composition root: REST
(chi, RFC 9457 problem details, `Idempotency-Key` middleware), Postgres
repositories with embedded migrations, a transactional outbox with an
always-on Kafka relay, and five clock-driven saga policies, on real Postgres
and real Kafka only. There is no in-memory mode and no log sink: `DATABASE_URL`,
`KAFKA_BROKERS` and the five sibling base URLs are mandatory and the process
exits at boot naming the missing one. `cmd/mcp` is a read-only MCP server
([ADR 0005](/docs/adr/0005-mcp-server)) and `cmd/a2a` a read-only A2A agent
([ADR 0007](/docs/adr/0007-a2a-inbound-adapter)); both read the same session
through the same `GetSession` use case. There is no web remote, no analytical
read side and no authentication on REST, MCP or A2A (a fleet-wide decision).
Packaged as a `Dockerfile` that builds every `cmd/*` and the Helm chart
`charts/checkout`; deployed on the fleet's local cluster by warehouse-infra.
:::

## What the context owns

| Concept | Where it lives | Rule in one line |
| --- | --- | --- |
| **CheckoutSession** (the one aggregate) | `internal/domain/checkoutsession` | Eight states (`OPEN`, `AUTHORIZING`, `DECLINED`, `PLACING`, `REQUOTE_REQUIRED`, `COMPLETED`, `EXPIRED`, `FAILED`); `COMPLETED`, `EXPIRED`, `FAILED` and `REQUOTE_REQUIRED` never change. |
| **Step order** | `internal/application/usecases` | Authorize, then place, then events. An outbound call is never inside a database transaction: the saga saves the state it acts from, calls out, then saves the outcome. |
| **Semantic lock** | `PLACING` | While the place-order outcome is unknown there is no void and no new attempt (`placement-in-doubt`). |
| **Stored placement request** | `checkout_sessions.placement_body` | The PII-free place-order body, replayed with the same key until a definitive answer; dropped 24 h after a closed state. |
| **Compensation debt** (`voidOwed`) | `checkout_sessions.void_authorization_id` | A rejected placement or a mismatching authorization is voided at once and retried every minute until it succeeds. |
| **Binding total** | `PriceQuote` fields on the session | Items + delivery charge per group + tax, allocated per group so the allocations sum to the quote total exactly. |
| **Anti-Corruption Layer** | `internal/adapters/outbound/customerordering` | The only code that knows the network's `POST /checkout`, `offer-not-quotable` and `requotedOffers`. |

## What it does not own

| Out of scope | Owned by |
| --- | --- |
| Item prices, delivery charges, tax, the binding quote | `pricing` (checkout asks for a quote and records its id, totals and expiry) |
| Talking to the payment provider, card tokens, capture, release, refund | `customer-payments` (capture and refund follow fulfilment facts, not checkout) |
| Delivery offers, capacity, promises and their confirmation | the network: `delivery-promise` and `customer-ordering` |
| The order record, the ship-to address, shopper PII, order history | `customer-ordering` (checkout keeps references only) |
| The cart, its lines and its delivery selection | `shopping-cart` |
| Sign-in and credentials | deferred: authentication is off fleet-wide |

## Classification and area

- **Supporting subdomain.** Checkout is the storefront's most complex context
  (a saga with a semantic lock) but a shopper never chooses a store for its
  saga: it must work, and working is not the differentiator. See
  [Subdomain classification](/docs/ddd/subdomain-classification) and the
  [Core domain chart](/docs/ddd/core-domain-chart).
- **Storefront area**, CloudEvents tier segment `storefront`: types look like
  `com.retailnetwork.storefront.checkout.checkoutsession.<EventName>`.
- It consumes the retail network's Open Host Service routes and never calls a
  restricted or in-org network route; see
  [Integration](/docs/ecosystem/integration).

## Main capabilities

1. **Open a session** from a ready cart: read the cart, pre-check that every
   selected delivery offer is still quotable with margin, obtain the binding
   price quote and persist the session `OPEN`
   (`POST /checkout-sessions`).
2. **Confirm it** with a payment-method token: authorize the exact quote total
   with an allocation per shipment group, store the PII-free placement request,
   place the order, and settle the answer
   (`POST /checkout-sessions/{sessionId}/confirmations`).
3. **Settle every outcome**: `COMPLETED` with the order id; a decline that keeps
   the session open for another token; `REQUOTE_REQUIRED` with the network's
   re-quoted offers; `FAILED` for a rejected request; `PLACING` (HTTP 202) when
   the outcome is unknown.
4. **Heal itself** with five policies: resolve in-doubt placements (alert at
   20 h, give up and void at 22 h), re-call stuck authorizations with the same
   key, retry owed voids, expire `OPEN` and `DECLINED` sessions with their
   quote, and purge stored placement requests.
5. **Read a session** over REST, MCP (`get_checkout_session`) and A2A
   (`checkout_session_status`).
6. **Publish seven CloudEvents** through the transactional outbox:
   `CheckoutCompleted` on `retailnetwork.checkout.events` (consumed by
   shopping-cart and customer-payments) and six analytics-only events on
   `retailnetwork.checkout.analytics`.

## Where to go next

| You want to | Read |
| --- | --- |
| See the layers, packages, binaries and the rules that police them | [Architecture](/docs/overview/architecture) |
| Run it and make the first calls | [Quickstart](/docs/overview/quickstart) |
| Know every setting | [Configuration](/docs/operations/configuration) |
| Deploy and operate it | [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability) |
| Fix something that broke | [Troubleshooting](/docs/operations/troubleshooting) |
| Know what tests and CI gates exist | [Testing](/docs/development/testing) |
| See who talks to whom | [Integration](/docs/ecosystem/integration) |
| See the use cases and their invariants | [Use cases](/docs/ddd/use-cases) |
| Read the design as ddd-crew artifacts | [DDD artifacts](/docs/ddd/ddd-artifacts), [Ubiquitous language](/docs/ddd/ubiquitous-language) |
| Read the contracts | [API overview](/docs/api-reference), [Event catalogue](/docs/api-reference/events), [MCP tools](/docs/mcp/tools) |
| Read the decisions | [ADR index](/docs/adr) |

Published at [https://iqvo.github.io/checkout/](https://iqvo.github.io/checkout/).

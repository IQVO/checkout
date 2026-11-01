---
id: bounded-context-canvas
title: Bounded Context Canvas
sidebar_label: Bounded Context Canvas
description: The ddd-crew Bounded Context Canvas v5 for Checkout, with purpose, classification, roles, every inbound and outbound message, business decisions, assumptions, metrics and open questions.
---

# Bounded Context Canvas

Following the ddd-crew [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas).
Every message row was checked against the code on this branch: the routes in
`internal/adapters/inbound/http`, the gateways in `internal/adapters/outbound`
and the events in `internal/adapters/outbound/kafka`.

## Canvas at a glance

Two pictures of the sections below, laid out like the ddd-crew canvas:
first the inbound collaborators feeding this context (purpose, domain roles
and the business decisions it enforces), then the outbound collaborators it
commands, queries and notifies. Arrows follow the message type: **solid blue**
commands (`cmd`), **dotted green** queries (`qry`), **thick orange** events
(`evt`); each arrow names its channel and the messages it carries. Box
shapes mark the collaborator kind: rounded = people, UI or any client;
blue = sibling bounded context; red = network context. Channel names on
the arrows are abbreviated; the Inbound and Outbound Communication tables
carry the exact route, topic and CloudEvents type for every message.

### Inbound

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 40, "padding": 6}}}%%
flowchart LR
    classDef bc fill:#fff3bf,stroke:#e67700,stroke-width:1.5px,color:#000
    classDef card fill:#fff9db,stroke:#f08c00,color:#000,text-align:left
    classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
    classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
    classDef net fill:#ffe3e3,stroke:#c92a2a,color:#000
    subgraph IN["Inbound - who commands or queries it"]
        direction TB
        in0(["Shopper via storefront-bff<br/>(experience layer)"]):::actor
    end
    subgraph BC["checkout - Supporting subdomain - storefront tier"]
        direction TB
        card["<b>Purpose</b><br/>The purchase saga: one<br/>transaction across price, payment<br/>and the network's promise<br/>confirmation. Authorize, place<br/>the order, then events.<br/><br/><b>Domain roles</b><br/>- Execution (saga orchestration)<br/>- Enforcer (no two holds on a<br/>card for one session)<br/><br/><b>Business decisions it enforces</b><br/>- Authorize before placing;<br/>capture and release follow<br/>fulfilment (D5)<br/>- The network's Checkout is the<br/>place-order step, via the ACL<br/>- Decline keeps the session open;<br/>409 voids and requotes; 422 fails<br/>- 503 or timeout: PLACING, 202,<br/>replay until a definitive answer"]:::card
    end
    in0 -->|"cmd - REST<br/>OpenSession, ConfirmSession"| BC
    in0 -.->|"qry - REST<br/>GetSession"| BC
    linkStyle 0 stroke:#1971c2,stroke-width:1.5px
    linkStyle 1 stroke:#2f9e44,stroke-width:1.5px
    class BC bc
```

Source: `internal/adapters/inbound/http/server.go` (the three session routes),
`internal/application/usecases` (the roles and decisions).

### Outbound

```mermaid
%%{init: {"flowchart": {"nodeSpacing": 14, "rankSpacing": 40, "padding": 6}}}%%
flowchart LR
    classDef bc fill:#fff3bf,stroke:#e67700,stroke-width:1.5px,color:#000
    classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
    classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
    classDef net fill:#ffe3e3,stroke:#c92a2a,color:#000
    BC["<b>checkout - Supporting subdomain - storefront tier</b>"]:::bc
    subgraph OUT["Outbound - whom it commands, queries and notifies"]
        direction TB
        out0["shopping-cart"]:::ctx
        out1["pricing"]:::ctx
        out2["customer-payments"]:::ctx
        out3["delivery-promise<br/>(network)"]:::net
        out4["customer-ordering<br/>(network)"]:::net
        out5(["analytics consumers"]):::actor
    end
    BC -.->|"qry - REST<br/>GET /carts/{cartId}<br/>GetCart"| out0
    BC -.->|"qry - REST<br/>GET /delivery-offers/{offerId}<br/>GetDeliveryOffer"| out3
    BC -->|"cmd - REST<br/>POST /price-quotes<br/>IssuePriceQuote"| out1
    BC -->|"cmd - REST<br/>POST /payment-authorizations, .../voids<br/>AuthorizePayment, VoidAuthorization"| out2
    BC -->|"cmd - REST via ACL<br/>POST /checkout<br/>PlaceOrder"| out4
    BC ==>|"evt - Kafka<br/>retailnetwork.checkout.events<br/>CheckoutCompleted"| out0
    BC ==>|"evt - Kafka<br/>retailnetwork.checkout.events<br/>CheckoutCompleted"| out2
    BC ==>|"evt - Kafka<br/>retailnetwork.checkout.analytics<br/>Opened, Declined, RequoteRequired,<br/>Failed, SessionExpired,<br/>PlacementInDoubt"| out5
    linkStyle 0 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 1 stroke:#2f9e44,stroke-width:1.5px
    linkStyle 2 stroke:#1971c2,stroke-width:1.5px
    linkStyle 3 stroke:#1971c2,stroke-width:1.5px
    linkStyle 4 stroke:#1971c2,stroke-width:1.5px
    linkStyle 5 stroke:#e8590c,stroke-width:2.5px
    linkStyle 6 stroke:#e8590c,stroke-width:2.5px
    linkStyle 7 stroke:#e8590c,stroke-width:2.5px
```

Source: `cmd/api/adapters.go` (the five gateways), `internal/adapters/outbound/kafka/encoder.go`
(the two topics and the seven events).

The diagrams omit assumptions, verification metrics, decisions taken and the
ubiquitous language (sections below) and the relationship pattern per
message (see the tables). The context consumes no event: everything it learns
it learns from a synchronous answer.

## Name

**Checkout** (`checkout`, code `CK`, CloudEvents tier `storefront`).

## Purpose

To run the purchase saga. The context:

- opens a session from a cart: reads the cart snapshot, pre-checks that every
  selected offer is still `QUOTED` with margin, obtains the binding
  `PriceQuote`, numbers the lines `1..n` and groups them by the quote's
  `groupNo`;
- on confirm authorizes the quote total with an allocation per group, then
  places the order through the Anti-Corruption Layer with the stored,
  PII-free request;
- settles the three definitive failures (decline, requote, contract bug) and
  the one unknown (in-doubt placement) with a defined state each;
- announces the outcome: `CheckoutCompleted` for the cart and for payments,
  six analytics events for the funnel and the operators.

## Strategic Classification

| Axis | Verdict |
| --- | --- |
| **Domain** | **Supporting** subdomain. See the [Core Domain Chart](/docs/ddd/core-domain-chart). |
| **Business Model** | Conversion (the purchase transaction). |
| **Evolution** | Custom-built. |

## Domain Roles

| Role | Applies? | Evidence in the code |
| --- | --- | --- |
| **Execution (saga orchestration)** | **Yes, primary** | The `CheckoutSession` aggregate (`OpenSession` through `ExpireSession`), one record of where the purchase stands, plus the five policies. |
| **Enforcer (no two holds on a card for one session)** | **Yes** | The deterministic `Idempotency-Key` `<sessionId>:auth:<attempt>` on `AuthorizePayment`, the `PLACING` semantic lock (`ErrPlacementInDoubt`) and `customer-payments`' one-authorization-per-session invariant. |
| Analysis | No | The cart's readiness is [shopping-cart](https://iqvo.github.io/warehouse-docs/storefront/contexts/shopping-cart)'s fact; checkout only enforces it (`ErrCartNotCheckoutReady`). |
| Specification | No | Prices, charges and tax are [pricing](https://iqvo.github.io/warehouse-docs/storefront/contexts/pricing)'s specification. |

## Inbound Communication

| Collaborator | Message | Type | Channel | Relationship |
| --- | --- | --- | --- | --- |
| Shopper via `storefront-bff` | `OpenSession` | Command | REST `POST /checkout-sessions` | UI |
| Shopper via `storefront-bff` | `ConfirmSession` | Command | REST `POST /checkout-sessions/{sessionId}/confirmations` | UI |
| Shopper via `storefront-bff` | `GetSession` | Query | REST `GET /checkout-sessions/{sessionId}` | UI |
| AI clients | `get_checkout_session` | Query | MCP, Streamable HTTP, `cmd/mcp` | read-only adapter over `GetSession` |
| agents | `checkout_session_status` | Query | A2A JSON-RPC, `cmd/a2a` | read-only adapter over `GetSession` |

Route-to-command note: the three routes map onto `OpenSession`,
`ConfirmSession` and `GetSession`. The other commands of the aggregate
(`RecordAuthorization`, `RecordDecline`, `RecordPlacementInDoubt`,
`RecordPlaced`, `RecordRequoteRequired`, `RecordFailure`, `ExpireSession`,
`VoidCompleted`, `MarkInDoubtAlerted`, `PurgePlacement`) have no inbound route:
they are the outcomes the saga records on itself, from the answers of its
outbound calls and from the five policies. `GetSession` is also served over
MCP (`get_checkout_session`) and A2A (`checkout_session_status`).

## Outbound Communication

| Collaborator | Message | Type | Channel | Relationship |
| --- | --- | --- | --- | --- |
| `shopping-cart` | `GetCart` | Query | REST `GET /carts/{cartId}` | Customer/Supplier (we are the Customer) |
| `delivery-promise` (network) | `GetDeliveryOffer` | Query | REST `GET /delivery-offers/{offerId}` (at open: pre-check that every selected offer is still `QUOTED`, with a 2-minute margin) | Open Host Service; we are the Customer |
| `pricing` | `IssuePriceQuote` | Command | REST `POST /price-quotes` | Customer/Supplier (we are the Customer) |
| `customer-payments` | `AuthorizePayment` | Command | REST `POST /payment-authorizations` (`Idempotency-Key` `<sessionId>:auth:<attempt>`, allocations per group) | Customer/Supplier (we are the Customer) |
| `customer-payments` | `VoidAuthorization` | Command | REST `POST /payment-authorizations/{authorizationId}/voids` (`Idempotency-Key` `<sessionId>:void`; after a requote, a rejection, a mismatching authorization or the 22 h give-up) | Customer/Supplier (we are the Customer) |
| `customer-ordering` (network) | `PlaceOrder` | Command | REST `POST /checkout` through the ACL (`internal/adapters/outbound/customerordering`); `Idempotency-Key` is the `sessionId`; `shipToAddressId` (NG1), never an inline address | Open Host Service behind an Anti-Corruption Layer; we are the Customer |
| `shopping-cart`, `customer-payments` | `CheckoutCompleted` | Event | Kafka `retailnetwork.checkout.events`, `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted` | Customer/Supplier (we are the Supplier) |
| analytics only | `CheckoutOpened` | Event | Kafka `retailnetwork.checkout.analytics`, `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutOpened` | analytics stream |
| analytics only | `CheckoutDeclined` | Event | Kafka `retailnetwork.checkout.analytics`, `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutDeclined` | analytics stream |
| analytics only | `CheckoutRequoteRequired` | Event | Kafka `retailnetwork.checkout.analytics`, `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutRequoteRequired` | analytics stream |
| analytics only | `CheckoutFailed` | Event | Kafka `retailnetwork.checkout.analytics`, `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutFailed` | analytics stream |
| analytics only | `CheckoutSessionExpired` | Event | Kafka `retailnetwork.checkout.analytics`, `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutSessionExpired` | analytics stream |
| analytics only | `CheckoutPlacementInDoubt` | Event | Kafka `retailnetwork.checkout.analytics`, `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutPlacementInDoubt` | analytics stream |

What the neighbours do with them: the cart closes on `CheckoutCompleted`
(`CloseOnCheckoutCompleted`); `customer-payments` binds the order to the
session's authorization on it (`BindOrderOnCheckoutCompleted`). Both consume it
from their own consumer group (`shopping-cart-checkout`,
`customer-payments-checkout` in warehouse-infra `helm-values`). Relationship evidence:
[Context Map](/docs/ddd/context-map).

## Ubiquitous Language

Full glossary: [Ubiquitous Language](/docs/ddd/ubiquitous-language).
Top terms: **Checkout Session**, **Place-Order Step** (the network's
"Checkout"), **In-Doubt Placement**, **Requote**, **Allocation**.

## Business Decisions

1. **Money flow (D5).** Authorize before placing the order; capture per
   shipment group on `FulfillmentShipped`; release on
   `FulfillmentCancelled`; refund on `PackageLost` or `PackageReturned`.
2. **The network's "Checkout" is the place-order step**, reached through an
   Anti-Corruption Layer in `internal/adapters/outbound/customerordering`,
   the only place that knows network wire names.
3. **A decline keeps the session open** for another token while the price
   quote is valid; a definitive `409` voids the authorization and asks for a
   requote; a `400` or `422` is a contract bug: void, `FAILED`, alert.
4. **A `503` or timeout leaves the session `PLACING`**, answers `202`, and
   `ResolveInDoubtPlacements` replays the stored body with the same
   `Idempotency-Key` until a definitive answer; alert at 20 h, give up and void at
   22 h, before the network's 24 h idempotency window.
5. **The cart must be ready, the offers quotable, the quote unexpired.**
   `ErrCartNotCheckoutReady` and `ErrOfferNotQuotable` at open,
   `ErrPriceQuoteExpired` at confirm, and `ExpireOnQuoteExpiry` ends an open or
   declined session with its quote.
6. **The authorized amount equals the quote total**
   (`ErrAuthorizationMismatch`).
7. **A closed session never changes** (`ErrSessionClosed`): `COMPLETED`,
   `EXPIRED`, `FAILED` and `REQUOTE_REQUIRED` allow no transition out.
8. **No PII.** The session holds `shopperId` and `shipToAddressId`; the
   stored placement request is PII-free.

## Assumptions

- The BFF mints the `Idempotency-Key` for every write and reads
  `GET /checkout-sessions/{sessionId}` to poll a `PLACING` session after a
  `202` (REST contract description).
- `customer-ordering` replays the first response to a repeated
  `Idempotency-Key` (the policy `ResolveInDoubtPlacements` says so), and the
  network's idempotency window is 24 h (decision text; see Open Questions).
- The network exposes `shipToAddressId` on `POST /checkout` (the EXTEND
  NG1), and its wire names (`Checkout`, `offer-not-quotable`,
  `requotedOffers`) stay inside the ACL.
- `customer-payments` returns a decline as a definitive answer and a
  successful void as a definitive answer; the session records both.
- Every POST route is idempotent on `Idempotency-Key` (global to the service, 409 on reuse); every published event
  leaves through the transactional outbox.
- One configured currency everywhere; the session records `currency` and
  `totalMinor` from the quote, and an `amountMinor` per group.

## Verification Metrics

| Metric | Target (proposed) | Source |
| --- | --- | --- |
| Sessions confirmed to `COMPLETED` over sessions confirmed | tracked; the conversion signal | metric `checkout.sessions.confirmed` (`outcome`), `CheckoutCompleted` and `CheckoutOpened` counts |
| Share of confirms ending `DECLINED` | tracked; alert on a spike | metric outcome `declined`, `CheckoutDeclined` |
| Share of sessions ending `REQUOTE_REQUIRED` | low; a rise means offers expire between cart and confirm | `CheckoutRequoteRequired` |
| `FAILED` sessions (contract bug) | 0; each one alerts | `CheckoutFailed` |
| Sessions in `PLACING` and their age | 0 older than 20 h; alert at 20 h, before the network's 24 h window | `checkout_sessions.state`, `updated_at` |
| Sessions with an authorization and no `orderId` after a definitive answer | 0 | `authorizationId` vs `orderId` |
| Authorization/void calls per session | one authorization key per attempt (`<sessionId>:auth:<attempt>`) | `customer-payments` metrics |
| Outbox age of the oldest unpublished checkout event | below 5 s (no metric or alert exists; query `outbox_events`) | `outbox_events.created_at` where `published_at IS NULL` |

## Decisions taken since the design model, and what stays open

The design model left ten hotspots (H1 to H10). The code settles most of them:

| Hotspot | Answer in the code |
| --- | --- |
| H1 Where does `DECLINED` go when the quote expires? | `ExpireSession` accepts `OPEN` and `DECLINED`; both go to `EXPIRED` (`ExpireOnQuoteExpiry`). |
| H2 What key does the second authorize use? | `<sessionId>:auth:<attempt>`; the attempt grows each time a `DECLINED` session is confirmed again, and a stuck call re-uses the same attempt. |
| H3 What if an offer is no longer quotable? | `offer-not-quotable` (422) at open, no state change. The code does not repeat the check at confirm: after an offer expired between open and confirm the answer comes from customer-ordering as a requote. |
| H4 What ends `AUTHORIZING` if the outcome is unknown? | `ResolveStuckAuthorizations` after 2 minutes, re-calling with the same key. |
| H5 What does a failed void do? | The debt stays on the session (`void_authorization_id`) and `RetryOwedVoids` retries every minute. |
| H6 The resolver and the 24 h void race | The resolver alerts at 20 h and gives up and voids at 22 h; the void is idempotent. |
| H7 Silent outcomes | `CheckoutSessionExpired` and `CheckoutPlacementInDoubt` are published (analytics). |
| H8 The network contract | Resolved: `shipToAddressId`, `offer-not-quotable` and `requotedOffers` are in customer-ordering's contract. |
| H9 The stored placement request | `StoredPlacement{RequestBody, RequestHash, PlacingSince}`, dropped 24 h after a closed state by `PurgeStoredPlacements`. |
| H10 How does the cart take the requoted offers? | Not verifiable from this repository: the session only carries `requotedOffers`; whether the BFF writes them into the cart is outside this code. |

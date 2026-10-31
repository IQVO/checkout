---
id: domain-message-flow
title: Domain Message Flow
sidebar_label: Domain Message Flow
description: Five Checkout scenarios as ddd-crew domain message flows, covering opening a session, confirming through authorize and place to completion, a decline and another token, a late requote or contract failure, and an in-doubt placement.
---

# Domain Message Flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling).
Five scenarios. Participants are contexts and actors. Every arrow is a
message of the code (a route, a gateway call or an event), prefixed `cmd:` (command), `qry:` (query) or `evt:`
(event). A `Note` names a command the session runs on itself. Kafka is drawn
as one participant. Event names are written `entity.EventName`; the full
type is `com.retailnetwork.<tier>.<producer>.<entity>.<EventName>` (see
[Domain Events](/docs/ddd/domain-events)). REST path
parameters are written without braces. The storefront-wide flows follow from
the [Context Map](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/context-map).

## 1. A shopper opens a session from a ready cart

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant BFF as storefront-bff
    participant CK as checkout
    participant SC as shopping-cart
    participant DP as delivery-promise
    participant PR as pricing
    participant K as Kafka analytics
    BFF->>CK: cmd: OpenSession<br/>POST /checkout-sessions
    CK->>SC: qry: GetCart<br/>GET /carts/cartId
    SC-->>CK: qry: reply cart, version,<br/>groups, selections, readiness
    Note over CK: ErrCartNotCheckoutReady<br/>when readiness does not hold
    CK->>DP: qry: GetDeliveryOffer<br/>per selected offer
    DP-->>CK: qry: reply offer still QUOTED<br/>with margin
    CK->>PR: cmd: IssuePriceQuote<br/>POST /price-quotes
    PR-->>CK: cmd: reply binding quote,<br/>total, expiresAt
    Note over CK: lines numbered 1..n,<br/>groups by the quote groupNo,<br/>session OPEN
    CK->>K: evt: checkoutsession.CheckoutOpened
    CK-->>BFF: cmd: reply 201 OPEN<br/>with the binding total
```

Source: `internal/adapters/inbound/http/handlers.go` (`handleOpen`),
`internal/application/usecases/open_session.go`,
`internal/domain/checkoutsession/open.go` (`CheckoutOpened`) and the three
gateways `shoppingcart`, `deliverypromise` and `pricing` under
`internal/adapters/outbound`.

Omitted: the outbox write and relay (see
[Sequence Diagrams](/docs/ddd/sequence-diagrams)); the
`503` the REST contract names when one of the three dependencies is
unreachable (the shopper retries with the same `Idempotency-Key`); what
happens when an offer is no longer `QUOTED` at this pre-check: `ErrOfferNotQuotable`,
`422 offer-not-quotable`, no session is created (hotspot H3 on
[EventStorming](/docs/ddd/eventstorming)).

## 2. Confirm: authorize, place the order, complete

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 96, "actorMargin": 8, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant BFF as storefront-bff
    participant CK as checkout
    participant CP as customer-payments
    participant CO as customer-ordering
    participant K as Kafka
    participant SC as shopping-cart
    BFF->>CK: cmd: ConfirmSession<br/>POST :confirm, token
    Note over CK: ErrPriceQuoteExpired<br/>OPEN to AUTHORIZING
    CK->>CP: cmd: AuthorizePayment<br/>key sessionId:auth:attempt,<br/>allocations per group
    CP-->>CK: cmd: reply authorizationId, total
    Note over CK: RecordAuthorization<br/>ErrAuthorizationMismatch<br/>AUTHORIZING to PLACING,<br/>placement request stored
    CK->>CO: cmd: PlaceOrder via ACL<br/>POST /checkout<br/>key sessionId
    CO-->>CK: cmd: reply orderId
    Note over CK: RecordPlaced<br/>PLACING to COMPLETED
    CK->>K: evt: checkoutsession.CheckoutCompleted
    CK-->>BFF: cmd: reply 200 COMPLETED<br/>with orderId
    K->>SC: evt: CheckoutCompleted<br/>cart closes
    K->>CP: evt: CheckoutCompleted<br/>order bound
```

Source: `internal/application/usecases/confirm_session.go`,
`internal/domain/checkoutsession/transitions.go` and `terminals.go`,
`internal/adapters/outbound/customerpayments/gateway.go` and
`customerordering/gateway.go`, `internal/adapters/outbound/kafka/encoder.go`.
The two consumers (shopping-cart and customer-payments) are other services;
their reactions are described from their published contracts, not from this
repository.

The two consumers react independently; neither blocks the answer to the
shopper. Omitted: the transactional outbox, the replies' status codes on the
two network and payment calls beyond the success path, and the parked
fulfilment facts `customer-payments` applies after the bind (gap S4).

## 3. A decline, then another token

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant BFF as storefront-bff
    participant CK as checkout
    participant CP as customer-payments
    participant K as Kafka analytics
    BFF->>CK: cmd: ConfirmSession<br/>first payment method token
    CK->>CP: cmd: AuthorizePayment<br/>POST /payment-authorizations
    CP-->>CK: cmd: reply declined
    CP->>K: evt: paymentauthorization.PaymentDeclined
    Note over CK: RecordDecline<br/>AUTHORIZING to DECLINED
    CK->>K: evt: checkoutsession.CheckoutDeclined
    CK-->>BFF: cmd: reply 200 DECLINED
    Note over BFF: the shopper picks another<br/>payment method while the<br/>price quote is valid
    BFF->>CK: cmd: ConfirmSession<br/>second payment method token
    Note over CK: ErrPriceQuoteExpired<br/>if the quote has lapsed<br/>DECLINED to AUTHORIZING
    CK->>CP: cmd: AuthorizePayment<br/>POST /payment-authorizations
    CP-->>CK: cmd: reply authorizationId
    Note over CK: RecordAuthorization<br/>AUTHORIZING to PLACING,<br/>then scenario 2 from the place-order step
```

Source: `internal/domain/checkoutsession/transitions.go` (`RecordDecline`,
`Confirm` from `DECLINED`), `internal/adapters/outbound/customerpayments/gateway.go`
(`Authorize`: a 201 in any state but `AUTHORIZED`, or a 422, is a decline).

A decline voids nothing, because no authorization exists. The quote is the
clock: the session stays open for as long as the binding price quote is valid,
and `ExpireOnQuoteExpiry` ends it afterwards (`DECLINED → EXPIRED`, hotspot H1).
The second `AuthorizePayment` uses a new key, `sessionId:auth:attempt` with the
attempt counter raised by one, so it cannot replay the first, declined answer
(hotspot H2).

## 4. A late requote, or a contract failure

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant BFF as storefront-bff
    participant CK as checkout
    participant CO as customer-ordering
    participant CP as customer-payments
    participant K as Kafka analytics
    Note over CK: session PLACING,<br/>authorization held,<br/>PlaceOrder just sent
    CK->>CO: cmd: PlaceOrder via ACL<br/>POST /checkout
    alt definitive 409, offer no longer quotable
        CO-->>CK: cmd: reply 409 with requoted offers
        Note over CK: the ACL maps it to a requote<br/>RecordRequoteRequired<br/>PLACING to REQUOTE_REQUIRED<br/>void owed
        CK->>K: evt: checkoutsession.CheckoutRequoteRequired
        CK->>CP: cmd: VoidAuthorization<br/>POST :void<br/>key sessionId:void
        CP-->>CK: cmd: reply voided
        CK-->>BFF: cmd: reply 200 REQUOTE_REQUIRED<br/>with requotedOffers
    else 400 or 422, a contract bug
        CO-->>CK: cmd: reply 400 or 422
        Note over CK: RecordFailure and an alert<br/>PLACING to FAILED<br/>void owed
        CK->>K: evt: checkoutsession.CheckoutFailed
        CK->>CP: cmd: VoidAuthorization<br/>key sessionId:void
        CP-->>CK: cmd: reply voided
        CK-->>BFF: cmd: reply 200 FAILED
    end
```

Source: `internal/adapters/outbound/customerordering/gateway.go` (`Place`:
which statuses are definitive), `internal/application/usecases/settle.go`
(`settlePlacement`, `settleVoid`), `internal/domain/checkoutsession/terminals.go`
(`RecordRequoteRequired`, `RecordFailure`).

The decision is recorded first, with the void owed in the same save; the void is
then attempted at once. When the void itself fails, the debt stays on the
session and `RetryOwedVoids` retries it every minute (hotspot H5). The shopper starts the
next attempt from the cart, with a new session. The REST contract says the BFF
writes the requoted offers into the cart as a new quote; the cart's
`AcceptRequotedOffers` (`POST /carts/{cartId}/delivery/requoted`, hotspot H10)
takes them, though `storefront-bff` has no route for it yet (both outside this
repository, not verified here). Omitted: the network's wire shapes
(`offer-not-quotable`, `requotedOffers`), which are the ACL's concern
(`internal/adapters/outbound/customerordering`, hotspot H8).

## 5. An in-doubt placement, resolved by replay

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant BFF as storefront-bff
    participant CK as checkout
    participant CO as customer-ordering
    participant K as Kafka
    Note over CK: session PLACING,<br/>authorization held
    CK->>CO: cmd: PlaceOrder via ACL<br/>key sessionId
    CO--xCK: timeout or 503
    Note over CK: RecordPlacementInDoubt<br/>PLACING stays PLACING,<br/>no void, no new attempt
    CK-->>BFF: cmd: reply 202 PLACING
    BFF->>CK: qry: GetSession<br/>GET /checkout-sessions/sessionId
    CK-->>BFF: qry: reply 200 PLACING
    Note over CK: ResolveInDoubtPlacements<br/>backoff from 30 s, alert at 20 h
    CK->>CO: cmd: PlaceOrder replay<br/>stored body, same key sessionId
    CO-->>CK: cmd: reply the first response, orderId
    Note over CK: RecordPlaced<br/>PLACING to COMPLETED
    CK->>K: evt: checkoutsession.CheckoutCompleted
    BFF->>CK: qry: GetSession
    CK-->>BFF: qry: reply 200 COMPLETED
```

Source: `internal/application/usecases/resolve_in_doubt.go`,
`settle.go`, `internal/domain/checkoutsession/transitions.go`
(`RecordPlacementInDoubt`), `internal/adapters/inbound/http/handlers.go`
(`handleConfirm` answers 202 while `Unsettled`).

The replay is safe because the stored request is PII-free and byte for byte
the same, and `customer-ordering` is expected to replay its first response to a repeated
`Idempotency-Key`. A replay can end any of the three definitive ways: the
diagram shows the success; a `409`, `400` or `422` on the replay takes scenario 4
from the void on. If the network is still unreachable the session stays
`PLACING`, and the alert at 20 h comes before the network's 24 h window.
Omitted: the BFF's polling cadence, which this service does not define; and the
interplay with customer-payments' own 24 h backstop, which voids an
authorization with no `CheckoutCompleted` (hotspot H6; that policy lives in
customer-payments, not here).
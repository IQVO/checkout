---
id: sequence-diagrams
title: Sequence Diagrams
sidebar_label: Sequence Diagrams
description: UML sequence diagrams for every Checkout command use case, from inbound adapter through aggregate, repository, the outbound clients and the Anti-Corruption Layer to the outbox and Kafka, with error, concurrency, idempotency and in-doubt branches.
---

# Sequence Diagrams

One UML sequence diagram per command use case on
[Use Cases](/docs/ddd/use-cases); the long
`ConfirmSession` is drawn in two parts (authorize, then place the order).
Participant names are the packages and ports the model designs, from the
[Class Diagram](/docs/ddd/class-diagram):

- `http` and `scheduler` are the inbound adapters.
- The use case is in `internal/application/usecases`.
- `Clients` stands for the outbound REST adapters `shoppingcart`,
  `deliverypromise` and `pricing`; `Payments` for `customerpayments`.
- `ACL` is `internal/adapters/outbound/customerordering`, the
  Anti-Corruption Layer and the only participant that knows the network's wire
  names.
- `Postgres` stands for the repository, the outbox and the idempotency store.
  They share one transaction through `UnitOfWork`.
- `Relay / Kafka` is the outbox relay. It reads committed `outbox_events`
  rows and publishes `CheckoutCompleted` to `retailnetwork.checkout.events`
  and the six other events to `retailnetwork.checkout.analytics`.

Every save is conditional on the loaded `version` (optimistic concurrency).
**No outbound call sits inside a database transaction**: the use case commits
the state it acts from, calls out, then commits the outcome. Full CloudEvents
types are on [Domain Events](/docs/ddd/domain-events).

## 1. OpenSession (REST POST /checkout-sessions)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as OpenSession
    participant A as CheckoutSession
    participant C as Clients
    participant DB as Postgres
    participant K as Relay / Kafka
    H->>DB: find Idempotency-Key
    alt key seen with the same body
        DB-->>H: stored response
        H-->>H: replay 201
    else new key
        H->>UC: Execute(cartId, shopperId, shipToAddressId)
        UC->>C: GetCart(cartId)
        C-->>UC: cart, version, groups, readiness
        alt readiness does not hold
            UC-->>H: 409 cart-not-checkout-ready
        else ready
            UC->>C: GetDeliveryOffer(offerId) per selected offer
            C-->>UC: offer still QUOTED with margin
            UC->>C: IssuePriceQuote(cart)
            C-->>UC: binding quote, total, expiresAt
            UC->>A: OpenSession(cart, quote, now)
            A-->>UC: OPEN session, lines 1..n, groups
            UC->>DB: BEGIN, insert session, outbox CheckoutOpened
            UC->>DB: store key response, COMMIT
            UC-->>H: 201 session with the binding total
            DB-->>K: relay publishes CheckoutOpened (analytics)
        end
    end
```

Derived from: `model.yaml → contexts[checkout].aggregates[CheckoutSession].commands[OpenSession]`,
`.invariants[ErrCartNotCheckoutReady]`, `.sync_calls`, `.rest["POST /checkout-sessions"]`,
`.publishes[CheckoutOpened]`, `conventions.integration_rules`.

Omitted: the `503` when `Clients` cannot answer (the shopper retries with the
same `Idempotency-Key`), the `422` on invalid input, and the case where an
offer is no longer `QUOTED` at the pre-check, which fails as
`ErrOfferNotQuotable` (`422 offer-not-quotable`, no state change; hotspot H3,
resolved, on [EventStorming](/docs/ddd/eventstorming)).

## 2. ConfirmSession, part 1: authorize (REST POST ...:confirm)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as ConfirmSession
    participant A as CheckoutSession
    participant C as Clients
    participant P as Payments
    participant DB as Postgres
    participant K as Relay / Kafka
    H->>DB: find Idempotency-Key
    alt key seen with the same body
        DB-->>H: stored response
        H-->>H: replay the first status
    else new key
        H->>UC: Execute(sessionId, paymentMethodToken)
        UC->>DB: load session (version)
        DB-->>UC: session or not found
        UC->>A: ConfirmSession(now)
        alt closed, in doubt or quote expired
            A-->>UC: ErrSessionClosed, ErrPlacementInDoubt<br/>or ErrPriceQuoteExpired
            UC-->>H: 409 problem
        else OPEN or DECLINED
            UC->>C: GetDeliveryOffer(offerId) per selected offer
            alt an offer is no longer QUOTED
                C-->>UC: not quotable
                UC-->>H: 422 offer-not-quotable, no state change
            else all quotable
            A-->>UC: AUTHORIZING, authAttempt + 1 from DECLINED
            UC->>DB: save, version-checked
            UC->>P: AuthorizePayment<br/>key sessionId:auth:authAttempt
            alt declined
                P-->>UC: declined
                UC->>A: RecordDecline(reason, now)
                UC->>DB: BEGIN, save DECLINED, outbox CheckoutDeclined
                UC->>DB: store key response, COMMIT
                UC-->>H: 200 DECLINED
                DB-->>K: relay publishes CheckoutDeclined (analytics)
            else authorized
                P-->>UC: authorizationId, total
                UC->>A: RecordAuthorization(authorizationId, total, request)
                alt total differs from the quote total
                    A-->>UC: ErrAuthorizationMismatch, voidOwed set
                    UC->>DB: save voidOwed (RetryOwedVoids voids it)
                    UC-->>H: 409 authorization-mismatch
                else equal
                    A-->>UC: PLACING with the stored request
                    UC->>DB: save, version-checked
                    Note over UC,DB: continues with part 2
                end
            end
            end
        end
    end
```

Derived from: `model.yaml → contexts[checkout].aggregates[CheckoutSession]`
(`transitions`, `invariants`), `.sync_calls[AuthorizePayment]`,
`.rest["POST /checkout-sessions/{sessionId}:confirm"]`,
`.publishes[CheckoutDeclined]`, `.policies[ResolveStuckAuthorizations, RetryOwedVoids]`.

Omitted: the `404` for an unknown session, a version conflict retried once
and then `409 concurrent-update`. A `Payments` timeout on authorize leaves
the session `AUTHORIZING`; `ResolveStuckAuthorizations` re-calls after 2
minutes with the same key (hotspot H4, resolved). A mismatching total sets
`voidOwed` (hotspot H5, resolved), and a second authorize after a decline is
a new attempt with its own key (hotspot H2, resolved).

## 3. ConfirmSession, part 2: place the order

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 100, "actorMargin": 8, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as ConfirmSession
    participant A as CheckoutSession
    participant O as ACL
    participant P as Payments
    participant DB as Postgres
    participant K as Relay / Kafka
    UC->>O: PlaceOrder(stored request)<br/>key sessionId
    alt order accepted
        O-->>UC: orderId
        UC->>A: RecordPlaced(orderId, now)
        UC->>DB: BEGIN, save COMPLETED, outbox CheckoutCompleted
        UC->>DB: store key response, COMMIT
        UC-->>H: 200 COMPLETED with orderId
        DB-->>K: relay publishes CheckoutCompleted (events topic)
    else definitive 409, requoted offers
        O-->>UC: requote, translated by the ACL
        UC->>A: RecordRequoteRequired(offers, now)<br/>voidOwed = authorizationId
        UC->>DB: BEGIN, save REQUOTE_REQUIRED, outbox CheckoutRequoteRequired
        UC->>DB: store key response, COMMIT
        UC->>P: VoidAuthorization<br/>key sessionId:void
        P-->>UC: voided (else RetryOwedVoids retries)
        UC-->>H: 200 REQUOTE_REQUIRED with requotedOffers
    else definitive 422, contract bug
        O-->>UC: rejected
        UC->>A: RecordFailure(code, now)<br/>voidOwed = authorizationId
        UC->>DB: BEGIN, save FAILED, outbox CheckoutFailed
        UC->>DB: store key response, COMMIT
        UC->>P: VoidAuthorization<br/>key sessionId:void
        P-->>UC: voided (else RetryOwedVoids retries)
        UC-->>H: 200 FAILED, alert raised
    else timeout or 503
        O--xUC: no definitive answer
        UC->>A: RecordPlacementInDoubt(now)
        Note over A: no void, no new attempt
        UC->>DB: BEGIN, save (stays PLACING), outbox CheckoutPlacementInDoubt
        UC->>DB: store key response, COMMIT
        UC-->>H: 202 PLACING
    end
```

Derived from: `model.yaml → contexts[checkout].business_decisions`,
`.aggregates[CheckoutSession]` (`transitions`, `ErrPlacementInDoubt`),
`.sync_calls` (`PlaceOrder`, `VoidAuthorization`),
`.publishes` (`CheckoutCompleted`, `CheckoutRequoteRequired`,
`CheckoutFailed`, `CheckoutPlacementInDoubt`),
`.policies[RetryOwedVoids]`.

The stored request is PII-free and carries `shipToAddressId`, never an
address; the `Idempotency-Key` towards the network is the `sessionId`. The
`REQUOTE_REQUIRED` and `FAILED` states are recorded together with
`voidOwed`, in one transaction, before the void is attempted; a failing void
is retried every minute by `RetryOwedVoids` (hotspot H5, resolved). Omitted:
the mapping of the network's wire names, which happens inside `ACL`
(`internal/adapters/outbound/customerordering`; hotspot H8, resolved).

## 4. ResolveInDoubtPlacements (scheduler)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 100, "actorMargin": 8, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as scheduler
    participant UC as Resolve
    participant A as CheckoutSession
    participant O as ACL
    participant P as Payments
    participant DB as Postgres
    participant K as Relay / Kafka
    S->>UC: Execute() with backoff
    UC->>DB: load PLACING sessions with a stored request
    loop each PLACING session
        UC->>O: PlaceOrder(stored request, same body)<br/>key sessionId
        alt first response replayed, order accepted
            O-->>UC: orderId
            UC->>A: RecordPlaced(orderId, now)
            UC->>DB: BEGIN, save COMPLETED, outbox CheckoutCompleted, COMMIT
            DB-->>K: relay publishes CheckoutCompleted
        else definitive 409 or 422
            O-->>UC: requote or rejected
            UC->>A: RecordRequoteRequired or RecordFailure<br/>voidOwed set
            UC->>DB: BEGIN, save, outbox event, COMMIT
            DB-->>K: relay publishes the analytics event
            UC->>P: VoidAuthorization<br/>key sessionId:void
        else still no answer
            O--xUC: timeout or 503
            UC->>A: RecordPlacementInDoubt(now)
            Note over UC: alert at 20 h from placingSince
        end
        opt placing for 22 h or more
            UC->>P: give up, VoidAuthorization<br/>key sessionId:void
        end
    end
```

Derived from: `model.yaml → contexts[checkout].policies[ResolveInDoubtPlacements]`,
`.business_decisions`, `.aggregates[CheckoutSession].transitions`,
`.sync_calls`.

The replay is safe because the body is byte for byte the stored one and the
key is the same: `customer-ordering` replays its first response. No shopper
waits on this path; the BFF polls `GetSession`. The timeline is ordered:
alert at 20 h, give up and void at 22 h, `customer-payments`' void of an
unbound authorization at 24 h as the backstop (hotspot H6, resolved); `Void`
is idempotent, so a race is harmless. Omitted: the backoff schedule, which the
model does not give.

## 5. ExpireSession (scheduler)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as scheduler
    participant UC as ExpireSession
    participant A as CheckoutSession
    participant DB as Postgres
    S->>UC: Execute() on the quote expiry clock
    UC->>DB: load OPEN or DECLINED sessions with quoteExpiresAt before now
    loop each expired session
        UC->>A: ExpireSession(now)
        alt still OPEN or DECLINED
            A-->>UC: EXPIRED
            UC->>DB: BEGIN, save, outbox CheckoutSessionExpired, COMMIT
        else moved on meanwhile
            A-->>UC: ErrSessionClosed
            Note over UC: skip, nothing to expire
        end
    end
    Note over UC,DB: analytics-only CheckoutSessionExpired
```

Derived from: `model.yaml → contexts[checkout].policies[ExpireOnQuoteExpiry]`,
`.aggregates[CheckoutSession].transitions[OPEN, EXPIRED]` and
`[DECLINED, EXPIRED]`, `.publishes[CheckoutSessionExpired]`.

`DECLINED` sessions expire like `OPEN` ones (hotspot H1, resolved). A session that moves to
`AUTHORIZING` between the load and the call is rejected by the version check
and by the aggregate.

## Not drawn

`RecordAuthorization`, `RecordDecline`, `RecordPlacementInDoubt`,
`RecordPlaced`, `RecordRequoteRequired` and `RecordFailure` are the steps
drawn inside diagrams 2 to 4; they have no entry point of their own. `GetSession`
(REST `GET /checkout-sessions/{sessionId}`) reads the repository through its
port and changes nothing.
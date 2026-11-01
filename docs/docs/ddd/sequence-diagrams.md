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
Participant names are the packages and ports of the code, from the
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

Every save is conditional on the loaded `version` (optimistic concurrency; a
conflict re-runs the command once, then answers `409 concurrent-update`).
**No outbound call sits inside a database transaction**: the use case commits
the state it acts from, calls out, then commits the outcome. The
`Idempotency-Key` record is written by the HTTP middleware after the use case
returned, not inside its transaction, and a 5xx is never stored. Full CloudEvents
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
            UC->>C: IssuePriceQuote(cart)<br/>key requestKey:price-quote
            C-->>UC: binding quote, total, expiresAt
            UC->>A: Open(cart, offers, quote, now)
            A-->>UC: OPEN session, lines 1..n, groups
            UC->>DB: BEGIN, insert session, outbox CheckoutOpened, COMMIT
            UC-->>H: 201 session with the binding total
            H->>DB: store key response
            DB-->>K: relay publishes CheckoutOpened (analytics)
        end
    end
```

Source: `internal/adapters/inbound/http/idempotency.go`,
`internal/application/usecases/open_session.go`,
`internal/domain/checkoutsession/open.go` (`Open`, `checkOffers`),
`internal/adapters/outbound/postgres/unit_of_work.go`.

Omitted: the `503` when `Clients` cannot answer (the shopper retries with the
same `Idempotency-Key`), the `422` on invalid input, and the case where an
offer is no longer `QUOTED` at the pre-check, which fails as
`ErrOfferNotQuotable` (`422 offer-not-quotable`, no state change; hotspot H3
on [EventStorming](/docs/ddd/eventstorming)).

## 2. ConfirmSession, part 1: authorize (REST POST .../confirmations)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as ConfirmSession
    participant A as CheckoutSession
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
        UC->>A: Confirm(token, now)
        alt closed, in doubt, void owed or quote expired
            A-->>UC: ErrSessionClosed, ErrPlacementInDoubt,<br/>ErrVoidPending or ErrPriceQuoteExpired
            UC-->>H: 409 or 422 problem
        else OPEN or DECLINED
            A-->>UC: AUTHORIZING, attempt + 1 from DECLINED
            UC->>DB: save, version-checked
            UC->>P: Authorize<br/>key sessionId:auth:attempt
            alt no answer, 409 or 5xx
                P--xUC: unknown outcome
                UC-->>H: 202 AUTHORIZING
            else declined
                P-->>UC: declined
                UC->>A: RecordDecline(reason, now)
                UC->>DB: BEGIN, save DECLINED, outbox CheckoutDeclined, COMMIT
                UC-->>H: 200 DECLINED
                DB-->>K: relay publishes CheckoutDeclined (analytics)
            else authorized
                P-->>UC: authorizationId, total
                UC->>A: RecordAuthorization(authorizationId, total, request)
                alt total or currency differs from the quote
                    A-->>UC: ErrAuthorizationMismatch, DECLINED, void owed
                    UC->>DB: save DECLINED, outbox CheckoutDeclined
                    UC->>P: Void, key sessionId:void
                    UC-->>H: 409 authorization-mismatch
                else equal
                    A-->>UC: PLACING with the stored request
                    UC->>DB: save, version-checked
                    Note over UC,DB: continues with part 2
                end
            end
        end
    end
```

Source: `internal/application/usecases/confirm_session.go`,
`internal/domain/checkoutsession/transitions.go` (`Confirm`,
`RecordAuthorization`, `RecordDecline`),
`internal/adapters/outbound/customerpayments/gateway.go`.

Omitted: the `404` for an unknown session and the `409 concurrent-update` after a
second version conflict. The offer check is not repeated here: it runs at open
only. A `Payments` timeout on authorize leaves the session `AUTHORIZING` and the
call answers 202; `ResolveStuckAuthorizations` re-calls after 2 minutes with the
same key (hotspot H4). A mismatching total sets the void owed (hotspot H5): the
void is attempted at once and `RetryOwedVoids` repeats it every minute until it
succeeds. A second authorize after a decline is a new attempt with its own key
(hotspot H2).

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
        UC->>DB: BEGIN, save COMPLETED, outbox CheckoutCompleted, COMMIT
        UC-->>H: 200 COMPLETED with orderId
        DB-->>K: relay publishes CheckoutCompleted (events topic)
    else definitive 409, requoted offers
        O-->>UC: requote, translated by the ACL
        UC->>A: RecordRequoteRequired(offers, now)<br/>voidOwed = authorizationId
        UC->>DB: BEGIN, save REQUOTE_REQUIRED, outbox CheckoutRequoteRequired, COMMIT
        UC->>P: Void<br/>key sessionId:void
        P-->>UC: voided (else RetryOwedVoids retries)
        UC-->>H: 200 REQUOTE_REQUIRED with requotedOffers
    else 400 or 422, contract bug
        O-->>UC: rejected
        UC->>A: RecordFailure(code, now)<br/>voidOwed = authorizationId
        UC->>DB: BEGIN, save FAILED, outbox CheckoutFailed, COMMIT
        UC->>P: Void<br/>key sessionId:void
        P-->>UC: voided (else RetryOwedVoids retries)
        UC-->>H: 200 FAILED, alert raised
    else timeout or 503
        O--xUC: no definitive answer
        UC->>A: RecordPlacementInDoubt(now)
        Note over A: no void, no new attempt
        UC->>DB: BEGIN, save (stays PLACING), outbox CheckoutPlacementInDoubt on the first time, COMMIT
        UC-->>H: 202 PLACING
    end
```

Source: `internal/application/usecases/settle.go` (`settlePlacement`,
`settleVoid`), `internal/adapters/outbound/customerordering/gateway.go` (`Place`),
`internal/domain/checkoutsession/terminals.go` and `transitions.go`.

The stored request is PII-free and carries `shipToAddressId`, never an
address; the `Idempotency-Key` towards the network is the `sessionId`. The
`REQUOTE_REQUIRED` and `FAILED` states are recorded together with
the owed void, in one transaction, before the void is attempted; a failing void
is retried every minute by `RetryOwedVoids` (hotspot H5). Omitted:
the mapping of the network's wire names, which happens inside `ACL`
(`internal/adapters/outbound/customerordering`; hotspot H8).

## 4. ResolveInDoubtPlacements (scheduler)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 100, "actorMargin": 8, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as scheduler
    participant UC as ResolveInDoubtPlacements
    participant A as CheckoutSession
    participant O as ACL
    participant P as Payments
    participant DB as Postgres
    participant K as Relay / Kafka
    S->>UC: Execute(200) every 15 s
    UC->>DB: ListPlacingDue(now)
    loop each due PLACING session
        opt in doubt for 20 h or more, not yet alerted
            UC->>A: MarkInDoubtAlerted(now)
            Note over UC: alert placement-in-doubt-20h
        end
        UC->>O: PlaceOrder(stored request)<br/>key sessionId
        alt order accepted
            O-->>UC: orderId
            UC->>A: RecordPlaced(orderId, now)
            UC->>DB: BEGIN, save COMPLETED, outbox CheckoutCompleted, COMMIT
            DB-->>K: relay publishes CheckoutCompleted
        else definitive 409, 400 or 422
            O-->>UC: requote or rejected
            UC->>A: RecordRequoteRequired or RecordFailure<br/>void owed
            UC->>DB: BEGIN, save, outbox event, COMMIT
            DB-->>K: relay publishes the analytics event
            UC->>P: Void<br/>key sessionId:void
        else no definitive answer
            O--xUC: timeout, 5xx or retriable 409
            alt in doubt for 22 h or more
                UC->>A: RecordFailure(placement-unresolved)<br/>void owed
                UC->>DB: BEGIN, save FAILED, outbox CheckoutFailed, COMMIT
                Note over UC: alert placement-given-up-22h
                UC->>P: Void<br/>key sessionId:void
            else
                UC->>A: RecordPlacementInDoubt(now)<br/>next attempt after backoff
                UC->>DB: save PLACING
            end
        end
    end
```

Source: `internal/application/usecases/resolve_in_doubt.go`,
`settle.go`, `internal/domain/checkoutsession/transitions.go` (`backoff`) and
`terminals.go` (`AlertDue`, `GiveUpDue`), `internal/adapters/inbound/scheduler/scheduler.go`.

The replay is safe because the body is byte for byte the stored one and the
key is the same: customer-ordering is expected to replay its first response. No
shopper waits on this path; the BFF polls `GetSession`. The next attempt of a
session still in doubt is pushed out by a backoff that starts at 30 s and
doubles up to 15 minutes. The timeline is ordered: alert at 20 h, give up and
void at 22 h, customer-payments' own void of an unbound authorization at 24 h as
the backstop (hotspot H6; that policy is not in this repository); `Void` is
idempotent, so a race is harmless.

## 5. ExpireSession (scheduler)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 110, "actorMargin": 12, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as scheduler
    participant UC as ExpireSession
    participant A as CheckoutSession
    participant DB as Postgres
    S->>UC: Execute(200) every minute
    UC->>DB: ListExpirable(now)
    loop each expired session
        UC->>A: ExpireSession(now)
        alt still OPEN or DECLINED
            A-->>UC: EXPIRED
            UC->>DB: BEGIN, save, outbox CheckoutSessionExpired, COMMIT
        else moved on meanwhile
            A-->>UC: ErrSessionClosed or illegal transition
            Note over UC: skip, nothing to expire
        end
    end
    Note over UC,DB: analytics-only CheckoutSessionExpired
```

Source: `internal/application/usecases/policies.go` (`ExpireOnQuoteExpiry`),
`internal/domain/checkoutsession/terminals.go` (`ExpireSession`).

`DECLINED` sessions expire like `OPEN` ones (hotspot H1). A session that moves to
`AUTHORIZING` between the load and the call is rejected by the aggregate
(`illegal-transition`) or by the version check, and the sweep skips it.

## Not drawn

`RecordAuthorization`, `RecordDecline`, `RecordPlacementInDoubt`,
`RecordPlaced`, `RecordRequoteRequired` and `RecordFailure` are the steps
drawn inside diagrams 2 to 4; they have no entry point of their own. `GetSession`
(REST `GET /checkout-sessions/{sessionId}`) reads the repository through its
port and changes nothing.
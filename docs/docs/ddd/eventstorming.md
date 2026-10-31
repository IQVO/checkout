---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
description: Process-level EventStorming boards for Checkout, covering opening a session, authorize and place, the three definitive failures, in-doubt resolution and expiry, with the hotspots taken from gaps and risks in the model, most now resolved.
---

# EventStorming

Process-level boards in the ddd-crew
[EventStorming glossary & cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet)
notation. Every sticky comes from `model.yaml`. Events published by a
neighbour are drawn as orange stickies prefixed with the producer. Every
hotspot is a gap or a risk the model itself shows, and its source is given
in the hotspot table. Boards are drawn top to bottom; each stays within the page width.

## Legend

| Colour | Sticky | Meaning here |
| --- | --- | --- |
| Pale yellow `#fff59d` | Actor | The shopper (through the BFF), the clock |
| Blue `#a5d8ff` | Command | A command from `aggregates[CheckoutSession].commands` |
| Yellow `#ffec99` | Aggregate | The `CheckoutSession` aggregate and its state |
| Orange `#ffc078` | Domain Event | Ours (`publishes`) or a neighbour's (prefixed with the producer) |
| Lilac `#d0bfff` | Policy | `policies`, or the reaction a neighbour takes to an event |
| Pink `#fcc2d7` | External System | A sibling context or the network (`shopping-cart`, `pricing`, `customer-payments`, `delivery-promise`, `customer-ordering`, analytics) |
| Red `#ff8787` | Hotspot | A gap or risk in the model (see the table at the end) |

## 1. Opening a session

```mermaid
flowchart TB
    S["Shopper<br/>via storefront-bff"]:::actor
    C0["OpenSession<br/>POST /checkout-sessions"]:::command
    SC["shopping-cart"]:::external
    DP["delivery-promise<br/>(network)"]:::external
    PR["pricing"]:::external
    G1["CheckoutSession<br/>OPEN"]:::aggregate
    E1["CheckoutOpened"]:::event
    AN(["analytics"]):::actor
    ERR["ErrOfferNotQuotable<br/>422 offer-not-quotable<br/>no state change"]:::aggregate

    S --> C0
    C0 -->|"qry GetCart,<br/>ErrCartNotCheckoutReady"| SC
    C0 -->|"qry GetDeliveryOffer<br/>per selected offer"| DP
    C0 -->|"cmd IssuePriceQuote"| PR
    SC --> G1
    DP --> G1
    PR -->|"binding quote,<br/>lines 1..n, groups"| G1
    G1 --> E1
    E1 --> AN
    DP -->|"an offer not QUOTED<br/>at the pre-authorize check"| ERR

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Derived from: `model.yaml → contexts[checkout].inbound` (BFF cmd row),
`.sync_calls` (`GetCart`, `GetDeliveryOffer`, `IssuePriceQuote`),
`.aggregates[CheckoutSession].invariants` (`ErrCartNotCheckoutReady`),
`.publishes[CheckoutOpened]`; gap S1 and S2 on the
[Model Gap Register](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/model-gap-register).

The pre-authorize `GetDeliveryOffer` check is repeated at confirm; an offer
that is no longer `QUOTED` (with margin) fails with `ErrOfferNotQuotable`
(`422 offer-not-quotable`), changes no state, and the BFF refreshes the cart
quote. `REQUOTE_REQUIRED` stays a `PLACING`-time `409` only.

Omitted: the `503` when a dependency is unreachable, and the outbox. The
price quote is binding and expiring; its expiry becomes the session's
`quoteExpiresAt`, the clock for board 6.

## 2. Authorize, then place the order

```mermaid
flowchart TB
    S["Shopper<br/>via storefront-bff"]:::actor
    C1["ConfirmSession<br/>POST ...:confirm<br/>ErrPriceQuoteExpired"]:::command
    G1["CheckoutSession<br/>OPEN to AUTHORIZING"]:::aggregate
    CP["customer-payments"]:::external
    C2["RecordAuthorization<br/>ErrAuthorizationMismatch"]:::command
    G2["CheckoutSession<br/>PLACING<br/>placement request stored"]:::aggregate
    CO["customer-ordering<br/>(network, via the ACL)"]:::external
    C3["RecordPlaced"]:::command
    G3["CheckoutSession<br/>COMPLETED"]:::aggregate
    E1["CheckoutCompleted"]:::event
    P1["shopping-cart<br/>CloseOnCheckoutCompleted"]:::policy
    P2["customer-payments<br/>BindOrderOnCheckoutCompleted"]:::policy
    P3["ResolveStuckAuthorizations<br/>AUTHORIZING over 2 min,<br/>same key"]:::policy

    S --> C1
    C1 --> G1
    G1 -->|"cmd AuthorizePayment<br/>key sessionId:auth:attempt"| CP
    CP -->|authorized| C2
    C2 --> G2
    G2 -->|"cmd PlaceOrder<br/>key sessionId"| CO
    CO -->|"order accepted"| C3
    C3 --> G3
    G3 --> E1
    E1 --> P1
    E1 --> P2
    P3 -->|"re-call, same key"| CP
    G1 --- P3

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Derived from: `model.yaml → contexts[checkout].aggregates[CheckoutSession]`
(`transitions`, `invariants`, `commands`), `.sync_calls`
(`AuthorizePayment`, `PlaceOrder`), `.publishes[CheckoutCompleted]`;
`contexts[customer-payments].policies[BindOrderOnCheckoutCompleted]`;
`contexts[shopping-cart].policies[CloseOnCheckoutCompleted]`;
`.policies[ResolveStuckAuthorizations]`.

`ResolveStuckAuthorizations` settles an unknown authorize outcome: a session
in `AUTHORIZING` for more than 2 minutes re-calls `AuthorizePayment` with the
same key `<sessionId>:auth:<authAttempt>`, which is idempotent and yields the
true outcome; `customer-payments`' 24 h void stays the backstop.

Omitted: the failure paths (boards 3 and 4), the in-doubt branch (board 5) and
the outbox. The money flow (D5) continues outside this context: capture on
`FulfillmentShipped`, release on `FulfillmentCancelled`, refund on
`PackageLost` or `PackageReturned` all belong to `customer-payments`.

## 3. A decline

```mermaid
flowchart TB
    A1["CheckoutSession<br/>AUTHORIZING"]:::aggregate
    CP["customer-payments"]:::external
    C4["RecordDecline"]:::command
    D1["CheckoutSession<br/>DECLINED<br/>another token allowed"]:::aggregate
    E2["CheckoutDeclined<br/>analytics only"]:::event
    X1["DECLINED to EXPIRED<br/>ExpireOnQuoteExpiry"]:::aggregate
    K1["authAttempt + 1<br/>key sessionId:auth:attempt"]:::command

    A1 -->|"cmd AuthorizePayment:<br/>declined"| CP
    CP --> C4
    C4 --> D1
    D1 --> E2
    D1 -->|"ConfirmSession again"| K1
    K1 --> A1
    D1 -->|"quote expires"| X1

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Derived from: `model.yaml → contexts[checkout].business_decisions` (decline
bullet), `.aggregates[CheckoutSession].transitions` (`RecordDecline`,
`DECLINED → AUTHORIZING`), `.publishes[CheckoutDeclined]`;
`contexts[customer-payments].publishes[PaymentDeclined]`.

A decline voids nothing: no authorization exists. The session stays open for
as long as the price quote is valid; `DECLINED → EXPIRED` is a modelled
transition (`ExpireSession`). Each `DECLINED → AUTHORIZING` increments the
session field `authAttempt`, so a retry with another token is a new attempt
under a new key `<sessionId>:auth:<authAttempt>` and is never answered with the
first, declined response, while a stuck call replays the same key.

## 4. Requote and contract failure

```mermaid
flowchart TB
    PL["CheckoutSession<br/>PLACING"]:::aggregate
    CO["customer-ordering<br/>(via the ACL)"]:::external
    V["VoidAuthorization<br/>key sessionId:void"]:::command
    C5["RecordRequoteRequired"]:::command
    G4["REQUOTE_REQUIRED<br/>requotedOffers"]:::aggregate
    E3["CheckoutRequoteRequired"]:::event
    C6["RecordFailure<br/>and an alert"]:::command
    G5["FAILED<br/>failure code"]:::aggregate
    E4["CheckoutFailed"]:::event
    OW["voidOwed = authorizationId<br/>set with the decision"]:::aggregate
    P4["RetryOwedVoids<br/>every minute until success"]:::policy

    PL -->|"cmd PlaceOrder"| CO
    CO -->|"definitive 409<br/>or 422"| V
    V -->|"after 409"| C5
    V -->|"after 422"| C6
    C5 --> G4
    G4 --> E3
    C6 --> G5
    G5 --> E4
    C5 --> OW
    C6 --> OW
    OW --> P4
    P4 -->|"idempotent void"| V

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Derived from: `model.yaml → contexts[checkout].business_decisions` (409 and
422 bullets), `.aggregates[CheckoutSession].transitions`,
`.sync_calls[VoidAuthorization]` ("only after a definitive rejection"),
`.publishes` (`CheckoutRequoteRequired`, `CheckoutFailed`), `.ul[Requote]`;
`.policies[RetryOwedVoids]`; `contexts[customer-payments].inbound`
(`VoidAuthorization`).

Every definitive rejection (`409`, `422`, and an `ErrAuthorizationMismatch`)
sets the session field `voidOwed` (the `authorizationId`) in the same
transaction as the decision to void, so no void is lost. The void is attempted
at once; if it fails, `RetryOwedVoids` runs every minute until it succeeds
(`Void` is idempotent, key `<sessionId>:void`). Both events are
analytics-only. Omitted: the shopper's next attempt after `REQUOTE_REQUIRED`,
which starts from the cart with a new session (hotspot H10 on how the cart
takes the requoted offers).

## 5. In-doubt placement

```mermaid
flowchart TB
    PL["CheckoutSession<br/>PLACING"]:::aggregate
    CO["customer-ordering<br/>(via the ACL)"]:::external
    C7["RecordPlacementInDoubt<br/>timeout or 503"]:::command
    LOCK["PLACING stays PLACING<br/>semantic lock, reply 202"]:::aggregate
    P1["ResolveInDoubtPlacements<br/>same body and key,<br/>alert at 20 h,<br/>void at 22 h"]:::policy
    R1["RecordPlaced"]:::command
    R2["RecordRequoteRequired<br/>or RecordFailure"]:::command
    E1["CheckoutCompleted"]:::event
    E5["CheckoutPlacementInDoubt<br/>analytics only"]:::event
    TL["20 h alert, 22 h give up<br/>and void, 24 h backstop"]:::policy

    PL -->|"cmd PlaceOrder"| CO
    CO -->|"no definitive answer"| C7
    C7 --> LOCK
    LOCK --> P1
    P1 -->|"replay"| CO
    CO -->|"order accepted"| R1
    CO -->|"409 or 422, then board 4"| R2
    R1 --> E1
    C7 --> E5
    P1 --> TL

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Derived from: `model.yaml → contexts[checkout].policies[ResolveInDoubtPlacements]`,
`.business_decisions` (503 or timeout bullet), `.aggregates[CheckoutSession]`
(`RecordPlacementInDoubt`, `ErrPlacementInDoubt`), `.ul[In-Doubt Placement]`;
`.publishes[CheckoutPlacementInDoubt]`;
`contexts[customer-payments].policies[VoidUnboundAuthorizations]`.

The lock is a business state: a restart does not release it, and the
resolver runs from the stored request (`StoredPlacement`, with `placingSince`
driving the clock). The timeline is ordered: the alert at 20 h, then
`ResolveInDoubtPlacements` gives up and voids at 22 h, then
`customer-payments`' `VoidUnboundAuthorizations` as the backstop at 24 h. All
before the network's 24 h idempotency window closes. `Void` is idempotent, so
a race between the resolver and the backstop is harmless.
`RecordPlacementInDoubt` raises the analytics-only
`CheckoutPlacementInDoubt`.

## 6. Expiry and terminal states

```mermaid
flowchart TB
    T(["Clock<br/>quote expiry"]):::actor
    P1["ExpireOnQuoteExpiry<br/>OPEN or DECLINED"]:::policy
    C8["ExpireSession"]:::command
    G1["CheckoutSession<br/>EXPIRED"]:::aggregate
    G2["COMPLETED, EXPIRED,<br/>FAILED, REQUOTE_REQUIRED<br/>terminal: ErrSessionClosed"]:::aggregate
    E6["CheckoutSessionExpired<br/>analytics only"]:::event

    T --> P1
    P1 --> C8
    C8 --> G1
    G1 --> G2
    G1 --> E6

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
```

Derived from: `model.yaml → contexts[checkout].policies[ExpireOnQuoteExpiry]`,
`.aggregates[CheckoutSession]` (`states`, `transitions`,
`ErrSessionClosed`), `.publishes[CheckoutSessionExpired]`.

`ExpireSession` covers `OPEN → EXPIRED` and `DECLINED → EXPIRED` and raises
the analytics-only `CheckoutSessionExpired`. `ErrSessionClosed` covers
`COMPLETED`, `EXPIRED`, `FAILED` and `REQUOTE_REQUIRED`; a
`DECLINED` session is not closed (it retries with another token or expires).

Omitted: the same-shaped `COMPLETED`, `FAILED` and `REQUOTE_REQUIRED`
terminals, drawn on boards 2 and 4. A session in `AUTHORIZING` or `PLACING`
is not touched by this policy: only an `OPEN` or `DECLINED` session expires
with its quote.

## Hotspots

| # | Hotspot | Where it comes from in the model |
| --- | --- | --- |
| H1 | **Resolved in the model-fix:** `DECLINED → EXPIRED` is a transition (`ExpireSession`, policy `ExpireOnQuoteExpiry`). `DECLINED` is not closed: it retries with another token or expires. `ErrSessionClosed` lists `COMPLETED`, `EXPIRED`, `FAILED` and `REQUOTE_REQUIRED` only. | `policies[ExpireOnQuoteExpiry]` vs `aggregates[CheckoutSession].transitions` |
| H2 | **Resolved in the model-fix:** `AuthorizePayment` is keyed `<sessionId>:auth:<authAttempt>`. The session field `authAttempt` starts at 1 and is incremented on each `DECLINED → AUTHORIZING`, so a retry with another token is a new attempt while a stuck call replays the same key. | `sync_calls[AuthorizePayment]` (note), `transitions` (`DECLINED → AUTHORIZING`), `customer-payments` `ErrDuplicateAuthorization` |
| H3 | **Resolved in the model-fix:** `ErrOfferNotQuotable` (`422 offer-not-quotable`) is raised by the pre-authorize `GetDeliveryOffer` check; no state change, the BFF refreshes the cart quote. `REQUOTE_REQUIRED` stays a `PLACING`-time `409` only. | `sync_calls[GetDeliveryOffer]` (note) vs `transitions`, `invariants` |
| H4 | **Resolved in the model-fix:** the policy `ResolveStuckAuthorizations` re-calls `AuthorizePayment` with the same key for a session in `AUTHORIZING` for more than 2 minutes; `customer-payments`' 24 h void stays the backstop. | `transitions`, `contexts[customer-payments].policies` |
| H5 | **Resolved in the model-fix:** the session field `voidOwed` (`authorizationId`) is set in the same transaction as the decision to void (definitive `409`, `422`, `ErrAuthorizationMismatch`); the policy `RetryOwedVoids` retries every minute until success. `Void` is idempotent. | `sync_calls[VoidAuthorization]`, `storefront/apis/checkout/openapi.yaml` |
| H6 | **Resolved in the model-fix:** an ordered timeline: alert at 20 h, `ResolveInDoubtPlacements` gives up and voids at 22 h, `customer-payments` `VoidUnboundAuthorizations` backstop at 24 h. A race between the two voids is harmless. | `policies[ResolveInDoubtPlacements]` vs `contexts[customer-payments].policies[VoidUnboundAuthorizations]` |
| H7 | **Resolved in the model-fix:** two new analytics-only events, `CheckoutSessionExpired` (`ExpireSession`) and `CheckoutPlacementInDoubt` (`RecordPlacementInDoubt`). `ConfirmSession` and `RecordAuthorization` stay eventless: `CheckoutCompleted` already covers the outcome. | `aggregates[CheckoutSession].commands` vs `publishes` |
| H8 | `shipToAddressId` (NG1), `offer-not-quotable` and `requotedOffers` are named by the storefront model; the network model did not carry them when this pack was written. **Resolved:** the network model's customer-ordering rows and customer-ordering's `apis/openapi.yaml` on develop now carry all three (`POST /checkout`). | `sync_calls[PlaceOrder]`, `business_decisions`, `meta.hard_boundary` vs `network/model/model.yaml → contexts[customer-ordering]` |
| H9 | **Resolved in the model-fix:** the value object `StoredPlacement{requestBody, requestHash, placingSince}`; the PII-free request replayed byte for byte, `placingSince` drives the 20 h alert, purged 24 h after a terminal state. | `purpose`, `aggregates[CheckoutSession].description`, `.size_estimate` |
| H10 | The REST contract says the BFF writes `requotedOffers` into the cart as a new quote, but the cart's commands quote only through `delivery-promise` (`QuoteDelivery`); none accepts an offer set from outside. **Resolved in the model and built:** shopping-cart's `AcceptRequotedOffers` (`POST /carts/{cartId}/delivery/requoted`); `storefront-bff` has no route that calls it yet. | `storefront/apis/checkout/openapi.yaml` (`CheckoutSession.requotedOffers`) vs `contexts[shopping-cart].aggregates[Cart].commands` |
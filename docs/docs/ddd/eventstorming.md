---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
description: Process-level EventStorming boards for Checkout, covering opening a session, authorize and place, the three definitive failures, in-doubt resolution and expiry, with the design-time hotspots and how the code settled them.
---

# EventStorming

Process-level boards in the ddd-crew
[EventStorming glossary & cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet)
notation. Every sticky was checked against the aggregate in `internal/domain/checkoutsession` and
the use cases in `internal/application/usecases`; each board ends with a
`Source:` line. Events published by a neighbour are drawn as orange stickies
prefixed with the producer. Every hotspot is a design-time question, and the
hotspot table says how the code settled it. Boards are drawn top to bottom;
each stays within the page width.

## Legend

| Colour | Sticky | Meaning here |
| --- | --- | --- |
| Pale yellow `#fff59d` | Actor | The shopper (through the BFF), the clock |
| Blue `#a5d8ff` | Command | A command of the `CheckoutSession` aggregate (`internal/domain/checkoutsession/transitions.go`, `terminals.go`) |
| Yellow `#ffec99` | Aggregate | The `CheckoutSession` aggregate and its state |
| Orange `#ffc078` | Domain Event | One of our seven (`internal/domain/checkoutsession/events.go`) or a neighbour's (prefixed with the producer) |
| Lilac `#d0bfff` | Policy | One of the five saga policies (`internal/application/usecases`), or the reaction a neighbour takes to an event |
| Pink `#fcc2d7` | External System | A sibling context or the network (`shopping-cart`, `pricing`, `customer-payments`, `delivery-promise`, `customer-ordering`, analytics) |
| Red `#ff8787` | Hotspot | A design-time question and how the code settled it (see the table at the end) |

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
    DP -->|"an offer not QUOTED<br/>or expiring within 2 min"| ERR

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Source: `internal/application/usecases/open_session.go` (`GetCart`,
`GetOffer`, `IssueQuote`), `internal/domain/checkoutsession/open.go`
(`ErrCartNotCheckoutReady`, `ErrOfferNotQuotable`, `CheckoutOpened`). The
storefront gaps S1 and S2 are on the
[Model Gap Register](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/model-gap-register).

The `GetDeliveryOffer` check runs once, at open. An offer that is not `QUOTED`,
or expires within 2 minutes, fails with `ErrOfferNotQuotable`
(`422 offer-not-quotable`), changes no state, and the BFF refreshes the cart
quote. The code does not repeat the check at confirm: an offer that expires
between open and confirm comes back from customer-ordering as a
`PLACING`-time `409`, which is the only way to reach `REQUOTE_REQUIRED`.

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

Source: `internal/domain/checkoutsession/transitions.go` and `terminals.go`
(`Confirm`, `RecordAuthorization`, `RecordPlaced`),
`internal/application/usecases/confirm_session.go` (`Authorize` then `Place`),
`policies.go` (`ResolveStuckAuthorizations`) and the `CheckoutCompleted`
consumers named on [Integration](/docs/ecosystem/integration).

`ResolveStuckAuthorizations` settles an unknown authorize outcome: a session
in `AUTHORIZING` for more than 2 minutes re-calls `AuthorizePayment` with the
same key `<sessionId>:auth:<attempt>`, which is idempotent and yields the
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
    K1["attempt + 1<br/>key sessionId:auth:attempt"]:::command

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

Source: `internal/domain/checkoutsession/transitions.go` (`RecordDecline`,
`Confirm` from `DECLINED`), `internal/adapters/outbound/customerpayments/gateway.go`
(what counts as a decline).

A decline voids nothing: no authorization exists. The session stays open for
as long as the price quote is valid; `DECLINED → EXPIRED` is a
transition (`ExpireSession`). Each `DECLINED → AUTHORIZING` increments the
session's attempt counter (it starts at 0), so a retry with another token is a new attempt
under a new key `<sessionId>:auth:<attempt>` and is never answered with the
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
    CO -->|"definitive 409"| C5
    CO -->|"400 or 422"| C6
    C5 --> G4
    G4 --> E3
    C6 --> G5
    G5 --> E4
    C5 --> OW
    C6 --> OW
    OW -->|"voided at once"| V
    OW --> P4
    P4 -->|"idempotent void"| V

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Source: `internal/domain/checkoutsession/terminals.go`
(`RecordRequoteRequired`, `RecordFailure`), `internal/application/usecases/settle.go`
(`settlePlacement`, `settleVoid`), `policies.go` (`RetryOwedVoids`),
`internal/adapters/outbound/customerordering/gateway.go` (the 409, 400 and 422
mapping), `internal/adapters/outbound/customerpayments/gateway.go` (`Void`).

Every definitive rejection (a definitive `409`, a `400` or `422`, and an `ErrAuthorizationMismatch`)
sets the session's owed void (the `authorizationId`, column `void_authorization_id`) in the same
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
    CO -->|"409, 400 or 422, then board 4"| R2
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

Source: `internal/application/usecases/resolve_in_doubt.go`,
`internal/domain/checkoutsession/transitions.go` (`RecordPlacementInDoubt`,
`backoff`) and `terminals.go` (`AlertDue`, `GiveUpDue`). The 24 h backstop is
customer-payments' own policy and is not built in this repository.

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

Source: `internal/application/usecases/policies.go` (`ExpireOnQuoteExpiry`),
`internal/domain/checkoutsession/terminals.go` (`ExpireSession`) and
`session.go` (`State.Closed`).

`ExpireSession` covers `OPEN → EXPIRED` and `DECLINED → EXPIRED` and raises
the analytics-only `CheckoutSessionExpired`. `ErrSessionClosed` covers
`COMPLETED`, `EXPIRED`, `FAILED` and `REQUOTE_REQUIRED`; a
`DECLINED` session is not closed (it retries with another token or expires).

Omitted: the same-shaped `COMPLETED`, `FAILED` and `REQUOTE_REQUIRED`
terminals, drawn on boards 2 and 4. A session in `AUTHORIZING` or `PLACING`
is not touched by this policy: only an `OPEN` or `DECLINED` session expires
with its quote.

## Hotspots

The hotspots are the design-time questions the storefront design raised
(H1 to H10). The table says how the code answers each; the same list with the
bounded-context view is on the
[Bounded Context Canvas](/docs/ddd/bounded-context-canvas#decisions-taken-since-the-design-model-and-what-stays-open).

| # | Hotspot | How the code answers it | Where |
| --- | --- | --- | --- |
| H1 | Where does `DECLINED` go when the quote expires? | Settled: `DECLINED` is not closed. `ExpireSession` accepts `OPEN` and `DECLINED` (policy `ExpireOnQuoteExpiry`); `ErrSessionClosed` covers `COMPLETED`, `EXPIRED`, `FAILED` and `REQUOTE_REQUIRED` only. | `internal/domain/checkoutsession/terminals.go`, `session.go` |
| H2 | What key does a second authorize use? | Settled: `<sessionId>:auth:<attempt>`. The counter starts at 0 and grows by one on each `DECLINED → AUTHORIZING`, so a retry with another token is a new attempt while a stuck call replays the same key. | `transitions.go` (`Confirm`), `usecases/confirm_session.go` |
| H3 | What if an offer is no longer quotable? | Settled at open only: `ErrOfferNotQuotable` (`422 offer-not-quotable`), no state change. There is no second check at confirm; a late expiry surfaces as a definitive `409` from customer-ordering and ends in `REQUOTE_REQUIRED`. | `open.go` (`checkOffers`), `customerordering/gateway.go` |
| H4 | What ends `AUTHORIZING` when the outcome is unknown? | Settled: `ResolveStuckAuthorizations` re-calls `AuthorizePayment` with the same key for a session in `AUTHORIZING` for more than 2 minutes. | `usecases/policies.go`, `transitions.go` (`StuckAuthorizationAfter`) |
| H5 | What does a failed void do? | Settled: the owed void is stored with the decision (a definitive `409`, `400`, `422` or `ErrAuthorizationMismatch`) and `RetryOwedVoids` retries every minute. `Void` is idempotent. | `usecases/settle.go`, `policies.go` |
| H6 | The resolver and the 24 h void race. | Settled: an ordered timeline: alert at 20 h, `ResolveInDoubtPlacements` gives up and voids at 22 h. The customer-payments backstop at 24 h is that service's own policy; a race between the two voids is harmless. | `usecases/resolve_in_doubt.go` |
| H7 | Silent outcomes. | Settled: two analytics-only events, `CheckoutSessionExpired` and `CheckoutPlacementInDoubt`. `Confirm` and `RecordAuthorization` stay eventless; `CheckoutCompleted` covers the outcome. | `events.go` |
| H8 | The network contract (`shipToAddressId`, `offer-not-quotable`, `requotedOffers`). | Settled in the gateway: the place-order body carries `shipToAddressId`, and a definitive `409` carries `requotedOffers`. The wire names stay inside the ACL. | `internal/adapters/outbound/customerordering` |
| H9 | The stored placement request. | Settled: `StoredPlacement{RequestBody, RequestHash, PlacingSince}`, replayed byte for byte, `PlacingSince` drives the 20 h alert, purged 24 h after a closed state. | `transitions.go`, `terminals.go` (`PurgePlacement`) |
| H10 | How does the cart take the requoted offers? | Open. This service only carries `requotedOffers` on the `REQUOTE_REQUIRED` session and in `CheckoutRequoteRequired`. Whether and how the BFF writes them into the cart (shopping-cart `AcceptRequotedOffers`) is outside this repository and not verified here. | `usecases/settle.go` |

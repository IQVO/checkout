---
id: class-diagram
title: Class Diagram
sidebar_label: Class Diagram
description: UML class diagrams of Checkout's domain model, its seven events and the application layer, and the hexagonal ports-and-adapters layout with the Anti-Corruption Layer as one outbound adapter.
---

# Class Diagram

UML class diagrams of the Go packages on `develop`: the domain package
`internal/domain/checkoutsession` (with `internal/domain/shared`), the ports of
`internal/application/ports` and the adapters. Fields are unexported in Go and
shown with `-`; methods are shown with `+`. The names are the built
identifiers; the [Ubiquitous Language](/docs/ddd/ubiquitous-language) page maps
each business term to its identifier.

## 1. The CheckoutSession aggregate

```mermaid
classDiagram
    direction TB
    class CheckoutSession {
        <<AggregateRoot>>
        -id ID
        -state State
        -cartId string
        -cartVersion int64
        -shopperId string
        -shipToAddressId string
        -lines Line[]
        -groups Group[]
        -priceQuoteId string
        -currency string
        -totalMinor int64
        -quoteExpiresAt time.Time
        -authorizationId string
        -orderId string
        -requoted RequotedOffer[]
        -failureCode string
        -declineReason string
        -authAttempt int
        -pendingToken string
        -authorizingSince time.Time
        -placement StoredPlacement
        -placementTries int
        -nextAttemptAt time.Time
        -alertedAt time.Time
        -voidOwed VoidOwed
        -terminalAt time.Time
        -version int64
        +Confirm(token, now) error
        +RecordAuthorization(authorizationId, authorizedMinor, currency, request, now) error
        +RecordDecline(reason, now) error
        +RecordPlacementInDoubt(now) error
        +RecordPlaced(orderId, now) error
        +RecordRequoteRequired(offers, now) error
        +RecordFailure(code, now) error
        +ExpireSession(now) error
        +VoidCompleted(now) error
        +MarkInDoubtAlerted(now) error
        +PurgePlacement(now) error
        +PullEvents() Event[]
    }
    class Line {
        <<ValueObject>>
        +LineNo int
        +CatalogItemID string
        +Quantity int
    }
    class Group {
        <<ValueObject>>
        +GroupNo int
        +OfferID string
        +Tier Tier
        +LineNos int[]
        +AmountMinor int64
    }
    class RequotedOffer {
        <<ValueObject>>
        +OfferID string
        +GroupNo int
        +Tier Tier
        +DeliverByEarliest string
        +DeliverByLatest string
        +ExpiresAt time.Time
    }
    class StoredPlacement {
        <<ValueObject>>
        +RequestBody bytes
        +RequestHash string
        +PlacingSince time.Time
    }
    class VoidOwed {
        <<ValueObject>>
        +AuthorizationID string
    }
    CheckoutSession *-- "1..100" Line : lines
    CheckoutSession *-- "1..*" Group : groups
    CheckoutSession *-- "0..*" RequotedOffer : requoted
    CheckoutSession *-- "0..1" StoredPlacement : placement
    CheckoutSession *-- "0..1" VoidOwed : voidOwed
```

Source: `internal/domain/checkoutsession/aggregate.go` (the fields),
`transitions.go` and `terminals.go` (the commands), `values.go` (the value
objects), `open.go` (`Open`, the factory function that returns an `OPEN`
session).

`State` is the string enumeration of the eight states `OPEN`, `AUTHORIZING`,
`DECLINED`, `PLACING`, `REQUOTE_REQUIRED`, `COMPLETED`, `EXPIRED` and `FAILED`;
`Tier` is `SAME_DAY`, `NEXT_DAY`, `TWO_DAY` or `STANDARD`, re-declared locally
(`session.go`): there is no shared kernel with `delivery-promise`.
`RequotedOffer` carries its delivery bounds as `YYYY-MM-DD` strings. The
`pendingToken` is kept only while `AUTHORIZING`. Bounds: at most 100 lines
(`MaxLines`), quantity 1 to 99 (`MaxQuantity`). The domain errors
(`ErrCartNotCheckoutReady`, `ErrPriceQuoteExpired`, `ErrSessionClosed`,
`ErrAuthorizationMismatch`, `ErrPlacementInDoubt`, `ErrOfferNotQuotable`,
`ErrVoidPending`, `ErrContractViolation`) are variables in `session.go`, listed
on the [Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas).

## 2. Events

```mermaid
classDiagram
    direction TB
    class Event {
        <<interface>>
        +EventName() string
        +Entity() string
        +SubjectID() string
        +AggregateVersion() int64
        +OccurredAt() time.Time
    }
    class CheckoutOpened {
        <<DomainEvent>>
        +SessionID ID
        +CartID string
        +ShopperID string
        +TotalMinor int64
    }
    class CheckoutDeclined {
        <<DomainEvent>>
        +SessionID ID
        +Reason string
    }
    class CheckoutPlacementInDoubt {
        <<DomainEvent>>
        +SessionID ID
        +PlacingSince time.Time
    }
    class CheckoutSessionExpired {
        <<DomainEvent>>
        +SessionID ID
    }
    class CheckoutFailed {
        <<DomainEvent>>
        +SessionID ID
        +Code string
    }
    Event <|.. CheckoutOpened
    Event <|.. CheckoutDeclined
    Event <|.. CheckoutPlacementInDoubt
    Event <|.. CheckoutSessionExpired
    Event <|.. CheckoutFailed
```

Source: `internal/domain/checkoutsession/events.go` (`CheckoutOpened`,
`CheckoutDeclined`, `CheckoutPlacementInDoubt`, `CheckoutSessionExpired`,
`CheckoutFailed`), `internal/domain/shared/shared.go` (`Event`).

```mermaid
classDiagram
    direction TB
    class Event {
        <<interface>>
    }
    class CheckoutCompleted {
        <<DomainEvent>>
        +SessionID ID
        +OrderID string
        +ShopperID string
        +CartID string
        +AuthorizationID string
        +Currency string
        +TotalMinor int64
        +Groups Group[]
    }
    class CheckoutRequoteRequired {
        <<DomainEvent>>
        +SessionID ID
        +RequotedOffers RequotedOffer[]
    }
    class Header {
        <<shared>>
        +Version int64
        +At time.Time
    }
    Event <|.. CheckoutCompleted
    Event <|.. CheckoutRequoteRequired
    CheckoutCompleted *-- Header
    CheckoutRequoteRequired *-- Header
```

Source: `internal/domain/checkoutsession/events.go`,
`internal/domain/shared/shared.go` (`Event`, `Header`). Every event embeds
`shared.Header` (the aggregate version and the instant). The wire payloads
(`internal/adapters/outbound/kafka/payloads.go`) are camelCase and are
described on [Domain Events](/docs/ddd/domain-events).

## 3. Application layer: use cases and ports

```mermaid
classDiagram
    direction LR
    class Deps {
        <<struct>>
        +Sessions SessionRepository
        +Carts CartReader
        +Offers OfferReader
        +Quoter PriceQuoter
        +Payments PaymentGateway
        +Placer OrderPlacer
        +Outbox EventOutbox
        +UoW UnitOfWork
        +Clock Clock
        +IDs IDGenerator
        +Alerter Alerter
        +Metrics SessionMetrics
    }
    class OpenSession
    class ConfirmSession
    class GetSession
    class ResolveInDoubtPlacements
    class ResolveStuckAuthorizations
    class RetryOwedVoids
    class ExpireOnQuoteExpiry
    class PurgeStoredPlacements
    Deps <|-- OpenSession
    Deps <|-- ConfirmSession
    Deps <|-- GetSession
    Deps <|-- ResolveInDoubtPlacements
    Deps <|-- ResolveStuckAuthorizations
    Deps <|-- RetryOwedVoids
    Deps <|-- ExpireOnQuoteExpiry
    Deps <|-- PurgeStoredPlacements
```

Source: `internal/application/usecases/deps.go` (the `Deps` bundle each use case
embeds), `open_session.go`, `confirm_session.go`, `queries.go`, `policies.go`,
`resolve_in_doubt.go`. `internal/application/ports` declares the interfaces
only (a fitness test enforces it): `SessionRepository`, `EventOutbox`,
`UnitOfWork`, `IdempotencyStore`, `Clock`, `IDGenerator`, `Alerter`,
`SessionMetrics` in `ports.go`; `CartReader`, `OfferReader`, `PriceQuoter`,
`PaymentGateway` and `OrderPlacer` (the Anti-Corruption Layer port) in
`gateways.go`.

## 4. Hexagonal ports and adapters

```mermaid
flowchart TB
    subgraph IN["internal/adapters/inbound"]
        HTTP["http<br/>3 session routes, healthz, readyz"]
        MCP["mcp<br/>get_checkout_session"]
        A2A["a2a<br/>checkout_session_status"]
        SCH["scheduler<br/>5 policy sweeps"]
    end
    APP["<b>internal/application/usecases</b><br/>OpenSession, ConfirmSession, GetSession<br/>ResolveInDoubtPlacements<br/>ResolveStuckAuthorizations<br/>RetryOwedVoids, ExpireOnQuoteExpiry<br/>PurgeStoredPlacements"]
    DOM["<b>internal/domain</b><br/>checkoutsession.CheckoutSession<br/>Line, Group, RequotedOffer<br/>StoredPlacement, VoidOwed<br/>shared"]
    PORTS["<b>internal/application/ports</b><br/>SessionRepository, EventOutbox<br/>UnitOfWork, IdempotencyStore<br/>CartReader, OfferReader, PriceQuoter<br/>PaymentGateway, OrderPlacer<br/>Clock, IDGenerator, Alerter<br/>SessionMetrics"]
    subgraph OUT["internal/adapters/outbound"]
        A1["postgres<br/>repository, outbox,<br/>idempotency store, UoW"]
        A2["outbox + kafka<br/>relay and sink"]
        A3["shoppingcart, deliverypromise,<br/>pricing, customerpayments<br/>REST clients over httpx"]
        A4["customerordering<br/>the Anti-Corruption Layer<br/>POST /checkout"]
        A5["alerts, clock, ids, telemetry"]
    end
    HTTP --> APP
    MCP --> APP
    A2A --> APP
    SCH --> APP
    APP --> DOM
    APP --> PORTS
    PORTS --- A1
    PORTS --- A3
    PORTS --- A4
    PORTS --- A5
    A1 -. outbox rows .-> A2

    classDef outblue fill:#e7f5ff,stroke:#1c7ed6,color:#000
    classDef acl fill:#ffe3e3,stroke:#c92a2a,color:#000
    class A3 outblue
    class A4 acl
```

Source: `cmd/api/adapters.go` (the wiring of the five gateways and the
Postgres adapters), `cmd/api/workers.go` (the relay and the five schedulers),
`cmd/mcp/main.go`, `cmd/a2a/main.go`, `internal/architecture/architecture_test.go`
(`TestHexagonalArchitecture`).

There is **no inbound Kafka consumer** and no inbox: the service consumes no
topic. There are **five outbound REST clients**: four plain clients and the
`customerordering` adapter, the Anti-Corruption Layer and the only place that
knows the network's wire names. `GetSession` is a real use case: the REST,
MCP and A2A adapters all call it. `cmd/mcp` and `cmd/a2a` wire only the read
repository. The `memory` and `repocontract` packages under
`internal/adapters/outbound` are test fixtures and a contract suite; the
runtime binaries must not import them
(`TestRuntimeBinaryHasNoInMemoryAdapters`).

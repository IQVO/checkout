---
id: domain-events
title: Domain Events
sidebar_label: Domain Events
description: Every CloudEvents type Checkout publishes, with topics, keys, payload fields, consumers, idempotency and one complete example envelope; Checkout consumes no event.
---

# Domain Events

Every event this context publishes, with the consuming contexts for the one
integration event. Checkout consumes **no event**. The catalogue is pinned by
`apis/asyncapi.yaml` and checked by `TestEventCatalogueMatchesContract`; the
short form is on the [event catalogue](/docs/api-reference/events). The
domain-wide rules are on
[Event standard: CloudEvents](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/event-standard-cloudevents).

## Envelope

Every Kafka message is a **CloudEvents 1.0 event in structured JSON mode**
(`internal/adapters/kafka/cloudevents/cloudevents.go`):

| Attribute | Value for this context |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID minted once when the event is written to the outbox; a redelivery carries the same `id` |
| `source` | `/retailnetwork/checkout` |
| `type` | `com.retailnetwork.storefront.checkout.checkoutsession.<EventName>` (tier `storefront`, entity `checkoutsession`) |
| `subject` | the aggregate root id (`sessionId`) |
| `time` | the instant the fact happened, UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:checkout:events:CheckoutCompleted:v1` for the integration event; the six analytics-only events carry `urn:retailnetwork:checkout:analytics:<EventName>:v1` ([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue), encoder goldens) |
| Kafka topic | `retailnetwork.checkout.events` for `CheckoutCompleted`; `retailnetwork.checkout.analytics` for the six analytics-only events |
| Kafka key | `sessionId`, so one session's events stay on one partition and in order |

## Published

Every event is written to `outbox_events` in the same transaction as the
aggregate save, and a relay publishes it. `CheckoutCompleted` is the only
integration event; the other six name their consumers as "analytics only"
([Context Map](/docs/ddd/context-map)).

### CheckoutCompleted

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted` |
| Topic | `retailnetwork.checkout.events` |
| Subject / key | `sessionId` |
| Dataschema | `urn:retailnetwork:checkout:events:CheckoutCompleted:v1` |
| Producing command / use case | `RecordPlaced`, after `customer-ordering` accepted the order (first call, or a replay by `ResolveInDoubtPlacements`) |
| Consumers | `shopping-cart`, `customer-payments` |

| Field | Type | Meaning |
| --- | --- | --- |
| `sessionId` | string | The completed session. |
| `orderId` | string | The order the network recorded. |
| `shopperId` | string | A reference to the shopper; no PII. |
| `cartId` | string | The cart the session came from. |
| `authorizationId` | string | The authorization held for the quote total. |
| `currency` | string | The one configured ISO 4217 currency. |
| `totalMinor` | integer | The quote total in minor units. |
| `groups` | array | One entry per shipment group: `groupNo`, `tier`, `amountMinor` (the allocation). |
| `completedAt` | timestamp | When the session completed. |

### CheckoutOpened

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutOpened` |
| Topic | `retailnetwork.checkout.analytics` |
| Subject / key | `sessionId` |
| Dataschema | `urn:retailnetwork:checkout:analytics:CheckoutOpened:v1` |
| Producing command / use case | `OpenSession` |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `sessionId` | string | The session opened. |
| `cartId` | string | The cart it was opened from. |
| `shopperId` | string | A reference to the shopper. |
| `totalMinor` | integer | The binding quote total. |
| `openedAt` | timestamp | When the session opened. |

### CheckoutDeclined

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutDeclined` |
| Topic | `retailnetwork.checkout.analytics` |
| Subject / key | `sessionId` |
| Dataschema | `urn:retailnetwork:checkout:analytics:CheckoutDeclined:v1` |
| Producing command / use case | `RecordDecline` (customer-payments declined the authorization), or `RecordAuthorization` when the authorized amount or currency is not the quote's (reason `authorization-mismatch`) |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `sessionId` | string | The session whose authorization was declined. |
| `reason` | string | The decline reason: `payment-<state>` (for example `payment-declined`) when customer-payments answers 201 in a state other than `AUTHORIZED`, `authorization-rejected: <slug>` on a 422, `declined` when none was given, or `authorization-mismatch`. |
| `at` | timestamp | When the decline was recorded. |

### CheckoutRequoteRequired

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutRequoteRequired` |
| Topic | `retailnetwork.checkout.analytics` |
| Subject / key | `sessionId` |
| Dataschema | `urn:retailnetwork:checkout:analytics:CheckoutRequoteRequired:v1` |
| Producing command / use case | `RecordRequoteRequired` (a definitive `409` from the network, after the authorization was voided) |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `sessionId` | string | The session that needs a requote. |
| `requotedOffers` | array | The re-quoted offers: `offerId`, `groupNo`, `tier`, `deliverByEarliest`, `deliverByLatest`, `expiresAt`. |
| `at` | timestamp | When the requote was recorded. |

### CheckoutFailed

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutFailed` |
| Topic | `retailnetwork.checkout.analytics` |
| Subject / key | `sessionId` |
| Dataschema | `urn:retailnetwork:checkout:analytics:CheckoutFailed:v1` |
| Producing command / use case | `RecordFailure`: a `400` or `422` from the network, a contract bug (code `place-order-rejected`), or the 22 h give-up of an in-doubt placement (code `placement-unresolved`). The authorization is voided and an alert is raised. |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `sessionId` | string | The session that failed. |
| `code` | string | The failure code stored on the session: `place-order-rejected` or `placement-unresolved`. |
| `at` | timestamp | When the failure was recorded. |

### CheckoutSessionExpired

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutSessionExpired` |
| Topic | `retailnetwork.checkout.analytics` |
| Subject / key | `sessionId` |
| Dataschema | `urn:retailnetwork:checkout:analytics:CheckoutSessionExpired:v1` |
| Producing command / use case | `ExpireSession` (policy `ExpireOnQuoteExpiry`, from `OPEN` or `DECLINED`) |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `sessionId` | string | The session that expired. |
| `at` | timestamp | When the expiry was recorded. |

The payload is `{sessionId, at}` only (`internal/adapters/outbound/kafka/testdata/CheckoutSessionExpired.golden.json`).

### CheckoutPlacementInDoubt

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutPlacementInDoubt` |
| Topic | `retailnetwork.checkout.analytics` |
| Subject / key | `sessionId` |
| Dataschema | `urn:retailnetwork:checkout:analytics:CheckoutPlacementInDoubt:v1` |
| Producing command / use case | `RecordPlacementInDoubt` (timeout or `503` from the network; the session stays `PLACING`) |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `sessionId` | string | The session whose place-order outcome is unknown. |
| `placingSince` | timestamp | When the session entered `PLACING` (the clock of the 20 h alert and the 22 h give-up). |
| `at` | timestamp | When the in-doubt outcome was recorded. |

`ConfirmSession` and `RecordAuthorization` raise no event: `CheckoutCompleted`
already covers the outcome. Expiry and an in-doubt placement are operator
facts, published analytics-only.

## Consumed

None. Checkout has no consumer, no consumer group, no DLQ and no inbox: the
session reacts to the synchronous answer of each call it makes. The two
consumers of `CheckoutCompleted` are other services; the group names they use
are theirs (in warehouse-infra they are `shopping-cart-checkout` and
`customer-payments-checkout`) and are not set by this repository.

### What the consumers do with `CheckoutCompleted`

This is the consumers' behaviour as their own contracts describe it; it is not
built in this repository.

| Consumer | Fields used | What it does |
| --- | --- | --- |
| `shopping-cart` | `cartId`, `sessionId` | Runs `CloseOnCheckoutCompleted`: `OPEN → CHECKED_OUT`, publishing `CartCheckedOut` (analytics). |
| `customer-payments` | `sessionId`, `orderId`, `authorizationId`, `groups` | Runs `BindOrderOnCheckoutCompleted`: binds the `orderId` to the session's authorization, then applies any parked fulfilment facts (gap S4). |

**Ordering.** `CheckoutCompleted` is keyed by `sessionId`, not `cartId` and
not `orderId`; per-session order holds, nothing more is promised across
topics. Fulfilment facts for the new order may reach `customer-payments`
before this event does; that consumer parks them until the bind (gap S4 on
the [Model Gap Register](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/model-gap-register)).
A session publishes `CheckoutCompleted` at most once, because `COMPLETED`
has no transition out (`ErrSessionClosed`); a redelivery carries the same
`id`, so consumers de-duplicate on it.

## Example envelope

`CheckoutCompleted`, the one fact the cart and payments follow. This is the
golden file the encoder test pins (shown re-indented):

```json
{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40",
  "source": "/retailnetwork/checkout",
  "type": "com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted",
  "subject": "sess-1",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:checkout:events:CheckoutCompleted:v1",
  "time": "2026-10-23T10:15:30.123456Z",
  "data": {
    "sessionId": "sess-1",
    "orderId": "order-1",
    "shopperId": "shopper-1",
    "cartId": "cart-1",
    "authorizationId": "auth-1",
    "currency": "USD",
    "totalMinor": 2100,
    "groups": [
      {"groupNo": 1, "tier": "NEXT_DAY", "amountMinor": 1365},
      {"groupNo": 2, "tier": "STANDARD", "amountMinor": 735}
    ],
    "completedAt": "2026-10-23T10:15:30.123456Z"
  }
}
```

Source: `internal/adapters/outbound/kafka/testdata/CheckoutCompleted.golden.json`
(re-indented here), `internal/adapters/outbound/kafka/payloads.go`.

The event carries references only: `shopperId`, `orderId` and
`authorizationId`. No address, name, e-mail or card data appears in any
checkout event; the stored placement request that holds `shipToAddressId` never
leaves the aggregate. The Kafka message also carries the `content-type` header
and, when the request had a span, `traceparent` and `tracestate`.

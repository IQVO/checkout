---
id: events
title: Event catalogue
sidebar_label: Event catalogue
sidebar_position: 2
description: The seven CloudEvents Checkout publishes on two Kafka topics, with type, topic, trigger and payload fields, and the statement that it consumes nothing.
---

# Event catalogue

Every Kafka message this service produces is a CloudEvents 1.0 event in
**structured mode**: the Kafka value is the whole JSON envelope and the header
`content-type` is `application/cloudevents+json; charset=UTF-8`. Envelopes are
built only in `internal/adapters/kafka/cloudevents`
([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)). The
contract is `apis/asyncapi.yaml`; the field-level view is on
[Domain events](/docs/ddd/domain-events).

:::info Built
The outbox and the always-on relay are on `develop` and are the only
publishing runtime of `cmd/api`. `TestEventCatalogueMatchesContract` fails the
build if the published types and `apis/asyncapi.yaml` disagree, and golden
files in `internal/adapters/outbound/kafka/testdata` pin every payload. In the
quickstart run, `CheckoutOpened` and `CheckoutCompleted` rows reached
`published_at` through the relay (see [Quickstart](/docs/overview/quickstart)).
:::

## Envelope

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID v4 minted once per occurrence and persisted with the outbox row; stable across redelivery |
| `source` | `/retailnetwork/checkout` |
| `type` | `com.retailnetwork.storefront.checkout.checkoutsession.<EventName>` |
| `subject` and Kafka key | the sessionId |
| `time` | the domain instant, RFC 3339 UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:checkout:<events\|analytics>:<EventName>:v1` |
| Kafka headers | `content-type: application/cloudevents+json; charset=UTF-8`, plus `traceparent` / `tracestate` when the request had a span |

`data` is camelCase, instants are RFC 3339 UTC, money is integer minor units. A
breaking payload change is a new `.v2` type, never a mutation. The real
golden `CheckoutCompleted`:

```json
{"specversion":"1.0","id":"3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40","source":"/retailnetwork/checkout","type":"com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted","subject":"sess-1","datacontenttype":"application/json","dataschema":"urn:retailnetwork:checkout:events:CheckoutCompleted:v1","time":"2026-10-23T10:15:30.123456Z","data":{"sessionId":"sess-1","orderId":"order-1","shopperId":"shopper-1","cartId":"cart-1","authorizationId":"auth-1","currency":"USD","totalMinor":2100,"groups":[{"groupNo":1,"tier":"NEXT_DAY","amountMinor":1365},{"groupNo":2,"tier":"STANDARD","amountMinor":735}],"completedAt":"2026-10-23T10:15:30.123456Z"}}
```

Source: `internal/adapters/outbound/kafka/testdata/CheckoutCompleted.golden.json`.

## Published

| Type (after `com.retailnetwork.storefront.checkout.checkoutsession.`) | Topic | Raised when | `data` |
| --- | --- | --- | --- |
| `CheckoutCompleted` | `retailnetwork.checkout.events` | the place-order step succeeded (`RecordPlaced`) | `sessionId`, `orderId`, `shopperId`, `cartId`, `authorizationId`, `currency`, `totalMinor`, `groups[{groupNo, tier, amountMinor}]`, `completedAt` |
| `CheckoutOpened` | `retailnetwork.checkout.analytics` | a session is opened | `sessionId`, `cartId`, `shopperId`, `totalMinor`, `openedAt` |
| `CheckoutDeclined` | `retailnetwork.checkout.analytics` | the payment was declined, or the authorized amount was not the quote total (reason `authorization-mismatch`) | `sessionId`, `reason`, `at` |
| `CheckoutRequoteRequired` | `retailnetwork.checkout.analytics` | a definitive 409 of the place-order step | `sessionId`, `requotedOffers[{offerId, groupNo, tier, deliverByEarliest, deliverByLatest, expiresAt}]`, `at` |
| `CheckoutFailed` | `retailnetwork.checkout.analytics` | the place-order step rejected the request (`place-order-rejected`) or the in-doubt placement was given up at 22 h (`placement-unresolved`) | `sessionId`, `code`, `at` |
| `CheckoutSessionExpired` | `retailnetwork.checkout.analytics` | an `OPEN` or `DECLINED` session passed its quote expiry | `sessionId`, `at` |
| `CheckoutPlacementInDoubt` | `retailnetwork.checkout.analytics` | the place-order outcome became unknown (first time only) | `sessionId`, `placingSince`, `at` |

`CheckoutCompleted` is the only integration event: shopping-cart closes the cart
on it and customer-payments binds the order to the authorization and the
allocations per group. The other six are analytics-only and have no consumer
wired today. `deliverByEarliest` and `deliverByLatest` are `YYYY-MM-DD` dates
in the destination's time zone. `Confirm` and a successful `RecordAuthorization`
raise nothing. Events are written in the same transaction as the session change
and delivered at least once.

Source: `internal/adapters/outbound/kafka/payloads.go`,
`internal/adapters/outbound/kafka/encoder.go`.

## Consumed

None. Checkout consumes no topic: no consumer, no consumer group, no DLQ. The
saga is driven by REST calls and clock-driven policies, and sibling data is read
synchronously through the anti-corruption clients
([Integration](/docs/ecosystem/integration)).

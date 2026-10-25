# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-25).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured
mode (the repo rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue
of every type checkout publishes. `TestEventCatalogueMatchesContract` checks it
against `apis/asyncapi.yaml`. Checkout consumes no topic.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across relay
  retries; consumers dedupe on it); `source` `/retailnetwork/checkout`; `subject` the
  `sessionId`; `time` the instant of the change, in UTC; `datacontenttype`
  `application/json`; `dataschema`
  `urn:retailnetwork:checkout:<events|analytics>:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one session stay ordered on one partition.
- `data` is camelCase; timestamps are RFC 3339 UTC; money is integer minor units.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.
- `<entity>` is the raising aggregate, lowercase: `checkoutsession`. Tier is `storefront`.

### Published types

`CheckoutCompleted` is the one integration event (topic `retailnetwork.checkout.events`,
consumed by shopping-cart and customer-payments). The other six are analytics-only and go
ONLY to `retailnetwork.checkout.analytics` (decision H7 added the last two).

```text
type: com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted stream: events
type: com.retailnetwork.storefront.checkout.checkoutsession.CheckoutOpened stream: analytics
type: com.retailnetwork.storefront.checkout.checkoutsession.CheckoutDeclined stream: analytics
type: com.retailnetwork.storefront.checkout.checkoutsession.CheckoutRequoteRequired stream: analytics
type: com.retailnetwork.storefront.checkout.checkoutsession.CheckoutFailed stream: analytics
type: com.retailnetwork.storefront.checkout.checkoutsession.CheckoutSessionExpired stream: analytics
type: com.retailnetwork.storefront.checkout.checkoutsession.CheckoutPlacementInDoubt stream: analytics
```

| Event | Subject / key | Raised when | `data` |
|---|---|---|---|
| `CheckoutCompleted` | `sessionId` | RecordPlaced is saved | `{sessionId, orderId, shopperId, cartId, authorizationId, currency, totalMinor, groups[{groupNo, tier, amountMinor}], completedAt}` |
| `CheckoutOpened` | `sessionId` | OpenSession is saved | `{sessionId, cartId, shopperId, totalMinor, openedAt}` |
| `CheckoutDeclined` | `sessionId` | RecordDecline is saved (also an authorization mismatch) | `{sessionId, reason, at}` |
| `CheckoutRequoteRequired` | `sessionId` | RecordRequoteRequired is saved | `{sessionId, requotedOffers[{offerId, groupNo, tier, deliverByEarliest, deliverByLatest, expiresAt}], at}` |
| `CheckoutFailed` | `sessionId` | RecordFailure is saved | `{sessionId, code, at}` |
| `CheckoutSessionExpired` | `sessionId` | ExpireSession is saved | `{sessionId, at}` |
| `CheckoutPlacementInDoubt` | `sessionId` | first RecordPlacementInDoubt is saved | `{sessionId, placingSince, at}` |

Payload rules:

- ConfirmSession and RecordAuthorization raise nothing (decision H7): CheckoutCompleted
  already covers the outcome.
- `CheckoutPlacementInDoubt` is raised on the first unknown outcome only, not on every
  pass of ResolveInDoubtPlacements.
- No event carries PII or card data: the session stores `shopperId` and
  `shipToAddressId` references and never a PSP token beyond the AUTHORIZING window.
- A breaking payload change is a new `.v2` type and dataschema, never a mutation of v1.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is no
  shared Go code.
- Adding a published type requires a line here in the same commit as its asyncapi
  message, or the architecture fitness test fails.

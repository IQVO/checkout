---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

checkout PUBLISHES seven events: `CheckoutCompleted` to `retailnetwork.checkout.events`
and six analytics-only events to `retailnetwork.checkout.analytics`. It CONSUMES nothing
(no consumer, no consumer group). Full contract:
`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md` and `apis/asyncapi.yaml`.

## Events: CloudEvents 1.0 is MANDATORY

Every message produced is a CloudEvents 1.0 event in structured content mode; read
`.claude/rules/fleet/cloudevents.md`. There is no flat envelope, no dual-write and no
envelope toggle (`TestNoEventEnvelopeToggleOrFlatEnvelope`).

- Build and decode only through `internal/adapters/kafka/cloudevents` (`New`,
  `Decode`, `ContentTypeHeader`); transport is `segmentio/kafka-go`.
- Required attributes: `specversion=1.0`; `id` (UUID minted once per domain event and
  stored with the outbox row); `source=/retailnetwork/checkout`; `type`;
  `subject` (the sessionId, never empty); `time` (UTC); `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:checkout:<events|analytics>:<Event>:v1`. Message key =
  subject, `kafkago.Hash{}` balancer.
- Tests: a golden exact-JSON test per published type
  (`internal/adapters/outbound/kafka/testdata`), Kafka integration tests via
  testcontainers only.

### Published types

| `type` | topic | `subject` |
| --- | --- | --- |
| `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted` | `retailnetwork.checkout.events` | sessionId |
| `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutOpened` | `retailnetwork.checkout.analytics` | sessionId |
| `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutDeclined` | `retailnetwork.checkout.analytics` | sessionId |
| `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutRequoteRequired` | `retailnetwork.checkout.analytics` | sessionId |
| `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutFailed` | `retailnetwork.checkout.analytics` | sessionId |
| `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutSessionExpired` | `retailnetwork.checkout.analytics` | sessionId |
| `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutPlacementInDoubt` | `retailnetwork.checkout.analytics` | sessionId |

### Consumed types

None. Sibling data is read synchronously (`docs/adr/0003-synchronous-contracts-and-the-anti-corruption-layer.md`).

## Consumer group id

No consumer, so no `*_CONSUMER_GROUP`. If one is ever added, its group comes from a
mandatory environment variable read in `cmd/api/config.go`, never a literal
(`TestKafkaConsumerGroupNeverHardcodedInline`).

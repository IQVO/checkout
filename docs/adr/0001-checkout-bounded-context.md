# 1. Checkout is a bounded context of the storefront domain

Status: Accepted (bootstrap). Design source: warehouse-docs `storefront/model/model.yaml`, context `checkout`.

## Context

The storefront is the e-commerce domain of the retail-network organisation: six bounded contexts, each its own
service and database. The purchase saga [14][16]: one transaction across price, payment and the network's promise confirmation. Authorize before placing the order (compensatable: void), place the order (pivot), then retriable steps via events. PLACING is the semantic lock [15]: while the place-order outcome is unknown there is no void and no new attempt. Holds shopperId and shipToAddressId references, never PII; the stored placement request is PII-free so it can be replayed byte for byte.

## Decision

- This repo owns the aggregates CheckoutSession; the invariants and error names are those of the model.
- Business decisions of the context:
- Money flow (D5): authorize before placing; capture per shipment group on FulfillmentShipped; release on FulfillmentCancelled; refund on PackageLost or PackageReturned.
- The network's "Checkout" (customer-ordering PlaceOrder) is this saga's place-order step, reached through an Anti-Corruption Layer (internal/adapters/outbound/customerordering, the only place that knows network wire names).
- A decline keeps the session open for another token while the price quote is valid; a definitive 409 voids the authorization and asks for a requote; a 422 is a contract bug: void, FAILED, alert.
- A 503 or timeout leaves the session PLACING, answers 202, and ResolveInDoubtPlacements replays the stored body with the same Idempotency-Key until a definitive answer; alert at 20 h, before the network's 24 h idempotency window.
- Events use the storefront CloudEvents namespace `com.retailnetwork.storefront.checkout.<entity>.<Event>` on `retailnetwork.checkout.events`
  (analytics-only events on `.analytics`). Published: CheckoutCompleted, CheckoutOpened, CheckoutDeclined, CheckoutRequoteRequired, CheckoutFailed, CheckoutSessionExpired, CheckoutPlacementInDoubt (ADR 0004). Consumed: none. No `warehouse.*` topic is consumed or produced.
- The storefront calls only the network's Open Host Service routes (model inbound rows for `shopper / storefront`),
  never an in-org or restricted route; sibling data used in a decision is a local event-fed read model, except the
  calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet and the network. This context stores no PII and no card data.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.

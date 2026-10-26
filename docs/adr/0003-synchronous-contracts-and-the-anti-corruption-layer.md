# ADR 0003: Synchronous contracts and the Anti-Corruption Layer

## Status

Accepted (2026-10-25).

## Context

Checkout consumes no event (model `consumes: []`). The saga reads and writes its
siblings over REST, restricted to the routes the model lists under `sync_calls` and to
the network's Open Host Service routes (rule 3 of the storefront campaign). Each
gateway restates the one or two routes it uses with its own DTOs; no Go package of
another context is imported.

## Decision

| Step | Callee | Route | Idempotency-Key | Gateway package |
|---|---|---|---|---|
| GetCart | shopping-cart | `GET /carts/{cartId}` | n/a | `adapters/outbound/shoppingcart` |
| GetDeliveryOffer | delivery-promise | `GET /delivery-offers/{offerId}` | n/a | `adapters/outbound/deliverypromise` |
| IssuePriceQuote | pricing | `POST /price-quotes` | `<requestKey>:price-quote` | `adapters/outbound/pricing` |
| AuthorizePayment | customer-payments | `POST /payment-authorizations` | `<sessionId>:auth:<attempt>` (H2) | `adapters/outbound/customerpayments` |
| VoidAuthorization | customer-payments | `POST /payment-authorizations/{id}:void` | `<sessionId>:void` | `adapters/outbound/customerpayments` |
| PlaceOrder | customer-ordering | `POST /checkout` | `<sessionId>` | `adapters/outbound/customerordering` (the ACL) |

- **The ACL** (`internal/adapters/outbound/customerordering`) is the only place that
  knows the network's wire names (its "Checkout" is this saga's Place-Order Step). It
  builds the request from references only: `shopperId`, `shipToAddressId` (NG1, never
  an inline address), the numbered lines and the groups' `{groupNo, offerId, lineNos}`.
  The body is rendered once, stored on the session (StoredPlacement, H9) and replayed
  byte for byte.
- **Outcome mapping of PlaceOrder.** 2xx: placed. A 409 whose problem type is
  `offer-not-quotable`, `promise-capacity-exhausted` or `book-closed`: a definitive
  requote carrying the re-quoted offers. 400 or 422: a contract bug (void, FAILED,
  alert). Anything else (timeout, transport error, 5xx, a `concurrent-update` 409, an
  answer without an `orderId`) leaves the outcome UNKNOWN: the session stays PLACING and
  the confirm route answers 202.
- **Void.** 200, 404 and a 409 `illegal-transition` (already voided, captured or expired)
  count as done. A transport error, 5xx or a `concurrent-update` leaves the debt on the
  session (`voidOwed`) and RetryOwedVoids tries again every minute (H5).
- **Authorize.** A 201 AUTHORIZED is an authorization, a 201 DECLINED a decline. A 422 is
  turned into a decline (a contract bug the shopper sees as a failed payment, with an
  `authorization-rejected` reason). A transport error, 5xx or 409 leaves the session
  AUTHORIZING; ResolveStuckAuthorizations re-calls with the SAME key after 2 minutes (H4).
- **Timeouts.** 5 s per sibling call (`SIBLING_TIMEOUT`), 15 s for PlaceOrder
  (`CUSTOMER_ORDERING_TIMEOUT`): the network promises and confirms every group before it
  answers. No outbound call ever runs inside a database transaction.

## Consequences

- A sibling that tightens a contract breaks only its gateway and its httptest.
- The five base URLs are mandatory boot configuration; there is no mode without them.

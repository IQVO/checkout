---
id: ubiquitous-language
title: Ubiquitous Language
sidebar_label: Ubiquitous Language
description: The terms Checkout speaks, each mapped to its code identifier on develop, plus the neighbour terms it uses and the terms most often confused with them, above all the network's Checkout.
---

# Ubiquitous Language

The terms this context speaks, each mapped to the identifier in the code. The
first table is the context's own vocabulary. The second holds the elements of
its aggregate and its policies. The third holds neighbour terms this context
uses but does not own; their definitions come from the owning context's
language. The storefront-wide glossary is [Ubiquitous Language](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/ubiquitous-language).

## Own terms

Source: `internal/domain/checkoutsession`, `internal/application/ports`, `internal/adapters/outbound/customerordering`.

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Checkout Session** | The purchase transaction from review to placed order. | `checkoutsession.CheckoutSession` | The network's "Checkout" (`customer-ordering` `PlaceOrder`), which is the session's place-order step. |
| **Place-Order Step** | The pivot of the saga: `customer-ordering` `POST /checkout` through the ACL. | `ports.OrderPlacer` | The whole session: placing the order is one step; authorizing before it and the events after it are the others. |
| **In-Doubt Placement** | The place-order outcome is unknown (timeout or 503); `PLACING` holds the semantic lock until the resolver settles it. | `checkoutsession.CheckoutSession.RecordPlacementInDoubt` | A failure: nothing is known to have failed, and nothing is known to have succeeded. The session neither voids nor retries as a new attempt. |
| **Requote** | A definitive 409 from the network: the authorization is voided and the session carries the re-quoted offers for the cart. | `checkoutsession.RequotedOffer` | The cart's **Delivery Quote**: a requote is the network's answer to a place-order attempt and ends the session in `REQUOTE_REQUIRED`; a new session starts from the cart. |
| **Allocation** | The share of an authorization that belongs to one shipment group. | `checkoutsession.Group.AmountMinor` | `customer-payments`' **Allocation** (`paymentauthorization.Allocation`, with a `PENDING`, `CAPTURED` or `RELEASED` state): the session's amount per group is what the authorization is split by; the payment-side object tracks the money. |

## Model elements of this context

Source: `internal/domain/checkoutsession/session.go`, `values.go`, `transitions.go`, `terminals.go`;
`internal/application/usecases/policies.go`, `resolve_in_doubt.go`.

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Session Line** | One numbered line of the session: `lineNo`, `catalogItemId`, `quantity`. | `checkoutsession.Line{LineNo, CatalogItemID, Quantity}` | The cart's `CartLine{CatalogItemID, Quantity}`: the session numbers lines 1..n when it opens. |
| **Session Group** | One shipment group: `groupNo` (from the quote), the chosen `offerId`, `tier` and the `lineNos` it covers. | `checkoutsession.Group{GroupNo, OfferID, Tier, LineNos, AmountMinor}` | The network's `GroupPromise{groupNo, promiseId, ...}` in a placed order: the session group carries the offer, the order carries the confirmed promise. |
| **Requoted Offer** | One offer the network returned in a definitive 409: `offerId`, `groupNo`, `tier`, delivery window and `expiresAt`. | `checkoutsession.RequotedOffer{OfferID, GroupNo, Tier, DeliverByEarliest, DeliverByLatest, ExpiresAt}` | The cart's `QuotedOffer`: same shape, different moment; this one answers a failed placement. |
| **Session states** | `OPEN`, `AUTHORIZING`, `DECLINED`, `PLACING`, `REQUOTE_REQUIRED`, `COMPLETED`, `EXPIRED`, `FAILED`. | `checkoutsession.State` | The network order's states (`PLACED`, `IN_FULFILLMENT`, ...): a `COMPLETED` session is the placed order's origin, not its status. |
| **Semantic Lock** | `PLACING` while the place-order outcome is unknown: no void and no new attempt. | `ErrPlacementInDoubt` (the invariant that enforces it) | A database lock: it is a business state that survives restarts and is visible to the shopper as `202`. |
| **Stored Placement Request** | The PII-free body sent to `customer-ordering`, kept so it can be replayed byte for byte. | `checkoutsession.StoredPlacement{RequestBody, RequestHash, PlacingSince}` | The order itself: it holds `shipToAddressId` and numbered lines, never the address. |
| **Resolve In-Doubt Placements** | Policy: re-send the stored body with the same `Idempotency-Key` with backoff until a definitive answer; alert at 20 h, give up and void at 22 h. | `ResolveInDoubtPlacements` | A retry of the shopper's confirm: confirm is refused while `PLACING`. |
| **Expire On Quote Expiry** | Policy: an `OPEN` or `DECLINED` session expires with its price quote. | `ExpireOnQuoteExpiry` | A session timeout in general: only the quote's expiry ends an open session. |
| **Void Owed** | The compensation debt: the authorization that must still be voided after a requote, a rejection or a mismatching authorization. | `checkoutsession.VoidOwed{AuthorizationID}`, column `void_authorization_id` | A refund: the void cancels a hold before any capture. While it is owed, a new confirm is `void-pending`. |
| **Retry Owed Voids** | Policy: void each owed authorization every minute until it succeeds. | `RetryOwedVoids` | The immediate void attempted at settlement: this is its retry. |
| **Resolve Stuck Authorizations** | Policy: re-call AuthorizePayment with the same key for a session `AUTHORIZING` over 2 minutes. | `ResolveStuckAuthorizations` | A new authorization attempt: the key (and the token) are the same. |
| **Authorization Attempt** | The counter in the authorization key `<sessionId>:auth:<attempt>`; it grows when a `DECLINED` session is confirmed again. | `CheckoutSession.AuthAttempt()` | A retry of a stuck call, which keeps the attempt. |
| **Purge Stored Placements** | Policy: drop the stored placement request 24 h after a closed state. | `PurgeStoredPlacements` | Deleting the session: only the request body goes. |
| **Idempotency Key** | The caller's header that makes a write replayable for 24 h; also the deterministic keys sent downstream. | `idempotency.Record`, `Idempotency-Key` | The CloudEvents `id`, which is minted per event occurrence. |

## Neighbour terms this context uses

| Term | Definition (owner's) | Code identifier (owner) | Not to be confused with |
| --- | --- | --- | --- |
| **Checkout** (network) | The act of turning a cart into a customer order, promising every group. | `order.PlaceOrder` (`customer-ordering`, [network](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering)) | The storefront's **Checkout Session**, a larger transaction of which this is one step. |
| **Customer Order** | What the shopper bought, with a promise per shipment group. | `order.CustomerOrder` (`customer-ordering`, [network](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering)) | The checkout session: the order is the network's record, the session is the storefront's saga. |
| **Ship-To** | The delivery address; synthetic PII held only in `customer-ordering`. | `order.ShipTo` (`customer-ordering`, [network](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering)) | The session's `shipToAddressId`: a reference, never the address. |
| **Shopper** | The (synthetic) customer placing orders. | `shopper.Shopper` (`customer-ordering`, [network](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering)) | The session's `shopperId`: a reference to it. |
| **Cart** | One shopper's held intent: lines, destination, delivery quote and selections. | `cart.Cart` ([shopping-cart](https://iqvo.github.io/warehouse-docs/storefront/contexts/shopping-cart)) | The session: the cart is intent, the session is the transaction. |
| **Checkout Readiness** | Every group selected and no offer expired. | `cart.CheckoutReadiness` ([shopping-cart](https://iqvo.github.io/warehouse-docs/storefront/contexts/shopping-cart)) | The session's own state; checkout enforces the readiness at open (`ErrCartNotCheckoutReady`). |
| **Price Quote** | A binding, immutable, expiring price snapshot for one checkout. | `quote.PriceQuote` ([pricing](https://iqvo.github.io/warehouse-docs/storefront/contexts/pricing)) | The cart's **Estimate** (non-binding). |
| **Payment Authorization** | A hold on the shopper's payment method for the quote total, split into allocations per shipment group. | `paymentauthorization.PaymentAuthorization` (`customer-payments`) | The charge: an authorization holds, a capture takes. |
| **Void** | Cancelling the whole authorization before any capture. | `paymentauthorization.PaymentAuthorization.Void` (`customer-payments`) | A refund (money returned after capture) or a release (one allocation). |
| **Payment Method Token** | The PSP token standing in for the shopper's card; the storefront never sees a card number. | `(provider port)` (`customer-payments`) | A card number: the session never holds one. |
| **DeliveryOffer** | A short-lived quote for (item, quantity, destination prefix, tier) with a delivery window, the witness path that proves it, and an expiry. | `offer.DeliveryOffer` (`delivery-promise`, [network](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise)) | The session group's `offerId`: a reference to such an offer. |

## Fleet translation

None. Checkout never produces or consumes a WES or WMS `warehouse.*` topic and
shares no term with the warehouse tiers.
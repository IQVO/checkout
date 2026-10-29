# Domain model: ubiquitous language, aggregates, events, use cases

## Ubiquitous Language (use these exact names)

- **Checkout Session** — the purchase transaction from review to placed order (the saga). Holds references (`shopperId`, `shipToAddressId`), never PII.
- **Place-Order Step** — the pivot of the saga: customer-ordering `POST /checkout` through the Anti-Corruption Layer. Not the network's "Checkout" as a whole.
- **In-Doubt Placement** — the place-order outcome is unknown (timeout, 5xx); PLACING is the semantic lock: no void, no new attempt (`ErrPlacementInDoubt`).
- **Requote** — a definitive 409 from the network: the authorization is voided and the session carries `requotedOffers` (REQUOTE_REQUIRED).
- **Allocation** — the share of the authorization that belongs to one shipment group (`SessionGroup.AmountMinor`; items + delivery + pro-rata tax).
- **Void Owed** — the decision to void an authorization, stored with the state change; RetryOwedVoids tries until it succeeds.
- **StoredPlacement** — the PII-free place-order request `{requestBody, requestHash, placingSince}`, replayed byte for byte; purged 24 h after a closed state.

## Aggregates

- **CheckoutSession** (`internal/domain/checkoutsession`): OPEN, AUTHORIZING, DECLINED, PLACING, REQUOTE_REQUIRED, COMPLETED, EXPIRED, FAILED. Closed: COMPLETED, EXPIRED, FAILED, REQUOTE_REQUIRED (`ErrSessionClosed`). Invariants: `ErrCartNotCheckoutReady`, `ErrPriceQuoteExpired`, `ErrAuthorizationMismatch`, `ErrPlacementInDoubt`, `ErrOfferNotQuotable` (H3), `ErrVoidPending`, `ErrContractViolation`.
- `internal/domain/shared`: ids, errors, Event.

## Domain events

- `CheckoutCompleted` (the only integration event, `.events`) — RecordPlaced; consumed by shopping-cart and customer-payments.
- Analytics-only: `CheckoutOpened`, `CheckoutDeclined`, `CheckoutRequoteRequired`, `CheckoutFailed`, `CheckoutSessionExpired`, `CheckoutPlacementInDoubt` (first unknown outcome). ConfirmSession and RecordAuthorization raise none.

## Key use cases (`internal/application/usecases`)

- `OpenSession` — GetCart, GetDeliveryOffer pre-check, IssuePriceQuote (all outside the transaction), then saves the session.
- `ConfirmSession` — saves AUTHORIZING, AuthorizePayment (`<sessionId>:auth:<attempt>`), stores the placement, PlaceOrder through the `OrderPlacer` port, settles the outcome (void when needed).
- `GetSession` — the BFF polling read.
- Policies: `ResolveInDoubtPlacements` (alert 20 h, give up and void 22 h), `ResolveStuckAuthorizations` (H4), `RetryOwedVoids` (H5), `ExpireOnQuoteExpiry` (OPEN and DECLINED), `PurgeStoredPlacements` (H9).

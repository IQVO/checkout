# ADR 0009: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01). Replaces the per-route `Idempotency-Key` store, the 422 answer for key reuse, the
`Idempotent-Replayed` header and the `:confirm` route of the checkout contract.

## Context

The 2026-10-30 application portfolio assessment measured the fleet's REST contracts: write operations only
partly keyed, two spellings of the replay header, key reuse answered 422 in most services and 409 in one,
three command-URL styles (`:verb`, verb sub-paths, sub-resources) and lists without paging. checkout showed
three of those:

- one command route in the `:verb` style (`POST /checkout-sessions/{sessionId}:confirm`), and a consumed route
  in the same style (`POST /payment-authorizations/{id}:void` on customer-payments);
- the replay header was spelled `Idempotent-Replayed` and a reused key was answered 422;
- keys were scoped per route: the store looked a record up by `(route, key)`, so the same key on another route
  was treated as a new key instead of a reuse.

checkout has no collection endpoint, so the paging rules do not apply here.

The fleet standard (warehouse-docs, "REST API guideline", rules R1-R8) removes each of those. This ADR adopts
it here, as a hard cutover: no alias route, no dual header, no toggle.

## Decision

1. **Noun routes (R1, R2).** Status codes and response bodies do not change with a rename; the `operationId`
   stays, so the MCP and A2A mappings do not move.

   | Old | New |
   |---|---|
   | `POST /checkout-sessions/{sessionId}:confirm` | `POST /checkout-sessions/{sessionId}/confirmations` |

   `confirmations` is the transition resource of the confirm act (guideline 1.4). The consumed route follows
   its provider: `POST /payment-authorizations/{id}:void` becomes
   `POST /payment-authorizations/{id}/voids` in `internal/adapters/outbound/customerpayments`. The other
   siblings' routes (`GET /carts/{cartId}`, `GET /delivery-offers/{offerId}`, `POST /price-quotes`,
   `POST /checkout`) are unchanged. `TestRetiredRoutesAreGone` asserts the old route answers 404 or 405.
2. **Idempotency (R3-R6, R8).** Both `POST` routes (`/checkout-sessions` and
   `/checkout-sessions/{sessionId}/confirmations`) sit behind one `RequireIdempotencyKey(store, clock)`
   middleware. The key is 1 to 128 printable ASCII characters: absent is 400 `idempotency-key-required`,
   malformed or longer than 128 is 400 `idempotency-key-invalid`. The fingerprint is method + path + SHA-256 of
   the body (the query string no longer counts): the same key and fingerprint replays the stored answer
   (status, body, `Content-Type`) with `Idempotency-Replayed: true`, and only a replay carries the header; the
   same key with another fingerprint is 409 `idempotency-key-reused`, never 422. A 2xx or 4xx answer is
   stored for 24 hours; a 5xx is never stored, so the retry re-executes.
3. **A global key (STANDARD 2.3).** `ports.IdempotencyStore` is now `Get(ctx, key)` / `Put(ctx, key, rec)`.
   Migration `0002_idempotency_key_global` makes `key` the sole primary key of `idempotency_keys` and drops the
   `route` column (a key stored under two routes keeps its newest row); the in-memory double does the same.
   Requests that carry the same key are serialised inside the process, so N concurrent identical requests run
   the command once and the others replay its answer; across replicas the store keeps the first record and the
   loser replays it. The key row is written after the handler answers, not in the same transaction as the
   aggregate change: the confirm saga commits in phases around its calls to customer-payments and
   customer-ordering (`AUTHORIZING` and `PLACING` are saved before the call out), so one transaction around the
   whole request would hide those states from the sweepers and lose them on a crash.
4. **Keys to siblings stay within 128 characters.** The pricing call is keyed `<Idempotency-Key>:price-quote`;
   a request key near the bound would overflow the siblings' 128 character limit once suffixed, so such a key
   is replaced by its SHA-256 hex (`priceQuoteKey`).
5. **Contract and enforcement.** `apis/openapi.yaml` declares the `Idempotency-Key` parameter (required: the
   middleware is active in every runtime mode), 400 and 409 and the `Idempotency-Replayed` header on every 2xx
   of both `POST` operations. The `rest-guideline` Spectral rule (`.spectral.yaml`,
   `.spectral/functions/restGuideline.js`) runs in the `api-lint` job with `--fail-severity=warn`.
6. **Consumers changed in this repo.** Only the customer-payments gateway (`Void` calls `.../voids`, its test
   and the integration fake). There is no web remote, seed script or chart probe that calls the renamed route.
   Callers of this service (storefront-bff, via its allow-list) update in their own pull requests.

### Exemptions

None needed. `GET /checkout-sessions/{sessionId}` is an item, `/healthz` and `/readyz` are not collections,
and checkout exposes no list.

## Consequences

- Breaking for every caller: a new confirm URL, 409 for key reuse, the `Idempotency-Replayed` header. This pull
  request must deploy together with the rest of the REST cutover set (storefront-bff, customer-payments and the
  other services); `develop` is half-migrated until the last of them lands.
- A client that reused one `Idempotency-Key` across the open and the confirm used to get two answers; it now
  gets 409 `idempotency-key-reused`. A UUID per logical submit avoids it.
- Migration 0002 drops a column, so an image from before this change cannot write idempotency keys on a migrated
  database: the cutover set rolls back together (the down migration restores the column).
- Because the key row is not in the aggregate's transaction, a crash between the commit and the key write loses
  the stored answer; the retry then re-executes against the new state (for a confirm that is an
  `illegal-transition` or `session-closed` 409, never a second authorization, because the downstream keys are
  derived from the session id).

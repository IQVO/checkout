---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 2
---

# Subdomain classification

`checkout` is a **Supporting** subdomain in the **Storefront** tier
(CloudEvents tier segment `storefront`). The verdict is the one in the
storefront section of the warehouse-docs aggregator
(`storefront/docs/strategic-design/subdomain-classification.md` on `main`,
both the problem-space table, row *Checkout*, and the solution-space table, row
`checkout`). Neighbour rows below are copied from that table, not recalled.
That page lists checkout's aggregate as `CheckoutSession` and its business model
as *Conversion (the purchase transaction)*, evolution *Custom-built*, which is
what the [Bounded Context Canvas](/docs/ddd/bounded-context-canvas) records.

The aggregator's older fleet-wide table (`docs/strategic-design/subdomain-classification.md`)
covers the warehouse contexts only and has no row for any storefront context;
the storefront contexts are classified in their own section.

## Why Supporting

- **Not Core**: a shopper never chooses a store for its purchase saga. The
  saga must work (a broken one loses the sale and can charge a card twice) but
  working is not the differentiator. What differentiates the storefront is
  `pricing`: the price of speed per tier, where the network's promise becomes
  revenue. The [Core domain chart](/docs/ddd/core-domain-chart) plots checkout
  at differentiation 0.48.
- **Not Generic**: the saga shape is specific to this network. Authorize first
  (compensatable), place the order through a promise-confirming network call
  (the pivot), then retriable steps by events. The three definitive failures
  each have their own compensation, and an unknown outcome is held in `PLACING`
  (the semantic lock) and settled by replaying a stored, PII-free body.
  A stock payment flow does not fit.
- **Complexity is the highest of the six storefront contexts** (0.82 on the
  chart): one aggregate with eight states, five clock-driven policies, and an
  Anti-Corruption Layer that keeps the network's wire names
  (`Checkout`, `offer-not-quotable`, `requotedOffers`) out of the domain
  ([ADR 0003](/docs/adr/0003-synchronous-contracts-and-the-anti-corruption-layer)).
  The classification and the geometry disagree by a hair on the differentiation
  axis (0.48 sits just under the midline); the verdict stays *Supporting*.
- **Business model**: conversion, the purchase transaction. **Evolution**:
  custom-built.

What would move it: a payment provider or the network offering a single
"authorize and place" call with its own recovery would shrink the saga toward
Generic. Promotions would add a second discount path to the quote, which
`pricing` absorbs without changing the session.

## Neighbours (from the storefront table)

| Bounded context | Classification | Tier | Business model (its row) | Relation to this context |
| --- | --- | --- | --- | --- |
| `pricing` | Core | `storefront` | Revenue (the price of speed per tier) | upstream: checkout asks for the binding quote (`POST /price-quotes`) |
| `shopping-cart` | Supporting | `storefront` | Conversion (holding intent without losing the total-cost-up-front rule) | upstream (`GET /carts/{cartId}`) and downstream (consumes `CheckoutCompleted`) |
| `customer-payments` | Generic | `storefront` | Revenue collection (buy a PSP) | upstream (authorize, void) and downstream (consumes `CheckoutCompleted`) |
| `product-discovery` | Supporting | `storefront` | Conversion enabler | no edge |
| `product-content` | Generic | `storefront` | Engagement enabler (buy a PIM in production) | no edge |
| `checkout` | **Supporting** | `storefront` | Conversion (the purchase transaction) | this context |

Count in that table: 3 Supporting, 2 Generic, 1 Core. The same page records that
order management and customer accounts are deliberately absent from the
storefront: the retail network already owns them (`customer-ordering`), so
checkout reaches the network through its Open Host Service routes
(`customer-ordering` `POST /checkout`, `delivery-promise`
`GET /delivery-offers/{offerId}`) and never calls a restricted route. The
experience layer (`storefront-bff`, `storefront-web`) carries no domain model and
is not a bounded context. See [Integration](/docs/ecosystem/integration) and the
[Context map](/docs/ddd/context-map).

## Tier is not classification

`storefront` groups contexts by tier, not by Core, Supporting or Generic. In the
same tier, `pricing` is Core and `customer-payments` is Generic. For this
context the two axes read: tier Storefront, class Supporting. The tier decides
the type prefix `com.retailnetwork.storefront.checkout.`; the class decides where
investment goes.

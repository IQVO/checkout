package ports

import "errors"

var (
	// ErrNotFound is what every repository returns for a Get that matches
	// nothing (problem type not-found, 404).
	ErrNotFound = errors.New("not found")
	// ErrConcurrentUpdate is what Save returns when the stored version is not
	// the one the aggregate was loaded at (problem type concurrent-update,
	// 409): the expectation is `stored version == aggregate.LoadedVersion()`,
	// and a create (LoadedVersion 0) expects that the id is not stored yet.
	ErrConcurrentUpdate = errors.New("concurrent update")

	// ErrCartNotFound: shopping-cart does not know the cart (becomes 422
	// validation-failed, the cartId in the request is wrong).
	ErrCartNotFound = errors.New("cart not found")
	// ErrQuoteRejected: pricing refused the quote request (422): an unpriced
	// item or a malformed request (problem type price-quote-rejected, 422).
	ErrQuoteRejected = errors.New("pricing rejected the quote request")

	// The *Unavailable sentinels mean the sibling cannot be reached, timed out
	// or answered a server error (problem type <service>-unavailable, 503).
	// The caller retries with the same Idempotency-Key.

	// ErrShoppingCartUnavailable: GetCart failed.
	ErrShoppingCartUnavailable = errors.New("shopping-cart unavailable")
	// ErrDeliveryPromiseUnavailable: GetDeliveryOffer failed.
	ErrDeliveryPromiseUnavailable = errors.New("delivery-promise unavailable")
	// ErrPricingUnavailable: IssuePriceQuote failed.
	ErrPricingUnavailable = errors.New("pricing unavailable")
	// ErrPaymentsUnavailable: AuthorizePayment or VoidAuthorization failed.
	ErrPaymentsUnavailable = errors.New("customer-payments unavailable")
	// ErrOrderingUnavailable: PlaceOrder timed out or answered 503/5xx or a
	// retriable 409: the outcome is unknown (the semantic lock holds).
	ErrOrderingUnavailable = errors.New("customer-ordering unavailable")
)

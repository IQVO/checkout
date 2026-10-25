package ports

import (
	"context"

	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// CartReader is shopping-cart GetCart (REST GET /carts/{cartId}).
type CartReader interface {
	// GetCart returns ErrCartNotFound or ErrShoppingCartUnavailable on failure.
	GetCart(ctx context.Context, cartID string) (exchange.Cart, error)
}

// OfferReader is delivery-promise GetDeliveryOffer (REST GET /delivery-offers/{offerId}).
type OfferReader interface {
	// GetOffer returns the offer's state; an unknown offer is reported as
	// Quoted=false (it is not quotable), a failure as ErrDeliveryPromiseUnavailable.
	GetOffer(ctx context.Context, offerID string) (checkoutsession.OfferStatus, error)
}

// PriceQuoter is pricing IssuePriceQuote (REST POST /price-quotes).
type PriceQuoter interface {
	// IssueQuote returns ErrQuoteRejected or ErrPricingUnavailable on failure.
	// The key is forwarded as Idempotency-Key.
	IssueQuote(ctx context.Context, key string, req exchange.QuoteRequest) (checkoutsession.PriceQuote, error)
}

// PaymentGateway is customer-payments AuthorizePayment and VoidAuthorization.
type PaymentGateway interface {
	// Authorize calls POST /payment-authorizations with the key
	// <sessionId>:auth:<attempt> (H2). ErrPaymentsUnavailable on failure.
	Authorize(ctx context.Context, key string, req exchange.AuthorizeRequest) (exchange.Authorization, error)
	// Void calls POST /payment-authorizations/{id}:void with the key
	// <sessionId>:void. An authorization that is already voided or unknown
	// counts as voided (the call is idempotent). ErrPaymentsUnavailable on failure.
	Void(ctx context.Context, key, authorizationID string) error
}

// Anti-Corruption Layer: the only code that knows the network's wire names.
type OrderPlacer interface {
	// BuildRequest renders the PII-free request body, deterministic so a
	// replay is byte for byte identical.
	BuildRequest(draft exchange.PlacementDraft) ([]byte, error)
	// Place sends the stored body with Idempotency-Key = sessionId. A
	// timeout, a 5xx or a retriable 409 is ErrOrderingUnavailable.
	Place(ctx context.Context, sessionID string, body []byte) (exchange.PlaceOutcome, error)
}

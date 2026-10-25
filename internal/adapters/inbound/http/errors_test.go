package http_test

import (
	"errors"
	"testing"

	httpadapter "github.com/IQVO/checkout/internal/adapters/inbound/http"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

func TestEveryDomainAndPortErrorHasItsProblem(t *testing.T) {
	cases := []struct {
		err    error
		status int
		slug   string
	}{
		{cs.ErrCartNotCheckoutReady, 409, "cart-not-checkout-ready"},
		{cs.ErrSessionClosed, 409, "session-closed"},
		{cs.ErrAuthorizationMismatch, 409, "authorization-mismatch"},
		{cs.ErrPlacementInDoubt, 409, "placement-in-doubt"},
		{cs.ErrVoidPending, 409, "void-pending"},
		{cs.ErrPriceQuoteExpired, 422, "price-quote-expired"},
		{cs.ErrOfferNotQuotable, 422, "offer-not-quotable"},
		{cs.ErrContractViolation, 502, "upstream-contract-violation"},
		{ports.ErrQuoteRejected, 422, "price-quote-rejected"},
		{ports.ErrPricingUnavailable, 503, "pricing-unavailable"},
		{ports.ErrPaymentsUnavailable, 503, "customer-payments-unavailable"},
		{ports.ErrOrderingUnavailable, 503, "customer-ordering-unavailable"},
		{ports.ErrConcurrentUpdate, 409, "concurrent-update"},
		{shared.ErrIllegalTransition, 409, "illegal-transition"},
		{shared.ErrValidation, 422, "validation-failed"},
		{errors.New("boom"), 500, "internal-error"},
	}
	for _, c := range cases {
		status, slug := httpadapter.ProblemOf(c.err)
		if status != c.status || slug != c.slug {
			t.Fatalf("%v -> %d %s, want %d %s", c.err, status, slug, c.status, c.slug)
		}
	}
}

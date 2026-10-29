package http

import (
	"errors"
	"net/http"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// problemBase is the namespace of this service's RFC 9457 `type` URIs.
const problemBase = "/problems/"

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type).
var errMalformedRequest = errors.New("request is not valid for this operation")

// problem is the fixed (status, slug, title) of one error category; the detail
// comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the adapter writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyReused   = problem{http.StatusUnprocessableEntity, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
)

func unprocessable(slug, title string) problem {
	return problem{http.StatusUnprocessableEntity, slug, title}
}

func conflict(slug, title string) problem {
	return problem{http.StatusConflict, slug, title}
}

func unavailable(service string) problem {
	return problem{http.StatusServiceUnavailable, service + "-unavailable", service + " is unavailable; retry with the same Idempotency-Key"}
}

var notFound = problem{http.StatusNotFound, "not-found", "Not found"}

// problemCatalogue maps every typed error to its problem, in match order. The
// slugs are the ones of apis/openapi.yaml and .claude/rules/rest-api.md.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{ports.ErrNotFound, notFound},
	{checkoutsession.ErrCartNotCheckoutReady, conflict("cart-not-checkout-ready", "A group is unselected or an offer expired; fix the cart first")},
	{checkoutsession.ErrSessionClosed, conflict("session-closed", "A completed, expired, failed or requote-required session never changes")},
	{checkoutsession.ErrAuthorizationMismatch, conflict("authorization-mismatch", "The authorized amount is not the quote total; the authorization is being voided")},
	{checkoutsession.ErrPlacementInDoubt, conflict("placement-in-doubt", "The place-order outcome is unknown; poll the session")},
	{checkoutsession.ErrVoidPending, conflict("void-pending", "A previous authorization is still being voided; retry shortly")},
	{checkoutsession.ErrPriceQuoteExpired, unprocessable("price-quote-expired", "The price quote expired; open a new session")},
	{checkoutsession.ErrOfferNotQuotable, unprocessable("offer-not-quotable", "A selected delivery offer is no longer quotable; refresh the delivery quote")},
	{ports.ErrCartNotFound, unprocessable("validation-failed", "Validation failed")},
	{ports.ErrQuoteRejected, unprocessable("price-quote-rejected", "Pricing refused the quote request")},
	{checkoutsession.ErrContractViolation, problem{http.StatusBadGateway, "upstream-contract-violation", "A sibling service answered something the checkout cannot use"}},
	{ports.ErrShoppingCartUnavailable, unavailable("shopping-cart")},
	{ports.ErrDeliveryPromiseUnavailable, unavailable("delivery-promise")},
	{ports.ErrPricingUnavailable, unavailable("pricing")},
	{ports.ErrPaymentsUnavailable, unavailable("customer-payments")},
	{ports.ErrOrderingUnavailable, unavailable("customer-ordering")},
	{ports.ErrConcurrentUpdate, conflict("concurrent-update", "The resource was modified by another request; retry")},
	{shared.ErrIllegalTransition, conflict("illegal-transition", "The session does not allow this change in its current state")},
	{shared.ErrValidation, unprocessable("validation-failed", "Validation failed")},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}

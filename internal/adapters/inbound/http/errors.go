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

// replayClass is what the Idempotency-Key middleware does with the answer of a
// retried request (ADR 0011, fleet rule .claude/rules/fleet/idempotency-and-outbox.md).
// Every problem type declares exactly one; the middleware reads it and keeps no
// list of slugs.
//
//   - replayFinal: the answer is a function of the request alone (or of state
//     that only moves one way for it). Stored under the key and replayed.
//   - replayOutcome: the command COMMITTED state through its own units of work
//     and then answered an error that must be remembered. Stored and replayed;
//     the retry must not run the command again. The key row is a separate
//     write here, so "committed" means the use case had finished, not that the
//     two share a transaction.
//   - replayRetryable: nothing committed and the answer depends on state that
//     can change (a lost optimistic lock, a fact another request or context
//     has not delivered yet, a state the aggregate has not reached, a
//     dependency that is not ready). NOT stored: the key is released and a
//     retry executes again. A 5xx is never stored whatever its class says.
//
// When unsure between FINAL and RETRYABLE the class is RETRYABLE. A problem
// that reaches the middleware without a class is released like RETRYABLE.
type replayClass string

const (
	replayFinal     replayClass = "FINAL"
	replayOutcome   replayClass = "OUTCOME"
	replayRetryable replayClass = "RETRYABLE"
)

// problem is the fixed (status, slug, title, replay class) of one error
// category; the detail comes from the error at write time. Build one with
// newProblem only (TestProblemsAreBuiltByTheConstructorOnly): a problem cannot
// exist without a class, and every problem is in the registry the tests read.
type problem struct {
	status int
	slug   string
	title  string
	class  replayClass
}

// registry holds every problem built by newProblem, in declaration order.
var registry []problem

func newProblem(status int, slug, title string, class replayClass) problem {
	p := problem{status: status, slug: slug, title: title, class: class}
	registry = append(registry, p)
	return p
}

var internalProblem = newProblem(http.StatusInternalServerError, "internal-error", "Internal server error", replayRetryable)

// Problems the adapter writes itself, before any use case runs. The middleware
// answers them without touching its store, so nothing is ever stored for them;
// FINAL records that each is a function of the request (and of the stored
// fingerprint) alone.
var (
	idempotencyKeyRequired = newProblem(http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required", replayFinal)
	idempotencyKeyInvalid  = newProblem(http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is not valid", replayFinal)
	idempotencyKeyReused   = newProblem(http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request", replayFinal)
)

func unprocessable(slug, title string, class replayClass) problem {
	return newProblem(http.StatusUnprocessableEntity, slug, title, class)
}

func conflict(slug, title string, class replayClass) problem {
	return newProblem(http.StatusConflict, slug, title, class)
}

// unavailable is a 503 for a sibling that cannot be reached: a 5xx is never
// stored, so the class is RETRYABLE.
func unavailable(service string) problem {
	return newProblem(http.StatusServiceUnavailable, service+"-unavailable", service+" is unavailable; retry with the same Idempotency-Key", replayRetryable)
}

var (
	// notFound serves the session of the path (minted here, never deleted) and
	// the identifier too long to name one; the slug is shared with causes
	// another request may change (ADR 0011, J1): RETRYABLE.
	notFound = newProblem(http.StatusNotFound, "not-found", "Not found", replayRetryable)
	// validationFailed is MIXED: the request's own fields (shared.ErrValidation)
	// and a cart shopping-cart does not know yet (ports.ErrCartNotFound), so one
	// class for the slug: RETRYABLE (ADR 0011, J2; splitting it is a follow-up).
	validationFailed = unprocessable("validation-failed", "Validation failed", replayRetryable)
)

// problemCatalogue maps every typed error to its problem, in match order. The
// slugs are the ones of apis/openapi.yaml and .claude/rules/rest-api.md. The
// replay class of each is declared on its problem, with the reason in ADR 0011
// (a test holds the ADR table equal to this catalogue).
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, newProblem(http.StatusBadRequest, "malformed-request", "Malformed request", replayFinal)},
	{ports.ErrNotFound, notFound},
	{checkoutsession.ErrCartNotCheckoutReady, conflict("cart-not-checkout-ready", "A group is unselected or an offer expired; fix the cart first", replayRetryable)},
	{checkoutsession.ErrSessionClosed, conflict("session-closed", "A completed, expired, failed or requote-required session never changes", replayFinal)},
	{checkoutsession.ErrAuthorizationMismatch, conflict("authorization-mismatch", "The authorized amount is not the quote total; the authorization is being voided", replayOutcome)},
	{checkoutsession.ErrPlacementInDoubt, conflict("placement-in-doubt", "The place-order outcome is unknown; poll the session", replayRetryable)},
	{checkoutsession.ErrVoidPending, conflict("void-pending", "A previous authorization is still being voided; retry shortly", replayRetryable)},
	{checkoutsession.ErrPriceQuoteExpired, unprocessable("price-quote-expired", "The price quote expired; open a new session", replayFinal)},
	{checkoutsession.ErrOfferNotQuotable, unprocessable("offer-not-quotable", "A selected delivery offer is no longer quotable; refresh the delivery quote", replayRetryable)},
	{ports.ErrCartNotFound, validationFailed},
	{ports.ErrQuoteRejected, unprocessable("price-quote-rejected", "Pricing refused the quote request", replayRetryable)},
	{checkoutsession.ErrContractViolation, newProblem(http.StatusBadGateway, "upstream-contract-violation", "A sibling service answered something the checkout cannot use", replayRetryable)},
	{ports.ErrShoppingCartUnavailable, unavailable("shopping-cart")},
	{ports.ErrDeliveryPromiseUnavailable, unavailable("delivery-promise")},
	{ports.ErrPricingUnavailable, unavailable("pricing")},
	{ports.ErrPaymentsUnavailable, unavailable("customer-payments")},
	{ports.ErrOrderingUnavailable, unavailable("customer-ordering")},
	{ports.ErrConcurrentUpdate, conflict("concurrent-update", "The resource was modified by another request; retry", replayRetryable)},
	{shared.ErrIllegalTransition, conflict("illegal-transition", "The session does not allow this change in its current state", replayRetryable)},
	{shared.ErrValidation, validationFailed},
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

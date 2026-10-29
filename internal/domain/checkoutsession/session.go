// Package checkoutsession is the CheckoutSession aggregate of the checkout
// bounded context: the purchase saga from review to placed order.
//
// States (model + decision H1): OPEN, AUTHORIZING, DECLINED, PLACING,
// REQUOTE_REQUIRED, COMPLETED, EXPIRED, FAILED. Transitions:
//
//	OPEN -> AUTHORIZING (ConfirmSession)      DECLINED -> AUTHORIZING (ConfirmSession)
//	AUTHORIZING -> PLACING (RecordAuthorization)
//	AUTHORIZING -> DECLINED (RecordDecline)
//	PLACING -> PLACING (RecordPlacementInDoubt)
//	PLACING -> COMPLETED (RecordPlaced)
//	PLACING -> REQUOTE_REQUIRED (RecordRequoteRequired)
//	PLACING -> FAILED (RecordFailure)
//	OPEN -> EXPIRED, DECLINED -> EXPIRED (ExpireSession, H1)
//
// COMPLETED, EXPIRED, FAILED and REQUOTE_REQUIRED are closed (ErrSessionClosed).
// PLACING is the semantic lock: no new attempt (ErrPlacementInDoubt) while the
// place-order outcome is unknown. The session holds shopperId and
// shipToAddressId references, never PII; the stored placement request is
// PII-free so it replays byte for byte.
//
// The package imports only the shared domain package and the standard
// library; the cart snapshot, the price quote and the offer states the caller
// fetched are handed in as plain values.
package checkoutsession

import (
	"errors"
	"time"
)

// Domain errors, one per Err of the model (problem types are their kebab-case
// names, see apis/openapi.yaml).
var (
	// ErrCartNotCheckoutReady: the cart's readiness does not hold.
	ErrCartNotCheckoutReady = errors.New("cart not checkout ready")
	// ErrPriceQuoteExpired: the price quote is expired at confirm.
	ErrPriceQuoteExpired = errors.New("price quote expired")
	// ErrSessionClosed: COMPLETED, EXPIRED, FAILED and REQUOTE_REQUIRED never change.
	ErrSessionClosed = errors.New("session closed")
	// ErrAuthorizationMismatch: the authorized amount is not the quote total.
	ErrAuthorizationMismatch = errors.New("authorization mismatch")
	// ErrPlacementInDoubt: no void and no new attempt while PLACING (the semantic lock).
	ErrPlacementInDoubt = errors.New("placement in doubt")
	// ErrOfferNotQuotable (H3): a selected offer is not QUOTED or expires within the margin.
	ErrOfferNotQuotable = errors.New("offer not quotable")
	// ErrVoidPending (H5): a void is still owed, so no new authorization yet.
	ErrVoidPending = errors.New("void pending")
	// ErrContractViolation (S1): sibling data cannot form a consistent session.
	ErrContractViolation = errors.New("sibling contract violation")
)

// Bounds and timings.
const (
	MaxLines     = 100
	MaxQuantity  = 99
	OfferMargin  = 2 * time.Minute
	MaxBackoff   = 15 * time.Minute
	firstBackoff = 30 * time.Second
)

// ID is the sessionId, the aggregate root id.
type ID string

// State is the lifecycle state of the session.
type State string

// The states.
const (
	StateOpen            State = "OPEN"
	StateAuthorizing     State = "AUTHORIZING"
	StateDeclined        State = "DECLINED"
	StatePlacing         State = "PLACING"
	StateRequoteRequired State = "REQUOTE_REQUIRED"
	StateCompleted       State = "COMPLETED"
	StateExpired         State = "EXPIRED"
	StateFailed          State = "FAILED"
)

// Valid reports whether s is one of the eight states.
func (s State) Valid() bool {
	switch s {
	case StateOpen, StateAuthorizing, StateDeclined, StatePlacing,
		StateRequoteRequired, StateCompleted, StateExpired, StateFailed:
		return true
	}
	return false
}

// Closed reports whether no transition leaves s (ErrSessionClosed).
func (s State) Closed() bool {
	return s == StateCompleted || s == StateExpired || s == StateFailed || s == StateRequoteRequired
}

// Tier is a delivery tier.
type Tier string

// The tiers.
const (
	TierSameDay  Tier = "SAME_DAY"
	TierNextDay  Tier = "NEXT_DAY"
	TierTwoDay   Tier = "TWO_DAY"
	TierStandard Tier = "STANDARD"
)

// Valid reports whether t is one of the four tiers.
func (t Tier) Valid() bool {
	return t == TierSameDay || t == TierNextDay || t == TierTwoDay || t == TierStandard
}

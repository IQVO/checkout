// Package exchange holds the plain data that crosses the application's
// outbound gateway ports (requests to and answers from the sibling services).
// It is data only; the interfaces live in package ports.
package exchange

import "github.com/IQVO/checkout/internal/domain/checkoutsession"

// Cart is the cart as checkout needs it: the domain snapshot (lines, selected
// groups, readiness) plus the destination the pricing call needs.
type Cart struct {
	Snapshot                checkoutsession.CartSnapshot
	DestinationPostalPrefix string
}

// QuoteRequest is the IssuePriceQuote request: exactly what the session froze.
type QuoteRequest struct {
	CartID                  string
	DestinationPostalPrefix string
	Lines                   []checkoutsession.Line
	Groups                  []checkoutsession.Group
}

// Allocation is the share of an authorization that belongs to one group.
type Allocation struct {
	GroupNo     int
	AmountMinor int64
}

// AuthorizeRequest is the AuthorizePayment request.
type AuthorizeRequest struct {
	SessionID   string
	Token       string
	Currency    string
	TotalMinor  int64
	Allocations []Allocation
}

// Authorization is the answer of AuthorizePayment: Authorized, or declined
// with a reason (the authorization row exists in DECLINED state, H7).
type Authorization struct {
	ID            string
	Authorized    bool
	Currency      string
	TotalMinor    int64
	DeclineReason string
}

// PlacementDraft is what the Anti-Corruption Layer needs to build the
// place-order request: references only, never PII.
type PlacementDraft struct {
	ShopperID       string
	ShipToAddressID string
	Lines           []checkoutsession.Line
	Groups          []checkoutsession.Group
}

// OutcomeKind classifies a definitive answer of the place-order step.
type OutcomeKind int

// The definitive outcomes. An unknown outcome is the error ErrOrderingUnavailable.
const (
	// OutcomePlaced: the order exists (201, or the replayed first answer).
	OutcomePlaced OutcomeKind = iota + 1
	// OutcomeRequote: a definitive 409 (offer-not-quotable,
	// promise-capacity-exhausted, book-closed) carrying re-quoted offers.
	OutcomeRequote
	// OutcomeRejected: a 400/422, a contract bug on our side.
	OutcomeRejected
)

// PlaceOutcome is the definitive answer of PlaceOrder.
type PlaceOutcome struct {
	Kind    OutcomeKind
	OrderID string
	Offers  []checkoutsession.RequotedOffer
	Detail  string
}

// OrderPlacer is customer-ordering PlaceOrder (REST POST /checkout) behind the

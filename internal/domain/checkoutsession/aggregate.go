package checkoutsession

import (
	"time"

	"github.com/IQVO/checkout/internal/domain/shared"
)

// CheckoutSession is the aggregate root.
type CheckoutSession struct {
	id               ID
	state            State
	cartID           string
	cartVersion      int64
	shopperID        string
	shipToAddressID  string
	lines            []Line
	groups           []Group
	priceQuoteID     string
	currency         string
	totalMinor       int64
	quoteExpiresAt   time.Time
	authorizationID  string
	orderID          string
	requoted         []RequotedOffer
	failureCode      string
	declineReason    string
	authAttempt      int
	pendingToken     string
	authorizingSince time.Time
	placement        *StoredPlacement
	placementTries   int
	nextAttemptAt    time.Time
	alertedAt        time.Time
	voidOwed         *VoidOwed
	terminalAt       time.Time
	createdAt        time.Time
	updatedAt        time.Time
	version          int64
	loaded           int64
	pending          []shared.Event
}

// ID returns the sessionId.
func (s *CheckoutSession) ID() ID { return s.id }

// State returns the lifecycle state.
func (s *CheckoutSession) State() State { return s.state }

// CartID returns the cart the session was opened for.
func (s *CheckoutSession) CartID() string { return s.cartID }

// CartVersion returns the cart version the snapshot was read at.
func (s *CheckoutSession) CartVersion() int64 { return s.cartVersion }

// ShopperID returns the shopper reference.
func (s *CheckoutSession) ShopperID() string { return s.shopperID }

// ShipToAddressID returns the saved-address reference (never the address).
func (s *CheckoutSession) ShipToAddressID() string { return s.shipToAddressID }

// Lines returns a copy of the numbered lines.
func (s *CheckoutSession) Lines() []Line { return append([]Line(nil), s.lines...) }

// Groups returns a copy of the shipment groups.
func (s *CheckoutSession) Groups() []Group { return cloneGroups(s.groups) }

// PriceQuoteID returns the binding price quote id.
func (s *CheckoutSession) PriceQuoteID() string { return s.priceQuoteID }

// Currency returns the ISO currency of the quote.
func (s *CheckoutSession) Currency() string { return s.currency }

// TotalMinor returns the binding quote total in minor units.
func (s *CheckoutSession) TotalMinor() int64 { return s.totalMinor }

// QuoteExpiresAt returns when the binding quote expires.
func (s *CheckoutSession) QuoteExpiresAt() time.Time { return s.quoteExpiresAt }

// AuthorizationID returns the payment authorization id, empty before one exists.
func (s *CheckoutSession) AuthorizationID() string { return s.authorizationID }

// OrderID returns the placed order id, empty until COMPLETED.
func (s *CheckoutSession) OrderID() string { return s.orderID }

// RequotedOffers returns a copy of the offers carried by REQUOTE_REQUIRED.
func (s *CheckoutSession) RequotedOffers() []RequotedOffer {
	return append([]RequotedOffer(nil), s.requoted...)
}

// FailureCode returns the failure code, empty unless FAILED.
func (s *CheckoutSession) FailureCode() string { return s.failureCode }

// DeclineReason returns the reason of the last decline, empty if none.
func (s *CheckoutSession) DeclineReason() string { return s.declineReason }

// AuthAttempt returns the authorization attempt counter (decision H2).
func (s *CheckoutSession) AuthAttempt() int { return s.authAttempt }

// PendingToken returns the PSP token of the attempt in flight, empty otherwise.
func (s *CheckoutSession) PendingToken() string { return s.pendingToken }

// AuthorizingSince returns when AUTHORIZING was entered, zero outside it.
func (s *CheckoutSession) AuthorizingSince() time.Time { return s.authorizingSince }

// Placement returns a copy of the stored placement request, nil if none.
func (s *CheckoutSession) Placement() *StoredPlacement { return clonePlacement(s.placement) }

// PlacementAttempts returns how many in-doubt passes were recorded.
func (s *CheckoutSession) PlacementAttempts() int { return s.placementTries }

// NextAttemptAt returns when the resolver may try again, zero when due now.
func (s *CheckoutSession) NextAttemptAt() time.Time { return s.nextAttemptAt }

// AlertedAt returns when the 20 h alert fired, zero if it has not.
func (s *CheckoutSession) AlertedAt() time.Time { return s.alertedAt }

// VoidOwed returns the owed void, nil when none is owed.
func (s *CheckoutSession) VoidOwed() *VoidOwed {
	if s.voidOwed == nil {
		return nil
	}
	v := *s.voidOwed
	return &v
}

// TerminalAt returns when a closed state was entered, zero while open.
func (s *CheckoutSession) TerminalAt() time.Time { return s.terminalAt }

// CreatedAt returns when the session was opened.
func (s *CheckoutSession) CreatedAt() time.Time { return s.createdAt }

// UpdatedAt returns the instant of the last change.
func (s *CheckoutSession) UpdatedAt() time.Time { return s.updatedAt }

// Version returns the aggregate version (1 on creation, +1 per change).
func (s *CheckoutSession) Version() int64 { return s.version }

// LoadedVersion returns the version the aggregate was loaded at (0 when new).
func (s *CheckoutSession) LoadedVersion() int64 { return s.loaded }

// MarkPersisted records that the current version is stored.
func (s *CheckoutSession) MarkPersisted() { s.loaded = s.version }

// PullEvents returns the events raised since the last call and clears them.
func (s *CheckoutSession) PullEvents() []shared.Event {
	out := s.pending
	s.pending = nil
	return out
}

// touch bumps the version and the change instant.
func (s *CheckoutSession) touch(now time.Time) time.Time {
	at := shared.UTC(now)
	s.version++
	s.updatedAt = at
	return at
}

// raise appends an event stamped with the current version.
func (s *CheckoutSession) raise(build func(h shared.Header) shared.Event, at time.Time) {
	s.pending = append(s.pending, build(shared.Header{Version: s.version, At: at}))
}

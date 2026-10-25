package checkoutsession

import (
	"time"

	"github.com/IQVO/checkout/internal/domain/shared"
)

const entity = "checkoutsession"

// CheckoutOpened is raised by OpenSession.
type CheckoutOpened struct {
	shared.Header
	SessionID  ID
	CartID     string
	ShopperID  string
	TotalMinor int64
}

// CheckoutDeclined is raised by RecordDecline.
type CheckoutDeclined struct {
	shared.Header
	SessionID ID
	Reason    string
}

// CheckoutPlacementInDoubt is raised the first time the place-order outcome is unknown.
type CheckoutPlacementInDoubt struct {
	shared.Header
	SessionID    ID
	PlacingSince time.Time
}

// CheckoutCompleted is raised by RecordPlaced.
type CheckoutCompleted struct {
	shared.Header
	SessionID       ID
	OrderID         string
	ShopperID       string
	CartID          string
	AuthorizationID string
	Currency        string
	TotalMinor      int64
	Groups          []Group
}

// CheckoutRequoteRequired is raised by RecordRequoteRequired.
type CheckoutRequoteRequired struct {
	shared.Header
	SessionID      ID
	RequotedOffers []RequotedOffer
}

// CheckoutFailed is raised by RecordFailure.
type CheckoutFailed struct {
	shared.Header
	SessionID ID
	Code      string
}

// CheckoutSessionExpired is raised by ExpireSession.
type CheckoutSessionExpired struct {
	shared.Header
	SessionID ID
}

// EventName returns "CheckoutOpened".
func (CheckoutOpened) EventName() string { return "CheckoutOpened" }

// EventName returns "CheckoutDeclined".
func (CheckoutDeclined) EventName() string { return "CheckoutDeclined" }

// EventName returns "CheckoutPlacementInDoubt".
func (CheckoutPlacementInDoubt) EventName() string { return "CheckoutPlacementInDoubt" }

// EventName returns "CheckoutCompleted".
func (CheckoutCompleted) EventName() string { return "CheckoutCompleted" }

// EventName returns "CheckoutRequoteRequired".
func (CheckoutRequoteRequired) EventName() string { return "CheckoutRequoteRequired" }

// EventName returns "CheckoutFailed".
func (CheckoutFailed) EventName() string { return "CheckoutFailed" }

// EventName returns "CheckoutSessionExpired".
func (CheckoutSessionExpired) EventName() string { return "CheckoutSessionExpired" }

// Entity returns "checkoutsession".
func (CheckoutOpened) Entity() string { return entity }

// Entity returns "checkoutsession".
func (CheckoutDeclined) Entity() string { return entity }

// Entity returns "checkoutsession".
func (CheckoutPlacementInDoubt) Entity() string { return entity }

// Entity returns "checkoutsession".
func (CheckoutCompleted) Entity() string { return entity }

// Entity returns "checkoutsession".
func (CheckoutRequoteRequired) Entity() string { return entity }

// Entity returns "checkoutsession".
func (CheckoutFailed) Entity() string { return entity }

// Entity returns "checkoutsession".
func (CheckoutSessionExpired) Entity() string { return entity }

// SubjectID returns the sessionId.
func (e CheckoutOpened) SubjectID() string { return string(e.SessionID) }

// SubjectID returns the sessionId.
func (e CheckoutDeclined) SubjectID() string { return string(e.SessionID) }

// SubjectID returns the sessionId.
func (e CheckoutPlacementInDoubt) SubjectID() string { return string(e.SessionID) }

// SubjectID returns the sessionId.
func (e CheckoutCompleted) SubjectID() string { return string(e.SessionID) }

// SubjectID returns the sessionId.
func (e CheckoutRequoteRequired) SubjectID() string { return string(e.SessionID) }

// SubjectID returns the sessionId.
func (e CheckoutFailed) SubjectID() string { return string(e.SessionID) }

// SubjectID returns the sessionId.
func (e CheckoutSessionExpired) SubjectID() string { return string(e.SessionID) }

var (
	_ shared.Event = CheckoutOpened{}
	_ shared.Event = CheckoutDeclined{}
	_ shared.Event = CheckoutPlacementInDoubt{}
	_ shared.Event = CheckoutCompleted{}
	_ shared.Event = CheckoutRequoteRequired{}
	_ shared.Event = CheckoutFailed{}
	_ shared.Event = CheckoutSessionExpired{}
)

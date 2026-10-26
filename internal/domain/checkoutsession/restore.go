package checkoutsession

import (
	"errors"
	"fmt"
	"time"

	"github.com/IQVO/checkout/internal/domain/shared"
)

// Snapshot is the persistable state of a CheckoutSession. Round-tripping
// through Snapshot and Restore loses nothing (repository doubles rely on it).
type Snapshot struct {
	ID                ID
	State             State
	CartID            string
	CartVersion       int64
	ShopperID         string
	ShipToAddressID   string
	Lines             []Line
	Groups            []Group
	PriceQuoteID      string
	Currency          string
	TotalMinor        int64
	QuoteExpiresAt    time.Time
	AuthorizationID   string
	OrderID           string
	Requoted          []RequotedOffer
	FailureCode       string
	DeclineReason     string
	AuthAttempt       int
	PendingToken      string
	AuthorizingSince  time.Time
	Placement         *StoredPlacement
	PlacementAttempts int
	NextAttemptAt     time.Time
	AlertedAt         time.Time
	VoidOwed          *VoidOwed
	TerminalAt        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Version           int64
}

// Snapshot returns the persistable state.
func (s *CheckoutSession) Snapshot() Snapshot {
	return Snapshot{
		ID: s.id, State: s.state, CartID: s.cartID, CartVersion: s.cartVersion, ShopperID: s.shopperID,
		ShipToAddressID: s.shipToAddressID, Lines: s.Lines(), Groups: s.Groups(), PriceQuoteID: s.priceQuoteID,
		Currency: s.currency, TotalMinor: s.totalMinor, QuoteExpiresAt: s.quoteExpiresAt,
		AuthorizationID: s.authorizationID, OrderID: s.orderID, Requoted: s.RequotedOffers(),
		FailureCode: s.failureCode, DeclineReason: s.declineReason, AuthAttempt: s.authAttempt,
		PendingToken: s.pendingToken, AuthorizingSince: s.authorizingSince, Placement: s.Placement(),
		PlacementAttempts: s.placementTries, NextAttemptAt: s.nextAttemptAt, AlertedAt: s.alertedAt,
		VoidOwed: s.VoidOwed(), TerminalAt: s.terminalAt, CreatedAt: s.createdAt, UpdatedAt: s.updatedAt, Version: s.version,
	}
}

// Restore rebuilds a persisted session, validating the invariants a stored row
// must keep. A violation is shared.ErrInvalidSnapshot.
func Restore(sn Snapshot) (*CheckoutSession, error) {
	if err := checkSnapshot(sn); err != nil {
		return nil, shared.Corrupt("checkout session %q: %v", sn.ID, err)
	}
	return &CheckoutSession{
		id: sn.ID, state: sn.State, cartID: sn.CartID, cartVersion: sn.CartVersion, shopperID: sn.ShopperID,
		shipToAddressID: sn.ShipToAddressID, lines: append([]Line(nil), sn.Lines...), groups: cloneGroups(sn.Groups),
		priceQuoteID: sn.PriceQuoteID, currency: sn.Currency, totalMinor: sn.TotalMinor,
		quoteExpiresAt: shared.UTC(sn.QuoteExpiresAt), authorizationID: sn.AuthorizationID, orderID: sn.OrderID,
		requoted: append([]RequotedOffer(nil), sn.Requoted...), failureCode: sn.FailureCode,
		declineReason: sn.DeclineReason, authAttempt: sn.AuthAttempt, pendingToken: sn.PendingToken,
		authorizingSince: shared.UTC(sn.AuthorizingSince), placement: restorePlacement(sn.Placement),
		placementTries: sn.PlacementAttempts, nextAttemptAt: shared.UTC(sn.NextAttemptAt),
		alertedAt: shared.UTC(sn.AlertedAt), voidOwed: restoreVoid(sn.VoidOwed), terminalAt: shared.UTC(sn.TerminalAt),
		createdAt: shared.UTC(sn.CreatedAt), updatedAt: shared.UTC(sn.UpdatedAt), version: sn.Version, loaded: sn.Version,
	}, nil
}

func restorePlacement(p *StoredPlacement) *StoredPlacement {
	c := clonePlacement(p)
	if c != nil {
		c.PlacingSince = shared.UTC(c.PlacingSince)
	}
	return c
}

func restoreVoid(v *VoidOwed) *VoidOwed {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func checkSnapshot(sn Snapshot) error {
	if !shared.ValidID(string(sn.ID)) || !shared.ValidID(sn.CartID) || !shared.ValidID(sn.ShopperID) || !shared.ValidID(sn.ShipToAddressID) {
		return errors.New("sessionId, cartId, shopperId and shipToAddressId must be valid ids")
	}
	if !sn.State.Valid() {
		return fmt.Errorf("state %q is unknown", sn.State)
	}
	if err := checkRequired(sn); err != nil {
		return err
	}
	return checkStateFields(sn)
}

func checkRequired(sn Snapshot) error {
	if sn.Version < 1 || sn.CartVersion < 1 || sn.CreatedAt.IsZero() || sn.UpdatedAt.IsZero() || sn.QuoteExpiresAt.IsZero() {
		return errors.New("versions, createdAt, updatedAt and quoteExpiresAt are required")
	}
	if len(sn.Lines) == 0 || len(sn.Groups) == 0 || sn.TotalMinor < 0 || len(sn.Currency) != 3 || sn.AuthAttempt < 0 {
		return errors.New("lines, groups, a currency and a non-negative total are required")
	}
	return nil
}

// checkStateFields keeps the per-state invariants: which fields a state needs.
func checkStateFields(sn Snapshot) error {
	switch sn.State {
	case StateAuthorizing:
		if sn.PendingToken == "" || sn.AuthorizingSince.IsZero() {
			return errors.New("AUTHORIZING needs the pending token and its start")
		}
	case StatePlacing, StateCompleted, StateRequoteRequired, StateFailed:
		if sn.AuthorizationID == "" {
			return fmt.Errorf("%s needs an authorizationId", sn.State)
		}
	}
	if sn.State == StatePlacing && sn.Placement == nil {
		return errors.New("PLACING needs the stored placement request")
	}
	if sn.State == StateCompleted && sn.OrderID == "" {
		return errors.New("COMPLETED needs an orderId")
	}
	if sn.State.Closed() && sn.TerminalAt.IsZero() {
		return errors.New("a closed state needs terminalAt")
	}
	return nil
}

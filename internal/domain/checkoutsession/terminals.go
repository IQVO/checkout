package checkoutsession

import (
	"time"

	"github.com/IQVO/checkout/internal/domain/shared"
)

// RecordPlaced is command RecordPlaced: PLACING becomes COMPLETED.
func (s *CheckoutSession) RecordPlaced(orderID string, now time.Time) error {
	if err := s.require("RecordPlaced", StatePlacing); err != nil {
		return err
	}
	if !shared.ValidID(orderID) {
		return shared.Invalid("orderId must be a valid id")
	}
	at := s.close(StateCompleted, now)
	s.orderID = orderID
	s.raise(func(h shared.Header) shared.Event {
		return CheckoutCompleted{
			Header: h, SessionID: s.id, OrderID: orderID, ShopperID: s.shopperID, CartID: s.cartID,
			AuthorizationID: s.authorizationID, Currency: s.currency, TotalMinor: s.totalMinor, Groups: cloneGroups(s.groups),
		}
	}, at)
	return nil
}

// RecordRequoteRequired is command RecordRequoteRequired: PLACING becomes
// REQUOTE_REQUIRED carrying the re-quoted offers. The decision to void the
// authorization is recorded in the same change (voidOwed, decision H5).
func (s *CheckoutSession) RecordRequoteRequired(offers []RequotedOffer, now time.Time) error {
	if err := s.require("RecordRequoteRequired", StatePlacing); err != nil {
		return err
	}
	for _, o := range offers {
		if err := o.validate(); err != nil {
			return err
		}
	}
	at := s.close(StateRequoteRequired, now)
	s.requoted = append([]RequotedOffer(nil), offers...)
	s.voidOwed = &VoidOwed{AuthorizationID: s.authorizationID}
	s.raise(func(h shared.Header) shared.Event {
		return CheckoutRequoteRequired{Header: h, SessionID: s.id, RequotedOffers: append([]RequotedOffer(nil), offers...)}
	}, at)
	return nil
}

// RecordFailure is command RecordFailure: PLACING becomes FAILED with a code
// and the void owed (decision H5).
func (s *CheckoutSession) RecordFailure(code string, now time.Time) error {
	if err := s.require("RecordFailure", StatePlacing); err != nil {
		return err
	}
	if !shared.ValidID(code) {
		return shared.Invalid("a failure code is required")
	}
	at := s.close(StateFailed, now)
	s.failureCode = code
	s.voidOwed = &VoidOwed{AuthorizationID: s.authorizationID}
	s.raise(func(h shared.Header) shared.Event {
		return CheckoutFailed{Header: h, SessionID: s.id, Code: code}
	}, at)
	return nil
}

// ExpireSession is command ExpireSession (policy ExpireOnQuoteExpiry): an OPEN
// or DECLINED session expires with its price quote (decision H1).
func (s *CheckoutSession) ExpireSession(now time.Time) error {
	if err := s.require("ExpireSession", StateOpen, StateDeclined); err != nil {
		return err
	}
	if shared.UTC(now).Before(s.quoteExpiresAt) {
		return shared.Invalid("the price quote has not expired yet")
	}
	at := s.close(StateExpired, now)
	s.raise(func(h shared.Header) shared.Event {
		return CheckoutSessionExpired{Header: h, SessionID: s.id}
	}, at)
	return nil
}

func (s *CheckoutSession) close(to State, now time.Time) time.Time {
	at := s.touch(now)
	s.state = to
	s.terminalAt = at
	s.nextAttemptAt = time.Time{}
	return at
}

// VoidCompleted records that the owed void succeeded (policy RetryOwedVoids).
func (s *CheckoutSession) VoidCompleted(now time.Time) error {
	if s.voidOwed == nil {
		return shared.ErrIllegalTransition
	}
	s.touch(now)
	s.voidOwed = nil
	return nil
}

// MarkInDoubtAlerted records that the 20 h alert fired (it fires once).
func (s *CheckoutSession) MarkInDoubtAlerted(now time.Time) error {
	if err := s.require("MarkInDoubtAlerted", StatePlacing); err != nil {
		return err
	}
	s.alertedAt = s.touch(now)
	return nil
}

// PurgePlacement drops the stored placement request 24 h after a closed state
// (decision H9).
func (s *CheckoutSession) PurgePlacement(now time.Time) error {
	if !s.state.Closed() || s.placement == nil || shared.UTC(now).Before(s.terminalAt.Add(PurgePlacementAfter)) {
		return shared.ErrIllegalTransition
	}
	s.touch(now)
	s.placement = nil
	return nil
}

// QuoteExpired reports whether the binding quote has expired at now.
func (s *CheckoutSession) QuoteExpired(now time.Time) bool {
	return !shared.UTC(now).Before(s.quoteExpiresAt)
}

// AuthorizationStuck reports an AUTHORIZING session older than 2 minutes (H4).
func (s *CheckoutSession) AuthorizationStuck(now time.Time) bool {
	return s.state == StateAuthorizing && !shared.UTC(now).Before(s.authorizingSince.Add(StuckAuthorizationAfter))
}

// ResolutionDue reports a PLACING session whose resolver pass is due.
func (s *CheckoutSession) ResolutionDue(now time.Time) bool {
	return s.state == StatePlacing && !shared.UTC(now).Before(s.nextAttemptAt)
}

// InDoubtFor returns how long the session has been PLACING, zero otherwise.
func (s *CheckoutSession) InDoubtFor(now time.Time) time.Duration {
	if s.state != StatePlacing || s.placement == nil {
		return 0
	}
	return shared.UTC(now).Sub(s.placement.PlacingSince)
}

// AlertDue reports a PLACING session at or past 20 h that has not alerted yet.
func (s *CheckoutSession) AlertDue(now time.Time) bool {
	return s.alertedAt.IsZero() && s.InDoubtFor(now) >= InDoubtAlertAfter
}

// GiveUpDue reports a PLACING session at or past 22 h (policy gives up and voids).
func (s *CheckoutSession) GiveUpDue(now time.Time) bool {
	return s.InDoubtFor(now) >= InDoubtGiveUpAfter
}

package checkoutsession

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/IQVO/checkout/internal/domain/shared"
)

// Timings of the policies (decisions H4, H6, H9).
const (
	StuckAuthorizationAfter = 2 * time.Minute
	InDoubtAlertAfter       = 20 * time.Hour
	InDoubtGiveUpAfter      = 22 * time.Hour
	PurgePlacementAfter     = 24 * time.Hour
	maxTokenLen             = 512
	maxReasonLen            = 200
)

// require returns nil when the session is in one of the wanted states, else
// ErrSessionClosed for a closed state or an illegal-transition error.
func (s *CheckoutSession) require(command string, want ...State) error {
	for _, w := range want {
		if s.state == w {
			return nil
		}
	}
	if s.state.Closed() {
		return ErrSessionClosed
	}
	return fmt.Errorf("%w: %s from %s", shared.ErrIllegalTransition, command, s.state)
}

// Confirm is command ConfirmSession: OPEN or DECLINED becomes AUTHORIZING. The
// PSP token is kept only while AUTHORIZING so a stuck authorization can be
// re-called with the same key (decision H4). Errors: ErrSessionClosed,
// ErrPlacementInDoubt (PLACING), ErrVoidPending, ErrPriceQuoteExpired,
// shared.ErrValidation, shared.ErrIllegalTransition (already AUTHORIZING).
func (s *CheckoutSession) Confirm(token string, now time.Time) error {
	if s.state == StatePlacing {
		return ErrPlacementInDoubt
	}
	if err := s.require("ConfirmSession", StateOpen, StateDeclined); err != nil {
		return err
	}
	if s.voidOwed != nil {
		return ErrVoidPending
	}
	if !shared.ValidTextN(token, maxTokenLen) {
		return shared.Invalid("paymentMethodToken must be a non-blank token of at most %d characters", maxTokenLen)
	}
	if !shared.UTC(now).Before(s.quoteExpiresAt) {
		return ErrPriceQuoteExpired
	}
	if s.state == StateDeclined {
		s.authAttempt++
	}
	at := s.touch(now)
	s.state = StateAuthorizing
	s.pendingToken = token
	s.authorizingSince = at
	s.declineReason = ""
	return nil
}

// RecordAuthorization is command RecordAuthorization: AUTHORIZING becomes
// PLACING and the PII-free placement request is stored in the same change.
// When the authorized amount or currency is not the quote's, the session goes
// back to DECLINED with the void owed (decision H5) and ErrAuthorizationMismatch
// is returned: the caller must still save the session.
func (s *CheckoutSession) RecordAuthorization(authorizationID string, authorizedMinor int64, currency string, request []byte, now time.Time) error {
	if err := s.require("RecordAuthorization", StateAuthorizing); err != nil {
		return err
	}
	if !shared.ValidID(authorizationID) {
		return shared.Invalid("authorizationId must be a valid id")
	}
	if authorizedMinor != s.totalMinor || currency != s.currency {
		s.decline("authorization-mismatch", now)
		s.voidOwed = &VoidOwed{AuthorizationID: authorizationID}
		return ErrAuthorizationMismatch
	}
	if len(request) == 0 {
		return shared.Invalid("a placement request is required")
	}
	sum := sha256.Sum256(request)
	at := s.touch(now)
	s.state = StatePlacing
	s.authorizationID = authorizationID
	s.pendingToken = ""
	s.authorizingSince = time.Time{}
	s.placement = &StoredPlacement{RequestBody: append([]byte(nil), request...), RequestHash: hex.EncodeToString(sum[:]), PlacingSince: at}
	return nil
}

// RecordDecline is command RecordDecline: AUTHORIZING becomes DECLINED.
func (s *CheckoutSession) RecordDecline(reason string, now time.Time) error {
	if err := s.require("RecordDecline", StateAuthorizing); err != nil {
		return err
	}
	if !shared.ValidTextN(reason, maxReasonLen) {
		return shared.Invalid("a decline reason of at most %d characters is required", maxReasonLen)
	}
	s.decline(reason, now)
	return nil
}

func (s *CheckoutSession) decline(reason string, now time.Time) {
	at := s.touch(now)
	s.state = StateDeclined
	s.declineReason = reason
	s.pendingToken = ""
	s.authorizingSince = time.Time{}
	s.raise(func(h shared.Header) shared.Event {
		return CheckoutDeclined{Header: h, SessionID: s.id, Reason: reason}
	}, at)
}

// RecordPlacementInDoubt is command RecordPlacementInDoubt: PLACING stays
// PLACING (the semantic lock), the attempt count grows and the next attempt is
// pushed out by an exponential backoff. The event is raised on the first one.
func (s *CheckoutSession) RecordPlacementInDoubt(now time.Time) error {
	if err := s.require("RecordPlacementInDoubt", StatePlacing); err != nil {
		return err
	}
	at := s.touch(now)
	s.placementTries++
	s.nextAttemptAt = at.Add(backoff(s.placementTries))
	if s.placementTries == 1 {
		since := s.placement.PlacingSince
		s.raise(func(h shared.Header) shared.Event {
			return CheckoutPlacementInDoubt{Header: h, SessionID: s.id, PlacingSince: since}
		}, at)
	}
	return nil
}

func backoff(attempt int) time.Duration {
	d := firstBackoff
	for i := 1; i < attempt && d < MaxBackoff; i++ {
		d *= 2
	}
	if d > MaxBackoff {
		return MaxBackoff
	}
	return d
}

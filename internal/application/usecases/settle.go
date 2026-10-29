package usecases

import (
	"context"
	"fmt"

	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/views"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// failurePlaceRejected is the failure code of a 400/422 from the place-order
// step; failureUnresolved the one of the 22 h give-up (decision H6).
const (
	failurePlaceRejected = "place-order-rejected"
	failureUnresolved    = "placement-unresolved"
)

// place sends the stored placement request (Idempotency-Key = sessionId) and
// settles whatever the answer is. It serves ConfirmSession and
// ResolveInDoubtPlacements: the same body and key on every pass, so a pass
// that raced a slow first call cannot place a second order.
func (d Deps) place(ctx context.Context, s *checkoutsession.CheckoutSession) (views.Session, error) {
	out, err := d.Placer.Place(ctx, string(s.ID()), s.Placement().RequestBody)
	return d.settlePlacement(ctx, s.ID(), out, err)
}

// settlePlacement applies the answer of the place-order step: an error of any
// kind is an unknown outcome (the semantic lock holds), a definitive answer is
// recorded and, for a requote or a rejection, followed by the owed void.
func (d Deps) settlePlacement(ctx context.Context, id checkoutsession.ID, out exchange.PlaceOutcome, placeErr error) (views.Session, error) {
	s, err := d.mutate(ctx, id, func(s *checkoutsession.CheckoutSession) error {
		return d.record(s, out, placeErr)
	})
	if superseded(err) {
		return d.current(ctx, id)
	}
	if err != nil {
		return views.Session{}, err
	}
	if placeErr == nil && out.Kind == exchange.OutcomeRejected {
		d.alert(ctx, "place-order-rejected", string(id), out.Detail)
	}
	if s.VoidOwed() != nil {
		if after := d.settleVoid(ctx, id); after != nil {
			s = after
		}
	}
	return views.Of(s), nil
}

func (d Deps) record(s *checkoutsession.CheckoutSession, out exchange.PlaceOutcome, placeErr error) error {
	now := d.now()
	switch {
	case placeErr != nil:
		return s.RecordPlacementInDoubt(now)
	case out.Kind == exchange.OutcomePlaced:
		return s.RecordPlaced(out.OrderID, now)
	case out.Kind == exchange.OutcomeRequote:
		return s.RecordRequoteRequired(out.Offers, now)
	case out.Kind == exchange.OutcomeRejected:
		return s.RecordFailure(failurePlaceRejected, now)
	}
	return shared.Invalid("unknown place-order outcome %d", out.Kind)
}

// settleVoid voids the authorization a session owes (key <sessionId>:void) and
// records it. A failed void is not an error: the debt stays on the session and
// RetryOwedVoids tries again every minute (decision H5). It returns the
// session as it is afterwards, nil when it cannot be read.
func (d Deps) settleVoid(ctx context.Context, id checkoutsession.ID) *checkoutsession.CheckoutSession {
	s, err := d.Sessions.Get(ctx, id)
	if err != nil {
		return nil
	}
	if s.VoidOwed() == nil {
		return s
	}
	if err := d.Payments.Void(ctx, fmt.Sprintf("%s:void", id), s.VoidOwed().AuthorizationID); err != nil {
		d.warn(ctx, "void failed, it stays owed", "sessionId", string(id), "error", err)
		return s
	}
	done, err := d.mutate(ctx, id, func(s *checkoutsession.CheckoutSession) error { return s.VoidCompleted(d.now()) })
	if err != nil {
		if !superseded(err) {
			d.warn(ctx, "recording the void failed", "sessionId", string(id), "error", err)
		}
		return s
	}
	return done
}

package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// ResolveInDoubtPlacements is the policy ResolveInDoubtPlacements: re-send the
// stored body with the same Idempotency-Key (customer-ordering replays its
// first response), with backoff, until a definitive answer. Timeline (decision
// H6): alert at 20 h, one last try and then give up and void at 22 h, both
// before customer-payments' 24 h backstop. The void is idempotent, so a race
// with that backstop is harmless.
type ResolveInDoubtPlacements struct{ Deps }

// Execute handles up to limit PLACING sessions that are due and returns how
// many it looked at.
func (u ResolveInDoubtPlacements) Execute(ctx context.Context, limit int) (int, error) {
	list, err := u.Sessions.ListPlacingDue(ctx, u.now(), limit)
	if err != nil {
		return 0, err
	}
	return sweep(list, func(s *checkoutsession.CheckoutSession) error {
		return u.resolve(ctx, s)
	})
}

func (u ResolveInDoubtPlacements) resolve(ctx context.Context, s *checkoutsession.CheckoutSession) error {
	if s.AlertDue(u.now()) {
		u.alert(ctx, "placement-in-doubt-20h", string(s.ID()), fmt.Sprintf("in doubt for %s", s.InDoubtFor(u.now()).Round(time.Minute)))
		marked, err := u.mutate(ctx, s.ID(), func(s *checkoutsession.CheckoutSession) error { return s.MarkInDoubtAlerted(u.now()) })
		if err != nil {
			return err
		}
		s = marked
	}
	out, placeErr := u.Placer.Place(ctx, string(s.ID()), s.Placement().RequestBody)
	if placeErr != nil && s.GiveUpDue(u.now()) {
		return u.giveUp(ctx, s.ID())
	}
	_, err := u.settlePlacement(ctx, s.ID(), out, placeErr)
	return err
}

// giveUp ends a placement that stayed unresolved for 22 h: FAILED with the
// void owed, then the void.
func (u ResolveInDoubtPlacements) giveUp(ctx context.Context, id checkoutsession.ID) error {
	_, err := u.mutate(ctx, id, func(s *checkoutsession.CheckoutSession) error { return s.RecordFailure(failureUnresolved, u.now()) })
	if err != nil {
		return err
	}
	u.alert(ctx, "placement-given-up-22h", string(id), "no definitive answer from customer-ordering; the authorization is being voided")
	u.settleVoid(ctx, id)
	return nil
}

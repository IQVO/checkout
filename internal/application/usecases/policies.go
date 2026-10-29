package usecases

import (
	"context"

	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// sweep runs step for every session of a scan. A session that another actor
// moved on or deleted since the scan is skipped (a second sweep is harmless);
// any other error stops the sweep. It returns how many sessions step handled.
func sweep(sessions []*checkoutsession.CheckoutSession, step func(s *checkoutsession.CheckoutSession) error) (int, error) {
	handled := 0
	for _, s := range sessions {
		err := step(s)
		switch {
		case err == nil:
			handled++
		case superseded(err), isNotFound(err):
		default:
			return handled, err
		}
	}
	return handled, nil
}

// ExpireOnQuoteExpiry is the policy ExpireOnQuoteExpiry (decision H1): an OPEN
// or DECLINED session expires with its price quote.
type ExpireOnQuoteExpiry struct{ Deps }

// Execute expires up to limit sessions and returns how many.
func (u ExpireOnQuoteExpiry) Execute(ctx context.Context, limit int) (int, error) {
	list, err := u.Sessions.ListExpirable(ctx, u.now(), limit)
	if err != nil {
		return 0, err
	}
	return sweep(list, func(s *checkoutsession.CheckoutSession) error {
		_, err := u.mutate(ctx, s.ID(), func(s *checkoutsession.CheckoutSession) error { return s.ExpireSession(u.now()) })
		return err
	})
}

// ResolveStuckAuthorizations is the policy ResolveStuckAuthorizations
// (decision H4): an AUTHORIZING session older than 2 minutes re-calls
// AuthorizePayment with the SAME key, which yields the true outcome, and the
// saga carries on from there.
type ResolveStuckAuthorizations struct{ Deps }

// Execute resolves up to limit sessions and returns how many it handled.
func (u ResolveStuckAuthorizations) Execute(ctx context.Context, limit int) (int, error) {
	list, err := u.Sessions.ListStuckAuthorizing(ctx, u.now().Add(-checkoutsession.StuckAuthorizationAfter), limit)
	if err != nil {
		return 0, err
	}
	return sweep(list, func(s *checkoutsession.CheckoutSession) error {
		_, err := u.authorize(ctx, s)
		return err
	})
}

// RetryOwedVoids is the policy RetryOwedVoids (decision H5): every minute until
// the void of each owed authorization succeeds.
type RetryOwedVoids struct{ Deps }

// Execute retries up to limit owed voids and returns how many succeeded.
func (u RetryOwedVoids) Execute(ctx context.Context, limit int) (int, error) {
	list, err := u.Sessions.ListVoidOwed(ctx, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, s := range list {
		if after := u.settleVoid(ctx, s.ID()); after != nil && after.VoidOwed() == nil {
			done++
		}
	}
	return done, nil
}

// PurgeStoredPlacements is the purge of decision H9: the stored placement
// request is dropped 24 h after a closed state. It raises no event.
type PurgeStoredPlacements struct{ Deps }

// Execute purges up to limit sessions and returns how many.
func (u PurgeStoredPlacements) Execute(ctx context.Context, limit int) (int, error) {
	list, err := u.Sessions.ListPurgeable(ctx, u.now().Add(-checkoutsession.PurgePlacementAfter), limit)
	if err != nil {
		return 0, err
	}
	return sweep(list, func(s *checkoutsession.CheckoutSession) error {
		_, err := u.mutate(ctx, s.ID(), func(s *checkoutsession.CheckoutSession) error { return s.PurgePlacement(u.now()) })
		return err
	})
}

package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/views"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// ConfirmSession is the command ConfirmSession
// (POST /checkout-sessions/{sessionId}:confirm).
type ConfirmSession struct{ Deps }

// Execute moves OPEN or DECLINED to AUTHORIZING (saved before the call out),
// authorizes the payment and drives the saga on: place the order, then settle
// the outcome. The returned session is in a definitive state, or still
// AUTHORIZING / PLACING when an outcome is unknown (the HTTP adapter answers
// 202). ErrAuthorizationMismatch is returned after the mismatching
// authorization was marked for voiding.
func (u ConfirmSession) Execute(ctx context.Context, sessionID, token string) (views.Session, error) {
	s, err := u.mutate(ctx, checkoutsession.ID(sessionID), func(s *checkoutsession.CheckoutSession) error {
		return s.Confirm(token, u.now())
	})
	if err != nil {
		return views.Session{}, err
	}
	return u.authorize(ctx, s)
}

// authorize calls AuthorizePayment for an AUTHORIZING session with the key
// <sessionId>:auth:<attempt> (decision H2) and drives the saga on. It is used
// by ConfirmSession and by ResolveStuckAuthorizations (decision H4), which
// re-calls it with the SAME key and token.
func (d Deps) authorize(ctx context.Context, s *checkoutsession.CheckoutSession) (views.Session, error) {
	key := fmt.Sprintf("%s:auth:%d", s.ID(), s.AuthAttempt())
	a, err := d.Payments.Authorize(ctx, key, authorizeRequest(s))
	if err != nil {
		d.warn(ctx, "authorization outcome unknown, the session stays AUTHORIZING", "sessionId", string(s.ID()), "error", err)
		return views.Of(s), nil
	}
	if !a.Authorized {
		return d.recordDecline(ctx, s.ID(), a.DeclineReason)
	}
	return d.recordAuthorization(ctx, s.ID(), a)
}

func authorizeRequest(s *checkoutsession.CheckoutSession) exchange.AuthorizeRequest {
	groups := s.Groups()
	allocs := make([]exchange.Allocation, len(groups))
	for i, g := range groups {
		allocs[i] = exchange.Allocation{GroupNo: g.GroupNo, AmountMinor: g.AmountMinor}
	}
	return exchange.AuthorizeRequest{
		SessionID: string(s.ID()), Token: s.PendingToken(), Currency: s.Currency(),
		TotalMinor: s.TotalMinor(), Allocations: allocs,
	}
}

func (d Deps) recordDecline(ctx context.Context, id checkoutsession.ID, reason string) (views.Session, error) {
	if reason == "" {
		reason = "declined"
	}
	s, err := d.mutate(ctx, id, func(s *checkoutsession.CheckoutSession) error { return s.RecordDecline(reason, d.now()) })
	if superseded(err) {
		return d.current(ctx, id)
	}
	if err != nil {
		return views.Session{}, err
	}
	return views.Of(s), nil
}

// recordAuthorization stores the placement request with the authorization and
// places the order. A mismatch is saved (DECLINED with the void owed) before
// the error is returned.
func (d Deps) recordAuthorization(ctx context.Context, id checkoutsession.ID, a exchange.Authorization) (views.Session, error) {
	mismatch := false
	s, err := d.mutate(ctx, id, func(s *checkoutsession.CheckoutSession) error {
		body, err := d.Placer.BuildRequest(draftOf(s))
		if err != nil {
			return err
		}
		mismatch = false
		err = s.RecordAuthorization(a.ID, a.TotalMinor, a.Currency, body, d.now())
		if errors.Is(err, checkoutsession.ErrAuthorizationMismatch) {
			mismatch = true
			return nil
		}
		return err
	})
	if superseded(err) {
		return d.current(ctx, id)
	}
	if err != nil {
		return views.Session{}, err
	}
	if mismatch {
		d.alert(ctx, "authorization-mismatch", string(id), fmt.Sprintf("authorized %d %s, quote total %d", a.TotalMinor, a.Currency, s.TotalMinor()))
		d.settleVoid(ctx, id)
		return views.Of(s), checkoutsession.ErrAuthorizationMismatch
	}
	return d.place(ctx, s)
}

func draftOf(s *checkoutsession.CheckoutSession) exchange.PlacementDraft {
	return exchange.PlacementDraft{ShopperID: s.ShopperID(), ShipToAddressID: s.ShipToAddressID(), Lines: s.Lines(), Groups: s.Groups()}
}

// current re-reads the session and returns it as it is.
func (d Deps) current(ctx context.Context, id checkoutsession.ID) (views.Session, error) {
	s, err := d.Sessions.Get(ctx, id)
	if err != nil {
		return views.Session{}, err
	}
	return views.Of(s), nil
}

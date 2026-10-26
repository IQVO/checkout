package memory

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

var _ ports.SessionRepository = (*SessionRepository)(nil)

// SessionRepository is the in-memory ports.SessionRepository.
type SessionRepository struct{ s *Store }

// Get implements ports.SessionRepository.
func (r *SessionRepository) Get(ctx context.Context, id checkoutsession.ID) (*checkoutsession.CheckoutSession, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("checkout session %s: %w", id, ports.ErrNotFound)
	}
	return checkoutsession.Restore(snap)
}

// Save implements ports.SessionRepository.
func (r *SessionRepository) Save(ctx context.Context, c *checkoutsession.CheckoutSession) error {
	defer r.s.enter(ctx)()
	stored, exists := r.s.sessions[c.ID()]
	if c.LoadedVersion() == 0 && exists || c.LoadedVersion() > 0 && (!exists || stored.Version != c.LoadedVersion()) {
		return fmt.Errorf("checkout session %s loaded at version %d: %w", c.ID(), c.LoadedVersion(), ports.ErrConcurrentUpdate)
	}
	r.s.sessions[c.ID()] = c.Snapshot()
	c.MarkPersisted()
	return nil
}

// scan restores the stored sessions the predicate keeps, ordered by the key
// (then id) and cut at limit.
func (r *SessionRepository) scan(ctx context.Context, limit int, keep func(checkoutsession.Snapshot) bool, key func(checkoutsession.Snapshot) time.Time) ([]*checkoutsession.CheckoutSession, error) {
	defer r.s.enter(ctx)()
	var snaps []checkoutsession.Snapshot
	for _, sn := range r.s.sessions {
		if keep(sn) {
			snaps = append(snaps, sn)
		}
	}
	slices.SortFunc(snaps, func(a, b checkoutsession.Snapshot) int {
		if c := key(a).Compare(key(b)); c != 0 {
			return c
		}
		return compareIDs(a.ID, b.ID)
	})
	if limit > 0 && len(snaps) > limit {
		snaps = snaps[:limit]
	}
	out := make([]*checkoutsession.CheckoutSession, 0, len(snaps))
	for _, sn := range snaps {
		c, err := checkoutsession.Restore(sn)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func compareIDs(a, b checkoutsession.ID) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ListStuckAuthorizing implements ports.SessionRepository.
func (r *SessionRepository) ListStuckAuthorizing(ctx context.Context, cutoff time.Time, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.scan(ctx, limit, func(s checkoutsession.Snapshot) bool {
		return s.State == checkoutsession.StateAuthorizing && !s.AuthorizingSince.After(cutoff)
	}, func(s checkoutsession.Snapshot) time.Time { return s.AuthorizingSince })
}

// ListPlacingDue implements ports.SessionRepository.
func (r *SessionRepository) ListPlacingDue(ctx context.Context, now time.Time, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.scan(ctx, limit, func(s checkoutsession.Snapshot) bool {
		return s.State == checkoutsession.StatePlacing && !s.NextAttemptAt.After(now)
	}, func(s checkoutsession.Snapshot) time.Time { return s.Placement.PlacingSince })
}

// ListExpirable implements ports.SessionRepository.
func (r *SessionRepository) ListExpirable(ctx context.Context, now time.Time, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.scan(ctx, limit, func(s checkoutsession.Snapshot) bool {
		open := s.State == checkoutsession.StateOpen || s.State == checkoutsession.StateDeclined
		return open && !s.QuoteExpiresAt.After(now)
	}, func(s checkoutsession.Snapshot) time.Time { return s.QuoteExpiresAt })
}

// ListVoidOwed implements ports.SessionRepository.
func (r *SessionRepository) ListVoidOwed(ctx context.Context, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.scan(ctx, limit, func(s checkoutsession.Snapshot) bool { return s.VoidOwed != nil },
		func(s checkoutsession.Snapshot) time.Time { return s.UpdatedAt })
}

// ListPurgeable implements ports.SessionRepository.
func (r *SessionRepository) ListPurgeable(ctx context.Context, cutoff time.Time, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.scan(ctx, limit, func(s checkoutsession.Snapshot) bool {
		return s.State.Closed() && s.Placement != nil && !s.TerminalAt.After(cutoff)
	}, func(s checkoutsession.Snapshot) time.Time { return s.TerminalAt })
}

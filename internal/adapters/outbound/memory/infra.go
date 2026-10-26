package memory

import (
	"context"

	"github.com/IQVO/checkout/internal/application/idempotency"
	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/domain/shared"
)

var (
	_ ports.EventOutbox      = (*Outbox)(nil)
	_ ports.UnitOfWork       = (*UnitOfWork)(nil)
	_ ports.IdempotencyStore = (*IdempotencyStore)(nil)
)

// UnitOfWork is the in-memory ports.UnitOfWork: serialised, atomic, re-entrant.
type UnitOfWork struct{ s *Store }

// Do runs fn holding the store. When fn returns an error or panics the state
// taken before fn is restored; the error is returned unchanged. A Do inside a
// Do joins it.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if ctx.Value(txKey{u.s}) != nil {
		return fn(ctx)
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	before := u.s.capture()
	committed := false
	defer func() {
		if !committed {
			u.s.restore(before)
		}
	}()
	err = fn(context.WithValue(ctx, txKey{u.s}, struct{}{}))
	committed = err == nil
	return err
}

// Outbox is the in-memory ports.EventOutbox.
type Outbox struct{ s *Store }

// Append stores the events in order.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.s.enter(ctx)()
	o.s.outbox = append(o.s.outbox, events...)
	return nil
}

// Events returns the appended events in order (a copy of the slice).
func (o *Outbox) Events() []shared.Event {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return append([]shared.Event(nil), o.s.outbox...)
}

// IdempotencyStore is the in-memory ports.IdempotencyStore. Records older than
// idempotency.TTL are treated as absent.
type IdempotencyStore struct{ s *Store }

// Get implements ports.IdempotencyStore.
func (i *IdempotencyStore) Get(ctx context.Context, route, key string) (idempotency.Record, error) {
	defer i.s.enter(ctx)()
	rec, ok := i.s.idempotency[idemKey{route, key}]
	if !ok || i.s.now().Sub(rec.CreatedAt) > idempotency.TTL {
		return idempotency.Record{}, ports.ErrNotFound
	}
	return rec, nil
}

// Put implements ports.IdempotencyStore.
func (i *IdempotencyStore) Put(ctx context.Context, route, key string, rec idempotency.Record) (bool, error) {
	defer i.s.enter(ctx)()
	k := idemKey{route, key}
	if old, ok := i.s.idempotency[k]; ok && i.s.now().Sub(old.CreatedAt) <= idempotency.TTL {
		return false, nil
	}
	i.s.idempotency[k] = rec
	return true, nil
}

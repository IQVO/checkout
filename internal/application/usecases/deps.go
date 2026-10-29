// Package usecases holds the application's use cases: one per operation of
// apis/openapi.yaml and one per policy. Every state change follows one shape
// inside ONE ports.UnitOfWork: load the aggregate, call the domain, save it and
// append the raised events to the outbox. An outbound call to a sibling is
// NEVER inside a unit of work: the saga saves the state it is about to act
// from, calls out, then saves the outcome. Domain, shared and port errors pass
// through unchanged (errors.Is keeps working): the HTTP adapter maps them to
// problem types.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// Deps bundles the ports the use cases need.
type Deps struct {
	Sessions ports.SessionRepository
	Carts    ports.CartReader
	Offers   ports.OfferReader
	Quoter   ports.PriceQuoter
	Payments ports.PaymentGateway
	Placer   ports.OrderPlacer
	Outbox   ports.EventOutbox
	UoW      ports.UnitOfWork
	Clock    ports.Clock
	IDs      ports.IDGenerator
	Alerter  ports.Alerter
	// Log receives anomalies that are acknowledged, not failed. Nil discards them.
	Log *slog.Logger
	// Metrics records the business outcome of each confirm attempt; nil records
	// nothing, so unit tests need no wiring.
	Metrics ports.SessionMetrics
}

// commandAttempts is how often a command runs when another writer got in
// between its load and its save.
const commandAttempts = 2

func (d Deps) now() time.Time { return d.Clock.Now().UTC() }

// persist saves the session and appends the events it raised, in the unit of
// work of ctx.
func (d Deps) persist(ctx context.Context, s *checkoutsession.CheckoutSession) error {
	events := s.PullEvents()
	if err := d.Sessions.Save(ctx, s); err != nil {
		return err
	}
	return d.publish(ctx, events)
}

// metrics returns the configured SessionMetrics or a no-op.
func (d Deps) metrics() ports.SessionMetrics {
	if d.Metrics != nil {
		return d.Metrics
	}
	return noMetrics{}
}

// noMetrics is the SessionMetrics of a Deps without one.
type noMetrics struct{}

func (noMetrics) SessionConfirmed(context.Context, string, string) {}

func (d Deps) publish(ctx context.Context, events []shared.Event) error {
	if err := d.Outbox.Append(ctx, events...); err != nil {
		return fmt.Errorf("append events to outbox: %w", err)
	}
	return nil
}

// transact runs attempt in a unit of work. A ports.ErrConcurrentUpdate means
// another writer got in between the load and the save, so the whole attempt
// runs again on fresh state, up to attempts times, before the conflict is
// reported.
func transact[T any](ctx context.Context, uow ports.UnitOfWork, attempt func(ctx context.Context) (T, error)) (T, error) {
	var out T
	run := func(ctx context.Context) error {
		v, err := attempt(ctx)
		if err != nil {
			return err
		}
		out = v
		return nil
	}
	var err error
	for i := 0; i < commandAttempts; i++ {
		if err = uow.Do(ctx, run); !errors.Is(err, ports.ErrConcurrentUpdate) {
			break
		}
	}
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// mutate is the shape of every state change on one existing session: load it,
// let the domain change it, save it with the events it raised, return it.
func (d Deps) mutate(ctx context.Context, id checkoutsession.ID, change func(s *checkoutsession.CheckoutSession) error) (*checkoutsession.CheckoutSession, error) {
	return transact(ctx, d.UoW, func(ctx context.Context) (*checkoutsession.CheckoutSession, error) {
		s, err := d.Sessions.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := change(s); err != nil {
			return nil, err
		}
		if err := d.persist(ctx, s); err != nil {
			return nil, err
		}
		return s, nil
	})
}

func (d Deps) alert(ctx context.Context, code, sessionID, detail string) {
	if d.Alerter != nil {
		d.Alerter.Alert(ctx, code, sessionID, detail)
	}
	d.warn(ctx, "alert", "code", code, "sessionId", sessionID, "detail", detail)
}

func (d Deps) warn(ctx context.Context, msg string, args ...any) {
	if d.Log != nil {
		d.Log.WarnContext(ctx, msg, args...)
	}
}

func isNotFound(err error) bool { return errors.Is(err, ports.ErrNotFound) }

// superseded reports an error that means another actor already moved the
// session on (the resolver beat the request, or the reverse): the caller
// re-reads and answers with the session as it is.
func superseded(err error) bool {
	return errors.Is(err, checkoutsession.ErrSessionClosed) ||
		errors.Is(err, shared.ErrIllegalTransition) ||
		errors.Is(err, ports.ErrConcurrentUpdate)
}

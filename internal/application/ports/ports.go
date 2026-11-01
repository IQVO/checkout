// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// sentinels of errors.go, the data packages idempotency and the request/answer
// structs of gateways.go.
//
// Optimistic concurrency: an aggregate is saved under the rule "stored version
// == LoadedVersion()"; a create (LoadedVersion 0) expects that the id is not
// stored. Any mismatch, including an update of a row that no longer exists, is
// ErrConcurrentUpdate. After a successful Save the adapter calls
// MarkPersisted(). Inside a UnitOfWork every repository joins the transaction
// carried by the ctx the unit handed to its function. No outbound call to a
// sibling ever runs inside a unit of work.
package ports

import (
	"context"
	"time"

	"github.com/IQVO/checkout/internal/application/idempotency"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// SessionRepository persists CheckoutSession aggregates.
type SessionRepository interface {
	// Get returns the session, or ErrNotFound.
	Get(ctx context.Context, id checkoutsession.ID) (*checkoutsession.CheckoutSession, error)
	// Save persists the session snapshot under the concurrency rule above.
	Save(ctx context.Context, s *checkoutsession.CheckoutSession) error
	// ListStuckAuthorizing returns up to limit AUTHORIZING sessions whose
	// authorizingSince is at or before the cutoff, oldest first (H4).
	ListStuckAuthorizing(ctx context.Context, cutoff time.Time, limit int) ([]*checkoutsession.CheckoutSession, error)
	// ListPlacingDue returns up to limit PLACING sessions whose next attempt
	// is at or before now, oldest first (ResolveInDoubtPlacements).
	ListPlacingDue(ctx context.Context, now time.Time, limit int) ([]*checkoutsession.CheckoutSession, error)
	// ListExpirable returns up to limit OPEN or DECLINED sessions whose quote
	// expired at or before now (ExpireOnQuoteExpiry).
	ListExpirable(ctx context.Context, now time.Time, limit int) ([]*checkoutsession.CheckoutSession, error)
	// ListVoidOwed returns up to limit sessions with a void owed (H5).
	ListVoidOwed(ctx context.Context, limit int) ([]*checkoutsession.CheckoutSession, error)
	// ListPurgeable returns up to limit closed sessions that still hold a
	// stored placement and reached their closed state at or before the cutoff (H9).
	ListPurgeable(ctx context.Context, cutoff time.Time, limit int) ([]*checkoutsession.CheckoutSession, error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// UnitOfWork runs fn as ONE atomic unit: every repository and outbox call fn
// makes with the ctx it is handed commits together when fn returns nil, or
// rolls back together when fn returns an error (or panics). Do is re-entrant;
// the error fn returns is returned unchanged.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// IdempotencyStore remembers the first response per Idempotency-Key for 24
// hours. The key is global to the service, not scoped to a route: the record
// carries the request fingerprint (method + path + body hash), so the same key
// on another route or with another body is detected as a reuse.
type IdempotencyStore interface {
	// Get returns the stored record, or ErrNotFound (expired records are
	// treated as absent).
	Get(ctx context.Context, key string) (idempotency.Record, error)
	// Put stores the record unless a fresh one exists; it reports whether it
	// stored.
	Put(ctx context.Context, key string, rec idempotency.Record) (bool, error)
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints the ids of new aggregates.
type IDGenerator interface {
	// NewSessionID mints the id of a new checkout session.
	NewSessionID() string
}

// Alerter raises the operator alerts of the saga: an in-doubt placement at
// 20 h, a contract bug (place-order 422, a sibling answer that cannot form a
// session) and a give-up at 22 h.
type Alerter interface {
	Alert(ctx context.Context, code, sessionID, detail string)
}

// Outcomes of one ConfirmSession attempt, as SessionMetrics.SessionConfirmed
// receives them.
const (
	ConfirmCompleted       = "completed"
	ConfirmDeclined        = "declined"
	ConfirmRequoteRequired = "requote-required"
	ConfirmFailed          = "failed"
	ConfirmInDoubt         = "in-doubt"
	ConfirmRefused         = "refused"
)

// SessionMetrics records the business outcome of every ConfirmSession attempt
// (Tier 2 of the fleet metrics convention). The use case calls it once per
// attempt with the outcome the shopper was answered with and, for every outcome
// but completed and requote-required, a stable low-cardinality reason slug. The
// implementation lives in internal/adapters/outbound/telemetry; a nil
// Deps.Metrics records nothing.
type SessionMetrics interface {
	SessionConfirmed(ctx context.Context, outcome, reason string)
}

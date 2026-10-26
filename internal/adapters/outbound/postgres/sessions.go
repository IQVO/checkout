package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// SessionRepository is the Postgres ports.SessionRepository.
type SessionRepository struct{ pool *pgxpool.Pool }

var _ ports.SessionRepository = (*SessionRepository)(nil)

// NewSessionRepository constructs a SessionRepository over pool.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

const sessionColumns = `session_id, state, cart_id, cart_version, shopper_id, ship_to_address_id, lines, groups,
	price_quote_id, currency, total_minor, quote_expires_at, authorization_id, order_id, requoted_offers, failure_code,
	decline_reason, auth_attempt, pending_token, authorizing_since, placement_body, placement_hash, placing_since,
	placement_attempts, next_attempt_at, alerted_at, void_authorization_id, terminal_at, created_at, updated_at, version`

// Get implements ports.SessionRepository. Inside a unit of work the row is
// locked (FOR UPDATE), so concurrent writers of one session serialise.
func (r *SessionRepository) Get(ctx context.Context, id checkoutsession.ID) (*checkoutsession.CheckoutSession, error) {
	suffix := ""
	if inUnitOfWork(ctx) {
		suffix = " FOR UPDATE"
	}
	row := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+sessionColumns+` FROM checkout_sessions WHERE session_id = $1`+suffix, string(id))
	s, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("checkout session %s: %w", id, ports.ErrNotFound)
	}
	return s, err
}

func collectSessions(rows pgx.Rows) ([]*checkoutsession.CheckoutSession, error) {
	defer rows.Close()
	var out []*checkoutsession.CheckoutSession
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SessionRepository) list(ctx context.Context, what, where, order string, args ...any) ([]*checkoutsession.CheckoutSession, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+sessionColumns+` FROM checkout_sessions WHERE `+where+` ORDER BY `+order+`, session_id LIMIT $1`, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", what, err)
	}
	return collectSessions(rows)
}

// ListStuckAuthorizing implements ports.SessionRepository.
func (r *SessionRepository) ListStuckAuthorizing(ctx context.Context, cutoff time.Time, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.list(ctx, "stuck authorizations", `state = 'AUTHORIZING' AND authorizing_since <= $2`, "authorizing_since", limit, cutoff.UTC())
}

// ListPlacingDue implements ports.SessionRepository.
func (r *SessionRepository) ListPlacingDue(ctx context.Context, now time.Time, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.list(ctx, "due placements", `state = 'PLACING' AND (next_attempt_at IS NULL OR next_attempt_at <= $2)`, "placing_since", limit, now.UTC())
}

// ListExpirable implements ports.SessionRepository.
func (r *SessionRepository) ListExpirable(ctx context.Context, now time.Time, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.list(ctx, "expirable sessions", `state IN ('OPEN','DECLINED') AND quote_expires_at <= $2`, "quote_expires_at", limit, now.UTC())
}

// ListVoidOwed implements ports.SessionRepository.
func (r *SessionRepository) ListVoidOwed(ctx context.Context, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.list(ctx, "owed voids", `void_authorization_id IS NOT NULL`, "updated_at", limit)
}

// ListPurgeable implements ports.SessionRepository.
func (r *SessionRepository) ListPurgeable(ctx context.Context, cutoff time.Time, limit int) ([]*checkoutsession.CheckoutSession, error) {
	return r.list(ctx, "purgeable placements", `placement_body IS NOT NULL AND terminal_at IS NOT NULL AND terminal_at <= $2`, "terminal_at", limit, cutoff.UTC())
}

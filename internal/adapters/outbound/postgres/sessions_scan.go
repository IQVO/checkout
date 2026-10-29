package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// row is the nullable shape of one checkout_sessions row.
type row struct {
	id, state, cartID, shopper, address, quoteID, currency string
	cartVersion, total, version                            int64
	lines, groups, requoted                                []byte
	quoteExpires, created, updated                         time.Time
	authID, orderID, failure, decline, token               *string
	authSince, placingSince, nextAttempt, alerted          *time.Time
	terminal                                               *time.Time
	body                                                   []byte
	hash, voidAuth                                         *string
	attempt, tries                                         int
}

func scanSession(r pgx.Row) (*checkoutsession.CheckoutSession, error) {
	var x row
	if err := r.Scan(&x.id, &x.state, &x.cartID, &x.cartVersion, &x.shopper, &x.address, &x.lines, &x.groups,
		&x.quoteID, &x.currency, &x.total, &x.quoteExpires, &x.authID, &x.orderID, &x.requoted, &x.failure,
		&x.decline, &x.attempt, &x.token, &x.authSince, &x.body, &x.hash, &x.placingSince,
		&x.tries, &x.nextAttempt, &x.alerted, &x.voidAuth, &x.terminal, &x.created, &x.updated, &x.version); err != nil {
		return nil, err
	}
	sn, err := x.snapshot()
	if err != nil {
		return nil, fmt.Errorf("decode checkout session %s: %w", x.id, err)
	}
	return checkoutsession.Restore(sn)
}

func (x row) snapshot() (checkoutsession.Snapshot, error) {
	lines, err := unmarshalLines(x.lines)
	if err != nil {
		return checkoutsession.Snapshot{}, err
	}
	groups, err := unmarshalGroups(x.groups)
	if err != nil {
		return checkoutsession.Snapshot{}, err
	}
	requoted, err := unmarshalRequoted(x.requoted)
	if err != nil {
		return checkoutsession.Snapshot{}, err
	}
	sn := checkoutsession.Snapshot{
		ID: checkoutsession.ID(x.id), State: checkoutsession.State(x.state), CartID: x.cartID, CartVersion: x.cartVersion,
		ShopperID: x.shopper, ShipToAddressID: x.address, Lines: lines, Groups: groups, PriceQuoteID: x.quoteID,
		Currency: x.currency, TotalMinor: x.total, QuoteExpiresAt: x.quoteExpires.UTC(), AuthorizationID: str(x.authID),
		OrderID: str(x.orderID), Requoted: requoted, FailureCode: str(x.failure), DeclineReason: str(x.decline),
		AuthAttempt: x.attempt, PendingToken: str(x.token), AuthorizingSince: when(x.authSince),
		PlacementAttempts: x.tries, NextAttemptAt: when(x.nextAttempt), AlertedAt: when(x.alerted),
		TerminalAt: when(x.terminal), CreatedAt: x.created.UTC(), UpdatedAt: x.updated.UTC(), Version: x.version,
	}
	if x.body != nil {
		sn.Placement = &checkoutsession.StoredPlacement{RequestBody: x.body, RequestHash: str(x.hash), PlacingSince: when(x.placingSince)}
	}
	if x.voidAuth != nil {
		sn.VoidOwed = &checkoutsession.VoidOwed{AuthorizationID: *x.voidAuth}
	}
	return sn, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func when(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.UTC()
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// Save implements ports.SessionRepository: a create (LoadedVersion 0) inserts
// and expects the id to be free; any other save updates the row at the loaded
// version. Zero affected rows is ErrConcurrentUpdate.
func (r *SessionRepository) Save(ctx context.Context, c *checkoutsession.CheckoutSession) error {
	sn := c.Snapshot()
	args, err := saveArgs(sn)
	if err != nil {
		return fmt.Errorf("encode checkout session %s: %w", sn.ID, err)
	}
	var affected int64
	err = inTx(ctx, r.pool, func(q querier) error {
		var err error
		var n interface{ RowsAffected() int64 }
		if c.LoadedVersion() == 0 {
			n, err = q.Exec(ctx, insertSession, args...)
		} else {
			n, err = q.Exec(ctx, updateSession, append(args, c.LoadedVersion())...)
		}
		if err == nil {
			affected = n.RowsAffected()
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("save checkout session %s: %w", sn.ID, err)
	}
	if affected != 1 {
		return fmt.Errorf("checkout session %s loaded at version %d: %w", sn.ID, c.LoadedVersion(), ports.ErrConcurrentUpdate)
	}
	c.MarkPersisted()
	return nil
}

const insertSession = `INSERT INTO checkout_sessions (` + sessionColumns + `) VALUES
	($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31)
	ON CONFLICT (session_id) DO NOTHING`

const updateSession = `UPDATE checkout_sessions SET state=$2, cart_id=$3, cart_version=$4, shopper_id=$5, ship_to_address_id=$6,
	lines=$7, groups=$8, price_quote_id=$9, currency=$10, total_minor=$11, quote_expires_at=$12, authorization_id=$13,
	order_id=$14, requoted_offers=$15, failure_code=$16, decline_reason=$17, auth_attempt=$18, pending_token=$19,
	authorizing_since=$20, placement_body=$21, placement_hash=$22, placing_since=$23, placement_attempts=$24,
	next_attempt_at=$25, alerted_at=$26, void_authorization_id=$27, terminal_at=$28, created_at=$29, updated_at=$30,
	version=$31 WHERE session_id=$1 AND version=$32`

func saveArgs(sn checkoutsession.Snapshot) ([]any, error) {
	lines, err := marshalLines(sn.Lines)
	if err != nil {
		return nil, err
	}
	groups, err := marshalGroups(sn.Groups)
	if err != nil {
		return nil, err
	}
	requoted, err := marshalRequoted(sn.Requoted)
	if err != nil {
		return nil, err
	}
	var body []byte
	var hash *string
	var placingSince *time.Time
	if p := sn.Placement; p != nil {
		body, hash, placingSince = p.RequestBody, &p.RequestHash, nullTime(p.PlacingSince)
	}
	var voidAuth *string
	if sn.VoidOwed != nil {
		voidAuth = &sn.VoidOwed.AuthorizationID
	}
	return []any{string(sn.ID), string(sn.State), sn.CartID, sn.CartVersion, sn.ShopperID, sn.ShipToAddressID, lines, groups,
		sn.PriceQuoteID, sn.Currency, sn.TotalMinor, sn.QuoteExpiresAt, nullString(sn.AuthorizationID), nullString(sn.OrderID),
		requoted, nullString(sn.FailureCode), nullString(sn.DeclineReason), sn.AuthAttempt, nullString(sn.PendingToken),
		nullTime(sn.AuthorizingSince), body, hash, placingSince, sn.PlacementAttempts, nullTime(sn.NextAttemptAt),
		nullTime(sn.AlertedAt), voidAuth, nullTime(sn.TerminalAt), sn.CreatedAt, sn.UpdatedAt, sn.Version}, nil
}

package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/application/views"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// refusalReasons maps an error that stopped a confirm before or after the saga
// to a stable reason slug, in match order.
var refusalReasons = []struct {
	err    error
	reason string
}{
	{checkoutsession.ErrAuthorizationMismatch, "authorization-mismatch"},
	{checkoutsession.ErrSessionClosed, "session-closed"},
	{checkoutsession.ErrPriceQuoteExpired, "price-quote-expired"},
	{ports.ErrNotFound, "not-found"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
	{shared.ErrIllegalTransition, "illegal-transition"},
	{shared.ErrValidation, "validation-failed"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// recordOutcome counts one confirm attempt by the outcome the shopper was
// answered with. An authorization mismatch is the one error that is also a
// session outcome (DECLINED with the void owed); it counts as declined.
func (u ConfirmSession) recordOutcome(ctx context.Context, v views.Session, err error) {
	outcome, reason := confirmOutcome(v, err)
	u.metrics().SessionConfirmed(ctx, outcome, reason)
}

func confirmOutcome(v views.Session, err error) (outcome, reason string) {
	if errors.Is(err, checkoutsession.ErrAuthorizationMismatch) {
		return ports.ConfirmDeclined, "authorization-mismatch"
	}
	if err != nil {
		return ports.ConfirmRefused, refusalReason(err)
	}
	switch v.State {
	case checkoutsession.StateCompleted:
		return ports.ConfirmCompleted, ""
	case checkoutsession.StateRequoteRequired:
		return ports.ConfirmRequoteRequired, ""
	case checkoutsession.StateDeclined:
		return ports.ConfirmDeclined, "payment-declined"
	case checkoutsession.StateFailed:
		return ports.ConfirmFailed, orReason(v.FailureCode, "failed")
	case checkoutsession.StatePlacing:
		return ports.ConfirmInDoubt, "placement-in-doubt"
	case checkoutsession.StateAuthorizing:
		return ports.ConfirmInDoubt, "authorization-unknown"
	}
	return ports.ConfirmRefused, "session-closed"
}

func refusalReason(err error) string {
	for _, r := range refusalReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}

func orReason(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

package usecases

import (
	"context"

	"github.com/IQVO/checkout/internal/application/views"
	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

// GetSession is the query GetSession (GET /checkout-sessions/{sessionId}). While
// a session is PLACING after an in-doubt placement it is the endpoint the BFF
// polls until a definitive outcome.
type GetSession struct{ Deps }

// Execute returns the session or ports.ErrNotFound.
func (u GetSession) Execute(ctx context.Context, sessionID string) (views.Session, error) {
	return u.current(ctx, checkoutsession.ID(sessionID))
}

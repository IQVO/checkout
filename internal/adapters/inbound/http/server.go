// Package http is the REST inbound adapter: every route of apis/openapi.yaml on
// a chi router, RFC 9457 problem documents for every error and the liveness and
// readiness endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/application/usecases"
)

// Route names under which the Idempotency-Key records are stored.
const (
	routeOpen    = "POST /checkout-sessions"
	routeConfirm = "POST /checkout-sessions/{sessionId}:confirm"
)

// Server holds the use cases the handlers call.
type Server struct {
	OpenSession    usecases.OpenSession
	ConfirmSession usecases.ConfirmSession
	GetSession     usecases.GetSession

	// IdempotencyStore and Clock back the Idempotency-Key middleware of the
	// POST routes.
	IdempotencyStore ports.IdempotencyStore
	Clock            ports.Clock
	// Readiness backs GET /readyz; nil means always ready (unit tests).
	Readiness *Readiness
}

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml.
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	// A NUL (U+0000) in the path, the query or a body is a uniform client error,
	// never a Postgres 22021 -> 500; it runs before the Idempotency-Key
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.With(s.idempotent(routeOpen)).Post("/checkout-sessions", s.handleOpen)
	r.Get("/checkout-sessions/{sessionId}", s.handleGet)
	r.With(s.idempotent(routeConfirm)).Post("/checkout-sessions/{sessionId}:confirm", s.handleConfirm)
	return r
}

func (s *Server) idempotent(route string) func(http.Handler) http.Handler {
	return RequireIdempotencyKey(s.IdempotencyStore, s.Clock, route)
}

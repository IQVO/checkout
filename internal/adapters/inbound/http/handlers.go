package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/checkout/internal/application/ports"
	"github.com/IQVO/checkout/internal/application/usecases"
	"github.com/IQVO/checkout/internal/application/views"
	"github.com/IQVO/checkout/internal/domain/shared"
)

// maxIDLength bounds a path identifier; a longer one cannot name a resource.
const maxIDLength = 128

// pathID returns the named path parameter, or false after writing 404 for one
// that can never name a resource.
func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := chi.URLParam(r, name)
	if id == "" || len(id) > maxIDLength {
		writeError(w, r, ports.ErrNotFound)
		return "", false
	}
	return id, true
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// handleOpen serves POST /checkout-sessions (OpenSession).
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req openRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.CartID == "" || req.ShopperID == "" || req.ShipToAddressID == "" {
		writeError(w, r, shared.Invalid("cartId, shopperId and shipToAddressId are required"))
		return
	}
	v, err := s.OpenSession.Execute(r.Context(), usecases.OpenCommand{
		RequestKey: r.Header.Get(IdempotencyKeyHeader), CartID: req.CartID, ShopperID: req.ShopperID, ShipToAddressID: req.ShipToAddressID,
	})
	respond(w, r, http.StatusCreated, v, err)
}

// handleGet serves GET /checkout-sessions/{sessionId} (GetSession).
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "sessionId")
	if !ok {
		return
	}
	v, err := s.GetSession.Execute(r.Context(), id)
	respond(w, r, http.StatusOK, v, err)
}

// handleConfirm serves POST /checkout-sessions/{sessionId}:confirm
// (ConfirmSession). 200 when the session reached a definitive state, 202 while
// the authorization or the place-order outcome is unknown.
func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "sessionId")
	if !ok {
		return
	}
	var req confirmRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.ConfirmSession.Execute(r.Context(), id, req.PaymentMethodToken)
	status := http.StatusOK
	if err == nil && v.Unsettled() {
		status = http.StatusAccepted
	}
	respond(w, r, status, v, err)
}

// respond writes the session view as status, or the error as its problem.
func respond(w http.ResponseWriter, r *http.Request, status int, v views.Session, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, status, sessionBody(v))
}

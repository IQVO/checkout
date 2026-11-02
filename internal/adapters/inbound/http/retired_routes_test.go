package http_test

import (
	"net/http"
	"testing"
)

// TestRetiredRoutesAreGone pins REST guideline R1: the colon custom method is
// retired with no alias, so the old route no longer reaches a handler.
func TestRetiredRoutesAreGone(t *testing.T) {
	e := newEnv(t)
	e.open()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/checkout-sessions/sess-1:confirm"},
		{http.MethodGet, "/checkout-sessions/sess-1:confirm"},
	} {
		r := e.do(tc.method, tc.path, "retired-"+tc.method, confirmBody)
		if r.Status != http.StatusNotFound && r.Status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 404 or 405", tc.method, tc.path, r.Status)
		}
	}
	if got := e.get("/checkout-sessions/sess-1").json(t)["state"]; got != "OPEN" {
		t.Fatalf("the retired route must not confirm the session, state = %v", got)
	}
}

// TestNewRoutesAnswer proves the noun routes replace the retired ones.
func TestNewRoutesAnswer(t *testing.T) {
	e := newEnv(t)
	e.open()
	e.wantStatus(e.post("/checkout-sessions/sess-1/confirmations", confirmBody), http.StatusOK)
	if r := e.get("/checkout-sessions/sess-1/confirmations"); r.Status != http.StatusNotFound && r.Status != http.StatusMethodNotAllowed {
		t.Fatalf("GET on the transition resource = %d, want 404 or 405", r.Status)
	}
}

package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestIdempotencyKeyRequiredAndReplay(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions", "", `{}`), http.StatusBadRequest, "idempotency-key-required")
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions", strings.Repeat("k", 129), `{}`), http.StatusBadRequest, "idempotency-key-required")

	first := e.do(http.MethodPost, "/checkout-sessions", "k1", `{"cartId":"cart-1","shopperId":"s1","shipToAddressId":"a1"}`)
	e.wantStatus(first, http.StatusCreated)
	again := e.do(http.MethodPost, "/checkout-sessions", "k1", `{"cartId":"cart-1","shopperId":"s1","shipToAddressId":"a1"}`)
	e.wantStatus(again, http.StatusCreated)
	if again.Body != first.Body || again.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay differs: %q vs %q (%v)", again.Body, first.Body, again.Header)
	}
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions", "k1", `{"cartId":"cart-1","shopperId":"s2","shipToAddressId":"a1"}`), http.StatusUnprocessableEntity, "idempotency-key-reused")
	if n := e.store.Counts().Sessions; n != 1 {
		t.Fatalf("replay created again: %d sessions", n)
	}
}

func TestIdempotencyRecordsClientErrorsNotServerErrors(t *testing.T) {
	e := newEnv(t)
	bad := e.do(http.MethodPost, "/checkout-sessions", "k2", `{"cartId":"","shopperId":"s","shipToAddressId":"a"}`)
	e.wantStatus(bad, http.StatusUnprocessableEntity)
	again := e.do(http.MethodPost, "/checkout-sessions", "k2", `{"cartId":"","shopperId":"s","shipToAddressId":"a"}`)
	e.wantStatus(again, http.StatusUnprocessableEntity)
	if again.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("a client error is stored under its key")
	}
}

func TestOversizedBodyIsMalformed(t *testing.T) {
	e := newEnv(t)
	big := `{"cartId":"` + strings.Repeat("a", 70<<10) + `"}`
	e.wantProblem(e.post("/checkout-sessions", big), http.StatusBadRequest, "malformed-request")
}

func TestRejectNUL(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.get("/checkout-sessions/a%00b"), http.StatusNotFound, "resource-not-found")
	e.wantProblem(e.get("/checkout-sessions/x?y=a%00b"), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/checkout-sessions", `{"cartId":"a\u0000b","shopperId":"s","shipToAddressId":"a"}`), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/checkout-sessions", "{\"cartId\":\"a\x00b\"}"), http.StatusBadRequest, "malformed-request")
}

func TestHealthAndReadiness(t *testing.T) {
	e := newEnv(t)
	e.wantStatus(e.get("/healthz"), http.StatusOK)
	e.wantStatus(e.get("/readyz"), http.StatusOK)
	e.ready.SetNotReady()
	e.wantStatus(e.get("/readyz"), http.StatusServiceUnavailable)
	e.wantStatus(e.get("/healthz"), http.StatusOK)
}

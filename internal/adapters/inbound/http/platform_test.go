package http_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/IQVO/checkout/internal/application/ports"
)

func TestIdempotencyKeyRequiredInvalidAndReplay(t *testing.T) {
	e := newEnv(t)
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions", "", `{}`), http.StatusBadRequest, "idempotency-key-required")
	// A key that is not 1-128 printable ASCII is idempotency-key-invalid, never "required".
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions", strings.Repeat("k", 129), `{}`), http.StatusBadRequest, "idempotency-key-invalid")
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions", "caf\u00e9", `{}`), http.StatusBadRequest, "idempotency-key-invalid")
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions", "tab\there", `{}`), http.StatusBadRequest, "idempotency-key-invalid")
	if got := e.do(http.MethodPost, "/checkout-sessions", strings.Repeat("k", 128), openBody); got.Status != http.StatusCreated {
		t.Fatalf("a 128-character key is valid: %d %s", got.Status, got.Body)
	}

	first := e.do(http.MethodPost, "/checkout-sessions", "k1", openBody)
	e.wantStatus(first, http.StatusCreated)
	if first.Header.Get("Idempotency-Replayed") != "" {
		t.Fatal("the first execution never carries Idempotency-Replayed")
	}
	again := e.do(http.MethodPost, "/checkout-sessions", "k1", openBody)
	e.wantStatus(again, http.StatusCreated)
	if again.Body != first.Body || again.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay differs: %q vs %q (%v)", again.Body, first.Body, again.Header)
	}
	if again.Header.Get("Idempotent-Replayed") != "" {
		t.Fatal("the retired Idempotent-Replayed spelling must not be sent")
	}
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions", "k1", `{"cartId":"cart-1","shopperId":"s2","shipToAddressId":"a1"}`), http.StatusConflict, "idempotency-key-reused")
	if n := e.store.Counts().Sessions; n != 2 {
		t.Fatalf("replay created again: %d sessions", n)
	}
}

func TestIdempotencyKeyIsGlobalAcrossRoutes(t *testing.T) {
	e := newEnv(t)
	e.wantStatus(e.do(http.MethodPost, "/checkout-sessions", "shared", openBody), http.StatusCreated)
	// The same key on another route is a different fingerprint: 409, and the confirm never ran.
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions/sess-1/confirmations", "shared", confirmBody), http.StatusConflict, "idempotency-key-reused")
	if got := e.get("/checkout-sessions/sess-1").json(t)["state"]; got != "OPEN" {
		t.Fatalf("a rejected reuse must not run the command, state = %v", got)
	}
}

func TestIdempotencyReplaysTheConfirmAnswer(t *testing.T) {
	e := newEnv(t)
	e.open()
	first := e.do(http.MethodPost, "/checkout-sessions/sess-1/confirmations", "c1", confirmBody)
	e.wantStatus(first, http.StatusOK)
	again := e.do(http.MethodPost, "/checkout-sessions/sess-1/confirmations", "c1", confirmBody)
	e.wantStatus(again, http.StatusOK)
	if again.Body != first.Body || again.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("confirm replay differs: %q vs %q", again.Body, first.Body)
	}
	e.wantProblem(e.do(http.MethodPost, "/checkout-sessions/sess-1/confirmations", "c1", `{"paymentMethodToken":"tok_2"}`), http.StatusConflict, "idempotency-key-reused")
}

func TestIdempotencyRecordsClientErrorsNotServerErrors(t *testing.T) {
	e := newEnv(t)
	bad := e.do(http.MethodPost, "/checkout-sessions", "k2", `{"cartId":"","shopperId":"s","shipToAddressId":"a"}`)
	e.wantStatus(bad, http.StatusUnprocessableEntity)
	again := e.do(http.MethodPost, "/checkout-sessions", "k2", `{"cartId":"","shopperId":"s","shipToAddressId":"a"}`)
	e.wantStatus(again, http.StatusUnprocessableEntity)
	if again.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatal("a client error is stored under its key")
	}

	// A 5xx is never stored: the retry with the same key runs the command again.
	e.carts.Err = ports.ErrShoppingCartUnavailable
	e.wantStatus(e.do(http.MethodPost, "/checkout-sessions", "k3", openBody), http.StatusServiceUnavailable)
	e.carts.Err = nil
	retry := e.do(http.MethodPost, "/checkout-sessions", "k3", openBody)
	e.wantStatus(retry, http.StatusCreated)
	if retry.Header.Get("Idempotency-Replayed") != "" {
		t.Fatal("a 5xx must not be stored, so the retry is a first execution")
	}
}

func TestConcurrentSameKeyRequestsRunOnce(t *testing.T) {
	e := newEnv(t)
	const n = 12
	replies := make([]reply, n)
	var wg sync.WaitGroup
	for i := range replies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replies[i] = e.do(http.MethodPost, "/checkout-sessions", "burst", openBody)
		}()
	}
	wg.Wait()
	replayed := 0
	for _, r := range replies {
		e.wantStatus(r, http.StatusCreated)
		if r.Body != replies[0].Body {
			t.Fatalf("every concurrent request gets the same answer: %q vs %q", r.Body, replies[0].Body)
		}
		if r.Header.Get("Idempotency-Replayed") == "true" {
			replayed++
		}
	}
	if replayed != n-1 {
		t.Fatalf("%d of %d concurrent requests replayed, want %d", replayed, n, n-1)
	}
	if got := e.store.Counts().Sessions; got != 1 {
		t.Fatalf("%d sessions for one key, want exactly 1", got)
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

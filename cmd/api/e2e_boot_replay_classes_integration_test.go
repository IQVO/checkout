//go:build integration

package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/postgres"
	"github.com/IQVO/checkout/internal/testing/kafkatest"
	"github.com/IQVO/checkout/internal/testing/pgtest"
)

const replayedHeader = "Idempotency-Replayed"

// TestReplayClassesOverRealPostgres drives the replay classes of ADR 0011
// through the shipped binary: real router, real Postgres store, real use cases,
// the five siblings faked. A RETRYABLE refusal leaves no row under its key and
// the same key executes again; an OUTCOME refusal is stored with its state
// committed; a FINAL one is stored. Keep this file's name sorting before
// e2e_flow_*: TestRealWiringReadyzIs503WhileKafkaIsDown stops the shared broker.
func TestReplayClassesOverRealPostgres(t *testing.T) {
	sib := fakeSiblings(t)
	dsn := pgtest.URL(t)
	svc := boot(t, bootEnv(t, dsn, kafkatest.Brokers(t), sib))
	pool, err := postgres.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	keyRows := func(key string) int { return count(`SELECT count(*) FROM idempotency_keys WHERE key = $1`, key) }
	sessions := func() int { return count(`SELECT count(*) FROM checkout_sessions`) }
	problem := func(t *testing.T, r reply, status int, slug string) {
		t.Helper()
		if r.status != status || r.json(t)["type"] != "/problems/"+slug {
			t.Fatalf("answer = %d %s, want %d /problems/%s", r.status, r.body, status, slug)
		}
	}

	t.Run("RETRYABLE cart-not-checkout-ready: no row under the key, the retry executes, is stored and replayed", func(t *testing.T) {
		before := sessions()
		sib.setCart(cartNotReady)
		first := svc.call(t, "POST", "/checkout-sessions", "k-notready", openBody)
		problem(t, first, http.StatusConflict, "cart-not-checkout-ready")
		if n := keyRows("k-notready"); n != 0 {
			t.Fatalf("a RETRYABLE refusal left %d idempotency rows: the key is not released", n)
		}
		sib.setCart(cartReady)
		retry := svc.call(t, "POST", "/checkout-sessions", "k-notready", openBody)
		if retry.status != http.StatusCreated || retry.header.Get(replayedHeader) != "" {
			t.Fatalf("retry = %d %s (%v), want a fresh 201 executed under the same key", retry.status, retry.body, retry.header)
		}
		if n := keyRows("k-notready"); n != 1 {
			t.Fatalf("%d rows after the success, want 1 (a 2xx is stored)", n)
		}
		again := svc.call(t, "POST", "/checkout-sessions", "k-notready", openBody)
		if again.status != http.StatusCreated || again.body != retry.body || again.header.Get(replayedHeader) != "true" {
			t.Fatalf("third call = %d %s (%v), want the stored 201 replayed", again.status, again.body, again.header)
		}
		if got := sessions(); got != before+1 {
			t.Fatalf("%d sessions, want %d", got, before+1)
		}
	})

	t.Run("RETRYABLE validation-failed for an unknown cart: released key, another body is a new request", func(t *testing.T) {
		sib.setCart(cartUnknown)
		first := svc.call(t, "POST", "/checkout-sessions", "k-nocart", openBody)
		problem(t, first, http.StatusUnprocessableEntity, "validation-failed")
		if n := keyRows("k-nocart"); n != 0 {
			t.Fatalf("a RETRYABLE refusal left %d idempotency rows", n)
		}
		other := `{"cartId":"cart-1","shopperId":"shopper-2","shipToAddressId":"addr-1"}`
		second := svc.call(t, "POST", "/checkout-sessions", "k-nocart", other)
		problem(t, second, http.StatusUnprocessableEntity, "validation-failed")
		sib.setCart(cartReady)
		retry := svc.call(t, "POST", "/checkout-sessions", "k-nocart", other)
		if retry.status != http.StatusCreated || retry.header.Get(replayedHeader) != "" {
			t.Fatalf("retry = %d %s (%v), want a fresh 201 once the cart is known", retry.status, retry.body, retry.header)
		}
	})

	t.Run("FINAL malformed-request is stored and replayed, another body is idempotency-key-reused", func(t *testing.T) {
		first := svc.call(t, "POST", "/checkout-sessions", "k-final", `{"unknown":1}`)
		problem(t, first, http.StatusBadRequest, "malformed-request")
		if n := keyRows("k-final"); n != 1 {
			t.Fatalf("%d rows after a FINAL refusal, want 1", n)
		}
		again := svc.call(t, "POST", "/checkout-sessions", "k-final", `{"unknown":1}`)
		if again.status != first.status || again.body != first.body || again.header.Get(replayedHeader) != "true" {
			t.Fatalf("replay = %d %s (%v), want the stored 400", again.status, again.body, again.header)
		}
		problem(t, svc.call(t, "POST", "/checkout-sessions", "k-final", openBody), http.StatusConflict, "idempotency-key-reused")
	})

	t.Run("OUTCOME authorization-mismatch: the declined session and the void are committed, the answer is stored and replayed", func(t *testing.T) {
		opened := svc.call(t, "POST", "/checkout-sessions", "k-open-mismatch", openBody)
		id, _ := opened.json(t)["sessionId"].(string)
		sib.setAuthDelta(1)
		defer sib.setAuthDelta(0)
		first := svc.call(t, "POST", "/checkout-sessions/"+id+"/confirmations", "k-mismatch", confirmBody)
		problem(t, first, http.StatusConflict, "authorization-mismatch")
		var state string
		if err := pool.QueryRow(context.Background(), `SELECT state FROM checkout_sessions WHERE session_id = $1`, id).Scan(&state); err != nil || state != "DECLINED" {
			t.Fatalf("session state after the refusal = %q (%v), want DECLINED: the refusal committed it", state, err)
		}
		if n := count(`SELECT count(*) FROM outbox_events WHERE subject = $1 AND event_type LIKE '%Declined'`, id); n != 1 {
			t.Fatalf("%d CheckoutDeclined events, want 1", n)
		}
		if n := keyRows("k-mismatch"); n != 1 {
			t.Fatalf("%d rows for the OUTCOME refusal, want 1 (stored)", n)
		}
		authorizations := sib.called("POST /payment-authorizations")
		replay := svc.call(t, "POST", "/checkout-sessions/"+id+"/confirmations", "k-mismatch", confirmBody)
		if replay.status != http.StatusConflict || replay.body != first.body || replay.header.Get(replayedHeader) != "true" {
			t.Fatalf("replay = %d %s (%v), want the stored 409", replay.status, replay.body, replay.header)
		}
		if got := sib.called("POST /payment-authorizations"); got != authorizations {
			t.Fatalf("a replay called customer-payments (%d calls, was %d)", got, authorizations)
		}
		if n := count(`SELECT count(*) FROM outbox_events WHERE subject = $1 AND event_type LIKE '%Declined'`, id); n != 1 {
			t.Fatalf("%d CheckoutDeclined events after the replay, want 1: a replay records nothing new", n)
		}
	})
}

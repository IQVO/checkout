//go:build integration

package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/postgres"
	"github.com/IQVO/checkout/internal/testing/kafkatest"
	"github.com/IQVO/checkout/internal/testing/pgtest"
)

// TestIdempotencyOverRealPostgres drives the Idempotency-Key contract (REST API
// guideline section 2) through the shipped binary: real router, real Postgres
// store, no skip gate. Keep this file's name sorting before e2e_flow_*: go test
// runs files in name order and TestRealWiringReadyzIs503WhileKafkaIsDown stops
// the shared Kafka broker for every test after it.
func TestIdempotencyOverRealPostgres(t *testing.T) {
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
	sessions := func() int { return count(`SELECT count(*) FROM checkout_sessions`) }

	t.Run("a missing key is 400 idempotency-key-required and a malformed one 400 idempotency-key-invalid", func(t *testing.T) {
		r := svc.call(t, "POST", "/checkout-sessions", "", openBody)
		if r.status != http.StatusBadRequest || r.json(t)["type"] != "/problems/idempotency-key-required" {
			t.Fatalf("no key = %d %s", r.status, r.body)
		}
		r = svc.call(t, "POST", "/checkout-sessions", strings.Repeat("k", 129), openBody)
		if r.status != http.StatusBadRequest || r.json(t)["type"] != "/problems/idempotency-key-invalid" {
			t.Fatalf("129-char key = %d %s", r.status, r.body)
		}
		if n := sessions(); n != 0 {
			t.Fatalf("a refused request opened %d sessions", n)
		}
	})
	t.Run("a fresh key executes, a byte-identical retry replays with Idempotency-Replayed", func(t *testing.T) {
		first := svc.call(t, "POST", "/checkout-sessions", "k-replay", openBody)
		if first.status != http.StatusCreated || first.header.Get("Idempotency-Replayed") != "" {
			t.Fatalf("first = %d %s (%v)", first.status, first.body, first.header)
		}
		again := svc.call(t, "POST", "/checkout-sessions", "k-replay", openBody)
		if again.status != http.StatusCreated || again.body != first.body || again.header.Get("Idempotency-Replayed") != "true" {
			t.Fatalf("replay = %d %s (%v)", again.status, again.body, again.header)
		}
		if n := sessions(); n != 1 {
			t.Fatalf("%d sessions after a replay, want 1", n)
		}
	})
	t.Run("the key is global: another body or another route is 409 idempotency-key-reused", func(t *testing.T) {
		before := sessions()
		other := svc.call(t, "POST", "/checkout-sessions", "k-replay", `{"cartId":"cart-1","shopperId":"shopper-2","shipToAddressId":"addr-1"}`)
		if other.status != http.StatusConflict || other.json(t)["type"] != "/problems/idempotency-key-reused" {
			t.Fatalf("other body = %d %s", other.status, other.body)
		}
		id, _ := svc.call(t, "POST", "/checkout-sessions", "k-global", openBody).json(t)["sessionId"].(string)
		cross := svc.call(t, "POST", "/checkout-sessions/"+id+"/confirmations", "k-global", confirmBody)
		if cross.status != http.StatusConflict || cross.json(t)["type"] != "/problems/idempotency-key-reused" {
			t.Fatalf("other route = %d %s", cross.status, cross.body)
		}
		if got := svc.call(t, "GET", "/checkout-sessions/"+id, "", "").json(t)["state"]; got != "OPEN" {
			t.Fatalf("a rejected reuse confirmed the session: %v", got)
		}
		if n := sessions(); n != before+1 {
			t.Fatalf("%d sessions, want %d", n, before+1)
		}
	})
	t.Run("a 4xx answer is cached and replayed byte for byte", func(t *testing.T) {
		const body = `{"cartId":"ghost","shopperId":"shopper-1","shipToAddressId":"addr-1"}`
		first := svc.call(t, "POST", "/checkout-sessions", "k-422", body)
		again := svc.call(t, "POST", "/checkout-sessions", "k-422", body)
		if first.status != http.StatusUnprocessableEntity || again.status != first.status || again.body != first.body ||
			first.header.Get("Idempotency-Replayed") != "" || again.header.Get("Idempotency-Replayed") != "true" {
			t.Fatalf("first = %d %s (%v), again = %d %s (%v)", first.status, first.body, first.header, again.status, again.body, again.header)
		}
	})
	t.Run("a 5xx answer is not stored, so the retry runs again", func(t *testing.T) {
		sib.setCartDown(true)
		down := svc.call(t, "POST", "/checkout-sessions", "k-503", openBody)
		sib.setCartDown(false)
		if down.status != http.StatusServiceUnavailable {
			t.Fatalf("down = %d %s", down.status, down.body)
		}
		if n := count(`SELECT count(*) FROM idempotency_keys WHERE key = $1`, "k-503"); n != 0 {
			t.Fatalf("a 5xx stored %d idempotency rows", n)
		}
		retry := svc.call(t, "POST", "/checkout-sessions", "k-503", openBody)
		if retry.status != http.StatusCreated || retry.header.Get("Idempotency-Replayed") != "" {
			t.Fatalf("retry = %d %s (%v)", retry.status, retry.body, retry.header)
		}
	})
	t.Run("concurrent requests with one key open exactly one session", func(t *testing.T) { concurrentSameKey(t, svc, sessions) })
}

// concurrentSameKey sends n identical keyed opens at once: exactly one runs, the
// others replay its answer.
func concurrentSameKey(t *testing.T, svc *service, sessions func() int) {
	t.Helper()
	const n = 16
	before := sessions()
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		run int
		ids = map[string]bool{}
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := svc.call(t, "POST", "/checkout-sessions", "k-race", openBody)
			mu.Lock()
			defer mu.Unlock()
			if r.status != http.StatusCreated {
				t.Errorf("concurrent open = %d %s", r.status, r.body)
				return
			}
			if r.header.Get("Idempotency-Replayed") == "" {
				run++
			}
			ids[r.json(t)["sessionId"].(string)] = true
		}()
	}
	wg.Wait()
	if run != 1 || len(ids) != 1 {
		t.Fatalf("%d executions and %d distinct sessions from %d identical requests, want 1 and 1", run, len(ids), n)
	}
	if got := sessions(); got != before+1 {
		t.Fatalf("%d sessions stored, want %d", got, before+1)
	}
}

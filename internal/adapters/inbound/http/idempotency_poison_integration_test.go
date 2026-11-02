//go:build integration

package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	httpadapter "github.com/IQVO/checkout/internal/adapters/inbound/http"
	"github.com/IQVO/checkout/internal/adapters/outbound/postgres"
	"github.com/IQVO/checkout/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/checkout/internal/testing/pgtest"
)

// The tcpostgres reference keeps the testcontainers postgres module import
// reachable in this package's directory (required by internal/architecture's
// TestPostgresIntegrationTestsUseTestcontainers for any integration test
// package that talks to pgx).
var _ = tcpostgres.Run

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// This service's Idempotency-Key middleware does not share a transaction with
// the handler: the use case's UnitOfWork owns one (and commits or rolls it
// back before the handler answers), and IdempotencyStore.Put records the
// outcome on a FRESH transaction. The 25P02 "current transaction is aborted"
// failure that other services needed a SAVEPOINT for therefore cannot occur
// here. These tests pin the invariant through the public middleware so a
// future change that joins the two transactions fails loudly:
//
//	a mapped 4xx over an aborted transaction is still recorded and returned;
//	a 2xx over an aborted transaction is never returned or cached.

const insertOutbox = `INSERT INTO outbox_events (event_id, topic, event_type, subject, key, dataschema, value)
	VALUES ('dup', 't', 'e', 's', ''::bytea, 'd', ''::bytea)`

// poison runs a unit of work whose second statement fails on the server (a
// 23505 unique violation), aborting the transaction it ran on. With swallow
// the handler hides the failure from the unit of work and tries to commit.
func poison(r *http.Request, pool *pgxpool.Pool, swallow bool) error {
	return postgres.NewUnitOfWork(pool).Do(r.Context(), func(ctx context.Context) error {
		tx, ok := pgtx.From(ctx)
		if !ok {
			return errors.New("the unit of work did not bind a transaction")
		}
		for i := 0; i < 2; i++ {
			if _, err := tx.Exec(ctx, insertOutbox); err != nil {
				if swallow {
					return nil // the transaction is aborted but the handler goes on
				}
				return err
			}
		}
		return errors.New("the duplicate insert did not abort the transaction")
	})
}

func keyedRequest(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/checkout-sessions/s1:confirm", strings.NewReader(`{}`))
	req.Header.Set(httpadapter.IdempotencyKeyHeader, key)
	return req
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestIdempotency_A409OverAnAbortedTransactionIsRecordedAndReplayed: a lost
// race on a unique index answers 409, not 500, the 409 is stored, and a retry
// replays it without re-running the handler.
func TestIdempotency_A409OverAnAbortedTransactionIsRecordedAndReplayed(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := poison(r, pool, false); err == nil {
			t.Error("the poisoned unit of work must fail")
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"/problems/session-closed","title":"Conflict","status":409}`))
	})
	mw := httpadapter.RequireIdempotencyKey(postgres.NewIdempotencyStore(pool), wallClock{})(handler)

	first := httptest.NewRecorder()
	mw.ServeHTTP(first, keyedRequest("k-poison"))
	if first.Code != http.StatusConflict {
		t.Fatalf("first = %d %s, want the truthful 409", first.Code, first.Body.String())
	}
	var status int
	err := pool.QueryRow(context.Background(), `SELECT status FROM idempotency_keys WHERE key = 'k-poison'`).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || err != nil || status != http.StatusConflict {
		t.Fatalf("the 409 outcome was not stored: status=%d err=%v", status, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM outbox_events`); n != 0 {
		t.Fatalf("the aborted unit of work left %d outbox rows", n)
	}

	replay := httptest.NewRecorder()
	mw.ServeHTTP(replay, keyedRequest("k-poison"))
	if replay.Code != http.StatusConflict || replay.Header().Get(httpadapter.ReplayedHeader) != "true" || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d replayed=%q body=%s, want the stored 409", replay.Code, replay.Header().Get(httpadapter.ReplayedHeader), replay.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1: a replay must never re-run it", calls.Load())
	}
}

// TestIdempotency_A2xxOverAnAbortedTransactionIsNeverReturnedOrCached: a unit
// of work that hides an aborted transaction cannot commit, so the handler's
// success never happens; the request fails with 5xx, nothing is stored, and the
// same key retries the real work.
func TestIdempotency_A2xxOverAnAbortedTransactionIsNeverReturnedOrCached(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			if err := poison(r, pool, true); err != nil {
				w.WriteHeader(http.StatusInternalServerError) // commit on an aborted transaction fails
				return
			}
			t.Error("committing an aborted transaction must fail")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sessionId":"s1"}`))
	})
	mw := httpadapter.RequireIdempotencyKey(postgres.NewIdempotencyStore(pool), wallClock{})(handler)

	first := httptest.NewRecorder()
	mw.ServeHTTP(first, keyedRequest("k-lie"))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first = %d %s, want 500 (the writes never happened)", first.Code, first.Body.String())
	}
	if n := count(t, pool, `SELECT count(*) FROM idempotency_keys WHERE key = 'k-lie'`); n != 0 {
		t.Fatalf("a failed success left %d idempotency rows: the key could not retry", n)
	}

	second := httptest.NewRecorder()
	mw.ServeHTTP(second, keyedRequest("k-lie"))
	if second.Code != http.StatusCreated || second.Header().Get(httpadapter.ReplayedHeader) != "" {
		t.Fatalf("retry = %d %s, want the real work to succeed", second.Code, second.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", calls.Load())
	}
}

package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/application/idempotency"
	"github.com/IQVO/checkout/internal/application/ports"
	cs "github.com/IQVO/checkout/internal/domain/checkoutsession"
	"github.com/IQVO/checkout/internal/domain/shared"
)

var errBoom = errors.New("boom")

func testUnitOfWork(t *testing.T, e Env) {
	t.Run("a unit that fails rolls back every table", func(t *testing.T) {
		err := e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.Sessions.Save(ctx, open(t, "sess-rb")))
			must(t, e.Outbox.Append(ctx, mustEvents(t)...))
			return errBoom
		})
		wantErr(t, err, errBoom)
		_, err = e.Sessions.Get(bg(), "sess-rb")
		wantErr(t, err, ports.ErrNotFound)
		if n := e.OutboxCount(t); n != 0 {
			t.Fatalf("outbox holds %d events after a rollback", n)
		}
	})
	t.Run("a unit that succeeds commits everything", func(t *testing.T) {
		must(t, e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.Sessions.Save(ctx, open(t, "sess-ok")))
			must(t, e.Outbox.Append(ctx, mustEvents(t)...))
			return nil
		}))
		if _, err := e.Sessions.Get(bg(), "sess-ok"); err != nil {
			t.Fatalf("committed session missing: %v", err)
		}
		if n := e.OutboxCount(t); n != 2 {
			t.Fatalf("outbox holds %d events, want 2", n)
		}
	})
	t.Run("a nested unit joins the outer one", func(t *testing.T) {
		err := e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.UoW.Do(ctx, func(ctx context.Context) error { return e.Sessions.Save(ctx, open(t, "sess-nest")) }))
			return errBoom
		})
		wantErr(t, err, errBoom)
		_, err = e.Sessions.Get(bg(), "sess-nest")
		wantErr(t, err, ports.ErrNotFound)
	})
}

// mustEvents returns two events a session raises.
func mustEvents(t *testing.T) []shared.Event {
	t.Helper()
	s, err := cs.Open(openInput("sess-ev"), t0)
	must(t, err)
	must(t, s.Confirm("tok", at(1)))
	must(t, s.RecordAuthorization("auth-ev", 2100, "USD", []byte("{}"), at(2)))
	must(t, s.RecordPlaced("order-ev", at(3)))
	return s.PullEvents()
}

func testIdempotency(t *testing.T, e Env) {
	_, err := e.Idempotency.Get(bg(), "POST /checkout-sessions", "k1")
	wantErr(t, err, ports.ErrNotFound)
	rec := idempotency.Record{RequestHash: "h1", Status: 201, ContentType: "application/json", Body: []byte(`{"a":1}`), CreatedAt: e.Clock.Now().UTC()}
	stored, err := e.Idempotency.Put(bg(), "POST /checkout-sessions", "k1", rec)
	must(t, err)
	if !stored {
		t.Fatal("the first Put must store")
	}
	stored, err = e.Idempotency.Put(bg(), "POST /checkout-sessions", "k1", idempotency.Record{RequestHash: "h2", Status: 500, CreatedAt: e.Clock.Now().UTC()})
	must(t, err)
	if stored {
		t.Fatal("a second Put must not overwrite")
	}
	got, err := e.Idempotency.Get(bg(), "POST /checkout-sessions", "k1")
	must(t, err)
	if got.RequestHash != "h1" || got.Status != 201 || got.ContentType != "application/json" || string(got.Body) != `{"a":1}` {
		t.Fatalf("stored record = %+v", got)
	}
	if _, err := e.Idempotency.Get(bg(), "POST /checkout-sessions/{sessionId}:confirm", "k1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("the route is part of the key: %v", err)
	}
	old := idempotency.Record{RequestHash: "old", Status: 200, CreatedAt: e.Clock.Now().UTC().Add(-idempotency.TTL - time.Minute)}
	if _, err := e.Idempotency.Put(bg(), "POST /checkout-sessions/{sessionId}:confirm", "k-old", old); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Idempotency.Get(bg(), "POST /checkout-sessions/{sessionId}:confirm", "k-old"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("an expired record must read as absent: %v", err)
	}
	replaced, err := e.Idempotency.Put(bg(), "POST /checkout-sessions/{sessionId}:confirm", "k-old", idempotency.Record{RequestHash: "new", Status: 201, CreatedAt: e.Clock.Now().UTC()})
	must(t, err)
	if !replaced {
		t.Fatal("an expired record is replaceable")
	}
}

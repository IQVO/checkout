package usecases_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/memory"
	"github.com/IQVO/checkout/internal/application/exchange"
	"github.com/IQVO/checkout/internal/application/ports"
)

// recordingMetrics keeps the "outcome/reason" of each ports.SessionMetrics call.
type recordingMetrics struct {
	mu    sync.Mutex
	calls []string
}

var _ ports.SessionMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) SessionConfirmed(_ context.Context, outcome, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, outcome+"/"+reason)
}

func (e *env) metered() *recordingMetrics {
	m := &recordingMetrics{}
	e.deps.Metrics = m
	return m
}

func wantCalls(t *testing.T, m *recordingMetrics, want ...string) {
	t.Helper()
	if !equalStrings(m.calls, want) {
		t.Fatalf("metric calls = %v, want %v", m.calls, want)
	}
}

func TestConfirmCountsACompletedSessionOnce(t *testing.T) {
	e := newEnv()
	m := e.metered()
	e.open(t)
	if _, err := e.confirm("sess-1", "tok_1"); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, m, "completed/")
}

func TestConfirmCountsADeclineWithItsReason(t *testing.T) {
	e := newEnv()
	m := e.metered()
	e.open(t)
	e.payments.Script = []memory.AuthStep{{Result: exchange.Authorization{DeclineReason: "insufficient-funds"}}}
	if _, err := e.confirm("sess-1", "tok_1"); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, m, "declined/payment-declined")
}

func TestConfirmCountsAnUnknownAuthorizationAsInDoubt(t *testing.T) {
	e := newEnv()
	m := e.metered()
	e.open(t)
	e.payments.Script = []memory.AuthStep{{Err: ports.ErrPaymentsUnavailable}}
	if _, err := e.confirm("sess-1", "tok_1"); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, m, "in-doubt/authorization-unknown")
}

func TestConfirmCountsAMismatchAsDeclined(t *testing.T) {
	e := newEnv()
	m := e.metered()
	e.open(t)
	e.payments.Script = []memory.AuthStep{{Mutate: func(a *exchange.Authorization) { a.TotalMinor-- }}}
	_, _ = e.confirm("sess-1", "tok_1")
	wantCalls(t, m, "declined/authorization-mismatch")
}

func TestConfirmCountsRefusalsByTheirReason(t *testing.T) {
	e := newEnv()
	m := e.metered()
	e.open(t)
	e.clock.Advance(16 * time.Minute)
	_, _ = e.confirm("sess-1", "tok")
	_, _ = e.confirm("missing", "tok")
	wantCalls(t, m, "refused/price-quote-expired", "refused/not-found")
}

func TestConfirmWithoutMetricsRecordsNothing(t *testing.T) {
	e := newEnv()
	e.open(t)
	if _, err := e.confirm("sess-1", "tok_1"); err != nil {
		t.Fatal(err)
	}
}

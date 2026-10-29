package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/IQVO/checkout/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/IQVO/checkout"

// sessionsConfirmedName is the Tier 2 business metric of the context (ADR 0006,
// the fleet's standard metrics convention): every confirm attempt, by outcome.
const sessionsConfirmedName = "checkout.sessions.confirmed"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")
)

// SessionMetrics implements ports.SessionMetrics against the global
// MeterProvider. Until Setup installs a real provider the global one is a
// no-op, so recording is cheap and safe in tests and local runs.
type SessionMetrics struct {
	counter metric.Int64Counter
}

var _ ports.SessionMetrics = (*SessionMetrics)(nil)

// NewSessionMetrics registers the counter. It only fails if the instrument name
// is invalid, which is a programming error, not a runtime condition. Call it
// after Setup so the instrument binds to the real MeterProvider.
func NewSessionMetrics() (*SessionMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		sessionsConfirmedName,
		metric.WithDescription("Checkout confirm attempts by outcome (completed, declined, requote-required, failed, in-doubt or refused; "+
			"reason says why when the outcome is not a success, such as payment-declined, placement-in-doubt or place-order-rejected). "+
			"A falling completed share with rising declined means payment is refusing shoppers; rising requote-required means offers "+
			"expire or change before shoppers confirm; rising in-doubt means the place-order step is slow or failing and orders may be "+
			"unaccounted for; rising refused with internal-error means the service itself is failing."),
		metric.WithUnit("{session}"),
	)
	if err != nil {
		return nil, err
	}
	return &SessionMetrics{counter: counter}, nil
}

// SessionConfirmed counts one confirm attempt with its outcome and, when there
// is one, the reason.
func (m *SessionMetrics) SessionConfirmed(ctx context.Context, outcome, reason string) {
	attrs := []attribute.KeyValue{outcomeKey.String(outcome)}
	if reason != "" {
		attrs = append(attrs, reasonKey.String(reason))
	}
	m.counter.Add(ctx, 1, metric.WithAttributes(attrs...))
}

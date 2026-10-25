// Package alerts is the production ports.Alerter: an alert is an ERROR log
// line with a stable `alert` code that the platform's log-based alert rules
// match (placement-in-doubt-20h, placement-given-up-22h, place-order-rejected,
// authorization-mismatch, sibling-contract-violation).
package alerts

import (
	"context"
	"log/slog"

	"github.com/IQVO/checkout/internal/application/ports"
)

// Logger writes alerts to a slog.Logger.
type Logger struct{ Log *slog.Logger }

var _ ports.Alerter = Logger{}

// Alert implements ports.Alerter.
func (l Logger) Alert(ctx context.Context, code, sessionID, detail string) {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	log.ErrorContext(ctx, "ALERT", "alert", code, "sessionId", sessionID, "detail", detail)
}

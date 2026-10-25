package alerts_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/IQVO/checkout/internal/adapters/outbound/alerts"
)

func TestAlertIsAnErrorLineWithTheStableCode(t *testing.T) {
	var buf bytes.Buffer
	l := alerts.Logger{Log: slog.New(slog.NewJSONHandler(&buf, nil))}
	l.Alert(context.Background(), "placement-in-doubt-20h", "sess-1", "in doubt for 20h0m0s")
	out := buf.String()
	for _, want := range []string{`"level":"ERROR"`, `"alert":"placement-in-doubt-20h"`, `"sessionId":"sess-1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("%s lacks %s", out, want)
		}
	}
	alerts.Logger{}.Alert(context.Background(), "x", "", "falls back to the default logger")
}

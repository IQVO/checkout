package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/httpx"
	outboxrelay "github.com/IQVO/checkout/internal/adapters/outbound/outbox"
	"github.com/IQVO/checkout/internal/adapters/outbound/telemetry"
)

// Boot errors for the variables the context cannot run without. Each message
// names the variable.
var (
	errMissingDatabaseURL = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")
	errMissingBrokers     = errors.New("KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, /readyz)")
)

// siblingURLs are the base URLs of the five services the saga calls; every one
// is mandatory.
var siblingURLs = []string{
	"SHOPPING_CART_URL", "DELIVERY_PROMISE_URL", "PRICING_URL", "CUSTOMER_PAYMENTS_URL", "CUSTOMER_ORDERING_URL",
}

// runtimeEnv is the validated part of the environment that selects the real
// runtime.
type runtimeEnv struct {
	databaseURL string
	brokers     []string
	// urls maps a sibling variable (SHOPPING_CART_URL, ...) to its base URL.
	urls map[string]string
	// timeout is the per-call timeout of the synchronous siblings.
	timeout time.Duration
	// orderingTimeout bounds the place-order call (it promises and confirms
	// every shipment group before it answers).
	orderingTimeout time.Duration
}

// loadRuntime validates the environment before anything is opened or bound:
// DATABASE_URL, KAFKA_BROKERS and the five sibling URLs are mandatory. This
// context consumes no topic, so there is no *_CONSUMER_GROUP. Every error names
// the variable.
func loadRuntime(env func(string) string) (runtimeEnv, error) {
	rt := runtimeEnv{databaseURL: env("DATABASE_URL"), urls: map[string]string{}}
	if rt.databaseURL == "" {
		return runtimeEnv{}, errMissingDatabaseURL
	}
	if rt.brokers = splitBrokers(env("KAFKA_BROKERS")); len(rt.brokers) == 0 {
		return runtimeEnv{}, errMissingBrokers
	}
	for _, name := range siblingURLs {
		raw := strings.TrimSpace(env(name))
		if raw == "" {
			return runtimeEnv{}, fmt.Errorf("%s is required: checkout calls this service synchronously", name)
		}
		if u, err := url.Parse(raw); err != nil || u.Scheme == "" || u.Host == "" {
			return runtimeEnv{}, fmt.Errorf("%s: %q is not an absolute URL", name, raw)
		}
		rt.urls[name] = raw
	}
	var err error
	if rt.timeout, err = positiveDuration("SIBLING_TIMEOUT", env("SIBLING_TIMEOUT"), httpx.DefaultTimeout); err != nil {
		return runtimeEnv{}, err
	}
	if rt.orderingTimeout, err = positiveDuration("CUSTOMER_ORDERING_TIMEOUT", env("CUSTOMER_ORDERING_TIMEOUT"), 15*time.Second); err != nil {
		return runtimeEnv{}, err
	}
	return rt, nil
}

func positiveDuration(name, raw string, fallback time.Duration) (time.Duration, error) {
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s: %q is not a positive Go duration", name, raw)
	}
	return d, nil
}

// splitBrokers splits KAFKA_BROKERS on commas, trimming blanks.
func splitBrokers(raw string) []string {
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

func joinBrokers(brokers []string) string { return strings.Join(brokers, ",") }

// parseInterval reads a Go-duration environment variable, falling back (with a
// warning) when it is unset, malformed or not positive.
func parseInterval(name, raw string, fallback time.Duration, logger *slog.Logger) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		logger.Warn("invalid "+name+"; using the default", "value", raw, "default", fallback)
		return fallback
	}
	return d
}

// parseRelayInterval reads OUTBOX_RELAY_INTERVAL (Go duration or plain
// seconds), defaulting when unset, malformed or non-positive.
func parseRelayInterval(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return outboxrelay.DefaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, serr := strconv.ParseFloat(raw, 64); serr == nil {
			d, err = time.Duration(secs*float64(time.Second)), nil
		}
	}
	if err != nil || d <= 0 {
		logger.Warn("invalid OUTBOX_RELAY_INTERVAL; using the default", "value", raw, "default", outboxrelay.DefaultInterval)
		return outboxrelay.DefaultInterval
	}
	return d
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS: how many times an outbox row may
// fail before the relay excludes it from claims. Unset = the fleet default (20);
// 0 = unlimited; negative or unparsable values log a warning and use the
// default, so a typo never silently removes the cap.
func outboxMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		logger.Warn("invalid OUTBOX_MAX_ATTEMPTS; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// shutdownDrainDelay reads SHUTDOWN_DRAIN_DELAY ("0" disables; unset, negative
// or unparsable = the default).
func shutdownDrainDelay(logger *slog.Logger) time.Duration {
	raw := os.Getenv("SHUTDOWN_DRAIN_DELAY")
	if raw == "" {
		return DefaultShutdownDrainDelay
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn("ignoring invalid SHUTDOWN_DRAIN_DELAY", "value", raw, "default", DefaultShutdownDrainDelay.String())
		return DefaultShutdownDrainDelay
	}
	return d
}

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error
// (the handler is wrapped so a record written with a span-carrying context also
// carries trace_id and span_id).
// (case-insensitive; "warning" is accepted like "warn" fleet-wide).
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

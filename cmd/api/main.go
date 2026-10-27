// Command api is checkout's composition root: it wires env config into
// adapters, adapters into use cases, and use cases into the HTTP router, the
// five clock-driven policies and the outbox relay, all on real Postgres and
// real Kafka: there is no in-memory mode and no log sink. This context
// consumes no topic, so there is no consumer and no consumer group.
//
// Environment:
//
//	HTTP_ADDR                   listen address (default :8080)
//	DATABASE_URL                Postgres DSN (REQUIRED)
//	MIGRATIONS_DATABASE_URL     direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS               comma-separated brokers (REQUIRED: relay, /readyz)
//	SHOPPING_CART_URL           base URLs of the five services the saga calls
//	DELIVERY_PROMISE_URL        (ALL REQUIRED, boot error naming the variable)
//	PRICING_URL
//	CUSTOMER_PAYMENTS_URL
//	CUSTOMER_ORDERING_URL
//	SIBLING_TIMEOUT             per-call timeout of the siblings (Go duration, default 5s)
//	CUSTOMER_ORDERING_TIMEOUT   timeout of the place-order call (Go duration, default 15s)
//	INDOUBT_SWEEP_INTERVAL      policy ResolveInDoubtPlacements tick (default 15s)
//	STUCK_SWEEP_INTERVAL        policy ResolveStuckAuthorizations tick (default 30s)
//	VOID_SWEEP_INTERVAL         policy RetryOwedVoids tick (default 1m)
//	EXPIRY_SWEEP_INTERVAL       policy ExpireOnQuoteExpiry tick (default 1m)
//	PURGE_SWEEP_INTERVAL        stored-placement purge tick (default 1h)
//	OUTBOX_RELAY_INTERVAL       relay poll interval (Go duration or seconds, default 1s)
//	OUTBOX_MAX_ATTEMPTS         failures before the relay excludes a row (default 20, 0 = unlimited)
//	SHUTDOWN_DRAIN_DELAY        wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL                   debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME           service.name of spans and metrics (default checkout)
//	OTEL_EXPORTER_OTLP_ENDPOINT Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION             service.version (default dev)
//	ENVIRONMENT                 deployment.environment.name (default local)
//
// GET /readyz is 200 only while Postgres answers a ping AND a Kafka broker
// answers a metadata request (verdict cached 3s, bounded 2s); /healthz is
// liveness only. The process exits non-zero when the relay or a scheduled
// policy returns on its own, so the orchestrator restarts it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundhttp "github.com/IQVO/checkout/internal/adapters/inbound/http"
	outboundkafka "github.com/IQVO/checkout/internal/adapters/outbound/kafka"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// workerDrainTimeout bounds how long shutdown waits for the schedulers and the
// relay to return after their context is cancelled.
const workerDrainTimeout = 10 * time.Second

// DefaultShutdownDrainDelay is how long shutdown waits, after flipping /readyz
// to not-ready, before closing the listener.
const DefaultShutdownDrainDelay = 5 * time.Second

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way).
func run(ctx context.Context) error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Misconfiguration is refused before anything is opened or bound.
	rt, err := loadRuntime(os.Getenv)
	if err != nil {
		return err
	}

	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed LAST: deferred before
	// everything below, so the final spans of the drain still leave the process.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	// closeAdapters (pool.Close) is deferred FIRST so it runs LAST, after the
	// HTTP drain, the schedulers and the relay have all stopped.
	ad, closeAdapters, err := buildAdapters(ctx, rt, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	readiness := newReadiness(ad.pool.Ping, rt.brokers)
	httpServer := &http.Server{
		Addr:              getenv("HTTP_ADDR", ":8080"),
		Handler:           inboundhttp.NewRouter(buildServer(ad, readiness), logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}

	relay, closeRelay := startOutboxRelay(ad.outbox, rt.brokers, logger)
	jobs, closeJobs := startSchedulers(ad.deps, logger)
	return serveUntilSignal(ctx, logger, httpServer, readiness, jobs, closeJobs, relay, closeRelay)
}

// newReadiness builds the /readyz gate: the drain gate plus a Postgres ping and
// a Kafka broker answering a topic-less metadata request, cached 3s, bounded 2s.
// The ping is a parameter so the gate is testable without a pool.
func newReadiness(ping func(context.Context) error, brokers []string) *inboundhttp.Readiness {
	return inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "postgres", Check: ping},
		inboundhttp.Dependency{Name: "kafka", Check: outboundkafka.NewBrokerCheck(brokers).Check},
	)
}

// serveUntilSignal runs httpServer until SIGINT/SIGTERM (or a listen error),
// then shuts down in the fleet order: (1) /readyz flips to 503, (2) wait the
// drain delay, (3) drain HTTP, (4) stop and await the schedulers, (5) stop and
// await the relay LAST so nothing committed above is stranded; the caller's
// deferred pool.Close runs after all of it.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness,
	jobs []*worker, closeJobs func(), relay *worker, closeRelay func(),
) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	died := watchWorkers(append([]*worker{relay}, jobs...)...)
	select {
	case err := <-errCh:
		closeJobs()
		closeRelay()
		return err
	case name := <-died:
		// The relay or a policy returned on its own: the service cannot run
		// without it and must not sit "alive" with a dead worker, so exit
		// non-zero and let the orchestrator restart it.
		closeJobs()
		closeRelay()
		return fmt.Errorf("%s stopped unexpectedly: exiting so the orchestrator restarts the service", name)
	case <-sigCtx.Done():
	}

	readiness.SetNotReady()
	if delay := shutdownDrainDelay(logger); delay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", delay.String())
		time.Sleep(delay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)

	closeJobs()
	for _, w := range jobs {
		await(logger, w)
	}
	closeRelay()
	await(logger, relay)
	return err
}

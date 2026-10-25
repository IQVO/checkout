package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/IQVO/checkout/internal/adapters/inbound/scheduler"
	outboundkafka "github.com/IQVO/checkout/internal/adapters/outbound/kafka"
	outboxrelay "github.com/IQVO/checkout/internal/adapters/outbound/outbox"
	"github.com/IQVO/checkout/internal/application/usecases"
)

// worker is a started background loop and the channel closed once it returned.
type worker struct {
	name string
	done chan struct{}
}

// startOutboxRelay starts the always-on relay goroutine over the Kafka sink.
// Kafka is dialled lazily on the first send, never here.
func startOutboxRelay(store outboxrelay.Store, brokers []string, logger *slog.Logger) (*worker, func()) {
	writer := outboundkafka.NewWriter(joinBrokers(brokers))
	interval := parseRelayInterval(os.Getenv("OUTBOX_RELAY_INTERVAL"), logger)
	maxAttempts := outboxMaxAttempts(os.Getenv("OUTBOX_MAX_ATTEMPTS"), logger)
	relay := outboxrelay.NewRelay(store, outboundkafka.Sink{W: writer}, logger, outboxrelay.WithInterval(interval), outboxrelay.WithMaxAttempts(maxAttempts))

	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "outbox-relay", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer func() {
			if err := writer.Close(); err != nil {
				logger.Error("kafka relay writer close failed", "error", err)
			}
		}()
		logger.Info("outbox relay running", "interval", interval, "max_attempts", maxAttempts,
			"events_topic", outboundkafka.EventsTopic, "analytics_topic", outboundkafka.AnalyticsTopic)
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("outbox relay stopped", "error", err)
		}
	}()
	return w, stop
}

// watchWorkers returns a channel that receives the name of every worker that
// returns. Workers only return when stopped or on an unrecoverable failure, so
// a value read while the service is serving means a worker died.
func watchWorkers(workers ...*worker) <-chan string {
	died := make(chan string, len(workers))
	for _, w := range workers {
		if w == nil {
			continue
		}
		go func() {
			<-w.done
			died <- w.name
		}()
	}
	return died
}

// await waits (bounded) for w to return; nil w is a no-op.
func await(logger *slog.Logger, w *worker) {
	if w == nil {
		return
	}
	select {
	case <-w.done:
	case <-time.After(workerDrainTimeout):
		logger.Warn("worker did not stop before the shutdown drain deadline", "worker", w.name)
	}
}

// startSchedulers starts the clock-driven policies of the saga.
func startSchedulers(d usecases.Deps, logger *slog.Logger) ([]*worker, func()) {
	ctx, stop := context.WithCancel(context.Background())
	every := func(env string, fallback time.Duration) time.Duration {
		return parseInterval(env, os.Getenv(env), fallback, logger)
	}
	jobs := []scheduler.Job{
		{Name: "ResolveInDoubtPlacements", Sweep: usecases.ResolveInDoubtPlacements{Deps: d}, Interval: every("INDOUBT_SWEEP_INTERVAL", scheduler.DefaultInDoubtInterval), Log: logger},
		{Name: "ResolveStuckAuthorizations", Sweep: usecases.ResolveStuckAuthorizations{Deps: d}, Interval: every("STUCK_SWEEP_INTERVAL", scheduler.DefaultStuckInterval), Log: logger},
		{Name: "RetryOwedVoids", Sweep: usecases.RetryOwedVoids{Deps: d}, Interval: every("VOID_SWEEP_INTERVAL", scheduler.DefaultVoidInterval), Log: logger},
		{Name: "ExpireOnQuoteExpiry", Sweep: usecases.ExpireOnQuoteExpiry{Deps: d}, Interval: every("EXPIRY_SWEEP_INTERVAL", scheduler.DefaultExpiryInterval), Log: logger},
		{Name: "PurgeStoredPlacements", Sweep: usecases.PurgeStoredPlacements{Deps: d}, Interval: every("PURGE_SWEEP_INTERVAL", scheduler.DefaultPurgeInterval), Log: logger},
	}
	workers := make([]*worker, 0, len(jobs))
	for _, job := range jobs {
		w := &worker{name: "scheduler " + job.Name, done: make(chan struct{})}
		workers = append(workers, w)
		go func() {
			defer close(w.done)
			logger.Info("scheduler running", "job", job.Name, "interval", job.Interval.String())
			if err := job.Run(ctx); !errors.Is(err, context.Canceled) {
				logger.Error("scheduler stopped", "job", job.Name, "error", err)
			}
		}()
	}
	return workers, stop
}

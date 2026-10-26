package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/IQVO/checkout/internal/adapters/inbound/http"
	"github.com/IQVO/checkout/internal/adapters/outbound/alerts"
	"github.com/IQVO/checkout/internal/adapters/outbound/clock"
	"github.com/IQVO/checkout/internal/adapters/outbound/customerordering"
	"github.com/IQVO/checkout/internal/adapters/outbound/customerpayments"
	"github.com/IQVO/checkout/internal/adapters/outbound/deliverypromise"
	"github.com/IQVO/checkout/internal/adapters/outbound/ids"
	outboundkafka "github.com/IQVO/checkout/internal/adapters/outbound/kafka"
	"github.com/IQVO/checkout/internal/adapters/outbound/postgres"
	"github.com/IQVO/checkout/internal/adapters/outbound/pricing"
	"github.com/IQVO/checkout/internal/adapters/outbound/shoppingcart"
	"github.com/IQVO/checkout/internal/application/usecases"
	"github.com/IQVO/checkout/internal/bootretry"
)

// adapters is the set of outbound adapters the composition root wires.
type adapters struct {
	deps        usecases.Deps
	idempotency *postgres.IdempotencyStore
	outbox      *postgres.OutboxRepo
	pool        *pgxpool.Pool
	clock       clock.System
	ids         ids.Generator
}

// buildAdapters wires the Postgres adapters, running the embedded migrations
// first, and the HTTP gateways of the five siblings.
func buildAdapters(ctx context.Context, rt runtimeEnv, logger *slog.Logger) (adapters, func(), error) {
	noop := func() {}
	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the LAST error is returned and the process refuses to boot.
	migrationsURL := getenv("MIGRATIONS_DATABASE_URL", rt.databaseURL)
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error { return postgres.RunMigrations(migrationsURL) }); err != nil {
		return adapters{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, rt.databaseURL)
	if err != nil {
		return adapters{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return adapters{}, noop, err
	}
	logger.Info("postgres adapters configured")

	outbox := postgres.NewOutboxRepo(pool, outboundkafka.NewEncoder())
	ad := adapters{pool: pool, outbox: outbox, idempotency: postgres.NewIdempotencyStore(pool)}
	client := &http.Client{Timeout: rt.timeout}
	ad.deps = usecases.Deps{
		Sessions: postgres.NewSessionRepository(pool),
		Carts:    shoppingcart.New(rt.urls["SHOPPING_CART_URL"], client),
		Offers:   deliverypromise.New(rt.urls["DELIVERY_PROMISE_URL"], client),
		Quoter:   pricing.New(rt.urls["PRICING_URL"], client),
		Payments: customerpayments.New(rt.urls["CUSTOMER_PAYMENTS_URL"], client),
		Placer:   customerordering.New(rt.urls["CUSTOMER_ORDERING_URL"], &http.Client{Timeout: rt.orderingTimeout}),
		Outbox:   outbox, UoW: postgres.NewUnitOfWork(pool), Clock: ad.clock, IDs: ad.ids,
		Alerter: alerts.Logger{Log: logger}, Log: logger,
	}
	return ad, pool.Close, nil
}

func buildServer(ad adapters, readiness *inboundhttp.Readiness) *inboundhttp.Server {
	d := ad.deps
	return &inboundhttp.Server{
		OpenSession: usecases.OpenSession{Deps: d}, ConfirmSession: usecases.ConfirmSession{Deps: d}, GetSession: usecases.GetSession{Deps: d},
		IdempotencyStore: ad.idempotency, Clock: ad.clock, Readiness: readiness,
	}
}

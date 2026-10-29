//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/outbound/kafka"
	"github.com/IQVO/checkout/internal/adapters/outbound/postgres"
	"github.com/IQVO/checkout/internal/adapters/outbound/repocontract"
	"github.com/IQVO/checkout/internal/testing/pgtest"
)

func TestPostgresHonoursTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Env {
		pool := pgtest.NewPool(t)
		return repocontract.Env{
			Sessions: postgres.NewSessionRepository(pool), Outbox: postgres.NewOutboxRepo(pool, kafka.NewEncoder()),
			UoW: postgres.NewUnitOfWork(pool), Idempotency: postgres.NewIdempotencyStore(pool),
			// The Postgres store judges TTL with the database's now(); the
			// contract stamps records with this wall clock.
			Clock: wallClock{},
			OutboxCount: func(t *testing.T) int {
				var n int
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			},
		}
	})
}

// wallClock is the real time: created_at and now() live in the same database.
type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

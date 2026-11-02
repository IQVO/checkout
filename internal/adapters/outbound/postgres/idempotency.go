package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/checkout/internal/application/idempotency"
	"github.com/IQVO/checkout/internal/application/ports"
)

// IdempotencyStore is the Postgres ports.IdempotencyStore over idempotency_keys.
// The key is the primary key on its own (migration 0002): one Idempotency-Key
// names one request fingerprint across every route of the service.
type IdempotencyStore struct{ pool *pgxpool.Pool }

var _ ports.IdempotencyStore = (*IdempotencyStore)(nil)

// NewIdempotencyStore constructs an IdempotencyStore over pool.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore { return &IdempotencyStore{pool: pool} }

func ttlSeconds() float64 { return idempotency.TTL.Seconds() }

// Get implements ports.IdempotencyStore.
func (s *IdempotencyStore) Get(ctx context.Context, key string) (idempotency.Record, error) {
	var rec idempotency.Record
	err := queryFor(ctx, s.pool).QueryRow(ctx, `
		SELECT request_hash, status, content_type, body, created_at FROM idempotency_keys
		WHERE key = $1 AND created_at > now() - make_interval(secs => $2)`, key, ttlSeconds()).
		Scan(&rec.RequestHash, &rec.Status, &rec.ContentType, &rec.Body, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.Record{}, ports.ErrNotFound
	}
	if err != nil {
		return idempotency.Record{}, fmt.Errorf("read idempotency key: %w", err)
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, nil
}

// Put implements ports.IdempotencyStore: it stores the record unless a fresh
// one exists; an expired record is replaced.
func (s *IdempotencyStore) Put(ctx context.Context, key string, rec idempotency.Record) (bool, error) {
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	body := rec.Body
	if body == nil {
		body = []byte{}
	}
	stored := false
	err := inTx(ctx, s.pool, func(q querier) error {
		tag, err := q.Exec(ctx, `
			INSERT INTO idempotency_keys (key, request_hash, status, content_type, body, created_at) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (key) DO UPDATE SET request_hash = EXCLUDED.request_hash, status = EXCLUDED.status,
				content_type = EXCLUDED.content_type, body = EXCLUDED.body, created_at = EXCLUDED.created_at
			WHERE idempotency_keys.created_at <= now() - make_interval(secs => $7)`,
			key, rec.RequestHash, rec.Status, rec.ContentType, body, created.UTC(), ttlSeconds())
		stored = err == nil && tag.RowsAffected() == 1
		return err
	})
	if err != nil {
		return false, fmt.Errorf("store idempotency key: %w", err)
	}
	return stored, nil
}

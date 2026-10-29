// Package scheduler is the clock-driven inbound adapter: it runs the policies
// of the checkout saga (ResolveInDoubtPlacements, ResolveStuckAuthorizations,
// RetryOwedVoids, ExpireOnQuoteExpiry and the placement purge) on a fixed interval. Each tick drains
// its backlog in batches (a full batch means there may be more) and logs what
// it did; a failing tick is logged and retried at the next one, never fatal.
package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// Defaults for the sweep intervals and the batch size.
const (
	DefaultInDoubtInterval = 15 * time.Second
	DefaultStuckInterval   = 30 * time.Second
	DefaultVoidInterval    = time.Minute
	DefaultExpiryInterval  = time.Minute
	DefaultPurgeInterval   = time.Hour
	DefaultBatch           = 200
)

// Sweep is one policy run: it handles up to limit sessions and returns how many.
type Sweep interface {
	Execute(ctx context.Context, limit int) (int, error)
}

// Job is a named Sweep run every Interval.
type Job struct {
	Name     string
	Sweep    Sweep
	Interval time.Duration
	// Batch is the page size; DefaultBatch when not positive.
	Batch int
	Log   *slog.Logger
}

// Tick runs the sweep until a batch comes back short, and returns the total.
func (j Job) Tick(ctx context.Context) (int, error) {
	batch := j.Batch
	if batch <= 0 {
		batch = DefaultBatch
	}
	total := 0
	for {
		n, err := j.Sweep.Execute(ctx, batch)
		total += n
		if err != nil || n < batch || ctx.Err() != nil {
			return total, err
		}
	}
}

// Run ticks immediately and then every Interval until ctx is cancelled, which
// is the only reason it returns (context.Canceled).
func (j Job) Run(ctx context.Context) error {
	log := j.Log
	if log == nil {
		log = slog.Default()
	}
	interval := j.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		n, err := j.Tick(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			log.ErrorContext(ctx, "scheduled sweep failed; trying again at the next tick", "job", j.Name, "handled", n, "error", err)
		case n > 0:
			log.InfoContext(ctx, "scheduled sweep done", "job", j.Name, "handled", n)
		}
		timer.Reset(interval)
	}
}

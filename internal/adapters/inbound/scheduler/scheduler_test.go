package scheduler_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/checkout/internal/adapters/inbound/scheduler"
)

type fakeSweep struct {
	mu      sync.Mutex
	answers []int
	err     error
	limits  []int
}

func (f *fakeSweep) Execute(_ context.Context, limit int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return 0, f.err
	}
	if len(f.answers) == 0 {
		return 0, nil
	}
	n := f.answers[0]
	f.answers = f.answers[1:]
	return n, nil
}

func (f *fakeSweep) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.limits)
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestTickDrainsFullBatchesUntilOneComesBackShort(t *testing.T) {
	s := &fakeSweep{answers: []int{3, 3, 1}}
	n, err := scheduler.Job{Name: "x", Sweep: s, Batch: 3, Log: quiet()}.Tick(context.Background())
	if err != nil || n != 7 || s.calls() != 3 || s.limits[0] != 3 {
		t.Fatalf("n=%d err=%v calls=%d limits=%v", n, err, s.calls(), s.limits)
	}
}

func TestTickUsesTheDefaultBatchAndStopsOnError(t *testing.T) {
	boom := errors.New("boom")
	s := &fakeSweep{err: boom}
	_, err := scheduler.Job{Sweep: s}.Tick(context.Background())
	if !errors.Is(err, boom) || s.limits[0] != scheduler.DefaultBatch {
		t.Fatalf("err=%v limits=%v", err, s.limits)
	}
}

func TestRunTicksImmediatelyThenOnTheIntervalUntilCancelled(t *testing.T) {
	s := &fakeSweep{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- scheduler.Job{Name: "x", Sweep: s, Interval: 5 * time.Millisecond, Log: quiet()}.Run(ctx)
	}()
	deadline := time.After(5 * time.Second)
	for s.calls() < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d ticks", s.calls())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}

func TestRunSurvivesAFailingTick(t *testing.T) {
	s := &fakeSweep{err: errors.New("db down")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Job{Name: "x", Sweep: s, Interval: time.Millisecond, Log: quiet()}.Run(ctx) }()
	for s.calls() < 2 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}

func TestRunDefaultsTheIntervalAndLogger(t *testing.T) {
	s := &fakeSweep{answers: []int{1}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Job{Name: "x", Sweep: s, Batch: 5}.Run(ctx) }()
	for s.calls() < 1 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

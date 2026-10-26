package memory

import (
	"fmt"
	"sync"
	"time"
)

// Clock is a settable ports.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock reading t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the current reading.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// SequentialIDs is a ports.IDGenerator minting sess-1, sess-2, ...
type SequentialIDs struct {
	mu       sync.Mutex
	sessions int
}

// NewSessionID returns the next cart id.
func (g *SequentialIDs) NewSessionID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessions++
	return fmt.Sprintf("sess-%d", g.sessions)
}

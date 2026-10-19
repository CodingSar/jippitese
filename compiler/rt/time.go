package rt

import (
	"sync"
	"time"
)

// Instant is `std.time.Instant`.
type Instant struct {
	Nanos int64 `json:"nanos"`
}

// Time converts to the standard library's representation.
func (i Instant) Time() time.Time { return time.Unix(0, i.Nanos).UTC() }

// Clock is where `now()` and `sleep` go.
type Clock interface {
	Now() Instant
	Sleep(ctx *Ctx, d time.Duration)
}

// RealClock is the wall clock.
type RealClock struct{}

// Now reads the wall clock.
func (RealClock) Now() Instant { return Instant{Nanos: time.Now().UnixNano()} }

// Sleep blocks, or returns early when ctx.Go is done.
func (RealClock) Sleep(ctx *Ctx, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Go.Done():
	}
}

// TestEpoch is where every FakeClock starts.
var TestEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// FakeClock is `std.testing.FakeClock`: it moves only when advanced or
// slept on.
type FakeClock struct {
	mu  sync.Mutex
	now Instant
}

// Now reads the fake clock.
func (c *FakeClock) Now() Instant {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep advances the fake clock by d.
func (c *FakeClock) Sleep(ctx *Ctx, d time.Duration) { c.Advance(d) }

// Advance moves the clock forward.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now.Nanos += int64(d)
}

// NewFakeClock returns a clock at the test epoch.
func NewFakeClock() *FakeClock { return &FakeClock{now: Instant{Nanos: TestEpoch.UnixNano()}} }

// FakeClockNew implements `FakeClock.new()`.
func FakeClockNew(ctx *Ctx) *FakeClock { return NewFakeClock() }

// FakeClockAdvance implements `FakeClock.advance(d)`.
func FakeClockAdvance(ctx *Ctx, c *FakeClock, d time.Duration) Unit {
	c.Advance(d)
	return U
}

// TimeNow implements `std.time.now()`.
func TimeNow(ctx *Ctx) Instant { return ctx.Clock.Now() }

// TimeSleep implements `std.time.sleep(d)`.
func TimeSleep(ctx *Ctx, d time.Duration) Unit {
	ctx.Clock.Sleep(ctx, d)
	return U
}

// InstantUnixNanos implements `Instant.unix_nanos`.
func InstantUnixNanos(ctx *Ctx, i Instant) int64 { return i.Nanos }

// InstantAdd implements `Instant.add`.
func InstantAdd(ctx *Ctx, i Instant, d time.Duration) Instant {
	return Instant{Nanos: i.Nanos + int64(d)}
}

// InstantSince implements `Instant.since`.
func InstantSince(ctx *Ctx, i, o Instant) time.Duration { return time.Duration(i.Nanos - o.Nanos) }

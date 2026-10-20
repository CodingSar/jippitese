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

// InstantAdd implements `Instant.add`; overflow traps.
func InstantAdd(ctx *Ctx, i Instant, d time.Duration) Instant {
	return Instant{Nanos: Add(ctx, i.Nanos, int64(d))}
}

// InstantSub implements `Instant.sub`; overflow traps.
func InstantSub(ctx *Ctx, i Instant, d time.Duration) Instant {
	return Instant{Nanos: Sub(ctx, i.Nanos, int64(d))}
}

// InstantSince implements `Instant.since`; overflow traps.
func InstantSince(ctx *Ctx, i, o Instant) time.Duration {
	return time.Duration(Sub(ctx, i.Nanos, o.Nanos))
}

func InstantBefore(ctx *Ctx, i, o Instant) bool { return i.Nanos < o.Nanos }
func InstantAfter(ctx *Ctx, i, o Instant) bool  { return i.Nanos > o.Nanos }

// InstantUnixMillis rounds toward the past, so the instant 1ns before the
// epoch is -1.
func InstantUnixMillis(ctx *Ctx, i Instant) int64 {
	ms := i.Nanos / 1e6
	if i.Nanos%1e6 < 0 {
		ms--
	}
	return ms
}

// InstantToRfc3339 is UTC with as many fraction digits as needed.
func InstantToRfc3339(ctx *Ctx, i Instant) string { return i.Time().Format(time.RFC3339Nano) }

// The UTC calendar fields; weekday is 1 (Monday) to 7 (Sunday).
func InstantYear(ctx *Ctx, i Instant) int64   { return int64(i.Time().Year()) }
func InstantMonth(ctx *Ctx, i Instant) int64  { return int64(i.Time().Month()) }
func InstantDay(ctx *Ctx, i Instant) int64    { return int64(i.Time().Day()) }
func InstantHour(ctx *Ctx, i Instant) int64   { return int64(i.Time().Hour()) }
func InstantMinute(ctx *Ctx, i Instant) int64 { return int64(i.Time().Minute()) }
func InstantSecond(ctx *Ctx, i Instant) int64 { return int64(i.Time().Second()) }
func InstantWeekday(ctx *Ctx, i Instant) int64 {
	if w := i.Time().Weekday(); w != time.Sunday {
		return int64(w)
	}
	return 7
}

// TimeFromUnixMillis implements `std.time.from_unix_millis`; an Instant
// outside the Int nanosecond range traps.
func TimeFromUnixMillis(ctx *Ctx, ms int64) Instant { return Instant{Nanos: Mul(ctx, ms, 1e6)} }

// TimeParseRfc3339 accepts RFC 3339 with or without a fraction and any offset.
func TimeParseRfc3339(ctx *Ctx, s string) Option[Instant] {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 1678 || t.Year() > 2261 {
		return Option_None[Instant]()
	}
	return Option_Some(Instant{Nanos: t.UnixNano()})
}

// TimeUtc implements `std.time.utc`; a field out of range (month 13, the
// 31st of April, hour 24) is None rather than normalised.
func TimeUtc(ctx *Ctx, year, month, day, hour, minute, second int64) Option[Instant] {
	if year < 1678 || year > 2261 {
		return Option_None[Instant]()
	}
	t := time.Date(int(year), time.Month(month), int(day), int(hour), int(minute), int(second), 0, time.UTC)
	if int64(t.Month()) != month || int64(t.Day()) != day || int64(t.Hour()) != hour || int64(t.Minute()) != minute || int64(t.Second()) != second {
		return Option_None[Instant]()
	}
	return Option_Some(Instant{Nanos: t.UnixNano()})
}

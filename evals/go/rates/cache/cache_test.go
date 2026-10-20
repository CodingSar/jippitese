package cache

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newFake is an empty cache on a fake clock.
func newFake() (*Rates, *fakeClock) {
	clock := &fakeClock{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := New()
	c.Now = clock.now
	return c, clock
}

func TestAnEntryIsServedUntilItsTTLPasses(t *testing.T) {
	c, clock := newFake()
	c.Set("EUR", 11000, 5*time.Minute)
	clock.advance(4 * time.Minute)
	if v, ok := c.Get("EUR"); !ok || v != 11000 {
		t.Errorf("Get = %d, %v; want 11000", v, ok)
	}
}

func TestAnEntryExpiresUnderTheFakeClock(t *testing.T) {
	c, clock := newFake()
	c.Set("EUR", 11000, 5*time.Minute)
	clock.advance(6 * time.Minute)
	if v, ok := c.Get("EUR"); ok {
		t.Errorf("Get = %d, want none", v)
	}
}

func TestRateRefillsAnExpiredEntryFromTheQuote(t *testing.T) {
	c, clock := newFake()
	c.Set("GBP", 1, 0)
	clock.advance(10 * time.Minute)
	if r := c.Rate("GBP"); r != 12700 {
		t.Errorf("Rate = %d, want 12700", r)
	}
	if v, ok := c.Get("GBP"); !ok || v != 12700 {
		t.Errorf("Get = %d, %v; want 12700", v, ok)
	}
}

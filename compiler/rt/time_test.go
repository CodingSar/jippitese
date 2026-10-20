package rt

import (
	"strings"
	"testing"
	"time"
)

func TestInstantCalendar(t *testing.T) {
	ctx := NewCtx()
	i := TimeUtc(ctx, 2026, 10, 18, 9, 30, 5).Some_0
	if InstantToRfc3339(ctx, i) != "2026-10-18T09:30:05Z" || InstantWeekday(ctx, i) != 7 || InstantYear(ctx, i) != 2026 || InstantMonth(ctx, i) != 10 || InstantDay(ctx, i) != 18 || InstantHour(ctx, i) != 9 || InstantMinute(ctx, i) != 30 || InstantSecond(ctx, i) != 5 {
		t.Fatalf("fields of %s", InstantToRfc3339(ctx, i))
	}
	if p := TimeParseRfc3339(ctx, "2026-10-18T11:30:05.5+02:00"); p.Tag != OptionTag_Some || InstantSince(ctx, p.Some_0, i) != 500*time.Millisecond {
		t.Fatalf("parse: %+v", p)
	}
	for _, bad := range []string{"2026-10-18", "2026-10-18 09:30:05Z", "9999-01-01T00:00:00Z", "x"} {
		if TimeParseRfc3339(ctx, bad).Tag != OptionTag_None {
			t.Fatalf("parsed %q", bad)
		}
	}
	for _, bad := range [][6]int64{{2026, 13, 1, 0, 0, 0}, {2026, 4, 31, 0, 0, 0}, {2025, 2, 29, 0, 0, 0}, {2026, 1, 1, 24, 0, 0}, {3000, 1, 1, 0, 0, 0}} {
		if TimeUtc(ctx, bad[0], bad[1], bad[2], bad[3], bad[4], bad[5]).Tag != OptionTag_None {
			t.Fatalf("utc %v", bad)
		}
	}
	if InstantUnixMillis(ctx, Instant{Nanos: -1}) != -1 || InstantUnixMillis(ctx, Instant{Nanos: 1_999_999}) != 1 || TimeFromUnixMillis(ctx, -2).Nanos != -2_000_000 {
		t.Fatal("unix millis")
	}
	if !InstantBefore(ctx, InstantSub(ctx, i, time.Second), i) || InstantAfter(ctx, i, i) {
		t.Fatal("ordering")
	}
	if m := traps(func() { InstantAdd(ctx, Instant{Nanos: 1 << 62}, 1<<62) }); !strings.Contains(m, "overflow") {
		t.Fatalf("add overflow: %q", m)
	}
	if DurationToStr(ctx, DurationTimes(ctx, DurationOfSeconds(ctx, 61), 60)) != "1h1m0s" || DurationHours(ctx, 3*time.Hour) != 3 || DurationMinutes(ctx, 90*time.Second) != 1 || DurationOfMillis(ctx, 5) != 5*time.Millisecond {
		t.Fatal("durations")
	}
}

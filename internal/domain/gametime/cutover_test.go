package gametime

import (
	"testing"
	"time"
)

// The live clock: legacy epoch and scale 60, cut over at a UTC midnight.
func liveClock() Clock {
	return Clock{
		Epoch: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Scale: 1, LegacyScale: 60,
		Cutover: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
	}
}

var zones = []time.Duration{0, 3*time.Hour + 30*time.Minute, -8 * time.Hour, 5*time.Hour + 45*time.Minute, 14 * time.Hour, -12 * time.Hour}

func TestCutoverIsAUTCMidnight(t *testing.T) {
	if err := liveClock().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := liveClock()
	bad.Cutover = bad.Cutover.Add(time.Hour)
	if bad.Validate() == nil {
		t.Error("a cut-over that is not a UTC midnight was accepted")
	}
}

func TestOffsets(t *testing.T) {
	for lon, want := range map[float64]time.Duration{0: 0, 7: 0, 8: time.Hour, 51.4: 3 * time.Hour, -74: -5 * time.Hour, -179: -12 * time.Hour, 179: 12 * time.Hour} {
		if got := OffsetFromLongitude(lon); got != want {
			t.Errorf("longitude %v -> %v, want %v", lon, got, want)
		}
	}
	if !ValidOffset(5*time.Hour+45*time.Minute) || ValidOffset(15*time.Hour) || ValidOffset(-13*time.Hour) || ValidOffset(time.Hour+time.Second) {
		t.Error("ValidOffset")
	}
	if got := LocalMidnight(time.Date(2026, 10, 7, 2, 0, 0, 0, time.UTC), -8*time.Hour); !got.Equal(time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("local midnight %v", got)
	}
}

// For every zone, day numbers never go back or repeat across that settlement's
// switch, and afterwards a day is a real local day.
func TestDayNumbersContinueAcrossTheCutoverInEveryZone(t *testing.T) {
	c := liveClock()
	for _, off := range zones {
		for _, hour := range []int{0, 6} {
			prev := int64(-2)
			for at := c.Cutover.Add(-3 * time.Hour); at.Before(c.Cutover.Add(96 * time.Hour)); at = at.Add(7 * time.Minute) {
				d := c.DayAtHourIn(at, hour, off)
				if d < prev {
					t.Fatalf("zone %v hour %d: the day number went back at %v: %d after %d", off, hour, at, d, prev)
				}
				prev = d
			}
			// after the switch the counter ticks once per real local day, at the local hour
			sw := c.cutoverFor(off)
			base := c.DayAtHourIn(sw.Add(time.Duration(hour)*time.Hour), hour, off)
			if hour > 0 && c.DayAtHourIn(sw.Add(time.Duration(hour)*time.Hour-time.Second), hour, off) != base-1 {
				t.Errorf("zone %v hour %d: the counter does not tick at local %02d:00", off, hour, hour)
			}
			if c.DayAtHourIn(sw.Add(time.Duration(hour)*time.Hour+24*time.Hour-time.Second), hour, off) != base ||
				c.DayAtHourIn(sw.Add(time.Duration(hour)*time.Hour+24*time.Hour), hour, off) != base+1 {
				t.Errorf("zone %v hour %d: a day is not 24 real hours", off, hour)
			}
			// local midnight really is local midnight
			if !sw.Equal(LocalMidnight(sw, off)) {
				t.Errorf("zone %v: the switch %v is not a local midnight", off, sw)
			}
		}
	}
}

// RealAtHourIn inverts DayAtHourIn on both sides of the switch.
func TestRealAtHourInvertsDayAtHour(t *testing.T) {
	c := liveClock()
	for _, off := range zones {
		for _, hour := range []int{0, 6} {
			for _, at := range []time.Time{c.Cutover.Add(-40 * 24 * time.Hour), c.Cutover.Add(-time.Hour), c.Cutover.Add(7 * time.Hour), c.Cutover.Add(100 * time.Hour)} {
				d := c.DayAtHourIn(at, hour, off)
				start := c.RealAtHourIn(d, hour, off)
				if start.After(at) || c.DayAtHourIn(start, hour, off) != d {
					t.Errorf("zone %v hour %d: day %d starts at %v (asked %v)", off, hour, d, start, at)
				}
				next := c.RealAtHourIn(d+1, hour, off)
				if !next.After(at) || c.DayAtHourIn(next, hour, off) != d+1 {
					t.Errorf("zone %v hour %d: the next boundary after %v is %v", off, hour, at, next)
				}
			}
		}
	}
}

// Two settlements in different zones turn their day at different instants (each at its
// own local midnight); their numbers are their own, so the shops of two zones never
// share a fence.
func TestZonesTurnTheDayAtTheirOwnMidnight(t *testing.T) {
	c := liveClock()
	day := c.Cutover.Add(30 * 24 * time.Hour)
	for _, off := range zones {
		mid := LocalMidnight(day.Add(12*time.Hour), off).Add(24 * time.Hour)
		if c.DayAtIn(mid, off) != c.DayAtIn(mid.Add(-time.Second), off)+1 {
			t.Errorf("zone %v does not turn its day at local midnight %v", off, mid)
		}
	}
}

func TestLegacyDaysAreRecognised(t *testing.T) {
	c := liveClock()
	if !c.IsLegacyDay(c.DayAt(c.Cutover.Add(-time.Hour))) || c.IsLegacyDay(c.DayAt(c.Cutover.Add(48*time.Hour))) {
		t.Error("the cut-over does not split legacy days from real ones")
	}
	if (Clock{Epoch: c.Epoch, Scale: 1}).IsLegacyDay(5) {
		t.Error("a clock with no cut-over has legacy days")
	}
}

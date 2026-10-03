package gametime

import (
	"testing"
	"time"
)

func TestClockCountsGameDaysFromTheEpoch(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Clock{Epoch: epoch, Scale: 60} // a game hour is a real minute: a game day is 24 real minutes
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := c.DayAt(epoch.Add(-time.Hour)); got != 0 {
		t.Errorf("before the epoch day = %d", got)
	}
	if got := c.DayAt(epoch.Add(23 * time.Minute)); got != 0 {
		t.Errorf("23 real minutes in = day %d, want 0", got)
	}
	if got := c.DayAt(epoch.Add(24 * time.Minute)); got != 1 {
		t.Errorf("24 real minutes in = day %d, want 1", got)
	}
}

func TestClockDayTicksAtTheMorningHour(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Clock{Epoch: epoch, Scale: 60}
	// 05:59 game time is 5 real minutes 59 s: the 06:00 boundary has not come.
	if got := c.DayAtHour(epoch.Add(5*time.Minute+59*time.Second), 6); got != -1 {
		t.Errorf("05:59 = %d, want -1", got)
	}
	if got := c.DayAtHour(epoch.Add(6*time.Minute), 6); got != 0 {
		t.Errorf("06:00 = %d, want 0", got)
	}
	// 05:00 the next day is still day 0, 06:00 the next day is day 1.
	if got := c.DayAtHour(epoch.Add(24*time.Minute+5*time.Minute), 6); got != 0 {
		t.Errorf("next day 05:00 = %d, want 0", got)
	}
	if got := c.DayAtHour(epoch.Add(24*time.Minute+6*time.Minute), 6); got != 1 {
		t.Errorf("next day 06:00 = %d, want 1", got)
	}
	if got := c.RealAtHour(1, 6); !got.Equal(epoch.Add(30 * time.Minute)) {
		t.Errorf("boundary of day 1 = %s", got)
	}
}

func TestClockWithoutAnEpochIsRefused(t *testing.T) {
	if (Clock{Scale: 60}).Validate() == nil {
		t.Fatal("a clock with no epoch was accepted")
	}
}

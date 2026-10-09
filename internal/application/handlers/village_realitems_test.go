package handlers

import (
	"testing"
	"time"
)

func TestRealItemGrace(t *testing.T) {
	from := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	r := RealItemRules{From: from, GraceDays: 7, BareHandsBPS: 6000}
	if want := from.AddDate(0, 0, 7); !r.GraceUntil().Equal(want) {
		t.Errorf("the grace ends %v, want %v", r.GraceUntil(), want)
	}
	for _, c := range []struct {
		at   time.Time
		want bool
	}{
		{from.Add(-time.Hour), true}, // before the rule date the older goods serve too
		{from.AddDate(0, 0, 6), true},
		{from.AddDate(0, 0, 7), false},
	} {
		if got := r.InGrace(c.at); got != c.want {
			t.Errorf("in the grace at %v: %v, want %v", c.at, got, c.want)
		}
	}
	if (RealItemRules{GraceDays: 7}).InGrace(from) || (RealItemRules{From: from}).InGrace(from) {
		t.Error("no rule date or no days means no grace")
	}
}

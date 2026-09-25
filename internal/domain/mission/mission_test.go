package mission

import (
	"errors"
	"testing"
	"time"
)

func sample() Mission {
	return Mission{
		Code: "errands", Board: "city_hall", MinLevel: 1,
		Objectives: []Objective{{Kind: Work, Count: 2}, {Kind: Buy, Target: "bandage", Count: 3}, {Kind: Deliver, Target: "bread", Count: 5}},
		Reward:     Reward{Cash: 400, XP: 40, Items: []ItemReward{{Item: "sandwich", Qty: 2}}},
	}
}

func TestValidate(t *testing.T) {
	if err := sample().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := sample()
	bad.Objectives[2].Target = ""
	if err := bad.Validate(); !errors.Is(err, ErrInvalidMission) {
		t.Errorf("a delivery of nothing accepted: %v", err)
	}
	once := sample()
	once.Cooldown = time.Hour
	if once.Validate() == nil {
		t.Error("a cooldown on a mission taken once accepted")
	}
	none := sample()
	none.Reward = Reward{}
	if none.Validate() == nil {
		t.Error("a mission rewarding nothing accepted")
	}
}

func TestAdvance(t *testing.T) {
	m := sample()
	p, moved := Advance(m.Objectives, nil, Event{Kind: Work, Targets: []string{"retail", "commerce"}})
	if !moved || p[0] != 1 {
		t.Fatalf("a shift moved nothing: %v", p)
	}
	p, moved = Advance(m.Objectives, p, Event{Kind: Buy, Targets: []string{"bread"}, Qty: 4})
	if moved {
		t.Errorf("bread moved the bandage objective: %v", p)
	}
	p, _ = Advance(m.Objectives, p, Event{Kind: Buy, Targets: []string{"bandage", "medicine"}, Qty: 10})
	if p[1] != 3 {
		t.Errorf("bandages = %d, want capped at 3", p[1])
	}
	// A delivery is never counted from an event.
	if _, moved := Advance(m.Objectives, p, Event{Kind: Deliver, Targets: []string{"bread"}, Qty: 5}); moved {
		t.Error("an event delivered goods")
	}
	p, _ = Advance(m.Objectives, p, Event{Kind: Work})
	if Done(m.Objectives, p) {
		t.Error("done before the delivery")
	}
	take := Deliverable(m.Objectives, p, "bread", 9)
	if take[2] != 5 {
		t.Errorf("hand-in takes %v, want 5 bread", take)
	}
	p[2] += take[2]
	if !Done(m.Objectives, p) {
		t.Errorf("not done at %v", p)
	}
}

func TestAvailable(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	wait := func(d time.Duration) time.Duration { return d / 60 }
	m := sample()
	if why, _ := m.Available(History{Level: 0}, now, wait); why != BlockedLevel {
		t.Errorf("why = %q, want level", why)
	}
	h := History{Level: 3, Completed: map[string]time.Time{"errands": now.Add(-time.Hour)}}
	if why, _ := m.Available(h, now, wait); why != BlockedDone {
		t.Errorf("why = %q, want done", why)
	}
	m.Repeatable, m.Cooldown = true, 24*time.Hour // 24 real minutes at scale 60
	h.Completed["errands"] = now.Add(-10 * time.Minute)
	why, left := m.Available(h, now, wait)
	if why != BlockedCooldown || left != 14*time.Minute {
		t.Errorf("why = %q, left %s; want cooldown, 14m", why, left)
	}
	h.Completed["errands"] = now.Add(-30 * time.Minute)
	if why, _ := m.Available(h, now, wait); why != BlockedNone {
		t.Errorf("why = %q after the cooldown", why)
	}
	m.Requires = []string{"intro"}
	if why, _ := m.Available(h, now, wait); why != BlockedRequires {
		t.Errorf("why = %q, want requires", why)
	}
}

func TestCapCash(t *testing.T) {
	cases := []struct{ cash, player, economy, paid, withheld int64 }{
		{400, 1000, 1000, 400, 0},
		{400, 150, 1000, 150, 250},
		{400, 1000, 0, 0, 400},
		{400, -5, 1000, 0, 400},
		{0, 100, 100, 0, 0},
	}
	for _, c := range cases {
		p, w := CapCash(c.cash, c.player, c.economy)
		if p != c.paid || w != c.withheld {
			t.Errorf("CapCash(%d, %d, %d) = %d, %d", c.cash, c.player, c.economy, p, w)
		}
	}
}

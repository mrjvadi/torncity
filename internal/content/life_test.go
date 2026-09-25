package content

import (
	"errors"
	"testing"
)

func TestShippedLife(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := snap.Life()
	if !ok {
		t.Fatal("the shipped content has no life section")
	}
	if len(d.Ranks.Ladder) < 3 || len(d.Avatars) == 0 || len(d.Sleep.Spots) == 0 {
		t.Fatalf("the shipped life is thin: %+v", d)
	}
	if _, ok := d.Spot("hostel"); !ok {
		t.Fatal("no hostel")
	}
	if d.Leaderboards.PeriodDuration() <= 0 || d.PhotoTTLDuration() <= 0 {
		t.Fatal("durations do not parse")
	}
	if e := d.Event(LifeJailed); e.Stress <= 0 {
		t.Fatalf("jail does not stress: %+v", e)
	}
}

func TestLifeValidation(t *testing.T) {
	for name, breakIt := range map[string]func(d *LifeDef){
		"ranks out of order":   func(d *LifeDef) { d.Ranks.Ladder[1].Min = -1 },
		"unknown event":        func(d *LifeDef) { d.Events = append(d.Events, LifeEventDef{Event: "fishing"}) },
		"spot at no place":     func(d *LifeDef) { d.Sleep.Spots[0].Place = "moon" },
		"reserved avatar":      func(d *LifeDef) { d.Avatars[0].Code = AvatarPhoto },
		"bad leaderboard size": func(d *LifeDef) { d.Leaderboards.Size = 0 },
		"penalty too big":      func(d *LifeDef) { d.Needs.MaxBPS = 9500 },
		"no rest":              func(d *LifeDef) { d.Sleep.Spots[0].Rest = 0 },
	} {
		p := shippedPack(t)
		breakIt(&p.Life[0])
		if err := p.Validate(); !errors.Is(err, ErrInvalidLifeContent) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

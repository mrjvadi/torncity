package inventory

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/item"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func bread() Item {
	return Item{Code: "bread", Form: Stack, Tradeable: true, Stealable: true,
		Effects:  []item.Effect{{Target: TargetEnergy, Op: item.EffectAdd, Value: 15}},
		Cooldown: 30 * time.Minute, CooldownGroup: "food"}
}

func TestUse(t *testing.T) {
	v := Vitals{Energy: 50, MaxEnergy: 100, Health: 100, MaxHealth: 100, MaxHappiness: 100, MaxNerve: 20}
	after, ready, err := Use(bread(), v, time.Time{}, now, 60)
	if err != nil || after.Energy != 65 || !ready.Equal(now.Add(30*time.Second)) {
		t.Fatalf("Use = %+v, %s, %v", after, ready, err)
	}
	// Twice inside the cooldown: refused, nothing changes.
	if _, _, err := Use(bread(), after, now, now.Add(10*time.Second), 60); !errors.Is(err, ErrCoolingDown) {
		t.Fatalf("a second bite = %v, want cooling down", err)
	}
	// Past it, a full bar caps the gain; a bar already full refuses.
	v.Energy = 95
	after, _, err = Use(bread(), v, now, now.Add(time.Minute), 60)
	if err != nil || after.Energy != 100 {
		t.Fatalf("near full = %+v, %v", after, err)
	}
	if _, _, err := Use(bread(), after, time.Time{}, now, 60); !errors.Is(err, ErrNoEffect) {
		t.Fatalf("on a full stomach = %v, want no effect", err)
	}
	if _, _, err := Use(Item{Code: "rock", Form: Stack}, v, time.Time{}, now, 60); !errors.Is(err, ErrNotUsable) {
		t.Fatalf("using a rock = %v", err)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(bread()); err != nil {
		t.Fatal(err)
	}
	bad := bread()
	bad.Form = "loose"
	bad.Effects = append(bad.Effects, item.Effect{Target: "charisma", Op: item.EffectAdd, Value: 1})
	bad.Durability = 5
	bad.Gear = &GearDef{}
	if err := Validate(bad); !errors.Is(err, ErrInvalidItem) {
		t.Fatalf("Validate(bad) = %v", err)
	}
}

func TestGearForWearsAndCaps(t *testing.T) {
	items := map[string]Item{
		"lockpick_set": {Code: "lockpick_set", Form: Unique, Durability: 10,
			Gear: &GearDef{Categories: []string{"burglary"}, Gear: crime.Gear{SuccessBPS: 1500}, Wear: 1}},
		"gloves": {Code: "gloves", Form: Stack,
			Gear: &GearDef{Categories: []string{"burglary"}, Gear: crime.Gear{SolveBPS: -1000}, Wear: 1, Confiscated: true}},
		"mask": {Code: "mask", Form: Unique,
			Gear: &GearDef{Crimes: []string{"pickpocketing"}, Gear: crime.Gear{WitnessBPS: -2000}}},
	}
	carried := []Holding{
		{Item: "lockpick_set", Instance: "a", Qty: 1, UsesLeft: 1},
		{Item: "lockpick_set", Instance: "b", Qty: 1, UsesLeft: 7},
		{Item: "gloves", Qty: 3},
		{Item: "mask", Instance: "m", Qty: 1},
	}
	caps := crime.GearCaps{SuccessBPS: 1000, SolveBPS: 3000, WitnessBPS: 3000}
	g, wear := GearFor(items, carried, "home_burglary", "burglary", caps)
	if g.SuccessBPS != 1000 || g.SolveBPS != -1000 || g.WitnessBPS != 0 {
		t.Fatalf("gear = %+v (the lockpick capped, the mask not for burglary)", g)
	}
	if len(wear) != 2 || wear[1].Holding.Instance != "b" || wear[1].Uses != 1 || wear[1].Breaks || wear[0].Holding.Item != "gloves" {
		t.Fatalf("wear = %+v; want the better lockpick and one pair of gloves", wear)
	}
	if got := Confiscated(items, carried); len(got) != 1 || got[0].Item != "gloves" {
		t.Errorf("confiscated = %+v", got)
	}
}

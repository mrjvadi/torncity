package crime

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// scriptDice rolls a fixed script, then zeros.
type scriptDice struct{ rolls []int64 }

func (s *scriptDice) Roll(n int64) int64 {
	if len(s.rolls) == 0 {
		return 0
	}
	v := s.rolls[0]
	s.rolls = s.rolls[1:]
	if v >= n {
		return n - 1
	}
	return v
}

func gearCrime() Crime {
	return Crime{
		Code: "home_burglary", Category: "burglary", Targets: []TargetKind{TargetNPC},
		NerveCost: 5,
		Success:   SuccessModel{BaseChanceBPS: 4000, AwarenessWeightBPS: 100, TargetAwareness: 5},
		Reward: Reward{MinCash: money.FromMinor(100), MaxCash: money.FromMinor(1000), XP: 10,
			Loot: []LootEntry{{Item: "jewellery", ChanceBPS: 5000, MinQty: 1, MaxQty: 2, MinQuality: 30, MaxQuality: 60}}},
		Failure: Failure{CatchChanceBPS: 5000, JailMin: time.Hour, JailMax: 2 * time.Hour},
	}
}

func policy() JusticePolicy { return JusticePolicy{JailTermPct: 100, FinePct: 100} }

// The same roll succeeds with a lockpick and fails without one.
func TestGearDecidesAnAttemptDeterministically(t *testing.T) {
	c := gearCrime()
	caps := GearCaps{SuccessBPS: 2500, CatchBPS: 2500, WitnessBPS: 5000, SolveBPS: 3000, RewardBPS: 5000, Nerve: 3}
	bare := c.SuccessChance(Situation{Victim: TargetNPC})
	kit := Combine(caps, Gear{SuccessBPS: 1500}, Gear{SuccessBPS: 1500})
	with := c.SuccessChance(Situation{Victim: TargetNPC, Gear: kit})
	if bare != 3500 || with != 6000 {
		t.Fatalf("chance bare %d, with gear %d; want 3500 and 6000 (two tools, capped at 2500)", bare, with)
	}
	roll := int64(4000) // between the two chances
	a := Attempt{Crime: c, Victim: TargetNPC, Chance: bare, NPCAllowance: money.FromMinor(1_000_000), Policy: policy()}
	out, err := Resolve(a, &scriptDice{rolls: []int64{roll, 9999}})
	if err != nil || out.Result == Succeeded {
		t.Fatalf("without gear = %+v, %v; want a failure", out, err)
	}
	a.Chance, a.Gear = with, kit
	out, err = Resolve(a, &scriptDice{rolls: []int64{roll, 400, 0, 1, 10}})
	if err != nil || out.Result != Succeeded {
		t.Fatalf("with gear = %+v, %v; want a success", out, err)
	}
	if out.Take.Minor() != 500 || len(out.Loot) != 1 || out.Loot[0] != (LootDrop{Item: "jewellery", Qty: 2, Quality: 40}) {
		t.Errorf("take and loot = %s, %+v", out.Take, out.Loot)
	}
}

// Caps hold: no pile of tools lifts a chance past the ceiling or a cap.
func TestGearCapsHold(t *testing.T) {
	caps := GearCaps{SuccessBPS: 2000, CatchBPS: 1000, Nerve: 2}
	g := Combine(caps, Gear{SuccessBPS: 9000, CatchBPS: -9000, Nerve: -10}, Gear{SuccessBPS: 9000})
	if g.SuccessBPS != 2000 || g.CatchBPS != -1000 || g.Nerve != -2 {
		t.Fatalf("combined = %+v", g)
	}
	if g.NerveCost(1) != 1 {
		t.Errorf("nerve cost fell below one: %d", g.NerveCost(1))
	}
	c := gearCrime()
	c.Success.BaseChanceBPS = 9000
	if got := c.SuccessChance(Situation{Victim: TargetNPC, Gear: g}); got != ChanceCeilingBPS {
		t.Errorf("chance with every tool = %d, want the ceiling %d", got, ChanceCeilingBPS)
	}
	if err := (GearCaps{SuccessBPS: -1}).Validate(); err == nil {
		t.Error("a negative cap is accepted")
	}
}

func TestCooldownLeft(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	last := now.Add(-30 * time.Second)
	// A 60-minute game cooldown at scale 60 is a real minute: 30s left.
	if got := CooldownLeft(last, time.Hour, time.Time{}, 0, 60, now); got != 30*time.Second {
		t.Errorf("left = %s, want 30s", got)
	}
	// The category's longer cooldown wins.
	if got := CooldownLeft(last, time.Hour, now.Add(-10*time.Second), 2*time.Hour, 60, now); got != 110*time.Second {
		t.Errorf("left = %s, want 110s", got)
	}
	if got := CooldownLeft(time.Time{}, time.Hour, time.Time{}, time.Hour, 60, now); got != 0 {
		t.Errorf("never tried, left = %s", got)
	}
}

// A player victim may lose an item: the roll after the witness picks it.
func TestStealItemFromAPlayer(t *testing.T) {
	c := Crime{Code: "pickpocketing", Category: "petty_theft", Targets: []TargetKind{TargetPlayer}, NerveCost: 2,
		Success: SuccessModel{BaseChanceBPS: 9000},
		Reward:  Reward{ShareBPS: 1000, MinTake: money.FromMinor(10), MaxTake: money.FromMinor(100), StealItemBPS: 3000},
		Failure: Failure{}}
	a := Attempt{Crime: c, Victim: TargetPlayer, Chance: 9000, VictimCash: money.FromMinor(500), VictimItems: 3, Policy: policy()}
	out, err := Resolve(a, &scriptDice{rolls: []int64{0, 100, 2}})
	if err != nil || out.StolenItem != 2 {
		t.Fatalf("outcome = %+v, %v; want the third item taken", out, err)
	}
	out, err = Resolve(a, &scriptDice{rolls: []int64{0, 5000}})
	if err != nil || out.StolenItem != -1 {
		t.Fatalf("outcome = %+v, %v; want nothing taken", out, err)
	}
}

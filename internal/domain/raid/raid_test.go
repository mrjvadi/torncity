package raid

import (
	"testing"
	"time"
)

func rules() Rules {
	return Rules{
		MinLead: 2 * time.Hour, MaxLead: 48 * time.Hour, Window: 30 * time.Minute, Cooldown: 72 * time.Hour, MaxPerWeek: 3,
		Shield: 48 * time.Hour, ShieldLootCutBPS: 5000, FoundingGrace: 7 * 24 * time.Hour, GraceRatio: 2, TargetRatioMax: 5, MinTargetValue: 2000,
		LootTreasuryBPS: 300, LootStackBPS: 1000, LootPersonalBPS: 500, DamageMaxBPS: 2000,
		DeathCashBPS: 2000, DeathGoodsBPS: 2500, GearWearBPS: 2000, LossCapBPS: 1500, FallCooldown: 12 * time.Hour,
		StealthBPSPerStep: 1500, StealthSteps: 3, StealthFloorBPS: 4000, NightPenaltyBPS: 1000, BeaconHop: 3 * time.Minute,
	}
}

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func filing() Filing {
	return Filing{Now: t0, Start: t0.Add(6 * time.Hour), Attacker: "a", Defender: "d", AttackerResidents: 5, DefenderResidents: 5,
		DefenderFoundedAt: t0.Add(-30 * 24 * time.Hour), DefenderExposed: 10_000}
}

func TestACleanFilingIsAllowed(t *testing.T) {
	if got := rules().Check(filing()); got != Allowed {
		t.Fatalf("refused: %s", got)
	}
}

func TestTheLeadHasBounds(t *testing.T) {
	f := filing()
	f.Start = t0.Add(time.Hour)
	if got := rules().Check(f); got != LeadTooShort {
		t.Errorf("short: %s", got)
	}
	f.Start = t0.Add(49 * time.Hour)
	if got := rules().Check(f); got != LeadTooLong {
		t.Errorf("long: %s", got)
	}
}

func TestTheCooldownBetweenAPairIs72Hours(t *testing.T) {
	f := filing()
	f.History = []Past{{Attacker: "a", Defender: "d", FiledAt: t0.Add(-71 * time.Hour)}}
	if got := rules().Check(f); got != Cooldown {
		t.Fatalf("71 hours after: %s", got)
	}
	f.History[0].FiledAt = t0.Add(-73 * time.Hour)
	if got := rules().Check(f); got != Allowed {
		t.Fatalf("73 hours after: %s", got)
	}
}

func TestAtMostThreeRaidsAWeekOnOneDefenderFromAnyone(t *testing.T) {
	f := filing()
	for i, who := range []string{"x", "y", "z"} {
		f.History = append(f.History, Past{Attacker: who, Defender: "d", FiledAt: t0.Add(-time.Duration(i+1) * 24 * time.Hour)})
	}
	if got := rules().Check(f); got != WeeklyLimit {
		t.Fatalf("the fourth in a week: %s", got)
	}
	f.History[2].FiledAt = t0.Add(-8 * 24 * time.Hour) // the third fell out of the week
	if got := rules().Check(f); got != Allowed {
		t.Fatalf("only two in the week: %s", got)
	}
}

func TestAShieldFollowsARaidThatTookLoot(t *testing.T) {
	r := rules()
	r.Cooldown = time.Hour
	f := filing()
	f.History = []Past{{Attacker: "a", Defender: "d", FiledAt: t0.Add(-24 * time.Hour), Looted: true, ResolvedAt: t0.Add(-20 * time.Hour)}}
	if got := r.Check(f); got != Shielded {
		t.Fatalf("20 hours after a loss: %s", got)
	}
}

func TestTheFoundingGraceBlocksAMuchLargerAttackerByTimeNotPresence(t *testing.T) {
	f := filing()
	f.DefenderFoundedAt = t0.Add(-3 * 24 * time.Hour)
	f.AttackerResidents, f.DefenderResidents = 10, 4
	if got := rules().Check(f); got != FoundingGrace {
		t.Fatalf("3 days old: %s", got)
	}
	f.DefenderFoundedAt = t0.Add(-8 * 24 * time.Hour)
	if got := rules().Check(f); got != Allowed {
		t.Fatalf("8 days old: %s", got)
	}
}

func TestANearEmptyDefenderIsNotATarget(t *testing.T) {
	f := filing()
	f.DefenderExposed = 1999
	if got := rules().Check(f); got != NothingToTake {
		t.Fatalf("%s", got)
	}
}

func TestLootIsCappedByShareCarryAndShield(t *testing.T) {
	r := rules()
	e := Exposed{Treasury: 100_000, Stock: map[string]int64{"firewood": 1000, "grain": 500}, Carry: 400}
	l := r.LootOf(e, false, 1000, 1000)
	if l.Cash != 3000 {
		t.Errorf("treasury 3 percent: %d", l.Cash)
	}
	if l.Goods["firewood"] != 100 || l.Goods["grain"] != 50 {
		t.Errorf("a tenth of each stack: %v", l.Goods)
	}
	// carry capacity: only 60 units can be hauled, taken in item order
	e.Carry = 60
	l = r.LootOf(e, false, 1000, 1000)
	if l.Units() != 60 {
		t.Errorf("the raiders carry 60: %v", l.Goods)
	}
	// after a loss, half
	l = r.LootOf(Exposed{Treasury: 100_000, Stock: map[string]int64{"firewood": 1000}, Carry: 1000}, true, 1000, 1000)
	if l.Cash != 1500 || l.Goods["firewood"] != 50 {
		t.Errorf("shielded: %d %v", l.Cash, l.Goods)
	}
	// a bully gains nothing
	l = r.LootOf(e, false, 6000, 1000)
	if l.Cash != 0 || l.Units() != 0 {
		t.Errorf("proportionality: %v", l)
	}
}

func TestAFallNeverCostsMoreThanTheCapOfNetAssets(t *testing.T) {
	r := rules()
	// carried: 10_000 cash and 100 firewood worth 10 each; net assets 10_000, so the cap is 1500
	c := Carried{Cash: 10_000, Goods: map[string]int64{"firewood": 100}, Value: map[string]int64{"firewood": 10}, NetAssets: 10_000}
	l := r.FallLoss(c, false)
	if !l.Capped {
		t.Fatal("the loss should have been capped")
	}
	if w := l.Worth(c.Value); w > 1500 {
		t.Fatalf("the loss is worth %d, over the cap of 1500", w)
	}
	// with enough assets nothing is capped: 20 percent cash, 25 percent goods
	c.NetAssets = 1_000_000
	l = r.FallLoss(c, false)
	if l.Capped || l.Cash != 2000 || l.Goods["firewood"] != 25 {
		t.Fatalf("uncapped: %+v", l)
	}
	// inside the fall cooldown nothing is lost
	if l = r.FallLoss(c, true); l.Cash != 0 || len(l.Goods) != 0 {
		t.Fatalf("cooling: %+v", l)
	}
	// nothing carried, nothing lost: bank, lots and skills are not in Carried
	if l = r.FallLoss(Carried{NetAssets: 5000}, false); l.Cash != 0 || len(l.Goods) != 0 {
		t.Fatalf("empty hands: %+v", l)
	}
}

func TestSeveralDetectorsCombineAsOneMinusTheProductOfMisses(t *testing.T) {
	if got := Combine([]int64{6000, 8000}); got != 9200 {
		t.Errorf("1 - 0.4*0.2 = 0.92: %d", got)
	}
	if got := Combine(nil); got != 0 {
		t.Errorf("none: %d", got)
	}
}

func TestARollIsTheSameOnEveryReplica(t *testing.T) {
	if Roll(42, "raid:detector") != Roll(42, "raid:detector") {
		t.Fatal("not deterministic")
	}
	if Roll(42, "a") == Roll(43, "a") && Roll(42, "b") == Roll(43, "b") {
		t.Error("the seed does not matter")
	}
	for i := 0; i < 1000; i++ {
		if r := Roll(int64(i), "x"); r < 0 || r >= BPS {
			t.Fatalf("out of range: %d", r)
		}
	}
}

func TestAnOffDutyDetectorSeesNothingAndConcealmentLowersTheChanceToAFloor(t *testing.T) {
	r := rules()
	if r.Chance(8000, false, false, 0) != 0 {
		t.Error("off duty")
	}
	if got := r.Chance(8000, true, false, 0); got != 8000 {
		t.Errorf("full: %d", got)
	}
	if got := r.Chance(8000, true, true, 0); got != 7000 {
		t.Errorf("night: %d", got)
	}
	if got := r.Chance(8000, true, false, 3); got >= 8000 || got < 3200 {
		t.Errorf("three steps: %d", got)
	}
	if got := r.Chance(8000, true, true, 10); got < 3200 {
		t.Errorf("never under the floor: %d", got)
	}
}

func TestALeadIsSightOverSpeed(t *testing.T) {
	if got := Lead(8000, 5); got != 96*time.Minute {
		t.Errorf("8 km on foot: %v", got)
	}
	if Lead(0, 5) != 0 || Lead(8000, 0) != 0 {
		t.Error("nothing seen")
	}
}

func chain() []Beacon {
	return []Beacon{
		{ID: "far", X: 0, Y: 0, Held: true},
		{ID: "mid", X: 50_000, Y: 0, Held: true},
		{ID: "home", X: 100_000, Y: 0, Held: true},
	}
}

func TestAStaffedAndFuelledChainOfBeaconsRelaysASighting(t *testing.T) {
	r := rules()
	hops := Reach(chain(), "far", "home", 60_000, nil)
	if hops != 2 {
		t.Fatalf("far to home through mid is two hops: %d", hops)
	}
	lead := 3 * time.Hour
	if got := r.Warning(lead, hops); got != 3*time.Hour-6*time.Minute {
		t.Errorf("the warning is late by two hops: %v", got)
	}
}

func TestAnUnheldBeaconBreaksTheChain(t *testing.T) {
	b := chain()
	b[1].Held = false // the keeper is missing or the fire has no wood
	if got := Reach(b, "far", "home", 60_000, nil); got != -1 {
		t.Fatalf("a dark tower breaks the chain: %d", got)
	}
	// and a tower out of range or out of sight is no link
	b[1].Held = true
	if got := Reach(b, "far", "home", 40_000, nil); got != -1 {
		t.Fatalf("out of relay range: %d", got)
	}
	if got := Reach(b, "far", "home", 60_000, func(a, b Beacon) bool { return false }); got != -1 {
		t.Fatalf("no line of sight: %d", got)
	}
}

func TestBearingsAndBins(t *testing.T) {
	if b := Bearing(0, 0, 1, 0); b < 359.9 && b > 0.1 {
		t.Errorf("north: %f", b)
	}
	if b := Bearing(0, 0, 0, 1); b < 89.9 || b > 90.1 {
		t.Errorf("east: %f", b)
	}
	if BinOf(90, 256) != 64 || BinOf(359.9, 256) != 0 {
		t.Errorf("bins: %d %d", BinOf(90, 256), BinOf(359.9, 256))
	}
	sight := make([]int32, 256)
	sight[64] = 8000
	if got := SightAlong(sight, 90, 5000); got != 5000 {
		t.Errorf("never beyond the raiders: %d", got)
	}
	if got := SightAlong(sight, 90, 20000); got != 8000 {
		t.Errorf("sight: %d", got)
	}
}

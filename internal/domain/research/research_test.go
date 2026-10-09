package research

import (
	"testing"
	"time"
)

func testRules() Rules {
	return Rules{FreeSlots: 1, SpeedFloorBPS: 10_000, ScholarFloorBPS: 500, SkillBPSPerLevel: 100, ScholarCapBPS: 2_000, NPCScholarLevel: 5,
		LiteracyBonusBPS: 2_000, CatchUpBPS: 4_000, EraBaseDepth: 4, EraGrace: 0, EraShareBPS: 1_600, AheadPerStepBPS: 3_000, AheadCapBPS: 10_000,
		SharePerPartnerBPS: 1_000, ShareCapBPS: 5_000, BreakthroughNeedPerDepth: 100, BreakthroughMaxBPS: 4_000}
}

func TestDepthsFollowThePrerequisitesAndCapabilities(t *testing.T) {
	d, err := Depths([]Node{
		{Code: "a"}, {Code: "b", Requires: []string{"a"}}, {Code: "c", Requires: []string{"b"}},
		{Code: "irr1", Provides: []string{"water"}}, {Code: "irr2", Provides: []string{"water"}, Requires: []string{"c"}},
		{Code: "farm", RequiresCapability: []string{"water"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d["a"] != 1 || d["c"] != 3 || d["irr2"] != 4 {
		t.Errorf("depths %v", d)
	}
	// a capability is met by its shallowest provider
	if d["farm"] != 2 {
		t.Errorf("farm depth %d, want 2 (irr1 satisfies the capability)", d["farm"])
	}
	if _, err := Depths([]Node{{Code: "x", Requires: []string{"y"}}, {Code: "y", Requires: []string{"x"}}}); err == nil {
		t.Error("a cycle was accepted")
	}
}

func TestTheWorldsFrontierAndTheAheadFactor(t *testing.T) {
	r := testRules()
	depths := map[string]int{"a": 1, "e": 5, "f": 6, "g": 7}
	share := map[string]int64{"e": 2_000, "f": 500}
	f := Frontier(depths, func(c string) int64 { return share[c] }, r)
	if f != 5 {
		t.Errorf("the frontier is %d, want 5 (e is held by 20 percent, f by 5)", f)
	}
	if got := Frontier(depths, func(string) int64 { return 0 }, r); got != 4 {
		t.Errorf("with nobody ahead the frontier is the base %d", got)
	}
	if AheadBPS(4, 4, r) != BPS || AheadBPS(5, 5, r) != BPS {
		t.Error("an item at the frontier costs the base")
	}
	if got := AheadBPS(6, 5, r); got != 13_000 {
		t.Errorf("one step ahead is %d, want 13000", got)
	}
	if got := AheadBPS(20, 5, r); got != 20_000 {
		t.Errorf("the cap is %d, want 20000", got)
	}
}

func TestScholarsAddAndNeverSlowDown(t *testing.T) {
	r := testRules()
	if ScholarBPS(0, r) != 500 || ScholarBPS(5, r) != 1_000 || ScholarBPS(100, r) != 2_000 || ScholarBPS(-3, r) != 500 {
		t.Errorf("scholar bps %d %d %d", ScholarBPS(0, r), ScholarBPS(5, r), ScholarBPS(100, r))
	}
	free := Slots(nil, r)
	if len(free) != 1 || free[0].Ref != FreeRef || Capacity(free) != 1 {
		t.Errorf("the free slot: %+v", free)
	}
	lab := Building{ID: "lab", Slots: 2, MinStaff: 2, BonusBPS: 1_000, Skills: []int{3}}
	if got := Capacity(Slots([]Building{lab}, r)); got != 1 {
		t.Errorf("an understaffed lab gives %d slots, want only the free one", got)
	}
	lab.Skills = []int{3, 8}
	slots := Slots([]Building{lab}, r)
	if Capacity(slots) != 3 || slots[1].StaffBPS != ScholarBPS(3, r)+ScholarBPS(8, r) {
		t.Errorf("a staffed lab: %+v", slots)
	}
	// more scholars never slow a project down
	one := Price(Input{BaseCost: 1000, BaseTime: 10 * time.Hour, Depth: 2, Frontier: 4, Slot: Slot{StaffBPS: 800}}, r)
	two := Price(Input{BaseCost: 1000, BaseTime: 10 * time.Hour, Depth: 2, Frontier: 4, Slot: Slot{StaffBPS: 1_600}}, r)
	if two.Duration > one.Duration {
		t.Errorf("two scholars (%v) are slower than one (%v)", two.Duration, one.Duration)
	}
}

func TestThePriceOfAProject(t *testing.T) {
	r := testRules()
	// the free slot, nothing else: exactly the old project
	base := Price(Input{BaseCost: 4000, BaseTime: 48 * time.Hour, Depth: 2, Frontier: 4, Slot: Slot{Ref: FreeRef}}, r)
	if base.Cost != 4000 || base.Duration != 48*time.Hour || base.SpeedBPS != BPS || base.AheadBPS != BPS {
		t.Errorf("the free slot must keep the old price and pace: %+v", base)
	}
	// ahead of the world by one step: 30 percent more cost and time
	ahead := Price(Input{BaseCost: 4000, BaseTime: 48 * time.Hour, Depth: 6, Frontier: 5, Slot: Slot{Ref: FreeRef}}, r)
	if ahead.Cost != 5200 || ahead.Effort != 62*time.Hour+24*time.Minute {
		t.Errorf("one step ahead: %+v", ahead)
	}
	// breakthrough: half the points of a depth-2 item's need (200) gives half the 40 percent
	disc := Price(Input{BaseCost: 4000, BaseTime: 48 * time.Hour, Depth: 2, Frontier: 4, Slot: Slot{Ref: FreeRef}, Experience: 100}, r)
	if disc.DiscountBPS != 2_000 || disc.Cost != 3200 {
		t.Errorf("a half-earned breakthrough: %+v", disc)
	}
	// a staffed lab, literate, with the world holding the item and two partners: the speeds add
	fast := Price(Input{BaseCost: 4000, BaseTime: 48 * time.Hour, Depth: 2, Frontier: 4, Slot: Slot{BonusBPS: 1_000, StaffBPS: 2_000}, Running: 0,
		LiteracyBPS: 5_000, HoldersShareBPS: 5_000, SharePartners: 2}, r)
	// 10000 + 1000 + 2000 + 5000*2000/10000 (1000) + 5000*4000/10000 (2000) + 2000
	if fast.SpeedBPS != 18_000 || fast.ShareBPS != 2_000 || fast.CatchUpBPS != 2_000 {
		t.Errorf("speed parts: %+v", fast)
	}
	if want := 48 * time.Hour * 10_000 / 18_000; fast.Duration != want.Round(time.Second) && fast.Duration != want {
		t.Errorf("duration %v, want %v", fast.Duration, want)
	}
	// the staff's share is split with the projects already running in the building
	shared := Price(Input{BaseCost: 4000, BaseTime: 48 * time.Hour, Depth: 2, Frontier: 4, Slot: Slot{StaffBPS: 2_000}, Running: 1}, r)
	if shared.SpeedBPS != 11_000 {
		t.Errorf("the second project in a building shares its staff: %d", shared.SpeedBPS)
	}
	// the sharing bonus is capped at 50 percent
	cap := Price(Input{BaseCost: 1000, BaseTime: time.Hour, Depth: 1, Frontier: 4, Slot: Slot{}, SharePartners: 30}, r)
	if cap.ShareBPS != 5_000 {
		t.Errorf("share %d, want the cap 5000", cap.ShareBPS)
	}
	// a price is never free and a duration never zero for a project with a time
	tiny := Price(Input{BaseCost: 1, BaseTime: time.Second, Depth: 1, Frontier: 4, Slot: Slot{BonusBPS: 9_000}, Experience: 1_000_000}, r)
	if tiny.Cost < 1 || tiny.Duration < time.Second {
		t.Errorf("tiny %+v", tiny)
	}
}

func TestPricingIsDeterministicAndOverflowSafe(t *testing.T) {
	r := testRules()
	in := Input{BaseCost: 1_000_000_000_000, BaseTime: 365 * 24 * time.Hour, Depth: 9, Frontier: 4, Slot: Slot{StaffBPS: 50_000}, Experience: 5}
	a, b := Price(in, r), Price(in, r)
	if a != b {
		t.Error("not deterministic")
	}
	if a.Cost <= 0 || a.Duration <= 0 || a.AheadBPS != 20_000 {
		t.Errorf("large inputs: %+v", a)
	}
}

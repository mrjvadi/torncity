package intel

import (
	"strconv"
	"testing"
	"time"
)

func rules() Rules {
	return Rules{BaseChanceBPS: 5000, SkillBPSPerLevel: 200, ChanceFloorBPS: 500, ChanceCapBPS: 9000, CoverCapDays: 30, CoverBPSPerDay: 100,
		CounterPostBPS: 1500, CounterVetBPS: 2000, CounterSmallBPS: 1500, SmallResidents: 30, DetectBPS: 3000, AttributionBPS: 4000, ExpelDays: 14,
		ReportTTL: 14 * 24 * time.Hour}
}

func sab() SabotageRules {
	return SabotageRules{DelayHours: 12, SpoilBPS: 500, BlindBPS: 3000, WeeklyLimit: 2, PerTargetDays: 7, SkipDays: 30, Cost: 400}
}

var now = time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

func TestCoverGrowsByTheDayUpToACap(t *testing.T) {
	r := rules()
	if r.Cover(0) != 0 || r.Cover(10) != 1000 || r.Cover(30) != 3000 || r.Cover(300) != 3000 {
		t.Fatal("cover")
	}
}

func TestCounterIntelligenceAddsPostsVettingAndSmallness(t *testing.T) {
	r := rules()
	if r.Counter(0, false, 100) != 0 || r.Counter(2, false, 100) != 3000 || r.Counter(1, true, 10) != 1500+2000+1500 {
		t.Fatal("counter")
	}
}

func TestTheChanceIsNeverCertainAndNeverHopeless(t *testing.T) {
	r := rules()
	if c := r.Chance(10, 30, 0); c != 9000 {
		t.Fatalf("capped chance = %d", c)
	}
	if c := r.Chance(0, 0, 100000); c != 500 {
		t.Fatalf("floored chance = %d", c)
	}
	if r.Chance(0, 30, 0) <= r.Chance(0, 0, 0) {
		t.Fatal("cover must help")
	}
	if r.Chance(0, 0, 3000) >= r.Chance(0, 0, 0) {
		t.Fatal("counter-intelligence must hurt")
	}
}

func TestResolveSplitsTheRollIntoCleanPartialAndBlown(t *testing.T) {
	cases := []struct {
		roll int64
		out  Outcome
		conf string
	}{{0, Clean, ConfHigh}, {1999, Clean, ConfHigh}, {2000, Clean, ConfMedium}, {3999, Clean, ConfMedium}, {4000, Partial, ConfLow}, {5999, Partial, ConfLow}, {6000, Blown, ""}, {9999, Blown, ""}}
	for _, c := range cases {
		o, conf := Resolve(6000, c.roll)
		if o != c.out || conf != c.conf {
			t.Fatalf("roll %d: %s %q, want %s %q", c.roll, o, conf, c.out, c.conf)
		}
	}
}

func TestARollIsTheSameOnEveryReplicaAndInRange(t *testing.T) {
	for i := 0; i < 500; i++ {
		l := "op-" + strconv.Itoa(i)
		a, b := Roll(l), Roll(l)
		if a != b || a < 0 || a > 9999 {
			t.Fatalf("roll %q = %d, %d", l, a, b)
		}
	}
}

func TestSeveralDetectorsCombineAsOneMinusTheMisses(t *testing.T) {
	if got := Combine([]int64{3000, 3000}); got != 5100 {
		t.Fatalf("combine = %d", got)
	}
	if Combine(nil) != 0 || Combine([]int64{10000, 10}) != 10000 {
		t.Fatal("edge cases")
	}
}

func TestExpulsionAndFreshness(t *testing.T) {
	r := rules()
	if !r.Expel(now).Equal(now.Add(14 * 24 * time.Hour)) {
		t.Fatal("expel")
	}
	if r.Aged(now, now.Add(13*24*time.Hour)) || !r.Aged(now, now.Add(14*24*time.Hour)) {
		t.Fatal("aged")
	}
}

func TestABandNamesTheRangeAndNeverTheFigure(t *testing.T) {
	b := Band{Names: []string{"small", "medium", "large", "vast"}, Edges: []int64{1000, 10000, 100000}}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	for v, want := range map[int64]string{0: "small", 999: "small", 1000: "medium", 9999: "medium", 10000: "large", 99999: "large", 100000: "vast", 1 << 40: "vast"} {
		if got := b.Of(v); got != want {
			t.Fatalf("band of %d = %s, want %s", v, got, want)
		}
	}
}

func TestABandWithAFigureForANameIsRefused(t *testing.T) {
	if (Band{Names: []string{"small", "12"}, Edges: []int64{5}}).Validate() == nil {
		t.Fatal("a figure is not a band name")
	}
	if (Band{Names: []string{"a", "b"}, Edges: nil}).Validate() == nil {
		t.Fatal("edges must be one fewer than names")
	}
}

func TestAReportCarriesBandNamesNeverFiguresOrNames(t *testing.T) {
	bands := map[string]Band{"treasury": {Names: []string{"small", "large"}, Edges: []int64{500}}}
	if err := (Fields{"treasury": "small"}).Check(bands); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Fields{{"treasury": "4200"}, {"treasury": "large "}, {"treasury": "Ali"}, {"treasury": "huge"}, {"stock": "small"}, {"treasury_exact": "small"}} {
		if bad.Check(bands) == nil {
			t.Fatalf("%v should be refused", bad)
		}
	}
}

func TestAPartialReportIsVaguer(t *testing.T) {
	bands := map[string]Band{"m": {Names: []string{"a", "b", "c", "d", "e"}, Edges: []int64{1, 2, 3, 4}}}
	got := Widen(Fields{"m": "e"}, bands)
	if got["m"] != "d" {
		t.Fatalf("widened = %v", got)
	}
	if Widen(Fields{"m": "c"}, bands)["m"] != "c" {
		t.Fatal("the middle stays")
	}
}

func TestSabotageIsLimitedByTheWeekTheTargetTheShieldAndTheTreaty(t *testing.T) {
	r := sab()
	ok := History{HasCover: true}
	if r.Check(DelayWork, ok, now) != Allowed {
		t.Fatal("a fresh act is allowed")
	}
	if r.Check("burn_down", ok, now) != UnknownKind {
		t.Fatal("there is no kind that destroys")
	}
	if r.Check(DelayWork, History{}, now) != NoCover {
		t.Fatal("no cover, no act")
	}
	if r.Check(DelayWork, History{HasCover: true, ActsThisWeek: 2}, now) != WeeklyLimit {
		t.Fatal("weekly limit of 2")
	}
	if r.Check(DelayWork, History{HasCover: true, ActsThisWeek: 1}, now) != Allowed {
		t.Fatal("one act this week leaves one")
	}
	if r.Check(SpoilStock, History{HasCover: true, LastSameAt: now.Add(-6 * 24 * time.Hour)}, now) != TooSoon {
		t.Fatal("same kind on the same target within 7 days")
	}
	if r.Check(SpoilStock, History{HasCover: true, LastSameAt: now.Add(-7 * 24 * time.Hour)}, now) != Allowed {
		t.Fatal("after 7 days")
	}
	if r.Check(SkipServiceDay, History{HasCover: true, LastSameAt: now.Add(-20 * 24 * time.Hour)}, now) != TooSoon {
		t.Fatal("a skipped day is once a month")
	}
	if r.Check(BlindPost, History{HasCover: true, ShieldedOrGrace: true}, now) != Protected {
		t.Fatal("grace and shield protect")
	}
	if r.Check(BlindPost, History{HasCover: true, TreatyForbids: true}, now) != Forbidden {
		t.Fatal("a treaty forbids")
	}
}

func TestSpoilageIsFivePercentOfOneStackAtLeastOneUnit(t *testing.T) {
	r := sab()
	for stack, want := range map[int64]int64{0: 0, 1: 1, 10: 1, 20: 1, 100: 5, 1000: 50} {
		if got := r.Spoil(stack); got != want {
			t.Fatalf("spoil of %d = %d, want %d", stack, got, want)
		}
	}
}

func TestADelayIsAtMostTwelveHoursAheadOfNow(t *testing.T) {
	r := sab()
	d := r.Delay(time.Time{}, now)
	if !d.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("first delay = %v", d)
	}
	if d2 := r.Delay(d, now); !d2.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("a second act does not reach beyond twelve hours: %v", d2)
	}
	if d3 := r.Delay(now.Add(-time.Hour), now); !d3.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("a stale delay restarts from now: %v", d3)
	}
}

func TestBlindingLowersAChanceByAShareNeverUnderZero(t *testing.T) {
	r := sab()
	if r.Blind(6000) != 4200 || r.Blind(0) != 0 {
		t.Fatal("blind")
	}
}

func TestNoKindDestroysOrHarms(t *testing.T) {
	for _, k := range Kinds() {
		switch k {
		case DelayWork, SpoilStock, BlindPost, SkipServiceDay:
		default:
			t.Fatalf("an unexpected kind %q", k)
		}
	}
	if len(Kinds()) != 4 {
		t.Fatal("four kinds")
	}
}

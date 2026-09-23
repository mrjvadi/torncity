package crime

import (
	"errors"
	"math"
	"math/rand"
	"testing"
	"testing/quick"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// script is a Dice that answers from a list, in order, and fails the test
// when asked for more rolls than it holds.
type script struct {
	t     *testing.T
	rolls []int64
	asked []int64
}

func (s *script) Roll(n int64) int64 {
	s.asked = append(s.asked, n)
	if len(s.rolls) == 0 {
		s.t.Fatalf("dice asked for roll %d (n=%d) beyond the script", len(s.asked), n)
	}
	v := s.rolls[0]
	s.rolls = s.rolls[1:]
	return v
}

func dice(t *testing.T, rolls ...int64) *script { return &script{t: t, rolls: rolls} }

// seeded is a Dice over math/rand with a fixed seed, for property tests.
type seeded struct{ r *rand.Rand }

func (s seeded) Roll(n int64) int64 { return s.r.Int63n(n) }

func amt(v int64) money.Amount { return money.FromMinor(v) }

func neutral() JusticePolicy { return JusticePolicy{JailTermPct: 100, FinePct: 100} }

// pickpocket can hit an NPC passer-by or a player.
func pickpocket() Crime {
	return Crime{
		Code:      "pickpocketing",
		Category:  "petty_theft",
		Targets:   []TargetKind{TargetNPC, TargetPlayer},
		Victims:   VictimModel{PerPlayerBPS: 1500, CapBPS: 6000},
		NerveCost: 2,
		Success: SuccessModel{
			BaseChanceBPS:      6000,
			SkillWeights:       []SkillWeight{{Skill: player.SkillStealth, BPSPerLevel: 150}},
			AwarenessWeightBPS: 100,
			TargetAwareness:    5,
			HeatPenaltyBPS:     50,
			WitnessChanceBPS:   2000,
		},
		Reward: Reward{
			MinCash: amt(10), MaxCash: amt(90),
			ShareBPS: 1500, MinTake: amt(10), MaxTake: amt(2000),
			XP: 6, CriminalXP: 10,
			SkillXP: []SkillXP{{Skill: player.SkillStealth, XP: 12}},
			Heat:    4,
		},
		Failure: Failure{
			CatchChanceBPS: 3500,
			JailMin:        2 * time.Hour, JailMax: 6 * time.Hour,
			FineMin: amt(100), FineMax: amt(400),
			Heat: 10,
		},
	}
}

// burglary is a timed NPC-only crime.
func burglary() Crime {
	c := pickpocket()
	c.Code, c.Category = "home_burglary", "burglary"
	c.Targets = []TargetKind{TargetNPC}
	c.Victims = VictimModel{}
	c.Duration = 2 * time.Hour
	c.Success.WitnessChanceBPS = 0
	c.Reward.ShareBPS, c.Reward.MinTake, c.Reward.MaxTake = 0, money.Amount{}, money.Amount{}
	c.Reward.MinCash, c.Reward.MaxCash = amt(500), amt(2500)
	return c
}

func TestShippedShapesValidate(t *testing.T) {
	for _, c := range []Crime{pickpocket(), burglary()} {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", c.Code, err)
		}
	}
}

func TestValidateRefusesEachMistake(t *testing.T) {
	cases := map[string]struct {
		edit func(*Crime)
		want error
	}{
		"no targets":        {func(c *Crime) { c.Targets = nil }, ErrNoTargets},
		"a target twice":    {func(c *Crime) { c.Targets = []TargetKind{TargetNPC, TargetNPC} }, ErrNoTargets},
		"unknown target":    {func(c *Crime) { c.Targets = []TargetKind{"bank_vault"} }, ErrUnknownTarget},
		"business for now":  {func(c *Crime) { c.Targets = []TargetKind{TargetBusiness} }, ErrUnplayableTarget},
		"timed player":      {func(c *Crime) { c.Duration = time.Hour }, ErrTimedPlayerCrime},
		"no nerve":          {func(c *Crime) { c.NerveCost = 0 }, ErrInvalidCrime},
		"too long":          {func(c *Crime) { c.Targets = []TargetKind{TargetNPC}; c.Duration = MaxDuration + 1 }, ErrInvalidCrime},
		"base above 100%":   {func(c *Crime) { c.Success.BaseChanceBPS = BPSWhole + 1 }, ErrInvalidCrime},
		"bad skill":         {func(c *Crime) { c.Success.SkillWeights[0].Skill = "juggling" }, ErrInvalidCrime},
		"inverted cash":     {func(c *Crime) { c.Reward.MinCash = amt(100) }, ErrInvalidCrime},
		"no share":          {func(c *Crime) { c.Reward.ShareBPS = 0 }, ErrInvalidCrime},
		"no victim model":   {func(c *Crime) { c.Victims = VictimModel{} }, ErrInvalidCrime},
		"arrest, no term":   {func(c *Crime) { c.Failure.JailMin, c.Failure.JailMax = 0, 0 }, ErrInvalidCrime},
		"inverted jail":     {func(c *Crime) { c.Failure.JailMin = 7 * time.Hour }, ErrInvalidCrime},
		"inverted fine":     {func(c *Crime) { c.Failure.FineMin = amt(500) }, ErrInvalidCrime},
		"negative xp":       {func(c *Crime) { c.Reward.XP = -1 }, ErrInvalidCrime},
		"heat beyond bound": {func(c *Crime) { c.Reward.Heat = MaxHeatGain + 1 }, ErrInvalidCrime},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := pickpocket()
			tc.edit(&c)
			if err := c.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}

	npcOnly := burglary()
	npcOnly.Reward.ShareBPS = 100
	if err := npcOnly.Validate(); !errors.Is(err, ErrInvalidCrime) {
		t.Errorf("a share on an NPC-only crime: Validate() = %v", err)
	}
	npcOnly = burglary()
	npcOnly.Success.WitnessChanceBPS = 10
	if err := npcOnly.Validate(); !errors.Is(err, ErrInvalidCrime) {
		t.Errorf("a witness chance on an NPC-only crime: Validate() = %v", err)
	}
	playerOnly := pickpocket()
	playerOnly.Targets = []TargetKind{TargetPlayer}
	if err := playerOnly.Validate(); !errors.Is(err, ErrInvalidCrime) {
		t.Errorf("a cash range on a player-only crime: Validate() = %v", err)
	}
}

func TestTiers(t *testing.T) {
	tiers := []Tier{{"novice", 0}, {"hustler", 150}, {"professional", 600}}
	if err := ValidateTiers(tiers); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		xp   int64
		want int
	}{{-5, 0}, {0, 0}, {149, 0}, {150, 1}, {599, 1}, {600, 2}, {math.MaxInt64, 2}} {
		if got := TierOf(tiers, tc.xp); got != tc.want {
			t.Errorf("TierOf(%d) = %d, want %d", tc.xp, got, tc.want)
		}
	}
	for name, bad := range map[string][]Tier{
		"empty":         nil,
		"not from zero": {{"a", 5}},
		"flat":          {{"a", 0}, {"b", 0}},
	} {
		if err := ValidateTiers(bad); !errors.Is(err, ErrInvalidTiers) {
			t.Errorf("%s: ValidateTiers() = %v", name, err)
		}
	}
}

func TestEligibilityReportsEveryShortfall(t *testing.T) {
	c := burglary()
	c.Requirements = Requirements{
		MinLevel: 5, MinTier: 1,
		Skills:         []SkillRequirement{{Skill: player.SkillLockpicking, Level: 3}},
		Certifications: []string{"locksmithing"},
		Tools:          []string{"crowbar"},
		Facilities:     []string{"rail_station"},
	}
	err := Eligibility(c, Candidate{Level: 2, Skills: []player.Skill{{Code: player.SkillLockpicking, Level: 1}}})
	var (
		level LevelShortfall
		tier  TierShortfall
		skill SkillShortfall
		cert  CertificationShortfall
		tool  ToolShortfall
		fac   FacilityShortfall
	)
	for _, target := range []any{&level, &tier, &skill, &cert, &tool, &fac} {
		if !errors.As(err, target) {
			t.Errorf("Eligibility() = %v: missing %T", err, target)
		}
	}
	if skill.Need != 3 || skill.Have != 1 || level.Need != 5 || level.Have != 2 {
		t.Errorf("shortfall details wrong: %+v %+v", skill, level)
	}
	ok := Candidate{
		Level: 5, Tier: 1,
		Skills:         []player.Skill{{Code: player.SkillLockpicking, Level: 3}},
		Certifications: []string{"locksmithing"}, Tools: []string{"crowbar"}, Facilities: []string{"rail_station"},
	}
	if err := Eligibility(c, ok); err != nil {
		t.Errorf("a candidate meeting everything: %v", err)
	}
}

func TestSuccessChance(t *testing.T) {
	c := pickpocket()
	skills := []player.Skill{{Code: player.SkillStealth, Level: 4}}
	// 6000 + 4×150 − (5+2)×100 − 10×50 = 5400 against an NPC at security 2.
	if got := c.SuccessChance(Situation{Skills: skills, Heat: 10, Victim: TargetNPC, VenueSecurity: 2}); got != 5400 {
		t.Errorf("NPC chance = %d, want 5400", got)
	}
	// Against a player the victim's awareness replaces the NPC figure:
	// 6000 + 600 − (20+2)×100 − 0 = 4400.
	if got := c.SuccessChance(Situation{Skills: skills, Victim: TargetPlayer, Awareness: 20, VenueSecurity: 2}); got != 4400 {
		t.Errorf("player chance = %d, want 4400", got)
	}
	if got := c.SuccessChance(Situation{Heat: 1000, Victim: TargetNPC}); got != ChanceFloorBPS {
		t.Errorf("hopeless chance = %d, want the floor", got)
	}
	skills[0].Level = 100
	if got := c.SuccessChance(Situation{Skills: skills, Victim: TargetNPC}); got != ChanceCeilingBPS {
		t.Errorf("certain chance = %d, want the ceiling", got)
	}
	if got := VictimAwareness(12, []player.Skill{{Code: player.SkillStreetwise, Level: 3}}); got != 15 {
		t.Errorf("VictimAwareness = %d, want 15", got)
	}
}

func TestResolveNPCSuccess(t *testing.T) {
	c := pickpocket()
	d := dice(t, 100, 30) // success; take 10+30
	out, err := Resolve(Attempt{Crime: c, Victim: TargetNPC, Chance: 5000, NPCAllowance: amt(1000), Policy: neutral()}, d)
	if err != nil {
		t.Fatal(err)
	}
	if out.Result != Succeeded || out.Take.Minor() != 40 || out.Witnessed || out.XP != 6 || out.CriminalXP != 10 || out.Heat != 4 {
		t.Errorf("outcome %+v", out)
	}
	if len(d.asked) != 2 || d.asked[1] != 81 {
		t.Errorf("rolls asked %v, want [10000 81]", d.asked)
	}

	// The daily allowance caps the take; an exhausted one pays nothing.
	out, _ = Resolve(Attempt{Crime: c, Victim: TargetNPC, Chance: 5000, NPCAllowance: amt(25), Policy: neutral()}, dice(t, 0, 80))
	if out.Take.Minor() != 25 {
		t.Errorf("capped take = %s, want 25", out.Take)
	}
	out, _ = Resolve(Attempt{Crime: c, Victim: TargetNPC, Chance: 5000, Policy: neutral()}, dice(t, 0, 80))
	if out.Result != Succeeded || !out.Take.IsZero() {
		t.Errorf("exhausted allowance: %+v", out)
	}
}

func TestResolvePlayerSuccessAndWitness(t *testing.T) {
	c := pickpocket()
	out, err := Resolve(Attempt{Crime: c, Victim: TargetPlayer, Chance: 5000, VictimCash: amt(1000), Policy: neutral()},
		dice(t, 4999, 1999))
	if err != nil {
		t.Fatal(err)
	}
	// 15% of 1000 = 150; the witness roll 1999 < 2000 is seen.
	if out.Take.Minor() != 150 || !out.Witnessed {
		t.Errorf("outcome %+v", out)
	}
	out, _ = Resolve(Attempt{Crime: c, Victim: TargetPlayer, Chance: 5000, VictimCash: amt(1000), Policy: neutral()},
		dice(t, 0, 2000))
	if out.Witnessed {
		t.Error("a witness roll at the chance is not a witness")
	}
}

func TestResolveFailures(t *testing.T) {
	c := pickpocket()
	// Failure, then an escape: roll 3500 is not below the 3500 catch chance.
	out, err := Resolve(Attempt{Crime: c, Victim: TargetNPC, Chance: 5000, Policy: neutral()}, dice(t, 5000, 3500))
	if err != nil {
		t.Fatal(err)
	}
	if out.Result != Escaped || out.XP != 0 || out.CriminalXP != 0 || out.Heat != 4 ||
		len(out.SkillXP) != 1 || out.SkillXP[0].XP != 6 {
		t.Errorf("escape %+v", out)
	}

	// Failure, arrest; term 2h + 3600s = 3h, × 150% = 4.5h; fine 100+100 = 200 × 50% = 100.
	pol := JusticePolicy{JailTermPct: 150, FinePct: 50}
	d := dice(t, 9999, 0, 3600, 100)
	out, err = Resolve(Attempt{Crime: c, Victim: TargetNPC, Chance: 5000, Policy: pol}, d)
	if err != nil {
		t.Fatal(err)
	}
	if out.Result != Caught || out.JailTerm != 4*time.Hour+30*time.Minute || out.Fine.Minor() != 100 ||
		out.Heat != 10 || !out.Take.IsZero() || out.XP != 0 {
		t.Errorf("arrest %+v", out)
	}
	if want := int64(4*3600 + 1); d.asked[2] != want || d.asked[3] != 301 {
		t.Errorf("sentence rolls asked %v", d.asked)
	}
}

func TestResolveRefusals(t *testing.T) {
	c := burglary()
	if _, err := Resolve(Attempt{Crime: c, Victim: TargetPlayer, Chance: 5000, Policy: neutral()}, dice(t)); !errors.Is(err, ErrNoVictim) {
		t.Errorf("a victim the crime cannot hit: %v", err)
	}
	if _, err := Resolve(Attempt{Crime: c, Victim: TargetNPC, Policy: neutral()}, nil); !errors.Is(err, ErrNoDice) {
		t.Errorf("no dice: %v", err)
	}
	if _, err := Resolve(Attempt{Crime: c, Victim: TargetNPC, Policy: neutral()}, dice(t, 10_000)); !errors.Is(err, ErrBadRoll) {
		t.Errorf("an out-of-range roll: %v", err)
	}
	if _, err := Resolve(Attempt{Crime: c, Victim: TargetNPC}, dice(t)); !errors.Is(err, ErrInvalidPolicy) {
		t.Errorf("a zero policy: %v", err)
	}
}

func TestPlayerTakeProperties(t *testing.T) {
	r := pickpocket().Reward
	for _, tc := range []struct{ cash, want int64 }{
		{0, 0}, {-5, 0}, {5, 5}, {10, 10}, {60, 10}, {1000, 150}, {100_000, 2000},
		{math.MaxInt64, 2000},
	} {
		got, err := PlayerTake(r, amt(tc.cash))
		if err != nil || got.Minor() != tc.want {
			t.Errorf("PlayerTake(%d) = %s, %v; want %d", tc.cash, got, err, tc.want)
		}
	}
	prop := func(cash int64, share uint16, lo, hi uint32) bool {
		rw := Reward{ShareBPS: int(share%BPSWhole) + 1, MinTake: amt(int64(min(lo, hi))), MaxTake: amt(int64(max(lo, hi)) + 1)}
		got, err := PlayerTake(rw, amt(cash))
		if err != nil {
			return false
		}
		v := got.Minor()
		switch {
		case v < 0, v > max(cash, 0), v > rw.MaxTake.Minor():
			return false
		case cash >= rw.MinTake.Minor() && v < rw.MinTake.Minor():
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 5000}); err != nil {
		t.Error(err)
	}
}

func TestSentenceProperties(t *testing.T) {
	f := pickpocket().Failure
	d := seeded{rand.New(rand.NewSource(7))}
	for i := 0; i < 2000; i++ {
		pol := JusticePolicy{JailTermPct: 1 + i%MaxPolicyPct, FinePct: i % (MaxPolicyPct + 1)}
		term, fine, err := Sentence(f, pol, d)
		if err != nil {
			t.Fatal(err)
		}
		minTerm := f.JailMin * time.Duration(pol.JailTermPct) / 100
		maxTerm := f.JailMax * time.Duration(pol.JailTermPct) / 100
		if term < max(minTerm, time.Second)-time.Second || term > maxTerm {
			t.Fatalf("term %s outside %s..%s at %d%%", term, minTerm, maxTerm, pol.JailTermPct)
		}
		if fine.Minor() < f.FineMin.Minor()*int64(pol.FinePct)/100 || fine.Minor() > f.FineMax.Minor()*int64(pol.FinePct)/100 {
			t.Fatalf("fine %s outside the scaled range at %d%%", fine, pol.FinePct)
		}
	}
}

func TestNerve(t *testing.T) {
	r := NerveRules{Max: 20, RegenAmount: 1, RegenInterval: 5 * time.Minute}
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	n, err := r.Spend(Nerve{Current: 20, UpdatedAt: t0}, 6)
	if err != nil || n.Current != 14 {
		t.Fatalf("Spend = %+v, %v", n, err)
	}
	// Twelve minutes later: two whole ticks, and the timestamp keeps the two
	// minutes already waited for the third.
	n = r.Regenerate(n, t0.Add(12*time.Minute))
	if n.Current != 16 || !n.UpdatedAt.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("Regenerate = %+v", n)
	}
	if left := r.FullIn(n, t0.Add(12*time.Minute)); left != 18*time.Minute {
		t.Errorf("FullIn = %s, want 18m", left)
	}
	var short NerveShortfall
	if _, err := r.Spend(n, 17); !errors.As(err, &short) || short.Need != 17 || short.Have != 16 {
		t.Errorf("overspend = %v", err)
	}
	// A full bar is stamped now, so a later spend starts a fresh wait.
	full := r.Regenerate(Nerve{Current: 20, UpdatedAt: t0}, t0.Add(time.Hour))
	if !full.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("a full bar kept its old stamp: %+v", full)
	}
	if n := r.Regenerate(Nerve{Current: 3, UpdatedAt: t0}, t0.Add(-time.Hour)); n.Current != 3 {
		t.Errorf("a clock stepping back paid out nerve: %+v", n)
	}
	if n := r.Regenerate(Nerve{Current: 3, UpdatedAt: t0}, t0.Add(1000*time.Hour)); n.Current != 20 {
		t.Errorf("a long absence did not refill: %+v", n)
	}
}

func TestHeat(t *testing.T) {
	r := HeatRules{Max: 100, DecayPerHour: 5}
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h := r.Add(r.Cool(Heat{}, t0), 30)
	if h.Level != 30 {
		t.Fatalf("Add = %+v", h)
	}
	h = r.Cool(h, t0.Add(150*time.Minute))
	if h.Level != 20 || !h.UpdatedAt.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("Cool = %+v", h)
	}
	if h := r.Add(h, 500); h.Level != 100 {
		t.Errorf("Add past the cap = %d", h.Level)
	}
	if h := r.Cool(Heat{Level: 20, UpdatedAt: t0}, t0.Add(10*time.Hour)); h.Level != 0 {
		t.Errorf("fully cooled = %+v", h)
	}
	for heat, want := range map[int]int{0: 0, 1: 1, 20: 1, 21: 2, 99: 5, 100: 5, 1000: 5} {
		if got := r.WantedLevel(heat); got != want {
			t.Errorf("WantedLevel(%d) = %d, want %d", heat, got, want)
		}
	}
}

func TestSettleProperties(t *testing.T) {
	s := Settle(amt(500), amt(300), amt(200), amt(450))
	if s.RestitutionFromCash.Minor() != 200 || s.RestitutionFromBank.Minor() != 300 ||
		s.FineFromCash.Minor() != 0 || s.FineFromBank.Minor() != 150 ||
		s.RestitutionShortfall.Minor() != 0 || s.FineShortfall.Minor() != 150 {
		t.Errorf("Settle = %+v", s)
	}
	prop := func(owed, fine, cash, bank uint32) bool {
		s := Settle(amt(int64(owed)), amt(int64(fine)), amt(int64(cash)), amt(int64(bank)))
		for _, v := range []money.Amount{s.RestitutionFromCash, s.RestitutionFromBank, s.FineFromCash,
			s.FineFromBank, s.RestitutionShortfall, s.FineShortfall} {
			if v.IsNegative() {
				return false
			}
		}
		// Every unit owed is paid or short; nothing is taken that is not there;
		// the fine is paid only once restitution is whole.
		return s.Restitution().Minor()+s.RestitutionShortfall.Minor() == int64(owed) &&
			s.FinePaid().Minor()+s.FineShortfall.Minor() == int64(fine) &&
			s.RestitutionFromCash.Minor()+s.FineFromCash.Minor() <= int64(cash) &&
			s.RestitutionFromBank.Minor()+s.FineFromBank.Minor() <= int64(bank) &&
			(s.FinePaid().IsZero() || s.RestitutionShortfall.IsZero())
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 5000}); err != nil {
		t.Error(err)
	}
}

func TestCharge(t *testing.T) {
	c, b, ok := Charge(amt(300), amt(100), amt(500))
	if !ok || c.Minor() != 100 || b.Minor() != 200 {
		t.Errorf("Charge = %s %s %v", c, b, ok)
	}
	if _, _, ok := Charge(amt(700), amt(100), amt(500)); ok {
		t.Error("a charge the two cannot cover was split")
	}
	if _, _, ok := Charge(amt(math.MaxInt64), amt(math.MaxInt64), amt(math.MaxInt64)); !ok {
		t.Error("a charge covered by cash alone was refused")
	}
}

func TestBail(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// A 6h game sentence served over 6 real minutes, 2 minutes in: 4h left.
	left := RemainingTerm(6*time.Hour, t0, t0.Add(6*time.Minute), t0.Add(2*time.Minute))
	if left != 4*time.Hour {
		t.Fatalf("RemainingTerm = %s", left)
	}
	b, err := Bail(amt(250), left)
	if err != nil || b.Minor() != 1000 {
		t.Errorf("Bail = %s, %v", b, err)
	}
	// Rounded up to the minor unit: one second at 250 per hour is 1.
	if b, _ := Bail(amt(250), time.Second); b.Minor() != 1 {
		t.Errorf("Bail(1s) = %s", b)
	}
	if left := RemainingTerm(6*time.Hour, t0, t0.Add(6*time.Minute), t0.Add(time.Hour)); left != 0 {
		t.Errorf("a served sentence has %s left", left)
	}
	if b, _ := Bail(amt(250), 0); !b.IsZero() {
		t.Errorf("bail on nothing left = %s", b)
	}
}

func TestInvestigation(t *testing.T) {
	m := InvestigationModel{BaseBPS: 2000, PerHeatBPS: 50, WitnessBonusBPS: 3000, EffortWeightBPS: 4000}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	// 2000 + 20×50 + 3000 + 5000×4000/10000 = 8000.
	if got := m.SolveChance(20, true, 5000); got != 8000 {
		t.Errorf("SolveChance = %d, want 8000", got)
	}
	if got := m.SolveChance(1000, true, 10_000); got != ChanceCeilingBPS {
		t.Errorf("SolveChance clamps high: %d", got)
	}
	ok, err := Solved(8000, dice(t, 7999))
	if err != nil || !ok {
		t.Errorf("Solved(7999 < 8000) = %v, %v", ok, err)
	}
	if ok, _ := Solved(8000, dice(t, 8000)); ok {
		t.Error("a roll at the chance solved the case")
	}
	if got := (Protection{MinLevel: 3, MinAge: 72 * time.Hour}).Protected(5, time.Now().Add(-time.Hour), time.Now()); !got {
		t.Error("a day-old account is not protected")
	}
}

func venues() []Venue {
	return []Venue{
		{Code: "city_centre", Default: true, OpportunityBPS: 10_000},
		{Code: "train_station", Arrivals: []string{"train"}, OpportunityBPS: 12_000, Security: 3},
		{Code: "business_district", WorkCategories: []string{"commerce"}, OpportunityBPS: 6_000},
	}
}

func TestVenues(t *testing.T) {
	vs := venues()
	if err := ValidateVenues(vs); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		w    Whereabouts
		want int
	}{
		{Whereabouts{}, 0},
		{Whereabouts{ArrivedBy: "train"}, 1},
		{Whereabouts{ArrivedBy: "car"}, 0},
		{Whereabouts{ShiftCategory: "commerce"}, 2},
		{Whereabouts{ShiftCategory: "commerce", ArrivedBy: "train"}, 2},
		{Whereabouts{ShiftCategory: "food", ArrivedBy: "train"}, 1},
	} {
		if got := Locate(vs, tc.w); got != tc.want {
			t.Errorf("Locate(%+v) = %d, want %d", tc.w, got, tc.want)
		}
	}
	for name, bad := range map[string][]Venue{
		"none":         nil,
		"two defaults": {{Code: "a", Default: true}, {Code: "b", Default: true}},
		"no default":   {{Code: "a"}},
		"twice":        {{Code: "a", Default: true}, {Code: "a"}},
		"mode twice":   {{Code: "a", Default: true, Arrivals: []string{"bus"}}, {Code: "b", Arrivals: []string{"bus"}}},
		"security":     {{Code: "a", Default: true, Security: MaxVenueSecurity + 1}},
	} {
		if err := ValidateVenues(bad); !errors.Is(err, ErrInvalidVenues) {
			t.Errorf("%s: ValidateVenues() = %v", name, err)
		}
	}
}

func TestVictimRules(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := VictimRules{
		Protection:   Protection{MinLevel: 3, MinAge: 72 * time.Hour},
		ActiveWindow: 30 * time.Minute, VictimCooldown: 6 * time.Hour, ThiefCooldown: 24 * time.Hour,
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	ok := Bystander{Level: 5, CreatedAt: now.Add(-100 * time.Hour), LastActiveAt: now.Add(-time.Minute), Venue: 1}
	if !r.Eligible(ok, 1, now) {
		t.Fatal("an ordinary bystander is not eligible")
	}
	for name, edit := range map[string]func(*Bystander){
		"elsewhere":        func(b *Bystander) { b.Venue = 0 },
		"low level":        func(b *Bystander) { b.Level = 2 },
		"new account":      func(b *Bystander) { b.CreatedAt = now.Add(-time.Hour) },
		"idle":             func(b *Bystander) { b.LastActiveAt = now.Add(-time.Hour) },
		"never active":     func(b *Bystander) { b.LastActiveAt = time.Time{} },
		"robbed recently":  func(b *Bystander) { b.LastVictimisedAt = now.Add(-time.Hour) },
		"same thief again": func(b *Bystander) { b.LastHitByThief = now.Add(-10 * time.Hour) },
	} {
		b := ok
		edit(&b)
		if r.Eligible(b, 1, now) {
			t.Errorf("%s: still eligible", name)
		}
	}
	b := ok
	b.LastVictimisedAt, b.LastHitByThief = now.Add(-7*time.Hour), now.Add(-25*time.Hour)
	if !r.Eligible(b, 1, now) {
		t.Error("cooldowns that have passed still protect")
	}
}

func TestChooseVictim(t *testing.T) {
	c := pickpocket()
	// 3 nearby × 1500 × 12000/10000 = 5400, under the 6000 cap.
	if got := c.PlayerVictimChance(3, 12_000); got != 5400 {
		t.Errorf("PlayerVictimChance = %d, want 5400", got)
	}
	if got := c.PlayerVictimChance(40, 12_000); got != 6000 {
		t.Errorf("PlayerVictimChance does not cap: %d", got)
	}
	if got := c.PlayerVictimChance(0, 12_000); got != 0 {
		t.Errorf("nobody nearby: %d", got)
	}
	kind, i, err := ChooseVictim(c, 3, 12_000, dice(t, 5399, 2))
	if err != nil || kind != TargetPlayer || i != 2 {
		t.Errorf("ChooseVictim = %s %d %v, want the third player", kind, i, err)
	}
	kind, _, err = ChooseVictim(c, 3, 12_000, dice(t, 5400))
	if err != nil || kind != TargetNPC {
		t.Errorf("ChooseVictim at the chance = %s %v, want an NPC", kind, err)
	}
	// Nobody nearby: an NPC without a roll.
	if kind, _, err := ChooseVictim(c, 0, 12_000, dice(t)); err != nil || kind != TargetNPC {
		t.Errorf("empty venue: %s %v", kind, err)
	}
	// An NPC-only crime never rolls; a player-only one picks straight away.
	if kind, _, _ := ChooseVictim(burglary(), 5, 10_000, dice(t)); kind != TargetNPC {
		t.Errorf("NPC-only crime chose %s", kind)
	}
	only := pickpocket()
	only.Targets = []TargetKind{TargetPlayer}
	if kind, i, _ := ChooseVictim(only, 5, 10_000, dice(t, 4)); kind != TargetPlayer || i != 4 {
		t.Errorf("player-only crime chose %s %d", kind, i)
	}
	if _, _, err := ChooseVictim(only, 0, 10_000, dice(t)); !errors.Is(err, ErrNoVictim) {
		t.Errorf("player-only crime with nobody around: %v", err)
	}
}

// TestResolveIsDeterministic replays random attempts with the same seed and
// checks every outcome stays inside the crime's authored bounds.
func TestResolveIsDeterministic(t *testing.T) {
	c := pickpocket()
	run := func() []Outcome {
		d := seeded{rand.New(rand.NewSource(42))}
		var out []Outcome
		for i := 0; i < 500; i++ {
			victim := TargetNPC
			if i%2 == 1 {
				victim = TargetPlayer
			}
			o, err := Resolve(Attempt{Crime: c, Victim: victim, Chance: 5000,
				VictimCash: amt(int64(i * 37)), NPCAllowance: amt(1_000_000), Policy: neutral()}, d)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, o)
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i].Result != b[i].Result || a[i].Take != b[i].Take || a[i].JailTerm != b[i].JailTerm || a[i].Fine != b[i].Fine {
			t.Fatalf("attempt %d differs between two runs with one seed", i)
		}
		if a[i].Result == Succeeded && i%2 == 0 &&
			(a[i].Take.Minor() < c.Reward.MinCash.Minor() || a[i].Take.Minor() > c.Reward.MaxCash.Minor()) {
			t.Fatalf("attempt %d took %s outside the cash range", i, a[i].Take)
		}
		if a[i].Result == Caught && (a[i].JailTerm < c.Failure.JailMin || a[i].JailTerm > c.Failure.JailMax) {
			t.Fatalf("attempt %d jailed for %s", i, a[i].JailTerm)
		}
	}
}

package labor

import "testing"

func TestDefaultValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	r := Default()
	r.Curve = []Point{{0, 10_000}, {5_000, 8_000}}
	if r.Validate() == nil {
		t.Error("a curve where scarcity lowers wages was accepted")
	}
	r = Default()
	r.Levels[0].MinShifts = 1
	if r.Validate() == nil {
		t.Error("a ladder not starting at zero was accepted")
	}
	r = Default()
	r.ShiftMinutes = 0
	if r.Validate() == nil {
		t.Error("a zero shift was accepted")
	}
}

func TestWorkRequiredIsCrewTimes(t *testing.T) {
	r := Default()
	if got := r.WorkRequired(60); got != 240 {
		t.Errorf("1h with a crew of 4 = %d worker-minutes, want 240", got)
	}
	if got := r.WorkRequired(0); got != 4 {
		t.Errorf("a zero build time still needs work, got %d", got)
	}
}

func TestSkillIsProductivity(t *testing.T) {
	r := Default()
	for shifts, want := range map[int64]string{0: "apprentice", 5: "apprentice", 6: "journeyman", 29: "journeyman", 30: "master", 500: "master"} {
		if got := r.LevelOf(shifts).Code; got != want {
			t.Errorf("%d shifts = %s, want %s", shifts, got, want)
		}
	}
	if a, m := r.Points(r.LevelOf(0).ProductivityBPS), r.Points(r.LevelOf(30).ProductivityBPS); a != 42 || m != 78 {
		t.Errorf("apprentice %d, master %d worker-minutes per shift, want 42 and 78", a, m)
	}
}

func TestMoreHomesMoreWorkers(t *testing.T) {
	r := Default()
	base := r.PoolSize(0, 0)
	if base != 5 {
		t.Fatalf("a village with no home built has a pool of %d, want 5 (8 households x 60%%, rounded up)", base)
	}
	if three := r.PoolSize(3*4, 0); three <= base {
		t.Errorf("three cottages did not grow the pool: %d -> %d", base, three)
	}
	if block := r.PoolSize(20, 0); block != 17 {
		t.Errorf("a housing block gives a pool of %d, want 17", block)
	}
	if r.PoolSize(0, 8) != 0 || r.PoolSize(0, 20) != 0 {
		t.Error("residents beyond the housing left NPCs to hire")
	}
	if r.PoolSize(0, 3) >= base {
		t.Error("player residents did not take homes from the NPC pool")
	}
}

func TestScarcityRaisesAndSurplusLowersTheWage(t *testing.T) {
	r := Default()
	pool := r.PoolSize(0, 0) // 5
	slack := r.NPCWage("village", r.Tightness(0, pool))
	balanced := r.NPCWage("village", r.Tightness(pool/2, pool)) // 2 of 5 = 40 %
	tight := r.NPCWage("village", r.Tightness(pool, pool))
	short := r.NPCWage("village", r.Tightness(2*pool, pool))
	if !(slack < balanced && balanced < tight && tight < short) {
		t.Fatalf("wage does not rise with scarcity: %d %d %d %d", slack, balanced, tight, short)
	}
	if slack != 21 || tight != 45 || short != 75 {
		t.Errorf("wages %d %d %d, want 21 45 75", slack, tight, short)
	}
	// The owner's example: more houses -> more workers -> the same demand is cheaper.
	demand := int64(4)
	few := r.NPCWage("village", r.Tightness(demand, r.PoolSize(0, 0)))
	many := r.NPCWage("village", r.Tightness(demand, r.PoolSize(20, 0)))
	if many >= few {
		t.Errorf("more houses did not lower the wage under the same demand: %d -> %d", few, many)
	}
	if r.NPCWage("village", 1_000_000) != 75 {
		t.Error("the wage kept rising past the curve")
	}
}

func TestMinimumWageIsAFloor(t *testing.T) {
	r := Default()
	r.BaseWage = 10
	if got := r.NPCWage("city", 0); got != 25 {
		t.Errorf("a city floor of 25 paid %d", got)
	}
	if got := r.NPCWage("village", 0); got != 10 {
		t.Errorf("a village floor of 10 paid %d", got)
	}
}

func TestProgressAndShifts(t *testing.T) {
	if ProgressBPS(120, 240) != 5_000 || ProgressBPS(0, 240) != 0 || ProgressBPS(300, 240) != BPS || ProgressBPS(1, 0) != BPS {
		t.Error("progress is wrong")
	}
	if ShiftsNeeded(240, 60) != 4 || ShiftsNeeded(241, 60) != 5 || ShiftsNeeded(0, 60) != 0 {
		t.Error("shifts needed is wrong")
	}
	if Default().Fee(30) != 1 {
		t.Errorf("5%% of 30 floors to 1, got %d", Default().Fee(30))
	}
}

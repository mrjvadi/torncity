package territory

import "testing"

// ray is a synthetic terrain: ground(bin, d) in metres.
type ray struct {
	observer int
	ground   func(bin, d int) int
}

func (r ray) ObserverGroundM() (int, error)      { return r.observer, nil }
func (r ray) PlanetRadiusM() int64               { return 6_371_000 }
func (r ray) GroundM(bin, _, d int) (int, error) { return r.ground(bin, d), nil }

// lookout is ADR 0042 8.1: base 1 km, cap 3 km, sight 3 km, a person at 2 m.
var lookout = Kind{Bins: 8, ObserverHeightM: 6, ClaimBaseM: 1000, ClaimCapM: 3000, SightM: 3000, StepM: 305,
	TargetHeightM: 2, RefractionNum: 7, RefractionDen: 6}

func flat0(int, int) int { return 0 }

func TestCoverage_FlatLookoutSeesToItsRange(t *testing.T) {
	c, err := Compute(ray{ground: flat0}, lookout, DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	for b := 0; b < 8; b++ {
		// 3000 m is not a multiple of the 305 m step: 9 steps = 2745 m.
		if c.Claim[b] != 2745 || c.Sight[b] != 2745 {
			t.Fatalf("bin %d claim %d sight %d, want 2745", b, c.Claim[b], c.Sight[b])
		}
	}
	if c.CapM != 3000 || c.ProminenceM != 0 {
		t.Fatalf("cap %d prominence %d", c.CapM, c.ProminenceM)
	}
}

func TestCoverage_RidgeBlocksTheClaimBehindIt(t *testing.T) {
	// a 60 m ridge 1.5 km north: the claim stops at the base on that ray, the
	// valley behind it is not claimed.
	g := func(bin, d int) int {
		if bin == 0 && d >= 1400 && d <= 1700 {
			return 60
		}
		return 0
	}
	c, _ := Compute(ray{ground: g}, lookout, DefaultParams())
	// the claim reaches the ridge itself (1525 m) and no further
	if c.Claim[0] != 1525 {
		t.Fatalf("claim behind the ridge %d, want 1525 (the ridge top)", c.Claim[0])
	}
	if c.Sight[0] != 1525 {
		t.Fatalf("sight %d reaches behind the ridge", c.Sight[0])
	}
	if c.Claim[4] != 2745 {
		t.Fatalf("open side claim %d", c.Claim[4])
	}
}

func TestCoverage_HighGroundDoublesTheCap(t *testing.T) {
	// watchtower on a 400 m ridge over a plain at 0: cap x2 (ADR 0042 4.4)
	tower := Kind{Bins: 8, ObserverHeightM: 12, ClaimBaseM: 1500, ClaimCapM: 5000, SightM: 8000, StepM: 305,
		TargetHeightM: 2, RefractionNum: 7, RefractionDen: 6}
	drop := func(h int) func(int, int) int {
		return func(_, d int) int {
			if d == 0 {
				return h
			}
			return 0
		}
	}
	c, _ := Compute(ray{observer: 400, ground: drop(400)}, tower, DefaultParams())
	if c.CapM != 10000 || c.ProminenceM != 400 {
		t.Fatalf("cap %d prominence %d, want 10000 and 400", c.CapM, c.ProminenceM)
	}
	if c.Claim[0] != 9760 { // 32 steps of 305
		t.Fatalf("claim %d", c.Claim[0])
	}
	// a lookout on a hill 200 m above the plain: cap x1.5 (worked example)
	c, _ = Compute(ray{observer: 200, ground: drop(200)}, lookout, DefaultParams())
	if c.CapM != 4500 {
		t.Fatalf("200 m hill cap %d, want 4500 (ADR 0042 worked example)", c.CapM)
	}
}

func TestCoverage_CurvatureHidesLowTargets(t *testing.T) {
	k := lookout
	k.SightM, k.ClaimCapM = 20_000, 20_000
	c, _ := Compute(ray{ground: flat0}, k, DefaultParams())
	if c.Sight[0] >= 17_000 {
		t.Fatalf("sight %d: the curve should hide the far end", c.Sight[0])
	}
	if c.Sight[0] < 10_000 {
		t.Fatalf("sight %d too short", c.Sight[0])
	}
}

func TestCoverage_DeterministicAndRefusesEmptyKind(t *testing.T) {
	g := func(b, d int) int { return (b*37 + d/50) % 90 }
	a, _ := Compute(ray{observer: 90, ground: g}, lookout, DefaultParams())
	b, _ := Compute(ray{observer: 90, ground: g}, lookout, DefaultParams())
	for i := range a.Claim {
		if a.Claim[i] != b.Claim[i] || a.Sight[i] != b.Sight[i] {
			t.Fatal("not deterministic")
		}
	}
	if _, err := Compute(ray{}, Kind{}, DefaultParams()); err == nil {
		t.Fatal("an empty kind must be refused")
	}
}

// ADR 0042 4.4 worked examples for the radar horizon.
func TestRadarHorizon(t *testing.T) {
	for _, c := range []struct{ h, t, wantKm int }{{30, 100, 64}, {30, 10000, 435}, {2030, 100, 227}, {2030, 10000, 598}} {
		got := RadarHorizonM(c.h, c.t)
		if d := got/1000 - c.wantKm; d < -1 || d > 1 {
			t.Errorf("RadarHorizonM(%d, %d) = %d m, want about %d km", c.h, c.t, got, c.wantKm)
		}
	}
}

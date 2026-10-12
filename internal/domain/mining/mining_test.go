package mining

import "testing"

func rules() Rules {
	return Rules{
		ReserveDivisor: 1000, ThinBelowBPS: 2000, ThinFloorBPS: 3000, LeanBelow: 350, RichFrom: 500,
		HoistKgMin: [2]int64{8, 32}, HoistDepthRefM: 30, HaulerKgMin: 10,
		MinConditionBPS: 3000, WaterLimitBPS: 6000,
	}.Normalise()
}

func TestGradeBands(t *testing.T) {
	r := rules()
	for _, c := range []struct {
		grade int32
		band  Band
		out   int64
	}{{200, BandLean, 8000}, {349, BandLean, 8000}, {350, BandMedium, 10000}, {499, BandMedium, 10000}, {500, BandRich, 12000}, {600, BandRich, 12000}} {
		if got := r.BandOf(c.grade); got != c.band {
			t.Fatalf("grade %d: band %s, want %s", c.grade, got, c.band)
		}
		if got := r.BandOutBPS(c.band); got != c.out {
			t.Fatalf("band %s: output %d, want %d", c.band, got, c.out)
		}
	}
}

func TestDepthSharesAddUp(t *testing.T) {
	r := rules()
	if r.Cumulative(DepthSurface) != 500 || r.Cumulative(DepthShaft1) != 3000 || r.Cumulative(DepthShaft2) != 7000 {
		t.Fatalf("cumulative shares %d %d %d", r.Cumulative(0), r.Cumulative(1), r.Cumulative(2))
	}
	// the deeper ground waits for steam: no method of the first release reaches it
	if r.Cumulative(DepthDeep) != 7000 {
		t.Fatalf("the deep band must not be reachable in the first release, got %d", r.Cumulative(DepthDeep))
	}
}

// A pit runs out and a minehead reaches the rest; the face thins gradually and nothing is taken that is not there.
func TestDepositDepletesGraduallyAndDeepensWithTheShaft(t *testing.T) {
	r := rules()
	const reserve = 20_000_000 // capacity 20000, the surface holds 1000
	if got := r.Reachable(reserve, DepthSurface); got != 1000 {
		t.Fatalf("surface reach = %d, want 1000", got)
	}
	var taken int64
	var last int64 = BPS
	var drops int
	for taken < 1000 {
		rem := r.Remaining(reserve, taken, DepthSurface)
		f := r.Thin(rem, r.Reachable(reserve, DepthSurface))
		if f > last {
			t.Fatalf("the yield rose from %d to %d while the face emptied", last, f)
		}
		if last-f > 300 {
			t.Fatalf("the yield fell from %d to %d in one step: a face must thin, not close overnight", last, f)
		}
		if f < last {
			drops++
		}
		last = f
		got := r.Grant(6, reserve, taken, DepthSurface)
		if got == 0 {
			t.Fatal("a face with ore left gave none")
		}
		taken += got
	}
	if taken != 1000 {
		t.Fatalf("the pit took %d, want exactly its surface share 1000", taken)
	}
	if drops < 20 {
		t.Fatalf("the yield thinned in %d steps only; it must fall gradually", drops)
	}
	if r.Grant(6, reserve, taken, DepthSurface) != 0 || r.Thin(0, 1000) != 0 {
		t.Fatal("an exhausted face still gives")
	}
	// the shaft reaches the next band: ore is there again, and the total never passes the reachable share
	if got := r.Remaining(reserve, taken, DepthShaft1); got != 5000 {
		t.Fatalf("after the pit the first shaft level has %d left, want 5000", got)
	}
	taken += r.Grant(1_000_000, reserve, taken, DepthShaft2)
	if taken != r.Reachable(reserve, DepthShaft2) {
		t.Fatalf("taken %d, want exactly the reachable %d", taken, r.Reachable(reserve, DepthShaft2))
	}
}

func TestThinIsLinearAndWhole(t *testing.T) {
	r := rules()
	if r.Thin(1000, 1000) != BPS || r.Thin(200, 1000) != BPS {
		t.Fatal("a face with a fifth or more left yields whole")
	}
	if got := r.Thin(100, 1000); got != 3000+(BPS-3000)*1000/2000 {
		t.Fatalf("half way down the thin zone = %d", got)
	}
	if got := r.Thin(1, 1000); got <= 3000 || got >= 3100 {
		t.Fatalf("the last unit yields near the floor, got %d", got)
	}
}

func TestMineheadOutputIsCappedByFaceHoistAndHaul(t *testing.T) {
	r := rules()
	// a 30 minute shift; the face breaks 100 kg
	face := int64(100)
	// a windlass at the surface lifts 8 kg a minute: 240 kg, more than the face; haulers 2 x 10 x 30 = 600
	kg, lim := Throughput(face, r.HoistKg(HoistWindlass, 0, true, 30), r.HaulKg(2, BPS, 30))
	if kg != 100 || lim != LimitFace {
		t.Fatalf("face-limited: %d %s", kg, lim)
	}
	// the same windlass at 60 m lifts a third: 80 kg, so the hoist binds
	hoist := r.HoistKg(HoistWindlass, 60, true, 30)
	if hoist != 80 {
		t.Fatalf("a windlass at 60 m lifts %d kg in 30 minutes, want 80", hoist)
	}
	kg, lim = Throughput(face, hoist, r.HaulKg(2, BPS, 30))
	if kg != 80 || lim != LimitHoist {
		t.Fatalf("hoist-limited: %d %s", kg, lim)
	}
	// a horse gin lifts four times that, so the face binds again
	if kg, lim = Throughput(face, r.HoistKg(HoistGin, 60, true, 30), r.HaulKg(2, BPS, 30)); kg != 100 || lim != LimitFace {
		t.Fatalf("a gin frees the hoist: %d %s", kg, lim)
	}
	// one hauler on an unmade way (half pace) carries 150 kg; the haul binds when the face breaks more
	kg, lim = Throughput(400, r.HoistKg(HoistGin, 30, true, 30), r.HaulKg(1, 5000, 30))
	if kg != 150 || lim != LimitHaul {
		t.Fatalf("haul-limited: %d %s", kg, lim)
	}
	// no banksman at the hoist, nothing is lifted; no hauler, nothing is carried
	if kg, lim = Throughput(face, r.HoistKg(HoistGin, 30, false, 30), 600); kg != 0 || lim != LimitHoist {
		t.Fatalf("an uncrewed hoist lifts %d (%s)", kg, lim)
	}
	if kg, lim = Throughput(face, 600, r.HaulKg(0, BPS, 30)); kg != 0 || lim != LimitHaul {
		t.Fatalf("no hauler carries %d (%s)", kg, lim)
	}
}

// An unsafe level is closed and stays on the books: its record is kept, it is not worked, and the timberman brings it back.
func TestAnUnsafeLevelClosesAndIsNeverDestroyed(t *testing.T) {
	r := rules()
	l := Level{ConditionBPS: 5000}
	if r.Check(l) != StatusOK {
		t.Fatal("a sound level is closed")
	}
	for i := 0; i < 50; i++ {
		l = Day(l, 300, 0, 0)
	}
	if l.ConditionBPS != 0 {
		t.Fatalf("props wear should bottom out at 0, got %d", l.ConditionBPS)
	}
	if r.Check(l) != StatusNeedsRepair {
		t.Fatalf("a level with rotten props is %s, want needs_repair", r.Check(l))
	}
	l = Set(l, 4000) // the timberman sets props
	if r.Check(l) != StatusOK {
		t.Fatalf("a repaired level is still %s", r.Check(l))
	}
	// water rises with no outlet and floods it; a pump (outflow) brings it back
	f := Level{ConditionBPS: BPS}
	for i := 0; i < 10; i++ {
		f = Day(f, 0, 900, 0)
	}
	if r.Check(f) != StatusFlooded {
		t.Fatalf("a level with no drainage is %s, want flooded", r.Check(f))
	}
	for i := 0; i < 10; i++ {
		f = Day(f, 0, 900, 2000)
	}
	if f.WaterBPS != 0 || r.Check(f) != StatusOK {
		t.Fatalf("a pumped level is %s, water %d", r.Check(f), f.WaterBPS)
	}
}

func TestAditNeedsAnOutletLowerThanTheFloor(t *testing.T) {
	r := rules()
	if n, ok := r.AditLengthM(20, 15); !ok || n != 750 {
		t.Fatalf("adit = %d %v, want 750 true", n, ok)
	}
	if _, ok := r.AditLengthM(10, 30); ok {
		t.Fatal("an outlet above the floor cannot drain by gravity")
	}
}

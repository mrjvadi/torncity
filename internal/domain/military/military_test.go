package military

import (
	"errors"
	"math"
	"testing"
)

func TestShare(t *testing.T) {
	for _, c := range []struct{ amount, bps, want int64 }{
		{10_000, 1000, 1000}, {999, 1000, 99}, {0, 5000, 0}, {math.MaxInt64, 10_000, math.MaxInt64}, {7, 0, 0},
	} {
		got, err := Share(c.amount, c.bps)
		if err != nil || got != c.want {
			t.Errorf("Share(%d, %d) = %d, %v; want %d", c.amount, c.bps, got, err, c.want)
		}
	}
	for _, c := range [][2]int64{{-1, 100}, {100, -1}, {100, 10_001}} {
		if _, err := Share(c[0], c[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("Share(%d, %d) = %v; want ErrInvalid", c[0], c[1], err)
		}
	}
}

func TestUpkeep(t *testing.T) {
	classes := map[string]Class{"fighter": {Code: "fighter", Branch: "air", Upkeep: 90},
		"tank": {Code: "tank", Branch: "ground", Upkeep: 35}}
	got, err := Upkeep(map[string]int64{"fighter": 4, "tank": 10, "dropped": 7}, classes)
	if err != nil || got != 4*90+10*35 {
		t.Fatalf("Upkeep = %d, %v", got, err)
	}
	if _, err := Upkeep(map[string]int64{"fighter": math.MaxInt64}, classes); !errors.Is(err, ErrOverflow) {
		t.Errorf("an overflowing upkeep = %v; want ErrOverflow", err)
	}
	if _, err := Upkeep(map[string]int64{"fighter": -1}, classes); !errors.Is(err, ErrInvalid) {
		t.Errorf("a negative count = %v; want ErrInvalid", err)
	}
}

func TestSettlePaysLevyAppropriationAndUpkeep(t *testing.T) {
	out, err := Settle(Period{
		Cities: []CityRevenue{
			{CityID: "a", Revenue: 10_000, Balance: 50_000},
			{CityID: "b", Revenue: 4_000, Balance: 100}, // can pay only what it holds
			{CityID: "c", Revenue: 0, Balance: 9_000},
		},
		RevenueShareBPS: 1000, DefenceBudgetBPS: 5000, Fund: 200, UpkeepDue: 600,
		Readiness: 8000, LossBPS: 1000, RecoveryBPS: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Levies) != 2 || out.Levies[0].Amount != 1000 || out.Levies[1].Amount != 100 || out.Levy != 1100 {
		t.Fatalf("levies = %+v, levy %d", out.Levies, out.Levy)
	}
	if out.Appropriation != 550 {
		t.Errorf("appropriation = %d, want 550", out.Appropriation)
	}
	if out.UpkeepPaid != 600 || out.Readiness != 8500 {
		t.Errorf("paid %d, readiness %d; want 600 and 8500", out.UpkeepPaid, out.Readiness)
	}
}

func TestSettleShortFundCostsReadinessNotDebt(t *testing.T) {
	out, err := Settle(Period{RevenueShareBPS: 1000, DefenceBudgetBPS: 3000, Fund: 100, UpkeepDue: 900,
		Readiness: 500, LossBPS: 1000, RecoveryBPS: 500})
	if err != nil {
		t.Fatal(err)
	}
	if out.UpkeepPaid != 100 || out.Readiness != 0 {
		t.Errorf("paid %d, readiness %d; want 100 and 0 (floored)", out.UpkeepPaid, out.Readiness)
	}
	full, _ := Settle(Period{Readiness: 9900, RecoveryBPS: 500})
	if full.Readiness != ReadinessFull || full.UpkeepPaid != 0 {
		t.Errorf("a period owing nothing: readiness %d (want capped at full), paid %d", full.Readiness, full.UpkeepPaid)
	}
	if _, err := Settle(Period{Readiness: 10_001}); !errors.Is(err, ErrInvalid) {
		t.Errorf("readiness over full = %v; want ErrInvalid", err)
	}
}

func TestBands(t *testing.T) {
	bands := []Band{{Code: "few", UpTo: 3}, {Code: "unit", UpTo: 12}, {Code: "large"}}
	if err := ValidateBands(bands); err != nil {
		t.Fatal(err)
	}
	for count, want := range map[int64]string{0: "", 1: "few", 3: "few", 4: "unit", 12: "unit", 13: "large", 5000: "large"} {
		if got := BandOf(bands, count); got != want {
			t.Errorf("BandOf(%d) = %q, want %q", count, got, want)
		}
	}
	for name, bad := range map[string][]Band{
		"empty":      nil,
		"unbounded":  {{Code: "a"}, {Code: "b", UpTo: 3}},
		"no last":    {{Code: "a", UpTo: 3}},
		"not rising": {{Code: "a", UpTo: 3}, {Code: "b", UpTo: 3}, {Code: "c"}},
		"repeated":   {{Code: "a", UpTo: 3}, {Code: "a"}},
	} {
		if err := ValidateBands(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v; want ErrInvalid", name, err)
		}
	}
}

func TestCheckExport(t *testing.T) {
	for _, c := range []struct {
		policy            ExportPolicy
		domestic, partner bool
		ok                bool
	}{
		{ExportDomestic, true, false, true},
		{ExportDomestic, false, true, false},
		{ExportPartners, false, true, true},
		{ExportPartners, false, false, false},
		{ExportOpen, false, false, true},
	} {
		err := CheckExport(c.policy, c.domestic, c.partner)
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrExportDenied)) {
			t.Errorf("CheckExport(%d, %t, %t) = %v", c.policy, c.domestic, c.partner, err)
		}
	}
}

// TestDetectionRangeFollowsTheFourthRoot checks the radar equation's shape:
// a 1 m² target is seen at the quoted range, halving the cross-section costs
// about 16%, and a ten-thousandth of it cuts the range tenfold.
func TestDetectionRangeFollowsTheFourthRoot(t *testing.T) {
	const radar = 200
	if got := DetectionRangeKM(radar, ReferenceRCS, 10_000); got != radar {
		t.Errorf("a 1 m² target: %d km, want %d", got, radar)
	}
	if got := DetectionRangeKM(radar, ReferenceRCS/2, 10_000); got != 168 {
		t.Errorf("half the cross-section: %d km, want 168 (16%% less)", got)
	}
	if got := DetectionRangeKM(radar, 10*ReferenceRCS, 10_000); got != 355 {
		t.Errorf("ten times the cross-section: %d km, want 355", got)
	}
	// 10 m² against 1 cm² (10000 : 1): exactly a tenth of the range.
	big, small := DetectionRangeKM(1000, 10_000, 10_000), DetectionRangeKM(1000, 1, 10_000)
	if big/small != 10 {
		t.Errorf("10 m² at %d km, 0.001 m² at %d km; want a tenfold ratio", big, small)
	}
	// A stealth fighter (0.005 m²) against a fighter radar and a VHF array
	// that sees it twenty times larger.
	stealth := DetectionRangeKM(radar, 5, 10_000)
	vhf := DetectionRangeKM(radar, 5, 200_000)
	if stealth != 53 || vhf <= stealth {
		t.Errorf("stealth seen at %d km, by VHF at %d km; want 53 and further", stealth, vhf)
	}
	for _, zero := range [][3]int64{{0, 1000, 10_000}, {100, 0, 10_000}, {100, 1000, 0}} {
		if got := DetectionRangeKM(zero[0], zero[1], zero[2]); got != 0 {
			t.Errorf("DetectionRangeKM(%v) = %d, want 0", zero, got)
		}
	}
}

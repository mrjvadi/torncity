package settlement

import (
	"math"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// realConfigParams loads the shipped configs/config.yml and turns its
// settlement section into spawn Params, exactly as cmd/game does.
func realConfigParams(t *testing.T) Params {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))
	cfg, err := config.Load(filepath.Join(root, "configs", "config.yml"))
	if err != nil {
		t.Fatalf("loading configs/config.yml: %v", err)
	}
	pen, err := cfg.Settlement.BiomePenaltyMap()
	if err != nil {
		t.Fatal(err)
	}
	return Params{
		MinSpawnDistanceKm: cfg.Settlement.MinSpawnDistanceKm,
		ThreatRadiusKm:     cfg.Settlement.ThreatRadiusKm,
		SearchMaxCells:     cfg.Settlement.SearchMaxCells,
		SearchMaxAttempts:  cfg.Settlement.SearchMaxAttempts,
		ExcludedBiomes:     cfg.Settlement.ExcludedBiomes,
		MaxAbsLatitudeDeg:  cfg.Settlement.MaxAbsLatitudeDeg,
		BiomePenalties:     pen,
		Site: SiteRules{GridLots: cfg.Settlement.VillageGridLots,
			MinBuildableShareBps: cfg.Settlement.MinBuildableLotShareBps,
			MaxShiftLots:         cfg.Settlement.GridShiftMaxLots},
	}
}

// realWorld is the real planet: seed 42, the shipped world content and
// worldgen.DefaultParams (which configs/config.yml's worldgen section
// matches).
func realWorld(t *testing.T) *worldgen.World {
	t.Helper()
	pack, err := content.LoadWorldGen(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	c, err := pack.ToContent()
	if err != nil {
		t.Fatal(err)
	}
	w, err := worldgen.Generate(42, worldgen.DefaultParams(), c)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func spawnSeries(t *testing.T, w *worldgen.World, p Params, count int) []Candidate {
	t.Helper()
	var existing []ExistingSettlement
	out := make([]Candidate, 0, count)
	for n := int64(1); n <= int64(count); n++ {
		c, err := FindSpawn(w, existing, n, p)
		if err != nil {
			t.Fatalf("FindSpawn(%d): %v", n, err)
		}
		out = append(out, c)
		existing = append(existing, ExistingSettlement{CellID: c.CellID, TierWeight: 1})
	}
	return out
}

// TestRealWorldSpawnsAreHabitableAndSpread is the regression for villages
// founded on polar ice: the first 200 spawns of the real world (seed 42, the
// shipped config) are never on ice, water or beyond the latitude cap, are
// spread over latitude, and are reproducible.
func TestRealWorldSpawnsAreHabitableAndSpread(t *testing.T) {
	if testing.Short() {
		t.Skip("generates the full-size planet")
	}
	w := realWorld(t)
	p := realConfigParams(t)
	const count = 200
	spawns := spawnSeries(t, w, p, count)

	excluded := map[string]bool{}
	for _, c := range p.ExcludedBiomes {
		excluded[c] = true
	}
	if !excluded["polar_ice"] {
		t.Fatalf("shipped config does not exclude polar_ice: %v", p.ExcludedBiomes)
	}
	known := map[string]bool{}
	for _, b := range w.Content.Biomes {
		known[b.Code] = true
	}
	for c := range excluded {
		if !known[c] {
			t.Errorf("config excludes unknown biome %q", c)
		}
	}
	for c := range p.BiomePenalties {
		if !known[c] {
			t.Errorf("config penalises unknown biome %q", c)
		}
	}

	hist := map[int]int{} // 10-degree |lat| bands
	biomes := map[string]int{}
	north, south := 0, 0
	for i, c := range spawns {
		cell := w.Cells[c.CellID]
		biome := w.BiomeCode(c.CellID)
		biomes[biome]++
		if cell.IsOcean || cell.IsLake {
			t.Errorf("spawn %d on water (cell %d)", i+1, c.CellID)
		}
		if excluded[biome] {
			t.Errorf("spawn %d on excluded biome %s (lat %.1f)", i+1, biome, c.LatDeg)
		}
		if math.Abs(c.LatDeg) > p.MaxAbsLatitudeDeg {
			t.Errorf("spawn %d at latitude %.1f beyond the cap %.1f", i+1, c.LatDeg, p.MaxAbsLatitudeDeg)
		}
		if c.LatDeg >= 0 {
			north++
		} else {
			south++
		}
		hist[int(math.Abs(c.LatDeg))/10*10]++
	}

	// Spread: both hemispheres and at least four different 10-degree bands,
	// no single band holding most of the villages.
	if north < count/5 || south < count/5 {
		t.Errorf("hemispheres unbalanced: north=%d south=%d", north, south)
	}
	if len(hist) < 4 {
		t.Errorf("only %d latitude bands used: %v", len(hist), hist)
	}
	for band, n := range hist {
		if n > count*6/10 {
			t.Errorf("band %d holds %d of %d spawns", band, n, count)
		}
	}

	bands := make([]int, 0, len(hist))
	for b := range hist {
		bands = append(bands, b)
	}
	sort.Ints(bands)
	var sb strings.Builder
	for _, b := range bands {
		sb.WriteString("\n  |lat| ")
		sb.WriteString(itoa(b))
		sb.WriteString("-")
		sb.WriteString(itoa(b + 10))
		sb.WriteString(": ")
		sb.WriteString(itoa(hist[b]))
	}
	t.Logf("latitude histogram of the first %d spawns (north %d / south %d):%s", count, north, south, sb.String())
	t.Logf("biome mix: %v", biomes)
	for i := 0; i < 10; i++ {
		c := spawns[i]
		t.Logf("spawn %2d idx=%d lat=%6.1f lon=%7.1f %s", i+1, c.LatticeIndex, c.LatDeg, c.LonDeg, w.BiomeCode(c.CellID))
	}

	// Determinism: the same world, params and N give the same spots.
	again := spawnSeries(t, w, p, 40)
	for i := range again {
		if again[i] != spawns[i] {
			t.Fatalf("spawn %d is not reproducible: %+v vs %+v", i+1, again[i], spawns[i])
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestLatticePointBand_StaysInBandAndIsPure(t *testing.T) {
	for n := int64(1); n <= 5000; n++ {
		lat, lon := LatticePointBand(n, 70)
		if math.Abs(lat) > 70.0000001 || lon < -180 || lon > 180 {
			t.Fatalf("LatticePointBand(%d, 70) = (%v,%v) out of band", n, lat, lon)
		}
		lat2, lon2 := LatticePointBand(n, 70)
		if lat != lat2 || lon != lon2 {
			t.Fatalf("LatticePointBand(%d) is not deterministic", n)
		}
	}
	// A band outside (0,90) is the whole sphere, i.e. LatticePoint.
	for _, n := range []int64{1, 7, 999} {
		a1, a2 := LatticePointBand(n, 0)
		b1, b2 := LatticePoint(n)
		if a1 != b1 || a2 != b2 {
			t.Fatalf("band 0 differs from LatticePoint at %d", n)
		}
	}
}

// TestBiomePenaltyLowersScoreButKeepsCellEligible pins the split between
// "uninhabitable" (excluded) and "harsh" (penalised).
func TestExcludedBiomeIsRejectedPenalisedBiomeIsNot(t *testing.T) {
	w := testWorld(t)
	var iceCell, deserts int32 = -1, -1
	for id, c := range w.Cells {
		if c.IsOcean || c.IsLake {
			continue
		}
		switch w.BiomeCode(int32(id)) {
		case "polar_ice":
			if iceCell < 0 {
				iceCell = int32(id)
			}
		case "desert":
			if deserts < 0 {
				deserts = int32(id)
			}
		}
	}
	rules := newBiomeRules(w, Params{ExcludedBiomes: []string{"polar_ice"}, BiomePenalties: map[string]float64{"desert": 4}})
	if iceCell >= 0 && eligible(w, iceCell, nil, rules, 30) {
		t.Errorf("polar ice cell %d is eligible", iceCell)
	}
	if deserts >= 0 {
		if !eligible(w, deserts, nil, rules, 30) {
			t.Errorf("desert cell %d must stay eligible", deserts)
		}
		if rules.penalty[w.Cells[deserts].BiomeIdx] != 4 {
			t.Errorf("desert penalty not applied")
		}
	}
	if iceCell < 0 || deserts < 0 {
		t.Skip("test world lacks ice or desert land")
	}
}

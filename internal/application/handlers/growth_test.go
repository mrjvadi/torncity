package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
)

// seeded settlements for the dual-read tests: the founding kit and the four
// founding grants, then a grown village, a town-like and a city-like one, built
// only from codes of the shipped catalogue.
var (
	foundingKit    = []string{"civic_hall", "road", "barter_post", "granary"}
	foundingGrants = []string{"oral_tradition", "communal_watch", "kin_apprenticeship", "barter_ring"}
)

func seededStanding(buildings, knowledge []string) application.SettlementStanding {
	var st application.SettlementStanding
	for i, b := range buildings {
		st.Buildings = append(st.Buildings, application.SettlementBuildingInstance{ID: fmt.Sprint(i), TypeCode: b, Status: "complete"})
	}
	for _, k := range knowledge {
		st.Knowledge = append(st.Knowledge, application.SettlementKnowledgeOwned{Code: k, AcquiredVia: "researched"})
	}
	return st
}

func seededSettlements() []SweepSettlement {
	grown := append(append([]string{}, foundingKit...), "cottage", "health_house", "teaching_circle", "watch_hut", "woodcutter_camp", "carpentry_workshop")
	grownK := append(append([]string{}, foundingGrants...), "basic_literacy", "record_keeping", "basic_medicine", "carpentry", "masonry", "masonry_ii", "smithing", "road_code")
	town := append(append([]string{}, grown...), "school", "market", "clinic", "police_post", "smithy", "masonry_workshop")
	townK := append(append([]string{}, grownK...), "basic_medicine_ii", "state_school", "periodic_market", "formal_constabulary")
	city := append(append([]string{}, town...), "bank", "port", "airport", "barracks", "mine")
	return []SweepSettlement{
		{ID: "s-fresh", Code: "fresh", Tier: "village", Standing: seededStanding(foundingKit, foundingGrants)},
		{ID: "s-grown", Code: "grown", Tier: "village", Standing: seededStanding(grown, grownK)},
		{ID: "s-town", Code: "town", Tier: "town", Standing: seededStanding(town, townK)},
		{ID: "s-city", Code: "city", Tier: "city", Standing: seededStanding(city, townK)},
	}
}

func wsettleBuilding(code string) wsettle.BuildingNeed { return wsettle.BuildingNeed{Code: code} }

func rowOf(t *testing.T, rows []SweepRow, kind, code string) SweepRow {
	t.Helper()
	for _, r := range rows {
		if r.Kind == kind && r.Code == code {
			return r
		}
	}
	t.Fatalf("no sweep row for %s/%s", kind, code)
	return SweepRow{}
}

// TestSweepJudgesEveryTagBothWays: every availability tag gets a row for every
// seeded settlement, and a tag is compared unless its gate is deferred or it is
// the neutral city's.
func TestSweepJudgesEveryTagBothWays(t *testing.T) {
	snap := shippedSnapshot(t)
	tags := snap.AllAvailabilityTags()
	if len(tags) < 400 {
		t.Fatalf("only %d tags: the shipped availability.yml is not what Appendix A was generated from", len(tags))
	}
	for _, s := range seededSettlements() {
		rows := Sweep(snap, s, 10000)
		if len(rows) != len(tags) {
			t.Fatalf("%s: %d rows for %d tags", s.Code, len(rows), len(tags))
		}
		sum := Summarise(rows)
		if sum.Compared+sum.Skipped != len(tags) {
			t.Fatalf("%s: compared %d + skipped %d != %d", s.Code, sum.Compared, sum.Skipped, len(tags))
		}
		for _, r := range rows {
			skip := r.Class == SweepDeferred || r.Class == SweepSupport
			if r.Compared == skip {
				t.Errorf("%s %s/%s: class %s but compared=%v", s.Code, r.Kind, r.Code, r.Class, r.Compared)
			}
		}
		t.Logf("%s (%s): %d compared, %d agree, %d disagree %v, %d not comparable",
			s.Code, s.Tier, sum.Compared, sum.Agree, sum.Disagree, sum.ByClass, sum.Skipped)
	}
}

// TestAppendixAOpenRowsAgreeWithTheTier: a class A row (open from founding,
// stage village) is offered by the tier and by the capabilities in every
// settlement: dropping the label changes nothing for it.
func TestAppendixAOpenRowsAgreeWithTheTier(t *testing.T) {
	snap := shippedSnapshot(t)
	open := 0
	for _, s := range seededSettlements() {
		for _, r := range Sweep(snap, s, 10000) {
			if r.Class != SweepOpen || r.Stage != content.StageVillage {
				continue
			}
			open++
			if !r.TierAnswer || !r.CapabilityAnswer {
				t.Errorf("%s %s/%s: an open village-stage row is tier=%v capabilities=%v", s.Code, r.Kind, r.Code, r.TierAnswer, r.CapabilityAnswer)
			}
		}
	}
	if open < 4*40 {
		t.Fatalf("only %d open village-stage rows swept: the appendix has well over 40 per settlement", open)
	}
}

// TestAppendixADGatesFollowWhatTheSettlementHas: the new gates ADR 0044 adds
// where `requires` was empty, row by row, against the seeded settlements.
func TestAppendixADGatesFollowWhatTheSettlementHas(t *testing.T) {
	snap := shippedSnapshot(t)
	by := map[string][]SweepRow{}
	for _, s := range seededSettlements() {
		by[s.Code] = Sweep(snap, s, 10000)
	}
	type want struct {
		row                   int
		kind, code            string
		settlement            string
		capAnswer, tierAnswer bool
		why                   string
	}
	cases := []want{
		// row 3: housing L1 + masonry_ii. The kit has no housing and no masonry; the grown village has both.
		{3, "building", "housing_block", "fresh", false, true, "a village lists it today; capabilities: no housing and no masonry_ii yet"},
		{3, "building", "housing_block", "grown", true, true, "both: cottage + masonry_ii"},
		// row 86: driving school = education L1 + a road.
		{86, "course", "driving_licence", "fresh", false, false, "no class building"},
		{86, "course", "driving_licence", "grown", true, false, "teaching_circle (education L1) and a road stand; the tier says town"},
		{86, "course", "driving_licence", "town", true, true, "both"},
		// row 112: a centre needs market L2 + civic hall.
		{112, "place", "city_centre", "fresh", false, false, "barter_post is market L1"},
		{112, "place", "city_centre", "town", true, false, "market L2 and a civic hall; the tier says city"},
		{112, "place", "city_centre", "city", true, true, "both"},
		// row 292: a courier needs a road and market L1: the fresh village already has both.
		{292, "company_type", "courier", "fresh", true, false, "road + barter_post; the tier says town"},
		{292, "company_type", "courier", "town", true, true, "both"},
		// row 187: coffee beans are grown on a farm (food L1).
		{187, "component", "coffee_bean", "fresh", false, false, "no farm"},
		// rows 336 to 339: a branch needs barracks.
		{336, "branch", "ground", "fresh", false, false, "no barracks"},
		{336, "branch", "ground", "city", true, true, "barracks stand; the tier says country, which a city has around it"},
	}
	for _, c := range cases {
		r := rowOf(t, by[c.settlement], c.kind, c.code)
		if r.Class != SweepGrowth {
			t.Errorf("%s/%s: class %s, want %s", c.kind, c.code, r.Class, SweepGrowth)
		}
		if r.Row != c.row {
			t.Errorf("%s/%s: appendix row %d, want %d", c.kind, c.code, r.Row, c.row)
		}
		if r.CapabilityAnswer != c.capAnswer || r.TierAnswer != c.tierAnswer {
			t.Errorf("%s %s/%s (%s): capabilities=%v tier=%v, want capabilities=%v tier=%v", c.settlement, c.kind, c.code, c.why,
				r.CapabilityAnswer, r.TierAnswer, c.capAnswer, c.tierAnswer)
		}
	}
	// Deferred rows say so and are never compared.
	for _, k := range [][2]string{{"office", "mayor"}, {"government_action", "country.war"}, {"treaty_type", "alliance"},
		{"place", "bus_terminal"}, {"transport_mode", "train"}, {"career", "hospitality"}, {"sleep_spot", "hostel"}, {"finance_service", "ton_exchange"}} {
		r := rowOf(t, by["city"], k[0], k[1])
		if r.Class != SweepDeferred || r.Compared {
			t.Errorf("%s/%s: class %s compared %v, want a deferred row", k[0], k[1], r.Class, r.Compared)
		}
	}
}

// TestSweepDisagreementReport prints the report the owner asked for: where the
// capabilities differ from the tiers, by gate class, on the seeded settlements.
func TestSweepDisagreementReport(t *testing.T) {
	snap := shippedSnapshot(t)
	for _, s := range seededSettlements() {
		var lines []string
		for _, r := range Sweep(snap, s, 10000) {
			if r.Disagrees() {
				lines = append(lines, fmt.Sprintf("  %-16s %-34s tier=%-5v capabilities=%-5v stage=%-8s %s", r.Class, r.Kind+"/"+r.Code, r.TierAnswer, r.CapabilityAnswer, r.Stage, r.Missing))
			}
		}
		sort.Strings(lines)
		t.Logf("%s (%s): %d disagreements\n%s", s.Code, s.Tier, len(lines), strings.Join(lines, "\n"))
	}
}

// --- the gate -----------------------------------------------------------------

type fakeStanding struct {
	mu    sync.Mutex
	calls int
	st    application.SettlementStanding
}

func (f *fakeStanding) Standing(context.Context, string) (application.SettlementStanding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.st, nil
}

type fakeStore struct {
	mu   sync.Mutex
	rows []application.GrowthDisagreement
	fail bool
}

func (f *fakeStore) Record(_ context.Context, rows []application.GrowthDisagreement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return fmt.Errorf("store down")
	}
	f.rows = append(f.rows, rows...)
	return nil
}

func (f *fakeStore) List(context.Context, int) ([]application.GrowthDisagreementRow, error) {
	return nil, nil
}

func withGrowth(t *testing.T, mode string, rd application.GrowthStandingReader, st application.GrowthDisagreementStore) *GrowthGate {
	t.Helper()
	g := ConfigureGrowth(GrowthConfig{Mode: mode, CacheTTL: time.Minute, RuinedBPS: 10000, FlushInterval: time.Hour}, rd, st, nil)
	t.Cleanup(func() { ConfigureGrowth(GrowthConfig{Mode: GrowthModeOff}, nil, nil, nil) })
	return g
}

func TestGrowthOffComputesNothing(t *testing.T) {
	g := withGrowth(t, GrowthModeOff, nil, nil)
	if g != nil || currentGrowth() != nil {
		t.Fatal("off must leave no gate")
	}
	snap := shippedSnapshot(t)
	tag, _ := snap.AvailabilityTag("building", "housing_block")
	if got := g.Decide("x", "c", snap, capabilitiesOf(snap, seededStanding(nil, nil), 10000), true, tag, true); !got {
		t.Fatal("a nil gate must return the tier answer")
	}
	if len(g.Pending()) != 0 {
		t.Fatal("a nil gate meters nothing")
	}
}

func TestGrowthShadowKeepsTheTierAndMetersTheDisagreement(t *testing.T) {
	store := &fakeStore{}
	g := withGrowth(t, GrowthModeShadow, nil, store)
	snap := shippedSnapshot(t)
	caps := capabilitiesOf(snap, seededStanding(foundingKit, foundingGrants), 10000)
	// courier: the tier (stage town) refuses a village; the capabilities (road + market L1) accept.
	tag, _ := snap.AvailabilityTag("company_type", "courier")
	if got := g.Decide("hubs", "city-1", snap, caps, true, tag, false); got {
		t.Fatal("shadow mode must keep the tier answer (refused)")
	}
	g.Decide("hubs", "city-1", snap, caps, true, tag, false)
	// agreement is not metered
	open, _ := snap.AvailabilityTag("building", "road")
	g.Decide("hubs", "city-1", snap, caps, true, open, true)
	// a content city (found=false) is never judged
	g.Decide("hubs", "city-2", snap, caps, false, tag, false)
	p := g.Pending()
	if len(p) != 1 || p[0].Count != 2 || p[0].TierAnswer || !p[0].CapabilityAnswer || p[0].Kind != "company_type" || p[0].Site != "hubs" {
		t.Fatalf("pending = %+v", p)
	}
	if err := g.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 1 || store.rows[0].Count != 2 || len(g.Pending()) != 0 {
		t.Fatalf("flush: stored %+v pending %+v", store.rows, g.Pending())
	}
	// a failed flush keeps the counts
	store.fail = true
	g.Decide("hubs", "city-1", snap, caps, true, tag, false)
	if err := g.Flush(context.Background()); err == nil {
		t.Fatal("a store error must surface")
	}
	if p := g.Pending(); len(p) != 1 || p[0].Count != 1 {
		t.Fatalf("a failed flush lost its counts: %+v", p)
	}
}

func TestGrowthAuthoritativeAndStagelessTagsFollowTheCapabilities(t *testing.T) {
	snap := shippedSnapshot(t)
	caps := capabilitiesOf(snap, seededStanding(foundingKit, foundingGrants), 10000)
	courier, _ := snap.AvailabilityTag("company_type", "courier")

	g := withGrowth(t, GrowthModeShadow, nil, nil)
	stageless := courier
	stageless.Stage = ""
	if got := g.Decide("hubs", "c", snap, caps, true, stageless, false); !got {
		t.Fatal("a tag with no stage can only be answered by the capabilities")
	}
	g = withGrowth(t, GrowthModeAuthoritative, nil, nil)
	if !g.Authoritative() {
		t.Fatal("authoritative mode must say so")
	}
	if got := g.Decide("hubs", "c", snap, caps, true, courier, false); !got {
		t.Fatal("authoritative mode must follow the capabilities")
	}
	// deferred gates are never overruled
	mayor, _ := snap.AvailabilityTag("office", "mayor")
	if got := g.Decide("hubs", "c", snap, caps, true, mayor, false); got {
		t.Fatal("a deferred (charter) gate keeps the tier answer")
	}
}

func TestGrowthOutsideTxIsCachedAndSkipsContentCities(t *testing.T) {
	rd := &fakeStanding{st: seededStanding(foundingKit, foundingGrants)}
	g := withGrowth(t, GrowthModeShadow, rd, nil)
	snap := shippedSnapshot(t)
	for i := 0; i < 3; i++ {
		caps, found, err := g.OutsideTx(context.Background(), snap, "city-1")
		if err != nil || !found || !caps.Stands(wsettleBuilding("road")) {
			t.Fatalf("OutsideTx = %v %v", found, err)
		}
	}
	if rd.calls != 1 {
		t.Fatalf("the pool reader was called %d times for 3 asks within the ttl", rd.calls)
	}
	empty := withGrowth(t, GrowthModeShadow, &fakeStanding{}, nil)
	if _, found, _ := empty.OutsideTx(context.Background(), snap, "neutral"); found {
		t.Fatal("a content city has no capabilities to compute")
	}
}

func TestHubsOfferedDualReadMetersButKeepsTheTier(t *testing.T) {
	snap := shippedSnapshot(t)
	g := withGrowth(t, GrowthModeShadow, nil, nil)
	st := seededStanding(foundingKit, foundingGrants)
	here := hubSettlement{stageRank: content.StageRank("village"), stands: standsIn(snap, st.Buildings),
		growth: g, cityID: "city-1", caps: g.FromRows(snap, st), capsFound: true}
	// courier is a town thing by tier: refused; capabilities (road + market L1) would accept
	if here.offered(snap, "company_type", "courier") {
		t.Fatal("the tier must stay authoritative in shadow mode")
	}
	if p := g.Pending(); len(p) != 1 || p[0].Code != "courier" {
		t.Fatalf("pending = %+v", p)
	}
	// with the flag off the same settlement behaves exactly as before and meters nothing
	here.growth = nil
	if here.offered(snap, "company_type", "courier") {
		t.Fatal("off: the tier answer")
	}
}

func TestBuildingListingComparesTheTierEffectAlone(t *testing.T) {
	snap := shippedSnapshot(t)
	g := withGrowth(t, GrowthModeShadow, nil, nil)
	st := seededStanding(foundingKit, foundingGrants)
	caps := g.FromRows(snap, st)
	// school (education L2) needs education L1 standing: a fresh village has none, so the capabilities
	// agree with the tier (not listed) and nothing is metered
	if got := g.DecideBuilding("build_menu", "c", snap, caps, true, "school", false); got {
		t.Fatal("shadow keeps the tier answer")
	}
	// a building the village lists and could build, but whose Appendix A gate the village lacks (housing_block)
	if got := g.DecideBuilding("build_menu", "c", snap, caps, true, "housing_block", true); !got {
		t.Fatal("shadow keeps the tier answer")
	}
	p := g.Pending()
	if len(p) != 1 || p[0].Code != "housing_block" || !p[0].TierAnswer || p[0].CapabilityAnswer {
		t.Fatalf("pending = %+v", p)
	}
}

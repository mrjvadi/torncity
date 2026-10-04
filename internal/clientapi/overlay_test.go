package clientapi

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/statesync"
)

// A small village: the hall with a road beside it, a woodcutter camp and a
// watch hut on the road, a cottage that is Resident's own with no road
// near, and a camp going up by work.
func overlayRows() []application.SettlementBuildingInstance {
	now := time.Now()
	return []application.SettlementBuildingInstance{
		{ID: "hall", TypeCode: "civic_hall", Status: "complete", LotX: 0, LotY: 0, CompletedAt: &now},
		{ID: "road1", TypeCode: "road", Status: "complete", LotX: 2, LotY: 0},
		{ID: "camp", TypeCode: "woodcutter_camp", Status: "complete", LotX: 3, LotY: 0},
		{ID: "hut", TypeCode: "watch_hut", Status: "complete", LotX: 2, LotY: 1},
		{ID: "house", TypeCode: "cottage", Status: "complete", LotX: 9, LotY: 9},
		{ID: "site", TypeCode: "watch_hut", Status: "building", LotX: 5, LotY: 5, WorkRequired: 10},
	}
}

func overlayFor(t *testing.T, kind string, resident bool, id string, f application.SettlementFacts) map[string]statesync.BuildingOverlay {
	t.Helper()
	in := overlayInput{Snap: testRegistry(t).Current(), Tier: "town", Rows: overlayRows(),
		Owners: map[string]string{"house": residentID}, Viewer: OverlayViewer{ID: id, Kind: kind, Resident: resident},
		Facts: f, StockBase: 60, ConcurrentN: 2}
	out := map[string]statesync.BuildingOverlay{}
	for _, o := range buildingOverlays(in) {
		out[o.ID] = o
	}
	return out
}

func facts() application.SettlementFacts {
	return application.SettlementFacts{Treasury: 100000, StockUnits: map[string]int64{}, Shifts: map[string]int{},
		OpenJobs: map[string]bool{"site": true}, Owned: map[string]string{"record_keeping": "research"}}
}

func has(o statesync.BuildingOverlay, a string) bool {
	for _, x := range o.Actions {
		if x == a {
			return true
		}
	}
	return false
}

func TestHeadSeesManagingVerbs(t *testing.T) {
	o := overlayFor(t, statesync.ViewerHead, true, headID, facts())
	hall := o["hall"]
	for _, a := range []string{"info", "treasury", "research"} {
		if !has(hall, a) {
			t.Errorf("head on the hall: no %q in %v", a, hall.Actions)
		}
	}
	if has(hall, "elections") {
		t.Error("no election is open, yet the hall offers elections")
	}
	hut := o["hut"]
	if !has(hut, "upgrade") || !hut.CanUpgrade {
		t.Errorf("head with money and knowledge should upgrade the watch hut: %+v", hut)
	}
	if !has(hut, "demolish") {
		t.Error("head may demolish a civic building")
	}
	camp := o["camp"]
	if !has(camp, "take_shift") || !has(camp, "workers") {
		t.Errorf("head (a resident) on a workplace: %v", camp.Actions)
	}
	if camp.Staff == nil || camp.Staff.Have != 0 || camp.Staff.Need != 3 {
		t.Errorf("camp staff = %+v, want 0/3", camp.Staff)
	}
	if camp.Status != statesync.BuildingIdle || !containsReason(camp.Reasons, statesync.ReasonNoStaff) {
		t.Errorf("an unstaffed camp is idle for no_staff: %+v", camp)
	}
	site := o["site"]
	if site.Status != statesync.BuildingRaising || !has(site, "help_build") || !has(site, "cancel") || !has(site, "workers") {
		t.Errorf("site under construction: %+v", site)
	}
	if _, ok := o["road1"]; ok {
		t.Error("a standing road has no overlay entry")
	}
	// The cottage is Resident's: the head does not manage it.
	if h := o["house"]; has(h, "demolish") || has(h, "upgrade") {
		t.Errorf("head must not manage a resident's house: %v", h.Actions)
	}
}

func TestUpgradeNeedsMoneyKnowledgeAndABuilder(t *testing.T) {
	f := facts()
	f.Treasury = 10
	if o := overlayFor(t, statesync.ViewerHead, true, headID, f); o["hut"].CanUpgrade || !has(o["hut"], "upgrade") {
		t.Errorf("no money: verb stays, can_upgrade off: %+v", o["hut"])
	}
	f = facts()
	f.Owned = map[string]string{}
	if o := overlayFor(t, statesync.ViewerHead, true, headID, f); o["hut"].CanUpgrade {
		t.Error("without the knowledge the step cannot start")
	}
	// the site under construction takes the only builder
	rows := overlayRows()
	in := overlayInput{Snap: testRegistry(t).Current(), Tier: "town", Rows: rows,
		Viewer: OverlayViewer{ID: headID, Kind: statesync.ViewerHead, Resident: true}, Facts: facts(), StockBase: 60, ConcurrentN: 1}
	for _, o := range buildingOverlays(in) {
		if o.ID == "hut" && o.CanUpgrade == false {
			return
		}
	}
	t.Error("the single builder is busy with the site; can_upgrade must be off")
}

func TestResidentVersusVisitor(t *testing.T) {
	res := overlayFor(t, statesync.ViewerMember, true, strangerID, facts())
	hut := res["hut"]
	if hut.CanUpgrade || has(hut, "upgrade") || has(hut, "demolish") {
		t.Errorf("a resident who is not the head manages nothing: %v", hut.Actions)
	}
	if !has(res["camp"], "take_shift") {
		t.Error("a resident may take a shift")
	}
	if has(res["camp"], "workers") {
		t.Error("only the head posts jobs for a workplace")
	}
	if !has(res["hall"], "treasury") || has(res["hall"], "research") {
		t.Errorf("a resident sees the treasury, not research: %v", res["hall"].Actions)
	}
	if !has(res["site"], "help_build") {
		t.Error("a resident may help raise a site")
	}

	vis := overlayFor(t, statesync.ViewerPublic, false, strangerID, application.SettlementFacts{})
	for id, o := range vis {
		if len(o.Actions) != 1 || o.Actions[0] != "info" || o.CanUpgrade || o.Staff != nil {
			t.Errorf("a visitor gets info only on %s: %+v", id, o)
		}
	}
	if _, ok := vis["site"]; ok {
		t.Error("a visitor is not told about what is being built")
	}
	if vis["hut"].Tier != 1 {
		t.Errorf("the tier is public: %+v", vis["hut"])
	}
}

func TestOwnHouseVersusPublicBuilding(t *testing.T) {
	owner := overlayFor(t, statesync.ViewerMember, true, residentID, facts())
	house := owner["house"]
	if !has(house, "demolish") || !has(house, "road") {
		t.Errorf("the owner manages the house and may ask for a road: %v", house.Actions)
	}
	if !containsReason(house.Reasons, statesync.ReasonNoRoad) || house.Status != statesync.BuildingIdle {
		t.Errorf("a house away from the road: %+v", house)
	}
	if has(owner["hut"], "demolish") {
		t.Error("a resident does not manage the village's hut")
	}
	other := overlayFor(t, statesync.ViewerMember, true, strangerID, facts())
	if has(other["house"], "demolish") || has(other["house"], "road") {
		t.Errorf("another resident has info only on the house: %v", other["house"].Actions)
	}
}

func TestElectionOpensTheHallVerb(t *testing.T) {
	f := facts()
	f.Election = &application.ElectionCalendar{Office: "village_head"}
	o := overlayFor(t, statesync.ViewerMember, true, strangerID, f)
	if !has(o["hall"], "elections") {
		t.Errorf("an open election shows on the hall: %v", o["hall"].Actions)
	}
}

func TestGoalPrefersMissionThenGrowth(t *testing.T) {
	snap := testRegistry(t).Current()
	var code string
	var obj int64
	for _, m := range snap.Missions() {
		if len(m.Objectives) > 0 {
			code, obj = m.Code, m.Objectives[0].Count
			break
		}
	}
	g := missionGoal(snap, []application.MissionProgress{{Code: code, Progress: []int64{0}}})
	if g == nil || g.Target != obj || g.Progress != 0 || g.GoTo == "" {
		t.Fatalf("mission goal = %+v", g)
	}
	if missionGoal(snap, nil) != nil {
		t.Error("no missions, no mission goal")
	}
	p := growthGoal(snap, true, nil, application.SettlementFacts{Owned: map[string]string{}})
	if p == nil || p.Code == "" || p.GoTo == "" || p.Target <= p.Progress {
		t.Fatalf("growth goal = %+v", p)
	}
	if p2 := growthGoal(snap, false, nil, application.SettlementFacts{}); p2 != nil {
		t.Errorf("a resident has no growth goal: %+v", p2)
	}
}

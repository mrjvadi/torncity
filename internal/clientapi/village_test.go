package clientapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

const (
	headID     = "11111111-1111-4111-8111-111111111111"
	residentID = "22222222-2222-4222-8222-222222222222"
	strangerID = "33333333-3333-4333-8333-333333333333"
	villageID  = "44444444-4444-4444-8444-444444444444"
)

type fakeSettlements struct {
	s        application.FoundedSettlement
	memberOf map[string]application.PlayerSettlement
}

func (f *fakeSettlements) ByID(_ context.Context, id string) (application.FoundedSettlement, error) {
	if id != f.s.CityID {
		return application.FoundedSettlement{}, application.ErrCityNotFound
	}
	return f.s, nil
}

func (f *fakeSettlements) ByPlayer(_ context.Context, id string) (application.PlayerSettlement, error) {
	if ps, ok := f.memberOf[id]; ok {
		return ps, nil
	}
	return application.PlayerSettlement{}, application.ErrCityNotFound
}

type fakeBuildings struct {
	rows []application.SettlementBuildingInstance
}

func (f *fakeBuildings) List(context.Context, string) ([]application.SettlementBuildingInstance, error) {
	return append([]application.SettlementBuildingInstance(nil), f.rows...), nil
}

func testRegistry(t *testing.T) *content.Registry {
	t.Helper()
	pack, err := content.Load("../../configs/content")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := content.BuildSnapshot(1, pack)
	if err != nil {
		t.Fatal(err)
	}
	r := content.NewRegistry()
	r.Swap(snap)
	return r
}

type villageFixture struct {
	svc       *VillageService
	buildings *fakeBuildings
	cell      int32
	now       time.Time
}

func newVillageFixture(t *testing.T) *villageFixture {
	t.Helper()
	w := testPlanet(t)
	cell := int32(-1)
	for id, c := range w.Cells {
		if !c.IsOcean && !c.IsLake {
			cell = int32(id)
			break
		}
	}
	world := &WorldService{Source: &fakeWorldSource{row: testWorldRow(), w: w}, CacheEntries: 4, RecheckEvery: time.Minute}
	s := application.FoundedSettlement{CityID: villageID, Code: "v-1", Name: "Amol", Tier: "village", WorldID: testWorldRow().ID,
		WorldCellID: cell}
	fs := &fakeSettlements{s: s, memberOf: map[string]application.PlayerSettlement{
		headID:     {FoundedSettlement: s, Offices: []string{"village_head"}},
		residentID: {FoundedSettlement: s, Resident: true},
	}}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	fb := &fakeBuildings{}
	fin := now.Add(2 * time.Hour)
	fb.rows = []application.SettlementBuildingInstance{
		{ID: "b-hall", SettlementID: villageID, TypeCode: "civic_hall", LotX: 1, LotY: 1, Status: "complete", QueuedAt: now.Add(-48 * time.Hour)},
		{ID: "b-road", SettlementID: villageID, TypeCode: "road", LotX: 0, LotY: 0, Status: "complete", QueuedAt: now.Add(-48 * time.Hour)},
		{ID: "b-camp", SettlementID: villageID, TypeCode: "militia_camp", LotX: 3, LotY: 3, Status: "building", Rotated: true,
			QueuedAt: now.Add(-time.Hour), FinishAt: &fin},
		{ID: "b-gone", SettlementID: villageID, TypeCode: "watch_hut", LotX: 4, LotY: 0, Status: "demolished"},
		{ID: "b-cancelled", SettlementID: villageID, TypeCode: "watch_hut", LotX: 4, LotY: 1, Status: "cancelled"},
	}
	return &villageFixture{buildings: fb, cell: cell, now: now, svc: &VillageService{Settlements: fs, Buildings: fb, World: world,
		Content: testRegistry(t), VillageGridLots: 5, Now: func() time.Time { return now }}}
}

// The terrain a client is given is the terrain the placement rules use.
func TestLayoutTerrainIsTheSamplingPlacementUses(t *testing.T) {
	f := newVillageFixture(t)
	l, err := f.svc.Layout(context.Background(), headID, villageID)
	if err != nil {
		t.Fatal(err)
	}
	w := testPlanet(t)
	pt := w.Cells[f.cell].Point
	placement := settlement.SampleGrid(w, pt.LatDeg, pt.LonDeg, 5, f.cell)
	if l.Grid.Lots != 5 || len(l.Lots) != 5 || l.Grid.LotM < 25 || l.Grid.LotM > 35 {
		t.Fatalf("grid %+v", l.Grid)
	}
	for y := range l.Lots {
		for x, lot := range l.Lots[y] {
			if lot.Buildable != placement[y][x].Buildable {
				t.Errorf("lot %d,%d buildable %v, placement says %v", x, y, lot.Buildable, placement[y][x].Buildable)
			}
			if len(lot.Tags) != len(placement[y][x].Tags) {
				t.Errorf("lot %d,%d tags %v vs %v", x, y, lot.Tags, placement[y][x].Tags)
			}
		}
	}
	if l.Settlement.Centre == nil || l.Settlement.Centre.Chunk == nil || l.Settlement.Centre.Chunk.LOD != int(w.Params.ChunkBaseLOD) {
		t.Errorf("centre %+v", l.Settlement.Centre)
	}
}

func TestLayoutBuildingsFootprintsAndStates(t *testing.T) {
	f := newVillageFixture(t)
	l, err := f.svc.Layout(context.Background(), headID, villageID)
	if err != nil {
		t.Fatal(err)
	}
	if l.Detail != DetailFull || !l.Viewer.Member || !l.Viewer.CanPlace {
		t.Fatalf("viewer %+v detail %s", l.Viewer, l.Detail)
	}
	by := map[string]LayoutBuilding{}
	for _, b := range l.Buildings {
		by[b.Type] = b
	}
	if len(l.Buildings) != 3 {
		t.Fatalf("%d buildings: %+v", len(l.Buildings), l.Buildings)
	}
	if b := by["civic_hall"]; b.W != 2 || b.H != 2 || b.State != StateBuilt || b.ID != "b-hall" {
		t.Errorf("hall %+v", b)
	}
	camp := by["militia_camp"]
	if camp.W != 1 || camp.H != 2 || !camp.Rotated || camp.State != StateUnderConstruction || camp.FinishAt == "" || camp.StartedAt == "" {
		t.Errorf("camp (2x1 turned) %+v", camp)
	}
	if len(l.Roads) != 1 || l.Roads[0] != (LayoutLotRef{0, 0}) {
		t.Errorf("roads %+v", l.Roads)
	}
	if by["civic_hall"].VisualSeed == by["road"].VisualSeed {
		t.Error("two buildings share a look seed")
	}
}

func TestLayoutVisibility(t *testing.T) {
	f := newVillageFixture(t)
	ctx := context.Background()
	res, err := f.svc.Layout(ctx, residentID, villageID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Detail != DetailFull || !res.Viewer.Member || res.Viewer.CanPlace {
		t.Errorf("a resident sees everything but may not place: %+v", res.Viewer)
	}
	stranger, err := f.svc.Layout(ctx, strangerID, villageID)
	if err != nil {
		t.Fatal(err)
	}
	if stranger.Detail != DetailCoarse || stranger.Viewer.Member || stranger.Viewer.CanPlace {
		t.Fatalf("stranger %+v", stranger.Viewer)
	}
	if len(stranger.Lots) != 5 {
		t.Error("terrain is public")
	}
	for _, b := range stranger.Buildings {
		if b.State != StateBuilt || b.ID != "" || b.FinishAt != "" || b.StartedAt != "" || b.DamageBPS != 0 {
			t.Errorf("stranger sees %+v", b)
		}
	}
	if len(stranger.Buildings) != 2 {
		t.Errorf("stranger sees %d buildings, want the 2 that stand", len(stranger.Buildings))
	}
	if stranger.ETag() == res.ETag() {
		t.Error("two views share an entity tag")
	}
	if _, err := f.svc.Layout(ctx, headID, "no-such"); err == nil {
		t.Error("unknown settlement answered")
	}
}

func TestLayoutVersionMovesWithThePicture(t *testing.T) {
	f := newVillageFixture(t)
	ctx := context.Background()
	a, _ := f.svc.Layout(ctx, headID, villageID)
	b, _ := f.svc.Layout(ctx, headID, villageID)
	if a.Version != b.Version {
		t.Fatal("the same picture has two versions")
	}
	f.buildings.rows[2].Status = "complete"
	c, _ := f.svc.Layout(ctx, headID, villageID)
	if c.Version == a.Version {
		t.Fatal("a finished building did not change the version")
	}
	f.buildings.rows = append(f.buildings.rows, application.SettlementBuildingInstance{ID: "b-new", SettlementID: villageID,
		TypeCode: "watch_hut", LotX: 2, LotY: 4, Status: "building"})
	d, _ := f.svc.Layout(ctx, headID, villageID)
	if d.Version == c.Version {
		t.Fatal("a new building did not change the version")
	}
	f.buildings.rows[len(f.buildings.rows)-1].Status = "cancelled"
	e, _ := f.svc.Layout(ctx, headID, villageID)
	if e.Version != c.Version {
		t.Fatal("a cancelled building still counts")
	}
}

func TestMineIsTheBootstrapSettlement(t *testing.T) {
	f := newVillageFixture(t)
	mine, err := f.svc.Mine(context.Background(), headID)
	if err != nil || mine == nil {
		t.Fatalf("head: %v %v", mine, err)
	}
	if !mine.IsHead || mine.Resident || mine.GridLots != 5 || mine.Tier != "village" || mine.Centre == nil ||
		mine.LayoutPath != "/api/v1/settlements/"+villageID+"/layout" {
		t.Errorf("%+v", mine)
	}
	if m, _ := f.svc.Mine(context.Background(), residentID); m == nil || m.IsHead || !m.Resident {
		t.Errorf("resident %+v", m)
	}
	if m, err := f.svc.Mine(context.Background(), strangerID); m != nil || err != nil {
		t.Errorf("stranger %+v %v", m, err)
	}
}

func TestLayoutEndpoint(t *testing.T) {
	f := newVillageFixture(t)
	api := newAPIFixtureWith(t, func(c *ServerConfig) { c.Villages = f.svc; c.WorldSvc = f.svc.World })
	// p1 is the principal of the fixture's link; make it the head.
	fs := f.svc.Settlements.(*fakeSettlements)
	fs.memberOf["p1"] = fs.memberOf[headID]
	token := signedIn(t, api)
	path := "/api/v1/settlements/" + villageID + "/layout"

	res := api.get(t, token, path, nil)
	var l VillageLayout
	if err := json.NewDecoder(res.Body).Decode(&l); err != nil || res.StatusCode != 200 {
		t.Fatalf("%d %v", res.StatusCode, err)
	}
	res.Body.Close()
	if res.Header.Get("ETag") != l.ETag() || res.Header.Get("Cache-Control") != "private, no-cache" || !l.Viewer.CanPlace {
		t.Errorf("headers %v viewer %+v", res.Header, l.Viewer)
	}
	res = api.get(t, token, path, map[string]string{"If-None-Match": l.ETag()})
	res.Body.Close()
	if res.StatusCode != http.StatusNotModified {
		t.Errorf("conditional: %d", res.StatusCode)
	}
	res = api.get(t, token, "/api/v1/settlements/nope/layout", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown: %d", res.StatusCode)
	}
	if res = api.get(t, "", path, nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", res.StatusCode)
	}
}

// A client names a placement by numbers; the handler reads the same lot
// token Telegram's buttons carry.
func TestPlacementArgumentsBecomeALotToken(t *testing.T) {
	cmd, args := clientAlias("settlement.build.place", map[string]json.RawMessage{
		"code": json.RawMessage(`"militia_camp"`), "x": json.RawMessage(`3`), "y": json.RawMessage(`1`), "rotated": json.RawMessage(`true`),
		"confirm": json.RawMessage(`"confirm"`)})
	if cmd != "settlement.build.place" || string(args["lot"]) != `"3-1-r"` || string(args["confirm"]) != `"confirm"` || args["x"] != nil {
		t.Errorf("%s %v", cmd, args)
	}
	_, args = clientAlias("settlement.build.place", map[string]json.RawMessage{"code": json.RawMessage(`"x"`), "lot": json.RawMessage(`"2-2"`)})
	if string(args["lot"]) != `"2-2"` {
		t.Errorf("an explicit lot token was replaced: %v", args)
	}
	if x, y, r, ok := screens.ParseLotToken("3-1-r"); !ok || x != 3 || y != 1 || !r {
		t.Error("the token is not what ParseLotToken reads")
	}
}

// The founding form's commands are a client's: the draft is read, the form is
// submitted with its fields as named arguments, and a refusal is a coded
// error whose view lists the problems (contract 1.3).
func TestFoundingFormCommandsFromAClient(t *testing.T) {
	f := newAPIFixture(t)
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, session := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	token := session["access_token"].(string)

	f.bus.resp = presenter.WithView(presenter.Edit(9, "Fix these.", nil), "founding_refusal",
		struct {
			Kind     string `json:"kind"`
			Problems []struct {
				Field string `json:"field"`
				Code  string `json:"code"`
			} `json:"problems"`
		}{Kind: "invalid"})
	status, out := f.call(t, "POST", "/api/v1/command", token, map[string]any{
		"command": "settlement.found.submit",
		"args":    map[string]any{"draft": "d1", "name": "Aria", "currency_code": "ARI", "color_a": "gold", "check": "1"}})
	if status != http.StatusOK || out["ok"] != false || errCode(out) != "founding_invalid" || out["screen"] != "founding_refusal" {
		t.Fatalf("refusal: %d %v", status, out)
	}
	if f.bus.subj[0] != "game.command.settlement.found.submit.v1" {
		t.Errorf("subject %s", f.bus.subj[0])
	}
	var sent map[string]any
	if err := json.Unmarshal(f.bus.sent[0].Payload, &sent); err != nil || sent["currency_code"] != "ARI" || sent["color_a"] != "gold" || sent["check"] != "1" {
		t.Errorf("payload %s", f.bus.sent[0].Payload)
	}

	f.bus.resp = presenter.WithView(presenter.Edit(9, "The form.", nil), "founding_form", struct{ State string }{"mine"})
	status, out = f.call(t, "POST", "/api/v1/command", token, map[string]any{"command": "settlement.found.draft", "args": map[string]any{}})
	if status != http.StatusOK || out["ok"] != true || out["screen"] != "founding_form" {
		t.Errorf("draft: %d %v", status, out)
	}
}

// A group-only village command is open to a client without client.group_
// commands, an ordinary group command is not, and a refused village command
// comes back as a coded error.
func TestVillageCommandsFromAClient(t *testing.T) {
	f := newAPIFixture(t)
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, session := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	token := session["access_token"].(string)

	f.bus.resp = presenter.WithView(presenter.Edit(9, "That lot is taken.", nil), "village_refusal", struct{ Kind string }{"occupied"})
	status, out := f.call(t, "POST", "/api/v1/command", token, map[string]any{
		"command": "settlement.build.place", "args": map[string]any{"code": "market", "x": 2, "y": 2}})
	if status != http.StatusOK || out["ok"] != false || errCode(out) != "village_occupied" || out["screen"] != "village_refusal" {
		t.Fatalf("refusal: %d %v", status, out)
	}
	if got := string(f.bus.sent[0].Payload); got != `{"code":"market","lot":"2-2"}` {
		t.Errorf("payload %s", got)
	}
	if f.bus.subj[0] != "game.command.settlement.build.place.v1" {
		t.Errorf("subject %s", f.bus.subj[0])
	}

	for _, cmd := range []string{"settlement.overview", "settlement.knowledge.research", "settlement.build.cancel", "settlement.build.demolish"} {
		status, out := f.call(t, "POST", "/api/v1/command", token, map[string]any{"command": cmd, "args": map[string]any{"code": "x", "id": "y"}})
		if status != http.StatusOK {
			t.Errorf("%s: %d %v", cmd, status, out)
		}
	}
	// founding needs a group: it names the group as the village's home.
	if status, out := f.call(t, "POST", "/api/v1/command", token, map[string]any{"command": "settlement.found"}); status != http.StatusForbidden || errCode(out) != "group_only" {
		t.Errorf("found: %d %v", status, out)
	}
}

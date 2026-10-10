package clientapi

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// A settlement as a client draws it (docs/adr/0028 sections 6.4 and 9.4):
// where it stands, its lot grid with the terrain of every lot, and the
// buildings on it. The terrain comes from settlement.SampleGridDetail, the
// very sampling the placement rules validate a building against
// (VillageHandler.grid), so the client and the server agree exactly on what
// is buildable; the footprints and turns are the ones the placement was
// validated with.

// Detail levels of a layout, by who asks (ADR 0030 section 2.1).
const (
	// DetailFull is a member's view: every building, being built ones
	// with their timers, damage, ids.
	DetailFull = "full"
	// DetailCoarse is a stranger's: the terrain and the buildings that
	// stand, nothing of what is being built, damaged or by whom.
	DetailCoarse = "coarse"
)

// Building states a client is told. The database keeps queued, building,
// complete; a demolished or cancelled building holds no lot and is not
// listed.
const (
	StatePlanned           = application.ViewPlanned
	StateUnderConstruction = application.ViewUnderConstruction
	StateBuilt             = application.ViewBuilt
	StateDamaged           = application.ViewDamaged
	StateRuin              = application.ViewRuin
)

// ErrNoSettlement means the player belongs to no settlement.
var ErrNoSettlement = errors.New("clientapi: you belong to no settlement")

// SettlementReader reads a settlement and who belongs to it
// (application.SettlementRepository, over the pool).
type SettlementReader interface {
	ByID(ctx context.Context, id string) (application.FoundedSettlement, error)
	ByPlayer(ctx context.Context, playerID string) (application.PlayerSettlement, error)
}

// BuildingReader lists a settlement's buildings
// (application.SettlementBuildingRepository, over the pool).
type BuildingReader interface {
	List(ctx context.Context, settlementID string) ([]application.SettlementBuildingInstance, error)
}

// CitizenReader reads a settlement's private property (the citizen loop,
// migration 0058): who holds which lot, who owns which building and the
// head's terms, plus the names players go by.
type CitizenReader interface {
	Lots(ctx context.Context, settlementID string) ([]application.SettlementLot, error)
	PrivateBuildings(ctx context.Context, settlementID string) ([]application.PrivateBuilding, error)
	Terms(ctx context.Context, settlementID string) (application.LotTerms, error)
	// Names maps player ids to the names they go by; an unknown id is left out.
	Names(ctx context.Context, playerIDs []string) (map[string]string, error)
	// Plans, Cells and OpenLots are the roads drawn out of the first grid and
	// the lots they opened (docs/adr/0044 5.5, migration 0110).
	Plans(ctx context.Context, settlementID string) ([]application.RoadPlanRow, error)
	Cells(ctx context.Context, settlementID string) ([]application.RoadCellRow, error)
	OpenLots(ctx context.Context, settlementID string) ([]application.OpenLotRow, error)
}

// VillageService assembles the settlement views.
type VillageService struct {
	Settlements SettlementReader
	Buildings   BuildingReader
	// Citizens and CitizenTerms add the tenure of lots to the layout (the
	// citizen loop); nil leaves it out. CitizenTerms turns the head's levers
	// into the lot price, permit fee and tax in force.
	Citizens     CitizenReader
	CitizenTerms func(application.LotTerms) (lotPrice, permitFee int64, taxBPS int)
	World        *WorldService
	Content      *content.Registry
	// Overlay reads what the per-viewer overlay and the goal need (overlay.go);
	// nil leaves them out. StockBaseCapacity is settlement.stock_base_capacity.
	Overlay           OverlayReader
	StockBaseCapacity int64
	// HomesPerBuildCrew is settlement.build_homes_per_crew.
	HomesPerBuildCrew int64
	// VillageGridLots is settlement.village_grid_lots.
	VillageGridLots int
	Now             func() time.Time
	// Land and LandRules add the trees and rocks of the land to the layout (docs/adr/0065); nil Land leaves them out.
	Land      application.LandReader
	LandRules application.LandRules
	// Farm and FarmRules add the stage of the farms' crops to the layout (docs/adr/0067); nil Farm leaves them out.
	Farm      application.FarmReader
	FarmRules application.FarmRules
}

// Place is a point on the planet with the base-LOD chunk holding it.
type Place struct {
	Lat   float64        `json:"lat"`
	Lon   float64        `json:"lon"`
	Chunk *ChunkAddrView `json:"chunk,omitempty"`
}

// ChunkAddrView is a chunk address as the endpoint takes it.
type ChunkAddrView struct {
	Face int `json:"face"`
	LOD  int `json:"lod"`
	X    int `json:"x"`
	Y    int `json:"y"`
}

// BootstrapSettlement is the player's own settlement, in the bootstrap.
type BootstrapSettlement struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
	Tier string `json:"tier"`
	// WorldCell is the world cell the settlement stands on, Centre its
	// middle (the centre of that cell, the middle of the lot grid).
	WorldCell int32  `json:"world_cell"`
	Centre    *Place `json:"centre,omitempty"`
	// IsHead is set when the player holds any office of the settlement's charter
	// (some permission); Permissions lists what they hold, so a client shows an
	// act only to the player who may make it. Resident when they live there.
	IsHead      bool     `json:"is_head"`
	Permissions []string `json:"permissions,omitempty"`
	Resident    bool     `json:"resident"`
	// GridLots is the side of the lot grid.
	GridLots   int    `json:"grid_lots"`
	LayoutPath string `json:"layout_path"`
	// Emblem, Motto and Currency are the founding form's choices (contract
	// 1.3); absent for a village founded before the form existed.
	Emblem   *EmblemView   `json:"emblem,omitempty"`
	Motto    string        `json:"motto,omitempty"`
	Currency *CurrencyView `json:"currency,omitempty"`
}

// EmblemView is a village's emblem as the four codes of the catalogue
// (configs/content/founding.yml) a client draws it from.
type EmblemView struct {
	Shape  string `json:"shape"`
	ColorA string `json:"color_a"`
	ColorB string `json:"color_b"`
	Icon   string `json:"icon"`
}

// CurrencyView is the national currency a village reserved; it uses SUP
// until it declares a country.
type CurrencyView struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Symbol string `json:"symbol"`
}

// Mine is the player's settlement, nil when they belong to none.
func (v *VillageService) Mine(ctx context.Context, playerID string) (*BootstrapSettlement, error) {
	ps, err := v.Settlements.ByPlayer(ctx, playerID)
	if errors.Is(err, application.ErrCityNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &BootstrapSettlement{ID: ps.CityID, Code: ps.Code, Name: ps.Name, Tier: application.TierCity, WorldCell: ps.WorldCellID,
		IsHead: holdsHead(ps), Permissions: ps.Permissions, Resident: ps.Resident, GridLots: v.gridLots(ps.Tier, ps.GridGrowth), LayoutPath: "/api/v1/settlements/" + ps.CityID + "/layout"}
	if _, w, err := v.World.active(ctx); err == nil {
		out.Centre = centreOf(w, ps.WorldCellID)
	}
	if e := ps.Emblem; e.Shape != "" {
		out.Emblem = &EmblemView{Shape: e.Shape, ColorA: e.ColorA, ColorB: e.ColorB, Icon: e.Icon}
	}
	out.Motto = ps.Motto
	if ps.Currency.Code != "" {
		out.Currency = &CurrencyView{Code: ps.Currency.Code, Name: ps.Currency.Name, Symbol: ps.Currency.Symbol}
	}
	return out, nil
}

// holdsHead reports that the player has a say in the settlement: some permission
// of its charter (ADR 0044 section 6). The founder's office holds them all.
func holdsHead(ps application.PlayerSettlement) bool { return ps.CanManage() }

func (v *VillageService) gridLots(tier string, growth int) int {
	return settlement.GridLotsGrown(tier, v.VillageGridLots, growth)
}

func centreOf(w *worldgen.World, cell int32) *Place {
	if cell < 0 || int(cell) >= len(w.Cells) {
		return nil
	}
	pt := w.Cells[cell].Point
	c := worldgen.ChunkOfLatLon(w.Params.ChunkBaseLOD, pt.LatDeg, pt.LonDeg)
	return &Place{Lat: pt.LatDeg, Lon: pt.LonDeg,
		Chunk: &ChunkAddrView{Face: int(c.Face), LOD: int(c.LOD), X: int(c.X), Y: int(c.Y)}}
}

// VillageLayout is GET /api/v1/settlements/{id}/layout.
type VillageLayout struct {
	// Version changes whenever anything a client draws changes; it is also
	// the ETag, so a client can poll cheaply until realtime carries it.
	Version    string           `json:"version"`
	Detail     string           `json:"detail"`
	Viewer     LayoutViewer     `json:"viewer"`
	Settlement LayoutSettlement `json:"settlement"`
	Grid       LayoutGrid       `json:"grid"`
	// Lots[y][x] is the terrain of lot (x, y).
	Lots      [][]LayoutLot    `json:"lots"`
	Buildings []LayoutBuilding `json:"buildings"`
	// Roads lists the lots that hold a road, for a client that draws roads
	// apart from buildings; they are also in Buildings.
	Roads []LayoutLotRef `json:"roads"`
	// Tenure lists the lots that have an owner, for a member only (contract
	// 1.4); a lot not listed is commons, on sale at Terms.LotPrice.
	Tenure []LayoutTenure `json:"tenure,omitempty"`
	// Terms are the land and permit terms in force, for a member only.
	Terms *LayoutTerms `json:"terms,omitempty"`
	// Ring is the commons round the grid and Woods the state of the settlement's wood (docs/adr/0065); absent while the land
	// model is off.
	Ring  *LayoutRing  `json:"ring,omitempty"`
	Woods *LayoutWoods `json:"woods,omitempty"`
	// Farms are the crops of the farms that work in cycles, for the client to draw the stage of their fields (docs/adr/0067).
	Farms []LayoutFarm `json:"farms,omitempty"`

	farmMark string // changes with any crop; part of the ETag, not of Version
	// Land is the land the roads opened beyond the first grid, for a member
	// only: the drawn road cells and the lots along them, in absolute lot
	// coordinates (negative west and south of the grid). A laid road is also in
	// Buildings and Roads like any road.
	Land *LayoutLand `json:"land,omitempty"`

	woodLots map[land.Pos]*application.LandLot // the land model's lots, for the sections that carry trees
}

// LayoutRing is the woodland ring of commons: lots outside the claimed grid, in the grid's signed coordinates.
type LayoutRing struct {
	Depth int             `json:"depth"`
	Lots  []LayoutRingLot `json:"lots"`
}

// LayoutRingLot is one lot of the ring.
type LayoutRingLot struct {
	X        int            `json:"x"`
	Y        int            `json:"y"`
	HeightM  float64        `json:"height_m"`
	SlopeM   float64        `json:"slope_m"`
	Biome    string         `json:"biome,omitempty"`
	Water    string         `json:"water,omitempty"`
	Trees    int            `json:"trees"`
	Rocks    int            `json:"rocks"`
	Stumps   int            `json:"stumps"`
	Saplings []LayoutGrowth `json:"saplings,omitempty"`
}

// LayoutGrowth is a sapling still growing; Stage runs from 0 (planted) to 1 (a tree).
type LayoutGrowth struct {
	Stage float64 `json:"stage"`
}

// LayoutWoods is the settlement's wood: the share of its trees that is left (the client thins its own countryside by it), the
// trees the land generated and the trees standing, and the generator version it is under (0: its founding grid stays clear).
type LayoutWoods struct {
	ForestRemainingBPS int `json:"forest_remaining_bps"`
	GeneratedTrees     int `json:"generated_trees"`
	TreesLeft          int `json:"trees_left"`
	GenVersion         int `json:"gen_version"`
	// Mark changes whenever anything on the land changes (a tree felled, a rock broken, a sapling planted or grown, regrowth); it
	// is part of the ETag, not of Version, so the version of the state sync stays the one it always was.
	Mark string `json:"mark,omitempty"`
}

// LayoutLand is the land the roads opened.
type LayoutLand struct {
	// Plans are the drawn roads, Cells their lots (laid or not), Open the lots
	// along them.
	Plans []LayoutRoadPlan `json:"plans"`
	Cells []LayoutRoadCell `json:"cells"`
	Open  []LayoutOpenLot  `json:"open"`
}

// LayoutRoadPlan is one drawn road.
type LayoutRoadPlan struct {
	ID    string `json:"id"`
	Class string `json:"class"`
	Lots  int    `json:"lots"`
	ToX   int    `json:"to_x"`
	ToY   int    `json:"to_y"`
}

// LayoutRoadCell is one lot of a drawn road. Water is "", "stream" or "river".
type LayoutRoadCell struct {
	X       int     `json:"x"`
	Y       int     `json:"y"`
	Plan    string  `json:"plan"`
	Built   bool    `json:"built"`
	Water   string  `json:"water,omitempty"`
	HeightM float64 `json:"height_m"`
}

// LayoutOpenLot is a lot a road opened, with the ground it stands on. Reason is
// "" (it can be bought and built on), "water" or "steep".
type LayoutOpenLot struct {
	X         int      `json:"x"`
	Y         int      `json:"y"`
	Buildable bool     `json:"buildable"`
	Reason    string   `json:"reason,omitempty"`
	HeightM   float64  `json:"height_m"`
	SlopeM    float64  `json:"slope_m"`
	Biome     string   `json:"biome,omitempty"`
	Water     string   `json:"water,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Trees     int      `json:"trees,omitempty"`
	Rocks     int      `json:"rocks,omitempty"`
	Stumps    int      `json:"stumps,omitempty"`
}

// LayoutTenure is one owned lot. Owner names the holder; Mine is set when the
// viewer holds it themselves.
type LayoutTenure struct {
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Tenure string `json:"tenure"`
	Mine   bool   `json:"mine"`
	Owner  string `json:"owner,omitempty"`
}

// LayoutTerms are what a lot, a permit and the property tax cost here.
type LayoutTerms struct {
	LotPrice  int64 `json:"lot_price"`
	PermitFee int64 `json:"permit_fee"`
	TaxBPS    int   `json:"tax_bps"`
}

// LayoutViewer says what the asker may do here.
type LayoutViewer struct {
	Member bool `json:"member"`
	// CanPlace is set for the settlement's head: place, cancel, demolish.
	CanPlace bool `json:"can_place"`
	// Resident is set when the viewer lives here: a resident may buy a free
	// lot and build a private building on their own (contract 1.4).
	Resident bool `json:"resident"`
}

// LayoutSettlement identifies the settlement.
type LayoutSettlement struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Tier      string `json:"tier"`
	WorldCell int32  `json:"world_cell"`
	Centre    *Place `json:"centre,omitempty"`
}

// LayoutGrid is the lot grid's geometry.
type LayoutGrid struct {
	Lots int     `json:"lots"`
	LotM float64 `json:"lot_m"`
	// Origin is the centre of lot (0, 0); x grows east and y north, both
	// in steps of LotM metres on the ground.
	Origin LayoutOrigin `json:"origin"`
	// SlopeLimit is the height difference between neighbouring lots above
	// which a lot counts as steep.
	SlopeLimit float64 `json:"slope_limit"`
}

// LayoutOrigin is the lat/lon of lot (0, 0).
type LayoutOrigin struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// LayoutLot is one lot's terrain.
type LayoutLot struct {
	HeightM float64 `json:"height_m"`
	SlopeM  float64 `json:"slope_m"`
	// Buildable is the placement rule's own answer for the terrain alone.
	Buildable bool   `json:"buildable"`
	Biome     string `json:"biome,omitempty"`
	// Water is "ocean", "lake", "river" or "stream", empty on dry ground.
	Water string   `json:"water,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	// Trees, Rocks and Stumps are what stands on the lot now and Saplings the trees still growing; Obstructed says a tree or a rock
	// stands on it, so it cannot be built on until cleared (Buildable stays the terrain's own answer; docs/adr/0065).
	Trees      int            `json:"trees,omitempty"`
	Rocks      int            `json:"rocks,omitempty"`
	Stumps     int            `json:"stumps,omitempty"`
	Saplings   []LayoutGrowth `json:"saplings,omitempty"`
	Obstructed bool           `json:"obstructed,omitempty"`
}

// LayoutLotRef is a lot's coordinates.
type LayoutLotRef struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// LayoutBuilding is a building on the grid. X, Y is its top-left lot (the
// lot with the smallest x and y) and W, H the footprint after the turn.
type LayoutBuilding struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	// Rotated says the footprint was turned a quarter turn when placed.
	Rotated bool   `json:"rotated"`
	State   string `json:"state"`
	// StartedAt and FinishAt bound a construction, RFC 3339; FinishAt is
	// empty for the founding kit and for buildings placed before it was
	// recorded.
	StartedAt string `json:"started_at,omitempty"`
	FinishAt  string `json:"finish_at,omitempty"`
	DamageBPS int    `json:"damage_bps,omitempty"`
	// VisualSeed seeds the client's own look of the building; the server
	// never ships a model.
	VisualSeed uint32 `json:"visual_seed"`
	// Private is set for a resident's building; Owner names them and Mine is
	// set when it is the viewer's own. Members only (contract 1.4).
	Private bool   `json:"private,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Mine    bool   `json:"mine,omitempty"`
}

// Layout is a settlement's layout as viewer sees it.
func (v *VillageService) Layout(ctx context.Context, viewerID, settlementID string) (VillageLayout, error) {
	s, err := v.Settlements.ByID(ctx, settlementID)
	if errors.Is(err, application.ErrCityNotFound) {
		return VillageLayout{}, err
	}
	if err != nil {
		return VillageLayout{}, err
	}
	viewer := LayoutViewer{}
	mine, err := v.Settlements.ByPlayer(ctx, viewerID)
	switch {
	case err == nil:
		if mine.CityID == s.CityID {
			viewer.Member = true
			viewer.CanPlace = holdsHead(mine)
			viewer.Resident = mine.Resident
		}
	case errors.Is(err, application.ErrCityNotFound):
	default:
		return VillageLayout{}, err
	}
	row, w, err := v.World.active(ctx)
	if err != nil {
		return VillageLayout{}, err
	}
	if s.WorldID != "" && s.WorldID != row.ID {
		// The settlement belongs to a world that is not the live one.
		return VillageLayout{}, application.ErrCityNotFound
	}
	if s.WorldCellID < 0 || int(s.WorldCellID) >= len(w.Cells) {
		return VillageLayout{}, application.ErrCityNotFound
	}
	rows, err := v.Buildings.List(ctx, s.CityID)
	if err != nil {
		return VillageLayout{}, err
	}

	pt := w.Cells[s.WorldCellID].Point
	lots := v.gridLots(s.Tier, s.GridGrowth)
	gridLat, gridLon := settlement.GridCentreGrown(w, pt.LatDeg, pt.LonDeg, s.GridShiftX, s.GridShiftY, s.GridGrowth)
	grid := settlement.SampleGridDetail(w, gridLat, gridLon, lots, s.WorldCellID)
	out := VillageLayout{
		Detail: DetailCoarse, Viewer: viewer,
		Settlement: LayoutSettlement{ID: s.CityID, Code: s.Code, Name: s.Name, Tier: s.Tier, WorldCell: s.WorldCellID,
			Centre: centreOf(w, s.WorldCellID)},
		Grid: LayoutGrid{Lots: lots, LotM: grid.LotMeters, Origin: LayoutOrigin{Lat: grid.OriginLat, Lon: grid.OriginLon},
			SlopeLimit: settlement.SlopeThresholdM()},
		Lots: make([][]LayoutLot, lots), Buildings: []LayoutBuilding{}, Roads: []LayoutLotRef{},
	}
	if viewer.Member {
		out.Detail = DetailFull
	}
	for y := range grid.Lots {
		out.Lots[y] = make([]LayoutLot, len(grid.Lots[y]))
		for x, l := range grid.Lots[y] {
			out.Lots[y][x] = LayoutLot{HeightM: round2(l.ElevationM), SlopeM: round2(l.SlopeM), Buildable: l.Buildable,
				Biome: l.Biome, Water: waterOf(l), Tags: l.Tags}
		}
	}

	snap := v.Content.Current()
	footprint := func(code string, rotated bool) (int, int) {
		if d, ok := snap.SettlementBuildingDef(code); ok {
			def := d.Def()
			if rotated {
				def = def.Rotate()
			}
			return def.FootprintW, def.FootprintH
		}
		return 1, 1
	}
	// The buildings, and the version of them, are application.ViewBuildings
	// and application.LayoutVersionOf: the same code the village events use
	// to say what the layout's version will be once they have committed.
	for _, b := range application.ViewBuildings(rows, viewer.Member, footprint) {
		out.Buildings = append(out.Buildings, LayoutBuilding{ID: b.ID, Type: b.Type, X: b.X, Y: b.Y, W: b.W, H: b.H,
			Rotated: b.Rotated, State: b.State, StartedAt: b.StartedAt, FinishAt: b.FinishAt, DamageBPS: b.DamageBPS,
			VisualSeed: b.VisualSeed})
		if b.Type == "road" && b.State != StatePlanned {
			out.Roads = append(out.Roads, LayoutLotRef{X: b.X, Y: b.Y})
		}
	}
	mark := ""
	var open []application.OpenLotRow
	if v.Citizens != nil {
		var err error
		if open, err = v.Citizens.OpenLots(ctx, s.CityID); err != nil {
			return VillageLayout{}, err
		}
	}
	woodsMark, err := v.addWoods(ctx, &out, w, s, lots, open, occupiedBy(rows, footprint), rows)
	if err != nil {
		return VillageLayout{}, err
	}
	if err := v.addFarms(ctx, &out, s, rows); err != nil {
		return VillageLayout{}, err
	}
	if viewer.Member && v.Citizens != nil {
		var err error
		if mark, err = v.addTenure(ctx, &out, viewerID, s.CityID); err != nil {
			return VillageLayout{}, err
		}
		roadMark, err := v.addLand(ctx, &out, s.CityID)
		if err != nil {
			return VillageLayout{}, err
		}
		mark = application.JoinMarks(mark, roadMark)
	}
	out.Version = layoutVersion(out, mark)
	if out.Woods != nil {
		out.Woods.Mark = woodsMark
	}
	return out, nil
}

// addLand adds the roads drawn out of the first grid to a member's layout and
// returns the mark the version folds in (application.LandMark).
func (v *VillageService) addLand(ctx context.Context, out *VillageLayout, settlementID string) (string, error) {
	plans, err := v.Citizens.Plans(ctx, settlementID)
	if err != nil || len(plans) == 0 {
		return "", err
	}
	cells, err := v.Citizens.Cells(ctx, settlementID)
	if err != nil {
		return "", err
	}
	open, err := v.Citizens.OpenLots(ctx, settlementID)
	if err != nil {
		return "", err
	}
	land := &LayoutLand{Plans: []LayoutRoadPlan{}, Cells: []LayoutRoadCell{}, Open: []LayoutOpenLot{}}
	count := map[string]int{}
	for _, c := range cells {
		count[c.PlanID]++
		water := ""
		switch c.Water {
		case application.RoadWaterStream:
			water = "stream"
		case application.RoadWaterRiver:
			water = "river"
		}
		land.Cells = append(land.Cells, LayoutRoadCell{X: c.X, Y: c.Y, Plan: c.PlanID, Built: c.Built(), Water: water, HeightM: round2(c.ElevationM)})
	}
	for _, p := range plans {
		land.Plans = append(land.Plans, LayoutRoadPlan{ID: p.ID, Class: p.Class, Lots: count[p.ID], ToX: p.ToX, ToY: p.ToY})
	}
	for _, o := range open {
		ol := LayoutOpenLot{X: o.X, Y: o.Y, Buildable: o.Buildable, Reason: o.Reason,
			HeightM: round2(o.HeightM), SlopeM: round2(o.SlopeM), Biome: o.Biome, Water: o.Water, Tags: o.Tags}
		if l, ok := out.woodLots[lotKey(o.X, o.Y)]; ok {
			ol.Trees, ol.Rocks, ol.Stumps = l.Trees, l.Rocks, l.Stumps
		}
		land.Open = append(land.Open, ol)
	}
	out.Land = land
	return application.LandMark(plans, cells), nil
}

// addTenure adds who owns which lot and building to a member's layout and
// returns the mark the version folds in (application.TenureMark).
func (v *VillageService) addTenure(ctx context.Context, out *VillageLayout, viewerID, settlementID string) (string, error) {
	lots, err := v.Citizens.Lots(ctx, settlementID)
	if err != nil {
		return "", err
	}
	priv, err := v.Citizens.PrivateBuildings(ctx, settlementID)
	if err != nil {
		return "", err
	}
	terms, err := v.Citizens.Terms(ctx, settlementID)
	if err != nil {
		return "", err
	}
	var ids []string
	for _, l := range lots {
		ids = append(ids, l.OwnerID)
	}
	owners := map[string]string{}
	for _, b := range priv {
		owners[b.BuildingID] = b.OwnerID
		ids = append(ids, b.OwnerID)
	}
	names, err := v.Citizens.Names(ctx, ids)
	if err != nil {
		return "", err
	}
	out.Tenure = []LayoutTenure{}
	for _, l := range lots {
		out.Tenure = append(out.Tenure, LayoutTenure{X: l.X, Y: l.Y, Tenure: l.Tenure, Mine: l.OwnerID == viewerID, Owner: names[l.OwnerID]})
	}
	for i, b := range out.Buildings {
		if owner, ok := owners[b.ID]; ok && b.ID != "" {
			out.Buildings[i].Private, out.Buildings[i].Owner, out.Buildings[i].Mine = true, names[owner], owner == viewerID
		}
	}
	if v.CitizenTerms != nil {
		price, permit, tax := v.CitizenTerms(terms)
		out.Terms = &LayoutTerms{LotPrice: price, PermitFee: permit, TaxBPS: tax}
	}
	return application.TenureMark(lots, priv), nil
}

// ETag is the layout's entity tag: its version and the detail it shows.
func (l VillageLayout) ETag() string {
	tag := l.Version + "." + l.Detail
	if l.Woods != nil && l.Woods.Mark != "" {
		tag += "." + l.Woods.Mark
	}
	if l.farmMark != "" {
		tag += ".f" + l.farmMark
	}
	return `"` + tag + `"`
}

func waterOf(l settlement.LotTerrain) string {
	switch {
	case l.Ocean:
		return "ocean"
	case l.Lake:
		return "lake"
	}
	return l.Stream
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// layoutVersion is the layout's version: application.LayoutVersionOf over
// what the layout shows.
func layoutVersion(l VillageLayout, tenure ...string) string {
	bs := make([]application.ViewBuilding, 0, len(l.Buildings))
	for _, b := range l.Buildings {
		bs = append(bs, application.ViewBuilding{ID: b.ID, Type: b.Type, X: b.X, Y: b.Y, W: b.W, H: b.H, Rotated: b.Rotated,
			State: b.State, FinishAt: b.FinishAt, DamageBPS: b.DamageBPS, VisualSeed: b.VisualSeed})
	}
	return application.LayoutVersionOf(l.Settlement.ID, l.Settlement.Tier, l.Settlement.Name, l.Grid.Lots, l.Viewer.CanPlace, bs, tenure...)
}

// LayoutVersions is the settlement's layout version for each kind of viewer
// (head, member, public) and its grid's side, as Layout would answer them:
// the same buildings, footprints and tenure mark, without sampling the
// terrain (which no version depends on). Client state sync puts the
// viewer's own version in the player's settlement summary (docs/adr/0034).
func (v *VillageService) LayoutVersions(ctx context.Context, settlementID string) (application.LayoutVersions, int, error) {
	s, err := v.Settlements.ByID(ctx, settlementID)
	if err != nil {
		return application.LayoutVersions{}, 0, err
	}
	rows, err := v.Buildings.List(ctx, s.CityID)
	if err != nil {
		return application.LayoutVersions{}, 0, err
	}
	snap := v.Content.Current()
	footprint := func(code string, rotated bool) (int, int) {
		if d, ok := snap.SettlementBuildingDef(code); ok {
			def := d.Def()
			if rotated {
				def = def.Rotate()
			}
			return def.FootprintW, def.FootprintH
		}
		return 1, 1
	}
	mark := ""
	if v.Citizens != nil {
		lots, err := v.Citizens.Lots(ctx, s.CityID)
		if err != nil {
			return application.LayoutVersions{}, 0, err
		}
		priv, err := v.Citizens.PrivateBuildings(ctx, s.CityID)
		if err != nil {
			return application.LayoutVersions{}, 0, err
		}
		mark = application.TenureMark(lots, priv)
		if plans, err := v.Citizens.Plans(ctx, s.CityID); err != nil {
			return application.LayoutVersions{}, 0, err
		} else if len(plans) > 0 {
			cells, err := v.Citizens.Cells(ctx, s.CityID)
			if err != nil {
				return application.LayoutVersions{}, 0, err
			}
			mark = application.JoinMarks(mark, application.LandMark(plans, cells))
		}
	}
	lots := v.gridLots(s.Tier, s.GridGrowth)
	return application.LayoutVersionsWithTenure(s.CityID, s.Tier, s.Name, lots, rows, footprint, mark), lots, nil
}

// landViewOf is the land of a settlement now, from the stored deltas; nil while the model is off.
func (v *VillageService) landViewOf(ctx context.Context, w *worldgen.World, s application.FoundedSettlement, side int, open []application.OpenLotRow, occupied map[land.Pos]bool, rows []application.SettlementBuildingInstance) (*application.LandView, error) {
	if v.Land == nil || !v.LandRules.Enabled() {
		return nil, nil
	}
	def, ok := v.Content.Current().Land()
	if !ok {
		return nil, nil
	}
	deltas, err := v.Land.Rows(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	saplings, err := v.Land.Saplings(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if v.Now != nil {
		now = v.Now().UTC()
	}
	return application.BuildLandWith(w, s, side, open, occupied, v.grazedBy(rows), def, v.LandRules, deltas, saplings, now), nil
}

// addWoods adds the trees and rocks to the lots, the ring of commons and the state of the wood, and returns the mark the
// version folds in.
func (v *VillageService) addWoods(ctx context.Context, out *VillageLayout, w *worldgen.World, s application.FoundedSettlement, side int, open []application.OpenLotRow, occupied map[land.Pos]bool, rows []application.SettlementBuildingInstance) (string, error) {
	lv, err := v.landViewOf(ctx, w, s, side, open, occupied, rows)
	if err != nil || lv == nil || len(lv.Lots) == 0 {
		return "", err
	}
	growth := func(l *application.LandLot) []LayoutGrowth {
		var g []LayoutGrowth
		for _, st := range l.Growing {
			g = append(g, LayoutGrowth{Stage: round2(st)})
		}
		return g
	}
	for y := range out.Lots {
		for x := range out.Lots[y] {
			if l, ok := lv.Lots[land.Pos{X: x, Y: y}]; ok {
				out.Lots[y][x].Trees, out.Lots[y][x].Rocks, out.Lots[y][x].Stumps = l.Trees, l.Rocks, l.Stumps
				out.Lots[y][x].Saplings, out.Lots[y][x].Obstructed = growth(l), l.Obstructed
			}
		}
	}
	ring := &LayoutRing{Depth: lv.Rules.Ring, Lots: []LayoutRingLot{}}
	for _, p := range lv.Order() {
		l := lv.Lots[p]
		if !l.Commons {
			continue
		}
		ring.Lots = append(ring.Lots, LayoutRingLot{X: p.X, Y: p.Y, Biome: l.Ground.Biome, Water: waterKind(l), Trees: l.Trees, Rocks: l.Rocks,
			Stumps: l.Stumps, Saplings: growth(l)})
	}
	out.Ring = ring
	out.woodLots = lv.Lots
	bps, gen, left := lv.Forest()
	out.Woods = &LayoutWoods{ForestRemainingBPS: bps, GeneratedTrees: gen, TreesLeft: left, GenVersion: lv.Version}
	return lv.Mark(), nil
}

func waterKind(l *application.LandLot) string {
	if l.Ground.Water {
		return "water"
	}
	return ""
}

func lotKey(x, y int) land.Pos { return land.Pos{X: x, Y: y} }

// occupiedBy is the lots the buildings hold, for the land model.
func occupiedBy(rows []application.SettlementBuildingInstance, footprint func(code string, rotated bool) (int, int)) map[land.Pos]bool {
	out := map[land.Pos]bool{}
	for _, b := range rows {
		if !b.Holds() {
			continue
		}
		fw, fh := footprint(b.TypeCode, b.Rotated)
		for dy := 0; dy < fh; dy++ {
			for dx := 0; dx < fw; dx++ {
				out[land.Pos{X: b.LotX + dx, Y: b.LotY + dy}] = true
			}
		}
	}
	return out
}

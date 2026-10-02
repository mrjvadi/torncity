package clientapi

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
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
	// VillageGridLots is settlement.village_grid_lots.
	VillageGridLots int
	Now             func() time.Time
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
	// IsHead is set when the player holds the settlement's top office, and
	// only such a player places buildings; Resident when they live there.
	IsHead   bool `json:"is_head"`
	Resident bool `json:"resident"`
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
	out := &BootstrapSettlement{ID: ps.CityID, Code: ps.Code, Name: ps.Name, Tier: ps.Tier, WorldCell: ps.WorldCellID,
		IsHead: holdsHead(ps), Resident: ps.Resident, GridLots: v.gridLots(ps.Tier, ps.GridGrowth), LayoutPath: "/api/v1/settlements/" + ps.CityID + "/layout"}
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

func holdsHead(ps application.PlayerSettlement) bool {
	head := settlement.HeadOffice(ps.Tier)
	for _, o := range ps.Offices {
		if o == head {
			return true
		}
	}
	return false
}

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
	if viewer.Member && v.Citizens != nil {
		var err error
		if mark, err = v.addTenure(ctx, &out, viewerID, s.CityID); err != nil {
			return VillageLayout{}, err
		}
	}
	out.Version = layoutVersion(out, mark)
	return out, nil
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
func (l VillageLayout) ETag() string { return `"` + l.Version + "." + l.Detail + `"` }

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
	}
	lots := v.gridLots(s.Tier, s.GridGrowth)
	return application.LayoutVersionsWithTenure(s.CityID, s.Tier, s.Name, lots, rows, footprint, mark), lots, nil
}

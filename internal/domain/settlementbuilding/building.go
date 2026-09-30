// Package settlementbuilding holds ADR 0031 section 3.2's building role/tier
// catalogue and ADR 0028 section 6's placement legality (phase W5): what a
// building type needs to exist at all (role/tier promotion, knowledge,
// terrain), where it may stand on a settlement's own ~30m lot grid, and the
// settlement-tier concurrent-build cap.
//
// EXTENDS ADR 0028 SECTION 7's FLAT SKETCH, DOES NOT REPLACE IT. A building
// still has a code, a footprint, a cost and a build time (ADR 0028); this
// package adds Role and Tier (ADR 0031): several codes may share one
// role/tier pair — watch_hut, militia_camp, retainer_hall and
// constable_post are four peers of role="security", tier=1 — the identical
// branching mechanism internal/domain/settlementknowledge's Provides/
// RequiresCapability already gives knowledge, mirrored here as Role/
// RequiresBuildingRole: a tier-2 promotion (watch_hut -> police_post) needs
// ANY tier-1 building of its role already standing, never one specific
// code.
//
// NO I/O, NO CLOCK, NO RANDOMNESS, and — the same discipline
// internal/domain/settlementknowledge's own doc draws — NO IMPORT OF
// internal/domain/settlementknowledge. A building's RequiresKnowledge is
// carried as opaque code strings; whether those codes actually exist in the
// loaded knowledge catalogue is a cross-content-type check the content
// layer (internal/content) makes, exactly the way it already cross-checks a
// component's RequiresTechnology against the technology tree without
// internal/domain/production importing internal/domain/technology.
package settlementbuilding

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// Sentinel errors.
var (
	// ErrInvalidBuilding means a building definition is unusable.
	ErrInvalidBuilding = errors.New("settlementbuilding: invalid building definition")
	// ErrUnreachableRole means some definition's RequiresBuildingRole names
	// a role/tier pair no definition in the same catalogue declares — the
	// buildings' own version of settlementknowledge's unreachable-capability
	// rule (ADR 0031 section 5.2).
	ErrUnreachableRole = errors.New("settlementbuilding: requires_building_role names a role/tier nothing provides")

	// ErrOutOfBounds means a footprint does not fit inside the grid.
	ErrOutOfBounds = errors.New("settlementbuilding: placement is out of bounds")
	// ErrUnbuildableLot means some lot of the footprint is not buildable
	// terrain (water, a river/stream channel, too steep).
	ErrUnbuildableLot = errors.New("settlementbuilding: a lot in the footprint is not buildable")
	// ErrLotOccupied means some lot of the footprint already holds a
	// building.
	ErrLotOccupied = errors.New("settlementbuilding: a lot in the footprint is already occupied")
	// ErrTerrainRequired means the building's own TerrainTags (a coastal
	// lot for a port, an arable biome for a farm, an ore deposit for a
	// mine) are not met anywhere in its footprint.
	ErrTerrainRequired = errors.New("settlementbuilding: the footprint's terrain does not permit this building")
	// ErrKnowledgeMissing means the settlement lacks a knowledge
	// prerequisite.
	ErrKnowledgeMissing = errors.New("settlementbuilding: a knowledge prerequisite is not held")
	// ErrRoleMissing means the settlement holds no building of the
	// role/tier a promotion requires.
	ErrRoleMissing = errors.New("settlementbuilding: no building of the required role/tier stands yet")
	// ErrAboveTier means the building belongs to a bigger settlement than this
	// one: a village never lists, and is never allowed, an airport.
	ErrAboveTier = errors.New("settlementbuilding: the settlement is too small for this building")
	// ErrConcurrentBuildCap means the settlement's tier-bound concurrent
	// construction cap (ADR 0028 section 6.3) is already full.
	ErrConcurrentBuildCap = errors.New("settlementbuilding: the concurrent construction cap is full")
	// ErrLiteracyTooLow means the settlement's own literacy_share has not
	// yet reached the building's MinLiteracyShareBPS (ADR 0031 section
	// 4.4).
	ErrLiteracyTooLow = errors.New("settlementbuilding: literacy share too low")
	// ErrAlreadyBuilt means the exact building role/tier pair this
	// definition promotes to is already standing at that tier (a
	// promotion happens once; re-promoting is not "one more building").
	// Two SEPARATE codes of the same role/tier (rival branches) are not
	// this case — see the package doc.
)

// Bounds on authored content.
const (
	// MaxFootprint bounds a single dimension of a footprint, lots.
	MaxFootprint = 10
	// MaxCostMoney bounds a building's money cost, minor units.
	MaxCostMoney = 1_000_000_000_000
	// MaxBuildTime bounds how long construction may take, GAME time.
	MaxBuildTime = 60 * 24 * time.Hour
	// MaxTier bounds Tier (village=1 .. an extension point's own ceiling,
	// ADR 0031 section 3.2's table goes to 4).
	MaxTier = 4
)

// RoleTier names one step of one role's growth (ADR 0031 section 3.2): a
// tier of a role several building codes may share.
type RoleTier struct {
	Role string
	Tier int
}

// TerrainMode is how a building relates to the terrain it names. Unlike
// settlementknowledge.TerrainMode, a building has no "preferred" concept in
// v1: a building either needs specific terrain under its footprint or it
// does not.
type TerrainMode string

const (
	TerrainNone     TerrainMode = ""
	TerrainRequired TerrainMode = "required"
)

// Def is one building type of the catalogue. It is content.
type Def struct {
	Code string
	// Role is the function this building fills ("security", "craft",
	// "extraction", "water_infra", "food", "health", "education",
	// "market", "storage"). Empty for the universal founding-kit rows
	// (road, civic_hall) that exist regardless of any branch (ADR 0028
	// section 7).
	Role string
	// Tier is how far Role has grown: 1 (village, informal) through 4 (an
	// extension point, ADR 0031 section 6). Zero for a Role=="" row.
	Tier int

	FootprintW, FootprintH int

	// RequiresKnowledge are settlement_knowledge codes the settlement must
	// hold (AND). Opaque strings here; see the package doc.
	RequiresKnowledge []string
	// RequiresKnowledgeCapability are settlement_knowledge CAPABILITY tags
	// the settlement must satisfy — each by ANY held item that Provides it
	// (AND across tags, OR within one), the identical branching mechanism
	// settlementknowledge.Tech.RequiresCapability gives knowledge items
	// (ADR 0031 section 3.1), mirrored here for a building's own gate: a
	// tier-2 market building may need "market_access" without naming
	// periodic_market, endowed_market_hall or trading_post specifically.
	// Opaque strings here too — see the package doc.
	RequiresKnowledgeCapability []string
	// RequiresBuildingRole is set for a tier promotion: the settlement
	// must already have ANY building of that exact role/tier standing
	// (the OR-mechanism, ADR 0031 section 3.2). Nil for a tier-1 (or
	// role-less) building.
	RequiresBuildingRole *RoleTier

	// TerrainTags/TerrainMode: terrain the FOOTPRINT itself must satisfy
	// (a port needs a coastal lot somewhere in it; a farm needs an arable
	// biome; a mine needs a deposit cell — the caller expresses each of
	// these as a terrain tag the same way settlementknowledge does).
	TerrainTags []string
	TerrainMode TerrainMode

	CostMoney int64
	// CostMaterials is finished-goods component code -> quantity (ADR
	// 0021's finished goods: concrete, steel, timber).
	CostMaterials map[string]int64
	BuildTime     time.Duration
	// Upkeep is drawn from the settlement's treasury every settlement
	// period (ADR 0028 section 8.5).
	Upkeep int64

	// MinLiteracyShareBPS gates this building on the settlement's own
	// literacy share (ADR 0031 section 4.4: "school ... requires
	// literacy_share >= threshold"), the same scale and the same rule
	// settlementknowledge.Tech.MinLiteracyShareBPS uses. Zero means no
	// gate.
	MinLiteracyShareBPS int

	// Storage is how many units of goods, in all, this standing building adds
	// to the village stock's capacity (a granary): the stock holds at most the
	// settlement's base capacity plus every standing building's Storage.
	Storage int64

	// Work is what a standing building makes when villagers work in it
	// (ADR 0033 section 4.1's daily loop): nothing without workers, never
	// a passive income. Zero value means "not a workplace".
	Work Work

	// Effects feed ADR 0028 section 8.1's coverage numbers
	// (food_coverage_bps, job_coverage_bps, service_coverage_bps,
	// happiness_bps) plus local_security_bps (ADR 0031 section 3.2). Open
	// target, same item.Effect shape as everywhere else.
	Effects []item.Effect
}

// Work is a workplace's production. Every shift a resident works in a
// standing building takes Consumes out of the village stock when it starts and
// puts Produces into it when it ends, and pays Wage from the treasury. The
// shape is content; how many shifts run at once is Workers.
type Work struct {
	// Produces and Consumes are component code -> quantity per shift.
	Produces map[string]int64
	Consumes map[string]int64
	// Workers is how many residents may work here at the same time.
	Workers int
	// Shift is how long one shift lasts, GAME time.
	Shift time.Duration
	// Wage is paid per finished shift from the village treasury, minor units.
	Wage int64
}

// Workplace reports whether the building can be worked in at all.
func (w Work) Workplace() bool { return len(w.Produces) > 0 }

// MaxShift bounds one shift, GAME time.
const MaxShift = 24 * time.Hour

// The settlement tiers, smallest first (internal/domain/settlement.Tier*).
const (
	SettlementVillage = "village"
	SettlementTown    = "town"
	SettlementCity    = "city"
)

var settlementTierRank = map[string]int{SettlementVillage: 1, SettlementTown: 2, SettlementCity: 3}

// MinSettlementTier is the smallest settlement that may list and build this
// building (ADR 0033 section 3): a role tier 1 is a village's, tier 2 a town's,
// tier 3 and up a city's. A row with no role tier (a legacy row) is a
// village's.
func (d Def) MinSettlementTier() string {
	switch {
	case d.Tier <= 1:
		return SettlementVillage
	case d.Tier == 2:
		return SettlementTown
	default:
		return SettlementCity
	}
}

// ListedAt reports whether a settlement of this tier lists the building: its
// own tier or below. An unknown tier lists nothing.
func (d Def) ListedAt(settlementTier string) bool {
	have, ok := settlementTierRank[settlementTier]
	return ok && have >= settlementTierRank[d.MinSettlementTier()]
}

// CanRotate reports whether rotating this building's footprint would
// change anything — false for a square footprint (including every 1x1),
// where "rotated" and "not rotated" place the identical lots.
func (d Def) CanRotate() bool { return d.FootprintW != d.FootprintH }

// Rotate returns d with its footprint's width and height swapped — turning
// a building the leader is about to place through 90 degrees before
// choosing its lot (ADR 0028 section 6: the leader places a building on
// the settlement's own grid; a building's own footprint orientation is
// part of that placement, not a fixed fact of the building type). Every
// other field is unchanged: a rotated building still needs the same
// knowledge, terrain and cost — only which lots its own footprint covers
// changes.
func (d Def) Rotate() Def {
	d.FootprintW, d.FootprintH = d.FootprintH, d.FootprintW
	return d
}

// Catalogue is the whole building catalogue, keyed by code.
type Catalogue map[string]Def

// ValidateCatalogue applies every load-time rule and returns every problem
// found, joined.
func ValidateCatalogue(defs []Def) error {
	var errs []error
	fail := func(sentinel error, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, args...)))
	}
	byCode := map[string]Def{}
	provided := map[RoleTier]bool{}
	for _, d := range defs {
		if d.Code == "" {
			fail(ErrInvalidBuilding, "a building has no code")
			continue
		}
		if _, dup := byCode[d.Code]; dup {
			fail(ErrInvalidBuilding, "%q declared twice", d.Code)
		}
		byCode[d.Code] = d
		if d.Role != "" {
			provided[RoleTier{d.Role, d.Tier}] = true
		}

		if d.FootprintW < 1 || d.FootprintW > MaxFootprint || d.FootprintH < 1 || d.FootprintH > MaxFootprint {
			fail(ErrInvalidBuilding, "%q footprint %dx%d", d.Code, d.FootprintW, d.FootprintH)
		}
		if d.CostMoney < 0 || d.CostMoney > MaxCostMoney {
			fail(ErrInvalidBuilding, "%q cost %d", d.Code, d.CostMoney)
		}
		for material, qty := range d.CostMaterials {
			if qty <= 0 {
				fail(ErrInvalidBuilding, "%q material %q quantity %d", d.Code, material, qty)
			}
		}
		if d.BuildTime <= 0 || d.BuildTime > MaxBuildTime {
			fail(ErrInvalidBuilding, "%q build time %s", d.Code, d.BuildTime)
		}
		if d.Upkeep < 0 {
			fail(ErrInvalidBuilding, "%q upkeep %d", d.Code, d.Upkeep)
		}
		if d.Role == "" && d.Tier != 0 {
			fail(ErrInvalidBuilding, "%q has a tier but no role", d.Code)
		}
		if d.Role != "" && (d.Tier < 1 || d.Tier > MaxTier) {
			fail(ErrInvalidBuilding, "%q role %q has tier %d", d.Code, d.Role, d.Tier)
		}
		if d.RequiresBuildingRole != nil {
			if d.RequiresBuildingRole.Tier >= d.Tier && d.Role != "" {
				fail(ErrInvalidBuilding, "%q requires its own role/tier %+v to be already standing", d.Code, *d.RequiresBuildingRole)
			}
		}
		for _, e := range d.Effects {
			if err := item.ValidateEffect(e); err != nil {
				fail(ErrInvalidBuilding, "%q effect: %v", d.Code, err)
			}
		}
		if len(d.TerrainTags) == 0 && d.TerrainMode != TerrainNone {
			fail(ErrInvalidBuilding, "%q sets a terrain mode with no terrain tags", d.Code)
		}
		if len(d.TerrainTags) > 0 && d.TerrainMode == TerrainNone {
			fail(ErrInvalidBuilding, "%q names terrain tags with no terrain mode", d.Code)
		}
		if d.MinLiteracyShareBPS < 0 || d.MinLiteracyShareBPS > 10_000 {
			fail(ErrInvalidBuilding, "%q literacy threshold %d", d.Code, d.MinLiteracyShareBPS)
		}
		if d.Storage < 0 {
			fail(ErrInvalidBuilding, "%q storage %d", d.Code, d.Storage)
		}
		if w := d.Work; w.Workplace() {
			if w.Workers < 1 || w.Workers > 50 {
				fail(ErrInvalidBuilding, "%q workers %d", d.Code, w.Workers)
			}
			if w.Shift <= 0 || w.Shift > MaxShift {
				fail(ErrInvalidBuilding, "%q shift %s", d.Code, w.Shift)
			}
			if w.Wage < 0 {
				fail(ErrInvalidBuilding, "%q wage %d", d.Code, w.Wage)
			}
			for c, q := range w.Produces {
				if q <= 0 {
					fail(ErrInvalidBuilding, "%q produces %q quantity %d", d.Code, c, q)
				}
				if w.Consumes[c] > 0 {
					fail(ErrInvalidBuilding, "%q consumes what it produces (%q): a cycle", d.Code, c)
				}
			}
			for c, q := range w.Consumes {
				if q <= 0 {
					fail(ErrInvalidBuilding, "%q consumes %q quantity %d", d.Code, c, q)
				}
			}
		} else if len(w.Consumes) > 0 || w.Workers != 0 || w.Wage != 0 {
			fail(ErrInvalidBuilding, "%q declares workers, wage or inputs but produces nothing", d.Code)
		}
	}
	for _, code := range sortedCodes(byCode) {
		d := byCode[code]
		if d.RequiresBuildingRole != nil && !provided[*d.RequiresBuildingRole] {
			fail(ErrUnreachableRole, "%q requires %+v", d.Code, *d.RequiresBuildingRole)
		}
	}
	return errors.Join(errs...)
}

// sortedCodes is deterministic iteration for anything built from a map.
func sortedCodes(byCode map[string]Def) []string {
	out := make([]string, 0, len(byCode))
	for c := range byCode {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

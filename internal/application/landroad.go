package application

import (
	"context"
	"time"
)

// A road opens the land it reaches (docs/adr/0044 5.5, migration 0110). The
// rows below are the stored plan; the rules are internal/domain/landroad.

// Water kinds of a stored road cell.
const (
	RoadWaterNone   = 0
	RoadWaterStream = 1
	RoadWaterRiver  = 2
)

// Reasons an open lot cannot be used.
const (
	OpenLotWater = "water"
	OpenLotSteep = "steep"
)

// RoadPlanRow is one drawn road (settlement_road_plans).
type RoadPlanRow struct {
	ID, SettlementID, DrawnBy string
	Class, Surface            string
	Lots, CrossingLots        int
	ClimbM, LengthM           int
	FromX, FromY, ToX, ToY    int
	CreatedAt                 time.Time
	CancelledAt               *time.Time
}

// TileKey names a world tile in a row: its cube face and grid position.
type TileKey struct {
	Face   int
	GX, GY int32
}

// RoadCellRow is one lot of a stored plan (settlement_road_cells).
type RoadCellRow struct {
	SettlementID string
	X, Y         int
	PlanID       string
	Seq          int
	HasParent    bool
	ParentX      int
	ParentY      int
	Water        int
	ElevationM   float64
	Tile         TileKey
	BuiltAt      *time.Time
}

// Built reports whether the road is laid on the cell.
func (c RoadCellRow) Built() bool { return c.BuiltAt != nil }

// OpenLotRow is one lot a road opened for sale (settlement_open_lots).
type OpenLotRow struct {
	SettlementID     string
	X, Y             int
	PlanID           string
	ServesX, ServesY int
	Dist             int
	Buildable        bool
	Reason           string
	HeightM, SlopeM  float64
	Biome, Water     string
	Tags             []string
	Tile             TileKey
}

// LandRoadRepository is the stored side of road plans, reached through
// Tx.Citizens (it is the same land lock and the same tables family).
type LandRoadRepository interface {
	// Plans lists a settlement's live plans, oldest first.
	Plans(ctx context.Context, settlementID string) ([]RoadPlanRow, error)
	// Cells lists the cells of the settlement's live plans, plan by plan in order.
	Cells(ctx context.Context, settlementID string) ([]RoadCellRow, error)
	// OpenLots lists the lots the settlement's roads opened.
	OpenLots(ctx context.Context, settlementID string) ([]OpenLotRow, error)
	// InsertPlan stores a drawn road with its cells and open lots in one go. A
	// cell or lot a second plan would take twice is kept as the first had it.
	InsertPlan(ctx context.Context, p RoadPlanRow, cells []RoadCellRow, open []OpenLotRow) error
	// MarkBuilt lays the road on the given cells (those not laid yet) and
	// returns how many it laid: a repeated call lays none.
	MarkBuilt(ctx context.Context, settlementID string, cells [][2]int, at time.Time) (int, error)
	// CancelPlan closes a plan and removes its cells; false when it was
	// already closed (the fence). The open lots are reconciled by SyncOpenLots.
	CancelPlan(ctx context.Context, planID string, at time.Time) (bool, error)
	// SyncOpenLots makes the stored open lots exactly want, except that a lot
	// somebody owns is never removed.
	SyncOpenLots(ctx context.Context, settlementID string, want []OpenLotRow) error
	// ForeignTiles lists, of the tiles on a face inside the box, those that
	// carry a road cell of another settlement.
	ForeignTiles(ctx context.Context, settlementID string, face int, gxMin, gxMax, gyMin, gyMax int32) ([]TileKey, error)
}

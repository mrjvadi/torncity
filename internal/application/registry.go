package application

import (
	"context"
	"errors"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/landuse"
)

// The land registry (docs/adr/0072, migration 0150): parcels, zones, tile claims and foundations. A parcel groups the lots one owner
// holds; a zone is a land-use rule players open on cells; a claim holds a tile of the world for a settlement; a foundation is laid on
// a lot a building could not stand on unaided.

// Owner kinds of a parcel.
const (
	ParcelOwnerTreasury = "treasury"
	ParcelOwnerPlayer   = "player"
)

// Zone kinds a zone may take beyond the building functions' zone kinds (content zoneKinds): the common land and a zone that restricts nothing.
const (
	ZoneKindCommons = "commons"
	ZoneKindOpen    = "open"
)

// The ledger reasons of the registry. A parcel change fee is a transfer to the treasury; the zone fee, the claim and the foundation's
// wage end money into the system sink (like any construction cost).
const (
	ReasonParcelChangeFee Reason = "parcel_change_fee"
	ReasonZoneOpenFee     Reason = "zone_open_fee"
	ReasonSettlementClaim Reason = "settlement_claim"
	ReasonFoundationWorks Reason = "foundation_works"
	ReasonClaimUpkeep     Reason = "claim_upkeep"
)

// Parcel is the lots one owner holds together.
type Parcel struct {
	ID, SettlementID string
	OwnerKind        string
	// OwnerID is the player; empty for the treasury.
	OwnerID   string
	Use       string
	ZoneID    string
	Cells     []landuse.Cell
	CreatedAt time.Time
}

// Zone is a land-use rule opened on cells (a decision of players through the charter).
type Zone struct {
	ID, SettlementID string
	Name, Kind       string
	OpenedBy         string
	OpenedAt         time.Time
	// EffectiveAt is when its rules bite (the notice runs until then).
	EffectiveAt time.Time
	Cells       []landuse.Cell
}

// TileClaim is a tile of the world a settlement holds.
type TileClaim struct {
	ID, SettlementID string
	Tile             TileKey
	ClaimedBy        string
	ClaimedAt        time.Time
	// PaidThrough is the instant the upkeep is paid to.
	PaidThrough         time.Time
	LedgerTransactionID string
}

// Foundation is a foundation laid on a lot.
type Foundation struct {
	SettlementID string
	X, Y         int
	LaidBy       string
	Steps        int
	LaidAt       time.Time
}

var (
	// ErrZoneCellTaken means a cell already lies in an open zone.
	ErrZoneCellTaken = errors.New("application: a cell already lies in a zone")
	// ErrTileClaimed means another settlement (or this one) already holds the tile.
	ErrTileClaimed = errors.New("application: the tile is already claimed")
	// ErrParcelNotFound means no such parcel.
	ErrParcelNotFound = errors.New("application: parcel not found")
)

// RegistryRules are the numbers and dates of the registry (config settlement.parcel_*, zone_*, slope_*, foundation_*, claim_*).
type RegistryRules struct {
	ParcelMaxCells    int
	ParcelChangeFee   int64
	ZoneOpenFee       int64
	ZoneMaxCells      int
	ZoneNotice        time.Duration
	ZoningRuleAt      time.Time
	ZoningGraceDays   int64
	SlopeRuleAt       time.Time
	SlopeGraceDays    int64
	Slope             landuse.Bands
	FoundationStone   int64
	FoundationWage    int64
	ClaimCost         int64
	ClaimUpkeepPerDay int64
	ClaimMaxTiles     int
}

// ZoningOn reports whether zone.open and zone.close are in force at now (the rule date plus the grace).
func (r RegistryRules) ZoningOn(now time.Time) bool {
	return !r.ZoningRuleAt.IsZero() && !now.Before(r.ZoningRuleAt.AddDate(0, 0, int(max(r.ZoningGraceDays, 0))))
}

// SlopeOn reports whether the slope bands count for a new building at now.
func (r RegistryRules) SlopeOn(now time.Time) bool {
	return !r.SlopeRuleAt.IsZero() && r.Slope.Valid() && !now.Before(r.SlopeRuleAt.AddDate(0, 0, int(max(r.SlopeGraceDays, 0))))
}

// SlopeStartsAt is the day the slope bands start to count.
func (r RegistryRules) SlopeStartsAt() time.Time {
	return r.SlopeRuleAt.AddDate(0, 0, int(max(r.SlopeGraceDays, 0)))
}

// ZoningStartsAt is the day zones start to count.
func (r RegistryRules) ZoningStartsAt() time.Time {
	return r.ZoningRuleAt.AddDate(0, 0, int(max(r.ZoningGraceDays, 0)))
}

// RegistryRepository persists parcels, zones, claims and foundations. Reach it through Tx.Registry.
type RegistryRepository interface {
	// Parcels lists a settlement's parcels with their cells, in id order.
	Parcels(ctx context.Context, settlementID string) ([]Parcel, error)
	// ParcelByID returns one parcel with its cells, or ErrParcelNotFound.
	ParcelByID(ctx context.Context, settlementID, id string) (*Parcel, error)
	// InsertParcel records a parcel and its cells.
	InsertParcel(ctx context.Context, p Parcel) error
	// MoveCells moves the cells of one parcel into another and drops the first when it is left empty (a join).
	MoveCells(ctx context.Context, settlementID, fromID, toID string, at time.Time) error
	// SplitOff moves cells out of a parcel into a new one (the new parcel's id and owner are given).
	SplitOff(ctx context.Context, from string, to Parcel, at time.Time) error
	// DropParcelsOfZone removes the treasury parcels a commons zone made, when it closes.
	DropParcelsOfZone(ctx context.Context, zoneID string) error
	// Zones lists the open zones of a settlement with their cells.
	Zones(ctx context.Context, settlementID string) ([]Zone, error)
	// OpenZone records a zone and its cells; ErrZoneCellTaken when a cell is in a zone already.
	OpenZone(ctx context.Context, z Zone) error
	// CloseZone closes a zone and frees its cells; false when it was closed already (the fence).
	CloseZone(ctx context.Context, settlementID, zoneID, by string, at time.Time) (bool, error)
	// ZoneCells maps each given cell to the zone that holds it (missing: none), with the zone's kind and when it takes effect.
	ZoneCells(ctx context.Context, settlementID string, cells []landuse.Cell) (map[landuse.Cell]ZoneRef, error)
	// Claims lists a settlement's claims.
	Claims(ctx context.Context, settlementID string) ([]TileClaim, error)
	// ClaimedTilesOfOthers lists the tiles other settlements claim inside a box of one face.
	ClaimedTilesOfOthers(ctx context.Context, settlementID string, face int, gxMin, gxMax, gyMin, gyMax int32) ([]TileKey, error)
	// InsertClaim records a claim; ErrTileClaimed when the tile is held.
	InsertClaim(ctx context.Context, c TileClaim) error
	// DueClaims lists the claims whose upkeep is paid to before now.
	DueClaims(ctx context.Context, settlementID string, now time.Time) ([]TileClaim, error)
	// AdvanceClaim moves a claim's paid-through instant from one value to another; false when it was moved already (the fence).
	AdvanceClaim(ctx context.Context, id string, from, to time.Time) (bool, error)
	// DropClaim lets a claim lapse; false when it was dropped already.
	DropClaim(ctx context.Context, id string) (bool, error)
	// Foundations lists the foundations laid in a settlement.
	Foundations(ctx context.Context, settlementID string) ([]Foundation, error)
	// InsertFoundation records a foundation; false when the lot has one already (the fence).
	InsertFoundation(ctx context.Context, f Foundation) (bool, error)
	// Audit appends an audit row of a registry act.
	Audit(ctx context.Context, id, settlementID, kind, actor string, detail map[string]any, at time.Time) error
}

// ZoneRef is the zone a cell lies in.
type ZoneRef struct {
	ID, Name, Kind string
	EffectiveAt    time.Time
}

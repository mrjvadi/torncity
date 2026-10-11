package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The land registry (docs/adr/0072): parcels, zones, tile claims and foundations of a settlement, on one screen. Every act of the
// registry answers this screen with a Notice naming what just happened, or a refusal of kind land_*.
const (
	ScreenLandRegistry  = "settlement_land_registry"
	AddrLandRegistry    = "settlement:land.registry"
	AddrParcelJoin      = "settlement:parcel.join"
	AddrParcelSplit     = "settlement:parcel.split"
	AddrZoneOpen        = "settlement:zone.open"
	AddrZoneClose       = "settlement:zone.close"
	AddrClaim           = "settlement:claim"
	AddrFoundationLay   = "settlement:foundation.lay"
	LandNoticeJoined    = "parcel_joined"
	LandNoticeSplit     = "parcel_split"
	LandNoticeZoneOpen  = "zone_opened"
	LandNoticeZoneShut  = "zone_closed"
	LandNoticeClaimed   = "claimed"
	LandNoticeFoundated = "foundation_laid"
)

// The refusals of the registry (village refusal kinds; Telegram text village.refusal.<kind>).
const (
	LandNotYours       = "land_not_yours"
	LandNotAdjacent    = "land_not_adjacent"
	LandTooBig         = "land_too_big"
	LandNotOneParcel   = "land_not_one_parcel"
	LandWholeParcel    = "land_whole_parcel"
	LandNotConnected   = "land_not_connected"
	LandNoMoney        = "land_no_money"
	LandBadCells       = "land_bad_cells"
	LandOffLand        = "land_off_land"
	LandCellOwned      = "land_cell_owned"
	LandCellZoned      = "land_cell_zoned"
	LandZoneName       = "land_zone_name"
	LandZoneKind       = "land_zone_kind"
	LandZoneSize       = "land_zone_size"
	LandZoneNone       = "land_zone_none"
	LandZoningOff      = "land_zoning_off"
	LandSlopeOff       = "land_slope_off"
	LandTileClaimed    = "land_tile_claimed"
	LandTileForeign    = "land_tile_foreign"
	LandTileApart      = "land_tile_apart"
	LandClaimLimit     = "land_claim_limit"
	LandCommons         = "land_commons"
	LandNeedsFoundation = "land_needs_foundation"
	LandNoFoundation   = "land_no_foundation_needed"
	LandFoundationDone = "land_foundation_exists"
	LandNoMasonry      = "land_no_masonry"
	LandSteepest       = "land_too_steep"
	LandZoneForbids    = "land_zone_forbids"
)

// RegCell is one lot by its absolute coordinates.
type RegCell struct{ X, Y int }

// ParcelLine is a parcel: the lots one owner holds together.
type ParcelLine struct {
	ID string
	// Owner is the holder's name (empty for the treasury); Treasury says the treasury holds it (the commons).
	Owner    presentation.Named
	Treasury bool
	Mine     bool
	Cells    []RegCell
	Use      string
	Zone     *presentation.Named
	// CanJoin lists the parcels of the same owner this one touches; CanSplit says the viewer may cut a part out of it.
	CanJoin  []string
	CanSplit bool
}

// ZoneLine is an open zone.
type ZoneLine struct {
	ID, Name, Kind string
	Cells          int
	// Effective says its rules bite now; EffectiveAt is when they do (the notice runs until then).
	Effective   bool
	EffectiveAt time.Time
	OpenedBy    presentation.Named
	// CanClose says the viewer holds zone.close.
	CanClose bool
}

// ClaimLine is a tile of the world the settlement holds.
type ClaimLine struct {
	ID          string
	Face        int
	GX, GY      int32
	ClaimedAt   time.Time
	PaidThrough time.Time
}

// LandRegistryView is the registry of a settlement: its parcels, zones, claims and what the viewer may do.
type LandRegistryView struct {
	Village string
	// Notice is what the last act did (LandNotice*); empty when the screen was only opened.
	Notice  string
	Parcels []ParcelLine
	Zones   []ZoneLine
	Claims  []ClaimLine
	// Quote is what an act would cost, shown before the confirm; nil when nothing is asked.
	Quote *LandQuote
	// Foundations is how many lots carry a foundation.
	Foundations int
	// ZoningOn says zones count; ZoningStartsAt is the day they start when they do not yet.
	ZoningOn       bool
	ZoningStartsAt *time.Time
	// SlopeOn says the slope bands count for a new building; SlopeStartsAt is the day they start when they do not yet.
	SlopeOn       bool
	SlopeStartsAt *time.Time
	// CanZone, CanCloseZone, CanClaim and CanLayFoundation say what the viewer holds the permission for.
	CanZone, CanCloseZone, CanClaim, CanLayFoundation bool
	// ZoneKinds lists the kinds a zone may take.
	ZoneKinds []string
	// The numbers the forms show.
	ParcelMaxCells, ZoneMaxCells, ClaimMaxTiles int
	ParcelChangeFee, ZoneOpenFee, ClaimCost     int64
	ClaimUpkeepPerDay                           int64
	FoundationStone, FoundationWage             int64
	// SlopeFlatM, SlopeGentleM and SlopeUnusableM are the band limits in metres.
	SlopeFlatM, SlopeGentleM, SlopeUnusableM int
}

var screenLandRegistry = presentation.Define[LandRegistryView](ScreenLandRegistry, "village")

// LandRegistry is the screen.
func LandRegistry(c presentation.Ctx, v LandRegistryView) *presentation.Response {
	a := []presentation.Action{back(AddrLand), refresh(AddrLandRegistry)}
	for _, p := range v.Parcels {
		if p.Mine {
			for _, other := range p.CanJoin {
				a = append(a, act(AddrParcelJoin, p.ID, other).Named("parcel.join").About(p.ID))
			}
		}
	}
	for _, z := range v.Zones {
		if z.CanClose {
			a = append(a, act(AddrZoneClose, z.ID).Named("zone.close").About(z.ID))
		}
	}
	return screenLandRegistry.Response(c.Lang, v, a...)
}

// LandQuote is the price of a registry act, shown before it is confirmed: the act, where it is, what it costs in money and stone.
type LandQuote struct {
	// Act is one of the Addr* of the registry (settlement:parcel.join, ...).
	Act string
	// Args are the arguments the confirm press repeats, in order.
	Args []string
	// Cells is how many lots the act touches; Lot the lot a claim or a foundation is about.
	Cells int
	Lot   *RegCell
	// Money is the whole bill, StoneNeed the stone it takes, StoneHave what the viewer carries and StoneBuy what he buys at the reference price.
	Money, StoneNeed, StoneHave, StoneBuy int64
	// Cash is what the viewer (or the treasury, for the treasury's act) holds.
	Cash int64
}

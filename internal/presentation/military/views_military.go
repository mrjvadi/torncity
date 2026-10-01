package military

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// Callback addresses of the military screens.
const (
	AddrMinistry = "military:ministry"
	AddrForces   = "military:forces"
	AddrBranch   = "military:branch"
	AddrStation  = "military:station"
	AddrProcure  = "military:procure"
	AddrArmsBuy  = "military:buy"
)

// MilitaryConfirm confirms a purchase or a move.
const MilitaryConfirm = "yes"

// ForceClassLine is one class of a country's equipment: how many, told in a
// band to everyone and exactly to the cleared.
type ForceClassLine struct {
	Class Named
	Band  string
	// Count is exact; zero unless the viewer is cleared.
	Count int64
}

// BranchForces is one branch and its classes.
type BranchForces struct {
	Branch  Named
	Classes []ForceClassLine
}

// PeriodLine is a settled defence period, as the ministry reports it.
type PeriodLine struct {
	Levy          int64
	Appropriation int64
	UpkeepDue     int64
	UpkeepPaid    int64
}

// MinistryView is a country's ministry of defence.
type MinistryView struct {
	// Unavailable, when set, is the whole answer: the service is not offered in
	// the settlement the viewer stands in, and no other fact is carried.
	Unavailable *economy.Unavailable
	Country     GovPlace
	// Offices are the defence offices and who holds or acts for each.
	Offices []GovOffice
	// Treasury and Fund are the national treasury's and the defence fund's
	// balances: a state's budget is public.
	Treasury, Fund int64
	// The levers in force: the cities' share of their revenue, the defence
	// share of it, the arms export policy.
	RevenueShareBPS, DefenceBudgetBPS, ArmsExports int64
	// Last is the last settled period, nil before the first; NextIn and
	// NextAt when the next ends, NextIn zero when unknown.
	Last   *PeriodLine
	NextIn time.Duration
	NextAt time.Time
	// Forces is the summary by branch.
	Forces []BranchForces
	// Cleared is a viewer who sees the forces in full; Readiness and
	// Upkeep are theirs to read.
	Cleared   bool
	Readiness int64
	Upkeep    int64
	// CanProcure is the viewer who buys arms for the state.
	CanProcure bool
	Notice     *Notice
	// PendingLicences is how many defence licence applications wait for
	// the minister (docs/adr/0022, section 2.14).
	PendingLicences int
}

// ForcesView is a country's forces by branch.
type ForcesView struct {
	// Unavailable, when set, is the whole answer: the service is not offered in
	// the settlement the viewer stands in, and no other fact is carried.
	Unavailable *economy.Unavailable
	Country     GovPlace
	Branches    []BranchForces
	// Cleared viewers see counts, readiness and upkeep, and a button per
	// branch.
	Cleared   bool
	Readiness int64
	Upkeep    int64
	// Moving is how many pieces are on their way to a garrison.
	Moving int64
}

// GarrisonLine is how many pieces of a group stand in one city.
type GarrisonLine struct {
	CityCode, City string
	Count          int64
}

// AssetGroup is one good (and design) of a branch's equipment.
type AssetGroup struct {
	Good  Good
	Class Named
	Count int64
	// Quality is the pieces' average.
	Quality int
	// Garrisons are where its pieces stand; Depot how many are stationed
	// nowhere yet (delivered, not deployed); Moving how many are on the
	// way.
	Garrisons []GarrisonLine
	Depot     int64
	Moving    int64
	// Committed are in an operation under way; Damaged out of action until
	// a defence period repairs them.
	Committed int64
	Damaged   int64
	// Attributes are the design's, in full: the cleared see the signature.
	Attributes []AttributeLine
	// SeenAt is how far a reference radar (a 1 m² target at
	// ReferenceRadarKM) sees it, for equipment with a radar cross-section;
	// zero when it has none.
	SeenAt int64
}

// MoveLine is equipment on its way to a garrison.
type MoveLine struct {
	Good           Good
	Qty            int64
	CityCode, City string
	Left           time.Duration
	At             time.Time
}

// BranchView is one branch's equipment, for a cleared viewer.
type BranchView struct {
	Country GovPlace
	Branch  Named
	Groups  []AssetGroup
	Moves   []MoveLine
	// CanStation is the viewer who commands the branch (or acts for its
	// commander).
	CanStation bool
	// ReferenceRadarKM is the reference radar SeenAt is measured against.
	ReferenceRadarKM int64
	Notice           *Notice
}

// StationView is ordering equipment to a garrison: choose the city, then how
// many, then confirm.
type StationView struct {
	Country GovPlace
	Branch  Named
	Good    Good
	// Available is how many may be ordered: the group's pieces not moving
	// and not already in the chosen city.
	Available int64
	// Cities are the country's cities to choose from; CityCode and City the
	// chosen one.
	Cities         []GovPlace
	CityCode, City string
	Qty            int64
	// Time is how long the move takes, real time on the game clock.
	Time time.Duration
	// Confirm asks for the confirmation of Qty.
	Confirm bool
}

// ProcureOffer is one listing of military goods, as a state's buyer sees it.
type ProcureOffer struct {
	No             int64
	Good           Good
	Company        CompanyRef
	CityCode, City string
	Country        GovPlace
	Left           int64
	Price          int64
	// Blocked says why the state may not buy it: "" when it may,
	// ProcureBlockedExport or ProcureBlockedEmbargo.
	Blocked string
}

// Why a listing is not for a state.
const (
	ProcureBlockedExport  = "export"
	ProcureBlockedEmbargo = "embargo"
)

// ProcureView is procurement: the military goods for sale that the state
// may buy.
type ProcureView struct {
	// Unavailable, when set, is the whole answer: the service is not offered in
	// the settlement the viewer stands in, and no other fact is carried.
	Unavailable *economy.Unavailable
	Country     GovPlace
	Fund        int64
	Offers      []ProcureOffer
	Notice      *Notice
}

// ArmsBuyView is one listing a state may buy from: how many, then confirm.
type ArmsBuyView struct {
	Country    GovPlace
	Offer      ProcureOffer
	Attributes []AttributeLine
	Fund       int64
	Qty        int64
	// Confirm asks for the confirmation of Qty at Total.
	Confirm bool
	Total   int64
}

// Military refusals.
const (
	MilitaryRefusedNotFound  = "not_found"
	MilitaryRefusedNotHolder = "not_holder"
	MilitaryRefusedNotArms   = "not_arms"
	MilitaryRefusedExport    = "export"
	MilitaryRefusedFunds     = "funds"
	MilitaryRefusedStock     = "stock"
	MilitaryRefusedCity      = "city"
	MilitaryRefusedNoCountry = "no_country"
	// MilitaryRefusedLicenceState is a verdict on a licence that is not
	// in a state for it: decided already, or revoked already.
	MilitaryRefusedLicenceState = "licence_state"
)

// MilitaryRefusalView is a refused military command.
type MilitaryRefusalView struct {
	Kind    string
	Country GovPlace
	// Office is the office whose holder may do it (not_holder).
	Office string
	// Need and Have are money (funds); Max a count (stock).
	Need, Have int64
	Max        int64
	Back       presentation.Ref
}

// MilitaryNoticeView is a private notice of the armed forces.
type MilitaryNoticeView struct {
	Country        GovPlace
	Good           Good
	Qty            int64
	CityCode, City string
	Branch         Named
}

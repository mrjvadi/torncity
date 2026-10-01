package military

// The military area's screens, by name on the wire. A name is part of the
// client contract (api/client-api.md): renaming one breaks every client in the
// field.
const (
	ScreenMinistry        = "ministry"
	ScreenForces          = "forces"
	ScreenBranch          = "branch"
	ScreenStation         = "station"
	ScreenProcure         = "procure"
	ScreenArmsBuy         = "arms_buy"
	ScreenMilitaryRefusal = "military_refusal"
	ScreenWarBoard        = "war_board"
	ScreenWarDeclare      = "war_declare"
	ScreenWarDecision     = "war_decision"
	ScreenWarRoom         = "war_room"
	ScreenWarTarget       = "war_target"
	ScreenWarLaunch       = "war_launch"
	ScreenStrikeReport    = "strike_report"
	ScreenWarRefusal      = "war_refusal"
	ScreenWarBlocked      = "war_blocked"
	ScreenCompanyDefence  = "company_defence"
	ScreenLicences        = "licences"
	ScreenKitPurchase     = "kit_purchase"
	ScreenStateRetrofit   = "state_retrofit"
	// The notices the armed forces and the war push to a player.
	ScreenMoveArrivedNotice = "move_arrived_notice"
	ScreenLicenceNotice     = "licence_notice"
	ScreenWarNotice         = "war_notice"
)

// The services a country-level screen answers "not available here" for: the
// Unavailable.Service of its view. Each is read from a tag of availability.yml
// that names the stage country.
const (
	ServiceArmedForces    = "armed_forces"
	ServiceProcurement    = "procurement"
	ServiceWar            = "war"
	ServiceDefenceLicence = "defence_licence"
)

// The availability tags the services above are read from: a kind and a code.
const (
	TagKindAction  = "government_action"
	TagKindOffice  = "office"
	TagProcure     = "country.procure"
	TagWar         = "country.war"
	TagLicence     = "country.defence_licence"
	TagDefenceHead = "defence_minister"
)

// RefusalCode is the code of a refused request: the area's name and the kind,
// "military_funds", "war_not_yet".
func RefusalCode(area, kind string) string { return area + "_" + kind }

// The quantities a stationing and a purchase offer besides the largest the
// stock allows.
var (
	StationQtyChoices = []int64{1, 5, 10}
	BuyQtyChoices     = []int64{1, 2, 5, 10}
)

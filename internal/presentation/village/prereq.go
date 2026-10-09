package village

import "github.com/mrjvadi/torncity/internal/presentation"

// A prerequisite as every upgrade view shows it (2026-10-09, the owner: every upgrade shows all its prerequisites and
// details). One structured line per thing the settlement lacks, so a client can name it, count it and link the fix.

// What a prerequisite is.
const (
	PrereqKnowledge  = "knowledge"
	PrereqBuilding   = "building"
	PrereqStaff      = "staff"
	PrereqItem       = "item"
	PrereqMoney      = "money"
	PrereqPermission = "permission"
	PrereqPersonal   = "personal"
	// PrereqLiteracy is the settlement's literacy share (Have and Need in basis points).
	PrereqLiteracy = "literacy"
	// PrereqTerrain is a land condition the settlement's place does not meet (Options name the tags).
	PrereqTerrain = "terrain"
)

// How the missing thing is got.
const (
	HowResearch = "research"
	HowBuild    = "build"
	HowTrain    = "train"
	HowBuy      = "buy"
	HowTravel   = "travel"
	// HowDonate: the treasury is short; residents donate or the settlement earns.
	HowDonate = "donate"
)

// Prerequisite is one missing thing and the way to get it.
type Prerequisite struct {
	Kind string
	// Item names it: the knowledge, the material, the role of a building (Role and Tier carry the rest).
	Item presentation.Named
	// Role and Tier are set for a building asked by role.
	Role string
	Tier int
	// Have and Need count it: units of a material, SUP of the treasury, basis points of literacy; 0 and 1 for a
	// knowledge item or a building.
	Have, Need int64
	// How is the way to get it (How* constants); Where names the place when it is a trip.
	How   string
	Where string
	// Options are the alternatives when any of several would do (the knowledge that provides a capability, the
	// buildings of a role).
	Options []presentation.Named
	// Makers are the workplaces that make a material; Price what Support asks per unit (0: not for sale).
	Makers []VillageMaker
	Price  int64
}

// UpgradeStaffLine is a post the next level seats.
type UpgradeStaffLine struct {
	Role  presentation.Named
	Slots int
	// WageBPS is the post's wage class, basis points of the market wage (0: the role's own).
	WageBPS    int
	ShiftHours int
}

// UpgradeCapacityLine is something the next level gives besides the plain effects: research slots, storage room by
// class.
type UpgradeCapacityLine struct {
	// Kind is "research_slots" or "storage_room"; Code the storage class when it is one.
	Kind  string
	Code  string
	Value int64
}

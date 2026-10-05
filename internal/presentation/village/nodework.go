package village

import "github.com/mrjvadi/torncity/internal/presentation"

// The work block of a standing building (roadmap 2.2, ADR 0041 section 11, rule 1c):
// every building is a working node, and the panel says what it does, who works there,
// what it uses and gives, and WHY it stands idle, in codes and numbers only (each client
// words them in its own locale).

// Node kinds (WorkNode.Kind).
const (
	NodeKindProduction     = "production"
	NodeKindExtraction     = "extraction"
	NodeKindService        = "service"
	NodeKindStorage        = "storage"
	NodeKindHousing        = "housing"
	NodeKindInfrastructure = "infrastructure"
	// NodeKindNone is a building the game gives no work to yet (honestly said).
	NodeKindNone = "none"
)

// Node statuses (WorkNode.Status).
const (
	NodeWorking = "working"
	NodeIdle    = "idle"
)

// Reasons a node stands idle or does less than it could (WorkReason.Code).
const (
	// NodeReasonNoFunction: the building has no work in the game yet.
	NodeReasonNoFunction = "no_function"
	// NodeReasonNoStaff: nobody is working a shift there.
	NodeReasonNoStaff = "no_staff"
	// NodeReasonNoInput: an input is not in the store (Item, Have, Need).
	NodeReasonNoInput = "no_input"
	// NodeReasonStorageFull: no room for the output (Class, Need spaces, Have free).
	NodeReasonStorageFull = "storage_full"
	// NodeReasonEmployerBroke: the treasury cannot pay the wage (Have balance, Need wage).
	NodeReasonEmployerBroke = "employer_broke"
	// NodeReasonNoKeeper: a store with no keeper gives only its communal room.
	NodeReasonNoKeeper = "no_keeper"
)

// WorkReason is one reason a node is not working, with the numbers behind it.
type WorkReason struct {
	Code string
	// Item names the missing input; Class the storage class that is full.
	Item  *presentation.Named `json:"item,omitempty"`
	Class string              `json:"class,omitempty"`
	// Have and Need are the quantities (units, spaces or money).
	Have, Need int64
}

// WorkSlot is one post of the building: its role and who holds it.
type WorkSlot struct {
	Role string
	// Worker is "player", "npc" or "empty"; Name is the player's name when it is one.
	Worker string
	Name   string `json:"name,omitempty"`
}

// WorkItemLine is an item and a quantity per shift.
type WorkItemLine struct {
	Item presentation.Named
	Qty  int64
}

// WorkNode is the work block.
type WorkNode struct {
	Kind   string
	Status string
	// Reasons are why it stands idle or is held back; empty when it works freely.
	Reasons []WorkReason
	// Slots are the posts (Filled of Max are held); empty for a node with no staff.
	Slots       []WorkSlot
	Filled, Max int
	// ShiftSeconds is how long one shift takes a player; Wage what the treasury pays
	// for it; Inputs and Outputs per shift.
	ShiftSeconds int64
	Wage         int64
	Inputs       []WorkItemLine
	Outputs      []WorkItemLine
	// Storage is the class the outputs go to with its free room (nil when there is no
	// output).
	StorageClass string `json:"storage_class,omitempty"`
	StorageFree  int64  `json:"storage_free,omitempty"`
	// IfUnstaffed is what the content says stands without staff: idle, base_room, decays.
	IfUnstaffed string `json:"if_unstaffed,omitempty"`
}

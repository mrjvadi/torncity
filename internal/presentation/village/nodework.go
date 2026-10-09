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
	// NodePaused: nobody works there and the crew stopped for a reason (WorkNode.Job.Paused).
	NodePaused = "paused"
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
	// NodeReasonNoFood: the village kitchen cannot feed a shift (an NPC does not start).
	NodeReasonNoFood = "no_food"
	// NodeReasonNeedsRepair: the workplace is too worn to work (closed).
	NodeReasonNeedsRepair = "needs_repair"
	// NodeReasonBudgetSpent: the job's shifts, or the posts' shifts for the local day, are used up.
	NodeReasonBudgetSpent = "budget_spent"
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

// WorkCondition is a workplace's wear (ADR 0041 6.10): BPS is its condition (10000 new),
// DecayBPSPerDay the wear per local day, OutputBPS the share of its output it still gives (0
// when Closed). CanRepair is true once a repair job may be posted (the head posts it with
// settlement.labor.post); RepairShifts and RepairMaterials are what it would take.
type WorkCondition struct {
	BPS             int64
	DecayBPSPerDay  int64
	OutputBPS       int64
	Closed          bool
	CanRepair       bool
	RepairShifts    int
	RepairMaterials []WorkItemLine
	// RepairJob is the open repair job (nil when none): its id is taken on the labour board.
	RepairJob *WorkJob `json:"repair_job,omitempty"`
}

// WorkJob is the hiring-board job of a standing workplace: its NPC crew and why it stopped.
type WorkJob struct {
	ID string
	// Wage a player is paid per shift; NPCCrew how many NPC labourers the head keeps on it;
	// ShiftsLeft the budget of shifts still paid; Priority 1 (first) to 4.
	Wage       int64
	NPCCrew    int
	ShiftsLeft int
	Priority   int
	// Paused is why the crew stopped (the reason codes above), empty when it runs.
	Paused string `json:"paused,omitempty"`
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
	// MealPoints is the food a shift eats (0: exempt, its worker eats from the produce),
	// FoodShifts how many such shifts the kitchen pot and the stock's food feed.
	MealPoints int64 `json:"meal_points,omitempty"`
	FoodShifts int64 `json:"food_shifts,omitempty"`
	// Condition is the wear of a production workplace (nil for a building that does not wear).
	Condition *WorkCondition `json:"condition,omitempty"`
	// Job is the posted job of the workplace (nil when the head has not posted one).
	Job *WorkJob `json:"job,omitempty"`
	// ToolWearBPS is the share of a tool one shift wears (0: the work needs none), ToolsHave the tools in the stock, and
	// BareHands says the next shift works at BareHandsBPS of its output because its tool is worn out and the stock has none.
	// KnowledgeBPS is the share of output the settlement's knowledge of this craft adds to every shift.
	KnowledgeBPS int64 `json:"knowledge_bps,omitempty"`
	ToolWearBPS  int64 `json:"tool_wear_bps,omitempty"`
	ToolsHave    int64 `json:"tools_have,omitempty"`
	BareHands    bool  `json:"bare_hands,omitempty"`
	BareHandsBPS int64 `json:"bare_hands_bps,omitempty"`
	// IfUnstaffed is what the content says stands without staff: idle, base_room, decays.
	IfUnstaffed string `json:"if_unstaffed,omitempty"`
}

package companies

import "time"

// ProduceTarget is something the company can make: a final design of its
// own, or a component it makes.
type ProduceTarget struct {
	Good Good
	// Batch is how many units one batch of a component yields; zero for a
	// design, which makes one unit per unit ordered.
	Batch int64
}

// ProductionLine is one production order.
type ProductionLine struct {
	No     int64
	Good   Good
	Output int64
	// Done orders have their quality; running ones their end.
	Done     bool
	Quality  int
	FinishAt time.Time
	Left     time.Duration
}

// OrdersView is a company's production floor.
type OrdersView struct {
	Ref     CompanyRef
	Targets []ProduceTarget
	// Locked are the components one step away, each with the way in.
	Locked  []LockedTarget
	Orders  []ProductionLine
	Max     int
	Running int
	// Crew is how many work an order: the owner and the employees.
	Crew int
}

// RecipeLine is one input of an order: what one unit (or batch) takes, what
// the whole order takes, and what the warehouse holds.
type RecipeLine struct {
	Component Named
	Per       int64
	Need      int64
	Have      int64
}

// ProducePresets are the order sizes the plan screen offers a button for.
var ProducePresets = []int64{1, 5, 10}

// ProduceView is the plan of an order of one target.
type ProduceView struct {
	Ref CompanyRef
	// Addr is the command a size or confirm button calls; empty means
	// AddrProduce. An upgrade-kit order (AddrProduceKit) reuses this same
	// screen — a kit's plan reads exactly like an order's, because it is
	// one: the design's own recipe, refitted onto an existing unit instead
	// of sold as a new one.
	Addr   string
	Target ProduceTarget
	// Qty is the order size planned; zero before one is chosen.
	Qty    int64
	Output int64
	Recipe []RecipeLine
	// Duration is the order's wait on the wall clock, FinishAt its end.
	Duration time.Duration
	FinishAt time.Time
	Crew     int
	// MaxQty is the most the warehouse can make now.
	MaxQty int64
	// Short is the order's shortages, when it cannot be placed.
	Short []Shortage
	// StockUp is what buying every short input from the city's suppliers
	// costs, when they sell them all: one tap buys them
	// (docs/adr/0021, section 14). Zero when they do not.
	StockUp int64
	// Bought is what the inputs just bought cost, after a one-tap
	// purchase.
	Bought int64
	// Placed is set once the order is running.
	Placed *ProductionLine
}

// SampleLine is a piece in the warehouse another company designed.
type SampleLine struct {
	Serial string
	Good   Good
	// Maker is the company whose design it is.
	Maker   string
	Quality int
	// ChanceBPS is the company's chance of recovering a design from it.
	ChanceBPS int64
}

// ReverseLine is one reverse engineering.
type ReverseLine struct {
	No       int64
	Good     Good
	Status   string
	FinishAt time.Time
	Left     time.Duration
	// Result is the copy's name when it succeeded.
	Result   string
	ResultNo int64
}

// ReverseLabView is a company's reverse-engineering lab.
type ReverseLabView struct {
	Ref     CompanyRef
	Samples []SampleLine
	Jobs    []ReverseLine
	// Confirm is the sample about to be taken apart.
	Confirm *SampleLine
	Skill   string
	Level   int
	Time    time.Duration
	Notice  string
}

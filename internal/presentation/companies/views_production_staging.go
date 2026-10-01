package companies

import "time"

// TechStep is a technology one step away, and how the company takes it:
// research it, or buy a license for it.
type TechStep struct {
	Tech     Named
	Research bool
}

// StudioKind is a good one step away from the studio.
type StudioKind struct {
	Item  Named
	Steps []TechStep
}

// LockedTarget is a component one step away from the floor.
type LockedTarget struct {
	Good  Good
	Steps []TechStep
}

// Next step kinds: the one thing a company's floor should do next.
const (
	StepDesignFirst = "design_first"
	StepDesignDraft = "design_draft"
	StepSell        = "sell"
	StepProduce     = "produce"
	StepProducing   = "producing"
	StepSupply      = "supply"
	StepBuyGoods    = "buy_goods"
	StepResearch    = "research"
	StepDesignNext  = "design_next"
)

// NextStep is the one step a company's floor should take next, worked out
// from where it stands: design a first product, buy its inputs, make it,
// sell it, research further, design something more advanced.
type NextStep struct {
	Kind string
	// Good is what the step is about: the product to make, sell or buy
	// inputs for; Qty how many (batches of a component, which yields Batch
	// units each); Total what the inputs cost.
	Good  Good
	Qty   int64
	Batch int64
	Total int64
	// Component is the input other companies make, for buy_goods.
	Component Named
	// Item is a good to design; Tech a technology to research for it.
	Item Named
	Tech Named
	// DesignNo and DesignName are the draft to finish.
	DesignNo   int64
	DesignName string
	// FinishAt and Left are when the running order is done.
	FinishAt time.Time
	Left     time.Duration
	// CanResearch is the owner, who alone runs the lab.
	CanResearch bool
}

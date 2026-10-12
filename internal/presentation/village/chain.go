package village

import "github.com/mrjvadi/torncity/internal/presentation"

// The chain view (docs/adr/0075 8, ADR 0045 8.6): what each kind of workplace of the settlement makes and uses an hour, how its inputs
// compare with what the running workplaces upstream make, and, for a target, the buildings and the hands the chain needs and the first
// link that cannot keep up. Rates are in thousandths of a unit an hour. Nothing here is worded or laid out.

// The chain screen.
const (
	ScreenChain = "settlement_chain"
	AddrChain   = "settlement:chain"
)

// Refusals of the chain screen.
const (
	ChainBadArgs = "chain_bad_args"
	ChainNoMaker = "chain_no_maker"
)

// ChainRateLine is an item and a rate an hour (thousandths of a unit).
type ChainRateLine struct {
	Item  presentation.Named
	Milli int64
}

// ChainFeedLine compares what a node asks of an item with what the running workplaces of the settlement make of it.
type ChainFeedLine struct {
	Item presentation.Named
	// NeedMilli is what the node's running buildings use an hour; HaveMilli what the running makers upstream make; Makers names them.
	NeedMilli, HaveMilli int64
	Makers               []presentation.Named
	// Stock is what the store holds of it now.
	Stock int64
}

// ChainNodeLine is a kind of workplace the settlement has built.
type ChainNodeLine struct {
	Node presentation.Named
	// Built is how many stand finished; Running how many worked in the last hour.
	Built, Running int
	ShiftMinutes   int64
	Slots          int64
	// Out and In are what one fully staffed building makes and uses an hour.
	Out, In []ChainRateLine
	Feeds   []ChainFeedLine
	// LastHour is what the node's buildings made in the last hour (from the statistics).
	LastHour []ChainRateLine
}

// ChainLineLine is a link of a target's chain.
type ChainLineLine struct {
	Node, Item presentation.Named
	// NeedMilli is what the chain wants of the item an hour; Count the buildings that makes; Running how many work; HaveMilli what they make.
	NeedMilli, HaveMilli int64
	Count                int64
	Running              int64
	Add                  int64
	Starved              bool
	Depth                int
}

// ChainRawLine is an item nothing in the settlement makes that the chain wants.
type ChainRawLine struct {
	Item      presentation.Named
	NeedMilli int64
	Stock     int64
}

// ChainView is the chain screen.
type ChainView struct {
	Village string
	Open    bool
	// Targets are the items a workplace of the settlement makes, to pick a target from; Target and PerHourMilli what was asked.
	Targets      []presentation.Named
	Target       presentation.Named
	PerHourMilli int64
	Lines        []ChainLineLine
	Raw          []ChainRawLine
	// First is the starved link nearest the target, when there is one.
	First *ChainLineLine
	Nodes []ChainNodeLine
}

var screenChain = presentation.Define[ChainView](ScreenChain, "village")

// Chain is the chain screen.
func Chain(c presentation.Ctx, v ChainView) *presentation.Response {
	a := []presentation.Action{back(AddrVillageOverview), refresh(AddrChain)}
	for _, t := range v.Targets {
		a = append(a, act(AddrChain, t.Code).Named("chain.target").About(t.Code))
	}
	return screenChain.Response(c.Lang, v, a...)
}

package village

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The trade desk (ADR 0049): the goods the settlement may put on sale to the travelling trader, what he would pay, the
// head's orders and the last market day. Nothing here is worded or laid out.

// The trade desk.
const (
	ScreenTradeDesk = "trade"
	AddrTradeDesk   = "settlement:trade"
)

// The acts of the trade desk, the first argument of settlement.trade.
const (
	// TradeActionKeep puts an item on sale and keeps back N units of it (0: sell it all).
	TradeActionKeep = "keep"
	// TradeActionOff takes an item off sale.
	TradeActionOff = "off"
)

// What a market day came to (the application's trade outcomes).
const (
	TradeOutcomeSold      = "sold"
	TradeOutcomeNoClerk   = "no_clerk"
	TradeOutcomeNoWage    = "no_wage"
	TradeOutcomeNothing   = "nothing"
	TradeOutcomeTooLittle = "too_little"
	TradeOutcomeNoRoad    = "no_road"
)

// TradeItemLine is one good the trader buys.
type TradeItemLine struct {
	Item presentation.Named
	// Stock is what the store holds; Reference the reference price, Unit what the trader pays per unit.
	Stock, Reference, Unit int64
	// On says it is on sale; Keep the units the head keeps back; Surplus what would be sold now.
	On      bool
	Keep    int64
	Surplus int64
}

// TradeSoldLine is one item of a market day.
type TradeSoldLine struct {
	Item presentation.Named
	Qty  int64
	Unit int64
}

// TradeDayLine is the last market day judged.
type TradeDayLine struct {
	Outcome string
	Gross   int64
	Wage    int64
	At      time.Time
	Lines   []TradeSoldLine
}

// TradeClerkLine is the clerk of the market's seat: the building he sits in, whether the labour pool has a person for it,
// his daily wage (paid on a day the trader comes) and who staffs it (a worker of the settlement's pool, "npc").
type TradeClerkLine struct {
	SeatBuilding presentation.Named
	Filled       bool
	Wage         int64
	StaffedBy    string
}

// TradeDeskView is the trade desk.
type TradeDeskView struct {
	Name string
	// HasPost says a market post stands (without one the trader does not come).
	HasPost bool
	// Cap is the most a visit pays; PriceBPS the share of the reference price the trader pays.
	Cap      int64
	PriceBPS int64
	// Prospect is what the trader would pay for the goods on sale if he came now.
	Prospect int64
	Items    []TradeItemLine
	Last     *TradeDayLine
	// NextAt is the next market day: the start of the settlement's next local day (zero without a market post).
	NextAt time.Time
	// Clerk is the clerk of the market's seat (nil without a market post).
	Clerk *TradeClerkLine
	// MayOrder says the viewer holds trade.export; KeepPresets are the amounts the desk offers to keep back.
	MayOrder    bool
	KeepPresets []int64
}

var screenTradeDesk = presentation.Define[TradeDeskView](ScreenTradeDesk, "village")

// TradeDesk is the trade desk.
func TradeDesk(c presentation.Ctx, v TradeDeskView) *presentation.Response {
	a := []presentation.Action{back(AddrMaterials), refresh(AddrTradeDesk)}
	if v.MayOrder && v.HasPost {
		for _, it := range v.Items {
			a = append(a, act(AddrTradeDesk, TradeActionKeep, it.Item.Code, "0").Named("trade.sell_all").About(it.Item.Code))
			for _, k := range v.KeepPresets {
				a = append(a, act(AddrTradeDesk, TradeActionKeep, it.Item.Code, strconv.FormatInt(k, 10)).Named("trade.keep").About(it.Item.Code))
			}
			if it.On {
				a = append(a, act(AddrTradeDesk, TradeActionOff, it.Item.Code).Named("trade.off").About(it.Item.Code))
			}
		}
	}
	return screenTradeDesk.Response(c.Lang, v, a...)
}

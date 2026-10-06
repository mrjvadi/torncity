package village

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The village desk (docs/adr/0033 section 6.8 and 6.10; roadmap 2.19 phase 2): the treasury's standing
// quote for its own money against SUP, at the live reference rate less the desk's fee. One atomic confirm
// with price protection; the conversion is never silent. The units it sells are the treasury's own holding:
// when they run out the desk says so, it never mints.

// Screen, addresses and refusals.
const (
	ScreenCurrencyDesk = "village_currency_desk"
	AddrCurrencyDesk   = "settlement:currency.desk"
	AddrCurrencyFee    = "settlement:currency.fee"

	// VillageDeskEmpty: the treasury holds too few units to sell.
	VillageDeskEmpty = "desk_empty"
	// VillageDeskNoSUP: the treasury holds too little SUP to buy units back.
	VillageDeskNoSUP = "desk_no_sup"
	// VillageDeskFunds: the player holds too little to convert.
	VillageDeskFunds = "desk_funds"
	// VillageDeskMoved: the price moved past the player's limit since the quote.
	VillageDeskMoved = "desk_moved"
)

// Stages of the desk screen.
const (
	// DeskMenu: the balances, the rate, the fee and the amounts to choose from.
	DeskMenu = "menu"
	// DeskAsk: the quote of one conversion, to confirm.
	DeskAsk = "ask"
	// DeskDone: the conversion was made.
	DeskDone = "done"
)

// DeskView is the desk screen.
type DeskView struct {
	Village string
	// Name and Symbol are the money's authored name and symbol (never the Latin code in Persian text).
	Name, Symbol string
	Stage        string
	// Side is "buy" (the player pays SUP and receives units) or "sell" (gives units, receives SUP).
	Side string
	// Amount is what the player offers: SUP for a buy, units for a sell. SUP and Units are the two
	// sides of the quote; Fee is kept by the treasury in the unit of what the player gives (SUP for a
	// buy, units for a sell) at FeeBPS.
	Amount, SUP, Units, Fee, FeeBPS int64
	// R0 and XRefPPM are the live rate (units per SUP = R0 x 1000000 / XRefPPM): never a peg.
	R0, XRefPPM int64
	// CashSUP and CashUnits are what the player holds; DeskUnits and DeskSUP what the treasury holds
	// to convert with.
	CashSUP, CashUnits, DeskUnits, DeskSUP int64
	// SlippageBPS is how far the price may move between the quote and the confirm before the desk
	// refuses (price protection).
	SlippageBPS int64
	// PresetsSUP are the SUP amounts the menu offers for a buy; PresetsUnits the unit amounts for a sell.
	PresetsSUP, PresetsUnits []int64
	// CanSetFee is true for the holder of currency.charter; MinFeeBPS and MaxFeeBPS bound the fee.
	CanSetFee, CanBuy, CanSell bool
	MinFeeBPS, MaxFeeBPS       int64
}

var screenCurrencyDesk = presentation.Define[DeskView](ScreenCurrencyDesk, "village")

// CurrencyDesk is the desk screen.
func CurrencyDesk(c presentation.Ctx, v DeskView) *presentation.Response {
	switch v.Stage {
	case DeskAsk:
		out := v.Units
		if v.Side == "sell" {
			out = v.SUP
		}
		return screenCurrencyDesk.Response(c.Lang, v,
			confirm(AddrCurrencyDesk, v.Side, strconv.FormatInt(v.Amount, 10), strconv.FormatInt(out, 10), ResidenceConfirm), back(AddrCurrencyDesk))
	case DeskDone:
		return screenCurrencyDesk.Response(c.Lang, v, back(AddrMoney), refresh(AddrCurrencyDesk))
	}
	a := []presentation.Action{back(AddrMoney), refresh(AddrCurrencyDesk)}
	for _, n := range v.PresetsSUP {
		a = append(a, act(AddrCurrencyDesk, "buy", strconv.FormatInt(n, 10)).Named("desk.buy"))
	}
	for _, n := range v.PresetsUnits {
		a = append(a, act(AddrCurrencyDesk, "sell", strconv.FormatInt(n, 10)).Named("desk.sell"))
	}
	if v.CanSetFee {
		for _, bps := range []int64{v.MinFeeBPS, 30, 100, v.MaxFeeBPS} {
			a = append(a, act(AddrCurrencyFee, strconv.FormatInt(bps, 10)).Named("desk.fee"))
		}
	}
	return screenCurrencyDesk.Response(c.Lang, v, a...)
}

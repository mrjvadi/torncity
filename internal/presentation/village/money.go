package village

import "github.com/mrjvadi/torncity/internal/presentation"

// The settlement's money value panel (docs/adr/0046-bags-merchants-currency-
// exchange.md section 7, phase M3). READ ONLY, and a display: nothing here moves
// money, and Nil never becomes settlement money (ADR 0029 10.4). The web map
// puts it with the currency in «بانک»; on Telegram it is reached from the shop,
// under «انبار و بازار».

// Screen and address.
const (
	ScreenVillageMoney = "village_money"
	AddrMoney          = "settlement:money"
)

// What the settlement's own currency does not have yet, as the core spells it.
// A figure that needs it is absent from the view, never made up (CLAUDE.md
// rule 2).
const (
	// MoneyNone: no market, reserve or cover exists for the currency yet (ADR
	// 0033 sections 6 and 7 are not built; roadmap 2.19).
	MoneyNone = "none"
)

// MoneyCurrency is the currency the settlement reserved at its founding.
type MoneyCurrency struct {
	Code, Name, Symbol string
	// Issued says the currency is in circulation; false until the settlement
	// currency exists (roadmap 2.19).
	Issued bool
}

// NilExample is an amount of the neutral currency and what it is worth in Nil.
type NilExample struct {
	Amount   int64
	NilMicro int64
}

// MoneyBasketLine is one good of the fixed basket (the tradable lines of the shop).
type MoneyBasketLine struct {
	Item presentation.Named
	Kind string
	// WeekMilli is the units one resident draws in a week, in thousandths.
	WeekMilli int64
	// Reference is the good's reference price; Price what the shop charges now,
	// 0 when it is not on the shelf today.
	Reference, Price int64
	OnShelf          bool
}

// MoneyChartered is a chartered settlement money's state (docs/adr/0033 section 6; roadmap 2.19):
// the live rate, the supply, the reserve and what the treasury holds of it. Absent until the money
// is chartered.
type MoneyChartered struct {
	// R0 is units per SUP at charter; XRefPPM the reference rate in parts per million (1000000 is
	// 1.00 until a book trades): the live rate is R0 x 1000000 / XRefPPM units per SUP, never a peg.
	R0, XRefPPM int64
	// Supply is the units in existence, PotSUP the reserve pot at the Reserve Bank in SUP, and
	// TreasuryUnits what the settlement's treasury holds of the money.
	Supply, PotSUP, TreasuryUnits int64
	// Status is chartered, wind_down or retired. BasisSUP is the backing basis, ExcessSUP the pot above it,
	// MarketCapSUP the supply's worth at the reference rate, CoverageBPS the pot over it (CoverageKnown false with
	// no supply), StabilisationUnits the units the head's purchases hold (docs/adr/0033 6.6).
	Status                            string
	BasisSUP, ExcessSUP, MarketCapSUP int64
	CoverageBPS                       int64
	CoverageKnown                     bool
	StabilisationUnits                int64
	// Macro is the latest macro reading and Trend the last seven, oldest first (docs/adr/0033 7.3); absent
	// until the first period closes.
	Macro *MoneyMacro  `json:"macro,omitempty"`
	Trend []MoneyMacro `json:"trend,omitempty"`
}

// MoneyView is what a settlement's money is worth.
type MoneyView struct {
	Village  string
	Currency MoneyCurrency
	// Chartered is set once the money exists; CanCharter says the viewer may charter it by hand
	// (it does not exist and the viewer holds currency.charter).
	Chartered  *MoneyChartered `json:"chartered,omitempty"`
	CanCharter bool            `json:"can_charter,omitempty"`
	// Market and Reserve say what the currency lacks: MoneyNone while the
	// market (x_ref) and the reserve with its cover do not exist.
	Market, Reserve string
	// NilUnitSup is how many units of the neutral currency one Nil stands for.
	NilUnitSup int64
	// NilPerUnitMicro is what one unit of the money the village works in (the
	// neutral currency) is worth, in millionths of a Nil.
	NilPerUnitMicro int64
	Examples        []NilExample
	// Treasury is the village treasury in the ledger; TreasuryNilMicro its worth
	// in Nil.
	Treasury, TreasuryNilMicro int64
	// Output is the goods the workplaces made in the last OutputDays game days,
	// at reference prices; OutputNilMicro its worth in Nil.
	Output, OutputNilMicro int64
	OutputDays             int
	Residents              int64
	// Basket is the fixed basket; IndexBPS is what it costs in the village as a
	// share of the reference (10000 = at the reference), over the lines on the
	// shelf, 0 with none; CoverBPS the share of the basket on the shelf today.
	Basket   []MoneyBasketLine
	IndexBPS int64
	CoverBPS int64
}

var screenMoney = presentation.Define[MoneyView](ScreenVillageMoney, "village")

// VillageMoney is the money value panel.
func VillageMoney(c presentation.Ctx, v MoneyView) *presentation.Response {
	a := []presentation.Action{back(AddrShop), refresh(AddrMoney)}
	if v.CanCharter {
		a = append(a, act(AddrCurrencyCharter).Named("currency.charter"))
	}
	if v.Chartered != nil {
		a = append(a, act(AddrCurrencyDesk).Named("currency.desk"), presentation.Do("fx.book").Named("fx.book"), act(AddrCurrencyReserve).Named("currency.reserve"))
	}
	return screenMoney.Response(c.Lang, v, a...)
}

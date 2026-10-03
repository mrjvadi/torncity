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

// MoneyView is what a settlement's money is worth.
type MoneyView struct {
	Village  string
	Currency MoneyCurrency
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
	return screenMoney.Response(c.Lang, v, back(AddrShop), refresh(AddrMoney))
}

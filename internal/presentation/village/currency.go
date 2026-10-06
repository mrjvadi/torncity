package village

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The head's charter of the settlement's own money (docs/adr/0033 section 6.2; roadmap 2.19 phase 1).
// A settlement is chartered automatically at founding and, once, at deploy for the old ones; this
// screen is the path for a settlement whose automatic charter could not be paid, and shows a chartered
// money's live rate and reserve to whoever opens it.

// Screen and address.
const (
	ScreenCurrencyCharter = "village_currency_charter"
	AddrCurrencyCharter   = "settlement:currency.charter"
)

// Stages of the charter screen.
const (
	// CharterAsk: the offer, with the fee, the deposit and the units it buys; confirm to charter.
	CharterAsk = "ask"
	// CharterDone: the money exists now.
	CharterDone = "done"
	// CharterExists: the settlement already has its money (the live rate and reserve).
	CharterExists = "exists"
)

// CurrencyCharterView is the charter screen.
type CurrencyCharterView struct {
	Village string
	// Name and Symbol are the currency's authored name and symbol (never the Latin code in Persian text).
	Name, Symbol string
	Stage        string
	// Treasury is the SUP the settlement holds; Fee and Deposit what a charter takes of it (Deposit
	// is the first deposit into the reserve pot, at least MinDeposit); both SUP minor units.
	Treasury, Fee, MinDeposit, Deposit int64
	// R0 is the chosen starting scale (units per SUP at charter; one of R0Options), Units what the
	// deposit buys at the reference rate after the mint fee (MintFeeBPS), kept in the pot.
	R0         int64
	R0Options  []int64
	MintFeeBPS int64
	Units      int64
	// CanPay is false when the treasury cannot cover the fee and the deposit: the head must top it
	// up first.
	CanPay bool
	// Supply, PotSUP and XRefPPM describe a money that exists (stage exists or done): the units in
	// existence, the reserve pot in SUP, and the live reference rate in parts per million
	// (1000000 is 1.00 until a book trades; never a peg).
	Supply, PotSUP, XRefPPM int64
}

var screenCurrencyCharter = presentation.Define[CurrencyCharterView](ScreenCurrencyCharter, "village")

// CurrencyCharter is the charter screen: the offer, the result, or the money's state.
func CurrencyCharter(c presentation.Ctx, v CurrencyCharterView) *presentation.Response {
	switch v.Stage {
	case CharterAsk:
		return screenCurrencyCharter.Response(c.Lang, v,
			confirm(AddrCurrencyCharter, strconv.FormatInt(v.R0, 10), strconv.FormatInt(v.Deposit, 10), ResidenceConfirm), back(AddrMoney))
	default:
		return screenCurrencyCharter.Response(c.Lang, v, back(AddrMoney), refresh(AddrCurrencyCharter))
	}
}

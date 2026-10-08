package village

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The reserve of a settlement's money and the head's tools over it (docs/adr/0033 sections 6.3 to 6.8, 6.13, 7;
// roadmap 2.19 phase 4): the valuation and coverage, the intervention on the book, mint more against a deposit,
// burn, the withdrawal of the excess after a public notice, retiring the money, and a holder's claim in a
// wind-down. The head's acts need the charter permissions currency.issue and bank.policy; the claim is any
// holder's.

// Screen and address.
const (
	ScreenCurrencyReserve = "village_reserve"
	AddrCurrencyReserve   = "settlement:currency.reserve"
)

// Actions of the reserve screen (the first argument of the command).
const (
	ReserveActionIssue    = "issue"
	ReserveActionBurn     = "burn"
	ReserveActionBuy      = "buy"
	ReserveActionSell     = "sell"
	ReserveActionWithdraw = "withdraw"
	ReserveActionCancel   = "cancel"
	ReserveActionRetire   = "retire"
	ReserveActionClaim    = "claim"
)

// Stages of the reserve screen.
const (
	ReserveMenu = "menu"
	ReserveAsk  = "ask"
	ReserveDone = "done"
)

// Refusals of the reserve screen, as the core spells them.
const (
	ReserveFunds    = "reserve_funds"
	ReserveNoUnits  = "reserve_no_units"
	ReserveNoExcess = "reserve_no_excess"
	ReserveBudget   = "reserve_budget"
	ReserveWinding  = "reserve_wind_down"
	ReserveNotWind  = "reserve_not_wind"
	ReserveNothing  = "reserve_nothing"
	ReserveInvalid  = "reserve_invalid"
	ReserveNotFound = "reserve_not_found"
)

// MoneyMacro is one period's macro reading (docs/adr/0033 7.3): the price indices (1000000 is 1.00 at charter),
// local inflation, supply growth and coverage.
type MoneyMacro struct {
	PeriodNo int64
	// XRefPPM is the reference rate at the tick; TradablePPM the tradable price index (it follows 1/x),
	// NonTradablePPM the local price index, PricePPM the basket's.
	XRefPPM, TradablePPM, NonTradablePPM, PricePPM int64
	// PiLocalBPS is the period's local inflation, SupplyGrowthBPS the supply's growth since the period before.
	PiLocalBPS, SupplyGrowthBPS int64
	// SupplyUnits is the units in existence; MSUP the money in circulation and YSUP the output, in SUP.
	SupplyUnits, MSUP, YSUP int64
	// CoverageBPS is the pot over the market value of the supply; CoverageKnown false with no supply.
	CoverageBPS   int64
	CoverageKnown bool
	At            time.Time
}

// InterventionLine is one request of the head, public.
type InterventionLine struct {
	ID           string
	Side         string
	Units, Price int64
	Status       string
	// SUPUsed is the SUP the pot set aside; Refusal why it was refused.
	SUPUsed      int64
	Refusal      string
	PostedAt     time.Time
	ExecuteAfter time.Time
}

// WithdrawalLine is one announced withdrawal of excess, public.
type WithdrawalLine struct {
	ID           string
	SUP          int64
	Status       string
	Refusal      string
	RequestedAt  time.Time
	ExecuteAfter time.Time
}

// ReserveView is the reserve screen.
type ReserveView struct {
	Village string
	// Name and Symbol are the money's authored name and symbol.
	Name, Symbol string
	Stage        string
	Action       string
	// Status is chartered, wind_down or retired.
	Status string
	// R0 and XRefPPM are the live rate (units per SUP = R0 x 1000000 / XRefPPM).
	R0, XRefPPM int64
	// PotSUP is the reserve pot, Basis the backing basis (the SUP value of the units outstanding when they were
	// issued), Excess the pot above it (the only part the head may withdraw), Supply the units in existence,
	// Stabilisation the units the head's purchases hold, MarketCapSUP the supply's worth at the reference
	// rate, CoverageBPS the pot over it.
	PotSUP, Basis, Excess, Supply, Stabilisation, MarketCapSUP int64
	CoverageBPS                                                int64
	CoverageKnown                                              bool
	// Minted, Burnt, Deposited and Released are the lifetime counters; InterventionOut and InterventionIn the SUP
	// that left the pot for the book and came back.
	Minted, Burnt, Deposited, Released, InterventionOut, InterventionIn int64
	// The Reserve Bank's levers in force.
	MintFeeBPS, ReserveFeeBPS, MaxMoveBPS, WithdrawNoticeHours int64
	// The intervention's limits: CapBPS of the pot a period may take, FloorBPS of the basis the pot may not
	// fall below, DelayHours before a request executes; BuyBudgetSUP and SellBudgetUnits what is allowed now.
	CapBPS, FloorBPS, DelayHours, BuyBudgetSUP, SellBudgetUnits int64
	// TreasurySUP and TreasuryUnits are what the treasury holds; CashSUP what the viewer holds, MyUnits their units.
	TreasurySUP, TreasuryUnits, CashSUP, MyUnits int64
	PendingWithdrawals                           int64
	Interventions                                []InterventionLine
	Withdrawals                                  []WithdrawalLine
	Macro                                        *MoneyMacro
	Trend                                        []MoneyMacro
	// WindDownAt and WindDownEndsAt are the wind-down's start and the end of the claim window.
	WindDownAt, WindDownEndsAt *time.Time
	// MyShare is what the viewer's units would claim now; CanClaim whether they may.
	MyShare  int64
	CanClaim bool
	// CanIssue and CanPolicy are the viewer's charter permissions currency.issue and bank.policy.
	CanIssue, CanPolicy bool
	// Presets are the SUP amounts the screen offers.
	Presets []int64
	// ask and done: the act in question and its figures. Amount is SUP or units by Action; Price micro-SUP per
	// unit; Out what comes of it (units minted, SUP at most set aside, the excess left after, the share
	// claimed).
	Amount, Price, Out int64
	// ExecuteAfter is when a request executes.
	ExecuteAfter *time.Time
	// Reason says why a figure is absent or an act cannot be confirmed (no_excess, budget, funds).
	Reason string
}

var screenCurrencyReserve = presentation.Define[ReserveView](ScreenCurrencyReserve, "village")

// CurrencyReserve is the reserve screen.
func CurrencyReserve(c presentation.Ctx, v ReserveView) *presentation.Response {
	switch v.Stage {
	case ReserveAsk:
		a := []presentation.Action{back(AddrCurrencyReserve)}
		if v.Reason == "" {
			a = append([]presentation.Action{confirm(AddrCurrencyReserve, v.Action, strconv.FormatInt(v.Amount, 10), strconv.FormatInt(v.Price, 10), "", ResidenceConfirm)}, a...)
		}
		return screenCurrencyReserve.Response(c.Lang, v, a...)
	case ReserveDone:
		return screenCurrencyReserve.Response(c.Lang, v, back(AddrMoney), refresh(AddrCurrencyReserve))
	}
	a := []presentation.Action{back(AddrMoney), refresh(AddrCurrencyReserve)}
	num := func(n int64) string { return strconv.FormatInt(n, 10) }
	if v.Status == "chartered" {
		if v.CanIssue {
			for _, n := range v.Presets {
				a = append(a, act(AddrCurrencyReserve, ReserveActionIssue, num(n)).Named("reserve.issue"))
			}
			a = append(a, act(AddrCurrencyReserve, ReserveActionRetire, "1").Named("reserve.retire"))
			if v.TreasuryUnits > 0 {
				a = append(a, act(AddrCurrencyReserve, ReserveActionBurn, num(v.TreasuryUnits/10+1)).Named("reserve.burn"))
			}
		}
		if v.CanPolicy {
			for _, n := range v.Presets {
				a = append(a, act(AddrCurrencyReserve, ReserveActionBuy, num(n*v.R0), num(refPriceOf(v))).Named("reserve.buy"))
			}
			if v.TreasuryUnits > 0 {
				a = append(a, act(AddrCurrencyReserve, ReserveActionSell, num(v.TreasuryUnits/10+1), num(refPriceOf(v))).Named("reserve.sell"))
			}
			if v.Excess-v.PendingWithdrawals > 0 {
				a = append(a, act(AddrCurrencyReserve, ReserveActionWithdraw, num(v.Excess-v.PendingWithdrawals)).Named("reserve.withdraw"))
			}
			for _, i := range v.Interventions {
				if i.Status == "pending" {
					a = append(a, act(AddrCurrencyReserve, ReserveActionCancel, "", "", i.ID).Named("reserve.cancel"))
				}
			}
			for _, w := range v.Withdrawals {
				if w.Status == "pending" {
					a = append(a, act(AddrCurrencyReserve, ReserveActionCancel, "", "", w.ID).Named("reserve.cancel"))
				}
			}
		}
	}
	if v.CanClaim {
		a = append(a, act(AddrCurrencyReserve, ReserveActionClaim, num(v.MyUnits)).Named("reserve.claim"))
	}
	return screenCurrencyReserve.Response(c.Lang, v, a...)
}

// refPriceOf is the price (micro-SUP per unit) the reference rate stands for.
func refPriceOf(v ReserveView) int64 {
	if v.R0 <= 0 || v.XRefPPM <= 0 {
		return 0
	}
	return max((v.XRefPPM+v.R0/2)/v.R0, 1)
}

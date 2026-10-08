package economy

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The floating VC/SUP book of a settlement's own money (docs/adr/0033 sections 6.8 to 6.10; roadmap 2.19
// phase 3). A price is MICRO-SUP per unit of the money (PriceScale per SUP): a client shows
// price / PriceScale SUP, or its reciprocal as units per SUP. Quantities are units, SUP amounts SUP minor
// units. A BUY order buys units with SUP, a SELL order sells units for SUP; the offered side is set aside
// when the order is placed.

// The book's screens and addresses.
const (
	ScreenFXBook     = "fx_book"
	ScreenFXOrder    = "fx_order"
	ScreenFXHistory  = "fx_history"
	ScreenFXConvert  = "fx_convert"
	ScreenFXRefusal  = "fx_refusal"
	AddrFXBook       = "fx:book"
	AddrFXPlace      = "fx:place"
	AddrFXCancel     = "fx:cancel"
	AddrFXHistory    = "fx:history"
	AddrFXConvert    = "fx:convert"
	FXPriceScale     = 1_000_000
	FXOrderAsk       = "ask"
	FXOrderDone      = "done"
	FXConvertMenu    = "menu"
	FXConvertAsk     = "ask"
	FXConvertDone    = "done"
	FXConfirm        = "confirm"
	FXSideBuy        = "buy"
	FXSideSell       = "sell"
	FXStatusOpen     = "open"
	FXStatusFilled   = "filled"
	FXStatusCancel   = "cancelled"
	FXStatusExpired  = "expired"
	FXRefusalNoMkt   = "no_market"
	FXRefusalInvalid = "invalid"
	FXRefusalBand    = "out_of_band"
	FXRefusalSmall   = "too_small"
	FXRefusalFunds   = "funds"
	FXRefusalMany    = "too_many"
	FXRefusalNotYour = "not_yours"
	FXRefusalGone    = "not_found"
	FXRefusalMoved   = "moved"
	FXRefusalLiquid  = "no_liquidity"
	FXRefusalOff     = "off"
)

// FXRefusalCode is the refusal code of a kind.
func FXRefusalCode(kind string) string { return "fx_" + kind }

// FXMoney is the money a book trades: the settlement it belongs to and the money's authored name.
type FXMoney struct {
	Settlement string
	Village    string
	Code       string
	Name       string
	Symbol     string
}

// FXLevel is one price level of the depth.
type FXLevel struct {
	Price  int64
	Units  int64
	Orders int64
}

// FXOrderLine is one order of the viewer.
type FXOrderLine struct {
	ID         string
	No         int64
	Side       string
	Units      int64
	Filled     int64
	Price      int64
	EscrowLeft int64
	Status     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// FXTradeLine is one fill.
type FXTradeLine struct {
	Price int64
	Units int64
	At    time.Time
}

// FXBookView is the book screen: the reference rate, the depth, the viewer's balances and open orders and
// the latest fills.
type FXBookView struct {
	FXMoney
	// R0 is units per SUP at charter; XRefPPM the reference rate (1000000 is 1.00); RefPrice the price it
	// stands for (micro-SUP per unit); LastPrice the latest fill's price, 0 before the first.
	R0, XRefPPM, RefPrice, LastPrice int64
	// BandLow and BandHigh are the prices an order may be placed at (the circuit breaker).
	BandLow, BandHigh int64
	// Bids are the open buy orders, highest price first; Asks the open sell orders, lowest first.
	Bids, Asks []FXLevel
	// ReserveFeeBPS is the fee on selling SUP (paid to Support's treasury), VillageFeeBPS the fee on selling
	// units (paid to the village treasury), MaxMoveBPS the circuit breaker, MinOrderSUP the least an order
	// must be worth.
	ReserveFeeBPS, VillageFeeBPS, MaxMoveBPS, MinOrderSUP int64
	// CashSUP and Units are the viewer's balances; EscrowSUP and EscrowUnits what their open orders hold.
	CashSUP, Units, EscrowSUP, EscrowUnits int64
	MyOrders                               []FXOrderLine
	Trades                                 []FXTradeLine
	// PresetUnits are quantities the screen offers.
	PresetUnits []int64
	// MinTrades is how many fills the reference needs before it moves; WindowPeriods how many periods it averages.
	MinTrades, WindowPeriods int64
	// Notice is the outcome of what the viewer just did: "cancelled".
	Notice string
}

// FXOrderView is the order screen: the order about to be placed (ask) and the order placed (done).
type FXOrderView struct {
	FXMoney
	Stage string
	Side  string
	Units int64
	Price int64
	// R0 and XRefPPM are the live reference; BandLow and BandHigh the prices allowed.
	R0, XRefPPM, BandLow, BandHigh int64
	// WorthSUP is units x price in SUP (rounded down); Escrow what the order sets aside: SUP (with the fee) for a
	// buy, units for a sell; FeeBPS the fee rate it carries (on SUP for a buy, on units for a sell).
	WorthSUP, Escrow, FeeBPS int64
	// Crosses says the best opposite order is at or inside the price, so the order fills at once at least in part.
	Crosses bool
	// CanPlace is false when the viewer cannot set the escrow aside.
	CanPlace bool
	// done: the order as it now stands, what it moved, and the balances after.
	Order                FXOrderLine
	Rested               bool
	UnitsMoved, SUPMoved int64
	CashSUP, UnitsHeld   int64
}

// FXPeriodLine is one period's reading.
type FXPeriodLine struct {
	PeriodNo     int64
	Trades       int64
	VolumeUnits  int64
	VWAP         int64
	ValuePPM     int64
	WindowTrades int64
	XRefBefore   int64
	XRefAfter    int64
	At           time.Time
}

// FXHistoryView is the reference rate's history, newest period first.
type FXHistoryView struct {
	FXMoney
	R0, XRefPPM, MinTrades, WindowPeriods int64
	// PeriodSeconds is how long a period lasts.
	PeriodSeconds int64
	Periods       []FXPeriodLine
}

// FXLeg is one leg of a conversion: a sale of units for SUP or a purchase of units with SUP, on one book.
type FXLeg struct {
	FXMoney
	Side string
	// UnitsIn/SUPIn are given, UnitsOut/SUPOut received; Fee is in the currency sold (SUP for a purchase,
	// units for a sale); Complete says the book can fill the leg.
	UnitsIn, SUPIn, UnitsOut, SUPOut, Fee int64
	Complete                              bool
}

// FXConvertView is the conversion screen: from a money to another through SUP, as one confirm with price
// protection. From and To are currency codes or "SUP".
type FXConvertView struct {
	Stage string
	From  string
	To    string
	// Amount is what is given, in the units of From; Out what the book would give now in To (Out is 0 when it
	// cannot fill the conversion); MinOut the least the confirm accepts (the quote less the slippage).
	Amount, Out, MinOut, SlippageBPS int64
	Legs                             []FXLeg
	Complete                         bool
	// Holdings: SUP and each money the viewer holds, for the menu.
	CashSUP  int64
	Holdings []FXHolding
	// done: what was given and received.
	Gave, Got int64
}

// FXHolding is a balance of a money the viewer holds.
type FXHolding struct {
	FXMoney
	Units int64
}

// FXRefusalView is a refused request.
type FXRefusalView struct {
	Kind string
	// Min and Max are the bounds a refusal quotes (the band, the least worth); Back is where the way back leads.
	Min, Max int64
	Back     presentation.Ref
}

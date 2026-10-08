package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/fx"
	"github.com/mrjvadi/torncity/internal/domain/market"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The floating VC/SUP order book of a settlement's own money (roadmap 2.19 phase 3, docs/adr/0033 sections
// 6.6 to 6.9, docs/adr/0029 section 4). One book per chartered currency, on the shared matcher
// (internal/domain/market) with the asset key {currency, "<CODE>/SUP"}; orders of players and settlements
// (the treasury, for the head's intervention of a later phase); limit orders with partial fills and
// cancel, and market orders that never rest (the building block of the two-leg conversion).
//
// A BUY order buys the settlement's units with SUP (it sells SUP); a SELL order sells units for SUP. The
// offered side is escrowed when the order is placed and released when it ends:
//
//	buy   SUP  owner's SUP account  -> fx_escrow (SUP)       the most its fills and their fee can come to
//	sell  VC   owner's holding      -> fx_escrow (VC)        its quantity
//
// Ledger (each one transaction of one currency, ADR 0009 section 2):
//
//	fx_escrow     owner's account   -> fx_escrow   an order is placed
//	fx_release    fx_escrow         -> owner       an order ends (cancelled, expired, filled with change left)
//	fx_trade_sup  buyer's escrow    -> seller + Support's treasury (the SUP fee)
//	fx_trade_vc   seller's escrow   -> buyer + the village treasury (the units fee)

// Ledger reasons of the book.
const (
	ReasonFXEscrow   Reason = "fx_escrow"
	ReasonFXRelease  Reason = "fx_release"
	ReasonFXTradeSUP Reason = "fx_trade_sup"
	ReasonFXTradeVC  Reason = "fx_trade_vc"
	// ReasonInterventionBuy funds the head's purchase of the money on the book from the reserve pot: pot
	// SUP to the treasury's SUP escrow (docs/adr/0033 6.7, 6.11). What comes back (a release, the proceeds of
	// a sale of the intervention stock) returns to the pot under fx_release and fx_trade_sup.
	ReasonInterventionBuy Reason = "intervention_buy"
)

// Purposes of an order: an ordinary one, or the treasury's intervention, which is funded from and pays into
// the reserve pot.
const (
	FXPurposeTrade        = "trade"
	FXPurposeIntervention = "intervention"
)

// Order sides, owner kinds, kinds and statuses.
const (
	FXBuy  = "buy"
	FXSell = "sell"

	FXOwnerPlayer     = "player"
	FXOwnerSettlement = "settlement"

	FXLimit  = "limit"
	FXMarket = "market"

	FXOpen      = "open"
	FXFilled    = "filled"
	FXCancelled = "cancelled"
	FXExpired   = "expired"

	// FXTradeReference is the reference_type of a fill's two transactions; the id is the fx_trades row.
	FXTradeReference = "fx_trades"
	// FXActionType is the game_actions type of a period of the book ending; FXActionReference its reference type.
	FXActionType      = "fx_period"
	FXActionReference = "fx_clock"
	// FXOrderReference is the reference_type of an escrow or a release; the id is the fx_orders row.
	FXOrderReference = "fx_orders"
)

// FX refusals, coded.
var (
	ErrFXNoMarket    = errors.Sentinel(errors.CodeConflict, "application.ErrFXNoMarket", "the settlement has no chartered money to trade")
	ErrFXInvalid     = errors.Sentinel(errors.CodeInvalidInput, "application.ErrFXInvalid", "the order is malformed")
	ErrFXOutOfBand   = errors.Sentinel(errors.CodeConflict, "application.ErrFXOutOfBand", "the price is further from the reference rate than the circuit breaker allows")
	ErrFXTooSmall    = errors.Sentinel(errors.CodeConflict, "application.ErrFXTooSmall", "the order is worth less than the smallest the book takes")
	ErrFXFunds       = errors.Sentinel(errors.CodeConflict, "application.ErrFXFunds", "the owner holds too little to set aside for the order")
	ErrFXTooMany     = errors.Sentinel(errors.CodeConflict, "application.ErrFXTooMany", "the owner has too many open orders")
	ErrFXNotYours    = errors.Sentinel(errors.CodeUnauthorized, "application.ErrFXNotYours", "the order is not the owner's")
	ErrFXNotFound    = errors.Sentinel(errors.CodeNotFound, "application.ErrFXNotFound", "the order does not exist")
	ErrFXMoved       = errors.Sentinel(errors.CodeConflict, "application.ErrFXMoved", "the book moved past the player's limit")
	ErrFXNoLiquidity = errors.Sentinel(errors.CodeConflict, "application.ErrFXNoLiquidity", "the book cannot fill the conversion")
)

// FXOwner is who an order belongs to.
type FXOwner struct{ Kind, ID string }

func (o FXOwner) key() string { return o.Kind + ":" + o.ID }

// FXOrder is one row of fx_orders.
type FXOrder struct {
	ID, SettlementID string
	No               int64
	Owner            FXOwner
	Side, Kind       string
	Quantity, Filled int64
	// Price is micro-SUP per unit; NotionalMicro the cumulative quantity x price of the fills; EscrowLeft what
	// the order still holds (SUP for a buy, units for a sell); FeeBPS the fee rate it carries.
	Price, NotionalMicro, EscrowLeft, FeeBPS int64
	Status                                   string
	// Purpose is FXPurposeTrade or FXPurposeIntervention.
	Purpose              string
	CreatedAt, ExpiresAt time.Time
	ClosedAt             *time.Time
}

// Remaining is the unfilled quantity.
func (o FXOrder) Remaining() int64 { return o.Quantity - o.Filled }

// FXTrade is one row of fx_trades.
type FXTrade struct {
	ID, SettlementID                       string
	BuyOrderID, SellOrderID                string
	Buyer, Seller                          FXOwner
	Quantity, Price, SUP, SUPFee, UnitsFee int64
	SUPTransactionID, VCTransactionID      string
	At                                     time.Time
}

// FXLevel is one price level of the depth.
type FXLevel struct {
	Price, Units int64
	Orders       int64
}

// FXRate is one row of fx_rate_history.
type FXRate struct {
	SettlementID                                  string
	PeriodNo                                      int64
	Trades, VolumeUnits, NotionalMicro            int64
	ValuePPM, WindowTrades, XRefBefore, XRefAfter int64
	At                                            time.Time
}

// FXStats are the fills of a currency in a span of time.
type FXStats struct{ Trades, Units, NotionalMicro int64 }

// FXClock is the one fx_clock row.
type FXClock struct {
	PeriodNo        int64
	PeriodStartedAt time.Time
	NextAt          *time.Time
	ActionID        string
	UpdatedAt       time.Time
}

// FXRepository is the port of the book, reached through Tx.FX.
type FXRepository interface {
	// InsertOrder writes a new order.
	InsertOrder(ctx context.Context, o FXOrder) error
	// SaveOrder writes an order's fills, escrow, status and close time.
	SaveOrder(ctx context.Context, o FXOrder) error
	// OrderByID reads an order, locked when lock is set, or ErrFXNotFound.
	OrderByID(ctx context.Context, id string, lock bool) (*FXOrder, error)
	// OpenOrders lists a currency's open orders of one side, best price first then oldest, at most limit.
	OpenOrders(ctx context.Context, settlementID, side string, limit int) ([]FXOrder, error)
	// OpenCount counts an owner's open orders in a currency.
	OpenCount(ctx context.Context, settlementID string, owner FXOwner) (int, error)
	// OrdersOf lists an owner's orders in a currency, open ones first then newest, at most limit.
	OrdersOf(ctx context.Context, settlementID string, owner FXOwner, limit int) ([]FXOrder, error)
	// ExpiredOpen lists open orders of a currency past their expiry at now, at most limit.
	ExpiredOpen(ctx context.Context, settlementID string, now time.Time, limit int) ([]FXOrder, error)
	// Depth aggregates a currency's open orders of one side by price, best first, at most levels.
	Depth(ctx context.Context, settlementID, side string, levels int) ([]FXLevel, error)
	// InsertTrade writes a fill.
	InsertTrade(ctx context.Context, t FXTrade) error
	// Trades lists a currency's latest fills, newest first.
	Trades(ctx context.Context, settlementID string, limit int) ([]FXTrade, error)
	// Stats sums the fills of a currency in [from, to).
	Stats(ctx context.Context, settlementID string, from, to time.Time) (FXStats, error)

	// Clock returns the book's clock, locked, starting it at period 1.
	Clock(ctx context.Context, now time.Time) (*FXClock, error)
	SaveClock(ctx context.Context, c FXClock) error
	// RecordRate writes a period's reading; false when the currency's period already has one.
	RecordRate(ctx context.Context, r FXRate) (bool, error)
	// Rates lists a currency's readings, newest first, at most limit.
	Rates(ctx context.Context, settlementID string, limit int) ([]FXRate, error)
	// SetXRef sets the reference rate of a chartered currency.
	SetXRef(ctx context.Context, settlementID string, xRefPPM int64) error
}

// FXRules are the book's rules (config currency.fx_*).
type FXRules struct {
	// ReserveFeeBPS is the fee on selling SUP (reserve.fx_fee_bps), paid to Support's treasury; the fee on
	// selling units is the settlement's own (village.fx_fee_bps, village_currency_state.fx_fee_bps).
	ReserveFeeBPS int64
	// MaxMoveBPS is the circuit breaker (reserve.max_move_bps).
	MaxMoveBPS int64
	// MinTrades is how many fills the window needs before x_ref moves; Window the number of periods averaged.
	MinTrades int64
	Window    int
	// OrderTTL is how long a limit order lives; MinOrderSUP the least an order must be worth in SUP;
	// BookLimit how many resting orders of a side one match looks at; MaxOpenOrders an owner's cap.
	OrderTTL time.Duration
	// UnitPresets are the quantities the book screen offers; ConvertSlippageBPS how far a conversion may
	// move between its quote and its confirm before it is refused; Period how long one period of the
	// reference rate lasts.
	UnitPresets        []int64
	ConvertSlippageBPS int64
	Period             time.Duration
	MinOrderSUP        int64
	BookLimit          int
	MaxOpenOrders      int
	// SupportCityID is the city whose treasury takes the fee on selling SUP.
	SupportCityID string
}

// Enabled reports whether the book is configured.
func (r FXRules) Enabled() bool {
	return r.MaxMoveBPS > 0 && r.Window > 0 && r.OrderTTL > 0 && r.BookLimit > 0 && r.SupportCityID != ""
}

// FXPlace asks for one order.
type FXPlace struct {
	SettlementID string
	Owner        FXOwner
	Side         string
	Units, Price int64
	// IOC makes it a market order: it takes what the book offers within Price and never rests; the rest is
	// cancelled and released.
	IOC bool
	At  time.Time
	// Purpose is FXPurposeIntervention for the head's order: the owner is the settlement, the SUP side is
	// the reserve pot. Empty is an ordinary order.
	Purpose string
}

// FXPlaced is what placing an order did.
type FXPlaced struct {
	Order FXOrder
	// Fills are the fills it made as the taker; UnitsMoved the units it sold (sell) or received net of the
	// units fee (buy); SUPMoved the SUP it received (sell, net of nothing: the fee is the buyer's) or
	// paid including the fee (buy).
	Fills      []FXTrade
	UnitsMoved int64
	SUPMoved   int64
	Rested     bool
}

// fxAccounts are the accounts one owner uses on a book.
type fxAccounts struct {
	sup, vc, escSUP, escVC Account
}

func fxOwnerAccounts(ctx context.Context, tx Tx, owner FXOwner, st CurrencyState, purpose string) (fxAccounts, error) {
	var a fxAccounts
	l := tx.Ledger()
	var err error
	switch owner.Kind {
	case FXOwnerPlayer:
		a.sup, err = l.AccountFor(ctx, AccountPlayerCash, owner.ID)
	case FXOwnerSettlement:
		if purpose == FXPurposeIntervention {
			a.sup, err = l.AccountForCurrency(ctx, AccountReservePot, owner.ID, DefaultCurrency)
		} else {
			a.sup, err = l.AccountFor(ctx, AccountCityTreasury, owner.ID)
		}
	default:
		return a, ErrFXInvalid
	}
	if err != nil {
		return a, err
	}
	if a.vc, err = l.AccountForCurrency(ctx, AccountForeignHolding, owner.ID, st.Code); err != nil {
		return a, err
	}
	if a.escSUP, err = l.AccountForCurrency(ctx, AccountFXEscrow, owner.ID, DefaultCurrency); err != nil {
		return a, err
	}
	a.escVC, err = l.AccountForCurrency(ctx, AccountFXEscrow, owner.ID, st.Code)
	return a, err
}

func fxMarketOrder(o FXOrder, code string, kind market.Kind) market.Order {
	side := market.Buy
	if o.Side == FXSell {
		side = market.Sell
	}
	return market.Order{
		ID: o.ID, Side: side, Kind: kind, Asset: fxAsset(code), Quantity: o.Quantity, Filled: o.Filled,
		UnitPrice: money.FromMinor(o.Price), Owner: o.Owner.key(), CreatedAt: o.CreatedAt, ExpiresAt: o.ExpiresAt,
	}
}

func fxAsset(code string) market.AssetKey {
	return market.AssetKey{Type: market.AssetCurrency, ID: fx.Pair(code), Channel: market.ChannelPublic}
}

// releaseFX gives an order's remaining escrow back to its owner and zeroes it.
func releaseFX(ctx context.Context, tx Tx, newID func() string, st CurrencyState, o *FXOrder, at time.Time) error {
	if o.EscrowLeft <= 0 {
		o.EscrowLeft = 0
		return nil
	}
	a, err := fxOwnerAccounts(ctx, tx, o.Owner, st, o.Purpose)
	if err != nil {
		return err
	}
	from, to := a.escSUP, a.sup
	if o.Side == FXSell {
		from, to = a.escVC, a.vc
	} else if o.Purpose == FXPurposeIntervention {
		// the change of an intervention purchase goes back to the pot
		if err := tx.Currency().AddInterventionFlow(ctx, o.SettlementID, 0, o.EscrowLeft); err != nil {
			return err
		}
	}
	if _, err := tx.Ledger().Post(ctx, LedgerTransaction{
		ID: newID(), Reason: ReasonFXRelease, CreatedAt: at, ReferenceType: FXOrderReference, ReferenceID: o.ID,
		Entries: []LedgerEntry{
			{AccountID: from.ID, Amount: money.FromMinor(-o.EscrowLeft)},
			{AccountID: to.ID, Amount: money.FromMinor(o.EscrowLeft)},
		},
	}); err != nil {
		return err
	}
	o.EscrowLeft = 0
	return nil
}

func closeFX(o *FXOrder, status string, at time.Time) {
	o.Status = status
	t := at
	o.ClosedAt = &t
}

// PlaceFX places one order on a currency's book: checks it against the circuit breaker, sets its offered
// side aside, matches it against the resting orders at the resting prices, settles every fill, and rests
// what a limit order did not fill. All in the caller's transaction, under the settlement's lock, so two
// orders never match at once; any error rolls the whole of it back.
func PlaceFX(ctx context.Context, tx Tx, newID func() string, rules FXRules, p FXPlace) (FXPlaced, error) {
	var out FXPlaced
	repo := tx.Currency()
	st, err := repo.State(ctx, p.SettlementID)
	if err != nil {
		return out, err
	}
	if st == nil || (st.Status != CurrencyChartered && !(st.Status == CurrencyWindDown && p.Purpose != FXPurposeIntervention)) {
		return out, ErrFXNoMarket // a money in wind-down keeps its book open, but the head no longer defends it
	}
	if p.Purpose == FXPurposeIntervention && p.Owner.Kind != FXOwnerSettlement {
		return out, ErrFXInvalid
	}
	if p.Units < 1 || p.Price < 1 || (p.Side != FXBuy && p.Side != FXSell) || (p.Owner.Kind != FXOwnerPlayer && p.Owner.Kind != FXOwnerSettlement) || p.Owner.ID == "" {
		return out, ErrFXInvalid
	}
	if !fx.InBand(p.Price, fx.RefPrice(st.XRefPPM, st.R0), rules.MaxMoveBPS) {
		return out, ErrFXOutOfBand
	}
	worth := p.Price
	if p.IOC {
		worth = fx.RefPrice(st.XRefPPM, st.R0) // a market order is worth what the reference says, not its limit
	}
	n, ok := fx.Notional(p.Units, worth)
	if !ok {
		return out, ErrFXInvalid
	}
	if fx.SUPOf(n) < rules.MinOrderSUP {
		return out, ErrFXTooSmall
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(p.SettlementID)); err != nil {
		return out, err
	}
	if !p.IOC && rules.MaxOpenOrders > 0 {
		if c, err := tx.FX().OpenCount(ctx, p.SettlementID, p.Owner); err != nil {
			return out, err
		} else if c >= rules.MaxOpenOrders {
			return out, ErrFXTooMany
		}
	}
	acc, err := fxOwnerAccounts(ctx, tx, p.Owner, *st, p.Purpose)
	if err != nil {
		return out, err
	}
	feeBPS, escrow := rules.ReserveFeeBPS, fx.BuyEscrow(p.Units, p.Price, rules.ReserveFeeBPS)
	from, to := acc.sup, acc.escSUP
	if p.Side == FXSell {
		feeBPS, escrow = st.FXFeeBPS, p.Units
		from, to = acc.vc, acc.escVC
	}
	if escrow <= 0 {
		return out, ErrFXInvalid
	}
	if from.Balance.Minor() < escrow {
		return out, ErrFXFunds
	}
	purpose := p.Purpose
	if purpose == "" {
		purpose = FXPurposeTrade
	}
	o := FXOrder{
		ID: newID(), SettlementID: p.SettlementID, Owner: p.Owner, Side: p.Side, Kind: FXLimit, Purpose: purpose,
		Quantity: p.Units, Price: p.Price, EscrowLeft: escrow, FeeBPS: feeBPS, Status: FXOpen,
		CreatedAt: p.At, ExpiresAt: p.At.Add(rules.OrderTTL),
	}
	kind := market.Limit
	if p.IOC {
		o.Kind, kind = FXMarket, market.Market
	}

	// the book as it stands, before this order
	bids, err := tx.FX().OpenOrders(ctx, p.SettlementID, FXBuy, rules.BookLimit)
	if err != nil {
		return out, err
	}
	asks, err := tx.FX().OpenOrders(ctx, p.SettlementID, FXSell, rules.BookLimit)
	if err != nil {
		return out, err
	}
	byID := map[string]*FXOrder{}
	book := market.NewBook(fxAsset(st.Code))
	for i := range bids {
		byID[bids[i].ID] = &bids[i]
		book.Bids = append(book.Bids, fxMarketOrder(bids[i], st.Code, market.Limit))
	}
	for i := range asks {
		byID[asks[i].ID] = &asks[i]
		book.Asks = append(book.Asks, fxMarketOrder(asks[i], st.Code, market.Limit))
	}

	escrowReason := ReasonFXEscrow
	if purpose == FXPurposeIntervention && p.Side == FXBuy {
		escrowReason = ReasonInterventionBuy
		if err := tx.Currency().AddInterventionFlow(ctx, p.SettlementID, escrow, 0); err != nil {
			return out, err
		}
	}
	if _, err := tx.Ledger().Post(ctx, LedgerTransaction{
		ID: newID(), Reason: escrowReason, CreatedAt: p.At, ReferenceType: FXOrderReference, ReferenceID: o.ID,
		Entries: []LedgerEntry{
			{AccountID: from.ID, Amount: money.FromMinor(-escrow)},
			{AccountID: to.ID, Amount: money.FromMinor(escrow)},
		},
	}); err != nil {
		return out, err
	}
	if err := tx.FX().InsertOrder(ctx, o); err != nil {
		return out, err
	}
	byID[o.ID] = &o

	res, err := market.Match(book, fxMarketOrder(o, st.Code, kind), p.At)
	if err != nil {
		return out, errors.Internal(err)
	}

	dirty := map[string]bool{o.ID: true}
	support, err := tx.Ledger().AccountFor(ctx, AccountCityTreasury, rules.SupportCityID)
	if err != nil {
		return out, err
	}
	village, err := tx.Ledger().AccountForCurrency(ctx, AccountForeignHolding, p.SettlementID, st.Code)
	if err != nil {
		return out, err
	}
	for _, t := range res.Trades {
		buy, sell := byID[t.BuyOrderID], byID[t.SellOrderID]
		if buy == nil || sell == nil {
			return out, errors.Internal(ErrFXInvalid)
		}
		price := t.UnitPrice.Minor()
		f, ok := fx.FillOf(t.Quantity, price, buy.NotionalMicro, buy.FeeBPS, sell.Filled, sell.FeeBPS)
		if !ok {
			return out, ErrFXInvalid
		}
		ba, err := fxOwnerAccounts(ctx, tx, buy.Owner, *st, buy.Purpose)
		if err != nil {
			return out, err
		}
		sa, err := fxOwnerAccounts(ctx, tx, sell.Owner, *st, sell.Purpose)
		if err != nil {
			return out, err
		}
		if sell.Purpose == FXPurposeIntervention && f.SUP > 0 {
			// the proceeds of selling the intervention stock go to the pot
			if err := tx.Currency().AddInterventionFlow(ctx, p.SettlementID, 0, f.SUP); err != nil {
				return out, err
			}
		}
		tr := FXTrade{
			ID: newID(), SettlementID: p.SettlementID, BuyOrderID: buy.ID, SellOrderID: sell.ID, Buyer: buy.Owner, Seller: sell.Owner,
			Quantity: t.Quantity, Price: price, SUP: f.SUP, SUPFee: f.SUPFee, UnitsFee: f.UnitsFee, At: p.At,
		}
		// the SUP side: the buyer's escrow pays the seller, and Support's treasury its fee
		if f.SUP+f.SUPFee > 0 {
			tr.SUPTransactionID = newID()
			entries := []LedgerEntry{{AccountID: ba.escSUP.ID, Amount: money.FromMinor(-(f.SUP + f.SUPFee))}}
			if f.SUP > 0 {
				entries = append(entries, LedgerEntry{AccountID: sa.sup.ID, Amount: money.FromMinor(f.SUP)})
			}
			if f.SUPFee > 0 {
				entries = append(entries, LedgerEntry{AccountID: support.ID, Amount: money.FromMinor(f.SUPFee)})
			}
			if _, err := tx.Ledger().Post(ctx, LedgerTransaction{ID: tr.SUPTransactionID, Reason: ReasonFXTradeSUP, CreatedAt: p.At,
				ReferenceType: FXTradeReference, ReferenceID: tr.ID, Entries: entries}); err != nil {
				return out, err
			}
		}
		// the unit side: the seller's escrow pays the buyer, and the village treasury its fee
		tr.VCTransactionID = newID()
		entries := []LedgerEntry{{AccountID: sa.escVC.ID, Amount: money.FromMinor(-f.Units)}}
		if net := f.Units - f.UnitsFee; net > 0 {
			entries = append(entries, LedgerEntry{AccountID: ba.vc.ID, Amount: money.FromMinor(net)})
		}
		if f.UnitsFee > 0 {
			entries = append(entries, LedgerEntry{AccountID: village.ID, Amount: money.FromMinor(f.UnitsFee)})
		}
		if len(entries) < 2 {
			return out, errors.Internal(ErrFXInvalid)
		}
		if _, err := tx.Ledger().Post(ctx, LedgerTransaction{ID: tr.VCTransactionID, Reason: ReasonFXTradeVC, CreatedAt: p.At,
			ReferenceType: FXTradeReference, ReferenceID: tr.ID, Entries: entries}); err != nil {
			return out, err
		}
		if err := tx.FX().InsertTrade(ctx, tr); err != nil {
			return out, err
		}
		buy.Filled += t.Quantity
		buy.NotionalMicro += f.Notional
		buy.EscrowLeft -= f.SUP + f.SUPFee
		sell.Filled += t.Quantity
		sell.EscrowLeft -= t.Quantity
		if buy.EscrowLeft < 0 || sell.EscrowLeft < 0 {
			return out, errors.Internal(ErrFXInvalid)
		}
		dirty[buy.ID], dirty[sell.ID] = true, true
		if t.BuyOrderID == o.ID || t.SellOrderID == o.ID {
			out.Fills = append(out.Fills, tr)
			if p.Side == FXBuy {
				out.UnitsMoved += f.Units - f.UnitsFee
				out.SUPMoved += f.SUP + f.SUPFee
			} else {
				out.UnitsMoved += f.Units
				out.SUPMoved += f.SUP
			}
		}
	}

	// resting orders that left the book without trading the rest of their size
	for _, rm := range res.Removed {
		ro := byID[rm.Order.ID]
		if ro == nil {
			continue
		}
		switch rm.Reason {
		case market.RemovedExpired:
			closeFX(ro, FXExpired, p.At)
		case market.RemovedSelfTrade:
			closeFX(ro, FXCancelled, p.At)
		default:
			closeFX(ro, FXFilled, p.At)
		}
		if err := releaseFX(ctx, tx, newID, *st, ro, p.At); err != nil {
			return out, err
		}
		dirty[ro.ID] = true
	}
	// the incoming order: filled, resting, or (market) the unfilled rest cancelled
	switch {
	case o.Remaining() == 0:
		closeFX(&o, FXFilled, p.At)
		if err := releaseFX(ctx, tx, newID, *st, &o, p.At); err != nil {
			return out, err
		}
	case res.Rests:
		out.Rested = true
	default:
		closeFX(&o, FXCancelled, p.At)
		if err := releaseFX(ctx, tx, newID, *st, &o, p.At); err != nil {
			return out, err
		}
	}
	for id := range dirty {
		if err := tx.FX().SaveOrder(ctx, *byID[id]); err != nil {
			return out, err
		}
	}
	out.Order = o
	return out, nil
}

// CancelFX ends an open order of the owner and gives its escrow back; an order that is already over is
// left as it is (a repeat does nothing). It returns the order as it now stands.
func CancelFX(ctx context.Context, tx Tx, newID func() string, owner FXOwner, orderID string, at time.Time) (*FXOrder, error) {
	probe, err := tx.FX().OrderByID(ctx, orderID, false)
	if err != nil {
		return nil, err
	}
	if probe.Owner != owner {
		return nil, ErrFXNotYours
	}
	st, err := tx.Currency().State(ctx, probe.SettlementID)
	if err != nil || st == nil {
		return nil, ErrFXNoMarket
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(probe.SettlementID)); err != nil {
		return nil, err
	}
	o, err := tx.FX().OrderByID(ctx, orderID, true)
	if err != nil {
		return nil, err
	}
	if o.Status != FXOpen {
		return o, nil
	}
	closeFX(o, FXCancelled, at)
	if err := releaseFX(ctx, tx, newID, *st, o, at); err != nil {
		return nil, err
	}
	return o, tx.FX().SaveOrder(ctx, *o)
}

// ExpireFX ends the open orders of a currency that are past their expiry and gives their escrow back.
func ExpireFX(ctx context.Context, tx Tx, newID func() string, settlementID string, now time.Time) (int, error) {
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil {
		return 0, err
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(settlementID)); err != nil {
		return 0, err
	}
	list, err := tx.FX().ExpiredOpen(ctx, settlementID, now, 500)
	if err != nil {
		return 0, err
	}
	for i := range list {
		closeFX(&list[i], FXExpired, now)
		if err := releaseFX(ctx, tx, newID, *st, &list[i], now); err != nil {
			return 0, err
		}
		if err := tx.FX().SaveOrder(ctx, list[i]); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}

// SettleFXRate closes one period of one currency's book: it reads the period's fills, appends the reading
// to the append-only history and, once the window holds enough fills, moves x_ref to the time-weighted
// average. It is idempotent: a currency's period has one reading (the history's primary key), and a repeat
// changes nothing. It returns whether it wrote the reading and the reference after it.
func SettleFXRate(ctx context.Context, tx Tx, rules FXRules, settlementID string, periodNo int64, from, to, now time.Time) (bool, int64, error) {
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil || st.Status != CurrencyChartered {
		return false, 0, err
	}
	hist, err := tx.FX().Rates(ctx, settlementID, rules.Window)
	if err != nil {
		return false, 0, err
	}
	for _, h := range hist {
		if h.PeriodNo >= periodNo {
			return false, st.XRefPPM, nil // this period (or a later one) already has its reading
		}
	}
	stats, err := tx.FX().Stats(ctx, settlementID, from, to)
	if err != nil {
		return false, 0, err
	}
	value := st.XRefPPM
	if len(hist) > 0 {
		value = hist[0].ValuePPM
	}
	if stats.Trades > 0 && stats.Units > 0 {
		value = fx.XFromPrice(fx.VWAPPrice(stats.Units, stats.NotionalMicro), st.R0)
	}
	// the window: the readings of the last periods, oldest first, this one last
	values := make([]int64, 0, rules.Window)
	trades := stats.Trades
	for i := len(hist) - 1; i >= 0; i-- {
		values = append(values, hist[i].ValuePPM)
		trades += hist[i].Trades
	}
	values = append(values, value)
	pad := st.XRefPPM
	if len(hist) > 0 {
		pad = hist[len(hist)-1].XRefBefore
	}
	next, moved := fx.NextRef(st.XRefPPM, values, pad, trades, rules.MinTrades, rules.Window)
	fresh, err := tx.FX().RecordRate(ctx, FXRate{
		SettlementID: settlementID, PeriodNo: periodNo, Trades: stats.Trades, VolumeUnits: stats.Units, NotionalMicro: stats.NotionalMicro,
		ValuePPM: value, WindowTrades: trades, XRefBefore: st.XRefPPM, XRefAfter: next, At: now,
	})
	if err != nil || !fresh {
		return false, st.XRefPPM, err
	}
	if moved {
		if err := tx.FX().SetXRef(ctx, settlementID, next); err != nil {
			return false, 0, err
		}
	}
	return true, next, nil
}

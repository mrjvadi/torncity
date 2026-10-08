package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/fx"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
)

// The floating VC/SUP book (docs/adr/0033 6.8 to 6.10; roadmap 2.19 phase 3): the book screen with its depth
// and the viewer's orders, placing and cancelling a limit order, the reference rate's history, and a
// conversion from one money to another through SUP as one confirm with price protection. The matching, the
// escrow and the fees are application.PlaceFX; the period that moves x_ref is Settle, from the scheduler.

// FXHandler serves the commands of the book.
type FXHandler struct {
	uow            application.UnitOfWork
	ids            IDGenerator
	cities         application.CityRepository
	rules          application.FXRules
	supportCode    string
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewFXHandler wires the book. supportCode is the code of the city whose treasury takes the fee on selling SUP.
func NewFXHandler(uow application.UnitOfWork, ids IDGenerator, cities application.CityRepository, rules application.FXRules,
	supportCode string, idempotencyTTL time.Duration, now func() time.Time,
) *FXHandler {
	if now == nil {
		now = time.Now
	}
	return &FXHandler{uow: uow, ids: ids, cities: cities, rules: rules, supportCode: supportCode, idempotencyTTL: idempotencyTTL, now: now}
}

// FXBookRequest is the payload of fx.book and fx.history.
type FXBookRequest struct {
	// Settlement names the money's settlement; empty means the viewer's own.
	Settlement string `json:"settlement,omitempty"`
}

// FXPlaceRequest is the payload of fx.place. Price is micro-SUP per unit, or SUP per unit with a decimal point.
type FXPlaceRequest struct {
	Settlement string `json:"settlement,omitempty"`
	Side       string `json:"side,omitempty"`
	Units      string `json:"units,omitempty"`
	Price      string `json:"price,omitempty"`
	Confirm    string `json:"confirm,omitempty"`
}

// FXCancelRequest is the payload of fx.cancel.
type FXCancelRequest struct {
	Order      string `json:"order"`
	Settlement string `json:"settlement,omitempty"`
}

// FXConvertRequest is the payload of fx.convert: From and To are currency codes or SUP; Amount is in the units
// of From; Quote is the output that was shown.
type FXConvertRequest struct {
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Amount  string `json:"amount,omitempty"`
	Quote   string `json:"quote,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

// fxRefused carries a refusal out of a unit of work.
type fxRefused struct{ view economy.FXRefusalView }

func (r *fxRefused) Error() string { return "handlers: the book refused: " + r.view.Kind }

func refuseFX(kind string) *fxRefused { return &fxRefused{view: economy.FXRefusalView{Kind: kind}} }

// fxRefusalOf maps the application's refusals to the screen's.
func (h *FXHandler) fxRefusalOf(err error) (*fxRefused, bool) {
	var r *fxRefused
	if stderrors.As(err, &r) {
		return r, true
	}
	pick := func(kind string) (*fxRefused, bool) { return refuseFX(kind), true }
	switch {
	case stderrors.Is(err, application.ErrFXNoMarket):
		return pick(economy.FXRefusalNoMkt)
	case stderrors.Is(err, application.ErrFXInvalid):
		return pick(economy.FXRefusalInvalid)
	case stderrors.Is(err, application.ErrFXOutOfBand):
		return pick(economy.FXRefusalBand)
	case stderrors.Is(err, application.ErrFXTooSmall):
		r := refuseFX(economy.FXRefusalSmall)
		r.view.Min = h.rules.MinOrderSUP
		return r, true
	case stderrors.Is(err, application.ErrFXFunds):
		return pick(economy.FXRefusalFunds)
	case stderrors.Is(err, application.ErrFXTooMany):
		return pick(economy.FXRefusalMany)
	case stderrors.Is(err, application.ErrFXNotYours):
		return pick(economy.FXRefusalNotYour)
	case stderrors.Is(err, application.ErrFXNotFound):
		return pick(economy.FXRefusalGone)
	case stderrors.Is(err, application.ErrFXMoved):
		return pick(economy.FXRefusalMoved)
	case stderrors.Is(err, application.ErrFXNoLiquidity):
		return pick(economy.FXRefusalLiquid)
	}
	return nil, false
}

func (h *FXHandler) finish(lang string, err error) (*presentation.Response, error) {
	if r, ok := h.fxRefusalOf(err); ok {
		return economy.FXRefusal(presentation.Ctx{Lang: lang}, r.view), nil
	}
	return nil, err
}

// player reads the acting player and the language they read.
func (h *FXHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

// settlementOf finds the settlement whose money a request is about: the one it names, else the viewer's
// own home, else the one founded by the group the command came from.
func (h *FXHandler) settlementOf(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, named string) (application.FoundedSettlement, error) {
	if named = strings.TrimSpace(named); named != "" {
		return tx.Settlements().ByID(ctx, named)
	}
	if home, err := tx.Employment().ResidenceCityID(ctx, p.ID); err != nil {
		return application.FoundedSettlement{}, err
	} else if home != "" {
		return tx.Settlements().ByID(ctx, home)
	}
	if meta.InGroup() {
		return tx.Settlements().ByFoundingGroup(ctx, meta.TelegramChatID)
	}
	return application.FoundedSettlement{}, refuseFX(economy.FXRefusalNoMkt)
}

// rulesNow is the rules with Support's treasury resolved.
func (h *FXHandler) rulesNow(ctx context.Context) (application.FXRules, error) {
	r := h.rules
	if r.SupportCityID == "" {
		c, err := h.cities.ByCode(ctx, h.supportCode)
		if err != nil {
			return r, err
		}
		r.SupportCityID = c.ID
	}
	return r, nil
}

func (h *FXHandler) reserve(ctx context.Context, tx application.Tx, p *application.Player, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

func (h *FXHandler) money(ctx context.Context, tx application.Tx, s application.FoundedSettlement, st application.CurrencyState) (economy.FXMoney, error) {
	m := economy.FXMoney{Settlement: s.CityID, Village: s.Name, Code: st.Code, Name: st.Code, Symbol: st.Code}
	names, err := tx.Currency().Names(ctx, []string{s.CityID})
	if err != nil {
		return m, err
	}
	if n, ok := names[s.CityID]; ok {
		if n.Name != "" {
			m.Name = n.Name
		}
		if n.Symbol != "" {
			m.Symbol = n.Symbol
		}
	}
	return m, nil
}

func orderLine(o application.FXOrder) economy.FXOrderLine {
	return economy.FXOrderLine{ID: o.ID, No: o.No, Side: o.Side, Units: o.Quantity, Filled: o.Filled, Price: o.Price, EscrowLeft: o.EscrowLeft,
		Status: o.Status, CreatedAt: o.CreatedAt, ExpiresAt: o.ExpiresAt}
}

// bookView reads the whole book screen for a player.
func (h *FXHandler) bookView(ctx context.Context, tx application.Tx, p *application.Player, s application.FoundedSettlement, notice string) (economy.FXBookView, error) {
	var v economy.FXBookView
	st, err := tx.Currency().State(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	if st == nil || st.Status != application.CurrencyChartered {
		return v, refuseFX(economy.FXRefusalNoMkt)
	}
	if v.FXMoney, err = h.money(ctx, tx, s, *st); err != nil {
		return v, err
	}
	ref := fx.RefPrice(st.XRefPPM, st.R0)
	v.R0, v.XRefPPM, v.RefPrice = st.R0, st.XRefPPM, ref
	v.BandLow, v.BandHigh = fx.Band(ref, h.rules.MaxMoveBPS)
	v.ReserveFeeBPS, v.VillageFeeBPS, v.MaxMoveBPS, v.MinOrderSUP = h.rules.ReserveFeeBPS, st.FXFeeBPS, h.rules.MaxMoveBPS, h.rules.MinOrderSUP
	v.MinTrades, v.WindowPeriods, v.PresetUnits, v.Notice = h.rules.MinTrades, int64(h.rules.Window), h.rules.UnitPresets, notice
	const levels = 10
	bids, err := tx.FX().Depth(ctx, s.CityID, application.FXBuy, levels)
	if err != nil {
		return v, err
	}
	asks, err := tx.FX().Depth(ctx, s.CityID, application.FXSell, levels)
	if err != nil {
		return v, err
	}
	for _, l := range bids {
		v.Bids = append(v.Bids, economy.FXLevel{Price: l.Price, Units: l.Units, Orders: l.Orders})
	}
	for _, l := range asks {
		v.Asks = append(v.Asks, economy.FXLevel{Price: l.Price, Units: l.Units, Orders: l.Orders})
	}
	trades, err := tx.FX().Trades(ctx, s.CityID, levels)
	if err != nil {
		return v, err
	}
	for _, t := range trades {
		v.Trades = append(v.Trades, economy.FXTradeLine{Price: t.Price, Units: t.Quantity, At: t.At})
	}
	if len(trades) > 0 {
		v.LastPrice = trades[0].Price
	}
	owner := application.FXOwner{Kind: application.FXOwnerPlayer, ID: p.ID}
	mine, err := tx.FX().OrdersOf(ctx, s.CityID, owner, 20)
	if err != nil {
		return v, err
	}
	for _, o := range mine {
		v.MyOrders = append(v.MyOrders, orderLine(o))
	}
	l := tx.Ledger()
	cash, err := l.AccountFor(ctx, application.AccountPlayerCash, p.ID)
	if err != nil {
		return v, err
	}
	hold, err := l.AccountForCurrency(ctx, application.AccountForeignHolding, p.ID, st.Code)
	if err != nil {
		return v, err
	}
	v.CashSUP, v.Units = cash.Balance.Minor(), hold.Balance.Minor()
	for _, o := range mine {
		if o.Status != application.FXOpen {
			continue
		}
		if o.Side == application.FXBuy {
			v.EscrowSUP += o.EscrowLeft
		} else {
			v.EscrowUnits += o.EscrowLeft
		}
	}
	return v, nil
}

// Book handles fx.book.
func (h *FXHandler) Book(ctx context.Context, meta envelope.Metadata, req FXBookRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	lang := meta.Language
	var view economy.FXBookView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		s, err := h.settlementOf(ctx, tx, meta, p, req.Settlement)
		if err != nil {
			return err
		}
		view, err = h.bookView(ctx, tx, p, s, "")
		return err
	})
	if err != nil {
		return h.finish(lang, err)
	}
	return economy.FXBook(presentation.Ctx{Lang: lang}, view).MarkPrivate(), nil
}

// Place handles fx.place: without confirm the order as it would be placed (what it sets aside, what it
// would fill at once), with confirm the order itself.
func (h *FXHandler) Place(ctx context.Context, meta envelope.Metadata, req FXPlaceRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	if r, err := h.rulesNow(ctx); err != nil {
		return nil, err
	} else if !r.Enabled() {
		return nil, errors.Internal(stderrors.New("handlers: the book is not configured"))
	}
	lang := meta.Language
	var view economy.FXOrderView
	var book *economy.FXBookView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		s, err := h.settlementOf(ctx, tx, meta, p, req.Settlement)
		if err != nil {
			return err
		}
		side := strings.ToLower(strings.TrimSpace(req.Side))
		if side == "" || strings.TrimSpace(req.Units) == "" {
			bv, err := h.bookView(ctx, tx, p, s, "")
			book = &bv
			return err
		}
		units, perr := strconv.ParseInt(strings.TrimSpace(req.Units), 10, 64)
		price, ok := fx.ParsePrice(req.Price)
		if perr != nil || units < 1 || !ok || (side != application.FXBuy && side != application.FXSell) {
			return refuseFX(economy.FXRefusalInvalid)
		}
		st, err := tx.Currency().State(ctx, s.CityID)
		if err != nil {
			return err
		}
		if st == nil || st.Status != application.CurrencyChartered {
			return refuseFX(economy.FXRefusalNoMkt)
		}
		rules, err := h.rulesNow(ctx)
		if err != nil {
			return err
		}
		money, err := h.money(ctx, tx, s, *st)
		if err != nil {
			return err
		}
		ref := fx.RefPrice(st.XRefPPM, st.R0)
		lo, hi := fx.Band(ref, rules.MaxMoveBPS)
		view = economy.FXOrderView{FXMoney: money, Side: side, Units: units, Price: price, R0: st.R0, XRefPPM: st.XRefPPM, BandLow: lo, BandHigh: hi}
		if !fx.InBand(price, ref, rules.MaxMoveBPS) {
			r := refuseFX(economy.FXRefusalBand)
			r.view.Min, r.view.Max = lo, hi
			return r
		}
		if strings.TrimSpace(req.Confirm) != economy.FXConfirm {
			view.Stage = economy.FXOrderAsk
			n, _ := fx.Notional(units, price)
			view.WorthSUP = fx.SUPOf(n)
			if side == application.FXBuy {
				view.FeeBPS, view.Escrow = rules.ReserveFeeBPS, fx.BuyEscrow(units, price, rules.ReserveFeeBPS)
			} else {
				view.FeeBPS, view.Escrow = st.FXFeeBPS, units
			}
			l := tx.Ledger()
			cash, err := l.AccountFor(ctx, application.AccountPlayerCash, p.ID)
			if err != nil {
				return err
			}
			hold, err := l.AccountForCurrency(ctx, application.AccountForeignHolding, p.ID, st.Code)
			if err != nil {
				return err
			}
			view.CashSUP, view.UnitsHeld = cash.Balance.Minor(), hold.Balance.Minor()
			if side == application.FXBuy {
				view.CanPlace = view.CashSUP >= view.Escrow
			} else {
				view.CanPlace = view.UnitsHeld >= view.Escrow
			}
			opp := application.FXSell
			if side == application.FXSell {
				opp = application.FXBuy
			}
			best, err := tx.FX().OpenOrders(ctx, s.CityID, opp, 1)
			if err != nil {
				return err
			}
			if len(best) > 0 {
				view.Crosses = (side == application.FXBuy && best[0].Price <= price) || (side == application.FXSell && best[0].Price >= price)
			}
			return nil
		}
		if fresh, err := h.reserve(ctx, tx, p, meta); err != nil {
			return err
		} else if !fresh {
			bv, err := h.bookView(ctx, tx, p, s, "")
			book = &bv
			return err // a repeated confirm: the book as it stands, nothing placed twice
		}
		placed, err := application.PlaceFX(ctx, tx, h.ids.NewID, rules, application.FXPlace{
			SettlementID: s.CityID, Owner: application.FXOwner{Kind: application.FXOwnerPlayer, ID: p.ID},
			Side: side, Units: units, Price: price, At: h.now(),
		})
		if err != nil {
			return err
		}
		view.Stage, view.Order, view.Rested = economy.FXOrderDone, orderLine(placed.Order), placed.Rested
		view.UnitsMoved, view.SUPMoved = placed.UnitsMoved, placed.SUPMoved
		l := tx.Ledger()
		cash, err := l.AccountFor(ctx, application.AccountPlayerCash, p.ID)
		if err != nil {
			return err
		}
		hold, err := l.AccountForCurrency(ctx, application.AccountForeignHolding, p.ID, st.Code)
		if err != nil {
			return err
		}
		view.CashSUP, view.UnitsHeld = cash.Balance.Minor(), hold.Balance.Minor()
		return nil
	})
	if err != nil {
		return h.finish(lang, err)
	}
	c := presentation.Ctx{Lang: lang}
	if book != nil {
		return economy.FXBook(c, *book).MarkPrivate(), nil
	}
	return economy.FXOrder(c, view).MarkPrivate(), nil
}

// Cancel handles fx.cancel: the owner ends an open order and gets its escrow back; a repeat does nothing.
func (h *FXHandler) Cancel(ctx context.Context, meta envelope.Metadata, req FXCancelRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	lang := meta.Language
	var view economy.FXBookView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		if _, err := h.reserve(ctx, tx, p, meta); err != nil {
			return err
		}
		o, err := application.CancelFX(ctx, tx, h.ids.NewID, application.FXOwner{Kind: application.FXOwnerPlayer, ID: p.ID}, strings.TrimSpace(req.Order), h.now())
		if err != nil {
			return err
		}
		s, err := tx.Settlements().ByID(ctx, o.SettlementID)
		if err != nil {
			return err
		}
		view, err = h.bookView(ctx, tx, p, s, "cancelled")
		return err
	})
	if err != nil {
		return h.finish(lang, err)
	}
	return economy.FXBook(presentation.Ctx{Lang: lang}, view).MarkPrivate(), nil
}

// History handles fx.history: the reference rate's readings, newest period first.
func (h *FXHandler) History(ctx context.Context, meta envelope.Metadata, req FXBookRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	lang := meta.Language
	var view economy.FXHistoryView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		s, err := h.settlementOf(ctx, tx, meta, p, req.Settlement)
		if err != nil {
			return err
		}
		st, err := tx.Currency().State(ctx, s.CityID)
		if err != nil {
			return err
		}
		if st == nil || st.Status != application.CurrencyChartered {
			return refuseFX(economy.FXRefusalNoMkt)
		}
		m, err := h.money(ctx, tx, s, *st)
		if err != nil {
			return err
		}
		view = economy.FXHistoryView{FXMoney: m, R0: st.R0, XRefPPM: st.XRefPPM, MinTrades: h.rules.MinTrades, WindowPeriods: int64(h.rules.Window),
			PeriodSeconds: int64(h.rules.Period / time.Second)}
		rates, err := tx.FX().Rates(ctx, s.CityID, 30)
		if err != nil {
			return err
		}
		for _, r := range rates {
			view.Periods = append(view.Periods, economy.FXPeriodLine{PeriodNo: r.PeriodNo, Trades: r.Trades, VolumeUnits: r.VolumeUnits,
				VWAP: fx.VWAPPrice(r.VolumeUnits, r.NotionalMicro), ValuePPM: r.ValuePPM, WindowTrades: r.WindowTrades,
				XRefBefore: r.XRefBefore, XRefAfter: r.XRefAfter, At: r.At})
		}
		return nil
	})
	if err != nil {
		return h.finish(lang, err)
	}
	return economy.FXHistory(presentation.Ctx{Lang: lang}, view), nil
}

// StartClock makes sure the book's clock runs. It is idempotent and safe on every replica: the clock row is
// locked and the scheduled action recorded once.
func (h *FXHandler) StartClock(ctx context.Context) error {
	// Support's treasury is resolved first: the configured rules carry only its code, so checking
	// h.rules alone would leave the clock unstarted on every live boot.
	r, err := h.rulesNow(ctx)
	if err != nil {
		return err
	}
	if !r.Enabled() || r.Period <= 0 {
		return nil
	}
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.FX().Clock(ctx, now)
		if err != nil || clock.ActionID != "" {
			return err
		}
		clock.PeriodStartedAt = now
		return h.schedule(ctx, tx, clock, now)
	})
}

// FXPayload is the jsonb of a period's scheduled action.
type FXPayload struct {
	PeriodNo int64 `json:"period_no"`
}

func (h *FXHandler) schedule(ctx context.Context, tx application.Tx, clock *application.FXClock, now time.Time) error {
	next := clock.PeriodStartedAt.Add(h.rules.Period)
	if !next.After(now) {
		next = now.Add(h.rules.Period)
	}
	payload, err := json.Marshal(FXPayload{PeriodNo: clock.PeriodNo})
	if err != nil {
		return err
	}
	actionID := h.ids.NewID()
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: actionID, ActionType: application.FXActionType, ActorType: "system", ReferenceType: application.FXActionReference,
		ReferenceID: actionID, Payload: payload, StartedAt: now, FinishAt: next,
	}); err != nil {
		return err
	}
	clock.NextAt, clock.ActionID, clock.UpdatedAt = &next, actionID, now
	return tx.FX().SaveClock(ctx, *clock)
}

// Settle handles fx.settle from the SCHEDULER: one period of the book, exactly once. It ends the orders past
// their expiry, appends each currency's reading and moves its x_ref once the window holds enough fills, then
// schedules the next period. Another replica's delivery of the same period finds the clock moved on (the
// fence) and does nothing.
func (h *FXHandler) Settle(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in FXPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("book payload is unreadable").WithCause(err)
		}
	}
	if in.PeriodNo < 1 {
		return nil, nil
	}
	if r, err := h.rulesNow(ctx); err != nil {
		return nil, err
	} else if !r.Enabled() {
		return nil, nil
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.FX().Clock(ctx, now)
		if err != nil {
			return err
		}
		if clock.PeriodNo != in.PeriodNo || clock.ActionID == "" || (req.ActionID != "" && clock.ActionID != req.ActionID) {
			return nil
		}
		if clock.NextAt != nil && now.Before(*clock.NextAt) {
			return errors.Internal(stderrors.New("handlers: a book period ran before it ended"))
		}
		rules, err := h.rulesNow(ctx)
		if err != nil {
			return err
		}
		ids, err := tx.Currency().CharteredSettlements(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := application.ExpireFX(ctx, tx, h.ids.NewID, id, now); err != nil {
				return err
			}
			if _, _, err := application.SettleFXRate(ctx, tx, rules, id, clock.PeriodNo, clock.PeriodStartedAt, now, now); err != nil {
				return err
			}
		}
		clock.PeriodNo++
		clock.PeriodStartedAt = now
		clock.NextAt, clock.ActionID = nil, ""
		return h.schedule(ctx, tx, clock, now)
	})
}

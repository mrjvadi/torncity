package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/domain/fx"
	"github.com/mrjvadi/torncity/internal/domain/reserve"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The reserve screen and the head's tools over it (docs/adr/0033 6.3 to 6.8, 6.13; roadmap 2.19 phase 4):
// settlement.currency.reserve. No action shows the reserve, its valuation, the macro readings and the public
// log; each act asks first (the figures it would have) and carries out on confirm, once.
//
//	issue     mint more against a deposit of SUP from the treasury (currency.issue)
//	burn      burn units the treasury holds (currency.issue)
//	buy,sell  the head's intervention on the book, posted now and executed at the next period close
//	          (bank.policy)
//	withdraw  announce a withdrawal of the excess over the basis; it executes after the Reserve Bank's
//	          notice (bank.policy)
//	cancel    withdraw a pending request (bank.policy)
//	retire    retire the money: a wind-down (currency.issue)
//	claim     a holder's share of the pot in a wind-down (any holder)

// VillageReserveRequest is the payload of settlement.currency.reserve.
type VillageReserveRequest struct {
	Action string `json:"action,omitempty"`
	// Amount is SUP for issue and withdraw, units for burn, buy and sell; Price micro-SUP per unit (or SUP with
	// a decimal point); ID a pending request to cancel.
	Amount  string `json:"amount,omitempty"`
	Price   string `json:"price,omitempty"`
	ID      string `json:"id,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	// Settlement names the settlement for a game client.
	Settlement string `json:"settlement,omitempty"`
}

func (r VillageReserveRequest) confirmed() bool { return r.Confirm == village.ResidenceConfirm }

// reserveRefusal maps the application's refusals to the screen's.
func reserveRefusal(err error) (error, bool) {
	pairs := []struct {
		err  error
		kind string
	}{
		{application.ErrReserveNoMoney, village.VillageNotAvailable},
		{application.ErrReserveInvalid, village.ReserveInvalid},
		{application.ErrReserveFunds, village.ReserveFunds},
		{application.ErrReserveNoUnits, village.ReserveNoUnits},
		{application.ErrReserveNoExcess, village.ReserveNoExcess},
		{application.ErrReserveBudget, village.ReserveBudget},
		{application.ErrReserveNotFound, village.ReserveNotFound},
		{application.ErrReserveWindDown, village.ReserveWinding},
		{application.ErrReserveNotWind, village.ReserveNotWind},
		{application.ErrReserveNothing, village.ReserveNothing},
	}
	for _, p := range pairs {
		if stderrors.Is(err, p.err) {
			return refuseVillage(p.kind), true
		}
	}
	return nil, false
}

// CurrencyReserve handles settlement.currency.reserve.
func (h *VillageHandler) CurrencyReserve(ctx context.Context, meta envelope.Metadata, req VillageReserveRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.ReserveView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		st, err := tx.Currency().State(ctx, s.CityID)
		if err != nil {
			return err
		}
		if st == nil || st.Status == application.CurrencyRetired && strings.TrimSpace(req.Action) != "" {
			return refuseVillage(village.VillageNotAvailable)
		}
		rules := h.currencyRules.Reserve
		terms, err := rules.Terms(ctx, tx)
		if err != nil {
			return err
		}
		if view, err = h.reserveView(ctx, tx, s, p.ID, terms); err != nil {
			return err
		}
		view.Stage = village.ReserveMenu
		view.CanIssue, err = h.mayVillage(ctx, tx, s, p.ID, charter.CurrencyIssue)
		if err != nil {
			return err
		}
		view.CanPolicy, err = h.mayVillage(ctx, tx, s, p.ID, charter.BankPolicy)
		if err != nil {
			return err
		}
		action := strings.TrimSpace(req.Action)
		if action == "" {
			return nil
		}
		view.Action, view.Stage = action, village.ReserveAsk
		amount, _ := strconv.ParseInt(strings.TrimSpace(req.Amount), 10, 64)
		view.Amount = amount
		now := h.now()
		need := func(ok bool) error {
			if !ok {
				return application.ErrNotOfficeHolder
			}
			return nil
		}
		confirmed := req.confirmed()
		fence := func() (bool, error) {
			if !confirmed {
				return false, nil
			}
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return false, err
			}
			if !fresh {
				view.Stage = village.ReserveDone // a repeated confirm: already done
				return false, nil
			}
			return true, nil
		}
		switch action {
		case village.ReserveActionIssue:
			if err := need(view.CanIssue); err != nil {
				return err
			}
			if amount < 1 || amount > view.TreasurySUP {
				view.Reason = "funds"
				if amount < 1 {
					return refuseVillage(village.ReserveInvalid)
				}
			}
			m, merr := currency.Mint(amount, st.Rate(), terms.MintFeeBPS)
			if merr != nil {
				return refuseVillage(village.ReserveInvalid)
			}
			view.Out = m.Units
			if doIt, err := fence(); err != nil || !doIt {
				return err
			}
			if _, err := application.MintMore(ctx, tx, h.ids.NewID, terms, application.MintMoreRequest{SettlementID: s.CityID, DepositSUP: amount,
				By: "player:" + p.ID, At: now}); err != nil {
				return err
			}
		case village.ReserveActionBurn:
			if err := need(view.CanIssue); err != nil {
				return err
			}
			if amount < 1 || amount > view.TreasuryUnits {
				view.Reason = "no_units"
				if amount < 1 {
					return refuseVillage(village.ReserveInvalid)
				}
			}
			view.Out = currency.BurnBasis(st.BasisSUP, st.Supply(), amount)
			if doIt, err := fence(); err != nil || !doIt {
				return err
			}
			if _, err := application.BurnTreasury(ctx, tx, h.ids.NewID, s.CityID, amount, "player:"+p.ID, now); err != nil {
				return err
			}
		case village.ReserveActionBuy, village.ReserveActionSell:
			if err := need(view.CanPolicy); err != nil {
				return err
			}
			price := int64(0)
			if t := strings.TrimSpace(req.Price); t != "" {
				pr, ok := fx.ParsePrice(t)
				if !ok {
					return refuseVillage(village.ReserveInvalid)
				}
				price = pr
			} else {
				price = fx.RefPrice(st.XRefPPM, st.R0)
			}
			view.Price = price
			if amount < 1 {
				return refuseVillage(village.ReserveInvalid)
			}
			ex := now.Add(rules.InterventionDelay)
			view.ExecuteAfter = &ex
			if action == village.ReserveActionBuy {
				view.Out = fx.BuyEscrow(amount, price, terms.FXFeeBPS)
				if view.BuyBudgetSUP <= 0 {
					view.Reason = "budget"
				}
			} else {
				view.Out = amount
				if view.SellBudgetUnits <= 0 {
					view.Reason = "budget"
				}
			}
			if doIt, err := fence(); err != nil || !doIt {
				return err
			}
			if view.Reason != "" {
				return refuseVillage(village.ReserveBudget)
			}
			if _, err := application.PostIntervention(ctx, tx, h.ids.NewID, rules, s.CityID, p.ID, action, amount, price, now); err != nil {
				return err
			}
		case village.ReserveActionWithdraw:
			if err := need(view.CanPolicy); err != nil {
				return err
			}
			if amount < 1 {
				return refuseVillage(village.ReserveInvalid)
			}
			ex := now.Add(terms.WithdrawNotice())
			view.ExecuteAfter = &ex
			view.Out = view.Excess - view.PendingWithdrawals - amount
			if view.Out < 0 {
				view.Reason = "no_excess"
			}
			if doIt, err := fence(); err != nil || !doIt {
				return err
			}
			if _, err := application.RequestWithdrawal(ctx, tx, h.ids.NewID, terms, s.CityID, p.ID, amount, now); err != nil {
				return err
			}
		case village.ReserveActionCancel:
			if err := need(view.CanPolicy); err != nil {
				return err
			}
			if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
				return err
			} else if !fresh {
				view.Stage = village.ReserveDone
				return nil
			}
			if err := application.CancelReserveRequest(ctx, tx, s.CityID, strings.TrimSpace(req.ID), now); err != nil {
				return err
			}
		case village.ReserveActionRetire:
			if err := need(view.CanIssue); err != nil {
				return err
			}
			view.Out = view.PotSUP
			if doIt, err := fence(); err != nil || !doIt {
				return err
			}
			if ok, err := application.BeginWindDown(ctx, tx, h.ids.NewID, rules, s.CityID, "retired", now); err != nil {
				return err
			} else if !ok {
				return refuseVillage(village.ReserveWinding)
			}
		case village.ReserveActionClaim:
			if st.Status != application.CurrencyWindDown {
				return refuseVillage(village.ReserveNotWind)
			}
			view.Amount, view.Out = view.MyUnits, view.MyShare
			if doIt, err := fence(); err != nil || !doIt {
				return err
			}
			r, err := application.ClaimPot(ctx, tx, h.ids.NewID, s.CityID, p.ID, now)
			if err != nil {
				return err
			}
			view.Amount, view.Out = r.Units, r.SUP
		default:
			return refuseVillage(village.ReserveInvalid)
		}
		keep := view
		if view, err = h.reserveView(ctx, tx, s, p.ID, terms); err != nil {
			return err
		}
		view.CanIssue, view.CanPolicy = keep.CanIssue, keep.CanPolicy
		view.Stage, view.Action, view.Amount, view.Price, view.Out, view.ExecuteAfter = village.ReserveDone, keep.Action, keep.Amount, keep.Price, keep.Out, keep.ExecuteAfter
		return nil
	})
	if err != nil {
		if r, ok := reserveRefusal(err); ok {
			err = r
		}
	}
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.CurrencyReserve(h.screen(meta, lang), view), nil
}

// reserveView fills the parts of the reserve screen every stage shares.
func (h *VillageHandler) reserveView(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string,
	terms application.ReserveTerms,
) (village.ReserveView, error) {
	var v village.ReserveView
	rd, err := application.ReadReserve(ctx, tx, s.CityID)
	if err != nil || rd == nil {
		return v, err
	}
	rules := h.currencyRules.Reserve
	st := rd.State
	v = village.ReserveView{Village: s.Name, Name: s.Currency.Name, Symbol: s.Currency.Symbol, Status: st.Status, R0: st.R0, XRefPPM: st.XRefPPM,
		PotSUP: rd.PotSUP, Basis: rd.Basis, Excess: rd.Excess, Supply: rd.Supply, Stabilisation: rd.Stabilisation, MarketCapSUP: rd.MarketCap,
		CoverageBPS: rd.Coverage, CoverageKnown: rd.CoverageOK, Minted: st.MintedUnits, Burnt: st.BurntUnits, Deposited: st.DepositedSUP,
		Released: st.ReleasedSUP, InterventionOut: st.InterventionOut, InterventionIn: st.InterventionIn,
		MintFeeBPS: terms.MintFeeBPS, ReserveFeeBPS: terms.FXFeeBPS, MaxMoveBPS: terms.MaxMoveBPS, WithdrawNoticeHours: terms.WithdrawNoticeHours,
		CapBPS: rules.InterventionCapBPS, FloorBPS: rules.PotFloorBPS, DelayHours: int64(rules.InterventionDelay.Hours()),
		TreasuryUnits: rd.Treasury, PendingWithdrawals: rd.PendingWithdrawals, Presets: rules.Presets,
		WindDownAt: st.WindDownAt, WindDownEndsAt: st.WindDownEndsAt}
	if v.Name == "" {
		v.Name, v.Symbol = st.Code, st.Code
	}
	if treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID); err == nil {
		v.TreasurySUP = treasury.Balance.Minor()
	}
	if cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, playerID); err == nil {
		v.CashSUP = cash.Balance.Minor()
	}
	if mine, err := tx.Ledger().AccountForCurrency(ctx, application.AccountForeignHolding, playerID, st.Code); err == nil {
		v.MyUnits = mine.Balance.Minor()
	}
	if st.Status == application.CurrencyChartered {
		usedSUP, usedUnits, err := tx.Currency().InterventionUse(ctx, s.CityID, h.now().Add(-rules.InterventionDelay))
		if err != nil {
			return v, err
		}
		v.BuyBudgetSUP = reserve.BuyBudget(rd.PotSUP, rd.Basis, usedSUP, rules.InterventionCapBPS, rules.PotFloorBPS)
		v.SellBudgetUnits = reserve.SellBudget(rd.Treasury, usedUnits, rules.InterventionCapBPS)
	}
	if st.Status == application.CurrencyWindDown && v.MyUnits > 0 && playerID != s.CityID {
		if claimable, err := tx.Currency().ClaimableUnits(ctx, s.CityID, st.Code); err == nil {
			v.MyShare = reserve.ClaimShare(rd.PotSUP, claimable, v.MyUnits)
		}
		v.CanClaim = st.WindDownEndsAt != nil && h.now().Before(*st.WindDownEndsAt)
	}
	is, err := tx.Currency().InterventionsOf(ctx, s.CityID, 10)
	if err != nil {
		return v, err
	}
	for _, i := range is {
		v.Interventions = append(v.Interventions, village.InterventionLine{ID: i.ID, Side: i.Side, Units: i.Units, Price: i.Price, Status: i.Status,
			SUPUsed: i.SUPUsed, Refusal: i.Refusal, PostedAt: i.PostedAt, ExecuteAfter: i.ExecuteAfter})
	}
	ws, err := tx.Currency().WithdrawalsOf(ctx, s.CityID, 10)
	if err != nil {
		return v, err
	}
	for _, w := range ws {
		v.Withdrawals = append(v.Withdrawals, village.WithdrawalLine{ID: w.ID, SUP: w.SUP, Status: w.Status, Refusal: w.Refusal,
			RequestedAt: w.RequestedAt, ExecuteAfter: w.ExecuteAfter})
	}
	rows, err := tx.Currency().MacroRows(ctx, s.CityID, 7)
	if err != nil {
		return v, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		v.Trend = append(v.Trend, macroLine(rows[i]))
	}
	if len(v.Trend) > 0 {
		last := v.Trend[len(v.Trend)-1]
		v.Macro = &last
	}
	return v, nil
}

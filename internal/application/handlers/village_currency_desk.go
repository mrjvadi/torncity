package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The village desk (roadmap 2.19 phase 2, docs/adr/0033 6.8 and 6.10). A resident converts between SUP and
// the settlement's own money at the treasury's quote: the live rate less the desk's fee. It is one atomic
// confirm with price protection; nothing converts silently. The units it sells are the treasury's own
// holding and its SUP proceeds go to the treasury; when either runs out the desk says so and never mints.

// VillageDeskRequest is the payload of settlement.currency.desk.
type VillageDeskRequest struct {
	// Side is "buy" (pay SUP, receive units) or "sell" (give units, receive SUP); empty shows the menu.
	Side string `json:"side,omitempty"`
	// Amount is what the player offers (SUP for a buy, units for a sell), Quote what they were
	// shown they would receive (the price protection: the desk refuses when it has moved against them
	// by more than currency.desk_slippage_bps), as text like every button argument.
	Amount  string `json:"amount,omitempty"`
	Quote   string `json:"quote,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	// Settlement names the settlement for a game client.
	Settlement string `json:"settlement,omitempty"`
}

func (r VillageDeskRequest) confirmed() bool { return r.Confirm == village.ResidenceConfirm }

// VillageDeskFeeRequest is the payload of settlement.currency.fee.
type VillageDeskFeeRequest struct {
	BPS        string `json:"bps"`
	Settlement string `json:"settlement,omitempty"`
}

// deskView fills the parts of the desk screen every stage shares.
func (h *VillageHandler) deskView(ctx context.Context, tx application.Tx, s application.FoundedSettlement, st application.CurrencyState,
	playerID string,
) (village.DeskView, error) {
	res, err := tx.Currency().Reservation(ctx, s.CityID)
	if err != nil {
		return village.DeskView{}, err
	}
	v := village.DeskView{Village: s.Name, R0: st.R0, XRefPPM: st.XRefPPM, FeeBPS: st.FXFeeBPS, SlippageBPS: h.currencyRules.DeskSlippageBPS,
		MinFeeBPS: currency.MinDeskFeeBPS, MaxFeeBPS: currency.MaxDeskFeeBPS}
	if res != nil {
		v.Name, v.Symbol = res.Name, res.Symbol
	}
	ledger := tx.Ledger()
	cash, err := ledger.AccountFor(ctx, application.AccountPlayerCash, playerID)
	if err != nil {
		return v, err
	}
	hold, err := ledger.AccountForCurrency(ctx, application.AccountForeignHolding, playerID, st.Code)
	if err != nil {
		return v, err
	}
	tHold, err := ledger.AccountForCurrency(ctx, application.AccountForeignHolding, s.CityID, st.Code)
	if err != nil {
		return v, err
	}
	tSUP, err := ledger.AccountFor(ctx, application.AccountCityTreasury, s.CityID)
	if err != nil {
		return v, err
	}
	v.CashSUP, v.CashUnits, v.DeskUnits, v.DeskSUP = cash.Balance.Minor(), hold.Balance.Minor(), tHold.Balance.Minor(), tSUP.Balance.Minor()
	for _, n := range h.currencyRules.DeskPresets {
		v.PresetsSUP = append(v.PresetsSUP, n)
		v.PresetsUnits = append(v.PresetsUnits, st.Rate().ToLocalFloor(n))
	}
	return v, nil
}

// CurrencyDesk handles settlement.currency.desk.
func (h *VillageHandler) CurrencyDesk(ctx context.Context, meta envelope.Metadata, req VillageDeskRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.DeskView
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
		if ok, err := h.resident(ctx, tx, p.ID, s.CityID); err != nil {
			return err
		} else if !ok {
			return refuseVillage(village.VillageNotResident)
		}
		st, err := tx.Currency().State(ctx, s.CityID)
		if err != nil {
			return err
		}
		if st == nil || st.Status != application.CurrencyChartered {
			return refuseVillage(village.VillageNotAvailable)
		}
		if view, err = h.deskView(ctx, tx, s, *st, p.ID); err != nil {
			return err
		}
		view.CanSetFee, err = h.mayVillage(ctx, tx, s, p.ID, charter.CurrencyCharter)
		if err != nil {
			return err
		}
		view.Stage = village.DeskMenu
		side := strings.TrimSpace(req.Side)
		if side == "" {
			return nil
		}
		if side != application.DeskBuy && side != application.DeskSell {
			return refuseVillage(village.VillageNotAvailable)
		}
		amount, perr := strconv.ParseInt(strings.TrimSpace(req.Amount), 10, 64)
		if perr != nil || amount < 1 {
			return refuseVillage(village.VillageNotAvailable)
		}
		q := application.QuoteDesk(*st, side, amount)
		view.Side, view.Amount, view.SUP, view.Units, view.Fee = side, amount, q.SUP, q.Units, q.Fee
		view.Stage = village.DeskAsk
		out := q.Units
		if side == application.DeskSell {
			out = q.SUP
		}
		if out <= 0 {
			return refuseVillage(village.VillageNotAvailable)
		}
		view.CanBuy = view.CashSUP >= q.SUP && view.DeskUnits >= q.Units
		view.CanSell = view.CashUnits >= q.Units && view.DeskSUP >= q.SUP
		if !req.confirmed() {
			return nil
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			view.Stage = village.DeskDone // a redelivered confirm: already converted
			return nil
		}
		var minOut int64
		if t := strings.TrimSpace(req.Quote); t != "" {
			shown, qerr := strconv.ParseInt(t, 10, 64)
			if qerr != nil || shown < 0 {
				return refuseVillage(village.VillageNotAvailable)
			}
			minOut = shown * (currency.BPS - h.currencyRules.DeskSlippageBPS) / currency.BPS
		}
		now := h.now()
		if _, err := application.ExecuteDesk(ctx, tx, h.ids.NewID, *st, p.ID, side, amount, minOut, now); err != nil {
			switch {
			case stderrors.Is(err, application.ErrDeskEmpty):
				return refuseVillage(village.VillageDeskEmpty)
			case stderrors.Is(err, application.ErrDeskNoSUP):
				return refuseVillage(village.VillageDeskNoSUP)
			case stderrors.Is(err, application.ErrDeskFunds):
				return refuseVillage(village.VillageDeskFunds)
			case stderrors.Is(err, application.ErrDeskMoved):
				return refuseVillage(village.VillageDeskMoved)
			case stderrors.Is(err, application.ErrDeskNone):
				return refuseVillage(village.VillageNotAvailable)
			}
			return err
		}
		if err := appendVillageEvent(ctx, tx, meta, "currency_desk", s.CityID, map[string]any{
			"settlement_id": s.CityID, "player_id": p.ID, "side": side, "sup": q.SUP, "units": q.Units, "fee_bps": st.FXFeeBPS,
		}); err != nil {
			return err
		}
		view, err = h.deskView(ctx, tx, s, *st, p.ID)
		if err != nil {
			return err
		}
		view.Stage, view.Side, view.Amount, view.SUP, view.Units, view.Fee = village.DeskDone, side, amount, q.SUP, q.Units, q.Fee
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.CurrencyDesk(h.screen(meta, lang), view), nil
}

// CurrencyDeskFee handles settlement.currency.fee: the head (currency.charter) sets the desk's fee within
// 10 to 300 bps. It answers with the desk's menu.
func (h *VillageHandler) CurrencyDeskFee(ctx context.Context, meta envelope.Metadata, req VillageDeskFeeRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.DeskView
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
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.CurrencyCharter); err != nil {
			return err
		}
		st, err := tx.Currency().State(ctx, s.CityID)
		if err != nil {
			return err
		}
		if st == nil || st.Status != application.CurrencyChartered {
			return refuseVillage(village.VillageNotAvailable)
		}
		bps, perr := strconv.ParseInt(strings.TrimSpace(req.BPS), 10, 64)
		if perr != nil || !currency.ValidDeskFee(bps) {
			return refuseVillage(village.VillageNotAvailable)
		}
		if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
			return err
		} else if fresh {
			if err := tx.Currency().SetFXFee(ctx, s.CityID, bps); err != nil {
				return err
			}
			st.FXFeeBPS = bps
		}
		st2, err := tx.Currency().State(ctx, s.CityID)
		if err != nil {
			return err
		}
		if view, err = h.deskView(ctx, tx, s, *st2, p.ID); err != nil {
			return err
		}
		view.Stage, view.CanSetFee = village.DeskMenu, true
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.CurrencyDesk(h.screen(meta, lang), view), nil
}

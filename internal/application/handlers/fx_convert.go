package handlers

import (
	"context"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/fx"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// Converting from one money to another through SUP (docs/adr/0033 6.8 and 6.10; docs/adr/0029 4.2): a sale of
// units for SUP, a purchase of units with SUP, or the two in a row for a conversion between two villages'
// moneys, each leg on its own book with its own fee. The confirm is one step with price protection: the
// legs are market orders of the viewer's, matched in one transaction, and the whole is refused and rolled
// back when what comes out is worse than the quote less the slippage. Nothing is converted silently.

const supCode = "SUP"

// fxSide is one side of a conversion resolved against the books.
type fxSide struct {
	code  string
	state *application.CurrencyState // nil for SUP
	set   application.FoundedSettlement
}

func (h *FXHandler) side(ctx context.Context, tx application.Tx, code string) (fxSide, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" || code == supCode {
		return fxSide{code: supCode}, nil
	}
	st, err := tx.Currency().StateByCode(ctx, code)
	if err != nil {
		return fxSide{}, err
	}
	if st == nil || st.Status != application.CurrencyChartered {
		return fxSide{}, refuseFX(economy.FXRefusalNoMkt)
	}
	s, err := tx.Settlements().ByID(ctx, st.SettlementID)
	if err != nil {
		return fxSide{}, err
	}
	return fxSide{code: st.Code, state: st, set: s}, nil
}

// legQuote prices one leg against its book without changing anything.
func (h *FXHandler) legQuote(ctx context.Context, tx application.Tx, rules application.FXRules, sd fxSide, selling bool, amount int64) (economy.FXLeg, application.FXQuote, error) {
	var leg economy.FXLeg
	m, err := h.money(ctx, tx, sd.set, *sd.state)
	if err != nil {
		return leg, application.FXQuote{}, err
	}
	leg.FXMoney = m
	now := h.now()
	if selling {
		q, err := application.QuoteFXSell(ctx, tx, rules, sd.set.CityID, amount, now)
		if err != nil {
			return leg, q, err
		}
		leg.Side, leg.UnitsIn, leg.SUPOut, leg.Fee, leg.Complete = economy.FXSideSell, amount, q.SUPOut, q.Fee, q.Complete
		return leg, q, nil
	}
	q, err := application.QuoteFXBuy(ctx, tx, rules, sd.set.CityID, amount, now)
	if err != nil {
		return leg, q, err
	}
	leg.Side, leg.SUPIn, leg.UnitsOut, leg.Fee, leg.Complete = economy.FXSideBuy, q.SUPIn, q.UnitsOut, q.Fee, q.Complete && q.Units > 0
	return leg, q, nil
}

// Convert handles fx.convert: the menu of what the viewer holds, the quote of one conversion, or the
// conversion itself.
func (h *FXHandler) Convert(ctx context.Context, meta envelope.Metadata, req FXConvertRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	lang := meta.Language
	var view economy.FXConvertView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		rules, err := h.rulesNow(ctx)
		if err != nil {
			return err
		}
		view = economy.FXConvertView{Stage: economy.FXConvertMenu, SlippageBPS: rules.ConvertSlippageBPS}
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
		if err != nil {
			return err
		}
		view.CashSUP = cash.Balance.Minor()
		held, err := tx.Currency().Holdings(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, hd := range held {
			s, err := tx.Settlements().ByID(ctx, hd.SettlementID)
			if err != nil {
				return err
			}
			st, err := tx.Currency().State(ctx, hd.SettlementID)
			if err != nil || st == nil {
				return err
			}
			m, err := h.money(ctx, tx, s, *st)
			if err != nil {
				return err
			}
			view.Holdings = append(view.Holdings, economy.FXHolding{FXMoney: m, Units: hd.Units})
		}
		from, to := strings.ToUpper(strings.TrimSpace(req.From)), strings.ToUpper(strings.TrimSpace(req.To))
		amount, perr := strconv.ParseInt(strings.TrimSpace(req.Amount), 10, 64)
		if from == "" || to == "" || perr != nil || amount < 1 {
			return nil // the menu
		}
		if from == to {
			return refuseFX(economy.FXRefusalInvalid)
		}
		src, err := h.side(ctx, tx, from)
		if err != nil {
			return err
		}
		dst, err := h.side(ctx, tx, to)
		if err != nil {
			return err
		}
		view.Stage, view.From, view.To, view.Amount = economy.FXConvertAsk, src.code, dst.code, amount

		// the quote: sell the source units for SUP, then buy the destination units with that SUP
		var sup, out int64
		complete := true
		if src.state != nil {
			leg, _, err := h.legQuote(ctx, tx, rules, src, true, amount)
			if err != nil {
				return err
			}
			view.Legs = append(view.Legs, leg)
			sup, complete = leg.SUPOut, leg.Complete
			if dst.state == nil {
				out = sup
			}
		} else {
			sup = amount
		}
		if dst.state != nil {
			leg, _, err := h.legQuote(ctx, tx, rules, dst, false, sup)
			if err != nil {
				return err
			}
			view.Legs = append(view.Legs, leg)
			out, complete = leg.UnitsOut, complete && leg.Complete
		}
		view.Out, view.Complete = out, complete && out > 0
		view.MinOut = out * (fx.BPS - rules.ConvertSlippageBPS) / fx.BPS
		if strings.TrimSpace(req.Confirm) != economy.FXConfirm {
			return nil
		}

		// the confirm: one step, price protection against the quote the player saw
		if fresh, err := h.reserve(ctx, tx, p, meta); err != nil {
			return err
		} else if !fresh {
			view.Stage = economy.FXConvertDone
			return nil
		}
		shown, qerr := strconv.ParseInt(strings.TrimSpace(req.Quote), 10, 64)
		if qerr != nil || shown < 0 {
			return refuseFX(economy.FXRefusalInvalid)
		}
		minOut := shown * (fx.BPS - rules.ConvertSlippageBPS) / fx.BPS
		owner := application.FXOwner{Kind: application.FXOwnerPlayer, ID: p.ID}
		now := h.now()
		gave, got := amount, int64(0)
		supGot := amount
		if src.state != nil {
			lo, _ := fx.Band(fx.RefPrice(src.state.XRefPPM, src.state.R0), rules.MaxMoveBPS)
			placed, err := application.PlaceFX(ctx, tx, h.ids.NewID, rules, application.FXPlace{
				SettlementID: src.set.CityID, Owner: owner, Side: application.FXSell, Units: amount, Price: lo, IOC: true, At: now})
			if err != nil {
				return err
			}
			if placed.UnitsMoved != amount {
				return application.ErrFXNoLiquidity
			}
			supGot = placed.SUPMoved
		}
		if dst.state == nil {
			got = supGot
		} else {
			q, err := application.QuoteFXBuy(ctx, tx, rules, dst.set.CityID, supGot, now)
			if err != nil {
				return err
			}
			if q.Units == 0 {
				return application.ErrFXNoLiquidity
			}
			_, hi := fx.Band(fx.RefPrice(dst.state.XRefPPM, dst.state.R0), rules.MaxMoveBPS)
			placed, err := application.PlaceFX(ctx, tx, h.ids.NewID, rules, application.FXPlace{
				SettlementID: dst.set.CityID, Owner: owner, Side: application.FXBuy, Units: q.Units, Price: min(q.Price, hi), IOC: true, At: now})
			if err != nil {
				return err
			}
			got = placed.UnitsMoved
		}
		if got < minOut || got <= 0 {
			return application.ErrFXMoved
		}
		view.Stage, view.Gave, view.Got = economy.FXConvertDone, gave, got
		return nil
	})
	if err != nil {
		return h.finish(lang, err)
	}
	return economy.FXConvert(presentation.Ctx{Lang: lang}, view).MarkPrivate(), nil
}

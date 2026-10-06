package handlers

import (
	"context"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// A settlement's own money, the head's path (roadmap 2.19 phase 1, docs/adr/0033 section 6.2). The
// founding charters it from the grant and the deploy charters the old settlements once; this is for
// a settlement whose automatic charter could not be paid, and the screen of a money that exists.

// WithCurrencyRules sets the money rules of a charter (config currency.*).
func (h *VillageHandler) WithCurrencyRules(r application.CurrencyRules) *VillageHandler {
	h.currencyRules = r
	return h
}

// VillageCurrencyRequest is the payload of settlement.currency.charter.
type VillageCurrencyRequest struct {
	// R0 is the chosen starting scale and Deposit the first deposit in SUP, as text like every
	// button argument; empty takes the configured defaults. Confirm is village.ResidenceConfirm on
	// the second press. Settlement names the settlement for a game client.
	R0         string `json:"r0,omitempty"`
	Deposit    string `json:"deposit,omitempty"`
	Confirm    string `json:"confirm,omitempty"`
	Settlement string `json:"settlement,omitempty"`
}

func (r VillageCurrencyRequest) confirmed() bool { return r.Confirm == village.ResidenceConfirm }

// CurrencyCharter handles settlement.currency.charter (the permission currency.charter): it offers the
// charter (the fee, the first deposit, the units it buys), and on confirm charters the money. A money
// that exists answers with its live rate and reserve. One transaction, idempotent by the settlement's
// currency state row.
func (h *VillageHandler) CurrencyCharter(ctx context.Context, meta envelope.Metadata, req VillageCurrencyRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.CurrencyCharterView
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
		rules := h.currencyRules
		view = village.CurrencyCharterView{Village: s.Name, Fee: rules.Terms.Fee, MinDeposit: rules.Terms.MinDeposit,
			R0Options: currency.RateOptions, MintFeeBPS: rules.MintFeeBPS}
		res, err := tx.Currency().Reservation(ctx, s.CityID)
		if err != nil {
			return err
		}
		if res == nil || !rules.Enabled() {
			return refuseVillage(village.VillageNotAvailable)
		}
		view.Name, view.Symbol = res.Name, res.Symbol
		if st, err := tx.Currency().State(ctx, s.CityID); err != nil {
			return err
		} else if st != nil {
			view.Stage, view.R0, view.Supply, view.XRefPPM = village.CharterExists, st.R0, st.Supply(), st.XRefPPM
			view.PotSUP = st.DepositedSUP - st.ReleasedSUP
			return nil
		}
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.CurrencyCharter); err != nil {
			return err
		}
		treasury, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		view.Treasury = treasury
		view.R0, view.Deposit = rules.CharterR0, rules.Terms.MinDeposit
		if t := strings.TrimSpace(req.R0); t != "" {
			n, perr := strconv.ParseInt(t, 10, 64)
			if perr != nil || !currency.ValidR0(n) {
				return refuseVillage(village.VillageNotAvailable)
			}
			view.R0 = n
		}
		if t := strings.TrimSpace(req.Deposit); t != "" {
			n, perr := strconv.ParseInt(t, 10, 64)
			if perr != nil || n < rules.Terms.MinDeposit {
				return refuseVillage(village.VillageNotAvailable)
			}
			view.Deposit = n
		}
		mint, err := currency.Mint(view.Deposit, currency.Rate{R0: view.R0, XRefPPM: currency.PPM}, rules.MintFeeBPS)
		if err != nil {
			return refuseVillage(village.VillageNotAvailable)
		}
		view.Units = mint.Units
		view.CanPay = treasury >= view.Fee+view.Deposit
		view.Stage = village.CharterAsk
		if !req.confirmed() {
			return nil
		}
		if !view.CanPay {
			return refuseVillage(village.VillageInsufficient)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			view.Stage = village.CharterDone // a redelivered confirm: already chartered
			return nil
		}
		out, err := application.CharterCurrency(ctx, tx, h.ids.NewID, rules, s.CityID, view.R0, view.Fee, view.Deposit, "head:"+p.ID, h.now())
		if err != nil {
			return err
		}
		view.Stage = village.CharterDone
		if !out.Done {
			return nil
		}
		view.Units = out.Units
		view.Supply, view.PotSUP, view.XRefPPM = out.Units, view.Deposit, currency.PPM
		if bal, berr := treasuryBalance(ctx, tx, s.CityID); berr == nil {
			view.Treasury = bal
		}
		return appendVillageEvent(ctx, tx, meta, "currency_chartered", s.CityID, map[string]any{
			"settlement_id": s.CityID, "player_id": p.ID, "code": out.Code, "r0": out.R0, "fee": out.Fee, "deposit": out.Deposit, "units": out.Units,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.CurrencyCharter(h.screen(meta, lang), view), nil
}

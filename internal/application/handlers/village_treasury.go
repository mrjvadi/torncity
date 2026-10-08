package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The village treasury's two faucets: the founding grant (WithFoundingGrant,
// grantTreasury) and residents' donations (settlement.donate). See
// internal/application/village_treasury.go and migration 0052.

// WithFoundingGrant sets the treasury a freshly founded village starts with
// (config settlement.founding_grant), minted in the founding transaction.
func (h *SettlementsHandler) WithFoundingGrant(amount int64) *SettlementsHandler {
	h.foundingGrant = amount
	return h
}

// WithCurrencyRules sets the money rules of the automatic charter that follows the founding grant
// (config currency.*, docs/adr/0033 section 6).
func (h *SettlementsHandler) WithCurrencyRules(r application.CurrencyRules) *SettlementsHandler {
	h.currencyRules = r
	return h
}

// charterCurrency charters the new settlement's money from its reservation, paying the fee and the
// first deposit from the founding grant when it covers them (the owner's rule of 2026-10-06), in
// the founding transaction. A treasury that cannot pay leaves the head the offer.
func (h *SettlementsHandler) charterCurrency(ctx context.Context, tx application.Tx, settlementID string, at time.Time) error {
	_, err := application.AutoCharter(ctx, tx, h.ids.NewID, h.currencyRules, settlementID, true, "system:founding", at)
	return err
}

// grantTreasury gives a just-founded settlement its founding grant, in the
// caller's founding transaction. A zero grant (not configured) does nothing.
func (h *SettlementsHandler) grantTreasury(ctx context.Context, tx application.Tx, settlementID string, at time.Time) error {
	if h.foundingGrant <= 0 {
		return nil
	}
	_, err := application.GrantSettlementTreasury(ctx, tx, settlementID, h.foundingGrant, h.ids.NewID(),
		application.SettlementGrantFounding, "system:founding", at)
	return err
}

// VillageDonateRequest is the payload of settlement.donate.
type VillageDonateRequest struct {
	// Amount is the gift in minor units, as text like every button argument;
	// empty asks for the amounts to choose from.
	Amount string `json:"amount,omitempty"`
	// Confirm is village.ResidenceConfirm on the second press.
	Confirm string `json:"confirm,omitempty"`
	// Settlement names the village for a game client; in a group it is
	// ignored (the group's own village is used).
	Settlement string `json:"settlement,omitempty"`
	// LocalSettle asks to convert at the desk and pay in the village's own money (convert, max_sup).
	LocalSettle
}

func (r VillageDonateRequest) confirmed() bool { return r.Confirm == village.ResidenceConfirm }

// WithDonationRules sets the donation window and the amounts the buttons
// offer (config settlement.donation_*).
func (h *VillageHandler) WithDonationRules(min, max int64, presets []int64) *VillageHandler {
	h.donationMin, h.donationMax = min, max
	h.donationPresets = append([]int64(nil), presets...)
	return h
}

// Donate handles settlement.donate: a resident gives from their own cash to
// their village's treasury. Three steps, like moving home: the amounts, the
// confirm, the gift. The gift is one ledger transaction (the resident's cash
// to the village treasury, reason settlement_donation) recorded in
// settlement_donations, and one settlement.donated event for the village
// news; a redelivered confirm repeats none of it.
func (h *VillageHandler) Donate(ctx context.Context, meta envelope.Metadata, req VillageDonateRequest) (*presentation.Response, error) {
	lang := meta.Language
	var (
		view  village.DonateView
		step  string // "menu", "ask" or "done"
		offer *application.LocalOffer
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
		if err != nil {
			return err
		}
		var s application.FoundedSettlement
		switch {
		case meta.FromClient() && req.Settlement == "" && home != "":
			s, err = tx.Settlements().ByID(ctx, home)
		default:
			s, err = h.residenceTarget(ctx, tx, meta, VillageJoinRequest{Settlement: req.Settlement})
		}
		if err != nil {
			if meta.FromClient() && stderrors.Is(err, application.ErrCityNotFound) {
				return refuseVillage(village.VillageNotResident)
			}
			return err
		}
		if home != s.CityID {
			return refuseVillage(village.VillageNotResident)
		}
		view = village.DonateView{
			Village: s.Name, Presets: h.donationPresets, Min: h.donationMin, Max: h.donationMax, SettlementID: s.CityID,
		}
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
		if err != nil {
			return err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return err
		}
		readBalances := func() error {
			c, err := tx.Ledger().Balance(ctx, cash.ID)
			if err != nil {
				return err
			}
			t, err := tx.Ledger().Balance(ctx, treasury.ID)
			if err != nil {
				return err
			}
			view.Cash, view.Treasury = c.Minor(), t.Minor()
			return nil
		}

		text := strings.TrimSpace(req.Amount)
		if text == "" {
			step = "menu"
			return readBalances()
		}
		amount, perr := strconv.ParseInt(text, 10, 64)
		if perr != nil || amount < h.donationMin || amount > h.donationMax {
			r := refuseVillage(village.VillageDonateRange)
			r.min, r.max = h.donationMin, h.donationMax
			return r
		}
		view.Amount = amount
		if err := readBalances(); err != nil {
			return err
		}
		if !req.confirmed() {
			step = "ask"
			if offer, err = application.LocalOfferFor(ctx, tx, s.CityID, p.ID, amount, 0); err != nil {
				return err
			}
			if view.Cash < amount && (offer == nil || !offer.Local) {
				return refuseVillage(village.VillageDonateNoCash)
			}
			return nil
		}

		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		step = "done"
		if !fresh {
			return nil // a redelivered confirm: already given
		}
		now := h.now()
		donationID, txID := h.ids.NewID(), h.ids.NewID()
		if err := tx.SettlementTreasury().RecordDonation(ctx, application.SettlementDonation{
			ID: donationID, SettlementID: s.CityID, PlayerID: p.ID, Amount: amount, LedgerTransactionID: txID, CreatedAt: now,
		}); err != nil {
			return err
		}
		// a village with its own money is paid in it when the donor holds the units (or converts at the
		// desk inside this confirm); otherwise the gift is SUP, as before
		local, lerr := application.PayLocal(ctx, tx, h.ids.NewID, application.LocalPayment{
			SettlementID: s.CityID, PlayerID: p.ID, Direction: application.LocalCollect, Flow: application.ReasonSettlementDonation,
			SUP: amount, TxID: txID, RefType: application.SettlementDonationReference, RefID: donationID, At: now,
			Convert: req.wantsConvert(), MaxConvertSUP: req.maxSUP(),
		})
		if lerr != nil {
			if r, ok := deskRefusal(lerr); ok {
				return r
			}
			return lerr
		}
		if local.Paid {
			if err := appendVillageEvent(ctx, tx, meta, "donated", s.CityID, map[string]any{
				"settlement_id": s.CityID, "player_id": p.ID, "amount": amount, "donation_id": donationID,
			}); err != nil {
				return err
			}
			return readBalances()
		}
		if view.Cash < amount {
			return refuseVillage(village.VillageDonateNoCash)
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: txID, Reason: application.ReasonSettlementDonation, CreatedAt: now,
			ReferenceType: application.SettlementDonationReference, ReferenceID: donationID,
			Entries: []application.LedgerEntry{
				{AccountID: cash.ID, Amount: money.FromMinor(-amount)},
				{AccountID: treasury.ID, Amount: money.FromMinor(amount)},
			},
		}); err != nil {
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				return refuseVillage(village.VillageDonateNoCash)
			}
			return err
		}
		if err := appendVillageEvent(ctx, tx, meta, "donated", s.CityID, map[string]any{
			"settlement_id": s.CityID, "player_id": p.ID, "amount": amount, "donation_id": donationID,
		}); err != nil {
			return err
		}
		return readBalances()
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	c := h.screen(meta, lang)
	switch step {
	case "menu":
		return village.VillageDonateMenu(c, view), nil
	case "ask":
		return attachOffer(village.VillageDonateConfirm(c, view), offer, 3), nil
	}
	return village.VillageDonateDone(c, view), nil
}

package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/licence"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

func licenceEscrow(ctx context.Context, tx application.Tx, id string) (application.Account, error) {
	return tx.Ledger().AccountFor(ctx, application.AccountLicenceEscrow, id)
}

// licenceApply asks for a licence at the posted fee: the fee is held in the application's escrow until an official decides.
func (h *VillageHandler) licenceApply(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageLicenceRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	def, ok := snap.LicenceKind(strings.TrimSpace(req.Code))
	if !ok {
		return refuseVillage(village.LicenceNoKind, village.AddrLicence)
	}
	if def.Gate == "" {
		return refuseVillage(village.LicenceDeferred, village.AddrLicence)
	}
	rule, err := tx.Licences().Rule(ctx, s.CityID, def.Code)
	if err != nil {
		return err
	}
	if rule == nil {
		return refuseVillage(village.LicenceNoRule, village.AddrLicence)
	}
	if rule.Mode != licence.Fixed {
		return refuseVillage(village.LicenceNotFixed, village.AddrLicence)
	}
	hk, hid, err := h.holderOf(ctx, tx, p, req.Company)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(req.Target)
	if err := h.rightCheck(ctx, tx, def, s.CityID, target, hk, hid); err != nil {
		return err
	}
	from, err := holderAccount(ctx, tx, hk, hid)
	if err != nil {
		return err
	}
	if bal, err := accountBalance(ctx, tx, from); err != nil {
		return err
	} else if bal < rule.Fee {
		return refuseVillage(village.LicenceNoMoney, village.AddrLicence)
	}
	app := application.LicenceApplication{ID: h.ids.NewID(), IssuerID: s.CityID, Kind: def.Code, TargetRef: target, HolderKind: hk, HolderID: hid, AppliedBy: p.ID, Fee: rule.Fee,
		TermDays: rule.TermDays, Transferable: rule.Transferable, At: now}
	if ok, err := tx.Licences().InsertApplication(ctx, app); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.LicenceHave, village.AddrLicence)
	}
	if rule.Fee > 0 {
		escrow, err := licenceEscrow(ctx, tx, app.ID)
		if err != nil {
			return err
		}
		if _, err := move(ctx, tx, from.ID, escrow.ID, rule.Fee, application.ReasonLicenceApplicationFee, application.LicenceReference, app.ID, "", now); err != nil {
			return err
		}
	}
	return h.licenceAudit(ctx, tx, s.CityID, "", p.ID, "applied", map[string]any{"kind": def.Code, "application": app.ID}, now)
}

// returnApplicationFee gives the held fee of an application back to its holder.
func (h *VillageHandler) returnApplicationFee(ctx context.Context, tx application.Tx, a application.LicenceApplication, now time.Time) error {
	if a.Fee <= 0 {
		return nil
	}
	escrow, err := licenceEscrow(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	to, err := holderAccount(ctx, tx, a.HolderKind, a.HolderID)
	if err != nil {
		return err
	}
	_, err = move(ctx, tx, escrow.ID, to.ID, a.Fee, application.ReasonLicenceAuctionRefund, application.LicenceReference, a.ID, "", now)
	return err
}

// licenceIssue grants a licence on an application: only the holder of licence.issue, never to himself or to a company he stands behind.
func (h *VillageHandler) licenceIssue(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageLicenceRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.LicenceIssue); err != nil {
		return err
	}
	a, err := tx.Licences().ApplicationByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if a == nil || a.IssuerID != s.CityID || a.Status != "pending" {
		return refuseVillage(village.LicenceNoApp, village.AddrLicence)
	}
	def, ok := snap.LicenceKind(a.Kind)
	if !ok {
		return refuseVillage(village.LicenceNoKind, village.AddrLicence)
	}
	officials, err := h.licenceOfficials(ctx, tx, s, p.ID)
	if err != nil {
		return err
	}
	if bad, err := h.licenceConflict(ctx, tx, officials, a.HolderKind, a.HolderID); err != nil {
		return err
	} else if bad {
		return refuseVillage(village.LicenceConflict, village.AddrLicence)
	}
	if err := h.rightCheck(ctx, tx, def, s.CityID, a.TargetRef, a.HolderKind, a.HolderID); err != nil {
		return err
	}
	l, err := h.grantLicence(ctx, tx, h.ids.NewID(), a.Kind, s.CityID, a.HolderKind, a.HolderID, a.TargetRef, "fixed", a.Fee, a.Transferable, now, now.Add(time.Duration(a.TermDays)*24*time.Hour), "", now)
	if err != nil {
		return err
	}
	if ok, err := tx.Licences().DecideApplication(ctx, a.ID, "pending", "granted", p.ID, l.ID, now); err != nil || !ok {
		return err
	}
	if a.Fee > 0 {
		escrow, err := licenceEscrow(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		treasury, err := cityTreasury(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if _, err := move(ctx, tx, escrow.ID, treasury.ID, a.Fee, application.ReasonLicenceIssueFee, application.LicenceReference, l.ID, "", now); err != nil {
			return err
		}
	}
	return h.licenceAudit(ctx, tx, s.CityID, l.ID, p.ID, "granted", map[string]any{"kind": a.Kind, "via": "fixed", "application": a.ID}, now)
}

// licenceDecide refuses an application (to is "refused"): the held fee goes back.
func (h *VillageHandler) licenceDecide(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, req VillageLicenceRequest, to string, now time.Time) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.LicenceIssue); err != nil {
		return err
	}
	a, err := tx.Licences().ApplicationByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if a == nil || a.IssuerID != s.CityID || a.Status != "pending" {
		return refuseVillage(village.LicenceNoApp, village.AddrLicence)
	}
	if ok, err := tx.Licences().DecideApplication(ctx, a.ID, "pending", to, p.ID, "", now); err != nil || !ok {
		return err
	}
	if err := h.returnApplicationFee(ctx, tx, *a, now); err != nil {
		return err
	}
	return h.licenceAudit(ctx, tx, s.CityID, "", p.ID, "declined", map[string]any{"kind": a.Kind, "application": a.ID}, now)
}

func (h *VillageHandler) licenceWithdraw(ctx context.Context, tx application.Tx, p *application.Player, req VillageLicenceRequest, now time.Time) error {
	a, err := tx.Licences().ApplicationByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if a == nil || a.AppliedBy != p.ID || a.Status != "pending" {
		return refuseVillage(village.LicenceNoApp, village.AddrLicence)
	}
	if ok, err := tx.Licences().DecideApplication(ctx, a.ID, "pending", "withdrawn", p.ID, "", now); err != nil || !ok {
		return err
	}
	return h.returnApplicationFee(ctx, tx, *a, now)
}

// licenceAuction opens a sealed-bid auction of a right.
func (h *VillageHandler) licenceAuction(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageLicenceRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.LicenceAuction); err != nil {
		return err
	}
	def, ok := snap.LicenceKind(strings.TrimSpace(req.Code))
	if !ok {
		return refuseVillage(village.LicenceNoKind, village.AddrLicence)
	}
	if def.Gate == "" {
		return refuseVillage(village.LicenceDeferred, village.AddrLicence)
	}
	rule, err := tx.Licences().Rule(ctx, s.CityID, def.Code)
	if err != nil {
		return err
	}
	if rule == nil {
		return refuseVillage(village.LicenceNoRule, village.AddrLicence)
	}
	if rule.Mode != licence.Auction {
		return refuseVillage(village.LicenceNotAuction, village.AddrLicence)
	}
	if !requirementOf(*rule).InForce(now) {
		return refuseVillage(village.LicenceInGrace, village.AddrLicence)
	}
	rules := h.licenceRules()
	reserve := atoi64(req.Reserve)
	if reserve == 0 {
		reserve = rule.Reserve
	}
	if !rules.ReserveOK(reserve) {
		return refuseVillage(village.LicenceReserveLow, village.AddrLicence)
	}
	window := atoi64(req.Window)
	if window == 0 {
		window = int64(rules.AuctionMin / time.Hour)
	}
	closes := now.Add(time.Duration(window) * time.Hour)
	if !rules.WindowOK(now, closes) {
		return refuseVillage(village.LicenceShort, village.AddrLicence)
	}
	target := strings.TrimSpace(req.Target)
	if def.Exclusive() {
		if target == "" || len(target) > 64 {
			return refuseVillage(village.LicenceNoTarget, village.AddrLicence)
		}
		if held, err := tx.Licences().Held(ctx, s.CityID, def.Code, target); err != nil {
			return err
		} else if held != nil {
			return refuseVillage(village.LicenceTaken, village.AddrLicence)
		}
	} else {
		target = ""
	}
	a := application.LicenceAuction{ID: h.ids.NewID(), IssuerID: s.CityID, Kind: def.Code, TargetRef: target, PostedBy: p.ID, Reserve: reserve, TermDays: rule.TermDays,
		Transferable: rule.Transferable, OpensAt: now, ClosesAt: closes}
	if ok, err := tx.Licences().InsertAuction(ctx, a); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.LicenceHave, village.AddrLicence)
	}
	if err := h.schedulePolity(ctx, tx, application.TickLicenceAuction, application.LicenceReference, a.ID, "", p.ID, now, closes); err != nil {
		return err
	}
	return h.licenceAudit(ctx, tx, s.CityID, "", p.ID, "auction_opened", map[string]any{"kind": def.Code, "auction": a.ID, "reserve": reserve, "closes_at": closes}, now)
}

// licenceBid places a sealed bid: held in the auction's escrow, one bid for each bidder, the amount of others never shown.
func (h *VillageHandler) licenceBid(ctx context.Context, tx application.Tx, p *application.Player, req VillageLicenceRequest, now time.Time) error {
	a, err := tx.Licences().AuctionByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if a == nil {
		return refuseVillage(village.LicenceNoAuction, village.AddrLicence)
	}
	if a.Status != "open" || !now.Before(a.ClosesAt) {
		return refuseVillage(village.LicenceClosed, village.AddrLicence)
	}
	amount := atoi64(req.Amount)
	if amount < a.Reserve {
		return refuseVillage(village.LicenceBelowReserve, village.AddrLicence)
	}
	hk, hid, err := h.holderOf(ctx, tx, p, req.Company)
	if err != nil {
		return err
	}
	s, err := tx.Settlements().ByID(ctx, a.IssuerID)
	if err != nil {
		return err
	}
	officials, err := h.licenceOfficials(ctx, tx, s, a.PostedBy)
	if err != nil {
		return err
	}
	if bad, err := h.licenceConflict(ctx, tx, officials, hk, hid); err != nil {
		return err
	} else if bad {
		return refuseVillage(village.LicenceConflict, village.AddrLicence)
	}
	from, err := holderAccount(ctx, tx, hk, hid)
	if err != nil {
		return err
	}
	if bal, err := accountBalance(ctx, tx, from); err != nil {
		return err
	} else if bal < amount {
		return refuseVillage(village.LicenceNoMoney, village.AddrLicence)
	}
	b := application.LicenceBid{ID: h.ids.NewID(), AuctionID: a.ID, BidderKind: hk, BidderID: hid, BidBy: p.ID, Amount: amount, At: now}
	if ok, err := tx.Licences().AddBid(ctx, b); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.LicenceHaveBid, village.AddrLicence)
	}
	escrow, err := licenceEscrow(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	_, err = move(ctx, tx, from.ID, escrow.ID, amount, application.ReasonLicenceAuctionEscrow, application.LicenceReference, a.ID, "", now)
	return err
}

var errAuctionClosedElsewhere = stderrors.New("handlers: the auction was closed by another replica")

// tickLicenceAuction closes the auction: the highest sealed bid at or above the reserve wins and pays what it bid; every other bid is returned in
// full. The award is one conditional update, so with any number of replicas exactly one closes it.
func (h *VillageHandler) tickLicenceAuction(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	a, err := tx.Licences().AuctionByID(ctx, id, true)
	if err != nil || a == nil || a.Status != "open" || now.Before(a.ClosesAt) {
		return err
	}
	bids, err := tx.Licences().Bids(ctx, a.ID)
	if err != nil {
		return err
	}
	var live []licence.Bid
	byBidder := map[string]application.LicenceBid{}
	for _, b := range bids {
		if b.Status != "standing" {
			continue
		}
		// a bidder that has since passed the share cap does not win
		if err := h.shareCheck(ctx, tx, a.IssuerID, a.Kind, b.BidderKind, b.BidderID); err != nil {
			continue
		}
		key := b.ID
		live = append(live, licence.Bid{ID: key, Bidder: b.BidderKind + ":" + b.BidderID, Amount: b.Amount, At: b.At})
		byBidder[key] = b
	}
	escrow, err := licenceEscrow(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	refund := func(b application.LicenceBid) error {
		to, err := holderAccount(ctx, tx, b.BidderKind, b.BidderID)
		if err != nil {
			return err
		}
		_, err = move(ctx, tx, escrow.ID, to.ID, b.Amount, application.ReasonLicenceAuctionRefund, application.LicenceReference, a.ID, "", now)
		return err
	}
	win, ok := licence.Winner(a.Reserve, live)
	if !ok {
		if moved, err := tx.Licences().SetAuctionStatus(ctx, a.ID, "open", "void", now); err != nil || !moved {
			return err
		}
		for _, b := range bids {
			if b.Status != "standing" {
				continue
			}
			if err := refund(b); err != nil {
				return err
			}
			if err := tx.Licences().SetBid(ctx, b.ID, "lost"); err != nil {
				return err
			}
		}
		return h.licenceAudit(ctx, tx, a.IssuerID, "", "", "auction_void", map[string]any{"auction": a.ID}, now)
	}
	wb := byBidder[win.ID]
	licenceID := h.ids.NewID()
	// the licence first (the auction names it), then the award: one conditional update under the auction's lock, so exactly one replica closes it
	l, err := h.grantLicence(ctx, tx, licenceID, a.Kind, a.IssuerID, wb.BidderKind, wb.BidderID, a.TargetRef, "auction", wb.Amount, a.Transferable, now, now.Add(time.Duration(a.TermDays)*24*time.Hour), "", now)
	if err != nil {
		return err
	}
	if moved, err := tx.Licences().AwardAuction(ctx, a.ID, wb.ID, licenceID, wb.Amount, now); err != nil {
		return err
	} else if !moved {
		return errAuctionClosedElsewhere // the transaction is rolled back with the licence in it
	}
	treasury, err := cityTreasury(ctx, tx, a.IssuerID)
	if err != nil {
		return err
	}
	if _, err := move(ctx, tx, escrow.ID, treasury.ID, wb.Amount, application.ReasonLicenceAuctionAward, application.LicenceReference, l.ID, "", now); err != nil {
		return err
	}
	for _, b := range bids {
		if b.Status != "standing" {
			continue
		}
		if b.ID == wb.ID {
			if err := tx.Licences().SetBid(ctx, b.ID, "won"); err != nil {
				return err
			}
			continue
		}
		if err := refund(b); err != nil {
			return err
		}
		if err := tx.Licences().SetBid(ctx, b.ID, "lost"); err != nil {
			return err
		}
	}
	return h.licenceAudit(ctx, tx, a.IssuerID, l.ID, "", "granted", map[string]any{"kind": a.Kind, "via": "auction", "auction": a.ID, "price": wb.Amount}, now)
}

// mine reports whether the player holds the licence, or owns the company that does.
func (h *VillageHandler) holds(ctx context.Context, tx application.Tx, p *application.Player, l *application.Licence) (bool, error) {
	if l.HolderKind == "player" {
		return l.HolderID == p.ID, nil
	}
	co, err := tx.Companies().ByID(ctx, l.HolderID)
	if err != nil || co == nil {
		return false, err
	}
	mine, err := ownsCompany(ctx, tx, p.ID, co.Code)
	return mine != nil, err
}

func (h *VillageHandler) licenceRenew(ctx context.Context, tx application.Tx, p *application.Player, req VillageLicenceRequest, now time.Time) error {
	l, err := tx.Licences().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if l == nil {
		return refuseVillage(village.LicenceNoLicence, village.AddrLicence)
	}
	if mine, err := h.holds(ctx, tx, p, l); err != nil {
		return err
	} else if !mine {
		return refuseVillage(village.LicenceNotHolder, village.AddrLicence)
	}
	if l.Status != "active" || !now.Before(l.TermEnd) {
		return refuseVillage(village.LicenceNotLive, village.AddrLicence)
	}
	rule, err := tx.Licences().Rule(ctx, l.IssuerID, l.Kind)
	if err != nil {
		return err
	}
	if rule == nil {
		return refuseVillage(village.LicenceNoRule, village.AddrLicence)
	}
	price := requirementOf(*rule).Price()
	from, err := holderAccount(ctx, tx, l.HolderKind, l.HolderID)
	if err != nil {
		return err
	}
	if bal, err := accountBalance(ctx, tx, from); err != nil {
		return err
	} else if bal < price {
		return refuseVillage(village.LicenceNoMoney, village.AddrLicence)
	}
	end := l.TermEnd.Add(time.Duration(rule.TermDays) * 24 * time.Hour)
	if max := now.Add(h.special.Licence.TermMax); end.After(max) {
		end = max
	}
	if !end.After(l.TermEnd) {
		return refuseVillage(village.LicenceTermBad, village.AddrLicence)
	}
	if price > 0 {
		treasury, err := cityTreasury(ctx, tx, l.IssuerID)
		if err != nil {
			return err
		}
		if _, err := move(ctx, tx, from.ID, treasury.ID, price, application.ReasonLicenceRenewal, application.LicenceReference, l.ID, "", now); err != nil {
			return err
		}
	}
	if ok, err := tx.Licences().Renew(ctx, l.ID, end, price); err != nil || !ok {
		return err
	}
	if err := h.schedulePolity(ctx, tx, application.TickLicenceExpiry, application.LicenceReference, l.ID, "", l.HolderID, now, end); err != nil {
		return err
	}
	return h.licenceAudit(ctx, tx, l.IssuerID, l.ID, p.ID, "renewed", map[string]any{"price": price, "term_end": end}, now)
}

// licenceList puts a transferable licence on the exchange board; the listing fee goes to the sink.
func (h *VillageHandler) licenceList(ctx context.Context, tx application.Tx, p *application.Player, req VillageLicenceRequest, now time.Time) error {
	l, err := tx.Licences().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if l == nil {
		return refuseVillage(village.LicenceNoLicence, village.AddrLicence)
	}
	if mine, err := h.holds(ctx, tx, p, l); err != nil {
		return err
	} else if !mine {
		return refuseVillage(village.LicenceNotHolder, village.AddrLicence)
	}
	if l.Status != "active" || !now.Before(l.TermEnd) {
		return refuseVillage(village.LicenceNotLive, village.AddrLicence)
	}
	if !l.Transferable {
		return refuseVillage(village.LicenceNotMovable, village.AddrLicence)
	}
	price := atoi64(req.Price)
	if price < 1 {
		return refuseVillage(village.LicenceBadArgs, village.AddrLicence)
	}
	if !h.licenceRules().TransferOpen(l.CreatedAt, now) {
		return refuseVillage(village.LicenceCooling, village.AddrLicence)
	}
	if fee := h.special.Licence.ListingFee; fee > 0 {
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
		if err != nil {
			return err
		}
		if bal, err := accountBalance(ctx, tx, cash); err != nil {
			return err
		} else if bal < fee {
			return refuseVillage(village.LicenceNoMoney, village.AddrLicence)
		}
		if _, err := move(ctx, tx, cash.ID, application.SystemSinkAccountID, fee, application.ReasonLicenceListingFee, application.LicenceReference, l.ID, "", now); err != nil {
			return err
		}
	}
	if ok, err := tx.Licences().InsertListing(ctx, application.LicenceListing{ID: h.ids.NewID(), LicenceID: l.ID, SellerID: p.ID, Price: price, At: now}); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.LicenceHave, village.AddrLicence)
	}
	return h.licenceAudit(ctx, tx, l.IssuerID, l.ID, p.ID, "listed", map[string]any{"price": price}, now)
}

func (h *VillageHandler) licenceDelist(ctx context.Context, tx application.Tx, p *application.Player, req VillageLicenceRequest, now time.Time) error {
	ls, err := tx.Licences().ListingByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if ls == nil || ls.SellerID != p.ID || ls.Status != "open" {
		return refuseVillage(village.LicenceNoListing, village.AddrLicence)
	}
	_, err = tx.Licences().CloseListing(ctx, ls.ID, "cancelled", now)
	return err
}

// licenceBuy moves a listed licence to the buyer: the price goes to the seller less the issuer's share, the old licence ends as transferred and
// the buyer's runs to the same end of term. One transfer for one listing (the listing is the fence).
func (h *VillageHandler) licenceBuy(ctx context.Context, tx application.Tx, p *application.Player, req VillageLicenceRequest, now time.Time) error {
	ls, err := tx.Licences().ListingByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if ls == nil || ls.Status != "open" {
		return refuseVillage(village.LicenceNoListing, village.AddrLicence)
	}
	if ls.SellerID == p.ID {
		return refuseVillage(village.LicenceOwnListing, village.AddrLicence)
	}
	l, err := tx.Licences().ByID(ctx, ls.LicenceID, true)
	if err != nil {
		return err
	}
	if l == nil || l.Status != "active" || !now.Before(l.TermEnd) || !l.Transferable {
		return refuseVillage(village.LicenceNotLive, village.AddrLicence)
	}
	if !h.licenceRules().TransferOpen(l.CreatedAt, now) {
		return refuseVillage(village.LicenceCooling, village.AddrLicence)
	}
	hk, hid, err := h.holderOf(ctx, tx, p, req.Company)
	if err != nil {
		return err
	}
	if hk == l.HolderKind && hid == l.HolderID {
		return refuseVillage(village.LicenceSameHolder, village.AddrLicence)
	}
	if err := h.shareCheck(ctx, tx, l.IssuerID, l.Kind, hk, hid); err != nil {
		return err
	}
	s, err := tx.Settlements().ByID(ctx, l.IssuerID)
	if err != nil {
		return err
	}
	officials, err := h.licenceOfficials(ctx, tx, s, "")
	if err != nil {
		return err
	}
	if bad, err := h.licenceConflict(ctx, tx, officials, hk, hid); err != nil {
		return err
	} else if bad {
		return refuseVillage(village.LicenceConflict, village.AddrLicence)
	}
	from, err := holderAccount(ctx, tx, hk, hid)
	if err != nil {
		return err
	}
	if bal, err := accountBalance(ctx, tx, from); err != nil {
		return err
	} else if bal < ls.Price {
		return refuseVillage(village.LicenceNoMoney, village.AddrLicence)
	}
	if ok, err := tx.Licences().CloseListing(ctx, ls.ID, "sold", now); err != nil || !ok {
		return err
	}
	if ok, err := tx.Licences().Transferred(ctx, l.ID, now); err != nil || !ok {
		return err
	}
	nl, err := h.grantLicence(ctx, tx, h.ids.NewID(), l.Kind, l.IssuerID, hk, hid, l.TargetRef, "transfer", ls.Price, l.Transferable, l.TermStart, l.TermEnd, l.ID, now)
	if err != nil {
		return err
	}
	fee, sellerPart := h.licenceRules().TransferFee(ls.Price)
	seller, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, ls.SellerID)
	if err != nil {
		return err
	}
	if sellerPart > 0 {
		if _, err := move(ctx, tx, from.ID, seller.ID, sellerPart, application.ReasonLicenceTransfer, application.LicenceReference, nl.ID, "", now); err != nil {
			return err
		}
	}
	if fee > 0 {
		treasury, err := cityTreasury(ctx, tx, l.IssuerID)
		if err != nil {
			return err
		}
		if _, err := move(ctx, tx, from.ID, treasury.ID, fee, application.ReasonLicenceTransferFee, application.LicenceReference, nl.ID, "", now); err != nil {
			return err
		}
	}
	return h.licenceAudit(ctx, tx, l.IssuerID, nl.ID, p.ID, "transferred", map[string]any{"from": l.ID, "price": ls.Price, "fee": fee}, now)
}

// licenceRevoke ends a licence. For cause (non-use, which the engine has recorded) it serves notice and the licence ends when the notice does; without
// cause it needs a second holder of licence.revoke to confirm, and refunds the unused part of what was paid.
func (h *VillageHandler) licenceRevoke(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, req VillageLicenceRequest, now time.Time) (string, error) {
	if !has {
		return "", refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.LicenceRevoke); err != nil {
		return "", err
	}
	l, err := tx.Licences().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return "", err
	}
	if l == nil || l.IssuerID != s.CityID {
		return "", refuseVillage(village.LicenceNoLicence, village.AddrLicence)
	}
	if l.Status != "active" {
		return "", refuseVillage(village.LicenceNotLive, village.AddrLicence)
	}
	rules := h.licenceRules()
	cause := strings.TrimSpace(req.Cause)
	if cause != "" {
		// only a cause the engine records can end a licence: a licence not used for the whole non-use time
		if cause != licence.CauseNonUse {
			return "", refuseVillage(village.LicenceBadCause, village.AddrLicence)
		}
		if !rules.Unused(l.TermStart, l.LastUsedAt, now) {
			return "", refuseVillage(village.LicenceBadCause, village.AddrLicence)
		}
		until := rules.NoticeEnds(now)
		if ok, err := tx.Licences().Notice(ctx, l.ID, cause, until); err != nil || !ok {
			return "", err
		}
		if err := h.schedulePolity(ctx, tx, application.TickLicenceNotice, application.LicenceReference, l.ID, "", p.ID, now, until); err != nil {
			return "", err
		}
		return village.LicenceNoticeNotice, h.licenceAudit(ctx, tx, s.CityID, l.ID, p.ID, "notice", map[string]any{"cause": cause, "until": until}, now)
	}
	holders, err := permissionHolders(ctx, tx, s, charter.LicenceRevoke)
	if err != nil {
		return "", err
	}
	if len(holders) < 2 {
		return "", refuseVillage(village.LicenceNeedSecond, village.AddrLicence)
	}
	if l.RevokeProposedBy == "" {
		if ok, err := tx.Licences().ProposeRevoke(ctx, l.ID, p.ID); err != nil || !ok {
			return "", err
		}
		return village.LicenceNoticeProposed, h.licenceAudit(ctx, tx, s.CityID, l.ID, p.ID, "revocation_proposed", nil, now)
	}
	if l.RevokeProposedBy == p.ID {
		return "", refuseVillage(village.LicenceNeedSecond, village.AddrLicence)
	}
	refund := licence.Refund(l.Paid, l.TermStart, l.TermEnd, now)
	if refund > 0 {
		treasury, err := cityTreasury(ctx, tx, s.CityID)
		if err != nil {
			return "", err
		}
		if bal, err := accountBalance(ctx, tx, treasury); err != nil {
			return "", err
		} else if bal < refund {
			return "", refuseVillage(village.LicenceNoMoney, village.AddrLicence)
		}
	}
	if ok, err := tx.Licences().Revoke(ctx, l.ID, licence.CauseWithdrawn, now); err != nil || !ok {
		return "", err
	}
	if refund > 0 {
		treasury, _ := cityTreasury(ctx, tx, s.CityID)
		to, err := holderAccount(ctx, tx, l.HolderKind, l.HolderID)
		if err != nil {
			return "", err
		}
		if _, err := move(ctx, tx, treasury.ID, to.ID, refund, application.ReasonLicenceRevokeRefund, application.LicenceReference, l.ID, "", now); err != nil {
			return "", err
		}
	}
	return village.LicenceNoticeRevoked, h.licenceAudit(ctx, tx, s.CityID, l.ID, p.ID, "revoked", map[string]any{"cause": licence.CauseWithdrawn, "refund": refund}, now)
}

// tickLicenceNotice ends a licence whose notice has run out.
func (h *VillageHandler) tickLicenceNotice(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	l, err := tx.Licences().ByID(ctx, id, true)
	if err != nil || l == nil || l.Status != "notice" || l.NoticeUntil == nil || now.Before(*l.NoticeUntil) {
		return err
	}
	if ok, err := tx.Licences().Revoke(ctx, l.ID, l.Cause, now); err != nil || !ok {
		return err
	}
	return h.licenceAudit(ctx, tx, l.IssuerID, l.ID, "", "revoked", map[string]any{"cause": l.Cause}, now)
}

// tickLicenceExpiry ends a licence whose term is over (a renewed licence has a later end and is left alone).
func (h *VillageHandler) tickLicenceExpiry(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	l, err := tx.Licences().ByID(ctx, id, true)
	if err != nil || l == nil || !l.Live() || now.Before(l.TermEnd) {
		return err
	}
	if ok, err := tx.Licences().Expire(ctx, l.ID, now); err != nil || !ok {
		return err
	}
	return h.licenceAudit(ctx, tx, l.IssuerID, l.ID, "", "expired", nil, now)
}

// licenceAllowed asks whether an action the settlement licenses may be done by one of the holders: before the market opens, with no requirement,
// during its grace or after it is closed, nothing is asked. A licence found is marked used (it feeds the non-use rule).
func licenceAllowed(ctx context.Context, tx application.Tx, openAt, now time.Time, issuerID, kind, target string, holders ...[2]string) (bool, error) {
	if openAt.IsZero() || now.Before(openAt) {
		return true, nil
	}
	rule, err := tx.Licences().Rule(ctx, issuerID, kind)
	if err != nil || rule == nil {
		return true, err
	}
	if !requirementOf(*rule).InForce(now) {
		return true, nil
	}
	for _, hd := range holders {
		live, err := tx.Licences().LiveOfHolder(ctx, hd[0], hd[1])
		if err != nil {
			return false, err
		}
		for _, l := range live {
			if l.IssuerID == issuerID && l.Kind == kind && (target == "" || l.TargetRef == target || l.TargetRef == "") && now.Before(l.TermEnd) {
				return true, tx.Licences().Touch(ctx, l.ID, now)
			}
		}
	}
	return false, nil
}

// holdersOfPlayer are the licence holders a player acts for: themselves and the companies they own.
func holdersOfPlayer(ctx context.Context, tx application.Tx, playerID string) ([][2]string, error) {
	out := [][2]string{{"player", playerID}}
	cs, err := tx.Companies().Of(ctx, playerID)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		out = append(out, [2]string{"company", c.ID})
	}
	return out, nil
}

// licenceGate is licenceAllowed as a refusal, for the village handlers.
func (h *VillageHandler) licenceGate(ctx context.Context, tx application.Tx, p *application.Player, issuerID, kind, target string) error {
	holders, err := holdersOfPlayer(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	ok, err := licenceAllowed(ctx, tx, h.special.LicenceAt, h.now(), issuerID, kind, target, holders...)
	if err != nil {
		return err
	}
	if !ok {
		return refuseVillage(village.LicenceMissing, village.AddrLicence)
	}
	return nil
}

// worksClassGate asks whether a company may bid on public works of a settlement: when the settlement requires the contractor class (docs/adr/0077
// 7.2), the company holds a live licence of the settlement. Before the market opens, with no requirement or in its grace, nothing is asked.
func (h *VillageHandler) worksClassGate(ctx context.Context, tx application.Tx, settlementID, companyID string, now time.Time) error {
	if settlementID == "" {
		return nil
	}
	ok, err := licenceAllowed(ctx, tx, h.special.LicenceAt, now, settlementID, "works_class", "", [2]string{"company", companyID})
	if err != nil {
		return err
	}
	if !ok {
		return refuseVillage(village.LicenceMissing, village.AddrLicence)
	}
	return nil
}

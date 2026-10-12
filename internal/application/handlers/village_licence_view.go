package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// licenceView builds the licence screen: the kinds and the charter's requirements, the viewer's licences, the public register, the auctions, the
// applications waiting for a decision, the exchange board and the audit. The bids of others in an open auction are sealed: only the viewer's own
// is shown.
func (h *VillageHandler) licenceView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player) (village.LicenceView, error) {
	v := village.LicenceView{Open: h.licenceOpen(), TransferFeeBPS: h.special.Licence.TransferFeeBPS, ListingFee: h.special.Licence.ListingFee}
	if has {
		v.Village = s.Name
	}
	if !v.Open || !has {
		return v, nil
	}
	now := h.now()
	v.MaySetKind = hasPermission(ctx, tx, s, p.ID, charter.LicenceSetKind)
	v.MayIssue = hasPermission(ctx, tx, s, p.ID, charter.LicenceIssue)
	v.MayAuction = hasPermission(ctx, tx, s, p.ID, charter.LicenceAuction)
	v.MayRevoke = hasPermission(ctx, tx, s, p.ID, charter.LicenceRevoke)
	rules, err := tx.Licences().Rules(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	ruleOf := map[string]application.LicenceRule{}
	for _, r := range rules {
		ruleOf[r.Kind] = r
	}
	for _, d := range snap.LicenceKinds() {
		missing, err := knowledgeMissing(ctx, tx, s.CityID, needsKnowledge(d.Requires))
		if err != nil {
			return v, err
		}
		line := village.LicenceKindLine{Code: d.Code, Name: d.Name, Target: d.Target, TargetName: d.TargetName, Options: h.licenceOptions(snap, d), Gate: d.Gate, Duty: d.Duty, Available: len(missing) == 0, Missing: missing, Deferred: d.Gate == ""}
		if r, ok := ruleOf[d.Code]; ok {
			at := r.EffectiveAt
			line.Required, line.Mode, line.Fee, line.Reserve, line.TermDays, line.Transferable, line.EffectiveAt = true, r.Mode, r.Fee, r.Reserve, r.TermDays, r.Transferable, &at
			line.InGrace = !requirementOf(r).InForce(now)
		}
		v.Kinds = append(v.Kinds, line)
	}
	mine, err := tx.Licences().LiveOfHolder(ctx, "player", p.ID)
	if err != nil {
		return v, err
	}
	companies, err := tx.Companies().Of(ctx, p.ID)
	if err != nil {
		return v, err
	}
	for _, co := range companies {
		v.Companies = append(v.Companies, presentation.Named{Code: co.Code, Name: co.Name})
		cl, err := tx.Licences().LiveOfHolder(ctx, "company", co.ID)
		if err != nil {
			return v, err
		}
		mine = append(mine, cl...)
	}
	for _, l := range mine {
		line, err := h.licenceLine(ctx, tx, l, p, true)
		if err != nil {
			return v, err
		}
		v.Mine = append(v.Mine, line)
	}
	reg, err := tx.Licences().OfIssuer(ctx, s.CityID, 50)
	if err != nil {
		return v, err
	}
	for _, l := range reg {
		line, err := h.licenceLine(ctx, tx, l, p, false)
		if err != nil {
			return v, err
		}
		v.Register = append(v.Register, line)
	}
	auctions, err := tx.Licences().AuctionsOf(ctx, s.CityID, 20)
	if err != nil {
		return v, err
	}
	for _, a := range auctions {
		line := village.LicenceAuctionLine{ID: a.ID, Kind: a.Kind, Target: a.TargetRef, Reserve: a.Reserve, Status: a.Status, ClosesAt: a.ClosesAt, Price: a.Price, MayBid: a.Status == "open" && now.Before(a.ClosesAt)}
		bids, err := tx.Licences().Bids(ctx, a.ID)
		if err != nil {
			return v, err
		}
		line.Bids = len(bids)
		for _, b := range bids {
			if b.BidBy == p.ID {
				cp := village.LicenceBidLine{ID: b.ID, Amount: b.Amount, Status: b.Status}
				line.MyBid = &cp
			}
		}
		v.Auctions = append(v.Auctions, line)
	}
	apps, err := tx.Licences().PendingApplications(ctx, s.CityID, 30)
	if err != nil {
		return v, err
	}
	for _, a := range apps {
		hn := h.holderNamed(ctx, tx, a.HolderKind, a.HolderID)
		v.Applications = append(v.Applications, village.LicenceApplicationLine{ID: a.ID, Kind: a.Kind, Target: a.TargetRef, Holder: hn, HolderKind: a.HolderKind, Fee: a.Fee, At: a.At,
			Mine: a.AppliedBy == p.ID, MayDecide: v.MayIssue})
	}
	listings, err := tx.Licences().OpenListings(ctx, s.CityID, 30)
	if err != nil {
		return v, err
	}
	for _, ls := range listings {
		l, err := tx.Licences().ByID(ctx, ls.LicenceID, false)
		if err != nil || l == nil {
			continue
		}
		ll, err := h.licenceLine(ctx, tx, *l, p, false)
		if err != nil {
			return v, err
		}
		fee, _ := h.licenceRules().TransferFee(ls.Price)
		v.Board = append(v.Board, village.LicenceListingLine{ID: ls.ID, Licence: ll, Price: ls.Price, Fee: fee, Mine: ls.SellerID == p.ID, MayBuy: ls.SellerID != p.ID})
	}
	events, err := tx.Licences().Events(ctx, s.CityID, 20)
	if err != nil {
		return v, err
	}
	for _, e := range events {
		line := village.LicenceEventLine{Kind: e.Kind, At: e.At}
		if e.ActorID != "" {
			line.Actor = presentation.Named{Code: e.ActorID, Name: h.playerName(ctx, tx, e.ActorID)}
		}
		if e.LicenceID != "" {
			if l, err := tx.Licences().ByID(ctx, e.LicenceID, false); err == nil && l != nil {
				line.Licence = l.No
			}
		}
		v.Events = append(v.Events, line)
	}
	return v, nil
}

func (h *VillageHandler) holderNamed(ctx context.Context, tx application.Tx, kind, id string) presentation.Named {
	if kind == "company" {
		if co, err := tx.Companies().ByID(ctx, id); err == nil && co != nil {
			return presentation.Named{Code: co.Code, Name: co.Name}
		}
		return presentation.Named{}
	}
	return presentation.Named{Code: id, Name: h.playerName(ctx, tx, id)}
}

func (h *VillageHandler) licenceLine(ctx context.Context, tx application.Tx, l application.Licence, viewer *application.Player, mineList bool) (village.LicenceLine, error) {
	line := village.LicenceLine{ID: l.ID, No: l.No, Kind: l.Kind, Holder: h.holderNamed(ctx, tx, l.HolderKind, l.HolderID), HolderKind: l.HolderKind, Target: l.TargetRef, Via: l.Via,
		Status: l.Status, Paid: l.Paid, TermEnd: l.TermEnd, NoticeUntil: l.NoticeUntil, Cause: l.Cause, Proposed: l.RevokeProposedBy != ""}
	mine, err := h.holds(ctx, tx, viewer, &l)
	if err != nil {
		return line, err
	}
	line.Mine = mine
	now := h.now()
	line.MayRenew = mine && l.Status == "active" && now.Before(l.TermEnd)
	line.MayList = mine && l.Status == "active" && l.Transferable && now.Before(l.TermEnd) && h.licenceRules().TransferOpen(l.CreatedAt, now)
	line.MayRevoke = l.Status == "active" && !mineList
	return line, nil
}

// licenceOptions are the things an exclusive right may be over, to pick from: the crafts for a craft right. A deposit or a road has no
// catalogue to pick from yet (those kinds wait for their reader), so the list is empty.
func (h *VillageHandler) licenceOptions(snap *content.Snapshot, d content.LicenceKindDef) []presentation.Named {
	if d.Target != "craft" {
		return nil
	}
	cd, ok := snap.Crafting()
	if !ok {
		return nil
	}
	names := map[string]string{}
	for _, sk := range snap.Skills() {
		names[sk.Code] = sk.Name
	}
	var out []presentation.Named
	for _, c := range cd.Apprenticeship.Crafts {
		out = append(out, presentation.Named{Code: c.Craft, Name: names[c.Craft]})
	}
	return out
}

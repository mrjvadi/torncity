package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/tender"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Public works by tender (docs/adr/0076 10). A government that has put a building on its own land out to build (the site is going up by
// work) may offer the work to the companies that do public works: they bid a price, a time and a bond; the rule picks one; the winner's crew
// builds the site and is paid by the company, and the government pays the price from an escrow in three stages as the work is done, less a
// retention held for the defects period.
//
//   - WHO WORKS THERE. The contractor's crew: the labourers of the settlement's market, hired on the site's job with the company as employer.
//   - WHAT IT CONSUMES. The building's materials and money (already taken when it was placed), the crew's wages (the company's treasury), the
//     bond (the company's money in the escrow), the filing fee (a sink).
//   - WHAT IT PROVIDES. A finished public building, and the contractor's pay and record.
//   - WHAT BREAKS. A contractor with no money for wages stops the site; a missed deadline forfeits its bond and debars it.

// VillageTenderRequest is the payload of settlement.tender.
type VillageTenderRequest struct {
	// Action is "" (the screen) or one of village.TenderAction*.
	Action string `json:"action,omitempty"`
	// ID names a tender (or a bid, for a withdrawal); Building the site being put out (post); Union a union that pays instead of the settlement.
	ID       string `json:"id,omitempty"`
	Building string `json:"building,omitempty"`
	Union    string `json:"union,omitempty"`
	// Rule, Cap, Due and Window are a tender: lowest_price or scored, the budget cap, the days the work may take, the hours bids are taken.
	Rule   string `json:"rule,omitempty"`
	Cap    string `json:"cap,omitempty"`
	Due    string `json:"due,omitempty"`
	Window string `json:"window,omitempty"`
	// Company is a company's public code (bid); Price and Days its offer.
	Company string `json:"company,omitempty"`
	Price   string `json:"price,omitempty"`
	Days    string `json:"days,omitempty"`
}

// Tender handles settlement.tender.
func (h *VillageHandler) Tender(ctx context.Context, meta envelope.Metadata, req VillageTenderRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.TenderView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, has, err := h.settlementOpt(ctx, tx, meta)
		if err != nil {
			return err
		}
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.tenderAct(ctx, tx, meta, s, has, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.tenderView(ctx, tx, s, has, p)
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Tender(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) tenderAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, act string, req VillageTenderRequest) (string, error) {
	if !h.polityOpen() {
		return "", refuseVillage(village.TenderNotYet, village.AddrTender)
	}
	now := h.now()
	switch act {
	case village.TenderActionPost:
		return village.TenderNoticePosted, h.tenderPost(ctx, tx, meta, s, has, p, req, now)
	case village.TenderActionBid:
		return village.TenderNoticeBid, h.tenderBid(ctx, tx, meta, p, req, now)
	case village.TenderActionWithdraw:
		return village.TenderNoticeWithdrawn, h.tenderWithdraw(ctx, tx, meta, p, req, now)
	case village.TenderActionCancel:
		return village.TenderNoticeCancelled, h.tenderCancel(ctx, tx, meta, s, has, p, req, now)
	case village.TenderActionAward:
		return h.tenderAwardAct(ctx, tx, meta, req, now)
	case village.TenderActionSettle:
		t, err := tx.Tenders().ByID(ctx, strings.TrimSpace(req.ID), true)
		if err != nil || t == nil {
			return "", refuseVillage(village.TenderNoTender, village.AddrTender)
		}
		return village.TenderNoticeStage, h.tenderSettleOf(ctx, tx, meta, t, now)
	case village.TenderActionAccept: // a defect is raised by the holder of works.accept within the defects period
		return village.TenderNoticeStage, h.tenderDefect(ctx, tx, meta, s, has, p, req, now)
	}
	return "", refuseVillage(village.TenderBadArgs, village.AddrTender)
}

// payer is who funds a tender: the settlement's treasury or a union's purse.
func (h *VillageHandler) tenderPayer(ctx context.Context, tx application.Tx, t application.Tender) (application.Account, error) {
	if t.PayerKind == "union" {
		u, err := tx.Unions().ByID(ctx, t.PayerID, false)
		if err != nil || u == nil {
			return application.Account{}, refuseVillage(village.UnionNoUnion, village.AddrTender)
		}
		return unionPurse(ctx, tx, u)
	}
	return tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, t.PayerID)
}

// tenderOfficials are the people who award a tender: the one who posted it and every holder of the posting power in the payer's charter.
func (h *VillageHandler) tenderOfficials(ctx context.Context, tx application.Tx, t application.Tender) ([]string, error) {
	out := []string{t.PostedBy}
	if t.PayerKind == "union" {
		hs, err := tx.Unions().HoldersOf(ctx, t.PayerID, charter.WorksTender)
		if err != nil {
			return nil, err
		}
		return append(out, hs...), nil
	}
	s, err := tx.Settlements().ByID(ctx, t.PayerID)
	if err != nil {
		return nil, err
	}
	st, err := loadCharterState(ctx, tx, s, "head")
	if err != nil {
		return nil, err
	}
	for _, o := range st.offices {
		if o.Closed {
			continue
		}
		has := false
		for _, g := range o.Grants {
			has = has || g.Permission == charter.WorksTender
		}
		if !has {
			continue
		}
		if o.Acquisition == charter.AcquireHead {
			if st.headHeld != "" {
				out = append(out, st.headHeld)
			}
			continue
		}
		for _, seat := range st.seats[o.ID] {
			out = append(out, seat.HolderID)
		}
	}
	return out, nil
}

func (h *VillageHandler) companyStake(ctx context.Context, tx application.Tx, c *application.Company) (tender.Company, error) {
	tc := tender.Company{Owner: c.OwnerID, Stakes: map[string]int64{}}
	if c.ManagerID != "" {
		tc.Managers = append(tc.Managers, c.ManagerID)
	}
	holders, err := tx.Companies().Shareholders(ctx, c.ID)
	if err != nil {
		return tc, err
	}
	for _, sh := range holders {
		if c.TotalShares > 0 {
			tc.Stakes[sh.PlayerID] += sh.Shares * tender.BPS / c.TotalShares
		}
	}
	return tc, nil
}

func (h *VillageHandler) tenderPost(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, req VillageTenderRequest, now time.Time) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.Building))
	if err != nil || b == nil {
		return refuseVillage(village.TenderNoBuilding, village.AddrTender)
	}
	payerKind, payerID := "settlement", s.CityID
	var limit int64
	if u := strings.TrimSpace(req.Union); u != "" {
		un, err := tx.Unions().ByID(ctx, u, false)
		if err != nil || un == nil || un.Status != "active" {
			return refuseVillage(village.UnionNoUnion, village.AddrTender)
		}
		if limit, err = h.requireUnionPower(ctx, tx, un, p.ID, charter.WorksTender); err != nil {
			return err
		}
		if m, err := tx.Unions().MemberOf(ctx, un.ID, b.SettlementID); err != nil || m == nil || !m.Active() {
			return refuseVillage(village.TenderOtherUnion, village.AddrTender)
		}
		payerKind, payerID = "union", un.ID
	} else {
		if b.SettlementID != s.CityID {
			return refuseVillage(village.TenderNoBuilding, village.AddrTender)
		}
		if limit, err = requirePermission(ctx, tx, s, p.ID, charter.WorksTender); err != nil {
			return err
		}
	}
	if b.Status != "building" || !b.ByWork() {
		return refuseVillage(village.TenderNotSite, village.AddrTender)
	}
	job, err := tx.SettlementTreasury().JobOfBuildingKind(ctx, b.ID, application.LaborKindConstruction)
	if err != nil {
		return err
	}
	if job == nil || job.EmployerKind != application.LaborEmployerSettlement {
		return refuseVillage(village.TenderNotSite, village.AddrTender) // a citizen's own site is his, not the treasury's
	}
	if have, err := tx.Tenders().ByBuilding(ctx, b.ID); err != nil {
		return err
	} else if have != nil {
		return refuseVillage(village.TenderHave, village.AddrTender)
	}
	rule := tender.Rule(strings.ToLower(strings.TrimSpace(req.Rule)))
	if rule == "" {
		rule = tender.LowestPrice
	}
	capv, due, window := atoi64(req.Cap), atoi64(req.Due), atoi64(req.Window)
	if window == 0 {
		window = int64(h.polity.TenderMin / time.Hour)
	}
	if !rule.Valid() || capv < 0 || due < 1 || due > 365 || time.Duration(window)*time.Hour < h.polity.TenderMin {
		return refuseVillage(village.TenderBadArgs, village.AddrTender)
	}
	if limit > 0 && (capv == 0 || capv > limit) {
		return refuseVillage(village.UnionNotHeld, village.AddrTender)
	}
	t := application.Tender{ID: h.ids.NewID(), PayerKind: payerKind, PayerID: payerID, SettlementID: b.SettlementID, BuildingID: b.ID, PostedBy: p.ID, BudgetCap: capv, Rule: string(rule),
		BondBPS: h.polity.BondBPS, RetentionBPS: h.polity.RetentionBPS, DefectsDays: int(h.polity.DefectsDays), DueDays: int(due), OpensAt: now,
		ClosesAt: now.Add(time.Duration(window) * time.Hour)}
	if fee := h.polity.TenderFee; fee > 0 {
		acct, err := h.tenderPayer(ctx, tx, t)
		if err != nil {
			return err
		}
		bal, err := accountBalance(ctx, tx, acct)
		if err != nil {
			return err
		}
		if bal < fee {
			return refuseVillage(village.TenderNoMoney, village.AddrTender)
		}
		if _, err := move(ctx, tx, acct.ID, application.SystemSinkAccountID, fee, application.ReasonTenderFee, application.TenderReference, t.ID, "", now); err != nil {
			return err
		}
	}
	if ok, err := tx.Tenders().Insert(ctx, t); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.TenderHave, village.AddrTender)
	}
	if err := h.schedulePolity(ctx, tx, application.TickTender, application.TenderReference, t.ID, "", p.ID, now, t.ClosesAt); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "tender_posted", b.SettlementID, map[string]any{"tender": t.ID, "building": b.ID, "payer_kind": payerKind, "cap": capv})
}

func (h *VillageHandler) tenderBid(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageTenderRequest, now time.Time) error {
	t, err := tx.Tenders().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || t == nil {
		return refuseVillage(village.TenderNoTender, village.AddrTender)
	}
	if t.Status != "open" || !now.Before(t.ClosesAt) {
		return refuseVillage(village.TenderClosed, village.AddrTender)
	}
	mine, err := tx.Companies().Of(ctx, p.ID)
	if err != nil {
		return err
	}
	var co *application.Company
	for i := range mine {
		if strings.EqualFold(mine[i].Code, strings.TrimSpace(req.Company)) {
			co = &mine[i]
		}
	}
	if co == nil {
		return refuseVillage(village.TenderNotCompany, village.AddrTender)
	}
	if co.TypeCode != "works_contractor" {
		return refuseVillage(village.TenderNoType, village.AddrTender)
	}
	until, err := tx.Tenders().DebarredUntil(ctx, co.ID)
	if err != nil {
		return err
	}
	if tender.Debarred(until, now) {
		return refuseVillage(village.TenderDebarred, village.AddrTender)
	}
	// the conflict of interest is refused when the bid is made, in the open (docs/adr/0076 10.4)
	officials, err := h.tenderOfficials(ctx, tx, *t)
	if err != nil {
		return err
	}
	stake, err := h.companyStake(ctx, tx, co)
	if err != nil {
		return err
	}
	if tender.Conflicts(officials, stake, h.polity.CoiRefuseFromBPS) {
		return refuseVillage(village.TenderConflict, village.AddrTender)
	}
	record, err := tx.Tenders().Record(ctx, co.ID)
	if err != nil {
		return err
	}
	price, days := atoi64(req.Price), atoi64(req.Days)
	bond := tender.BondFor(price, t.BondBPS)
	bid := tender.Bid{Company: co.ID, Price: price, Days: days, Record: int64(record), Bond: bond, At: now}
	if err := tender.Check(bid, t.BudgetCap, t.BondBPS); err != nil {
		return refuseVillage(village.TenderBadBid, village.AddrTender)
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, co.ID)
	if err != nil {
		return err
	}
	bal, err := accountBalance(ctx, tx, treasury)
	if err != nil {
		return err
	}
	if bal < bond {
		return refuseVillage(village.TenderNoMoney, village.AddrTender)
	}
	row := application.TenderBid{ID: h.ids.NewID(), TenderID: t.ID, CompanyID: co.ID, BidBy: p.ID, Price: price, Days: int(days), Record: record, Bond: bond, At: now}
	if fresh, err := tx.Tenders().AddBid(ctx, row); err != nil {
		return err
	} else if !fresh {
		return refuseVillage(village.TenderHaveBid, village.AddrTender)
	}
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountWorksEscrow, t.ID)
	if err != nil {
		return err
	}
	_, err = move(ctx, tx, treasury.ID, escrow.ID, bond, application.ReasonWorksBondPost, application.TenderReference, t.ID, "", now)
	return err
}

func (h *VillageHandler) tenderWithdraw(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageTenderRequest, now time.Time) error {
	t, err := tx.Tenders().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || t == nil {
		return refuseVillage(village.TenderNoTender, village.AddrTender)
	}
	if t.Status != "open" || !now.Before(t.ClosesAt) {
		return refuseVillage(village.TenderClosed, village.AddrTender)
	}
	bids, err := tx.Tenders().Bids(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, b := range bids {
		if b.BidBy != p.ID || b.Status != "standing" {
			continue
		}
		if err := h.returnBond(ctx, tx, *t, b, now); err != nil {
			return err
		}
		return tx.Tenders().SetBid(ctx, b.ID, "withdrawn")
	}
	return refuseVillage(village.TenderNoBid, village.AddrTender)
}

// returnBond gives a bid's bond back to its company.
func (h *VillageHandler) returnBond(ctx context.Context, tx application.Tx, t application.Tender, b application.TenderBid, now time.Time) error {
	if b.Bond <= 0 {
		return nil
	}
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountWorksEscrow, t.ID)
	if err != nil {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, b.CompanyID)
	if err != nil {
		return err
	}
	_, err = move(ctx, tx, escrow.ID, treasury.ID, b.Bond, application.ReasonWorksBondReturn, application.TenderReference, t.ID, "", now)
	return err
}

func (h *VillageHandler) tenderCancel(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, req VillageTenderRequest, now time.Time) error {
	t, err := tx.Tenders().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || t == nil {
		return refuseVillage(village.TenderNoTender, village.AddrTender)
	}
	if err := h.tenderPoster(ctx, tx, s, has, p, *t); err != nil {
		return err
	}
	if ok, err := tx.Tenders().SetStatus(ctx, t.ID, "open", "cancelled", now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.TenderClosed, village.AddrTender)
	}
	return h.releaseAllBonds(ctx, tx, *t, "lost", now)
}

// tenderPoster checks the player may act for the tender's payer.
func (h *VillageHandler) tenderPoster(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, t application.Tender) error {
	if t.PayerKind == "union" {
		u, err := tx.Unions().ByID(ctx, t.PayerID, false)
		if err != nil || u == nil {
			return refuseVillage(village.UnionNoUnion, village.AddrTender)
		}
		_, err = h.requireUnionPower(ctx, tx, u, p.ID, charter.WorksTender)
		return err
	}
	if !has || s.CityID != t.PayerID {
		return refuseVillage(village.TenderNotPayer, village.AddrTender)
	}
	_, err := requirePermission(ctx, tx, s, p.ID, charter.WorksTender)
	return err
}

func (h *VillageHandler) releaseAllBonds(ctx context.Context, tx application.Tx, t application.Tender, mark string, now time.Time) error {
	bids, err := tx.Tenders().Bids(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, b := range bids {
		if b.Status != "standing" {
			continue
		}
		if err := h.returnBond(ctx, tx, t, b, now); err != nil {
			return err
		}
		if err := tx.Tenders().SetBid(ctx, b.ID, mark); err != nil {
			return err
		}
	}
	return nil
}

func (h *VillageHandler) tickTender(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	t, err := tx.Tenders().ByID(ctx, id, true)
	if err != nil || t == nil || t.Status != "open" || now.Before(t.ClosesAt) {
		return err
	}
	_, err = h.awardTender(ctx, tx, meta, t, now)
	return err
}

func (h *VillageHandler) tenderAwardAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, req VillageTenderRequest, now time.Time) (string, error) {
	t, err := tx.Tenders().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || t == nil {
		return "", refuseVillage(village.TenderNoTender, village.AddrTender)
	}
	if t.Status != "open" {
		return "", refuseVillage(village.TenderClosed, village.AddrTender)
	}
	if now.Before(t.ClosesAt) {
		return "", refuseVillage(village.TenderNotYetDue, village.AddrTender)
	}
	won, err := h.awardTender(ctx, tx, meta, t, now)
	if err != nil {
		return "", err
	}
	if !won {
		return village.TenderNoticeNoBids, nil
	}
	return village.TenderNoticeAwarded, nil
}

// awardTender picks the winner by the published rule and starts the work. The award is one conditional update, so with any number of
// replicas calling at once exactly one wins it; the others find the tender awarded and change nothing.
func (h *VillageHandler) awardTender(ctx context.Context, tx application.Tx, meta envelope.Metadata, t *application.Tender, now time.Time) (bool, error) {
	bids, err := tx.Tenders().Bids(ctx, t.ID)
	if err != nil {
		return false, err
	}
	officials, err := h.tenderOfficials(ctx, tx, *t)
	if err != nil {
		return false, err
	}
	var valid []tender.Bid
	byCompany := map[string]application.TenderBid{}
	for _, b := range bids {
		if b.Status != "standing" {
			continue
		}
		co, err := tx.Companies().ByID(ctx, b.CompanyID)
		if err != nil || co == nil || !co.Active() {
			continue
		}
		if until, err := tx.Tenders().DebarredUntil(ctx, b.CompanyID); err != nil {
			return false, err
		} else if tender.Debarred(until, now) {
			continue
		}
		stake, err := h.companyStake(ctx, tx, co)
		if err != nil {
			return false, err
		}
		if tender.Conflicts(officials, stake, h.polity.CoiRefuseFromBPS) {
			continue
		}
		valid = append(valid, tender.Bid{Company: b.CompanyID, Price: b.Price, Days: int64(b.Days), Record: int64(b.Record), Bond: b.Bond, At: b.At})
		byCompany[b.CompanyID] = b
	}
	win, ok := tender.Winner(tender.Rule(t.Rule), valid)
	if !ok {
		if moved, err := tx.Tenders().SetStatus(ctx, t.ID, "open", "failed", now); err != nil || !moved {
			return false, err
		}
		if err := h.releaseAllBonds(ctx, tx, *t, "lost", now); err != nil {
			return false, err
		}
		return false, h.auditTender(ctx, tx, meta, *t, "no_bids", now, nil)
	}
	payer, err := h.tenderPayer(ctx, tx, *t)
	if err != nil {
		return false, err
	}
	bal, err := accountBalance(ctx, tx, payer)
	if err != nil {
		return false, err
	}
	if bal < win.Price {
		if moved, err := tx.Tenders().SetStatus(ctx, t.ID, "open", "failed", now); err != nil || !moved {
			return false, err
		}
		if err := h.releaseAllBonds(ctx, tx, *t, "lost", now); err != nil {
			return false, err
		}
		return false, h.auditTender(ctx, tx, meta, *t, "payer_short", now, map[string]any{"price": win.Price})
	}
	wb := byCompany[win.Company]
	dueAt := now.Add(time.Duration(win.Days) * 24 * time.Hour)
	if won, err := tx.Tenders().Award(ctx, t.ID, wb.ID, win.Company, win.Price, now, dueAt); err != nil || !won {
		return false, err
	}
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountWorksEscrow, t.ID)
	if err != nil {
		return false, err
	}
	if _, err := move(ctx, tx, payer.ID, escrow.ID, win.Price, application.ReasonWorksEscrowFund, application.TenderReference, t.ID, "", now); err != nil {
		return false, err
	}
	for _, b := range bids {
		if b.Status != "standing" {
			continue
		}
		if b.ID == wb.ID {
			if err := tx.Tenders().SetBid(ctx, b.ID, "won"); err != nil {
				return false, err
			}
			continue
		}
		if err := h.returnBond(ctx, tx, *t, b, now); err != nil {
			return false, err
		}
		if err := tx.Tenders().SetBid(ctx, b.ID, "lost"); err != nil {
			return false, err
		}
	}
	if _, err := tx.Tenders().SetSiteEmployer(ctx, t.BuildingID, application.LaborEmployerCompany, win.Company); err != nil {
		return false, err
	}
	if err := h.schedulePolity(ctx, tx, application.TickTenderDue, application.TenderReference, t.ID, "", wb.BidBy, now, dueAt.Add(h.polity.TenderGrace)); err != nil {
		return false, err
	}
	if err := h.auditTender(ctx, tx, meta, *t, "awarded", now, map[string]any{"company": win.Company, "price": win.Price, "days": win.Days}); err != nil {
		return false, err
	}
	// work already done on the site counts: stages are paid for what is built from now on and what already stood is paid at once
	return true, nil
}

func (h *VillageHandler) auditTender(ctx context.Context, tx application.Tx, meta envelope.Metadata, t application.Tender, action string, now time.Time, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["tender"] = t.ID
	detail["building"] = t.BuildingID
	if t.PayerKind == "union" {
		if err := h.auditUnion(ctx, tx, t.PayerID, "", "tender_"+action, "", now, detail); err != nil {
			return err
		}
	}
	return appendVillageEvent(ctx, tx, meta, "tender_"+action, t.SettlementID, detail)
}

// tenderProgress is called when a shift of a site's work is done: if the site belongs to a contractor, the stages it has reached are paid.
func (h *VillageHandler) tenderProgress(ctx context.Context, tx application.Tx, meta envelope.Metadata, buildingID string, now time.Time) error {
	if h.polity.RuleAt.IsZero() {
		return nil
	}
	t, err := tx.Tenders().ByBuilding(ctx, buildingID)
	if err != nil || t == nil || t.Status != "awarded" {
		return err
	}
	locked, err := tx.Tenders().ByID(ctx, t.ID, true)
	if err != nil || locked == nil || locked.Status != "awarded" {
		return err
	}
	return h.tenderSettleOf(ctx, tx, meta, locked, now)
}

// tenderSettleOf pays every stage the work has reached and not been paid for, each once.
func (h *VillageHandler) tenderSettleOf(ctx context.Context, tx application.Tx, meta envelope.Metadata, t *application.Tender, now time.Time) error {
	if t.Status != "awarded" {
		return nil
	}
	b, err := tx.SettlementBuildings().Get(ctx, t.BuildingID)
	if err != nil || b == nil {
		return err
	}
	done := tender.StagesDone(b.WorkDone, b.WorkRequired)
	if b.Status == "complete" {
		done = tender.Stages
	}
	if done <= t.StagesPaid {
		return nil
	}
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountWorksEscrow, t.ID)
	if err != nil {
		return err
	}
	company, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, t.CompanyID)
	if err != nil {
		return err
	}
	for k := t.StagesPaid + 1; k <= done; k++ {
		pay, held := tender.StagePay(t.Price, k, t.RetentionBPS)
		txID := ""
		if pay > 0 {
			txID = h.ids.NewID()
		}
		fresh, err := tx.Tenders().PayStage(ctx, application.TenderStage{TenderID: t.ID, Stage: k, Paid: pay, Held: held, LedgerTx: txID, At: now})
		if err != nil {
			return err
		}
		if !fresh {
			continue
		}
		if pay > 0 {
			if _, err := move(ctx, tx, escrow.ID, company.ID, pay, application.ReasonWorksMilestone, application.TenderReference, t.ID, txID, now); err != nil {
				return err
			}
		}
		if err := h.auditTender(ctx, tx, meta, *t, "stage_paid", now, map[string]any{"stage": k, "paid": pay, "held": held}); err != nil {
			return err
		}
		if k == tender.Stages {
			at := tender.RetentionReleaseAt(now, int64(t.DefectsDays))
			if err := h.schedulePolity(ctx, tx, application.TickRetention, application.TenderReference, t.ID, "", t.PostedBy, now, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// tenderDefect is the holder of works.accept raising a defect within the defects period: the retention goes back to the government and the
// work is closed.
func (h *VillageHandler) tenderDefect(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, req VillageTenderRequest, now time.Time) error {
	t, err := tx.Tenders().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || t == nil || t.Status != "awarded" || t.StagesPaid < tender.Stages {
		return refuseVillage(village.TenderNoTender, village.AddrTender)
	}
	if t.PayerKind == "union" {
		u, err := tx.Unions().ByID(ctx, t.PayerID, false)
		if err != nil || u == nil {
			return refuseVillage(village.UnionNoUnion, village.AddrTender)
		}
		if _, err := h.requireUnionPower(ctx, tx, u, p.ID, charter.WorksAccept); err != nil {
			return err
		}
	} else {
		if !has || s.CityID != t.PayerID {
			return refuseVillage(village.TenderNotPayer, village.AddrTender)
		}
		if _, err := requirePermission(ctx, tx, s, p.ID, charter.WorksAccept); err != nil {
			return err
		}
	}
	if t.LastStageAt == nil || !now.Before(tender.RetentionReleaseAt(*t.LastStageAt, int64(t.DefectsDays))) {
		return refuseVillage(village.TenderClosed, village.AddrTender)
	}
	return h.closeWork(ctx, tx, meta, t, true, now)
}

func (h *VillageHandler) tickRetention(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	t, err := tx.Tenders().ByID(ctx, id, true)
	if err != nil || t == nil || t.Status != "awarded" || t.StagesPaid < tender.Stages || t.LastStageAt == nil {
		return err
	}
	if now.Before(tender.RetentionReleaseAt(*t.LastStageAt, int64(t.DefectsDays))) {
		return nil
	}
	return h.closeWork(ctx, tx, meta, t, false, now)
}

// closeWork ends a finished work: the retention to the contractor (or, for a defect, back to the government) and the bond back to the
// contractor; the escrow is left empty.
func (h *VillageHandler) closeWork(ctx context.Context, tx application.Tx, meta envelope.Metadata, t *application.Tender, defect bool, now time.Time) error {
	if moved, err := tx.Tenders().SetStatus(ctx, t.ID, "awarded", "done", now); err != nil || !moved {
		return err
	}
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountWorksEscrow, t.ID)
	if err != nil {
		return err
	}
	company, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, t.CompanyID)
	if err != nil {
		return err
	}
	if t.RetentionHeld > 0 {
		if defect {
			payer, err := h.tenderPayer(ctx, tx, *t)
			if err != nil {
				return err
			}
			if _, err := move(ctx, tx, escrow.ID, payer.ID, t.RetentionHeld, application.ReasonWorksEscrowRefund, application.TenderReference, t.ID, "", now); err != nil {
				return err
			}
		} else if _, err := move(ctx, tx, escrow.ID, company.ID, t.RetentionHeld, application.ReasonWorksRetentionRelease, application.TenderReference, t.ID, "", now); err != nil {
			return err
		}
	}
	bids, err := tx.Tenders().Bids(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, b := range bids {
		if b.Status == "won" {
			if err := h.returnBond(ctx, tx, *t, b, now); err != nil {
				return err
			}
		}
	}
	if err := tx.Tenders().ClearRetention(ctx, t.ID); err != nil {
		return err
	}
	action := "released"
	if defect {
		action = "defect"
	}
	return h.auditTender(ctx, tx, meta, *t, action, now, nil)
}

// tickTenderDue is the contractor's deadline (and its grace) passing: if the work is not finished the bond is forfeited, what is left in the
// escrow goes back to the government, the site goes back to the government's crew and the company is debarred.
func (h *VillageHandler) tickTenderDue(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	t, err := tx.Tenders().ByID(ctx, id, true)
	if err != nil || t == nil || t.Status != "awarded" || t.DueAt == nil {
		return err
	}
	if !tender.Overdue(*t.DueAt, h.polity.TenderGrace, now) {
		return nil
	}
	// the work may have reached stages that were not paid yet: they are paid first
	if err := h.tenderSettleOf(ctx, tx, meta, t, now); err != nil {
		return err
	}
	t, err = tx.Tenders().ByID(ctx, id, true)
	if err != nil || t == nil || t.Status != "awarded" {
		return err
	}
	if t.StagesPaid >= tender.Stages {
		return nil // finished: the retention clock runs
	}
	if moved, err := tx.Tenders().SetStatus(ctx, t.ID, "awarded", "failed", now); err != nil || !moved {
		return err
	}
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountWorksEscrow, t.ID)
	if err != nil {
		return err
	}
	payer, err := h.tenderPayer(ctx, tx, *t)
	if err != nil {
		return err
	}
	bids, err := tx.Tenders().Bids(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, b := range bids {
		if b.Status == "won" && b.Bond > 0 {
			if _, err := move(ctx, tx, escrow.ID, payer.ID, b.Bond, application.ReasonWorksBondForfeit, application.TenderReference, t.ID, "", now); err != nil {
				return err
			}
		}
	}
	bal, err := accountBalance(ctx, tx, escrow)
	if err != nil {
		return err
	}
	if bal > 0 {
		if _, err := move(ctx, tx, escrow.ID, payer.ID, bal, application.ReasonWorksEscrowRefund, application.TenderReference, t.ID, "", now); err != nil {
			return err
		}
	}
	if err := tx.Tenders().ClearRetention(ctx, t.ID); err != nil {
		return err
	}
	if t.PayerKind == "settlement" {
		_, err = tx.Tenders().SetSiteEmployer(ctx, t.BuildingID, application.LaborEmployerSettlement, t.PayerID)
	} else {
		_, err = tx.Tenders().SetSiteEmployer(ctx, t.BuildingID, application.LaborEmployerSettlement, t.SettlementID)
	}
	if err != nil {
		return err
	}
	if err := tx.Tenders().Debar(ctx, t.CompanyID, now.AddDate(0, 0, int(h.polity.DebarDays)), "bond_forfeited", t.ID, now); err != nil {
		return err
	}
	return h.auditTender(ctx, tx, meta, *t, "failed", now, map[string]any{"company": t.CompanyID})
}

// ---- the screen -------------------------------------------------------------------------------------------------------------------

func (h *VillageHandler) tenderLine(ctx context.Context, tx application.Tx, t application.Tender, viewer *application.Player, mine map[string]bool, mayPost bool) (village.TenderLine, error) {
	st, err := tx.Settlements().ByID(ctx, t.SettlementID)
	if err != nil {
		return village.TenderLine{}, err
	}
	l := village.TenderLine{ID: t.ID, Settlement: named(st.Code, st.Name), PayerKind: t.PayerKind, Status: t.Status, Rule: t.Rule, BudgetCap: t.BudgetCap,
		BondBPS: t.BondBPS, RetentionBPS: t.RetentionBPS, DefectsDays: t.DefectsDays, DueDays: t.DueDays, ClosesAt: t.ClosesAt, Price: t.Price, DueAt: t.DueAt,
		StagesPaid: t.StagesPaid, RetentionHeld: t.RetentionHeld, MayCancel: mayPost && t.Status == "open", MayAward: mayPost && t.Status == "open" && !h.now().Before(t.ClosesAt)}
	if b, err := tx.SettlementBuildings().Get(ctx, t.BuildingID); err == nil && b != nil {
		l.Building = named(b.TypeCode, h.buildingName(b.TypeCode))
		l.WorkDone, l.WorkRequired = b.WorkDone, b.WorkRequired
	}
	if t.PayerKind == "settlement" {
		l.Payer = named(st.Code, st.Name)
	} else if u, err := tx.Unions().ByID(ctx, t.PayerID, false); err == nil && u != nil {
		l.Payer = named(u.ID, u.Name)
	}
	if t.CompanyID != "" {
		if co, err := tx.Companies().ByID(ctx, t.CompanyID); err == nil && co != nil {
			n := named(co.Code, co.Name)
			l.Company = &n
		}
	}
	bids, err := tx.Tenders().Bids(ctx, t.ID)
	if err != nil {
		return l, err
	}
	for _, b := range bids {
		cn := named(b.CompanyID, "")
		if co, err := tx.Companies().ByID(ctx, b.CompanyID); err == nil && co != nil {
			cn = named(co.Code, co.Name)
		}
		l.Bids = append(l.Bids, village.TenderBidLine{ID: b.ID, Company: cn, Price: b.Price, Days: b.Days, Record: b.Record, Bond: b.Bond, Status: b.Status, Mine: b.BidBy == viewer.ID})
	}
	stages, err := tx.Tenders().Stages(ctx, t.ID)
	if err != nil {
		return l, err
	}
	for _, sg := range stages {
		l.Stages = append(l.Stages, village.TenderStageLine{Stage: sg.Stage, Paid: sg.Paid, Held: sg.Held, At: sg.At})
	}
	l.MayBid = t.Status == "open" && h.now().Before(t.ClosesAt) && len(mine) > 0
	return l, nil
}

func (h *VillageHandler) tenderView(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player) (village.TenderView, error) {
	v := village.TenderView{Open: h.polityOpen(), Fee: h.polity.TenderFee, BondBPS: h.polity.BondBPS, RetentionBPS: h.polity.RetentionBPS}
	mine := map[string]bool{}
	companies, err := tx.Companies().Of(ctx, p.ID)
	if err != nil {
		return v, err
	}
	for _, c := range companies {
		if c.TypeCode == "works_contractor" {
			mine[c.ID] = true
			cl := village.TenderCompanyLine{Company: named(c.Code, c.Name)}
			if cl.Record, err = tx.Tenders().Record(ctx, c.ID); err != nil {
				return v, err
			}
			if until, err := tx.Tenders().DebarredUntil(ctx, c.ID); err == nil && until.After(h.now()) {
				u := until
				cl.DebarredUntil = &u
			}
			v.Companies = append(v.Companies, cl)
		}
	}
	mayPost := false
	if has {
		v.Village = s.Name
		_, mayPost = hasPermissionMap(ctx, tx, s, p.ID, charter.WorksTender)
		_, v.MayAccept = hasPermissionMap(ctx, tx, s, p.ID, charter.WorksAccept)
		v.MayTender = mayPost
		list, err := tx.Tenders().OfSettlement(ctx, s.CityID, 20)
		if err != nil {
			return v, err
		}
		for _, t := range list {
			line, err := h.tenderLine(ctx, tx, t, p, mine, mayPost)
			if err != nil {
				return v, err
			}
			v.Tenders = append(v.Tenders, line)
		}
		if mayPost {
			bs, err := tx.SettlementBuildings().List(ctx, s.CityID)
			if err != nil {
				return v, err
			}
			for _, b := range bs {
				if b.Status != "building" || !b.ByWork() {
					continue
				}
				if have, err := tx.Tenders().ByBuilding(ctx, b.ID); err != nil || have != nil {
					continue
				}
				if job, err := tx.SettlementTreasury().JobOfBuildingKind(ctx, b.ID, application.LaborKindConstruction); err != nil || job == nil || job.EmployerKind != application.LaborEmployerSettlement {
					continue
				}
				v.Sites = append(v.Sites, village.TenderSiteLine{ID: b.ID, Building: named(b.TypeCode, h.buildingName(b.TypeCode)), WorkRequired: b.WorkRequired, WorkDone: b.WorkDone})
			}
		}
	}
	if len(mine) > 0 {
		board, err := tx.Tenders().Open(ctx, 20)
		if err != nil {
			return v, err
		}
		for _, t := range board {
			line, err := h.tenderLine(ctx, tx, t, p, mine, false)
			if err != nil {
				return v, err
			}
			v.Board = append(v.Board, line)
		}
	}
	return v, nil
}

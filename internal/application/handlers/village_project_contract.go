package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/tender"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The tender of a special project (docs/adr/0077 4.3): B12's contractor machinery (docs/adr/0076 10) generalised to the project's own stage
// count. The poster offers the rest of the work to the companies that do public works; the rule picks one; the project's fund funds an escrow with
// the price; every stage the project completes pays the contractor its share less the retention; the crew of a let project is the company's own
// (the company pays its people), and the retention is held for the defects period. A missed deadline forfeits the bond and debars the company.

func (h *VillageHandler) contractOpen() bool { return h.polityOpen() }

func contractEscrow(ctx context.Context, tx application.Tx, contractID string) (application.Account, error) {
	return tx.Ledger().AccountFor(ctx, application.AccountProjectEscrow, contractID)
}

// contractOfficials are the people who post and award a project's tender: the one who posted it and every holder of works.tender.
func (h *VillageHandler) contractOfficials(ctx context.Context, tx application.Tx, s application.FoundedSettlement, c application.ProjectContract) ([]string, error) {
	hs, err := permissionHolders(ctx, tx, s, charter.WorksTender)
	if err != nil {
		return nil, err
	}
	return append([]string{c.PostedBy}, hs...), nil
}

func (h *VillageHandler) contractPost(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageProjectRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if !h.contractOpen() {
		return refuseVillage(village.ProjectNotYet, village.AddrProject)
	}
	limit, err := requirePermission(ctx, tx, s, p.ID, charter.WorksTender)
	if err != nil {
		return err
	}
	pr, err := h.loadActive(ctx, tx, s, strings.TrimSpace(req.ID))
	if err != nil {
		return err
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
		return refuseVillage(village.ProjectBadArgs, village.AddrProject)
	}
	if limit > 0 && (capv == 0 || capv > limit) {
		return refuseVillage(village.ProjectBadArgs, village.AddrProject)
	}
	if running, err := tx.Projects().ShiftsOf(ctx, pr.ID, 100); err != nil {
		return err
	} else if runningHours(running, pr.Stage) > 0 {
		return refuseVillage(village.ProjectBusy, village.AddrProject)
	}
	c := application.ProjectContract{ID: h.ids.NewID(), ProjectID: pr.ID, PostedBy: p.ID, BudgetCap: capv, Rule: string(rule), BondBPS: h.polity.BondBPS, RetentionBPS: h.polity.RetentionBPS,
		DefectsDays: int(h.polity.DefectsDays), DueDays: int(due), OpensAt: now, ClosesAt: now.Add(time.Duration(window) * time.Hour)}
	if fee := h.polity.TenderFee; fee > 0 {
		bal, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if bal < fee {
			return refuseVillage(village.ProjectNoMoney, village.AddrProject)
		}
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return err
		}
		if _, err := move(ctx, tx, acct.ID, application.SystemSinkAccountID, fee, application.ReasonTenderFee, application.ContractReference, c.ID, "", now); err != nil {
			return err
		}
	}
	if ok, err := tx.Projects().InsertContract(ctx, c); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.ProjectHasContract, village.AddrProject)
	}
	if err := h.schedulePolity(ctx, tx, application.TickContractClose, application.ContractReference, c.ID, "", p.ID, now, c.ClosesAt); err != nil {
		return err
	}
	return specialEvent(ctx, tx, meta, "project_tender_posted", s.CityID, map[string]any{"project": pr.ID, "contract": c.ID, "cap": capv})
}

func (h *VillageHandler) contractBid(ctx context.Context, tx application.Tx, p *application.Player, req VillageProjectRequest, now time.Time) error {
	c, err := tx.Projects().ContractByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if c == nil {
		return refuseVillage(village.ProjectNoContract, village.AddrProject)
	}
	if c.Status != "open" || !now.Before(c.ClosesAt) {
		return refuseVillage(village.ProjectContractEnd, village.AddrProject)
	}
	co, err := ownsCompany(ctx, tx, p.ID, req.Company)
	if err != nil {
		return err
	}
	if co == nil {
		return refuseVillage(village.ProjectNotCompany, village.AddrProject)
	}
	if co.TypeCode != "works_contractor" {
		return refuseVillage(village.ProjectNoType, village.AddrProject)
	}
	if until, err := tx.Tenders().DebarredUntil(ctx, co.ID); err != nil {
		return err
	} else if tender.Debarred(until, now) {
		return refuseVillage(village.ProjectDebarred, village.AddrProject)
	}
	pr, err := tx.Projects().ByID(ctx, c.ProjectID, false)
	if err != nil || pr == nil {
		return refuseVillage(village.ProjectNoProject, village.AddrProject)
	}
	s, err := tx.Settlements().ByID(ctx, pr.SettlementID)
	if err != nil {
		return err
	}
	officials, err := h.contractOfficials(ctx, tx, s, *c)
	if err != nil {
		return err
	}
	stake, err := h.companyStake(ctx, tx, co)
	if err != nil {
		return err
	}
	if tender.Conflicts(officials, stake, h.polity.CoiRefuseFromBPS) {
		return refuseVillage(village.ProjectConflict, village.AddrProject)
	}
	record, err := tx.Tenders().Record(ctx, co.ID)
	if err != nil {
		return err
	}
	price, days := atoi64(req.Price), atoi64(req.Days)
	bond := tender.BondFor(price, c.BondBPS)
	if err := tender.Check(tender.Bid{Company: co.ID, Price: price, Days: days, Record: int64(record), Bond: bond, At: now}, c.BudgetCap, c.BondBPS); err != nil {
		return refuseVillage(village.ProjectBadBid, village.AddrProject)
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, co.ID)
	if err != nil {
		return err
	}
	if bal, err := accountBalance(ctx, tx, treasury); err != nil {
		return err
	} else if bal < bond {
		return refuseVillage(village.ProjectNoMoney, village.AddrProject)
	}
	row := application.ProjectContractBid{ID: h.ids.NewID(), ContractID: c.ID, CompanyID: co.ID, BidBy: p.ID, Price: price, Days: int(days), Record: record, Bond: bond, At: now}
	if fresh, err := tx.Projects().AddContractBid(ctx, row); err != nil {
		return err
	} else if !fresh {
		return refuseVillage(village.ProjectHaveBid, village.AddrProject)
	}
	escrow, err := contractEscrow(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	_, err = move(ctx, tx, treasury.ID, escrow.ID, bond, application.ReasonWorksBondPost, application.ContractReference, c.ID, "", now)
	return err
}

func (h *VillageHandler) contractWithdraw(ctx context.Context, tx application.Tx, p *application.Player, req VillageProjectRequest, now time.Time) error {
	c, err := tx.Projects().ContractByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if c == nil {
		return refuseVillage(village.ProjectNoContract, village.AddrProject)
	}
	if c.Status != "open" || !now.Before(c.ClosesAt) {
		return refuseVillage(village.ProjectContractEnd, village.AddrProject)
	}
	bids, err := tx.Projects().ContractBids(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, b := range bids {
		if b.BidBy != p.ID || b.Status != "standing" {
			continue
		}
		if err := h.returnContractBond(ctx, tx, *c, b, now); err != nil {
			return err
		}
		return tx.Projects().SetContractBid(ctx, b.ID, "withdrawn")
	}
	return refuseVillage(village.ProjectNoBid, village.AddrProject)
}

func (h *VillageHandler) returnContractBond(ctx context.Context, tx application.Tx, c application.ProjectContract, b application.ProjectContractBid, now time.Time) error {
	if b.Bond <= 0 {
		return nil
	}
	escrow, err := contractEscrow(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, b.CompanyID)
	if err != nil {
		return err
	}
	_, err = move(ctx, tx, escrow.ID, treasury.ID, b.Bond, application.ReasonWorksBondReturn, application.ContractReference, c.ID, "", now)
	return err
}

func (h *VillageHandler) releaseContractBonds(ctx context.Context, tx application.Tx, c application.ProjectContract, to string, now time.Time) error {
	bids, err := tx.Projects().ContractBids(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, b := range bids {
		if b.Status != "standing" {
			continue
		}
		if err := h.returnContractBond(ctx, tx, c, b, now); err != nil {
			return err
		}
		if err := tx.Projects().SetContractBid(ctx, b.ID, to); err != nil {
			return err
		}
	}
	return nil
}

// tickContractClose is the bids closing: the contract is awarded, or fails with no bid that can stand.
func (h *VillageHandler) tickContractClose(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	c, _, err := h.lockContract(ctx, tx, id)
	if err != nil || c == nil || c.Status != "open" || now.Before(c.ClosesAt) {
		return err
	}
	_, err = h.awardContract(ctx, tx, meta, c, now)
	return err
}

func (h *VillageHandler) contractAwardAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageProjectRequest, now time.Time,
) (string, error) {
	if !has {
		return "", refuseVillage(village.VillageNoSettlement)
	}
	c, _, err := h.lockContract(ctx, tx, strings.TrimSpace(req.ID))
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", refuseVillage(village.ProjectNoContract, village.AddrProject)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.WorksTender); err != nil {
		return "", err
	}
	if c.Status != "open" {
		return "", refuseVillage(village.ProjectContractEnd, village.AddrProject)
	}
	if now.Before(c.ClosesAt) {
		return "", refuseVillage(village.ProjectContractEnd, village.AddrProject)
	}
	won, err := h.awardContract(ctx, tx, meta, c, now)
	if err != nil {
		return "", err
	}
	if !won {
		return village.ProjectNoticeTender, nil
	}
	return village.ProjectNoticeAwarded, nil
}

// awardContract picks the winner by the published rule, funds the escrow from the project's fund and starts the work. One conditional update:
// with any number of replicas calling at once exactly one wins it.
func (h *VillageHandler) awardContract(ctx context.Context, tx application.Tx, meta envelope.Metadata, c *application.ProjectContract, now time.Time) (bool, error) {
	pr, err := tx.Projects().ByID(ctx, c.ProjectID, false) // the caller holds its lock
	if err != nil || pr == nil {
		return false, err
	}
	s, err := tx.Settlements().ByID(ctx, pr.SettlementID)
	if err != nil {
		return false, err
	}
	bids, err := tx.Projects().ContractBids(ctx, c.ID)
	if err != nil {
		return false, err
	}
	officials, err := h.contractOfficials(ctx, tx, s, *c)
	if err != nil {
		return false, err
	}
	var valid []tender.Bid
	byCompany := map[string]application.ProjectContractBid{}
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
	fail := func(why string) (bool, error) {
		if moved, err := tx.Projects().SetContractStatus(ctx, c.ID, "open", "failed", now); err != nil || !moved {
			return false, err
		}
		if err := h.releaseContractBonds(ctx, tx, *c, "lost", now); err != nil {
			return false, err
		}
		return false, specialEvent(ctx, tx, meta, "project_tender_"+why, s.CityID, map[string]any{"project": pr.ID, "contract": c.ID})
	}
	win, ok := tender.Winner(tender.Rule(c.Rule), valid)
	if !ok || !pr.Active() {
		return fail("no_bids")
	}
	fund, err := fundOf(ctx, tx, pr.ID)
	if err != nil {
		return false, err
	}
	if bal, err := accountBalance(ctx, tx, fund); err != nil {
		return false, err
	} else if bal < win.Price {
		return fail("fund_short")
	}
	wb := byCompany[win.Company]
	dueAt := now.Add(time.Duration(win.Days) * 24 * time.Hour)
	if won, err := tx.Projects().AwardContract(ctx, c.ID, wb.ID, win.Company, win.Price, now, dueAt); err != nil || !won {
		return false, err
	}
	escrow, err := contractEscrow(ctx, tx, c.ID)
	if err != nil {
		return false, err
	}
	if _, err := move(ctx, tx, fund.ID, escrow.ID, win.Price, application.ReasonWorksEscrowFund, application.ContractReference, c.ID, "", now); err != nil {
		return false, err
	}
	for _, b := range bids {
		if b.Status != "standing" {
			continue
		}
		if b.ID == wb.ID {
			if err := tx.Projects().SetContractBid(ctx, b.ID, "won"); err != nil {
				return false, err
			}
			continue
		}
		if err := h.returnContractBond(ctx, tx, *c, b, now); err != nil {
			return false, err
		}
		if err := tx.Projects().SetContractBid(ctx, b.ID, "lost"); err != nil {
			return false, err
		}
	}
	if err := h.schedulePolity(ctx, tx, application.TickContractDue, application.ContractReference, c.ID, "", wb.BidBy, now, dueAt.Add(h.polity.TenderGrace)); err != nil {
		return false, err
	}
	return true, specialEvent(ctx, tx, meta, "project_tender_awarded", s.CityID, map[string]any{"project": pr.ID, "contract": c.ID, "company": win.Company, "price": win.Price, "days": win.Days})
}

// contractStageDone pays the contractor for the stage the project just completed, once (the paid-stage row is the fence).
func (h *VillageHandler) contractStageDone(ctx context.Context, tx application.Tx, meta envelope.Metadata, pr *application.SpecialProject, stageNo int, now time.Time) error {
	c, err := tx.Projects().ContractOf(ctx, pr.ID)
	if err != nil || c == nil || c.Status != "awarded" {
		return err
	}
	locked, err := tx.Projects().ContractByID(ctx, c.ID, true)
	if err != nil || locked == nil || locked.Status != "awarded" {
		return err
	}
	pay, held := tender.StagePayN(locked.Price, stageNo, pr.Stages, locked.RetentionBPS)
	txID := ""
	if pay > 0 {
		txID = h.ids.NewID()
	}
	fresh, err := tx.Projects().PayContractStage(ctx, application.ProjectContractStage{ContractID: locked.ID, Stage: stageNo, Paid: pay, Held: held, LedgerTx: txID, At: now})
	if err != nil || !fresh {
		return err
	}
	if pay > 0 {
		escrow, err := contractEscrow(ctx, tx, locked.ID)
		if err != nil {
			return err
		}
		company, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, locked.CompanyID)
		if err != nil {
			return err
		}
		if _, err := move(ctx, tx, escrow.ID, company.ID, pay, application.ReasonWorksMilestone, application.ContractReference, locked.ID, txID, now); err != nil {
			return err
		}
	}
	if held > 0 {
		if err := tx.Projects().AddContractRetention(ctx, locked.ID, held, now); err != nil {
			return err
		}
	}
	if stageNo == pr.Stages {
		return h.schedulePolity(ctx, tx, application.TickContractRelease, application.ContractReference, locked.ID, "", pr.StartedBy, now, tender.RetentionReleaseAt(now, int64(locked.DefectsDays)))
	}
	return nil
}

// tickContractRelease ends a finished contract: the retention goes to the company, or to the fund if a defect was raised; the bond is returned.
func (h *VillageHandler) tickContractRelease(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	c, pr, err := h.lockContract(ctx, tx, id)
	if err != nil || c == nil || c.Status != "awarded" || c.StagesPaid < pr.Stages || c.LastStageAt == nil {
		return err
	}
	if now.Before(tender.RetentionReleaseAt(*c.LastStageAt, int64(c.DefectsDays))) {
		return nil
	}
	if moved, err := tx.Projects().SetContractStatus(ctx, c.ID, "awarded", "done", now); err != nil || !moved {
		return err
	}
	escrow, err := contractEscrow(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	company, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, c.CompanyID)
	if err != nil {
		return err
	}
	if c.RetentionHeld > 0 {
		to := company
		if c.DefectRaised {
			if to, err = fundOf(ctx, tx, pr.ID); err != nil {
				return err
			}
		}
		if _, err := move(ctx, tx, escrow.ID, to.ID, c.RetentionHeld, application.ReasonWorksRetentionRelease, application.ContractReference, c.ID, "", now); err != nil {
			return err
		}
	}
	if err := tx.Projects().ClearContractRetention(ctx, c.ID); err != nil {
		return err
	}
	bids, err := tx.Projects().ContractBids(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, b := range bids {
		if b.Status == "won" && b.Bond > 0 {
			if _, err := move(ctx, tx, escrow.ID, company.ID, b.Bond, application.ReasonWorksBondReturn, application.ContractReference, c.ID, "", now); err != nil {
				return err
			}
		}
	}
	return specialEvent(ctx, tx, meta, "project_contract_done", pr.SettlementID, map[string]any{"project": pr.ID, "contract": c.ID, "company": c.CompanyID})
}

// tickContractDue is the deadline of the work: a contractor that has not finished it forfeits its bond, what is left of the escrow returns to
// the fund, and the company is kept from public works for a time.
func (h *VillageHandler) tickContractDue(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	c, pr, err := h.lockContract(ctx, tx, id)
	if err != nil || c == nil || c.Status != "awarded" || c.DueAt == nil {
		return err
	}
	if c.StagesPaid >= pr.Stages || !tender.Overdue(*c.DueAt, h.polity.TenderGrace, now) {
		return nil
	}
	if moved, err := tx.Projects().SetContractStatus(ctx, c.ID, "awarded", "failed", now); err != nil || !moved {
		return err
	}
	escrow, err := contractEscrow(ctx, tx, c.ID)
	if err != nil {
		return err
	}
	fund, err := fundOf(ctx, tx, pr.ID)
	if err != nil {
		return err
	}
	bids, err := tx.Projects().ContractBids(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, b := range bids {
		if b.Status == "won" && b.Bond > 0 {
			if _, err := move(ctx, tx, escrow.ID, fund.ID, b.Bond, application.ReasonWorksBondForfeit, application.ContractReference, c.ID, "", now); err != nil {
				return err
			}
		}
	}
	if bal, err := accountBalance(ctx, tx, escrow); err != nil {
		return err
	} else if bal > 0 {
		if _, err := move(ctx, tx, escrow.ID, fund.ID, bal, application.ReasonWorksEscrowRefund, application.ContractReference, c.ID, "", now); err != nil {
			return err
		}
	}
	if err := tx.Projects().ClearContractRetention(ctx, c.ID); err != nil {
		return err
	}
	if err := tx.Tenders().Debar(ctx, c.CompanyID, now.AddDate(0, 0, int(h.polity.DebarDays)), "bond_forfeited", "", now); err != nil {
		return err
	}
	return specialEvent(ctx, tx, meta, "project_contract_failed", pr.SettlementID, map[string]any{"project": pr.ID, "contract": c.ID, "company": c.CompanyID})
}

// contractDefect raises a defect in the finished work within the defects period: the retention then goes to the fund, not to the company.
func (h *VillageHandler) contractDefect(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, req VillageProjectRequest, now time.Time) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.ProjectAccept); err != nil {
		return err
	}
	c, err := tx.Projects().ContractByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if c == nil {
		return refuseVillage(village.ProjectNoContract, village.AddrProject)
	}
	pr, err := tx.Projects().ByID(ctx, c.ProjectID, false)
	if err != nil || pr == nil || pr.SettlementID != s.CityID {
		return refuseVillage(village.ProjectNoContract, village.AddrProject)
	}
	if c.Status != "awarded" || c.LastStageAt == nil || !now.Before(tender.RetentionReleaseAt(*c.LastStageAt, int64(c.DefectsDays))) {
		return refuseVillage(village.ProjectNoDefect, village.AddrProject)
	}
	if ok, err := tx.Projects().RaiseContractDefect(ctx, c.ID); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.ProjectNoDefect, village.AddrProject)
	}
	return nil
}

// lockContract locks a project and then its contract: the order every act that touches both takes (a stage completes with the project locked and
// then pays the contract), so a close of the bids and a stage accepted at once cannot wait for each other.
func (h *VillageHandler) lockContract(ctx context.Context, tx application.Tx, id string) (*application.ProjectContract, *application.SpecialProject, error) {
	c, err := tx.Projects().ContractByID(ctx, id, false)
	if err != nil || c == nil {
		return nil, nil, err
	}
	pr, err := tx.Projects().ByID(ctx, c.ProjectID, true)
	if err != nil || pr == nil {
		return nil, nil, err
	}
	c, err = tx.Projects().ContractByID(ctx, id, true)
	return c, pr, err
}

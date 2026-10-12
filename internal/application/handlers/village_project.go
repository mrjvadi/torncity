package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/project"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Special projects (docs/adr/0077 4): a long public work of many stages.
//
//   - WHO WORKS THERE. Residents take a crew shift on the site (a timed activity, one at a time, paid from the fund), the settlement hires the
//     labour of the pool by the hour (paid to the sink, a few hours a day), or a contractor's crew does it (the company pays its own people).
//   - WHAT IT CONSUMES. Materials delivered to the site (from the treasury's stock or from what a resident carries, used up as the stage is
//     done), crew-hours, money in the fund (the treasury's allocation and the pledges), and time: a stage has a least duration.
//   - WHAT IT PROVIDES. A finished work that stands as a service post of the settlement: its keepers are paid and its fuel burnt every local day;
//     a work nobody keeps stands idle (neglected), never destroyed. What it does is read by the system its effect names.
//   - WHAT BREAKS. No materials, no crew, no research for a later stage, or an empty fund: the stage waits, and nothing already done is lost.

// VillageProjectRequest is the payload of settlement.project.
type VillageProjectRequest struct {
	// Action is "" (the screen) or one of village.ProjectAction*.
	Action string `json:"action,omitempty"`
	// ID names a project, a pledge or a contract; Code a kind of project or an item.
	ID   string `json:"id,omitempty"`
	Code string `json:"code,omitempty"`
	// N is a quantity (items, hours); Amount money in minor units; Anonymous "1" for a gift whose name is not shown.
	N         string `json:"n,omitempty"`
	Amount    string `json:"amount,omitempty"`
	Anonymous string `json:"anonymous,omitempty"`
	// Rule, Cap, Due and Window are a tender: lowest_price or scored, the budget cap, the days the work may take, the hours bids are taken.
	Rule   string `json:"rule,omitempty"`
	Cap    string `json:"cap,omitempty"`
	Due    string `json:"due,omitempty"`
	Window string `json:"window,omitempty"`
	// Company, Price and Days are a company's bid.
	Company string `json:"company,omitempty"`
	Price   string `json:"price,omitempty"`
	Days    string `json:"days,omitempty"`
}

// Project handles settlement.project.
func (h *VillageHandler) Project(ctx context.Context, meta envelope.Metadata, req VillageProjectRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.ProjectView
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
		if act := strings.TrimSpace(req.Action); act != "" && act != village.ProjectActionOpen {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.projectAct(ctx, tx, meta, snap, s, has, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.projectView(ctx, tx, snap, s, has, p, strings.TrimSpace(req.ID))
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Project(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) projectAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool,
	p *application.Player, act string, req VillageProjectRequest,
) (string, error) {
	if !h.projectsOpen() {
		return "", refuseVillage(village.ProjectNotYet, village.AddrProject)
	}
	now := h.now()
	switch act {
	case village.ProjectActionStart:
		if !has {
			return "", refuseVillage(village.VillageNoSettlement)
		}
		return village.ProjectNoticeStarted, h.projectStart(ctx, tx, meta, snap, s, p, req, now)
	case village.ProjectActionPledge:
		return village.ProjectNoticePledged, h.projectPledge(ctx, tx, meta, p, req, now)
	case village.ProjectActionUnpledge:
		return village.ProjectNoticeUnpledged, h.projectUnpledge(ctx, tx, p, req, now)
	case village.ProjectActionAllocate:
		return village.ProjectNoticeAllocated, h.projectAllocate(ctx, tx, meta, s, has, p, req, now)
	case village.ProjectActionDeliver, village.ProjectActionGive:
		return h.projectDeliver(ctx, tx, meta, snap, s, has, p, req, act == village.ProjectActionGive, now)
	case village.ProjectActionWork:
		return village.ProjectNoticeWorking, h.projectWork(ctx, tx, meta, snap, s, has, p, req, now)
	case village.ProjectActionHire:
		return h.projectHire(ctx, tx, meta, snap, s, has, p, req, now)
	case village.ProjectActionCancel:
		return village.ProjectNoticeCancelled, h.projectCancel(ctx, tx, meta, snap, s, has, p, req, now)
	case village.ProjectActionTender:
		return village.ProjectNoticeTender, h.contractPost(ctx, tx, meta, s, has, p, req, now)
	case village.ProjectActionBid:
		return village.ProjectNoticeBid, h.contractBid(ctx, tx, p, req, now)
	case village.ProjectActionWithdraw:
		return village.ProjectNoticeWithdrawn, h.contractWithdraw(ctx, tx, p, req, now)
	case village.ProjectActionAward:
		return h.contractAwardAct(ctx, tx, meta, snap, s, has, p, req, now)
	case village.ProjectActionDefect:
		return village.ProjectNoticeDefect, h.contractDefect(ctx, tx, s, has, p, req, now)
	}
	return "", refuseVillage(village.ProjectBadArgs, village.AddrProject)
}

// fundOf is the project's fund account.
func fundOf(ctx context.Context, tx application.Tx, projectID string) (application.Account, error) {
	return tx.Ledger().AccountFor(ctx, application.AccountProjectFund, projectID)
}

// loadActive reads a project of the settlement under its lock and refuses one that is not being built.
func (h *VillageHandler) loadActive(ctx context.Context, tx application.Tx, s application.FoundedSettlement, id string) (*application.SpecialProject, error) {
	pr, err := tx.Projects().ByID(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if pr == nil || pr.SettlementID != s.CityID {
		return nil, refuseVillage(village.ProjectNoProject, village.AddrProject)
	}
	if !pr.Active() {
		return nil, refuseVillage(village.ProjectNotActive, village.AddrProject)
	}
	return pr, nil
}

func (h *VillageHandler) projectStart(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, req VillageProjectRequest, now time.Time,
) error {
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.ProjectStart); err != nil {
		return err
	}
	def, ok := snap.SpecialProject(strings.TrimSpace(req.Code))
	if !ok {
		return refuseVillage(village.ProjectNoKind, village.AddrProject)
	}
	if missing, err := knowledgeMissing(ctx, tx, s.CityID, needsKnowledge(def.Requires)); err != nil {
		return err
	} else if len(missing) > 0 {
		return researchRefusal(village.ProjectResearch, village.AddrProject, missing, snap)
	}
	if n, err := tx.Projects().CountActive(ctx, s.CityID); err != nil {
		return err
	} else if n >= h.special.Projects.MaxOpen {
		return refuseVillage(village.ProjectTooMany, village.AddrProject)
	}
	if fee := h.special.Projects.StartFee; fee > 0 {
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
		if _, err := move(ctx, tx, acct.ID, application.SystemSinkAccountID, fee, application.ReasonProjectFee, application.ProjectReference, s.CityID, "", now); err != nil {
			return err
		}
	}
	pr := application.SpecialProject{ID: h.ids.NewID(), Kind: def.Code, SettlementID: s.CityID, StartedBy: p.ID, Status: "active", Stages: len(def.Stages), Stage: 1, StartedAt: now}
	if ok, err := tx.Projects().Insert(ctx, pr); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.ProjectTooMany, village.AddrProject)
	}
	return specialEvent(ctx, tx, meta, "project_started", s.CityID, map[string]any{"project": pr.ID, "kind": def.Code, "by": p.ID})
}

func (h *VillageHandler) projectPledge(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player, req VillageProjectRequest, now time.Time) error {
	amount := atoi64(req.Amount)
	if amount < 1 {
		return refuseVillage(village.ProjectBadArgs, village.AddrProject)
	}
	if amount < h.special.Projects.PledgeMin {
		return refuseVillage(village.ProjectBelowMin, village.AddrProject)
	}
	pr, err := tx.Projects().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil {
		return err
	}
	if pr == nil {
		return refuseVillage(village.ProjectNoProject, village.AddrProject)
	}
	if !pr.Active() {
		return refuseVillage(village.ProjectNotActive, village.AddrProject)
	}
	cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
	if err != nil {
		return err
	}
	if bal, err := accountBalance(ctx, tx, cash); err != nil {
		return err
	} else if bal < amount {
		return refuseVillage(village.ProjectNoMoney, village.AddrProject)
	}
	fund, err := fundOf(ctx, tx, pr.ID)
	if err != nil {
		return err
	}
	id := h.ids.NewID()
	txID, err := move(ctx, tx, cash.ID, fund.ID, amount, application.ReasonProjectPledge, application.ProjectReference, pr.ID, "", now)
	if err != nil {
		return err
	}
	if err := tx.Projects().AddPledge(ctx, application.ProjectPledge{ID: id, ProjectID: pr.ID, PlayerID: p.ID, Kind: "pledge", Amount: amount, Anonymous: req.Anonymous == "1", LedgerTx: txID, At: now}); err != nil {
		return err
	}
	return specialEvent(ctx, tx, meta, "project_pledged", pr.SettlementID, map[string]any{"project": pr.ID, "amount": amount, "anonymous": req.Anonymous == "1"})
}

// projectUnpledge takes a pledge back: only before the first stage has started, only by the one who gave it, and only what is in the fund.
func (h *VillageHandler) projectUnpledge(ctx context.Context, tx application.Tx, p *application.Player, req VillageProjectRequest, now time.Time) error {
	pl, err := tx.Projects().PledgeByID(ctx, strings.TrimSpace(req.ID), false)
	if err != nil {
		return err
	}
	if pl == nil || pl.PlayerID != p.ID || pl.Kind != "pledge" {
		return refuseVillage(village.ProjectNoPledge, village.AddrProject)
	}
	pr, err := tx.Projects().ByID(ctx, pl.ProjectID, true)
	if err != nil {
		return err
	}
	if pr == nil || !pr.Active() || !project.Refundable(pr.Stage, pr.StageStartedAt != nil) {
		return refuseVillage(village.ProjectSpent, village.AddrProject)
	}
	if ok, err := tx.Projects().RefundPledge(ctx, pl.ID, now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.ProjectNoPledge, village.AddrProject)
	}
	fund, err := fundOf(ctx, tx, pr.ID)
	if err != nil {
		return err
	}
	cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.ID)
	if err != nil {
		return err
	}
	_, err = move(ctx, tx, fund.ID, cash.ID, pl.Amount, application.ReasonProjectPledgeRefund, application.ProjectReference, pr.ID, "", now)
	return err
}

func (h *VillageHandler) projectAllocate(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player,
	req VillageProjectRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	limit, err := requirePermission(ctx, tx, s, p.ID, charter.ProjectFund)
	if err != nil {
		return err
	}
	amount := atoi64(req.Amount)
	if amount < 1 || (limit > 0 && amount > limit) {
		return refuseVillage(village.ProjectBadArgs, village.AddrProject)
	}
	pr, err := h.loadActive(ctx, tx, s, strings.TrimSpace(req.ID))
	if err != nil {
		return err
	}
	if bal, err := treasuryBalance(ctx, tx, s.CityID); err != nil {
		return err
	} else if bal < amount {
		return refuseVillage(village.ProjectNoMoney, village.AddrProject)
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
	if err != nil {
		return err
	}
	fund, err := fundOf(ctx, tx, pr.ID)
	if err != nil {
		return err
	}
	txID, err := move(ctx, tx, treasury.ID, fund.ID, amount, application.ReasonProjectAllocation, application.ProjectReference, pr.ID, "", now)
	if err != nil {
		return err
	}
	return tx.Projects().AddPledge(ctx, application.ProjectPledge{ID: h.ids.NewID(), ProjectID: pr.ID, Kind: "allocation", Amount: amount, LedgerTx: txID, At: now})
}

// stageNow is the stage the project is in, from its kind.
func stageNow(def content.SpecialProjectDef, pr *application.SpecialProject) (content.ProjectStageDef, bool) {
	if pr.Stage < 1 || pr.Stage > len(def.Stages) {
		return content.ProjectStageDef{}, false
	}
	return def.Stages[pr.Stage-1], true
}

func domainStage(st content.ProjectStageDef) project.Stage {
	return project.Stage{Materials: st.Materials, LabourHours: st.LabourHours, MinHours: st.MinHours}
}

// stageGate refuses an act on a stage whose extra research the settlement lacks.
func (h *VillageHandler) stageGate(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, st content.ProjectStageDef) error {
	if missing, err := knowledgeMissing(ctx, tx, s.CityID, st.Knowledge); err != nil {
		return err
	} else if len(missing) > 0 {
		return researchRefusal(village.ProjectResearch, village.AddrProject, missing, snap)
	}
	return nil
}

// projectDeliver puts materials on the site: from the treasury's stock (the holder of project.fund) or from what a resident carries.
func (h *VillageHandler) projectDeliver(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool,
	p *application.Player, req VillageProjectRequest, give bool, now time.Time,
) (string, error) {
	var pr *application.SpecialProject
	var err error
	if give {
		// anyone standing at the site may bring what they carry, from wherever they come
		if pr, s, err = h.siteOf(ctx, tx, strings.TrimSpace(req.ID)); err != nil {
			return "", err
		}
		if ok, err := h.presentHere(ctx, tx, p, s); err != nil {
			return "", err
		} else if !ok {
			return "", refuseVillage(village.ProjectNotHere, village.AddrProject)
		}
	} else {
		if !has {
			return "", refuseVillage(village.VillageNoSettlement)
		}
		if _, err := requirePermission(ctx, tx, s, p.ID, charter.ProjectFund); err != nil {
			return "", err
		}
		if pr, err = h.loadActive(ctx, tx, s, strings.TrimSpace(req.ID)); err != nil {
			return "", err
		}
	}
	def, _ := snap.SpecialProject(pr.Kind)
	st, ok := stageNow(def, pr)
	if !ok {
		return "", refuseVillage(village.ProjectNotActive, village.AddrProject)
	}
	if err := h.stageGate(ctx, tx, snap, s, st); err != nil {
		return "", err
	}
	item := strings.TrimSpace(req.Code)
	qty := parseQty(req.N)
	stock, err := tx.Projects().Stock(ctx, pr.ID)
	if err != nil {
		return "", err
	}
	if qty > project.Missing(domainStage(st), stock)[item] {
		return "", refuseVillage(village.ProjectNotNeeded, village.AddrProject)
	}
	org := application.SettlementOrg(s.CityID)
	mv := application.ItemMove{ID: h.ids.NewID(), Item: item, Qty: qty, Reason: application.ItemProjectMaterials, ReferenceType: application.ProjectReference, ReferenceID: pr.ID, At: now}
	if give {
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return "", err
		}
		mv.From, mv.FromHolding = p.ID, application.HoldCarried
	} else {
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return "", err
		}
		mv.FromOrg, mv.FromHolding = org, application.HoldWarehouse
	}
	if err := tx.Items().Move(ctx, mv); err != nil {
		if stderrors.Is(err, application.ErrNotEnoughItems) {
			return "", refuseVillage(village.ProjectNoStock, village.AddrProject)
		}
		return "", err
	}
	if err := tx.Projects().AddStock(ctx, pr.ID, item, qty); err != nil {
		return "", err
	}
	if err := h.beginStage(ctx, tx, pr, st, now); err != nil {
		return "", err
	}
	done, err := h.projectAdvance(ctx, tx, meta, snap, s, pr, now)
	if err != nil {
		return "", err
	}
	if done {
		return village.ProjectNoticeStage, nil
	}
	return village.ProjectNoticeDelivered, nil
}

// runningHours are the hours of the shifts now running on the current stage.
func runningHours(shifts []application.ProjectShift, stage int) int64 {
	var n int64
	for _, sh := range shifts {
		if sh.Status == "running" && sh.Stage == stage {
			n += sh.Hours
		}
	}
	return n
}

func (h *VillageHandler) projectWork(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool,
	p *application.Player, req VillageProjectRequest, now time.Time,
) error {
	// the crew goes to the site wherever it comes from: a company of another town works a let project in the town it stands in
	pr, s, err := h.siteOf(ctx, tx, strings.TrimSpace(req.ID))
	if err != nil {
		return err
	}
	if ok, err := h.presentHere(ctx, tx, p, s); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.ProjectNotHere, village.AddrProject)
	}
	def, _ := snap.SpecialProject(pr.Kind)
	st, ok := stageNow(def, pr)
	if !ok {
		return refuseVillage(village.ProjectNotActive, village.AddrProject)
	}
	if err := h.stageGate(ctx, tx, snap, s, st); err != nil {
		return err
	}
	shifts, err := tx.Projects().ShiftsOf(ctx, pr.ID, 100)
	if err != nil {
		return err
	}
	tune := h.special.Projects
	if pr.StageHours+runningHours(shifts, pr.Stage) >= st.LabourHours {
		return refuseVillage(village.ProjectCrewFull, village.AddrProject)
	}
	// who pays the crew: the contractor, when the project is let; otherwise the fund
	payerKind, payerID := "fund", pr.ID
	c, err := tx.Projects().ContractOf(ctx, pr.ID)
	if err != nil {
		return err
	}
	wage := project.ShiftWage(tune.ShiftHours, tune.WagePerHour)
	var payer application.Account
	if c != nil && c.Status == "awarded" {
		co, err := tx.Companies().ByID(ctx, c.CompanyID)
		if err != nil || co == nil {
			return refuseVillage(village.ProjectNoCrew, village.AddrProject)
		}
		mine, err := ownsCompany(ctx, tx, p.ID, co.Code)
		if err != nil {
			return err
		}
		if mine == nil {
			return refuseVillage(village.ProjectNoCrew, village.AddrProject)
		}
		payerKind, payerID = "company", co.ID
		if payer, err = tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, co.ID); err != nil {
			return err
		}
	} else if c != nil && c.Status == "open" {
		// while bids are taken the crew waits for the winner
		return refuseVillage(village.ProjectHasContract, village.AddrProject)
	} else if payer, err = fundOf(ctx, tx, pr.ID); err != nil {
		return err
	}
	var committed int64
	for _, sh := range shifts {
		if sh.Status == "running" && sh.PayerKind == payerKind && sh.PayerID == payerID {
			committed += sh.Wage
		}
	}
	if bal, err := accountBalance(ctx, tx, payer); err != nil {
		return err
	} else if bal < committed+wage {
		return refuseVillage(village.ProjectFundShort, village.AddrProject)
	}
	sh := application.ProjectShift{ID: h.ids.NewID(), ProjectID: pr.ID, Stage: pr.Stage, PlayerID: p.ID, Hours: tune.ShiftHours, Wage: wage, PayerKind: payerKind, PayerID: payerID,
		StartedAt: now, EndsAt: now.Add(time.Duration(tune.ShiftMinutes) * time.Minute)}
	if ok, err := tx.Projects().StartShift(ctx, sh); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.ProjectBusy, village.AddrProject)
	}
	if err := h.beginStage(ctx, tx, pr, st, now); err != nil {
		return err
	}
	return h.schedulePolity(ctx, tx, application.TickProjectShift, application.ProjectReference, sh.ID, "", p.ID, now, sh.EndsAt)
}

// projectHire takes hours of the pool's labour into the stage, paid to the sink: a few hours a day.
func (h *VillageHandler) projectHire(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool,
	p *application.Player, req VillageProjectRequest, now time.Time,
) (string, error) {
	if !has {
		return "", refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.ProjectFund); err != nil {
		return "", err
	}
	pr, err := h.loadActive(ctx, tx, s, strings.TrimSpace(req.ID))
	if err != nil {
		return "", err
	}
	if c, err := tx.Projects().ContractOf(ctx, pr.ID); err != nil {
		return "", err
	} else if c != nil {
		return "", refuseVillage(village.ProjectNoCrew, village.AddrProject)
	}
	def, _ := snap.SpecialProject(pr.Kind)
	st, ok := stageNow(def, pr)
	if !ok {
		return "", refuseVillage(village.ProjectNotActive, village.AddrProject)
	}
	if err := h.stageGate(ctx, tx, snap, s, st); err != nil {
		return "", err
	}
	tune := h.special.Projects
	day := gameDayNumber(now)
	hired, err := tx.Projects().NPCHoursOn(ctx, pr.ID, day)
	if err != nil {
		return "", err
	}
	shifts, err := tx.Projects().ShiftsOf(ctx, pr.ID, 100)
	if err != nil {
		return "", err
	}
	need := st.LabourHours - pr.StageHours - runningHours(shifts, pr.Stage)
	hours := project.NPCHours(tune.NPCHoursPerDay, hired, min(parseQty(req.N), max(need, 0)))
	if hours < 1 {
		return "", refuseVillage(village.ProjectNoHours, village.AddrProject)
	}
	wage := project.ShiftWage(hours, tune.NPCWagePerHour)
	fund, err := fundOf(ctx, tx, pr.ID)
	if err != nil {
		return "", err
	}
	if bal, err := accountBalance(ctx, tx, fund); err != nil {
		return "", err
	} else if bal < wage {
		return "", refuseVillage(village.ProjectFundShort, village.AddrProject)
	}
	if _, err := move(ctx, tx, fund.ID, application.SystemSinkAccountID, wage, application.ReasonProjectStagePay, application.ProjectReference, pr.ID, "", now); err != nil {
		return "", err
	}
	if err := tx.Projects().AddNPCHours(ctx, pr.ID, day, pr.Stage, hours, wage); err != nil {
		return "", err
	}
	if err := tx.Projects().AddHours(ctx, pr.ID, hours); err != nil {
		return "", err
	}
	pr.StageHours += hours
	if err := h.beginStage(ctx, tx, pr, st, now); err != nil {
		return "", err
	}
	done, err := h.projectAdvance(ctx, tx, meta, snap, s, pr, now)
	if err != nil {
		return "", err
	}
	if done {
		return village.ProjectNoticeStage, nil
	}
	return village.ProjectNoticeHired, nil
}

// tickProjectShift ends a crew shift: the wage is paid, the hours are counted, the stage is looked at. Exactly once (EndShift is the fence).
func (h *VillageHandler) tickProjectShift(ctx context.Context, tx application.Tx, meta envelope.Metadata, shiftID string, now time.Time) error {
	snap := h.content.Current()
	sh, err := tx.Projects().Shift(ctx, shiftID, false)
	if err != nil || sh == nil || sh.Status != "running" {
		return err
	}
	// the project first, then the shift: the order every act takes
	pr, err := tx.Projects().ByID(ctx, sh.ProjectID, true)
	if err != nil || pr == nil {
		return err
	}
	sh, err = tx.Projects().Shift(ctx, shiftID, true)
	if err != nil || sh == nil || sh.Status != "running" {
		return err
	}
	if now.Before(sh.EndsAt.Add(-time.Second)) {
		return h.schedulePolity(ctx, tx, application.TickProjectShift, application.ProjectReference, sh.ID, "", sh.PlayerID, now, sh.EndsAt)
	}
	var txID string
	if pr.Active() && sh.Wage > 0 {
		var payer application.Account
		if sh.PayerKind == "company" {
			payer, err = tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, sh.PayerID)
		} else {
			payer, err = fundOf(ctx, tx, pr.ID)
		}
		if err != nil {
			return err
		}
		bal, err := accountBalance(ctx, tx, payer)
		if err != nil {
			return err
		}
		pay := min(sh.Wage, bal)
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, sh.PlayerID)
		if err != nil {
			return err
		}
		if txID, err = move(ctx, tx, payer.ID, cash.ID, pay, application.ReasonProjectStagePay, application.ProjectReference, pr.ID, "", now); err != nil {
			return err
		}
	}
	if ok, err := tx.Projects().EndShift(ctx, sh.ID, txID, now); err != nil || !ok {
		return err
	}
	if !pr.Active() || sh.Stage != pr.Stage {
		return nil
	}
	if err := tx.Projects().AddHours(ctx, pr.ID, sh.Hours); err != nil {
		return err
	}
	pr.StageHours += sh.Hours
	s, err := tx.Settlements().ByID(ctx, pr.SettlementID)
	if err != nil {
		return err
	}
	_, err = h.projectAdvance(ctx, tx, meta, snap, s, pr, now)
	return err
}

// projectAdvance accepts the stage in hand if everything it asks is there. The stage number is the fence: a replay finishes nothing twice.
func (h *VillageHandler) projectAdvance(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	pr *application.SpecialProject, now time.Time,
) (bool, error) {
	def, ok := snap.SpecialProject(pr.Kind)
	if !ok || !pr.Active() {
		return false, nil
	}
	st, ok := stageNow(def, pr)
	if !ok {
		return false, nil
	}
	if missing, err := knowledgeMissing(ctx, tx, s.CityID, st.Knowledge); err != nil || len(missing) > 0 {
		return false, err
	}
	stock, err := tx.Projects().Stock(ctx, pr.ID)
	if err != nil {
		return false, err
	}
	var started, delayed time.Time
	if pr.StageStartedAt != nil {
		started = *pr.StageStartedAt
	}
	if pr.DelayedUntil != nil {
		delayed = *pr.DelayedUntil
	}
	if project.Check(domainStage(st), stock, pr.StageHours, started, delayed, now) != project.Ready {
		return false, nil
	}
	stageNo := pr.Stage
	finish := stageNo == pr.Stages
	if ok, err := tx.Projects().CompleteStage(ctx, pr.ID, stageNo, pr.StageHours, finish, now); err != nil || !ok {
		return false, err
	}
	if len(st.Materials) > 0 {
		if ok, err := tx.Projects().TakeStock(ctx, pr.ID, st.Materials); err != nil {
			return false, err
		} else if !ok {
			return false, errProjectStock
		}
	}
	if err := h.contractStageDone(ctx, tx, meta, pr, stageNo, now); err != nil {
		return false, err
	}
	name := "project_stage_done"
	if finish {
		name = "project_finished"
	}
	return true, specialEvent(ctx, tx, meta, name, s.CityID, map[string]any{"project": pr.ID, "kind": pr.Kind, "stage": stageNo})
}

var errProjectStock = stderrors.New("handlers: the project's stock does not hold the stage it was judged ready for")

// projectCancel ends a project before the limit stage: what is in the fund returns pro rata to those who gave it. Materials already built in
// stay in the site.
func (h *VillageHandler) projectCancel(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, has bool,
	p *application.Player, req VillageProjectRequest, now time.Time,
) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.ProjectStart); err != nil {
		return err
	}
	pr, err := h.loadActive(ctx, tx, s, strings.TrimSpace(req.ID))
	if err != nil {
		return err
	}
	if !project.CancelOpen(pr.Stage, h.special.Projects.CancelBeforeStage) {
		return refuseVillage(village.ProjectTooLate, village.AddrProject)
	}
	if c, err := tx.Projects().ContractOf(ctx, pr.ID); err != nil {
		return err
	} else if c != nil {
		return refuseVillage(village.ProjectHasContract, village.AddrProject)
	}
	if running, err := tx.Projects().ShiftsOf(ctx, pr.ID, 100); err != nil {
		return err
	} else if runningHours(running, pr.Stage) > 0 {
		return refuseVillage(village.ProjectBusy, village.AddrProject)
	}
	if ok, err := tx.Projects().Cancel(ctx, pr.ID, now); err != nil || !ok {
		return err
	}
	fund, err := fundOf(ctx, tx, pr.ID)
	if err != nil {
		return err
	}
	bal, err := accountBalance(ctx, tx, fund)
	if err != nil {
		return err
	}
	pledges, err := tx.Projects().Pledges(ctx, pr.ID)
	if err != nil {
		return err
	}
	var held []project.Pledge
	byID := map[string]application.ProjectPledge{}
	for _, pl := range pledges {
		if pl.Status == "held" {
			held = append(held, project.Pledge{ID: pl.ID, Amount: pl.Amount})
			byID[pl.ID] = pl
		}
	}
	shares := project.Refunds(bal, held)
	ids := make([]string, 0, len(shares))
	for id := range shares {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pl := byID[id]
		var to application.Account
		if pl.Kind == "allocation" {
			to, err = tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		} else {
			to, err = tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, pl.PlayerID)
		}
		if err != nil {
			return err
		}
		if _, err := move(ctx, tx, fund.ID, to.ID, shares[id], application.ReasonProjectPledgeRefund, application.ProjectReference, pr.ID, "", now); err != nil {
			return err
		}
		if _, err := tx.Projects().RefundPledge(ctx, id, now); err != nil {
			return err
		}
	}
	return specialEvent(ctx, tx, meta, "project_cancelled", s.CityID, map[string]any{"project": pr.ID, "by": p.ID})
}

// beginStage marks the stage in hand begun, once, and puts the end of its least time on the clock: a stage whose materials and crew are all there
// still waits until then, and something must look at it when the time is over.
func (h *VillageHandler) beginStage(ctx context.Context, tx application.Tx, pr *application.SpecialProject, st content.ProjectStageDef, now time.Time) error {
	if pr.StageStartedAt != nil {
		return nil
	}
	if err := tx.Projects().StartStage(ctx, pr.ID, now); err != nil {
		return err
	}
	pr.StageStartedAt = &now
	if st.MinHours > 0 {
		return h.schedulePolity(ctx, tx, application.TickProjectStage, application.ProjectReference, pr.ID, strconv.Itoa(pr.Stage), pr.StartedBy, now, now.Add(time.Duration(st.MinHours)*time.Hour))
	}
	return nil
}

// tickProjectStage looks at a stage when its least time (or a delay done to it) is over. The stage number is the fence: a tick for a stage that
// has been done already finds the project in the next one and changes nothing.
func (h *VillageHandler) tickProjectStage(ctx context.Context, tx application.Tx, meta envelope.Metadata, id, aux string, now time.Time) error {
	pr, err := tx.Projects().ByID(ctx, id, true)
	if err != nil || pr == nil || !pr.Active() {
		return err
	}
	if n, perr := strconv.Atoi(aux); perr == nil && n != pr.Stage {
		return nil
	}
	s, err := tx.Settlements().ByID(ctx, pr.SettlementID)
	if err != nil {
		return err
	}
	_, err = h.projectAdvance(ctx, tx, meta, h.content.Current(), s, pr, now)
	return err
}

// siteOf reads an active project by its id under its lock, with the settlement it stands in.
func (h *VillageHandler) siteOf(ctx context.Context, tx application.Tx, id string) (*application.SpecialProject, application.FoundedSettlement, error) {
	pr, err := tx.Projects().ByID(ctx, id, true)
	if err != nil {
		return nil, application.FoundedSettlement{}, err
	}
	if pr == nil {
		return nil, application.FoundedSettlement{}, refuseVillage(village.ProjectNoProject, village.AddrProject)
	}
	if !pr.Active() {
		return nil, application.FoundedSettlement{}, refuseVillage(village.ProjectNotActive, village.AddrProject)
	}
	s, err := tx.Settlements().ByID(ctx, pr.SettlementID)
	return pr, s, err
}

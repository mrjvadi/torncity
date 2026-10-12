package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/project"
	"github.com/mrjvadi/torncity/internal/domain/tender"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// projectView builds the project screen: what the settlement could start, its projects with what each stage waits for, the donors, the crews and
// the tender, and the open tenders a company of the viewer could bid on.
func (h *VillageHandler) projectView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player,
	openID string,
) (village.ProjectView, error) {
	v := village.ProjectView{Open: h.projectsOpen(), Fee: h.special.Projects.StartFee, PledgeMin: h.special.Projects.PledgeMin, ShiftMinutes: h.special.Projects.ShiftMinutes}
	if has {
		v.Village = s.Name
	}
	if !v.Open {
		return v, nil
	}
	comps, err := h.contractorCompanies(ctx, tx, p)
	if err != nil {
		return v, err
	}
	v.Companies = comps
	if !has {
		if openID != "" {
			if err := h.openForeign(ctx, tx, snap, p, openID, nil, &v); err != nil {
				return v, err
			}
		}
		return v, h.projectBoard(ctx, tx, p, &v)
	}
	mayStart, mayFund, mayAccept, mayTender := hasPermission(ctx, tx, s, p.ID, charter.ProjectStart), hasPermission(ctx, tx, s, p.ID, charter.ProjectFund),
		hasPermission(ctx, tx, s, p.ID, charter.ProjectAccept), hasPermission(ctx, tx, s, p.ID, charter.WorksTender)
	v.MayStart = mayStart
	for _, d := range snap.SpecialProjects() {
		missing, err := knowledgeMissing(ctx, tx, s.CityID, needsKnowledge(d.Requires))
		if err != nil {
			return v, err
		}
		v.Kinds = append(v.Kinds, village.ProjectKindLine{Code: d.Code, Name: d.Name, Stages: len(d.Stages), Effect: d.Effect, EffectName: d.EffectName, Available: len(missing) == 0, Missing: missing})
	}
	projects, err := tx.Projects().OfSettlement(ctx, s.CityID, 20)
	if err != nil {
		return v, err
	}
	var lastDay *application.ServiceDay
	if hasFinished(projects) {
		lastDay, _ = tx.ServiceDays().Last(ctx, s.CityID)
	}
	here, err := h.presentHere(ctx, tx, p, s)
	if err != nil {
		return v, err
	}
	running, err := tx.Projects().RunningShift(ctx, p.ID)
	if err != nil {
		return v, err
	}
	for _, pr := range projects {
		line, err := h.projectLine(ctx, tx, snap, s, p, pr, lastDay, projectFlags{fund: mayFund, start: mayStart, accept: mayAccept, tender: mayTender, here: here, busy: running != nil})
		if err != nil {
			return v, err
		}
		v.Projects = append(v.Projects, line)
	}
	// a work of another town, opened by name: anyone may look at it and give to it
	if openID != "" {
		found := false
		for _, l := range v.Projects {
			found = found || l.ID == openID
		}
		if !found {
			if err := h.openForeign(ctx, tx, snap, p, openID, lastDay, &v); err != nil {
				return v, err
			}
		}
	}
	return v, h.projectBoard(ctx, tx, p, &v)
}

// openForeign adds the line of a work of another town to the screen, for looking and for giving.
func (h *VillageHandler) openForeign(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, id string, lastDay *application.ServiceDay, v *village.ProjectView) error {
	pr, err := tx.Projects().ByID(ctx, id, false)
	if err != nil || pr == nil {
		return err
	}
	owner, err := tx.Settlements().ByID(ctx, pr.SettlementID)
	if err != nil {
		return err
	}
	line, err := h.projectLine(ctx, tx, snap, owner, p, *pr, lastDay, projectFlags{})
	if err != nil {
		return err
	}
	v.Projects = append(v.Projects, line)
	return nil
}

func hasFinished(ps []application.SpecialProject) bool {
	for _, p := range ps {
		if p.Status == "finished" {
			return true
		}
	}
	return false
}

type projectFlags struct{ fund, start, accept, tender, here, busy bool }

func (h *VillageHandler) projectLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, viewer *application.Player,
	pr application.SpecialProject, lastDay *application.ServiceDay, f projectFlags,
) (village.ProjectLine, error) {
	def, _ := snap.SpecialProject(pr.Kind)
	l := village.ProjectLine{ID: pr.ID, Kind: presentation.Named{Code: pr.Kind, Name: def.Name}, Effect: def.Effect, Status: pr.Status, Stage: pr.Stage, Stages: pr.Stages,
		Hours: pr.StageHours, MinUntil: nil, Delay: pr.DelayedUntil, ShiftHours: h.special.Projects.ShiftHours, HirePerDay: h.special.Projects.NPCHoursPerDay}
	fund, err := fundOf(ctx, tx, pr.ID)
	if err != nil {
		return l, err
	}
	if l.Fund, err = accountBalance(ctx, tx, fund); err != nil {
		return l, err
	}
	pledges, err := tx.Projects().Pledges(ctx, pr.ID)
	if err != nil {
		return l, err
	}
	refundable := pr.Active() && project.Refundable(pr.Stage, pr.StageStartedAt != nil)
	for _, pl := range pledges {
		d := village.DonorLine{ID: pl.ID, Kind: pl.Kind, Amount: pl.Amount, At: pl.At, Status: pl.Status, Anonymous: pl.Anonymous, Mine: pl.PlayerID == viewer.ID}
		if pl.Kind == "pledge" && (!pl.Anonymous || d.Mine) {
			d.Name = h.playerName(ctx, tx, pl.PlayerID)
		}
		d.Refunds = d.Mine && pl.Kind == "pledge" && pl.Status == "held" && refundable
		l.Donors = append(l.Donors, d)
	}
	stages, err := tx.Projects().Stages(ctx, pr.ID)
	if err != nil {
		return l, err
	}
	for _, st := range stages {
		l.Done = append(l.Done, village.ProjectStageLine{Stage: st.Stage, Hours: st.Hours, At: st.CompletedAt})
	}
	if pr.Status == "finished" && lastDay != nil {
		l.Held = lastDay.HeldPost(pr.ID)
		for _, post := range lastDay.Posts {
			if post.BuildingID == pr.ID {
				l.Idle = post.Idle
			}
		}
	}
	if !pr.Active() {
		return l, nil
	}
	st, ok := stageNow(def, &pr)
	if !ok {
		return l, nil
	}
	l.NeedHour = st.LabourHours
	stock, err := tx.Projects().Stock(ctx, pr.ID)
	if err != nil {
		return l, err
	}
	for item, need := range st.Materials {
		l.Materials = append(l.Materials, village.ProjectMaterialLine{Item: item, Need: need, Have: min(stock[item], need)})
	}
	sort.Slice(l.Materials, func(i, j int) bool { return l.Materials[i].Item < l.Materials[j].Item })
	var started, delayed time.Time
	if pr.StageStartedAt != nil {
		started = *pr.StageStartedAt
		until := started.Add(time.Duration(st.MinHours) * time.Hour)
		l.MinUntil = &until
	}
	if pr.DelayedUntil != nil {
		delayed = *pr.DelayedUntil
	}
	if missing, err := knowledgeMissing(ctx, tx, s.CityID, st.Knowledge); err != nil {
		return l, err
	} else if len(missing) > 0 {
		l.Waiting = village.ProjectWaitResearch
	} else {
		l.Waiting = string(project.Check(domainStage(st), stock, pr.StageHours, started, delayed, h.now()))
	}
	shifts, err := tx.Projects().ShiftsOf(ctx, pr.ID, 20)
	if err != nil {
		return l, err
	}
	for _, sh := range shifts {
		line := village.ProjectShiftLine{ID: sh.ID, Player: presentation.Named{Code: sh.PlayerID, Name: h.playerName(ctx, tx, sh.PlayerID)}, Hours: sh.Hours, Status: sh.Status, EndsAt: sh.EndsAt, Mine: sh.PlayerID == viewer.ID}
		l.Shifts = append(l.Shifts, line)
		if line.Mine && sh.Status == "running" {
			cp := line
			l.MyShift = &cp
		}
	}
	c, err := tx.Projects().ContractOf(ctx, pr.ID)
	if err != nil {
		return l, err
	}
	if c != nil {
		cl, err := h.contractLine(ctx, tx, *c, viewer, f.tender || f.start)
		if err != nil {
			return l, err
		}
		l.Contract = &cl
	}
	hired, err := tx.Projects().NPCHoursOn(ctx, pr.ID, gameDayNumber(h.now()))
	if err != nil {
		return l, err
	}
	l.HiredToday = hired
	l.MayPledge = true
	l.MayFund = f.fund
	l.MayCancel = f.start && project.CancelOpen(pr.Stage, h.special.Projects.CancelBeforeStage) && c == nil
	l.MayWork = f.here && !f.busy && l.MyShift == nil && pr.StageHours+runningHours(shifts, pr.Stage) < st.LabourHours && (c == nil || c.Status == "awarded")
	l.MayHire = f.fund && c == nil && pr.StageHours < st.LabourHours
	l.MayTender = f.tender && c == nil
	return l, nil
}

func (h *VillageHandler) contractLine(ctx context.Context, tx application.Tx, c application.ProjectContract, viewer *application.Player, mayAward bool) (village.ProjectContractLine, error) {
	l := village.ProjectContractLine{ID: c.ID, Status: c.Status, Rule: c.Rule, BudgetCap: c.BudgetCap, BondBPS: c.BondBPS, RetentionBPS: c.RetentionBPS, DefectsDays: c.DefectsDays, DueDays: c.DueDays,
		ClosesAt: c.ClosesAt, Price: c.Price, DueAt: c.DueAt, StagesPaid: c.StagesPaid, RetentionHeld: c.RetentionHeld}
	if c.CompanyID != "" {
		if co, err := tx.Companies().ByID(ctx, c.CompanyID); err == nil && co != nil {
			l.Company = &presentation.Named{Code: co.Code, Name: co.Name}
		}
	}
	bids, err := tx.Projects().ContractBids(ctx, c.ID)
	if err != nil {
		return l, err
	}
	for _, b := range bids {
		bl := village.ProjectBidLine{ID: b.ID, Price: b.Price, Days: b.Days, Record: b.Record, Bond: b.Bond, Status: b.Status, Mine: b.BidBy == viewer.ID}
		if co, err := tx.Companies().ByID(ctx, b.CompanyID); err == nil && co != nil {
			bl.Company = presentation.Named{Code: co.Code, Name: co.Name}
		}
		l.Bids = append(l.Bids, bl)
	}
	now := h.now()
	l.MayAward = mayAward && c.Status == "open" && !now.Before(c.ClosesAt)
	l.MayCancel = false
	l.MayDefect = mayAward && c.Status == "awarded" && c.LastStageAt != nil && now.Before(tender.RetentionReleaseAt(*c.LastStageAt, int64(c.DefectsDays)))
	return l, nil
}

// contractorCompanies are the viewer's companies that do public works, with their record and any debarment.
func (h *VillageHandler) contractorCompanies(ctx context.Context, tx application.Tx, p *application.Player) ([]village.TenderCompanyLine, error) {
	mine, err := tx.Companies().Of(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var out []village.TenderCompanyLine
	for _, co := range mine {
		if co.TypeCode != "works_contractor" {
			continue
		}
		rec, err := tx.Tenders().Record(ctx, co.ID)
		if err != nil {
			return nil, err
		}
		line := village.TenderCompanyLine{Company: presentation.Named{Code: co.Code, Name: co.Name}, Record: rec}
		if until, err := tx.Tenders().DebarredUntil(ctx, co.ID); err == nil && tender.Debarred(until, h.now()) {
			u := until
			line.DebarredUntil = &u
		}
		out = append(out, line)
	}
	return out, nil
}

// projectBoard lists the open tenders of projects anywhere that a company of the viewer could bid on.
func (h *VillageHandler) projectBoard(ctx context.Context, tx application.Tx, p *application.Player, v *village.ProjectView) error {
	if len(v.Companies) == 0 {
		return nil
	}
	open, err := tx.Projects().OpenContracts(ctx, 20)
	if err != nil {
		return err
	}
	snap := h.content.Current()
	for _, c := range open {
		pr, err := tx.Projects().ByID(ctx, c.ProjectID, false)
		if err != nil || pr == nil {
			continue
		}
		st, err := tx.Settlements().ByID(ctx, pr.SettlementID)
		if err != nil {
			continue
		}
		cl, err := h.contractLine(ctx, tx, c, p, false)
		if err != nil {
			return err
		}
		cl.MayBid = !h.now().After(c.ClosesAt)
		def, _ := snap.SpecialProject(pr.Kind)
		v.Board = append(v.Board, village.ProjectBoardLine{Contract: cl, Project: presentation.Named{Code: pr.ID, Name: def.Name}, Settlement: presentation.Named{Code: st.Code, Name: st.Name}})
	}
	return nil
}

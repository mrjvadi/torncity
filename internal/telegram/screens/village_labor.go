package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The labour market's screens (docs/adr/0037-labor-market.md): the hiring
// board, a construction site, and a worker's own status. Group screens like
// the rest of the village; the numbers are computed by the use case.

// Structured screens (clients).
const (
	ScreenLaborBoard = village.ScreenLaborBoard
	ScreenLaborSite  = village.ScreenLaborSite
	ScreenLaborMine  = village.ScreenLaborMine
)

// LaborBoard renders the hiring board.
func LaborBoard(c Context, v LaborBoardView) *presenter.Response {
	return c.withView(renderLaborBoard(c, v), ScreenLaborBoard, v)
}

// LaborSite renders a construction site.
func LaborSite(c Context, v LaborSiteView) *presenter.Response {
	return c.withView(renderLaborSite(c, v), ScreenLaborSite, v)
}

// LaborMine renders the viewer's status.
func LaborMine(c Context, v LaborMineView) *presenter.Response {
	return c.withView(renderLaborMine(c, v), ScreenLaborMine, v)
}

func (c Context) laborLevel(code string) string { return c.T("village.labor.level."+code, nil) }

func laborMarketText(c Context, m LaborMarketLine) string {
	return c.T("village.labor.market."+m.Level, map[string]any{
		"pool": m.Pool, "available": m.Available, "working": m.Working, "wage": FormatMoney(c, m.NPCWage),
		"min": FormatMoney(c, m.MinWage), "housing": m.Housing, "percent": m.TightnessBPS / 100,
	})
}

// workHours is a worker-minutes figure as hours with one decimal.
func workHours(minutes int64) string {
	return strconv.FormatFloat(float64(minutes)/60, 'f', 1, 64)
}

// workLeft is a worker-minutes figure as "worker-hours" text.
func workLeft(c Context, minutes int64) string {
	return c.T("village.labor.work_left", map[string]any{"hours": workHours(minutes)})
}

func laborJobText(c Context, j LaborJobLine) string {
	employer := c.T("village.labor.employer.village", nil)
	if j.EmployerKind == "player" {
		employer = c.T("village.labor.employer.player", map[string]any{"name": j.Employer})
	}
	key := "village.labor.job.construction"
	if j.Kind == "production" {
		key = "village.labor.job.production"
	}
	return c.T(key, map[string]any{
		"building": c.SettlementBuildingName(j.Building), "wage": FormatMoney(c, j.Wage), "employer": employer,
		"percent": j.ProgressBPS / 100, "left": workLeft(c, j.LeftMinutes), "shifts": j.Left, "workers": j.Workers,
	})
}

func shiftText(c Context, s LaborShiftLine) string {
	who := s.Worker
	if s.WorkerNPC {
		who = c.T("village.labor.npc_worker", nil)
	}
	return c.T("village.labor.worker", map[string]any{"name": who, "time": FormatClock(c, s.FinishAt), "duration": FormatDuration(c, s.Left)})
}

func renderLaborBoard(c Context, v LaborBoardView) *presenter.Response {
	kb := keyboards.New()
	var lines []string
	for _, j := range v.Jobs {
		lines = append(lines, laborJobText(c, j))
		label := c.T("village.labor.button.job", map[string]any{"building": c.SettlementBuildingName(j.Building), "wage": FormatMoney(c, j.Wage)})
		if b, ok := keyboards.Button(label, AddrLaborSite, j.BuildingID); ok {
			kb.Row(b)
		}
	}
	jobs := body(lines...)
	if jobs == "" {
		jobs = c.T("village.labor.board.empty", nil)
	}
	var working string
	if v.Working != nil {
		working = c.T("village.labor.board.working", map[string]any{
			"building": c.SettlementBuildingName(v.Working.Building), "time": FormatClock(c, v.Working.FinishAt),
			"duration": FormatDuration(c, v.Working.Left), "wage": FormatMoney(c, v.Working.Wage),
		})
	}
	var sites string
	if len(v.Sites) > 0 {
		var names []string
		for _, s := range v.Sites {
			names = append(names, c.T("village.labor.board.site_item", map[string]any{"building": c.SettlementBuildingName(s.Building), "percent": s.ProgressBPS / 100}))
			if b, ok := keyboards.Button(c.T("village.labor.button.post", map[string]any{"building": c.SettlementBuildingName(s.Building)}),
				AddrLaborPost, s.ID); ok {
				kb.Row(b)
			}
		}
		sites = c.T("village.labor.board.sites", map[string]any{"names": c.list(names)})
	}
	var notResident string
	if !v.Resident {
		notResident = c.T("village.labor.not_resident", nil)
	}
	text := paragraphs(
		c.T("village.labor.board.title", map[string]any{"village": v.Village}),
		working, jobs, sites, laborMarketText(c, v.Market), notResident, c.T("village.labor.board.hint", nil),
	)
	kb.Row(villageButtons(c, "village.labor.button.mine", AddrLaborMine, "village.button.build", AddrBuildMenu)...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrLaborBoard}))
	return c.respond(text, kb.Build())
}

func renderLaborSite(c Context, v LaborSiteView) *presenter.Response {
	kb := keyboards.New()
	name := c.SettlementBuildingName(v.Building)
	head := c.T("village.labor.site.title", map[string]any{"building": name, "village": v.Village})
	var progress string
	if v.Status == "complete" {
		progress = c.T("village.labor.site.done", map[string]any{"building": name})
	} else {
		progress = c.T("village.labor.site.progress", map[string]any{
			"percent": v.ProgressBPS / 100, "left": workHours(v.LeftMinutes), "done": workHours(v.DoneMinutes), "total": workHours(v.RequiredMinutes),
		})
	}
	var just string
	if v.Just != "" {
		just = c.T("village.labor.just."+v.Just, nil)
	}
	var crew string
	if len(v.Workers) > 0 {
		var lines []string
		for _, w := range v.Workers {
			lines = append(lines, shiftText(c, w))
		}
		crew = c.T("village.labor.site.workers", map[string]any{"count": len(v.Workers), "list": body(lines...)})
	} else if v.Status != "complete" {
		crew = c.T("village.labor.site.no_workers", nil)
	}
	var job string
	if v.Job != nil {
		job = laborJobText(c, *v.Job)
		if v.Job.NPCCrew > 0 {
			job += "\n" + c.T("village.labor.site.crew", map[string]any{"crew": v.Job.NPCCrew})
		}
	} else if v.Status != "complete" {
		job = c.T("village.labor.site.no_job", nil)
	}
	if v.Working != nil {
		job = paragraphs(c.T("village.labor.board.working", map[string]any{
			"building": name, "time": FormatClock(c, v.Working.FinishAt), "duration": FormatDuration(c, v.Working.Left),
			"wage": FormatMoney(c, v.Working.Wage),
		}), job)
	}
	jobID := ""
	if v.Job != nil {
		jobID = v.Job.ID
	}
	if v.CanWork && jobID != "" {
		if b, ok := keyboards.Button(c.T("village.labor.button.work", map[string]any{"wage": FormatMoney(c, v.WorkWage)}), AddrLaborTake, jobID); ok {
			kb.Row(b)
		}
	}
	if v.CanEmploy && jobID != "" {
		var row []presenter.Button
		for _, n := range v.HirePresets {
			label := c.T("village.labor.button.hire", map[string]any{"count": n})
			if b, ok := keyboards.Button(label, AddrLaborHire, jobID, strconv.Itoa(n)); ok {
				row = append(row, b)
			}
		}
		if len(row) > 0 {
			kb.Row(row...)
		}
		var wrow []presenter.Button
		for _, p := range v.WagePresets {
			label := c.T("village.labor.button.wage", map[string]any{"wage": FormatMoney(c, p.Wage)})
			if b, ok := keyboards.Button(label, AddrLaborWage, jobID, strconv.Itoa(p.Percent)); ok {
				wrow = append(wrow, b)
			}
		}
		if len(wrow) > 0 {
			kb.Row(wrow...)
		}
		if b, ok := keyboards.Button(c.T("village.labor.button.close", nil), AddrLaborClose, jobID); ok {
			kb.Row(b)
		}
	}
	if v.CanPost {
		if b, ok := keyboards.Button(c.T("village.labor.button.post", map[string]any{"building": name}), AddrLaborPost, v.ID); ok {
			kb.Row(b)
		}
	}
	hire := ""
	if v.CanEmploy && jobID != "" {
		hire = c.T("village.labor.site.hire", map[string]any{"available": v.NPCAvailable, "wage": FormatMoney(c, v.NPCWage)})
	}
	text := paragraphs(head, just, progress, job, crew, hire, laborMarketText(c, v.Market))
	kb.Row(villageButtons(c, "village.labor.button.board", AddrLaborBoard, "village.labor.button.mine", AddrLaborMine)...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLaborBoard, RefreshData: AddrLaborSite + ":" + v.ID}))
	return c.respond(text, kb.Build())
}

func renderLaborMine(c Context, v LaborMineView) *presenter.Response {
	kb := keyboards.New()
	var working string
	if v.Working != nil {
		working = c.T("village.labor.board.working", map[string]any{
			"building": c.SettlementBuildingName(v.Working.Building), "time": FormatClock(c, v.Working.FinishAt),
			"duration": FormatDuration(c, v.Working.Left), "wage": FormatMoney(c, v.Working.Wage),
		})
	} else {
		working = c.T("village.labor.mine.idle", nil)
	}
	next := ""
	if v.NextLevel != "" {
		next = c.T("village.labor.mine.next", map[string]any{"level": c.laborLevel(v.NextLevel), "shifts": v.NextShifts})
	}
	text := paragraphs(
		c.T("village.labor.mine.title", map[string]any{"village": v.Village}),
		working,
		c.T("village.labor.mine.stats", map[string]any{
			"level": c.laborLevel(v.Level), "percent": v.ProductivityBPS / 100, "shifts": v.Shifts, "earned": FormatMoney(c, v.Earned),
		}),
		next, laborMarketText(c, v.Market),
	)
	kb.Row(villageButtons(c, "village.labor.button.board", AddrLaborBoard, "village.button.work", AddrWork)...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLaborBoard, RefreshData: AddrLaborMine}))
	return c.respond(text, kb.Build())
}

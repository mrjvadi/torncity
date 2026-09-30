package screens

import (
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The labour market's screens (docs/adr/0035-labor-market.md): the hiring
// board, a construction site, and a worker's own status. Group screens like
// the rest of the village; the numbers are computed by the use case.

// Addresses.
const (
	AddrLaborBoard = "settlement:labor.board"
	AddrLaborSite  = "settlement:labor.site"
	AddrLaborTake  = "settlement:labor.take"
	AddrLaborHire  = "settlement:labor.hire"
	AddrLaborWage  = "settlement:labor.wage"
	AddrLaborClose = "settlement:labor.close"
	AddrLaborPost  = "settlement:labor.post"
	AddrLaborMine  = "settlement:labor.mine"
)

// Structured screens (clients).
const (
	ScreenLaborBoard = "labor_board"
	ScreenLaborSite  = "labor_site"
	ScreenLaborMine  = "labor_mine"
)

// Labour refusal kinds (village.refusal.<kind>).
const (
	LaborNoJob         = "labor_no_job"
	LaborNotHere       = "labor_not_here"
	LaborFullyStaffed  = "labor_fully_staffed"
	LaborBudgetSpent   = "labor_budget_spent"
	LaborNotEmployer   = "labor_not_employer"
	LaborNoNPC         = "labor_no_npc"
	LaborWageTooLow    = "labor_wage_too_low"
	LaborEmployerBroke = "labor_employer_broke"
	LaborNoSite        = "labor_no_site"
)

// Market levels.
const (
	MarketSlack    = "slack"
	MarketBalanced = "balanced"
	MarketTight    = "tight"
	MarketShort    = "short"
)

// LaborMarketLine is the village's labour market at a glance.
type LaborMarketLine struct {
	// Housing is the homes' capacity (base plus buildings); Pool the NPC
	// labourers who live here, Available those not on a shift now.
	Housing, Pool, Available int64
	// Working is every shift in progress; Vacancies the shifts open jobs still
	// pay for.
	Working, Vacancies int64
	// TightnessBPS is demand over the labour force; Level names the band.
	TightnessBPS int64
	Level        string
	// NPCWage is what an NPC labourer asks for a shift now; MinWage the
	// statutory floor of the village's tier.
	NPCWage, MinWage int64
}

// LaborJobLine is one job on the board.
type LaborJobLine struct {
	ID         string
	BuildingID string
	Building   Named
	Kind       string
	// EmployerKind is "settlement" or "player"; Employer the player's name
	// (empty for the village).
	EmployerKind string
	Employer     string
	Wage         int64
	Left         int
	Total        int
	// ProgressBPS and LeftMinutes are the site's progress and the work left,
	// worker-minutes; zero for a production job.
	ProgressBPS int64
	LeftMinutes int64
	Workers     int
	NPCCrew     int
	// CanTake reports that the viewer may take this job now; Mine that the
	// viewer is the employer.
	CanTake bool
	Mine    bool
	// Points is the work one shift of the viewer adds.
	Points int64
}

// LaborBoardView is the hiring board.
type LaborBoardView struct {
	Village string
	Jobs    []LaborJobLine
	Market  LaborMarketLine
	// Working is the viewer's shift in progress, or nil.
	Working *LaborShiftLine
	// Resident reports that the viewer lives here.
	Resident bool
	// Sites are the buildings under construction with no open job, which the
	// employer can post one for.
	Sites []LaborSiteRef
}

// LaborSiteRef is a building under construction.
type LaborSiteRef struct {
	ID          string
	Building    Named
	ProgressBPS int64
}

// LaborShiftLine is a shift in progress.
type LaborShiftLine struct {
	ID        string
	Building  Named
	Kind      string
	Worker    string
	WorkerNPC bool
	Level     string
	FinishAt  time.Time
	Left      time.Duration
	Wage      int64
	Points    int64
}

// LaborPreset is a wage the employer may set: a share of the market wage.
type LaborPreset struct {
	Percent int
	Wage    int64
}

// LaborSiteView is the panel of one construction site.
type LaborSiteView struct {
	Village  string
	Building Named
	ID       string
	// Status is "building" or "complete".
	Status      string
	ProgressBPS int64
	// RequiredMinutes, DoneMinutes and LeftMinutes are worker-minutes.
	RequiredMinutes, DoneMinutes, LeftMinutes int64
	// Job is the open job of the site, nil when it has none.
	Job     *LaborJobLine
	Workers []LaborShiftLine
	Market  LaborMarketLine
	// CanWork: the viewer may work a shift here now (the wage they get and the
	// work it adds are WorkWage and WorkPoints); Working the viewer's shift.
	CanWork    bool
	WorkWage   int64
	WorkPoints int64
	Working    *LaborShiftLine
	// CanEmploy: the viewer is the employer. HirePresets are the crew sizes
	// offered, WagePresets the wages; NPCAvailable how many labourers are free.
	CanEmploy    bool
	HirePresets  []int
	WagePresets  []LaborPreset
	NPCAvailable int64
	NPCWage      int64
	// Just says what the last press did: "worked", "hired", "wage", "posted".
	Just string
}

// LaborMineView is the viewer's own labour status.
type LaborMineView struct {
	Village string
	Shifts  int64
	Earned  int64
	// Level names the skill level, ProductivityBPS its productivity; NextLevel
	// and NextShifts the next rung and the shifts still needed for it (empty and
	// zero at the top).
	Level           string
	ProductivityBPS int64
	NextLevel       string
	NextShifts      int64
	Working         *LaborShiftLine
	Market          LaborMarketLine
}

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

// workLeft is a worker-minutes figure as "worker-hours" text.
func workLeft(c Context, minutes int64) string {
	return c.T("village.labor.work_left", map[string]any{"hours": strconv.FormatFloat(float64(minutes)/60, 'f', 1, 64)})
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
			names = append(names, c.SettlementBuildingName(s.Building)+" ("+strconv.FormatInt(s.ProgressBPS/100, 10)+"٪)")
			if b, ok := keyboards.Button(c.T("village.labor.button.post", map[string]any{"building": c.SettlementBuildingName(s.Building)}),
				AddrLaborPost, s.ID); ok {
				kb.Row(b)
			}
		}
		sites = c.T("village.labor.board.sites", map[string]any{"names": strings.Join(names, "، ")})
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
			"percent": v.ProgressBPS / 100, "left": workLeft(c, v.LeftMinutes), "done": workLeft(c, v.DoneMinutes), "total": workLeft(c, v.RequiredMinutes),
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

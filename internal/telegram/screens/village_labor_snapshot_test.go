package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The labour market (village_labor.go): the hiring board, a construction site
// and a worker's own status. Its own snapshot area, testdata/snapshots/
// <language>/village_labor.txt.
func init() { snapshotAreas["village_labor"] = villageLaborSnapshots }

func villageLaborSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)
	camp := sampleNamed(c.Lang, "woodcutter_camp", "کارگاه هیزم‌شکنی", "Woodcutter's camp")
	cottage := sampleNamed(c.Lang, "cottage", "خانهٔ روستایی", "Cottage")
	joinery := sampleNamed(c.Lang, "carpentry_workshop", "کارگاه نجاری", "Carpentry workshop")

	balanced := LaborMarketLine{Housing: 12, Pool: 12, Available: 10, Working: 2, Vacancies: 3, TightnessBPS: 4200, Level: MarketBalanced, NPCWage: 30, MinWage: 10}
	short := LaborMarketLine{Housing: 0, Pool: 5, Available: 0, Working: 5, Vacancies: 6, TightnessBPS: 21000, Level: MarketShort, NPCWage: 75, MinWage: 10}
	slack := LaborMarketLine{Housing: 20, Pool: 17, Available: 17, Working: 0, Vacancies: 1, TightnessBPS: 500, Level: MarketSlack, NPCWage: 22, MinWage: 10}
	tight := LaborMarketLine{Housing: 4, Pool: 7, Available: 2, Working: 5, Vacancies: 3, TightnessBPS: 11000, Level: MarketTight, NPCWage: 46, MinWage: 10}
	job := LaborJobLine{ID: "j1", BuildingID: "b1", Building: camp, Kind: "construction", EmployerKind: "settlement", Wage: 30, Left: 3, Total: 3,
		ProgressBPS: 3500, LeftMinutes: 78, Workers: 1, NPCCrew: 2, CanTake: true}
	citizenJob := LaborJobLine{ID: "j2", BuildingID: "b2", Building: cottage, Kind: "construction", EmployerKind: "player", Employer: sampleWho(c.Lang, "سارا", "Sara"), Wage: 45,
		Left: 5, Total: 6, ProgressBPS: 0, LeftMinutes: 240, CanTake: true}
	prodJob := LaborJobLine{ID: "j3", BuildingID: "b3", Building: joinery, Kind: "production", EmployerKind: "settlement", Wage: 30, Left: 18, Total: 20,
		ProgressBPS: 10000, CanTake: true}

	add("Hiring board · three jobs, my shift running", LaborBoard(g, LaborBoardView{
		Village: villageNameFor(c), Resident: true, Market: balanced, Jobs: []LaborJobLine{job, citizenJob, prodJob},
		Working: &LaborShiftLine{ID: "s1", Building: camp, Kind: "construction", FinishAt: snapshotNow.Add(35 * time.Minute), Left: 35 * time.Minute, Wage: 30},
	}))
	add("Hiring board · empty, a site without a job for the head", LaborBoard(g, LaborBoardView{
		Village: villageNameFor(c), Resident: true, Market: slack,
		Sites: []LaborSiteRef{{ID: "b1", Building: camp, ProgressBPS: 1200}},
	}))
	add("Hiring board · a labour shortage, not a resident", LaborBoard(g, LaborBoardView{
		Village: villageNameFor(c), Market: short, Jobs: []LaborJobLine{job},
	}))

	site := LaborSiteView{
		Village: villageNameFor(c), Building: camp, ID: "b1", Status: "building", ProgressBPS: 3500, RequiredMinutes: 120, DoneMinutes: 42, LeftMinutes: 78,
		Job: &job, Market: tight, CanWork: true, WorkWage: 30, WorkPoints: 42,
		Workers: []LaborShiftLine{
			{ID: "s1", Building: camp, Worker: sampleWho(c.Lang, "سارا", "Sara"), Level: "journeyman", FinishAt: snapshotNow.Add(20 * time.Minute), Left: 20 * time.Minute, Wage: 30, Points: 60},
			{ID: "s2", Building: camp, WorkerNPC: true, FinishAt: snapshotNow.Add(45 * time.Minute), Left: 45 * time.Minute, Wage: 46, Points: 51},
		},
	}
	add("Site · a player may work", LaborSite(g, site))
	head := site
	head.CanWork, head.CanEmploy = false, true
	head.HirePresets = []int{1, 2, 4}
	head.WagePresets = []LaborPreset{{Percent: 100, Wage: 46}, {Percent: 125, Wage: 57}, {Percent: 150, Wage: 69}, {Percent: 200, Wage: 92}}
	head.NPCAvailable, head.NPCWage = 2, 46
	add("Site · the head, hiring labourers", LaborSite(g, head))
	hired := head
	hired.Just = "hired"
	add("Site · just hired", LaborSite(g, hired))
	worked := site
	worked.Just, worked.CanWork = "worked", false
	worked.Working = &LaborShiftLine{ID: "s9", Building: camp, Kind: "construction", FinishAt: snapshotNow.Add(time.Hour), Left: time.Hour, Wage: 30}
	add("Site · just took the job", LaborSite(g, worked))
	add("Site · no workers, no job (a stalled build)", LaborSite(g, LaborSiteView{
		Village: villageNameFor(c), Building: cottage, ID: "b2", Status: "building", ProgressBPS: 1000, RequiredMinutes: 240, DoneMinutes: 24, LeftMinutes: 216,
		Market: short, CanPost: true,
	}))
	add("Site · finished", LaborSite(g, LaborSiteView{
		Village: villageNameFor(c), Building: camp, ID: "b1", Status: "complete", ProgressBPS: 10000, RequiredMinutes: 120, DoneMinutes: 120, Market: balanced,
	}))

	add("My work · a journeyman, no shift", LaborMine(g, LaborMineView{
		Village: villageNameFor(c), Shifts: 9, Earned: 310, Level: "journeyman", ProductivityBPS: 10000, NextLevel: "master", NextShifts: 21, Market: balanced,
	}))
	add("My work · a new apprentice, on a shift", LaborMine(g, LaborMineView{
		Village: villageNameFor(c), Shifts: 0, Level: "apprentice", ProductivityBPS: 7000, NextLevel: "journeyman", NextShifts: 6, Market: slack,
		Working: &LaborShiftLine{ID: "s1", Building: camp, Kind: "construction", FinishAt: snapshotNow.Add(50 * time.Minute), Left: 50 * time.Minute, Wage: 27},
	}))

	for _, kind := range []string{LaborNoJob, LaborNotHere, LaborFullyStaffed, LaborBudgetSpent, LaborNotEmployer, LaborNoNPC, LaborWageTooLow, LaborEmployerBroke, LaborNoSite} {
		add("Refusal · "+kind, VillageRefusal(g, VillageRefusalView{Kind: kind, Back: presentation.RefOfAddress(AddrLaborBoard)}))
	}
	add("Construction progress · by work", ConstructionProgress(g, ConstructionProgressView{Name: villageNameFor(c), Lines: []ConstructionLine{
		{Building: camp, State: ConstructionBuilding, ID: "b1", ByWork: true, ProgressBPS: 3500, LeftMinutes: 78},
	}}))
	_ = prodJob
}

func sampleWho(lang, fa, en string) string {
	if lang == "fa" {
		return fa
	}
	return en
}

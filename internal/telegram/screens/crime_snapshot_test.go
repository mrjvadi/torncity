package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The crime screens join the snapshot harness as their own area,
// testdata/snapshots/<language>/crime.txt.
func init() { snapshotAreas["crime"] = crimeSnapshots }

// group is c as rendered in a group chat.
func group(c Context) Context {
	c.Shared = true
	return c
}

func crimeSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	pick := Named{Code: "pickpocketing", Name: "Pickpocketing"}
	burgle := Named{Code: "home_burglary", Name: "Home burglary"}
	scam := Named{Code: "street_scam", Name: "Street scam"}
	shop := Named{Code: "shoplifting", Name: "Shoplifting"}
	station := Named{Code: "train_station", Name: "Train station"}
	centre := Named{Code: "city_centre", Name: "City centre"}
	nerve := NerveView{Nerve: 14, Max: 20, FullIn: 30 * time.Minute}
	heat := HeatView{Heat: 23, Max: 100, Wanted: 2, Stars: 5}
	tier := TierView{Tier: Named{Code: "novice", Name: "Novice"}, XP: 96, Next: Named{Code: "hustler", Name: "Hustler"}, NextXP: 150}
	categories := []Named{
		{Code: "petty_theft", Name: "Petty theft"}, {Code: "street_crime", Name: "Street crime"},
		{Code: "burglary", Name: "Burglary"}, {Code: "fraud", Name: "Fraud"}, {Code: "smuggling", Name: "Smuggling"},
	}

	add("Hub · at the train station", CrimeHub(sent(c), CrimeHubView{
		CityCode: "ostmarch", City: "Ostmarch", Venue: station, Nerve: nerve, Heat: heat, Tier: tier, Categories: categories,
	}))
	add("Hub · clean, full of nerve", CrimeHub(c, CrimeHubView{
		CityCode: "ostmarch", City: "Ostmarch", Venue: centre, Nerve: NerveView{Nerve: 20, Max: 20},
		Heat: HeatView{Max: 100, Stars: 5}, Tier: TierView{Tier: Named{Code: "professional", Name: "Professional"}, XP: 1200},
		Categories: categories,
	}))
	add("Hub · in jail", CrimeHub(c, CrimeHubView{
		CityCode: "ostmarch", City: "Ostmarch", Venue: centre, Nerve: nerve, Heat: heat, Tier: tier, Categories: categories,
		Jail: &CrimeProgress{Remaining: 95 * time.Minute, EndsAt: snapshotNow.Add(95 * time.Minute)},
	}))
	add("Hub · a burglary under way", CrimeHub(c, CrimeHubView{
		CityCode: "ostmarch", City: "Ostmarch", Venue: centre, Nerve: nerve, Heat: heat, Tier: tier, Categories: categories,
		Busy: &CrimeProgress{Crime: burgle, Remaining: 90 * time.Second, EndsAt: snapshotNow.Add(90 * time.Second)},
	}))
	add("Hub · on the road", CrimeHub(c, CrimeHubView{Nerve: nerve, Heat: heat, Tier: tier, Travelling: true}))

	add("List · petty theft", CrimeList(c, CrimeListView{
		Category: categories[0], Page: 1, Pages: 1,
		Crimes: []CrimeLine{{Crime: pick, Nerve: 2, Eligible: true}, {Crime: shop, Nerve: 2}},
	}))
	add("List · burglary, a timed crime", CrimeList(c, CrimeListView{
		Category: categories[2], Page: 1, Pages: 1,
		Crimes: []CrimeLine{{Crime: burgle, Nerve: 6, Duration: 2 * time.Minute}},
	}))

	add("Crime · pickpocketing, ready", CrimeDetail(c, CrimeDetailView{
		Crime: pick, Category: categories[0], Nerve: 2, ChanceBPS: 5160, HitsPlayers: true, HitsNPCs: true,
		MinTake: 15, MaxTake: 90, JailMin: 2 * time.Minute, JailMax: 6 * time.Minute, FineMin: 100, FineMax: 400,
		Requirements: []CrimeRequirement{{
			Requirement: Requirement{Kind: ReqVenue, Met: true},
			Venues:      []Named{centre, {Code: "bus_terminal", Name: "Bus terminal"}, station, {Code: "airport", Name: "Airport"}},
			Here:        station,
		}},
		CanCommit: true, Nonce: "a1b2c3d4e5f6",
	}))
	add("Crime · home burglary, out of reach", CrimeDetail(c, CrimeDetailView{
		Crime: burgle, Category: categories[2], Nerve: 6, Duration: 2 * time.Minute, ChanceBPS: 3200, HitsNPCs: true,
		MinTake: 500, MaxTake: 2500, JailMin: 8 * time.Minute, JailMax: 24 * time.Minute, FineMin: 500, FineMax: 2000,
		Requirements: []CrimeRequirement{
			{Requirement: Requirement{Kind: ReqLevel, Need: 5, Have: 3}},
			{Requirement: Requirement{Kind: ReqCrimeTier}, Tier: Named{Code: "hustler", Name: "Hustler"}, HaveTier: Named{Code: "novice", Name: "Novice"}},
			{Requirement: Requirement{Kind: ReqSkill, Skill: "lockpicking", Need: 3, Have: 1}},
			{Requirement: Requirement{Kind: ReqSkill, Met: true, Skill: "stealth", Need: 2, Have: 4}},
			{Requirement: Requirement{Kind: ReqVenue}, Venues: []Named{centre}, Here: station},
		},
	}))
	add("Crime · smuggling needs a rail station", CrimeDetail(c, CrimeDetailView{
		Crime: Named{Code: "goods_smuggling", Name: "Smuggling small goods"}, Category: categories[4], Nerve: 8,
		Duration: 4 * time.Minute, ChanceBPS: 4100, HitsNPCs: true, MinTake: 1500, MaxTake: 5000,
		JailMin: 12 * time.Minute, JailMax: 36 * time.Minute, FineMin: 1000, FineMax: 4000,
		Requirements: []CrimeRequirement{{Requirement: Requirement{Kind: ReqFacility}, Facility: "rail_station"}},
	}))
	add("Crime · not enough nerve", CrimeDetail(c, CrimeDetailView{
		Crime: scam, Category: categories[3], Nerve: 3, Duration: 20 * time.Second, ChanceBPS: 5500, HitsNPCs: true,
		MinTake: 80, MaxTake: 400, JailMin: 2 * time.Minute, JailMax: 6 * time.Minute, FineMin: 150, FineMax: 600,
		Blocked: CrimeBlockedNerve, Need: 3, Have: 1, Wait: 10 * time.Minute,
	}))
	add("Crime · from a cell", CrimeDetail(c, CrimeDetailView{
		Crime: shop, Category: categories[0], Nerve: 2, ChanceBPS: 6000, HitsNPCs: true, MinTake: 20, MaxTake: 120,
		JailMin: time.Minute, JailMax: 3 * time.Minute, FineMin: 50, FineMax: 200, Blocked: CrimeBlockedJail,
	}))

	success := CrimeResultView{
		Player: who.me, Crime: pick, Venue: station, CityCode: "ostmarch", City: "Ostmarch", Result: CrimeOutcomeSucceeded,
		VictimPlayer: true, Take: 1500, XP: 5, CriminalXP: 8,
		Skills: []SkillGain{{Skill: "stealth", XP: 12, Level: 2}}, Heat: heat, Nerve: nerve,
	}
	add("Result · a pocket picked, as the thief reads it", CrimeResult(c, success))
	add("Result · a pocket picked, as the group reads it", CrimeResult(group(c), success))
	empty := success
	empty.Take = 0
	add("Result · empty pockets", CrimeResult(c, empty))
	npc := success
	npc.VictimPlayer, npc.Take, npc.Crime, npc.Venue = false, 64, shop, centre
	add("Result · a shop's till", CrimeResult(c, npc))
	dry := npc
	dry.Take, dry.DrySpell = 0, true
	add("Result · the city bled dry for today", CrimeResult(c, dry))
	escaped := CrimeResultView{
		Player: who.me, Crime: pick, Venue: station, CityCode: "ostmarch", City: "Ostmarch", Result: CrimeOutcomeEscaped,
		Skills: []SkillGain{{Skill: "stealth", XP: 6}}, Heat: heat, Nerve: nerve,
	}
	add("Result · spotted, got away (group)", CrimeResult(group(c), escaped))
	caught := CrimeResultView{
		Player: who.me, Crime: pick, Venue: station, CityCode: "ostmarch", City: "Ostmarch", Result: CrimeOutcomeCaught,
		Heat: HeatView{Heat: 31, Max: 100, Wanted: 2, Stars: 5}, Fine: 300, FinePaid: 300,
		Jail: &CrimeProgress{Remaining: 4 * time.Minute, EndsAt: snapshotNow.Add(4 * time.Minute)},
	}
	add("Result · caught in the act (group)", CrimeResult(group(c), caught))
	short := caught
	short.FinePaid = 120
	add("Result · caught, the fine not fully paid", CrimeResult(c, short))
	notice := caught
	notice.Crime, notice.Venue, notice.Notice = burgle, centre, true
	add("Notice · a burglary ended in an arrest", CrimeResult(sent(c), notice).MarkPrivate())
	take := success
	take.Notice = true
	add("Notice · the take, told privately", CrimeResult(sent(c), take).MarkPrivate())

	add("Started · a burglary (group)", CrimeStarted(group(sent(c)), CrimeStartedView{
		Player: who.me, Crime: burgle, Venue: centre, Duration: 2 * time.Minute, EndsAt: snapshotNow.Add(2 * time.Minute), Nerve: nerve,
	}))

	record := CrimeRecordView{
		Nerve: nerve, Heat: heat, Tier: tier, Attempts: 12, Successes: 8, Arrests: 3, Convictions: 1,
		UnpaidRestitution: 400, UnpaidFines: 250,
		Recent: []CrimeRecordLine{
			{Crime: burgle, Result: application.CrimeInProgress}, {Crime: pick, Result: application.CrimeSucceeded},
			{Crime: shop, Result: application.CrimeEscaped}, {Crime: pick, Result: application.CrimeCaught},
		},
	}
	add("Record · as the player reads it", CrimeRecord(c, record))
	add("Record · as the group reads it", CrimeRecord(group(c), record))
	add("Record · clean", CrimeRecord(c, CrimeRecordView{Nerve: NerveView{Nerve: 20, Max: 20}, Heat: HeatView{Max: 100, Stars: 5},
		Tier: TierView{Tier: Named{Code: "novice", Name: "Novice"}, Next: Named{Code: "hustler", Name: "Hustler"}, NextXP: 150}}))

	add("Jail · serving, with bail", Jail(c, JailView{
		InJail: true, CityCode: "ostmarch", City: "Ostmarch", Reason: application.SentenceForConviction,
		Remaining: 95 * time.Minute, EndsAt: snapshotNow.Add(95 * time.Minute), Bail: 3960, Nonce: "f00dfeed0001",
	}))
	add("Jail · free", Jail(c, JailView{}))
	add("Bailed · as the player reads it", Bailed(c, BailedView{Player: who.me, Bail: 3960}))
	add("Bailed · as the group reads it", Bailed(group(c), BailedView{Player: who.me, Bail: 3960}))
	add("Notice · sentence served", ReleasedNotice(sent(c), Named{Code: "ostmarch", Name: "Ostmarch"}))

	victim := VictimNoticeView{
		Crime: pick, Venue: station, CityCode: "ostmarch", City: "Ostmarch", Amount: 1500,
		CrimeID: someID, ReportFee: 200, ReportWithin: 24 * time.Hour,
	}
	add("Notice · your pocket was picked", VictimNotice(sent(c), victim))
	seen := victim
	seen.ThiefName, seen.ThiefCode = who.friend, friendCode
	add("Notice · your pocket was picked, and a witness saw who", VictimNotice(sent(c), seen))
	add("Report · confirm", ReportConfirm(c, ReportConfirmView{
		CrimeID: someID, Crime: pick, CityCode: "ostmarch", City: "Ostmarch", Amount: 1500, Fee: 200,
		Investigation: 6 * time.Minute, ReportWithin: 23*time.Hour + 40*time.Minute,
	}))
	add("Report · filed", CaseFiled(c, 6*time.Minute, snapshotNow.Add(6*time.Minute)))
	add("Cases", Cases(c, CasesView{Cases: []CaseLine{
		{Crime: pick, CityCode: "ostmarch", City: "Ostmarch", Amount: 1500, Status: application.ReportInvestigating, Remaining: 4 * time.Minute},
		{Crime: pick, CityCode: "brennhaven", City: "Brennhaven", Amount: 300, Status: application.ReportInvestigating, Remaining: 10 * time.Second},
		{Crime: pick, CityCode: "ostmarch", City: "Ostmarch", Amount: 800, Status: application.ReportSolved, Thief: who.friend, Restored: 800},
		{Crime: pick, CityCode: "calderis", City: "Calderis", Amount: 120, Status: application.ReportUnsolved},
	}}))
	add("Cases · none", Cases(c, CasesView{}))
	outcome := CaseOutcomeView{
		Crime: pick, CityCode: "ostmarch", City: "Ostmarch", Solved: true, Thief: who.friend, ThiefCode: friendCode,
		Stolen: 1500, Restored: 1100, Shortfall: 400, Fine: 300, FinePaid: 0, Term: 4 * time.Minute,
	}
	add("Notice · case solved, part of it recovered", CaseSolvedNotice(sent(c), outcome))
	closed := outcome
	closed.Solved = false
	add("Notice · case closed", CaseSolvedNotice(sent(c), closed))
	add("Notice · convicted", ConvictedNotice(sent(c), outcome))

	for _, r := range []struct {
		title string
		view  CrimeRefusalView
	}{
		{"requirements", CrimeRefusalView{Kind: CrimeRefusedRequirements, Crime: burgle, Missing: []CrimeRequirement{
			{Requirement: Requirement{Kind: ReqLevel, Need: 5, Have: 3}},
			{Requirement: Requirement{Kind: ReqCrimeTier}, Tier: Named{Code: "hustler", Name: "Hustler"}, HaveTier: Named{Code: "novice", Name: "Novice"}},
		}}},
		{"not enough nerve", CrimeRefusalView{Kind: CrimeRefusedNerve, Crime: burgle, Need: 6, Have: 2}},
		{"in jail", CrimeRefusalView{Kind: CrimeRefusedJail, Remaining: 42 * time.Minute}},
		{"busy", CrimeRefusalView{Kind: CrimeRefusedBusy}},
		{"at work", CrimeRefusalView{Kind: CrimeRefusedWork}},
		{"travelling", CrimeRefusalView{Kind: CrimeRefusedTravelling}},
		{"nowhere", CrimeRefusalView{Kind: CrimeRefusedNowhere}},
		{"nobody around", CrimeRefusalView{Kind: CrimeRefusedNoVictim}},
		{"no such crime", CrimeRefusalView{Kind: CrimeRefusedNotFound}},
		{"not your theft", CrimeRefusalView{Kind: CrimeRefusedNotYours}},
		{"report window passed", CrimeRefusalView{Kind: CrimeRefusedExpired}},
		{"nothing stolen", CrimeRefusalView{Kind: CrimeRefusedNothingStolen}},
		{"cannot afford", CrimeRefusalView{Kind: CrimeRefusedCannotAfford, Amount: 3960, Cash: 1200}},
		{"not in jail", CrimeRefusalView{Kind: CrimeRefusedNotJailed}},
	} {
		add("Refused · "+r.title, CrimeRefusal(c, r.view))
	}
	add("Error · a journey from jail", Error(c, application.ErrInJail.WithDetail("remaining_seconds", int64(42*60))))
	add("Error · a journey from jail, time unknown", Error(c, application.ErrInJail))
	add("Error · a shift during a crime", Error(c, application.ErrCrimeInProgress))
}

package render

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/presentation/military"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// militaryFixtures are the military, war and defence screens with realistic
// views, for the actions tests: every action the core lists is a command the
// game serves, and every button Telegram draws is one of them.
func militaryFixtures() []fixture {
	c := presentation.Ctx{Lang: "fa"}
	at := time.Date(2026, 3, 4, 10, 20, 30, 0, time.UTC)
	home := presentation.GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	other := presentation.GovPlace{Kind: "country", Code: "vantor_federation", Name: "Vantor"}
	city := presentation.GovPlace{Kind: "city", Code: "kessmoor", Name: "Kessmoor"}
	stealth := presentation.Good{Item: named("stealth_fighter"), DesignNo: 3, Design: "Wraith"}
	jet := presentation.Good{Item: named("fighter_jet")}
	company := presentation.CompanyRef{Code: "acme", Name: "Acme", Type: named("aerospace")}
	attrs := []military.AttributeLine{{Name: "speed", Value: 2100}, {Name: "rcs", Value: 5}}
	forces := []military.BranchForces{{Branch: named("air"), Classes: []military.ForceClassLine{
		{Class: named("fighter"), Band: "squadron", Count: 12}}}, {Branch: named("ground")}}
	gone := &economy.Unavailable{Service: military.ServiceWar, Stage: "country", Here: "village",
		Nearest: &presentation.Named{Code: "support", Name: "Central"}}

	ministry := military.MinistryView{Country: home, Offices: []military.GovOffice{{Code: "defence_minister", Seats: 1}},
		Treasury: 9000, Fund: 4000, RevenueShareBPS: 1000, DefenceBudgetBPS: 2000, ArmsExports: 1,
		Last: &military.PeriodLine{Levy: 100, Appropriation: 200, UpkeepDue: 50, UpkeepPaid: 40}, NextIn: time.Hour, NextAt: at,
		Forces: forces, Cleared: true, Readiness: 9500, Upkeep: 70, CanProcure: true, PendingLicences: 2,
		Notice: &military.Notice{Code: military.NoticeAnswerAccept}}
	forcesV := military.ForcesView{Country: home, Branches: forces, Cleared: true, Readiness: 9500, Upkeep: 70, Moving: 2}
	group := military.AssetGroup{Good: stealth, Class: named("stealth_fighter"), Count: 5, Quality: 80, Attributes: attrs, SeenAt: 40,
		Garrisons: []military.GarrisonLine{{CityCode: "kessmoor", City: "Kessmoor", Count: 3}}, Depot: 1, Moving: 1}
	branch := military.BranchView{Country: home, Branch: named("air"), Groups: []military.AssetGroup{group}, CanStation: true,
		Moves:            []military.MoveLine{{Good: stealth, Qty: 1, CityCode: "kessmoor", City: "Kessmoor", Left: time.Hour, At: at}},
		ReferenceRadarKM: 300, Notice: &military.Notice{Code: military.NoticeStationStarted, Count: 2, Good: stealth,
			CityCode: "kessmoor", City: "Kessmoor", Time: time.Hour}}
	stationCity := military.StationView{Country: home, Branch: named("air"), Good: stealth, Available: 4, Cities: []military.GovPlace{city}}
	stationQty := stationCity
	stationQty.CityCode, stationQty.City, stationQty.Time = "kessmoor", "Kessmoor", time.Hour
	stationOK := stationQty
	stationOK.Qty, stationOK.Confirm = 3, true

	offer := military.ProcureOffer{No: 7, Good: jet, Company: company, CityCode: "kessmoor", City: "Kessmoor", Country: home, Left: 6, Price: 100}
	blocked := offer
	blocked.No, blocked.Blocked = 8, military.ProcureBlockedExport
	procure := military.ProcureView{Country: home, Fund: 450, Offers: []military.ProcureOffer{offer, blocked},
		Notice: &military.Notice{Code: military.NoticeBuyDone, Count: 2, Good: jet, Total: 200}}
	buy := military.ArmsBuyView{Country: home, Offer: offer, Attributes: attrs, Fund: 450}
	buyOK := buy
	buyOK.Qty, buyOK.Confirm, buyOK.Total = 3, true, 300
	mRefusal := military.MilitaryRefusalView{Kind: military.MilitaryRefusedFunds, Country: home, Need: 500, Have: 450,
		Back: presentation.RefOfAddress(military.AddrProcure + ":" + home.Code)}
	mRefusalHolder := military.MilitaryRefusalView{Kind: military.MilitaryRefusedNotHolder, Country: home, Office: "defence_minister"}

	proposal := military.ProposalLine{No: 5, Kind: "ceasefire", Other: other, Incoming: true, ExpiresIn: time.Hour}
	warLine := military.WarLine{No: 3, Attacker: home, Defender: other, Ground: "aggression", Status: "active", Since: time.Hour,
		Proposals: []military.ProposalLine{proposal}, CanPropose: true, CanResume: true, AttackerAllies: []military.GovPlace{other}}
	op := military.OperationLine{No: 12, Kind: "air", Objective: "city", Country: home, CityCode: "kessmoor", City: "Kessmoor", Target: other,
		DamageBand: "light", LostBand: "unit", EnemyLostBand: "squadron", Ago: time.Hour}
	board := military.WarBoardView{Country: home, Wars: []military.WarLine{warLine},
		Joinable:   []military.JoinLine{{WarNo: 4, Ally: other, Enemy: home}},
		Occupied:   []military.OccupationLine{{CityCode: "calderis", City: "Calderis", Controller: home, DeJure: other, Since: time.Hour}},
		Damaged:    []military.DamageLine{{CityCode: "calderis", City: "Calderis", Band: "heavy", ClosedIn: time.Minute}},
		Operations: []military.OperationLine{op}, CanDeclare: true, CanCommand: true,
		Notice: &military.Notice{Code: military.NoticeDeclareDone, Target: other, Time: 24 * time.Hour}}
	target := other
	declareT := military.DeclareView{Country: home, Targets: []military.GovPlace{other}}
	declareG := military.DeclareView{Country: home, Target: &target, Grounds: []string{"aggression", "self_defence"}}
	declareC := military.DeclareView{Country: home, Target: &target, Ground: "aggression", Notice: 24 * time.Hour,
		Breaks: []military.Named{named("non_aggression")}, Allies: []military.GovPlace{other}}
	decision := func(kind string) military.WarDecisionView {
		return military.WarDecisionView{Kind: kind, Country: home, WarNo: 3, Other: other, Ally: other, Notice: time.Hour, TTL: time.Hour}
	}
	roomTarget := military.RoomTarget{CityCode: "calderis", City: "Calderis", Country: other, WarNo: 3, DistanceKM: 500, DamageBand: "light"}
	room := military.WarRoomView{Country: home, Targets: []military.RoomTarget{roomTarget}, Running: []military.OperationLine{op}, Readiness: 9000,
		Notice: &military.Notice{Code: military.NoticeLaunchDone, Kind: "air", CityCode: "calderis", City: "Calderis", Time: time.Minute}}
	options := []military.ForceOption{
		{Kind: "air", Class: named("stealth_fighter"), Ready: 3, FromCode: "kessmoor", From: "Kessmoor", DistanceKM: 520, Munitions: 8,
			CanLaunch: true, Office: "air_force_commander"},
		{Kind: "ground", Class: named(military.WarAllUnits), Ready: 14, FromCode: "kessmoor", From: "Kessmoor", DistanceKM: 520,
			Office: "ground_forces_commander", CanLaunch: true},
		{Kind: "missile", Class: named("ballistic"), Ready: 12, FromCode: "kessmoor", From: "Kessmoor", DistanceKM: 520,
			Office: "ground_forces_commander"},
	}
	warTarget := military.WarTargetView{Country: home, Target: roomTarget, Options: options,
		Occupied: &military.OccupationLine{DeJure: other}}
	launch := military.LaunchView{Country: home, Target: roomTarget, Option: options[0], Prepare: time.Minute,
		Objectives: []string{"city", "defences"}}
	launchQty := launch
	launchQty.Objective, launchQty.Quantities = "defences", []int64{1, 2, 3}
	launchOK := launchQty
	launchOK.Qty, launchOK.Confirm, launchOK.Munitions = 2, true, 4
	launchOK.Estimate = &military.Estimate{Chance: "likely", DamageBand: "heavy"}
	assault := military.LaunchView{Country: home, Target: roomTarget, Option: options[1], Prepare: time.Minute, Objective: "take",
		Qty: 14, Confirm: true, Estimate: &military.Estimate{Chance: "even", LossBand: "unit"}}
	report := military.StrikeReportView{No: 12, Kind: "air", Objective: "defences", Country: home, Target: other, CityCode: "calderis",
		City: "Calderis", Class: named("stealth_fighter"), Ours: true, Committed: 2, EnemyLost: 2, SeenAtKM: 38, Fired: 6, Munitions: 4, Hits: 3}
	theirs := report
	theirs.Ours = false
	wRefusal := military.WarRefusalView{Kind: military.WarRefusedNotYet, Country: home, In: time.Hour,
		Back: presentation.RefOfAddress(military.AddrWarRoom)}
	wRefusalHolder := military.WarRefusalView{Kind: military.WarRefusedNotHolder, Country: home, Office: "president"}
	blockedBorder := military.WarBlockedView{Border: true, From: home, To: other, Back: presentation.RefOfAddress(military.AddrHome)}
	blockedCity := military.WarBlockedView{CityCode: "kessmoor", City: "Kessmoor", In: time.Hour}
	notice := func(kind string) military.WarNoticeView {
		return military.WarNoticeView{Kind: kind, Country: home, Other: other, Ally: other, CityCode: "kessmoor", City: "Kessmoor",
			WarNo: 3, ProposalNo: 5, ProposalKind: "peace", Band: "heavy", In: time.Hour,
			Injury: &military.InjuryLine{Damage: 10, Health: 40, Max: 100, Hospital: true, EndsAt: at}}
	}

	entry := military.LicenceEntry{No: 4, Company: company, Kind: "contractor", Basis: "minister", Status: "pending"}
	active := entry
	active.No, active.Status = 5, "active"
	ended := entry
	ended.No, ended.Status = 6, "revoked"
	licences := military.LicencesView{Country: home, Pending: []military.LicenceEntry{entry}, InForce: []military.LicenceEntry{active},
		Ended: []military.LicenceEntry{ended}, CanDecide: true, Notice: "approve", NoticeCompany: "Acme"}
	licenceConfirm := military.LicencesView{Country: home, Confirm: &active, RevokeNotice: 24 * time.Hour}
	defence := military.CompanyDefenceView{Ref: company, Licence: &entry, Owned: 1, Tier: 1, MinTechs: 2, MinTier: 2, CanApply: true,
		Applied: true, NoMinister: true}
	defenceLab := military.CompanyDefenceView{Ref: company, Owned: 1, Tier: 1, MinTechs: 2, MinTier: 2}
	defenceMaker := military.CompanyDefenceView{Ref: company, Manufacturer: true, Licence: &active}
	arrived := military.MilitaryNoticeView{Country: home, Good: stealth, Qty: 2, CityCode: "kessmoor", City: "Kessmoor", Branch: named("air")}
	licenceNotice := func(kind string) military.LicenceNoticeView {
		return military.LicenceNoticeView{Kind: kind, Company: company, Country: home, EffectiveAt: at}
	}
	kit := military.KitPurchaseView{Bought: true, Seller: "Acme", Country: home.Code}
	retrofit := military.StateRetrofitView{Country: home.Code, KitSerial: "k1", TargetSerial: "u1", Good: stealth, FromVer: 1, ToVer: 2,
		Duration: time.Hour, FinishAt: at}

	gate := func(f func(presentation.Ctx, *economy.Unavailable) *presentation.Response) *presentation.Response {
		return f(c, gone)
	}
	_ = gate
	tg := func(f func(screens.Context) *presenter.Response) func(screens.Context) *presenter.Response { return f }
	return []fixture{
		{"ministry", military.Ministry(c, ministry), tg(func(x screens.Context) *presenter.Response { return screens.Ministry(x, ministry) })},
		{"ministry unavailable", military.Ministry(c, military.MinistryView{Unavailable: gone}),
			tg(func(x screens.Context) *presenter.Response {
				return screens.Ministry(x, military.MinistryView{Unavailable: gone})
			})},
		{"forces", military.Forces(c, forcesV), tg(func(x screens.Context) *presenter.Response { return screens.Forces(x, forcesV) })},
		{"forces unavailable", military.Forces(c, military.ForcesView{Unavailable: gone}),
			tg(func(x screens.Context) *presenter.Response {
				return screens.Forces(x, military.ForcesView{Unavailable: gone})
			})},
		{"branch", military.Branch(c, branch), tg(func(x screens.Context) *presenter.Response { return screens.Branch(x, branch) })},
		{"station city", military.Station(c, stationCity), tg(func(x screens.Context) *presenter.Response { return screens.Station(x, stationCity) })},
		{"station qty", military.Station(c, stationQty), tg(func(x screens.Context) *presenter.Response { return screens.Station(x, stationQty) })},
		{"station confirm", military.Station(c, stationOK), tg(func(x screens.Context) *presenter.Response { return screens.Station(x, stationOK) })},
		{"procure", military.Procure(c, procure), tg(func(x screens.Context) *presenter.Response { return screens.Procure(x, procure) })},
		{"procure unavailable", military.Procure(c, military.ProcureView{Unavailable: gone}),
			tg(func(x screens.Context) *presenter.Response {
				return screens.Procure(x, military.ProcureView{Unavailable: gone})
			})},
		{"arms buy", military.ArmsBuy(c, buy), tg(func(x screens.Context) *presenter.Response { return screens.ArmsBuy(x, buy) })},
		{"arms buy confirm", military.ArmsBuy(c, buyOK), tg(func(x screens.Context) *presenter.Response { return screens.ArmsBuy(x, buyOK) })},
		{"military refusal", military.MilitaryRefusal(c, mRefusal), tg(func(x screens.Context) *presenter.Response { return screens.MilitaryRefusal(x, mRefusal) })},
		{"military refusal holder", military.MilitaryRefusal(c, mRefusalHolder),
			tg(func(x screens.Context) *presenter.Response { return screens.MilitaryRefusal(x, mRefusalHolder) })},
		{"kit purchase", military.KitPurchase(c, kit), tg(func(x screens.Context) *presenter.Response { return screens.KitPurchase(x, kit) })},
		{"state retrofit", military.StateRetrofit(c, retrofit), tg(func(x screens.Context) *presenter.Response { return screens.StateRetrofit(x, retrofit) })},
		{"war board", military.WarBoard(c, board), tg(func(x screens.Context) *presenter.Response { return screens.WarBoard(x, board) })},
		{"war board unavailable", military.WarBoard(c, military.WarBoardView{Unavailable: gone}),
			tg(func(x screens.Context) *presenter.Response {
				return screens.WarBoard(x, military.WarBoardView{Unavailable: gone})
			})},
		{"declare target", military.Declare(c, declareT), tg(func(x screens.Context) *presenter.Response { return screens.Declare(x, declareT) })},
		{"declare ground", military.Declare(c, declareG), tg(func(x screens.Context) *presenter.Response { return screens.Declare(x, declareG) })},
		{"declare confirm", military.Declare(c, declareC), tg(func(x screens.Context) *presenter.Response { return screens.Declare(x, declareC) })},
		{"decision join", military.WarDecision(c, decision("join")), tg(func(x screens.Context) *presenter.Response { return screens.WarDecision(x, decision("join")) })},
		{"decision ceasefire", military.WarDecision(c, decision("ceasefire")),
			tg(func(x screens.Context) *presenter.Response { return screens.WarDecision(x, decision("ceasefire")) })},
		{"decision resume", military.WarDecision(c, decision("resume")), tg(func(x screens.Context) *presenter.Response { return screens.WarDecision(x, decision("resume")) })},
		{"war room", military.WarRoom(c, room), tg(func(x screens.Context) *presenter.Response { return screens.WarRoom(x, room) })},
		{"war room unavailable", military.WarRoom(c, military.WarRoomView{Unavailable: gone}),
			tg(func(x screens.Context) *presenter.Response {
				return screens.WarRoom(x, military.WarRoomView{Unavailable: gone})
			})},
		{"war target", military.WarTarget(c, warTarget), tg(func(x screens.Context) *presenter.Response { return screens.WarTarget(x, warTarget) })},
		{"launch objective", military.WarLaunch(c, launch), tg(func(x screens.Context) *presenter.Response { return screens.WarLaunch(x, launch) })},
		{"launch qty", military.WarLaunch(c, launchQty), tg(func(x screens.Context) *presenter.Response { return screens.WarLaunch(x, launchQty) })},
		{"launch confirm", military.WarLaunch(c, launchOK), tg(func(x screens.Context) *presenter.Response { return screens.WarLaunch(x, launchOK) })},
		{"launch assault", military.WarLaunch(c, assault), tg(func(x screens.Context) *presenter.Response { return screens.WarLaunch(x, assault) })},
		{"strike report ours", military.StrikeReport(c, report), tg(func(x screens.Context) *presenter.Response { return screens.StrikeReport(x, report) })},
		{"strike report theirs", military.StrikeReport(c, theirs), tg(func(x screens.Context) *presenter.Response { return screens.StrikeReport(x, theirs) })},
		{"war refusal", military.WarRefusal(c, wRefusal), tg(func(x screens.Context) *presenter.Response { return screens.WarRefusal(x, wRefusal) })},
		{"war refusal holder", military.WarRefusal(c, wRefusalHolder), tg(func(x screens.Context) *presenter.Response { return screens.WarRefusal(x, wRefusalHolder) })},
		{"war blocked border", military.WarBlocked(c, blockedBorder), tg(func(x screens.Context) *presenter.Response { return screens.WarBlocked(x, blockedBorder) })},
		{"war blocked city", military.WarBlocked(c, blockedCity), tg(func(x screens.Context) *presenter.Response { return screens.WarBlocked(x, blockedCity) })},
		{"war notice ally", military.WarNotice(c, notice("ally")), tg(func(x screens.Context) *presenter.Response { return screens.WarNotice(x, notice("ally")) })},
		{"war notice proposal", military.WarNotice(c, notice("proposal")), tg(func(x screens.Context) *presenter.Response { return screens.WarNotice(x, notice("proposal")) })},
		{"war notice struck", military.WarNotice(c, notice("struck")), tg(func(x screens.Context) *presenter.Response { return screens.WarNotice(x, notice("struck")) })},
		{"licences", military.Licences(c, licences), tg(func(x screens.Context) *presenter.Response { return screens.Licences(x, licences) })},
		{"licences revoke", military.Licences(c, licenceConfirm), tg(func(x screens.Context) *presenter.Response { return screens.Licences(x, licenceConfirm) })},
		{"licences unavailable", military.Licences(c, military.LicencesView{Unavailable: gone}),
			tg(func(x screens.Context) *presenter.Response {
				return screens.Licences(x, military.LicencesView{Unavailable: gone})
			})},
		{"company defence", military.CompanyDefence(c, defence), tg(func(x screens.Context) *presenter.Response { return screens.CompanyDefence(x, defence) })},
		{"company defence lab", military.CompanyDefence(c, defenceLab), tg(func(x screens.Context) *presenter.Response { return screens.CompanyDefence(x, defenceLab) })},
		{"company defence maker", military.CompanyDefence(c, defenceMaker),
			tg(func(x screens.Context) *presenter.Response { return screens.CompanyDefence(x, defenceMaker) })},
		{"move arrived notice", military.MoveArrivedNotice(c, arrived), tg(func(x screens.Context) *presenter.Response { return screens.MoveArrivedNotice(x, arrived) })},
		{"licence notice applied", military.LicenceNotice(c, licenceNotice("applied")),
			tg(func(x screens.Context) *presenter.Response { return screens.LicenceNotice(x, licenceNotice("applied")) })},
		{"licence notice approved", military.LicenceNotice(c, licenceNotice("approved")),
			tg(func(x screens.Context) *presenter.Response {
				return screens.LicenceNotice(x, licenceNotice("approved"))
			})},
	}
}

// TestMilitaryFixturesCoverEveryScreen: the area's fixtures draw every one of
// its screens, so an action list added to a constructor is tested.
func TestMilitaryFixturesCoverEveryScreen(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range militaryFixtures() {
		seen[f.neutral.Screen] = true
	}
	for _, s := range presentation.Specs() {
		if s.Area == "military" && !seen[s.Name] {
			t.Errorf("screen %q has no fixture in militaryFixtures", s.Name)
		}
	}
}

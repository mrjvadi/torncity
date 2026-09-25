package screens

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// War joins the snapshot harness as an area of its own —
// testdata/snapshots/<language>/war.txt — and its group lines join group.txt.
func init() { snapshotAreas["war"] = warSnapshots }

var (
	calderis    = RoomTarget{CityCode: "calderis", City: "Calderis", Country: otherCountry, WarNo: 3, DistanceKM: 520}
	vantorReach = RoomTarget{CityCode: "vantor_reach", City: "Vantor Reach", Country: otherCountry, WarNo: 3,
		DistanceKM: 1000, DamageBand: "heavy"}
)

// allyNames are a third country's name, the way the content names one.
var allyNames = map[string]string{"fa": "اتحادیهٔ شمال", "en": "Northern League"}

func warSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	ally := GovPlace{Kind: "country", Code: "ally", Name: allyNames[c.Lang]}
	active := WarLine{No: 3, Attacker: homeCountry, Defender: otherCountry, Ground: "territorial_claim", Status: "active",
		Since: 26 * time.Hour, Broke: true, CanPropose: true,
		Proposals: []ProposalLine{{No: 7, Kind: "ceasefire", Other: otherCountry, Incoming: true, ExpiresIn: 30 * time.Hour}}}
	declared := WarLine{No: 4, Attacker: otherCountry, Defender: homeCountry, Ground: "retaliation", Status: "declared",
		ActiveIn: 20 * time.Hour, ActiveAt: snapshotNow.Add(20 * time.Hour),
		DefenderAllies: []GovPlace{ally}}
	ceasefire := WarLine{No: 2, Attacker: homeCountry, Defender: otherCountry, Ground: "self_defence", Status: "ceasefire",
		Since: 5 * time.Hour, CanPropose: true, CanResume: true,
		Proposals: []ProposalLine{{No: 5, Kind: "peace", Other: otherCountry, ExpiresIn: 40 * time.Hour}}}
	ended := WarLine{No: 1, Attacker: otherCountry, Defender: homeCountry, Ground: "aggression", Status: "ended", Since: 72 * time.Hour}
	ops := []OperationLine{
		{No: 12, Kind: "air", Objective: "defences", Country: homeCountry, CityCode: "calderis", City: "Calderis",
			Target: otherCountry, LostBand: "few", EnemyLostBand: "few", Ago: 40 * time.Minute},
		{No: 11, Kind: "missile", Objective: "city", Country: otherCountry, CityCode: "kessmoor", City: "Kessmoor",
			Target: homeCountry, DamageBand: "moderate", LostBand: "unit", Ago: 3 * time.Hour},
		{No: 13, Kind: "ground", Objective: "take", Country: homeCountry, CityCode: "calderis", City: "Calderis",
			Target: otherCountry, Pending: true, StrikesIn: 6 * time.Minute},
		{No: 10, Kind: "ground", Objective: "take", Country: homeCountry, CityCode: "calderis", City: "Calderis",
			Target: otherCountry, Captured: true, Ago: 50 * time.Hour},
		{No: 9, Kind: "air", Objective: "city", Country: homeCountry, CityCode: "vantor_reach", City: "Vantor Reach",
			Target: otherCountry, CalledOff: true},
	}
	board := WarBoardView{Country: homeCountry, Wars: []WarLine{active, declared, ceasefire, ended},
		Occupied: []OccupationLine{{CityCode: "calderis", City: "Calderis", Controller: homeCountry, DeJure: otherCountry,
			Since: 50 * time.Hour}},
		Damaged:    []DamageLine{{CityCode: "kessmoor", City: "Kessmoor", Band: "moderate", ClosedIn: 4 * time.Minute}},
		Operations: ops, CanDeclare: true, CanCommand: true}
	add("Board · the president's", WarBoard(c, board))
	public := board
	public.CanDeclare, public.CanCommand = false, false
	add("Board · as a group reads it", WarBoard(group(c), public))
	add("Board · at peace", WarBoard(c, WarBoardView{Country: otherCountry}))
	add("Board · an ally called", WarBoard(c, WarBoardView{Country: ally,
		Joinable: []JoinLine{{WarNo: 4, Ally: homeCountry, Enemy: otherCountry}}, CanDeclare: true}))

	add("Declare · choose the country", Declare(c, DeclareView{Country: homeCountry, Targets: []GovPlace{otherCountry}}))
	target := otherCountry
	add("Declare · choose the ground", Declare(c, DeclareView{Country: homeCountry, Target: &target,
		Grounds: []string{"aggression", "self_defence", "territorial_claim", "protection_of_nationals", "retaliation"}}))
	add("Declare · confirm, breaking a pact", Declare(c, DeclareView{Country: homeCountry, Target: &target,
		Ground: "protection_of_nationals", Notice: 24 * time.Hour,
		Breaks: []Named{{Code: "non_aggression", Name: "Non-aggression pact"}},
		Allies: []GovPlace{ally}}))
	add("Declare · none left", Declare(c, DeclareView{Country: homeCountry}))

	for _, kind := range []string{"join", "ceasefire", "peace", "resume"} {
		add("Decide · "+kind, WarDecision(c, WarDecisionView{Kind: kind, Country: homeCountry, WarNo: 3, Other: otherCountry,
			Ally: ally, Notice: 24 * time.Hour, TTL: 48 * time.Hour}))
	}

	add("Room · the air force's commander", WarRoom(c, WarRoomView{Country: homeCountry, Readiness: 9500,
		Targets: []RoomTarget{calderis, vantorReach}, Running: ops[2:3]}))
	add("Room · no war", WarRoom(c, WarRoomView{Country: homeCountry, Readiness: 10000}))

	stealth := Named{Code: "stealth_fighter", Name: "Stealth fighters"}
	options := []ForceOption{
		{Kind: "air", Class: stealth, Ready: 3, FromCode: "kessmoor", From: "Kessmoor", DistanceKM: 520, Munitions: 8,
			CanLaunch: true, Office: "air_force_commander"},
		{Kind: "missile", Class: Named{Code: "ballistic", Name: "Ballistic missiles"}, Ready: 12, FromCode: "kessmoor",
			From: "Kessmoor", DistanceKM: 520, Office: "ground_forces_commander"},
		{Kind: "ground", Class: Named{Code: WarAllUnits}, Ready: 14, FromCode: "kessmoor", From: "Kessmoor", DistanceKM: 520,
			Office: "ground_forces_commander"},
	}
	add("Target · forces in reach", WarTarget(c, WarTargetView{Country: homeCountry, Target: vantorReach, Options: options,
		Occupied: &OccupationLine{DeJure: otherCountry}}))
	add("Target · nothing in reach", WarTarget(c, WarTargetView{Country: homeCountry, Target: calderis}))

	launch := LaunchView{Country: homeCountry, Target: calderis, Option: options[0], Prepare: time.Minute,
		Objectives: []string{"city", "defences"}}
	add("Launch · choose the objective", WarLaunch(c, launch))
	launch.Objective, launch.Quantities = "defences", []int64{1, 2, 3}
	add("Launch · how many", WarLaunch(c, launch))
	launch.Qty, launch.Confirm, launch.Munitions = 2, true, 4
	launch.Estimate = &Estimate{Chance: "likely", LossBand: "", DamageBand: "heavy"}
	add("Launch · the estimate, confirm", WarLaunch(c, launch))
	missile := LaunchView{Country: homeCountry, Target: vantorReach, Option: options[1], Prepare: 20 * time.Second,
		Objective: "city", Qty: 8, Confirm: true, Estimate: &Estimate{Chance: "even", LossBand: "unit", DamageBand: "moderate"}}
	add("Launch · a missile salvo", WarLaunch(c, missile))
	ground := LaunchView{Country: homeCountry, Target: calderis, Option: options[2], Prepare: 6 * time.Minute,
		Objective: "take", Qty: 14, Confirm: true, Estimate: &Estimate{Chance: "unlikely", LossBand: "unit"}}
	add("Launch · a ground assault", WarLaunch(c, ground))

	report := StrikeReportView{No: 12, Kind: "air", Objective: "defences", Country: homeCountry, Target: otherCountry,
		CityCode: "calderis", City: "Calderis", Class: stealth, Ours: true, Committed: 2, Lost: 0, EnemyLost: 2, EnemyDmg: 1,
		SeenAtKM: 38, Fired: 6, Munitions: 4, Hits: 3}
	add("Report · our air strike", StrikeReport(sent(c), report))
	theirs := StrikeReportView{No: 11, Kind: "missile", Objective: "city", Country: otherCountry, Target: homeCountry,
		CityCode: "kessmoor", City: "Kessmoor", Class: Named{Code: "cruise", Name: "Cruise missiles"}, Committed: 10, Lost: 4,
		Fired: 8, Hits: 5, DamageBPS: 1250, DamageBand: "light"}
	add("Report · a salvo on our city", StrikeReport(sent(c), theirs))
	assault := StrikeReportView{No: 10, Kind: "ground", Objective: "take", Country: homeCountry, Target: otherCountry,
		CityCode: "calderis", City: "Calderis", Class: Named{Code: WarAllUnits}, Ours: true, Committed: 14, Lost: 2,
		Damaged: 3, EnemyLost: 4, Captured: true}
	add("Report · a city taken", StrikeReport(sent(c), assault))
	held := assault
	held.Captured, held.EnemyLost = false, 1
	add("Report · an assault held off", StrikeReport(sent(c), held))
	add("Report · called off", StrikeReport(sent(c), StrikeReportView{No: 9, Kind: "air", Objective: "city",
		Country: homeCountry, Target: otherCountry, CityCode: "vantor_reach", City: "Vantor Reach", Class: stealth,
		Ours: true, CalledOff: true}))

	add("Notice · an ally attacked", WarNotice(sent(c), WarNoticeView{Kind: "ally",
		Country: ally, Other: otherCountry, Ally: homeCountry, WarNo: 4}))
	add("Notice · a ceasefire offered", WarNotice(sent(c), WarNoticeView{Kind: "proposal", Country: homeCountry,
		Other: otherCountry, WarNo: 3, ProposalNo: 7, ProposalKind: "ceasefire", In: 48 * time.Hour}))
	add("Notice · our city struck", WarNotice(sent(c), WarNoticeView{Kind: "struck", Country: homeCountry, Other: otherCountry,
		CityCode: "kessmoor", City: "Kessmoor", Band: "heavy"}))

	for _, r := range []WarRefusalView{
		{Kind: WarRefusedNotHolder, Country: homeCountry, Office: "president"},
		{Kind: WarRefusedNoCountry},
		{Kind: WarRefusedNotFound},
		{Kind: WarRefusedSelf, Country: homeCountry},
		{Kind: WarRefusedAtWar, Country: homeCountry},
		{Kind: WarRefusedState, Country: homeCountry},
		{Kind: WarRefusedNotEnemy, Country: homeCountry},
		{Kind: WarRefusedNotYet, Country: homeCountry, In: 20 * time.Minute},
		{Kind: WarRefusedNoForces, Country: homeCountry},
		{Kind: WarRefusedNoMunition, Country: homeCountry},
		{Kind: WarRefusedOpen, Country: homeCountry},
		{Kind: WarRefusedNoAlly, Country: homeCountry},
		{Kind: WarRefusedStock, Country: homeCountry, Max: 3},
		{Kind: "not_cleared", Country: homeCountry},
	} {
		add("Refused · "+r.Kind, WarRefusal(c, r))
	}
	add("Blocked · the border", WarBlocked(c, WarBlockedView{Border: true, From: homeCountry, To: otherCountry,
		Back: []string{AddrCities}}))
	add("Blocked · a struck city", WarBlocked(c, WarBlockedView{CityCode: "kessmoor", City: "Kessmoor", In: 5 * time.Minute,
		Back: []string{AddrCities}}))
}

// warAnnouncements are the lines the groups of the countries at war read.
func warAnnouncements(c Context, book *screentest.Book) {
	ally := GovPlace{Kind: "country", Code: "ally", Name: allyNames[c.Lang]}
	book.AddText("announcement · war declared", WarDeclaredAnnouncement(c, homeCountry, otherCountry, "territorial_claim",
		24*time.Hour, false))
	book.AddText("announcement · war declared, a pact broken", WarDeclaredAnnouncement(c, otherCountry, homeCountry,
		"retaliation", 24*time.Hour, true))
	book.AddText("announcement · an ally joined", WarJoinedAnnouncement(c,
		ally, homeCountry, otherCountry))
	for _, kind := range []string{"ceasefire", "peace", "resumed"} {
		book.AddText("announcement · "+kind, WarSettledAnnouncement(c, kind, homeCountry, otherCountry, 24*time.Hour))
	}
	book.AddText("announcement · a strike on a city", StrikeAnnouncement(c, otherCountry, "missile", "city", "kessmoor",
		"Kessmoor", homeCountry, "heavy"))
	book.AddText("announcement · a strike on air defences", StrikeAnnouncement(c, homeCountry, "air", "defences", "calderis",
		"Calderis", otherCountry, ""))
	book.AddText("announcement · a strike repelled", StrikeAnnouncement(c, homeCountry, "air", "repelled", "calderis",
		"Calderis", otherCountry, ""))
	book.AddText("announcement · an assault held off", StrikeAnnouncement(c, homeCountry, "ground", "held", "calderis",
		"Calderis", otherCountry, ""))
	book.AddText("announcement · a city taken", CityTakenAnnouncement(c, false, homeCountry, "calderis", "Calderis", otherCountry))
	book.AddText("announcement · a city liberated", CityTakenAnnouncement(c, true, otherCountry, "calderis", "Calderis",
		homeCountry))
}

// TestWarButtonsFitTelegram renders every decision of war with the longest
// codes the content ships and checks the decisive button is there: an
// address over Telegram's 64 bytes is dropped by the keyboard builder,
// silently.
func TestWarButtonsFitTelegram(t *testing.T) {
	c := Context{Msgs: catalogue(t), Lang: "fa"}
	far := otherCountry
	longTarget := RoomTarget{CityCode: "aldrin_hollow", City: "Aldrin Hollow", Country: far}
	stealth := ForceOption{Kind: "air", Class: Named{Code: "stealth_fighter"}, Ready: 999, FromCode: "fenwick_span",
		CanLaunch: true}
	for name, tc := range map[string]struct {
		resp   *presenter.Response
		prefix string
	}{
		"declare confirm": {Declare(c, DeclareView{Country: homeCountry, Target: &far, Ground: "protection_of_nationals"}),
			AddrWarDeclare + ":vantor_federation:protection_of_nationals:yes"},
		"launch confirm": {WarLaunch(c, LaunchView{Country: homeCountry, Target: longTarget, Option: stealth,
			Objective: "defences", Qty: 999, Confirm: true}), AddrWarLaunch + ":aldrin_hollow:air:stealth_fighter:defences:999:yes"},
		"propose confirm": {WarDecision(c, WarDecisionView{Kind: "ceasefire", Country: homeCountry, WarNo: 99999999}),
			AddrWarPropose + ":99999999:ceasefire:yes"},
		"answer": {WarNotice(c, WarNoticeView{Kind: "proposal", Country: homeCountry, ProposalNo: 99999999,
			ProposalKind: "ceasefire"}), AddrWarAnswer + ":99999999:decline"},
		"target": {WarRoom(c, WarRoomView{Country: homeCountry, Targets: []RoomTarget{longTarget}}),
			AddrWarTarget + ":aldrin_hollow"},
	} {
		found := false
		for _, row := range tc.resp.Keyboard.Rows {
			for _, b := range row {
				if b.CallbackData == tc.prefix {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s: no button %q (len %d) on the screen", name, tc.prefix, len(tc.prefix))
		}
	}
}

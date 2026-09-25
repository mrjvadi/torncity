package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// Factions join the snapshot harness as an area of their own —
// testdata/snapshots/<language>/factions.txt — and their group lines join
// group.txt.
func init() { snapshotAreas["factions"] = factionSnapshots }

var factionNames = map[string][2]string{"fa": {"شیرهای شمال", "سایه‌ها"}, "en": {"Northern Lions", "The Shades"}}

// applicants are two more players, named the way players of each language
// are.
var applicants = map[string][2]string{"fa": {"آرش", "دارا"}, "en": {"Arash", "Dara"}}

func sampleFactions(c Context) (FactionRef, FactionRef) {
	n := factionNames[c.Lang]
	return FactionRef{Code: "L10N5ZA", Name: n[0]}, FactionRef{Code: "SH4D3SB", Name: n[1]}
}

func factionSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	lions, shades := sampleFactions(c)
	me := GovPlayer{Name: who.me, Code: myCode}
	friend := GovPlayer{Name: who.friend, Code: friendCode}
	third := GovPlayer{Name: who.third, Code: thirdCode}
	heist := Named{Code: "warehouse_heist", Name: "Warehouse heist"}
	van := Named{Code: "armoured_van_job", Name: "Armoured van job"}
	industrial := Named{Code: "industrial_zone", Name: "Industrial zone"}
	business := Named{Code: "business_district", Name: "Business district"}

	add("List · a city's factions, the viewer in none", FactionList(c, FactionListView{CityCode: "ostmarch", City: "Ostmarch",
		Fee: 50000, Factions: []FactionLine{{Ref: lions, Members: 7}, {Ref: shades, Members: 3}}}))
	add("List · empty, the viewer a member", FactionList(c, FactionListView{CityCode: "kessmoor", City: "Kessmoor", Mine: &lions}))
	add("Page · as an outsider", FactionPage(c, FactionPageView{Ref: lions, CityCode: "ostmarch", City: "Ostmarch", Linked: true,
		CanApply: true, Members: []FactionMemberLine{{Player: me, Rank: "leader"}, {Player: friend, Rank: "officer"},
			{Player: third, Rank: "member"}}}))
	add("Page · as a group reads it", FactionPage(group(c), FactionPageView{Ref: shades, CityCode: "ostmarch", City: "Ostmarch",
		Members: []FactionMemberLine{{Player: friend, Rank: "leader"}}}))
	add("Page · its leader, in a group to link", FactionPage(group(c), FactionPageView{Ref: lions, CityCode: "ostmarch",
		City: "Ostmarch", Mine: true, CanLink: true, Members: []FactionMemberLine{{Player: me, Rank: "leader"}}}))

	add("Found · the fee and the ways to pay", FactionFound(c, FactionFoundView{CityCode: "ostmarch", City: "Ostmarch",
		Fee: 50000, NameMin: 3, NameMax: 24, Payment: PaymentChoice{Amount: 50000, Accepted: []string{MethodCash, MethodCard},
			Usable: []string{MethodCard}, Cash: 1200, Bank: 90000}}))
	add("Founded", FactionFounded(c, FactionFoundedView{Ref: lions, CityCode: "ostmarch", City: "Ostmarch", Fee: 50000,
		Method: MethodCard}))

	gathering := FactionOperationLine{No: 4, Status: application.HeistGathering, Crime: heist, Place: industrial,
		CityCode: "ostmarch", City: "Ostmarch", Min: 2, Max: 5, Nerve: 5, Left: 90 * time.Second, At: snapshotNow.Add(90 * time.Second),
		Crew: []FactionMemberLine{{Player: me, Rank: "leader"}}}
	running := gathering
	running.Status, running.ChanceBPS, running.Left, running.At = application.HeistRunning, 6200, 3*time.Minute, snapshotNow.Add(3*time.Minute)
	running.Crew = append(running.Crew, FactionMemberLine{Player: friend, Rank: "officer"}, FactionMemberLine{Player: third, Rank: "member"})
	add("Home · the leader, a crew gathering", FactionHome(c, FactionHomeView{Ref: lions, Rank: "leader", CityCode: "ostmarch",
		City: "Ostmarch", Members: 7, MaxMembers: 30, Bank: 184500, Applications: 2, Operation: &gathering,
		Rights: []string{"invite", "decide", "kick", "promote", "deposit", "withdraw", "link", "plan", "launch", "join"}}))
	add("Home · a member, linked", FactionHome(c, FactionHomeView{Ref: lions, Rank: "member", Linked: true, CityCode: "ostmarch",
		City: "Ostmarch", Members: 7, MaxMembers: 30, Bank: 184500, Rights: []string{"deposit", "join"}}))

	add("Members · the leader's view", FactionMembers(c, FactionMembersView{Ref: lions, Max: 30, CanInvite: true,
		Members: []FactionMemberLine{{Player: me, Rank: "leader", Self: true},
			{Player: friend, Rank: "officer", CanKick: true, CanDemote: true, CanLead: true},
			{Player: third, Rank: "member", CanKick: true, CanPromote: true, CanLead: true}},
		Requests: []FactionRequestLine{{No: 12, Kind: "apply", Player: GovPlayer{Name: applicants[c.Lang][0], Code: "A2R4S6H"}, CanDecide: true},
			{No: 13, Kind: "invite", Player: GovPlayer{Name: applicants[c.Lang][1], Code: "D4R4X1Y"}}}}))
	add("Invited", FactionInvited(c, third))
	add("Applied", FactionApplied(c, shades))
	add("Answered · joined by invitation", FactionAnswered(c, FactionAnsweredView{Ref: lions, Kind: "invite", Accepted: true, Player: me}))
	add("Answered · an application turned down", FactionAnswered(c, FactionAnsweredView{Ref: lions, Kind: "apply", Player: third}))
	for _, kind := range []string{FactionConfirmKick, FactionConfirmLead, FactionConfirmLeave, FactionConfirmDisband} {
		add("Confirm · "+kind, FactionConfirm(c, FactionConfirmView{Kind: kind, Ref: lions, Player: friend}))
	}
	add("Left", FactionLeft(c, FactionLeftView{Ref: lions}))
	add("Disbanded, the bank paid out", FactionLeft(c, FactionLeftView{Ref: lions, Disbanded: true, PaidOut: 12000}))
	add("Linked in a group", FactionLinked(group(c), FactionLinkedView{Ref: lions}))

	add("Bank · an officer after a deposit", FactionBank(c, FactionBankView{Ref: lions, Balance: 194500, CanDeposit: true,
		CanWithdraw: true, Cash: 3000, BankBalance: 42000, Min: 100, Max: 10000000, Methods: []string{MethodCash, MethodCard},
		Done: &FactionMoneyDone{Deposit: true, Amount: 10000, Method: MethodCash}}))
	add("Bank · a member after nothing", FactionBank(c, FactionBankView{Ref: lions, Balance: 194500, CanDeposit: true,
		Cash: 3000, BankBalance: 42000, Min: 100, Max: 10000000, Methods: []string{MethodCash, MethodCard}}))

	plans := []FactionPlanLine{
		{Crime: heist, Min: 2, Max: 5, Nerve: 5, MinLevel: 3, Duration: 3 * time.Minute, Places: []Named{industrial}},
		{Crime: van, Min: 3, Max: 6, Nerve: 8, MinLevel: 5, Duration: 4 * time.Minute, Places: []Named{business}},
	}
	add("Crime · nothing under way, an officer", FactionCrime(c, FactionCrimeView{Ref: lions, CanPlan: true, CanLaunch: true,
		CanJoin: true, CutBPS: 1500, Crimes: plans}))
	add("Crime · planned, a member may join", FactionCrime(c, FactionCrimeView{Ref: lions, Notice: FactionNoticePlanned,
		CanJoin: true, CutBPS: 1500, Operation: &gathering}))
	ready := gathering
	ready.Crew = append(ready.Crew, FactionMemberLine{Player: friend, Rank: "officer"})
	add("Crime · the crew ready, the leader", FactionCrime(c, FactionCrimeView{Ref: lions, Notice: FactionNoticeJoined,
		CanPlan: true, CanLaunch: true, CanJoin: true, InCrew: true, CutBPS: 1500, Operation: &ready}))
	add("Crime · under way, as a group reads it", FactionCrime(group(c), FactionCrimeView{Ref: lions,
		Notice: FactionNoticeLaunched, CutBPS: 1500, Operation: &running}))

	for _, kind := range []string{FactionRefusedNone, FactionRefusedNotMember, FactionRefusedRank, FactionRefusedNotFound,
		FactionRefusedAlreadyMember, FactionRefusedNameTaken, FactionRefusedNotGroup, FactionRefusedGroupTaken,
		FactionRefusedNoPlayer, FactionRefusedTheirs, FactionRefusedPendingFull, FactionRefusedPending,
		FactionRefusedRequestGone, FactionRefusedNotYours, FactionRefusedNotInIt, FactionRefusedOnAJob,
		FactionRefusedLeaderLeaving, FactionRefusedNoSuchCrime, FactionRefusedOperationOpen, FactionRefusedNoPlaceHere,
		FactionRefusedNoOperation, FactionRefusedCrewFull, FactionRefusedElsewhere} {
		add("Refused · "+kind, FactionRefusal(c, FactionRefusalView{Kind: kind}))
	}
	add("Refused · a name", FactionRefusal(c, FactionRefusalView{Kind: FactionRefusedName, Min: 3, Max: 24}))
	add("Refused · the bank short", FactionRefusal(c, FactionRefusalView{Kind: FactionRefusedBankShort, Amount: 50000, Balance: 12000}))
	add("Refused · full", FactionRefusal(c, FactionRefusalView{Kind: FactionRefusedFull, Max: 30}))
	add("Refused · level", FactionRefusal(c, FactionRefusalView{Kind: FactionRefusedLevel, Level: 5}))
	add("Refused · crew short", FactionRefusal(c, FactionRefusalView{Kind: FactionRefusedCrewShort, Need: 3, Have: 2}))
	add("Not here · the crew gathers elsewhere", NotHere(c, NotHereView{Need: "place.need.heist", Crime: heist,
		Place: industrial, Here: Named{Code: "bazaar", Name: "Bazaar"}, Walk: 30 * time.Second, Then: "faction.crime"}))

	add("Notice · invited", FactionRequestNotice(sent(c), FactionRequestNoticeView{No: 13, Kind: "invite", Ref: lions, Player: me}))
	add("Notice · an application", FactionRequestNotice(sent(c), FactionRequestNoticeView{No: 12, Kind: "apply", Ref: lions,
		Player: third}))
	add("Notice · the invitation accepted", FactionAnswerNotice(sent(c), FactionAnsweredView{Ref: lions, Kind: "invite",
		Accepted: true, Player: third}))
	add("Notice · the application accepted", FactionAnswerNotice(sent(c), FactionAnsweredView{Ref: lions, Kind: "apply",
		Accepted: true, Player: third}))
	add("Notice · the application declined", FactionAnswerNotice(sent(c), FactionAnsweredView{Ref: lions, Kind: "apply",
		Player: third}))
	add("Notice · kicked", FactionKickedNotice(sent(c), lions, who.me))
	add("Notice · the job went to plan", FactionCrimeNotice(sent(c), FactionCrimeNoticeView{Ref: lions, Crime: heist,
		Result: application.HeistSucceeded, Share: 2210, Take: 7800, Cut: 1170, XP: 30}))
	add("Notice · caught and hurt", FactionCrimeNotice(sent(c), FactionCrimeNoticeView{Ref: lions, Crime: van,
		Result: application.HeistCaught, XP: 0, Fine: 2400, FinePaid: 1000,
		Jail:   &CrimeProgress{Remaining: 18 * time.Minute, EndsAt: snapshotNow.Add(18 * time.Minute)},
		Injury: &InjuryView{Damage: 44, Health: 18, Max: 100, Hospital: true, EndsAt: snapshotNow.Add(7 * time.Minute)}}))
	add("Notice · escaped", FactionCrimeNotice(sent(c), FactionCrimeNoticeView{Ref: lions, Crime: heist,
		Result: application.HeistEscaped, Injury: &InjuryView{Damage: 12, Health: 70, Max: 100}}))
}

// factionAnnouncements are the lines a faction's group, or its city's, reads.
func factionAnnouncements(c Context, who people, book *screentest.Book) {
	lions, _ := sampleFactions(c)
	heist := Named{Code: "warehouse_heist", Name: "Warehouse heist"}
	industrial := Named{Code: "industrial_zone", Name: "Industrial zone"}
	book.AddText("faction · founded", FactionGroupLine(c, "founded", lions, who.me, Named{}, Named{}, "", ""))
	book.AddText("faction · joined", FactionGroupLine(c, "joined", lions, who.friend, Named{}, Named{}, "", ""))
	book.AddText("faction · left", FactionGroupLine(c, "left", lions, who.third, Named{}, Named{}, "", ""))
	book.AddText("faction · linked", FactionGroupLine(c, "linked", lions, who.me, Named{}, Named{}, "", ""))
	book.AddText("faction · ranked", FactionGroupLine(c, "ranked", lions, who.friend, Named{}, Named{}, "", "officer"))
	book.AddText("faction · disbanded", FactionGroupLine(c, "disbanded", lions, who.me, Named{}, Named{}, "", ""))
	book.AddText("faction · planned", FactionGroupLine(c, "planned", lions, who.me, heist, industrial, "", ""))
	book.AddText("faction · launched", FactionGroupLine(c, "launched", lions, "", heist, Named{}, "", ""))
	for _, r := range []string{application.HeistSucceeded, application.HeistEscaped, application.HeistCaught} {
		book.AddText("faction · crime resolved · "+r, FactionGroupLine(c, "crime_resolved", lions, "", heist, Named{}, r, ""))
	}
	for _, command := range []string{"faction.found", "faction.invite", "faction.deposit", "faction.withdraw"} {
		book.AddText("question · "+command, InputPrompt(c, command, ""))
		book.AddText("question · "+command+" · reply box", InputPlaceholder(c, command))
	}
}

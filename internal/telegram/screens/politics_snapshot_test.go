package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// Stage F politics join the snapshot harness as an area of their own —
// testdata/snapshots/<language>/politics.txt: a city's budget and the
// editor that divides it, proposals before a council or a parliament, a
// member's vote, and the public lines of a vote.
func init() { snapshotAreas["politics"] = politicsSnapshots }

func politicsSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	me := GovPlayer{Name: who.me, Code: myCode}
	friend := GovPlayer{Name: who.friend, Code: friendCode}
	third := GovPlayer{Name: who.third, Code: thirdCode}
	city := GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}
	country := GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	lines := []string{"police", "hospital", "transit", "education", "infrastructure", "marketing", "defence"}
	inForce := map[string]int64{"police": 3000, "transit": 2000, "defence": 1000}
	budgetLever := GovLever{Code: "city.budget", Type: application.LeverAllocation, HeldBy: "mayor",
		Notice: 24 * time.Hour, Cooldown: 72 * time.Hour, ConfirmBy: "city_council", FromOffice: true, SetBy: &me,
		Allocation: inForce, Categories: lines}

	// The budget screen.
	last := &BudgetPeriodView{Spent: 4200, Spendable: 6000, Lines: []BudgetLineView{
		{Code: "police", Effect: "investigation", Spent: 1800, EffectBPS: 180},
		{Code: "transit", Effect: "transit_fare", Spent: 1200, EffectBPS: 480},
		{Code: "defence", Effect: "defence_fund", Spent: 600},
		{Code: "hospital", Effect: "hospital_price"},
	}}
	add("Budget · the mayor's view", Budget(c, BudgetView{City: city, Lever: "city.budget", CanPropose: true,
		SpendShareBPS: 2000, Order: lines, Allocation: inForce, Treasury: 30000, Last: last,
		NextAt: snapshotNow.Add(14 * time.Minute), NextIn: 14 * time.Minute}))
	add("Budget · a new division announced", Budget(group(c), BudgetView{City: city, Lever: "city.budget",
		SpendShareBPS: 2000, Order: lines, Allocation: inForce, Pending: map[string]int64{"hospital": 5000, "infrastructure": 2500},
		PendingIn: 20 * time.Hour, Treasury: 30000, Last: last, NextAt: snapshotNow.Add(3 * time.Minute), NextIn: 3 * time.Minute}))
	add("Budget · nothing allocated, no period yet", Budget(c, BudgetView{City: city, Lever: "city.budget",
		SpendShareBPS: 2000, Order: lines, Treasury: 1200}))
	add("Budget · in no city", Budget(c, BudgetView{NoCity: true}))

	// The allocation editor.
	draft := map[string]int64{"police": 3000, "transit": 2000, "education": 500, "defence": 1000}
	var edit []AllocationLine
	for _, code := range lines {
		l := AllocationLine{Code: code, Share: draft[code], Up: "6400001"}
		if draft[code] > 0 {
			l.Down = "5400002"
		}
		edit = append(edit, l)
	}
	add("Allocation · editing the budget", AllocationEdit(c, AllocationEditView{Place: city, Lever: budgetLever,
		Draft: "6410002", Lines: edit, Total: 6500, SpendShareBPS: 2000, Changed: true}))
	add("Allocation · waiting for the cooldown", AllocationEdit(c, AllocationEditView{Place: city, Lever: budgetLever,
		Draft: "6400002", NextChangeIn: 40 * time.Hour, SpendShareBPS: 2000}))
	add("Allocation · confirm, to the council", AllocationConfirm(c, AllocationConfirmView{Place: city, Lever: budgetLever,
		Draft: "6410002", New: draft, VoteBy: "city_council"}))
	add("Allocation · confirm, announced at once", AllocationConfirm(c, AllocationConfirmView{Place: city, Lever: budgetLever,
		Draft: "6410002", New: draft}))
	add("Allocation · announced", PolicyAnnounced(c, PolicyAnnouncedView{Place: city, Lever: budgetLever,
		OldAllocation: inForce, NewAllocation: draft, In: 24 * time.Hour}))
	tax := GovLever{Code: "city.tax_rate", Type: "bps", Value: 450, Default: 450, Min: 0, Max: 2500, HeldBy: "mayor",
		Notice: 24 * time.Hour, Cooldown: 72 * time.Hour, ConfirmBy: "city_council"}
	add("Tax · a large change goes to the council", PolicyConfirm(c, PolicyConfirmView{Place: city, Lever: tax,
		NewValue: 1200, VoteBy: "city_council"}))
	add("Refusal · needs the council", PolicyRefused(c, PolicyRefusalView{
		Err: application.ErrPolicyRequiresConfirmation.WithDetail("body", "city_council"), Place: &city, Lever: &tax}))
	add("Refusal · an allocation beyond the whole", PolicyRefused(c, PolicyRefusalView{
		Err: application.ErrInvalidAllocation, Place: &city, Lever: &budgetLever}))

	// Proposals.
	budgetBill := BillView{No: 12, Place: city, Office: "mayor", By: me, Body: "city_council", Rule: "majority",
		Seats: 5, Held: 3, Needs: 2, Status: application.ProposalOpen, Yes: 1,
		Subject:  BillSubject{Kind: application.ProposalLever, Code: "city.budget", LeverType: application.LeverAllocation, Allocation: draft, Categories: lines},
		Votes:    []BillVoteLine{{Player: friend, Yes: true}},
		ClosesAt: snapshotNow.Add(47 * time.Hour), Remaining: 47 * time.Hour, CanVote: true}
	add("Proposal · a councillor may vote", Bill(c, budgetBill))
	submitted := budgetBill
	submitted.CanVote, submitted.Notice, submitted.Yes, submitted.Votes = false, BillNoticeSubmitted, 0, nil
	add("Proposal · just submitted", Bill(c, submitted))
	passed := budgetBill
	passed.Status, passed.Yes, passed.Nay, passed.CanVote, passed.Notice = application.ProposalPassed, 2, 1, false, BillNoticeVoted
	passed.Votes = []BillVoteLine{{Player: friend, Yes: true}, {Player: third, Yes: true}, {Player: me}}
	add("Proposal · passed by the vote just cast", Bill(c, passed))
	war := BillView{No: 13, Place: country, Office: "president", By: friend, Body: "parliament", Rule: "supermajority",
		Threshold: "2/3", Quorum: "1/2", Seats: 9, Held: 6, Needs: 4, Status: application.ProposalFailed, Yes: 2, Nay: 3,
		Subject: BillSubject{Kind: application.ProposalAction, Code: "country.war",
			Target: &GovPlace{Kind: "country", Code: "vantor_federation", Name: "Vantor Federation"}}}
	add("Proposal · a declaration of war rejected", Bill(group(c), war))
	taxBill := BillView{No: 14, Place: city, Office: "city_council", By: third, Body: "city_council", Rule: "majority",
		Quorum: "1/2", Seats: 5, Held: 5, Needs: 3, Status: application.ProposalLapsed, LapsedWhy: "cooldown", Yes: 3,
		Subject: BillSubject{Kind: application.ProposalLever, Code: "city.property_tax", LeverType: "bps", Value: 50}}
	add("Proposal · passed but lapsed", Bill(c, taxBill))
	add("Proposals · the list", Bills(c, BillsView{Bills: []BillView{budgetBill, war, taxBill}}))
	add("Proposals · none", Bills(c, BillsView{}))
	for _, kind := range []string{BillRefusedNotFound, BillRefusedNotMember, BillRefusedUnderWay} {
		add("Refused · "+kind, BillRefusal(c, BillRefusalView{Kind: kind, No: 12, Body: "city_council"}))
	}
	add("Notice · your proposal passed", BillDecidedNotice(sent(c), passed))
	add("Notice · your proposal was rejected", BillDecidedNotice(sent(c), war))
}

// politicsAnnouncements are the public lines of a vote; they join the group
// lines (group.txt).
func politicsAnnouncements(c Context, who people, book *screentest.Book) {
	me := GovPlayer{Name: who.me, Code: myCode}
	city := GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}
	country := GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	lines := []string{"police", "hospital", "transit", "education", "infrastructure", "marketing", "defence"}
	budget := BillView{No: 12, Place: city, Office: "mayor", By: me, Body: "city_council",
		Subject: BillSubject{Kind: application.ProposalLever, Code: "city.budget", LeverType: application.LeverAllocation,
			Allocation: map[string]int64{"police": 3000, "transit": 2000}, Categories: lines}}
	passed := budget
	passed.Status, passed.Yes, passed.Nay = application.ProposalPassed, 3, 1
	war := BillView{No: 13, Place: country, Office: "president", By: me, Body: "parliament",
		Status: application.ProposalFailed, Yes: 2, Nay: 5,
		Subject: BillSubject{Kind: application.ProposalAction, Code: "country.war",
			Target: &GovPlace{Kind: "country", Code: "vantor_federation", Name: "Vantor Federation"}}}
	lapsed := BillView{No: 14, Place: city, Body: "city_council", Status: application.ProposalLapsed, LapsedWhy: "cooldown",
		Subject: BillSubject{Kind: application.ProposalLever, Code: "city.property_tax", LeverType: "bps", Value: 50}}
	book.AddText("announcement · a proposal before the council", BillOpenedAnnouncement(c, budget, 48*time.Hour))
	book.AddText("announcement · a declaration of war before parliament", BillOpenedAnnouncement(c, war, 48*time.Hour))
	book.AddText("announcement · a proposal passed", BillDecidedAnnouncement(c, passed))
	book.AddText("announcement · a proposal rejected", BillDecidedAnnouncement(c, war))
	book.AddText("announcement · a proposal lapsed", BillDecidedAnnouncement(c, lapsed))
	words := "The markets close early tonight for maintenance."
	if c.Lang == "fa" {
		words = "بازارها امشب برای نگهداری زودتر بسته می‌شوند."
	}
	book.AddText("announcement · the operators speak", OperatorAnnouncement(c, words))
}

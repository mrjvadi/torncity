package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The teaching network joins the snapshot harness as an area of its own (docs/adr/0071).
func init() { snapshotAreas["apprentice"] = apprenticeSnapshots }

func apprenticeSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	names := map[string][3]string{"fa": {"رضا", "کارگاه نجاری", "نجار"}, "en": {"Reza", "Carpentry workshop", "carpenter"}}[c.Lang]
	shop := Named{Code: "w1", Name: names[1]}
	master := Named{Code: "M4ST3RX", Name: names[0]}
	offer := ApprenticeLine{ID: "a1", Craft: "carpentry", Role: "apprentice", With: master, Workshop: shop, Status: "offered", Fee: 400, WageShareBPS: 5000,
		TermDays: 28, ExamShifts: 3, CanAccept: true, CanLeave: true}
	running := ApprenticeLine{ID: "a2", Craft: "weaving", Role: "apprentice", NPC: true, Workshop: shop, Status: "active", TermDays: 28, WageShareBPS: 5000,
		EndsAt: snapshotNow.Add(20 * 24 * time.Hour), Shifts: 2, ExamShifts: 3, CanLeave: true}
	mine := ApprenticeLine{ID: "a3", Craft: "carpentry", Role: "master", With: Named{Code: "APPRNT1", Name: names[2]}, Workshop: shop, Status: "completed",
		TermDays: 28, WageShareBPS: 5000, Shifts: 5, ExamShifts: 3, CanExam: true}
	done := ApprenticeLine{ID: "a4", Craft: "pottery", Role: "apprentice", With: master, Workshop: shop, Status: "completed", Shifts: 4, ExamShifts: 3, Certified: true}
	add("Apprentices · an offer, a pool master, an exam to judge", Apprentices(c, ApprenticesView{Lines: []ApprenticeLine{offer, running, mine, done},
		CanJoin: []ApprenticeJoinLine{{Workshop: Named{Code: "w2", Name: names[1]}, Craft: "carpentry", UpTo: 2}}, Notice: "started"}))
	add("Apprentices · nothing yet", Apprentices(c, ApprenticesView{}))
	add("Teachers · a craft with a class, a master, the pool and the city", Teachers(c, TeachersView{Craft: "carpentry", Course: Named{Code: "carpentry_craft", Name: names[1]},
		Crafts: []string{"carpentry", "weaving"}, Entries: []TeacherEntry{
			{Kind: "class", Settlement: Named{Code: "ostmarch", Name: "Ostmarch"}, Teacher: master, CraftLevel: 5, TeachingLevel: 2, UpTo: 4, Fee: 800, FreeSeats: 9, Here: true},
			{Kind: "master", Settlement: Named{Code: "fenwick_span", Name: "Fenwick Span"}, KM: 120, Teacher: master, CraftLevel: 4, UpTo: 4, FreeSeats: 2},
			{Kind: "npc_master", Settlement: Named{Code: "fenwick_span", Name: "Fenwick Span"}, KM: 120, CraftLevel: 2, UpTo: 2},
			{Kind: "city", Settlement: Named{Code: "support", Name: "Support"}, Fee: 800, FreeSeats: 99},
		}}))
	add("Teachers · nobody near", Teachers(c, TeachersView{Craft: "weaving", Course: Named{Code: "weaving_craft", Name: names[1]}, Crafts: []string{"carpentry", "weaving"}}))
	for _, k := range []string{"not_master", "range", "cannot_afford", "exam_not_ready", "too_skilled"} {
		add("Apprentice refusal · "+k, ApprenticeRefusal(c, ApprenticeRefusalView{Kind: k}))
	}
}

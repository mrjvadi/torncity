package charter

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func TestElectionCalendar(t *testing.T) {
	s := Defaults()
	if s.PhaseAt(t0, t0.Add(47*time.Hour)) != PhaseCandidacy || s.PhaseAt(t0, t0.Add(48*time.Hour)) != PhaseVoting ||
		s.PhaseAt(t0, t0.Add(119*time.Hour)) != PhaseVoting || s.PhaseAt(t0, t0.Add(120*time.Hour)) != PhaseClosed {
		t.Error("the election calendar: 48h candidacy, 72h voting")
	}
	if !s.ElectionCloses(t0).Equal(t0.Add(120 * time.Hour)) {
		t.Error("closing time")
	}
}

// A recall of 20 percent of six people would be one person: the floor makes it three,
// and never more than everyone.
func TestRecallThresholdsHaveAFloor(t *testing.T) {
	s := Defaults()
	for eligible, want := range map[int]int{1: 1, 2: 2, 3: 3, 6: 3, 15: 3, 16: 4, 50: 10, 100: 20, 101: 21} {
		if got := s.RecallSignatures(eligible); got != want {
			t.Errorf("eligible %d: %d signatures, want %d", eligible, got, want)
		}
	}
}

func TestRecallNeedsTenure(t *testing.T) {
	s := Defaults()
	since := t0
	if s.RecallTenureOK(since, since.Add(5*24*time.Hour-time.Second)) || !s.RecallTenureOK(since, since.Add(5*24*time.Hour)) {
		t.Error("a holder is safe for the first five days")
	}
	if !s.RecallCoolingUntil(t0).Equal(t0.Add(14 * 24 * time.Hour)) {
		t.Error("cool-down")
	}
}

func TestCarries(t *testing.T) {
	if !Carries(3, 1, 3) || Carries(2, 1, 4) || Carries(2, 2, 2) || Carries(0, 0, 1) {
		t.Error("a ballot carries on more yes than no with the quorum cast")
	}
	s := Defaults()
	if s.QuorumVotes(4) != 3 || s.QuorumVotes(100) != 30 || s.QuorumVotes(2) != 2 {
		t.Errorf("quorum %d %d %d", s.QuorumVotes(4), s.QuorumVotes(100), s.QuorumVotes(2))
	}
	if s.NeedsVote(5) || !s.NeedsVote(6) {
		t.Error("a small settlement has nobody to ask")
	}
}

func TestStructuralChanges(t *testing.T) {
	base := Office{Title: "کلانتر", Seats: 1, Acquisition: AcquireAppointment, Grants: []Grant{{StorageTake, 0}, {RoadDraw, 0}}}
	rename := base
	rename.Title = "نگهبان"
	rename.Seats = 3
	rename.Grants = []Grant{{StorageTake, 0}, {RoadDraw, 0}, {JobsPost, 0}}
	if Structural(base, rename) {
		t.Error("a rename, seats and ordinary permissions are not structural")
	}
	key := base
	key.Grants = append(append([]Grant(nil), base.Grants...), Grant{OfficeAppoint, 0})
	if !Structural(base, key) {
		t.Error("adding office.appoint is structural")
	}
	limit := base
	limit.Grants = []Grant{{TreasurySpend, 100}}
	limit2 := limit
	limit2.Grants = []Grant{{TreasurySpend, 200}}
	if !Structural(limit, limit2) {
		t.Error("a spending ceiling is structural")
	}
	elected := base
	elected.Acquisition = AcquireElection
	if !Structural(base, elected) || !ClosingNeedsVote(elected) || ClosingNeedsVote(base) {
		t.Error("how an office is filled is structural; closing an elected office needs the vote")
	}
	dep := base
	dep.Deputy = true
	if !Structural(base, dep) {
		t.Error("naming the deputy is structural")
	}
}

func TestActingGrants(t *testing.T) {
	got := Held{}
	for _, g := range ActingGrants(AllPermissions(), 2000) {
		got[g.Permission] = g.Limit
	}
	for _, p := range []Permission{CharterAmend, OfficeCreate, OfficeEdit, OfficeAppoint, OfficeDismiss, TreatyPropose, SettingsTimezone} {
		if _, ok := got[p]; ok {
			t.Errorf("an acting head holds %s", p)
		}
	}
	if l, ok := got[TreasurySpend]; !ok || l != 2000 {
		t.Errorf("an acting head's spend = %d %v, want capped at 2000", l, ok)
	}
	if _, ok := got[RoadDraw]; !ok {
		t.Error("an acting head runs the day-to-day (roads)")
	}
	if l := ActingGrants([]Grant{{TreasurySpend, 500}}, 2000)[0].Limit; l != 500 {
		t.Errorf("a lower ceiling stays lower: %d", l)
	}
}

func TestActingHeadIsTheDeputyThenTheLongestServing(t *testing.T) {
	seats := []SeatInfo{
		{"c", "sheriff", t0.Add(-3 * time.Hour)},
		{"a", "clerk", t0.Add(-10 * time.Hour)},
		{"b", "deputy", t0.Add(-1 * time.Hour)},
		{"d", "deputy", t0.Add(-2 * time.Hour)},
	}
	if s, ok := ActingHead(seats, "deputy"); !ok || s.PlayerID != "d" {
		t.Errorf("the deputy office's longest serving: %+v %v", s, ok)
	}
	if s, ok := ActingHead(seats, ""); !ok || s.PlayerID != "a" {
		t.Errorf("no deputy office: the longest serving of all: %+v %v", s, ok)
	}
	if s, ok := ActingHead(seats[:1], "deputy"); !ok || s.PlayerID != "c" {
		t.Errorf("a deputy office with nobody in it falls back: %+v %v", s, ok)
	}
	if _, ok := ActingHead(nil, "deputy"); ok {
		t.Error("nobody can act")
	}
	if !Defaults().ActingEnds(t0).Equal(t0.Add(7 * 24 * time.Hour)) {
		t.Error("an acting head acts for seven days")
	}
}

func TestActingForJudgesTheVacancy(t *testing.T) {
	set := Defaults()
	offices := []Office{FoundersOffice("h", "head"), {ID: "d", Title: "deputy", Seats: 1, Acquisition: AcquireAppointment, Deputy: true, Grants: []Grant{{JobsPost, 0}}}}
	holders := map[string][]SeatInfo{"d": {{PlayerID: "p", OfficeID: "d", Since: t0}}}
	player, office, grants, ends, ok := ActingFor(offices, holders, t0, set, t0.Add(time.Hour))
	if !ok || player != "p" || office != "d" || !ends.Equal(t0.Add(7*24*time.Hour)) {
		t.Fatalf("%v %v %v %v", player, office, ends, ok)
	}
	got := Held{}
	for _, g := range grants {
		got[g.Permission] = g.Limit
	}
	if _, has := got[CharterAmend]; has || got[TreasurySpend] != 2000 {
		t.Errorf("acting grants %+v", got)
	}
	if _, _, _, _, ok := ActingFor(offices, holders, t0, set, t0.Add(7*24*time.Hour)); ok {
		t.Error("an acting head acted after the acting days")
	}
	if _, _, _, _, ok := ActingFor(offices, nil, t0, set, t0); ok {
		t.Error("nobody holds a seat, nobody acts")
	}
}

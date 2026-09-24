package election

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

var rules = Rules{
	Candidacy: 48 * time.Hour, Voting: 48 * time.Hour, MinLevel: 5, MinResidency: 168 * time.Hour,
	CleanRecord: true, Deposit: money.FromMinor(5000), RefundShareBPS: 1000, VoterMinResidency: 24 * time.Hour,
	ReopenAfter: 168 * time.Hour,
}

func TestRulesValidate(t *testing.T) {
	if err := rules.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := rules
	bad.Voting, bad.RefundShareBPS = 0, 20_000
	if err := bad.Validate(); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("Validate = %v, want ErrInvalidRules", err)
	}
}

func TestScheduleAndPhases(t *testing.T) {
	open := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s := Plan(rules, open)
	if got, want := s.CandidacyEndsAt, open.Add(48*time.Hour); !got.Equal(want) {
		t.Fatalf("candidacy ends %s, want %s", got, want)
	}
	if got, want := s.VotingEndsAt, open.Add(96*time.Hour); !got.Equal(want) {
		t.Fatalf("voting ends %s, want %s", got, want)
	}
	for _, tc := range []struct {
		at   time.Duration
		want Phase
	}{{0, Candidacy}, {47 * time.Hour, Candidacy}, {48 * time.Hour, Voting}, {95 * time.Hour, Voting}, {96 * time.Hour, Counting}} {
		if got := s.At(open.Add(tc.at)); got != tc.want {
			t.Errorf("at +%s: %s, want %s", tc.at, got, tc.want)
		}
	}
}

func TestEligibility(t *testing.T) {
	ok := Person{Resident: true, ResidentFor: 200 * time.Hour, Level: 6}
	if err := CanStand(rules, ok); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		p   Person
		why string
	}{
		{Person{ResidentFor: 200 * time.Hour, Level: 6}, WhyNotResident},
		{Person{Resident: true, ResidentFor: time.Hour, Level: 6}, WhyTooNew},
		{Person{Resident: true, ResidentFor: 200 * time.Hour, Level: 2}, WhyLevel},
		{Person{Resident: true, ResidentFor: 200 * time.Hour, Level: 6, Jailed: true}, WhyJailed},
		{Person{Resident: true, ResidentFor: 200 * time.Hour, Level: 6, Owes: true}, WhyRecord},
	} {
		var r Refusal
		err := CanStand(rules, tc.p)
		if !errors.As(err, &r) || r.Why != tc.why || !errors.Is(err, ErrNotEligible) {
			t.Errorf("CanStand(%+v) = %v, want %s", tc.p, err, tc.why)
		}
	}
	lenient := rules
	lenient.CleanRecord = false
	if err := CanStand(lenient, Person{Resident: true, ResidentFor: 200 * time.Hour, Level: 6, Owes: true}); err != nil {
		t.Errorf("without a clean-record rule a debtor may stand: %v", err)
	}
	if err := CanVote(rules, Person{Resident: true, ResidentFor: 25 * time.Hour}); err != nil {
		t.Error(err)
	}
	if err := CanVote(rules, Person{Resident: true, ResidentFor: time.Hour}); err == nil {
		t.Error("a newcomer voted")
	}
}

func TestCountFillsSeatsInOrder(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	res := Count([]Candidate{
		{PlayerID: "c", StoodAt: t0.Add(2 * time.Minute), Votes: 5},
		{PlayerID: "a", StoodAt: t0.Add(time.Minute), Votes: 5},
		{PlayerID: "b", StoodAt: t0, Votes: 9},
		{PlayerID: "d", StoodAt: t0, Votes: 0},
	}, 2)
	if res.Cast != 19 {
		t.Fatalf("cast = %d, want 19", res.Cast)
	}
	if len(res.Elected) != 2 || res.Elected[0].PlayerID != "b" || res.Elected[1].PlayerID != "a" {
		t.Fatalf("elected = %+v, want b then a (the earlier candidacy wins the tie)", res.Elected)
	}
	if res.Ranked[3].PlayerID != "d" {
		t.Fatalf("ranked = %+v", res.Ranked)
	}
	// A seat is never filled by a candidate nobody voted for.
	if got := Count([]Candidate{{PlayerID: "x"}}, 1); len(got.Elected) != 0 {
		t.Fatalf("an unvoted candidate was elected: %+v", got.Elected)
	}
}

func TestRefund(t *testing.T) {
	if !Refunded(rules, 10, 100) || Refunded(rules, 9, 100) {
		t.Fatal("the refund line is 10% of the votes cast")
	}
	if !Refunded(rules, 0, 0) {
		t.Fatal("with no votes cast every deposit comes back")
	}
}

func TestNextOpening(t *testing.T) {
	counted := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	term := 720 * time.Hour
	if at, ok := NextOpening(rules, term, counted, true); !ok || !at.Equal(counted.Add(term-96*time.Hour)) {
		t.Fatalf("filled: %s %v, want the count of the next election at the term's end", at, ok)
	}
	if at, ok := NextOpening(rules, term, counted, false); !ok || !at.Equal(counted.Add(rules.ReopenAfter)) {
		t.Fatalf("unfilled: %s %v, want reopen_after later", at, ok)
	}
	if at, ok := NextOpening(rules, 24*time.Hour, counted, true); !ok || !at.Equal(counted) {
		t.Fatalf("short term: %s %v, want at once", at, ok)
	}
	if _, ok := NextOpening(rules, 0, counted, true); ok {
		t.Fatal("an office held at pleasure opened a next election")
	}
}

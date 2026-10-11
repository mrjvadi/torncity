package household

import (
	"errors"
	"fmt"
	"testing"
)

func testRules() Rules {
	return Rules{
		Weights:              Weights{Food: 3000, Water: 2500, Warmth: 1500, Shelter: 1500, Work: 1000, Care: 500},
		SatisfactionAlphaBPS: 2000, LeaveBelowBPS: 3000, LeaveAfterDays: 3, ArriveAboveBPS: 5000, VacancyMarginBPS: 0, ArrivalsPerDay: 2,
		BirthAboveBPS: 8000, BirthChanceBPS: 200, EarnerBPS: 6000, DependantWeightBPS: 5000, DependantToEarnerDays: 60,
		Compositions: []Composition{{1, 0, 2}, {2, 0, 4}, {2, 1, 3}, {2, 2, 2}},
	}
}

func TestRulesValidate(t *testing.T) {
	if err := testRules().Validate(); err != nil {
		t.Fatalf("the test rules: %v", err)
	}
	bad := testRules()
	bad.Weights.Care = 400
	if err := bad.Validate(); !errors.Is(err, ErrInvalidRules) {
		t.Errorf("weights not adding up to 10000: %v", err)
	}
	bad = testRules()
	bad.Compositions = nil
	if err := bad.Validate(); !errors.Is(err, ErrInvalidRules) {
		t.Errorf("no compositions: %v", err)
	}
	if err := (Rules{}).Validate(); err == nil {
		t.Error("the zero value must be invalid")
	}
}

// The same seed gives the same household on every replica; the mean number of earners over many seeds is the rule's.
func TestComposeIsDeterministicAndMeanEarners(t *testing.T) {
	r := testRules()
	if a, b := r.Compose(42), r.Compose(42); a != b {
		t.Fatalf("one seed, two households: %+v %+v", a, b)
	}
	var earners, n int64
	for i := 0; i < 20000; i++ {
		s := r.Compose(Hash("settlement", fmt.Sprint(i)))
		if s.Earners > s.Adults || s.Adults < 1 {
			t.Fatalf("a household of %+v", s)
		}
		earners += s.Earners
		n++
	}
	mean := earners * BPS / n
	if mean < r.EarnerBPS-300 || mean > r.EarnerBPS+300 {
		t.Errorf("the mean earners a household is %d bps, want about %d", mean, r.EarnerBPS)
	}
}

// With one adult who always works, every household has exactly one earner: the flip can be exactly size-neutral.
func TestComposeExactEarners(t *testing.T) {
	r := testRules()
	r.Compositions = []Composition{{1, 0, 1}}
	r.EarnerBPS = BPS
	for i := 0; i < 50; i++ {
		if s := r.Compose(uint64(i)); s.Earners != 1 || s.Adults != 1 {
			t.Fatalf("seed %d: %+v", i, s)
		}
	}
}

func TestQuantityAndRatio(t *testing.T) {
	r := testRules()
	persons := r.PersonsBPS(2, 2) // 2 adults + 2 children at half
	if persons != 30000 {
		t.Fatalf("persons = %d", persons)
	}
	if q := Quantity(persons, 1000); q != 3 {
		t.Errorf("a litre each: %d", q)
	}
	if q := Quantity(persons, 500); q != 2 { // 1.5 rounded up
		t.Errorf("half each: %d", q)
	}
	if Quantity(0, 1000) != 0 || Quantity(persons, 0) != 0 {
		t.Error("nothing needed, nothing asked")
	}
	if Ratio(1, 0) != BPS || Ratio(0, 5) != 0 || Ratio(9, 5) != BPS || Ratio(1, 4) != 2500 {
		t.Error("ratio")
	}
}

func TestScoreAndWorst(t *testing.T) {
	w := testRules().Weights
	full := Met{BPS, BPS, BPS, BPS, BPS, BPS}
	if w.Score(full) != BPS || full.Worst() != "" {
		t.Errorf("all met: %d %q", w.Score(full), full.Worst())
	}
	m := Met{Food: 0, Water: BPS, Warmth: BPS, Shelter: BPS, Work: BPS, Care: BPS}
	if got := w.Score(m); got != 7000 {
		t.Errorf("no food scores %d", got)
	}
	if m.Worst() != NeedFood {
		t.Errorf("worst = %q", m.Worst())
	}
	m.Water = 100
	if m.Worst() != NeedFood {
		t.Errorf("worst on a tie of lowest = %q", m.Worst())
	}
}

// A household that stays unhappy for LeaveAfterDays days in a row leaves; one good day in between breaks the run.
func TestDepartureByDays(t *testing.T) {
	r := testRules()
	r.SatisfactionAlphaBPS = BPS // the average is the day's score
	s := State{SatisfactionBPS: 7000}
	var leave bool
	for day := 1; day <= 3; day++ {
		s, leave = r.Next(s, 1000)
		if leave != (day == 3) {
			t.Fatalf("day %d: leave = %v (low days %d)", day, leave, s.LowDays)
		}
	}
	s, _ = r.Next(State{SatisfactionBPS: 1000, LowDays: 2}, 9000)
	if s.LowDays != 0 {
		t.Errorf("a good day must break the run: %+v", s)
	}
}

func TestSatisfactionMovesSlowly(t *testing.T) {
	r := testRules()
	s := State{SatisfactionBPS: 8000}
	s, _ = r.Next(s, 0)
	if s.SatisfactionBPS != 6400 {
		t.Errorf("one bad day moves 8000 by a fifth of the gap: %d", s.SatisfactionBPS)
	}
}

func TestArrivals(t *testing.T) {
	r := testRules()
	if n := r.Arrivals(8, 0, BPS, true, 0); n != 2 {
		t.Errorf("an empty settlement takes the first arrivals even with no post: %d", n)
	}
	if n := r.Arrivals(8, 3, BPS, true, 0); n != 0 {
		t.Errorf("no post open and people living: no pull, got %d", n)
	}
	if n := r.Arrivals(8, 3, BPS, true, 4); n != 2 {
		t.Errorf("a post open: %d", n)
	}
	if n := r.Arrivals(8, 7, BPS, true, 4); n != 1 {
		t.Errorf("one free home: %d", n)
	}
	if n := r.Arrivals(8, 8, BPS, true, 4); n != 0 {
		t.Errorf("no free home: %d", n)
	}
	if n := r.Arrivals(8, 3, 4000, true, 4); n != 0 {
		t.Errorf("an unhappy settlement draws nobody: %d", n)
	}
	if n := r.Arrivals(8, 3, BPS, false, 4); n != 0 {
		t.Errorf("a closed settlement admits nobody: %d", n)
	}
	r.VacancyMarginBPS = 2500
	if n := r.Arrivals(8, 6, BPS, true, 4); n != 0 {
		t.Errorf("the vacancy margin keeps two homes free: %d", n)
	}
}

func TestBorn(t *testing.T) {
	r := testRules()
	r.BirthChanceBPS = BPS
	if !r.Born(State{SatisfactionBPS: 9000}, 1, 7) {
		t.Error("a satisfied household with room has a child")
	}
	if r.Born(State{SatisfactionBPS: 9000}, 0, 7) {
		t.Error("no room, no child")
	}
	if r.Born(State{SatisfactionBPS: 5000}, 1, 7) {
		t.Error("an unhappy household has no child")
	}
}

// The seat's holder is the same on every call, a hash of the key over the earners ordered by id, and a household with more earners
// holds proportionally more of the seats.
func TestSeatOwner(t *testing.T) {
	hs := []Earners{{"b", 2}, {"a", 1}, {"c", 0}}
	rev := []Earners{{"c", 0}, {"a", 1}, {"b", 2}}
	counts := map[string]int{}
	for i := 0; i < 3000; i++ {
		key := fmt.Sprint("seat", i)
		x, y := SeatOwner(key, hs), SeatOwner(key, rev)
		if x != y {
			t.Fatalf("the order of the list changed the holder: %q %q", x, y)
		}
		counts[x]++
	}
	if counts["c"] != 0 || counts[""] != 0 {
		t.Errorf("a household with no earner holds a seat: %v", counts)
	}
	if counts["b"] < counts["a"]*15/10 {
		t.Errorf("two earners should hold about twice the seats of one: %v", counts)
	}
	if SeatOwner("x", nil) != "" || SeatOwner("x", []Earners{{"a", 0}}) != "" {
		t.Error("nobody works, nobody holds the seat")
	}
}

func TestMaxDependants(t *testing.T) {
	if got := testRules().MaxDependants(); got != 3 {
		t.Errorf("the largest family has two children, so a household keeps three: %d", got)
	}
}

func TestMoodAndCapped(t *testing.T) {
	if Mood(nil) != BPS || Mood([]int64{2000, 4000}) != 3000 {
		t.Error("mood")
	}
	if Capped(100, 50, 120) != 20 || Capped(100, 50, 0) != 50 || Capped(200, 50, 120) != 0 {
		t.Error("capped")
	}
}

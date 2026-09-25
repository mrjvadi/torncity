package legislature

import "testing"

func TestDecide(t *testing.T) {
	maj := Rule{Kind: Majority}
	two3 := Rule{Kind: Supermajority, Num: 2, Den: 3}
	una := Rule{Kind: Unanimous}
	quorum := Rule{Kind: Majority, QuorumNum: 1, QuorumDen: 2}
	tests := []struct {
		name  string
		rule  Rule
		tally Tally
		final bool
		want  Outcome
	}{
		{"majority, nothing cast", maj, Tally{Seats: 5, Held: 5}, false, Pending},
		{"majority, three of five yes", maj, Tally{Seats: 5, Held: 5, Yes: 3}, false, Passed},
		{"majority, two yes, three to come", maj, Tally{Seats: 5, Held: 5, Yes: 2}, false, Pending},
		{"majority, three of five no", maj, Tally{Seats: 5, Held: 5, No: 3}, false, Failed},
		{"majority, a tie at the close", maj, Tally{Seats: 4, Held: 4, Yes: 2, No: 2}, true, Failed},
		{"majority, one yes at the close", maj, Tally{Seats: 5, Held: 5, Yes: 1}, true, Passed},
		{"majority, nobody voted", maj, Tally{Seats: 5, Held: 5}, true, Failed},
		{"majority, nobody seated", maj, Tally{Seats: 5}, false, Failed},
		{"quorum not met at the close", quorum, Tally{Seats: 5, Held: 5, Yes: 2}, true, Failed},
		{"quorum met at the close", quorum, Tally{Seats: 5, Held: 5, Yes: 2, No: 1}, true, Passed},
		{"quorum out of reach", quorum, Tally{Seats: 5, Held: 2}, false, Failed},
		{"two thirds, four of six already", two3, Tally{Seats: 6, Held: 6, Yes: 4}, false, Passed},
		{"two thirds, three of six", two3, Tally{Seats: 6, Held: 6, Yes: 3}, false, Pending},
		{"two thirds, four of six at the close", two3, Tally{Seats: 6, Held: 6, Yes: 4, No: 2}, true, Passed},
		{"two thirds, three of six against", two3, Tally{Seats: 6, Held: 6, No: 3}, false, Failed},
		{"unanimous, one against", una, Tally{Seats: 3, Held: 3, Yes: 2, No: 1}, false, Failed},
		{"unanimous, all for", una, Tally{Seats: 3, Held: 3, Yes: 3}, false, Passed},
		{"unanimous, one silent at the close", una, Tally{Seats: 3, Held: 3, Yes: 2}, true, Failed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.rule.Validate(); err != nil {
				t.Fatal(err)
			}
			if got := Decide(tc.rule, tc.tally, tc.final); got != tc.want {
				t.Fatalf("Decide = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNeeds(t *testing.T) {
	if n := Needs(Rule{Kind: Majority}, 5); n != 3 {
		t.Fatalf("majority of 5 needs %d, want 3", n)
	}
	if n := Needs(Rule{Kind: Supermajority, Num: 2, Den: 3}, 9); n != 6 {
		t.Fatalf("two thirds of 9 needs %d, want 6", n)
	}
	if n := Needs(Rule{Kind: Unanimous}, 4); n != 4 {
		t.Fatalf("unanimous of 4 needs %d, want 4", n)
	}
}

func TestValidate(t *testing.T) {
	for _, r := range []Rule{{Kind: "plurality"}, {Kind: Supermajority, Num: 1, Den: 2}, {Kind: Majority, Num: 2, Den: 3},
		{Kind: Majority, QuorumNum: 3, QuorumDen: 2}} {
		if r.Validate() == nil {
			t.Errorf("%+v validated", r)
		}
	}
}

package settlement

import "testing"

func TestNextTier(t *testing.T) {
	for tier, want := range map[string]string{"village": "town", "": "town", "town": "city", "city": ""} {
		if got := NextTier(tier); got != want {
			t.Errorf("NextTier(%q) = %q, want %q", tier, got, want)
		}
	}
}

func TestEvaluate(t *testing.T) {
	rule := TierRule{From: "village", To: "town", Residents: 8, LiteracyBPS: 2000, Buildings: 5,
		Roles: []RoleNeed{{"food", 1}, {"education", 2}}, KnowledgeLearned: 2, Treasury: 1000}
	got := rule.Evaluate(Standing{Residents: 8, LiteracyBPS: 1500, Buildings: 6,
		RoleTiers: map[string]int{"food": 1, "education": 1}, KnowledgeLearned: 2, Treasury: 5000})
	if got.Met {
		t.Fatal("literacy and the school are short, yet the step is met")
	}
	wantMet := []bool{true, false, true, true, false, true, true}
	if len(got.Criteria) != len(wantMet) {
		t.Fatalf("%d criteria, want %d: %+v", len(got.Criteria), len(wantMet), got.Criteria)
	}
	for i, m := range wantMet {
		if got.Criteria[i].Met != m {
			t.Errorf("criterion %d (%s %s) met = %v, want %v", i, got.Criteria[i].Kind, got.Criteria[i].Role, got.Criteria[i].Met, m)
		}
	}
	all := rule.Evaluate(Standing{Residents: 9, LiteracyBPS: 2000, Buildings: 5,
		RoleTiers: map[string]int{"food": 3, "education": 2}, KnowledgeLearned: 9, Treasury: 1000})
	if !all.Met {
		t.Fatalf("everything is at its threshold yet the step is not met: %+v", all.Criteria)
	}
	if !(TierRule{}).Evaluate(Standing{}).Met {
		t.Fatal("a rule that asks for nothing must be met")
	}
}

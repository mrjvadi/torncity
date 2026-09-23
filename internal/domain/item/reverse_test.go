package item

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReverseChanceAndLoss(t *testing.T) {
	tests := []struct {
		level, difficulty int
		chance, loss      int64
	}{
		{0, 100, 0, MaxReverseLossBPS},
		{30, 80, 0, 5_750},     // fifty below: certain failure
		{50, 50, 5_000, 3_000}, // skill equals difficulty: a coin toss
		{60, 50, 6_000, 2_450}, // ten above
		{95, 50, MaxReverseChanceBPS, 525},
		{100, 50, MaxReverseChanceBPS, MinReverseLossBPS},
		{100, 0, MaxReverseChanceBPS, MinReverseLossBPS},
	}
	for _, tc := range tests {
		if got := ReverseChanceBPS(tc.level, tc.difficulty); got != tc.chance {
			t.Errorf("chance(%d vs %d) = %d, want %d", tc.level, tc.difficulty, got, tc.chance)
		}
		if got := ReverseLossBPS(tc.level, tc.difficulty); got != tc.loss {
			t.Errorf("loss(%d vs %d) = %d, want %d", tc.level, tc.difficulty, got, tc.loss)
		}
	}
}

// TestReverseDeterministicInRoll: the roll decides, and only the roll.
func TestReverseDeterministicInRoll(t *testing.T) {
	a, orig := phoneArchetype(), phoneDesign()
	eng := Engineer{Skill: "electronics", Level: 60} // chance 6000

	below, err := ReverseEngineer(a, orig, eng, 5_999)
	mustNil(t, err)
	if !below.Succeeded {
		t.Error("roll just under the chance failed")
	}
	at, err := ReverseEngineer(a, orig, eng, 6_000)
	mustNil(t, err)
	if at.Succeeded {
		t.Error("roll at the chance succeeded")
	}
	again, err := ReverseEngineer(a, orig, eng, 5_999)
	mustNil(t, err)
	if !reflect.DeepEqual(below, again) {
		t.Error("the same roll produced a different outcome")
	}
}

func TestReverseFailureDestroysSample(t *testing.T) {
	out, err := ReverseEngineer(medicineArchetype(), medicineDesign(300, 100),
		Engineer{Skill: "chemistry", Level: 30}, 0)
	mustNil(t, err)
	if out.Succeeded || !out.SampleConsumed || out.ChanceBPS != 0 {
		t.Errorf("fifty below difficulty: %+v", out)
	}
	if out.Design.Fills != nil || out.Design.Archetype != "" {
		t.Error("a failed attempt handed out a design")
	}
}

// TestReverseNeverGrantsTechnology is ADR 0005 §6's crucial rule. The copier
// gets a producible design and exactly the technology access it had before:
// it can build phones from bought cpu_a7, and still cannot make cpu_a7.
func TestReverseNeverGrantsTechnology(t *testing.T) {
	a, orig, comps := phoneArchetype(), phoneDesign(), testComponents()
	copier := TechAccess{Unlocked: NewSet("display")}

	before := CanManufacture(comps["cpu_a7"], copier)
	if !errors.Is(before, ErrTechnologyLocked) {
		t.Fatalf("precondition: copier can already make cpu_a7: %v", before)
	}

	out, err := ReverseEngineer(a, orig, Engineer{Skill: "electronics", Level: MaxSkillLevel}, 0)
	mustNil(t, err)
	if !out.Succeeded {
		t.Fatal("a master engineer with the lowest roll failed")
	}

	// The copy is a working, producible design.
	mustNil(t, ValidateStructure(a, out.Design, comps))
	if _, err := DeriveRecipe(out.Design); err != nil {
		t.Fatalf("copy has no recipe: %v", err)
	}
	// And the copier still cannot make the chip, or author around it.
	if err := CanManufacture(comps["cpu_a7"], copier); !errors.Is(err, ErrTechnologyLocked) {
		t.Errorf("after the copy, copier can make cpu_a7: %v", err)
	}
	if err := ValidateDesign(a, out.Design, comps, copier); !errors.Is(err, ErrTechnologyLocked) {
		t.Errorf("after the copy, copier passes the authoring gate: %v", err)
	}
}

// TestNoTechnologyChannel guards the rule structurally: neither a design nor
// a reverse-engineering outcome has anywhere to carry a technology. Adding
// such a field is the one change that would quietly break ADR 0005 §6, so it
// fails here first.
func TestNoTechnologyChannel(t *testing.T) {
	var walk func(reflect.Type, string)
	seen := map[reflect.Type]bool{}
	walk = func(rt reflect.Type, path string) {
		if seen[rt] {
			return
		}
		seen[rt] = true
		switch rt.Kind() {
		case reflect.Struct:
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				name := strings.ToLower(f.Name)
				if strings.Contains(name, "tech") || strings.Contains(name, "licen") {
					t.Errorf("%s.%s can carry a technology", path, f.Name)
				}
				walk(f.Type, path+"."+f.Name)
			}
		case reflect.Map, reflect.Slice, reflect.Pointer:
			walk(rt.Elem(), path+"[]")
		}
	}
	walk(reflect.TypeOf(ReverseOutcome{}), "ReverseOutcome")
	walk(reflect.TypeOf(Design{}), "Design")
}

// TestReverseDegradesBySkill: better engineers make better copies; none is
// ever exact.
func TestReverseDegradesBySkill(t *testing.T) {
	a, orig := rifleArchetype(), rifleDesign("scope_x4") // difficulty 40
	var prevLoss, prevOverhead int64 = MaxQualityLossBPS + 1, MaxOverheadBPS + 1
	for _, level := range []int{20, 40, 60, 80, 100} {
		out, err := ReverseEngineer(a, orig, Engineer{Skill: "engineering", Level: level}, 0)
		mustNil(t, err)
		if !out.Succeeded {
			t.Fatalf("level %d failed with roll 0", level)
		}
		d := out.Design
		if d.QualityLossBPS <= 0 || d.OverheadBPS <= 0 {
			t.Errorf("level %d: copy is exact (%d, %d)", level, d.QualityLossBPS, d.OverheadBPS)
		}
		if d.QualityLossBPS > prevLoss || d.OverheadBPS > prevOverhead {
			t.Errorf("level %d: copy worse than a less skilled one", level)
		}
		if d.Origin != OriginReverseEngineered || d.ID != "" {
			t.Errorf("level %d: origin %q id %q", level, d.Origin, d.ID)
		}
		if !reflect.DeepEqual(d.Fills, orig.Fills) {
			t.Errorf("level %d: bill of materials differs from the original", level)
		}
		prevLoss, prevOverhead = d.QualityLossBPS, d.OverheadBPS
	}

	// The median engineer: loss 3000 split into 1500 quality, 1500 cost.
	out, err := ReverseEngineer(a, orig, Engineer{Skill: "engineering", Level: 40}, 0)
	mustNil(t, err)
	if out.LossBPS != 3_000 || out.Design.QualityLossBPS != 1_500 || out.Design.OverheadBPS != 1_500 {
		t.Errorf("median copy: %+v", out)
	}
	// Overhead reaches the recipe: 1 barrel × 1.15 rounds up to 2.
	r, err := DeriveRecipe(out.Design)
	mustNil(t, err)
	if r[0].Quantity != 2 {
		t.Errorf("copied recipe %v does not carry the overhead", r)
	}
}

func TestReverseCopyOfCopyCompounds(t *testing.T) {
	a := rifleArchetype()
	eng := Engineer{Skill: "engineering", Level: 40}
	first, err := ReverseEngineer(a, rifleDesign(""), eng, 0)
	mustNil(t, err)
	second, err := ReverseEngineer(a, first.Design, eng, 0)
	mustNil(t, err)
	// 1 − 0.85 × 0.85 = 0.2775; 1.15 × 1.15 − 1 = 0.3225.
	if second.Design.QualityLossBPS != 2_775 || second.Design.OverheadBPS != 3_225 {
		t.Errorf("copy of copy: loss %d, overhead %d", second.Design.QualityLossBPS, second.Design.OverheadBPS)
	}
}

func TestReverseCopyDoesNotAlias(t *testing.T) {
	orig := rifleDesign("")
	out, err := ReverseEngineer(rifleArchetype(), orig, Engineer{Skill: "engineering", Level: 90}, 0)
	mustNil(t, err)
	out.Design.Fills["barrel"] = Fill{Component: "tampered", Quantity: 1}
	if orig.Fills["barrel"].Component != "barrel_long" {
		t.Error("changing the copy changed the original")
	}
}

// TestMedicineHarderThanWeapons: ADR 0005 §6 — otherwise the pharmaceutical
// branch dies to copying. The difficulty is content; the rule is that it
// matters.
func TestMedicineHarderThanWeapons(t *testing.T) {
	med := ReverseChanceBPS(60, medicineArchetype().ReverseDifficulty)
	gun := ReverseChanceBPS(60, rifleArchetype().ReverseDifficulty)
	if med >= gun {
		t.Errorf("same engineer: medicine %d bps, rifle %d bps", med, gun)
	}
}

func TestReverseRejects(t *testing.T) {
	surgery := Archetype{Code: "surgery", Method: MethodServe}
	eng := Engineer{Skill: "engineering", Level: 50}
	tests := []struct {
		name string
		a    Archetype
		d    Design
		eng  Engineer
		roll int
		want error
	}{
		{"service", surgery, Design{Archetype: "surgery"}, eng, 0, ErrNotReversible},
		{"design of another archetype", rifleArchetype(), phoneDesign(), eng, 0, ErrArchetypeMismatch},
		{"wrong skill", rifleArchetype(), rifleDesign(""), Engineer{Skill: "chemistry", Level: 100}, 0, ErrWrongSkill},
		{"negative level", rifleArchetype(), rifleDesign(""), Engineer{Skill: "engineering", Level: -1}, 0, ErrInvalidSkillLevel},
		{"level above max", rifleArchetype(), rifleDesign(""), Engineer{Skill: "engineering", Level: 101}, 0, ErrInvalidSkillLevel},
		{"negative roll", rifleArchetype(), rifleDesign(""), eng, -1, ErrInvalidRoll},
		{"roll at scale", rifleArchetype(), rifleDesign(""), eng, RollScale, ErrInvalidRoll},
		{"bad difficulty", func() Archetype { a := rifleArchetype(); a.ReverseDifficulty = 101; return a }(),
			rifleDesign(""), eng, 0, ErrInvalidReverseDifficulty},
		{"bad original degradation", rifleArchetype(),
			func() Design { d := rifleDesign(""); d.OverheadBPS = MaxOverheadBPS + 1; return d }(), eng, 0, ErrInvalidDegradation},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ReverseEngineer(tc.a, tc.d, tc.eng, tc.roll); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestReverseDegradationBounded: copying the worst possible design stays in
// bounds, so a chain of copies can never overflow or reach zero quality.
func TestReverseDegradationBounded(t *testing.T) {
	d := rifleDesign("")
	d.QualityLossBPS, d.OverheadBPS = MaxQualityLossBPS, MaxOverheadBPS
	out, err := ReverseEngineer(rifleArchetype(), d, Engineer{Skill: "engineering", Level: 90}, 0)
	mustNil(t, err)
	if out.Design.QualityLossBPS != MaxQualityLossBPS || out.Design.OverheadBPS != MaxOverheadBPS {
		t.Errorf("got loss %d, overhead %d", out.Design.QualityLossBPS, out.Design.OverheadBPS)
	}
	mustNil(t, ValidateStructure(rifleArchetype(), out.Design, testComponents()))
}

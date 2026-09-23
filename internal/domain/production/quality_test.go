package production

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

func TestRollQuality(t *testing.T) {
	mid := item.RollScale / 2
	tests := []struct {
		name string
		q    QualityInputs
		roll int
		want int
	}{
		// 50×80 + 30×60 + 20×100 = 7800 hundredths.
		{"machines, neutral roll", QualityInputs{InputQuality: 80, WorkerSkill: 60, Machines: true, MachineCondition: 100}, mid, 78},
		// A worn machine drags quality: 50×80 + 30×60 + 20×20 = 6200.
		{"worn machines", QualityInputs{InputQuality: 80, WorkerSkill: 60, Machines: true, MachineCondition: 20}, mid, 62},
		// Handmade ignores machine condition: 60×80 + 40×60 = 7200.
		{"handmade", QualityInputs{InputQuality: 80, WorkerSkill: 60, MachineCondition: 0}, mid, 72},
		{"lowest roll", QualityInputs{InputQuality: 80, WorkerSkill: 60}, 0, 62},
		{"highest roll", QualityInputs{InputQuality: 80, WorkerSkill: 60}, item.RollScale - 1, 81},
		// A copied design with 15% loss: 7200 × 0.85 = 6120.
		{"copied design", QualityInputs{InputQuality: 80, WorkerSkill: 60, DesignQualityLossBPS: 1_500}, mid, 61},
		{"clamped at the top", QualityInputs{InputQuality: 100, WorkerSkill: 100}, item.RollScale - 1, 100},
		{"clamped at the bottom", QualityInputs{}, 0, 0},
		// Bad flour makes bad bread, whoever bakes it: 60×10 + 40×100 = 4600.
		{"bad inputs, master baker", QualityInputs{InputQuality: 10, WorkerSkill: 100}, mid, 46},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RollQuality(tc.q, tc.roll)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRollQualityMonotonic(t *testing.T) {
	q := QualityInputs{InputQuality: 50, WorkerSkill: 50, Machines: true, MachineCondition: 50}
	prev := -1
	for roll := 0; roll < item.RollScale; roll += 97 {
		got, err := RollQuality(q, roll)
		if err != nil {
			t.Fatal(err)
		}
		if got < prev {
			t.Fatalf("roll %d gave %d after %d", roll, got, prev)
		}
		prev = got
	}
	for skill := 0; skill < item.MaxSkillLevel; skill++ {
		lo, _ := RollQuality(QualityInputs{InputQuality: 50, WorkerSkill: skill}, 5000)
		hi, _ := RollQuality(QualityInputs{InputQuality: 50, WorkerSkill: skill + 1}, 5000)
		if hi < lo {
			t.Fatalf("skill %d scored below skill %d", skill+1, skill)
		}
	}
}

func TestRollQualityRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		q    QualityInputs
		roll int
	}{
		"input above max":   {QualityInputs{InputQuality: 101}, 0},
		"negative skill":    {QualityInputs{WorkerSkill: -1}, 0},
		"condition above":   {QualityInputs{MachineCondition: 101}, 0},
		"design loss":       {QualityInputs{DesignQualityLossBPS: item.MaxQualityLossBPS + 1}, 0},
		"negative roll":     {QualityInputs{}, -1},
		"roll at the scale": {QualityInputs{}, item.RollScale},
	} {
		if _, err := RollQuality(tc.q, tc.roll); !errors.Is(err, ErrInvalidQualityInput) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestInputQuality(t *testing.T) {
	consumed := item.Recipe{{Component: "flour_whole", Quantity: 500}, {Component: "water_tap", Quantity: 300}}
	got, err := InputQuality(consumed, map[string]int{"flour_whole": 70, "water_tap": 100})
	if err != nil {
		t.Fatal(err)
	}
	if got != 81 { // (35000 + 30000) / 800 = 81.25
		t.Errorf("got %d, want 81", got)
	}
	if got, _ := InputQuality(nil, nil); got != item.MaxQuality {
		t.Errorf("labour-only order: got %d", got)
	}
	if _, err := InputQuality(consumed, map[string]int{"flour_whole": 70}); !errors.Is(err, ErrInvalidQualityInput) {
		t.Errorf("unknown quality: got %v", err)
	}
	if _, err := InputQuality(consumed, map[string]int{"flour_whole": 70, "water_tap": 101}); !errors.Is(err, ErrInvalidQualityInput) {
		t.Errorf("quality above max: got %v", err)
	}
}

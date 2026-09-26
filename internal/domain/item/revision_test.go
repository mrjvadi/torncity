package item

import (
	"errors"
	"fmt"
	"testing"
)

func TestRevise(t *testing.T) {
	parent := phoneDesign()
	parent.Version = 1

	t.Run("changed fills starts a fresh baseline", func(t *testing.T) {
		changed := map[string]Fill{
			"cpu":      {Component: "cpu_a8", Quantity: 1},
			"battery":  parent.Fills["battery"],
			"screen":   parent.Fills["screen"],
			"body":     parent.Fills["body"],
			"speakers": parent.Fills["speakers"],
		}
		withGains := parent
		withGains.Improvements = map[string]int64{"performance": 500}

		next, err := Revise(withGains, changed)
		mustNil(t, err)
		if next.Version != 2 {
			t.Fatalf("version = %d, want 2", next.Version)
		}
		if next.LineageID != parent.ID {
			t.Fatalf("lineage = %q, want %q", next.LineageID, parent.ID)
		}
		if next.ParentID != withGains.ID {
			t.Fatalf("parent = %q, want %q", next.ParentID, withGains.ID)
		}
		if next.Improvements != nil {
			t.Fatalf("improvements carried into a redesign: %v", next.Improvements)
		}
		if next.Origin != OriginAuthored {
			t.Fatalf("origin = %q, want authored", next.Origin)
		}
	})

	t.Run("same fills carries improvements forward", func(t *testing.T) {
		withGains := parent
		withGains.Improvements = map[string]int64{"performance": 500}
		next, err := Revise(withGains, nil)
		mustNil(t, err)
		if next.Improvements["performance"] != 500 {
			t.Fatalf("improvements = %v, want performance 500", next.Improvements)
		}
		// The clone must be independent of the parent's map.
		next.Improvements["performance"] = 999
		if withGains.Improvements["performance"] != 500 {
			t.Fatalf("Revise did not clone Improvements: parent mutated")
		}
	})

	t.Run("chains lineage across versions", func(t *testing.T) {
		v2, err := Revise(parent, nil)
		mustNil(t, err)
		v2.ID = "phone-v2"
		v3, err := Revise(v2, nil)
		mustNil(t, err)
		if v3.Version != 3 || v3.LineageID != parent.ID || v3.ParentID != "phone-v2" {
			t.Fatalf("v3 = %+v", v3)
		}
	})

	t.Run("refuses a parent with no id", func(t *testing.T) {
		_, err := Revise(Design{Archetype: "phone"}, nil)
		if !errors.Is(err, ErrEmptyParent) {
			t.Fatalf("err = %v, want ErrEmptyParent", err)
		}
	})
}

func TestApplyImprovement(t *testing.T) {
	parent := phoneDesign()
	parent.Version = 1

	t.Run("refuses an empty attribute", func(t *testing.T) {
		_, _, err := ApplyImprovement(parent, "")
		if !errors.Is(err, ErrEmptyAttribute) {
			t.Fatalf("err = %v, want ErrEmptyAttribute", err)
		}
	})

	t.Run("compounds toward the cap and eventually refuses", func(t *testing.T) {
		d := parent
		var total int64
		for i := 0; i < 200; i++ {
			next, delta, err := ApplyImprovement(d, "detection_range")
			next.ID = fmt.Sprintf("phone-improved-%d", i)
			if err != nil {
				if errors.Is(err, ErrImprovementCapped) {
					if total != MaxImprovementBPS {
						t.Fatalf("stopped at %d bps, want exactly the cap %d", total, MaxImprovementBPS)
					}
					return
				}
				t.Fatalf("unexpected error: %v", err)
			}
			if delta <= 0 {
				t.Fatalf("project %d: non-positive delta %d", i, delta)
			}
			total += delta
			if total > MaxImprovementBPS {
				t.Fatalf("project %d: total %d exceeds cap %d", i, total, MaxImprovementBPS)
			}
			if next.Improvements["detection_range"] != total {
				t.Fatalf("project %d: stored %d, want %d", i, next.Improvements["detection_range"], total)
			}
			d = next
		}
		t.Fatalf("did not reach the cap within 200 projects")
	})

	t.Run("deltas never increase (diminishing returns)", func(t *testing.T) {
		d := parent
		prevDelta := int64(1 << 62)
		for i := 0; i < 50; i++ {
			next, delta, err := ApplyImprovement(d, "range")
			if err != nil {
				break
			}
			if delta > prevDelta {
				t.Fatalf("project %d: delta %d grew from previous %d", i, delta, prevDelta)
			}
			prevDelta = delta
			next.ID = fmt.Sprintf("phone-range-%d", i)
			d = next
		}
	})

	t.Run("each project is its own version", func(t *testing.T) {
		next, _, err := ApplyImprovement(parent, "range")
		mustNil(t, err)
		if next.Version != parent.Version+1 {
			t.Fatalf("version = %d, want %d", next.Version, parent.Version+1)
		}
		if next.ParentID != parent.ID {
			t.Fatalf("parent = %q, want %q", next.ParentID, parent.ID)
		}
	})
}

func TestNextImprovementBPS(t *testing.T) {
	if _, err := NextImprovementBPS(-1); !errors.Is(err, ErrImprovementCapped) {
		t.Fatalf("negative gained: err = %v", err)
	}
	if _, err := NextImprovementBPS(MaxImprovementBPS + 1); !errors.Is(err, ErrImprovementCapped) {
		t.Fatalf("over cap: err = %v", err)
	}
	if _, err := NextImprovementBPS(MaxImprovementBPS); !errors.Is(err, ErrImprovementCapped) {
		t.Fatalf("at cap: err = %v, want ErrImprovementCapped", err)
	}
	first, err := NextImprovementBPS(0)
	mustNil(t, err)
	if first != ImprovementStepBPS {
		t.Fatalf("first step = %d, want the nominal step %d", first, ImprovementStepBPS)
	}
}

func TestImprovementEffects(t *testing.T) {
	d := phoneDesign()
	if effs := ImprovementEffects(d); effs != nil {
		t.Fatalf("no improvements: effects = %v, want nil", effs)
	}
	d.Improvements = map[string]int64{"detection_range": 500, "rcs": -300}
	effs := ImprovementEffects(d)
	got := map[string]int64{}
	for _, e := range effs {
		if e.Op != EffectMultiply {
			t.Fatalf("op = %q, want multiply", e.Op)
		}
		got[e.Target] = e.Value
	}
	if got["detection_range"] != BPS+500 || got["rcs"] != BPS-300 {
		t.Fatalf("effects = %v", got)
	}
}

func TestProducible(t *testing.T) {
	d := phoneDesign()
	if err := d.Producible(); err != nil {
		t.Fatalf("not retired: err = %v", err)
	}
	d.Retired = true
	if err := d.Producible(); !errors.Is(err, ErrDesignRetired) {
		t.Fatalf("retired: err = %v, want ErrDesignRetired", err)
	}
}

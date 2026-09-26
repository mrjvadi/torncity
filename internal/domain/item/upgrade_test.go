package item

import (
	"errors"
	"testing"
)

func TestRetrofit(t *testing.T) {
	current := phoneDesign()
	current.ID = "phone-v1"
	current.Version = 1
	target := current
	target.ID = "phone-v2"
	target.Version = 2
	target.LineageID = "phone-v1"

	inst := Instance{Serial: "s1", Archetype: "phone", DesignID: current.ID,
		Provenance: Provenance{ProductionOrderID: "o1"}}

	t.Run("moves the instance to the target design", func(t *testing.T) {
		out, err := Retrofit(inst, current, target)
		mustNil(t, err)
		if out.DesignID != target.ID {
			t.Fatalf("design = %q, want %q", out.DesignID, target.ID)
		}
		if out.Serial != inst.Serial || out.Provenance != inst.Provenance {
			t.Fatalf("retrofit changed identity/provenance: %+v", out)
		}
	})

	t.Run("refuses an instance not of the current design", func(t *testing.T) {
		wrong := inst
		wrong.DesignID = "somethingElse"
		_, err := Retrofit(wrong, current, target)
		if !errors.Is(err, ErrWrongInstance) {
			t.Fatalf("err = %v, want ErrWrongInstance", err)
		}
	})

	t.Run("refuses a kit for a different lineage", func(t *testing.T) {
		other := target
		other.LineageID = "rifle-v1"
		_, err := Retrofit(inst, current, other)
		if !errors.Is(err, ErrNotSameLineage) {
			t.Fatalf("err = %v, want ErrNotSameLineage", err)
		}
	})

	t.Run("refuses a target that is not later", func(t *testing.T) {
		same := current
		_, err := Retrofit(inst, current, same)
		if !errors.Is(err, ErrNotAnUpgrade) {
			t.Fatalf("err = %v, want ErrNotAnUpgrade", err)
		}
		older := current
		older.ID = "phone-v0"
		older.LineageID = current.ID
		older.Version = 0
		olderInst := inst
		olderInst.DesignID = current.ID
		_, err = Retrofit(olderInst, current, older)
		if !errors.Is(err, ErrNotAnUpgrade) {
			t.Fatalf("older target: err = %v, want ErrNotAnUpgrade", err)
		}
	})

	t.Run("chains through Revise's lineage", func(t *testing.T) {
		v1 := phoneDesign()
		v1.ID = "chain-v1"
		v2, err := Revise(v1, nil)
		mustNil(t, err)
		v2.ID = "chain-v2"
		v3, err := Revise(v2, nil)
		mustNil(t, err)
		v3.ID = "chain-v3"

		i := Instance{Serial: "s2", Archetype: "phone", DesignID: v1.ID, Provenance: Provenance{ProductionOrderID: "o2"}}
		up, err := Retrofit(i, v1, v3)
		mustNil(t, err)
		if up.DesignID != v3.ID {
			t.Fatalf("design = %q, want %q", up.DesignID, v3.ID)
		}
	})
}

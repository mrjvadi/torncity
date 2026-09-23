package item

import (
	"errors"
	"testing"
)

func TestValidateStructureFailures(t *testing.T) {
	tests := []struct {
		name      string
		archetype Archetype
		mutate    func(*Design)
		want      error
	}{
		{"archetype mismatch", rifleArchetype(), func(d *Design) { d.Archetype = "phone" }, ErrArchetypeMismatch},
		{"fill for unknown slot", rifleArchetype(), func(d *Design) {
			d.Fills["bayonet"] = Fill{Component: "barrel_long", Quantity: 1}
		}, ErrUnknownSlot},
		{"required slot empty", rifleArchetype(), func(d *Design) { delete(d.Fills, "barrel") }, ErrRequiredSlotEmpty},
		{"unknown component", rifleArchetype(), func(d *Design) {
			d.Fills["barrel"] = Fill{Component: "barrel_gold", Quantity: 1}
		}, ErrUnknownComponent},
		{"category mismatch", rifleArchetype(), func(d *Design) {
			d.Fills["barrel"] = Fill{Component: "scope_x4", Quantity: 1}
		}, ErrCategoryMismatch},
		{"assembly quantity not the fixed one", rifleArchetype(), func(d *Design) {
			d.Fills["ammo"] = Fill{Component: "round_762", Quantity: 2}
		}, ErrQuantityOutOfRange},
		{"zero quantity", rifleArchetype(), func(d *Design) {
			d.Fills["ammo"] = Fill{Component: "round_762", Quantity: 0}
		}, ErrQuantityOutOfRange},
		{"formulation above range", medicineArchetype(), func(d *Design) {
			d.Fills["active"] = Fill{Component: "morphine_base", Quantity: 501}
		}, ErrQuantityOutOfRange},
		{"formulation below range", medicineArchetype(), func(d *Design) {
			d.Fills["carrier"] = Fill{Component: "starch", Quantity: 9}
		}, ErrQuantityOutOfRange},
		{"negative quality loss", rifleArchetype(), func(d *Design) { d.QualityLossBPS = -1 }, ErrInvalidDegradation},
		{"quality loss above max", rifleArchetype(), func(d *Design) { d.QualityLossBPS = MaxQualityLossBPS + 1 }, ErrInvalidDegradation},
		{"overhead above max", rifleArchetype(), func(d *Design) { d.OverheadBPS = MaxOverheadBPS + 1 }, ErrInvalidDegradation},
		{"unknown origin", rifleArchetype(), func(d *Design) { d.Origin = "" }, ErrUnknownOrigin},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Design
			if tc.archetype.Code == "painkiller" {
				d = medicineDesign(300, 100)
			} else {
				d = rifleDesign("")
			}
			tc.mutate(&d)
			if err := ValidateStructure(tc.archetype, d, testComponents()); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateStructureAccepts(t *testing.T) {
	// A rifle without its optional sight is still a rifle.
	mustNil(t, ValidateStructure(rifleArchetype(), rifleDesign(""), testComponents()))
	// Both ends of a formulation range are inside it.
	mustNil(t, ValidateStructure(medicineArchetype(), medicineDesign(50, 200), testComponents()))
	mustNil(t, ValidateStructure(medicineArchetype(), medicineDesign(500, 10), testComponents()))
	// Bread without its optional salt.
	d := breadDesign()
	delete(d.Fills, "salt")
	mustNil(t, ValidateStructure(breadArchetype(), d, testComponents()))
}

func TestValidateDesignTechnology(t *testing.T) {
	a, d, comps := phoneArchetype(), phoneDesign(), testComponents()

	noSemis := TechAccess{Unlocked: NewSet("battery_chemistry", "display", "electronics")}
	err := ValidateDesign(a, d, comps, noSemis)
	if !errors.Is(err, ErrTechnologyLocked) {
		t.Fatalf("designing around cpu_a7 without semiconductor: got %v", err)
	}

	licensed := noSemis
	licensed.Licensed = NewSet("semiconductor")
	mustNil(t, ValidateDesign(a, d, comps, licensed))

	mustNil(t, ValidateDesign(a, d, comps, fullAccess()))

	// A component with no technology needs none.
	mustNil(t, ValidateDesign(rifleArchetype(), rifleDesign("scope_x8"), comps, TechAccess{}))

	// Structure errors still come through the technology gate.
	bad := phoneDesign()
	delete(bad.Fills, "cpu")
	if err := ValidateDesign(a, bad, comps, fullAccess()); !errors.Is(err, ErrRequiredSlotEmpty) {
		t.Errorf("got %v, want ErrRequiredSlotEmpty", err)
	}
}

func TestCanManufacture(t *testing.T) {
	cpu := testComponents()["cpu_a7"]
	if err := CanManufacture(cpu, TechAccess{}); !errors.Is(err, ErrTechnologyLocked) {
		t.Errorf("no access: got %v", err)
	}
	mustNil(t, CanManufacture(cpu, TechAccess{Unlocked: NewSet("semiconductor")}))
	mustNil(t, CanManufacture(cpu, TechAccess{Licensed: NewSet("semiconductor")}))
}

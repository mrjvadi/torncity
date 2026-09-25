package content

import (
	"errors"
	"testing"
)

// TestShippedWar checks the war the game ships: the three operations, a
// doctrine and city rules the domain accepts, every fighting class with a
// role, and an occupation that empties city offices and hands the city to a
// governor held by conquest.
func TestShippedWar(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	def, ok := snap.War()
	if !ok {
		t.Fatal("no war section is shipped")
	}
	if _, ok := snap.Action(ActionWar); !ok {
		t.Error("the war action is not shipped")
	}
	for _, op := range []string{OperationAir, OperationMissile, OperationGround} {
		if o, ok := def.Operation(op); !ok || o.PrepareTime() <= 0 {
			t.Errorf("operation %s is not shipped with a preparation", op)
		}
	}
	if err := def.DoctrineRules().Validate(); err != nil {
		t.Error(err)
	}
	if err := def.CityRules().Validate(); err != nil {
		t.Error(err)
	}
	roles := map[string]bool{}
	for _, c := range snap.ForceClasses() {
		if cb, ok := snap.CombatOf(c.Code); ok {
			roles[cb.Role] = true
		}
	}
	for _, r := range []string{RoleAircraft, RoleMunition, RoleMissile, RoleGround, RoleArtillery, RoleRadar, RoleSAM} {
		if !roles[r] {
			t.Errorf("no shipped class plays %s", r)
		}
	}
	if def.Occupation.Governor == "" || len(def.Occupation.Vacate) == 0 {
		t.Error("an occupation neither empties offices nor names a governor")
	}
}

func TestWarContentRefusals(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(p *Pack)
	}{
		{"an unknown operation", func(p *Pack) { p.War[0].Operations[0].Code = "orbital" }},
		{"an operation with no preparation", func(p *Pack) { p.War[0].Operations[0].Prepare = "0s" }},
		{"no ground to declare on", func(p *Pack) { p.War[0].Grounds = nil }},
		{"a doctrine with no reaction", func(p *Pack) { p.War[0].Doctrine.ReactionSeconds = 0 }},
		{"a chance floor above the ceiling", func(p *Pack) { p.War[0].Doctrine.PkFloorBPS = 9999 }},
		{"a city of no structure", func(p *Pack) { p.War[0].City.Structure = 0 }},
		{"damage bands not rising", func(p *Pack) { p.War[0].DamageBands[1].UpTo = 1 }},
		{"a governor who is elected", func(p *Pack) { p.War[0].Occupation.Governor = "mayor" }},
		{"vacating a country office", func(p *Pack) { p.War[0].Occupation.Vacate = []string{"president"} }},
		{"a role nobody plays", func(p *Pack) { p.ForceClasses[0].Combat = &CombatDef{Role: "cavalry"} }},
		{"a battery with no rounds", func(p *Pack) {
			for i, c := range p.ForceClasses {
				if c.Combat != nil && c.Combat.Role == RoleSAM {
					cb := *c.Combat
					cb.Rounds = 0
					p.ForceClasses[i].Combat = &cb
				}
			}
		}},
		{"two war sections", func(p *Pack) { p.War = append(p.War, p.War[0]) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := shippedPack(t)
			tc.break_(p)
			if err := p.Validate(); !errors.Is(err, ErrInvalidWarContent) {
				t.Errorf("Validate() = %v, want ErrInvalidWarContent", err)
			}
		})
	}
}

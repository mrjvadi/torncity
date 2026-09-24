package content

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/domain/technology"
)

// TestShippedMilitary checks the armed forces the game ships: every military
// good is a class of one branch, restricted to states, and a stealth fighter
// built from its shipped parts has the cross-section of a bird while a
// conventional fighter has one a radar sees four times further.
func TestShippedMilitary(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{ActionProcure, ActionSanction, ActionTreaty} {
		if _, ok := snap.Action(code); !ok {
			t.Errorf("action %s is not shipped", code)
		}
	}
	if len(snap.Branches()) != 4 || len(snap.ForceClasses()) == 0 || len(snap.TreatyTypes()) != 3 {
		t.Fatalf("branches %d, classes %d, treaty types %d", len(snap.Branches()), len(snap.ForceClasses()), len(snap.TreatyTypes()))
	}
	for _, c := range snap.ForceClasses() {
		for _, it := range c.Items {
			def, ok := snap.ItemDef(it)
			if !ok {
				t.Fatalf("class %s names %s", c.Code, it)
			}
			if err := technology.Cleared(def.ExportControl.Control(), technology.Buyer{Kind: "player"}); err == nil {
				t.Errorf("a player may buy %s", it)
			}
			if err := technology.Cleared(def.ExportControl.Control(), technology.Buyer{Kind: "company", Sector: "defence"}); err == nil {
				t.Errorf("a company may buy %s", it)
			}
			if err := technology.Cleared(def.ExportControl.Control(), technology.Buyer{Kind: StateBuyerClass}); err != nil {
				t.Errorf("a state may not buy %s: %v", it, err)
			}
		}
	}
	for _, code := range []string{"stealth_shaping", "radar_absorbent_materials", "aesa_radar"} {
		def, ok := snap.Technology(code)
		if !ok {
			t.Fatalf("technology %s is not shipped", code)
		}
		if technology.Cleared(def.Tech().Control, technology.Buyer{Kind: "company", Sector: "civilian"}) == nil {
			t.Errorf("a civilian company may license %s", code)
		}
	}

	design := func(arch string, fills map[string]string) map[string]int64 {
		a, ok := snap.Archetype(arch)
		if !ok {
			t.Fatalf("archetype %s", arch)
		}
		d := item.Design{Archetype: arch, Origin: item.OriginAuthored, Fills: map[string]item.Fill{}}
		for slot, comp := range fills {
			d.Fills[slot] = item.Fill{Component: comp, Quantity: 1}
		}
		attrs, err := item.ComputeAttributes(a, d, snap.Components())
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		return attrs
	}
	stealth := design("stealth_fighter", map[string]string{"airframe": "stealth_airframe", "coating": "ram_coating",
		"engine": "turbofan", "radar": "fire_control_radar"})
	plain := design("fighter", map[string]string{"airframe": "fighter_airframe", "engine": "turbofan", "radar": "fire_control_radar"})
	if stealth["rcs"] != 5 || plain["rcs"] != 5000 {
		t.Fatalf("rcs: stealth %d, conventional %d; want 5 and 5000", stealth["rcs"], plain["rcs"])
	}
	radar := plain["detection_range"]
	seeStealth := military.DetectionRangeKM(radar, stealth["rcs"], 10_000)
	seePlain := military.DetectionRangeKM(radar, plain["rcs"], 10_000)
	if seePlain < 4*seeStealth {
		t.Errorf("a fighter radar sees a stealth fighter at %d km and a conventional one at %d km", seeStealth, seePlain)
	}
}

func TestMilitaryContentRefusals(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(p *Pack)
	}{
		{"class of an unknown item", func(p *Pack) { p.ForceClasses[0].Items = []string{"laser"} }},
		{"item in two classes", func(p *Pack) { p.ForceClasses[1].Items = p.ForceClasses[0].Items }},
		{"civilian good as arms", func(p *Pack) { p.ForceClasses[0].Items = []string{"phone"} }},
		{"unknown branch", func(p *Pack) { p.ForceClasses[0].Branch = "space" }},
		{"branch without a command", func(p *Pack) { p.Branches[0].Command = "country.nothing" }},
		{"branch that never moves", func(p *Pack) { p.Branches[0].Redeploy = "0s" }},
		{"action nobody implements", func(p *Pack) {
			p.Actions = append(p.Actions, ActionDef{Code: "country.coup", Jurisdiction: CountryLevel, HeldBy: "president"})
		}},
		{"action of an unknown office", func(p *Pack) { p.Actions[0].HeldBy = "king" }},
		{"action at another level", func(p *Pack) { p.Actions[0].HeldBy = "mayor" }},
		{"clearance of a city office", func(p *Pack) { p.MilitaryClearance = append(p.MilitaryClearance, "mayor") }},
		{"bands not rising", func(p *Pack) { p.StrengthBands[1].UpTo = 1 }},
		{"a discount above all", func(p *Pack) { p.TreatyTypes[2].TariffDiscountBPS = 10_001 }},
		{"a ground twice", func(p *Pack) { p.SanctionGrounds = append(p.SanctionGrounds, p.SanctionGrounds[0]) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := shippedPack(t)
			tc.break_(p)
			if err := p.Validate(); !errors.Is(err, ErrInvalidMilitaryContent) {
				t.Errorf("Validate() = %v, want ErrInvalidMilitaryContent", err)
			}
		})
	}
}

package content

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/production"
	"github.com/mrjvadi/torncity/internal/domain/technology"
)

// Every component a company makes plans as an order of the production rules
// from the stock its inputs name, and is refused, all of it, without them.
func TestShippedComponentRecipesPlan(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	components := snap.Components()
	made := 0
	for _, c := range snap.ComponentDefs() {
		a, d, def, ok := snap.ComponentRecipe(c.Code)
		if !ok {
			continue
		}
		made++
		profile, ok := snap.Profile(a.Method)
		if !ok {
			t.Fatalf("%s: no timing for %s", c.Code, a.Method)
		}
		stock := production.Stock{}
		for _, in := range def.Inputs {
			stock[in.Component] = in.Quantity * 3
		}
		plan, err := production.PlanOrder(production.Request{Archetype: a, Design: d, Components: components,
			Quantity: 3, Workers: 1}, profile, stock)
		if err != nil {
			t.Fatalf("%s: %v", c.Code, err)
		}
		if plan.Output != 3 || plan.Duration <= 0 {
			t.Errorf("%s: plan %+v", c.Code, plan)
		}
		if _, err := production.PlanOrder(production.Request{Archetype: a, Design: d, Components: components,
			Quantity: 4, Workers: 1}, profile, stock); !errors.Is(err, production.ErrInsufficientInputs) {
			t.Errorf("%s: short of inputs = %v", c.Code, err)
		}
	}
	if made == 0 {
		t.Fatal("no shipped component is made by a company")
	}
	if len(snap.MadeBy("mine")) == 0 || len(snap.MadeBy("farm")) == 0 || len(snap.MadeBy("factory")) == 0 {
		t.Error("the shipped mine, farm and factory make nothing")
	}
}

// The phone the owner describes can be designed from shipped content: a
// factory designs a phone, its board and its battery need technologies, and
// every part is made by someone or sold by a supplier.
func TestShippedPhoneChain(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	var phone ItemDef
	for _, d := range snap.DesignableItems("factory") {
		if d.Code == "phone" {
			phone = d
		}
	}
	if phone.Code == "" {
		t.Fatal("a factory cannot design a phone")
	}
	a, _ := snap.Archetype(phone.Archetype)
	fills := map[string]item.Fill{}
	for _, s := range a.Slots {
		cands := snap.SlotCandidates(a, s.Name)
		if len(cands) == 0 {
			t.Fatalf("nothing fits the %s slot", s.Name)
		}
		fills[s.Name] = item.Fill{Component: cands[0].Code, Quantity: s.Quantity.Min}
	}
	design := item.Design{Archetype: a.Code, Fills: fills, Origin: item.OriginAuthored}
	none := item.TechAccess{}
	if err := item.ValidateDesign(a, design, snap.Components(), none); !errors.Is(err, item.ErrTechnologyLocked) {
		t.Errorf("a phone designed without technology = %v", err)
	}
	all := item.TechAccess{Unlocked: item.NewSet(snap.TechCodes()...)}
	if err := item.ValidateDesign(a, design, snap.Components(), all); err != nil {
		t.Errorf("a phone designed with every technology = %v", err)
	}
	supplied := map[string]bool{}
	for _, s := range snap.Suppliers() {
		for _, sh := range s.Shelves {
			supplied[sh.Component] = true
		}
	}
	var trace func(code string, depth int)
	trace = func(code string, depth int) {
		if depth > 10 {
			t.Fatalf("%s: the chain does not end", code)
		}
		_, _, def, made := snap.ComponentRecipe(code)
		if !made && !supplied[code] {
			t.Errorf("%s is neither made nor supplied: no chain can start", code)
			return
		}
		if made {
			for _, in := range def.Inputs {
				trace(in.Component, depth+1)
			}
		}
	}
	for _, f := range fills {
		trace(f.Component, 0)
	}
}

func TestProductionValidation(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(p *Pack)
		want   string
	}{
		{"unknown technology", func(p *Pack) { p.Components[0].RequiresTechnology = []string{"telepathy"} }, "unknown technology"},
		{"no price", func(p *Pack) { p.Components[0].BasePrice = 0 }, "base price"},
		{"same code as an item", func(p *Pack) { p.Components[0].Code = "bread" }, "shares its code"},
		{"untimed method", func(p *Pack) { p.MethodProfiles = p.MethodProfiles[:1] }, "does not time"},
		{"cycle", func(p *Pack) { p.Technologies[0].Requires = []string{p.Technologies[1].Code} }, "cycle"},
		{"supplier sells nothing known", func(p *Pack) { p.Suppliers[0].Shelves[0].Component = "unobtainium" }, "unknown component"},
		{"produces nothing known", func(p *Pack) { p.CompanyTypes[0].Produces = []string{"spaceship"} }, "unknown archetype"},
		{"consumes itself", func(p *Pack) {
			for i := range p.Components {
				if pr := p.Components[i].Production; pr != nil {
					pr.Inputs = append(pr.Inputs, RecipeInputDef{Component: p.Components[i].Code, Quantity: 1})
					return
				}
			}
		}, "consumes itself"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := shippedPack(t)
			tc.break_(p)
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

// The shipped tree stages a new tech studio (docs/adr/0021, section 14): its
// basic gadgets are open at once, a phone is two researches away and hidden,
// and after semiconductors the phone is one step away — microchips and
// battery chemistry, both of which the studio may research.
func TestShippedStaging(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	comps, tree := snap.Components(), snap.TechTree()
	fresh := technology.Standing{CompanyType: "tech_studio", Owned: item.NewSet(), Published: item.NewSet(),
		Buyer: technology.Buyer{Kind: "company", Sector: "civilian"}}
	reach := func(code string, s technology.Standing) (technology.Reach, []technology.Step) {
		t.Helper()
		def, ok := snap.ItemDef(code)
		if !ok {
			t.Fatalf("no item %s", code)
		}
		a, _ := snap.Archetype(def.Archetype)
		access := technology.Access(item.SortedCodes(s.Owned), nil, nil)
		missing, possible := item.DesignGaps(a, def.RequiresTechnology, comps, access)
		if !possible {
			t.Fatalf("%s cannot be designed at all", code)
		}
		return technology.Assess(missing, tree, s, nil)
	}
	designable := map[string]bool{}
	for _, d := range snap.DesignableItems("tech_studio") {
		designable[d.Code] = true
	}
	for _, code := range []string{"led_torch", "pocket_radio", "phone"} {
		if !designable[code] {
			t.Fatalf("a tech studio does not design %s", code)
		}
	}
	if r, _ := reach("led_torch", fresh); r != technology.Ready {
		t.Errorf("a new studio and a torch: %v, want ready", r)
	}
	if r, _ := reach("phone", fresh); r != technology.Far {
		t.Errorf("a new studio and a phone: %v, want far", r)
	}
	semis := fresh
	semis.Owned = item.NewSet("semiconductors")
	if r, steps := reach("phone", semis); r != technology.Next || len(steps) != 2 {
		t.Errorf("a studio with semiconductors and a phone: %v %v, want two steps", r, steps)
	}
	all := fresh
	all.Owned = item.NewSet("semiconductors", "microchips", "batteries")
	if r, _ := reach("phone", all); r != technology.Ready {
		t.Errorf("a studio with the chain and a phone: %v, want ready", r)
	}
	// A factory needs vehicle engineering for a car, one step away.
	factory := fresh
	factory.CompanyType = "factory"
	if r, steps := reach("car", factory); r != technology.Next || len(steps) != 1 || steps[0].Tech != "vehicle_engineering" {
		t.Errorf("a new factory and a car: %v %v", r, steps)
	}
	// The defence licence: a captain of the armed forces.
	d, ok := snap.DefenceLicence()
	if !ok || d.Sector != "defence" || snap.DefenceRankTier() != 3 {
		t.Errorf("defence licence %+v, rank tier %d", d, snap.DefenceRankTier())
	}
}

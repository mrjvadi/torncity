package content

import (
	"errors"
	"testing"
)

func TestShippedCompanies(t *testing.T) {
	snap, err := BuildSnapshot(1, shippedPack(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.CompanyTypes()) == 0 {
		t.Fatal("no company types shipped")
	}
	def, ty, ok := snap.CompanyType("grocery")
	if !ok || def.Place == "" || !ty.Hires("retail") {
		t.Fatalf("grocery: %+v %+v %v", def, ty, ok)
	}
	for _, c := range snap.Cities() {
		if _, m, ok := snap.CompanyMarket(c.Code); !ok || m.Population <= 0 || len(m.Demand) == 0 {
			t.Errorf("city %s has no NPC market", c.Code)
		}
	}
	if len(snap.CompanyReservedNames()) == 0 {
		t.Error("no reserved names shipped")
	}
}

func TestCompanyContentIsChecked(t *testing.T) {
	for name, mutate := range map[string]func(p *Pack){
		"unknown career":    func(p *Pack) { p.CompanyTypes[0].Careers = []string{"astronaut"} },
		"unknown place":     func(p *Pack) { p.CompanyTypes[0].Place = "moon" },
		"unknown category":  func(p *Pack) { p.CompanyTypes[0].Category = "ghosts" },
		"repeated type":     func(p *Pack) { p.CompanyTypes = append(p.CompanyTypes, p.CompanyTypes[0]) },
		"bad price range":   func(p *Pack) { p.CompanyTypes[0].PriceMinBPS = 12000 },
		"unknown city":      func(p *Pack) { p.CompanyMarkets[0].City = "atlantis" },
		"repeated market":   func(p *Pack) { p.CompanyMarkets = append(p.CompanyMarkets, p.CompanyMarkets[0]) },
		"negative demand":   func(p *Pack) { p.CompanyDemand[0].PerThousand = -1 },
		"repeated reserved": func(p *Pack) { p.CompanyReservedNames = append(p.CompanyReservedNames, "POLICE") },
		"upper-case code":   func(p *Pack) { p.CompanyTypes[0].Code = "Grocery" },
	} {
		p := shippedPack(t)
		mutate(p)
		if err := p.Validate(); !errors.Is(err, ErrInvalidCompanyContent) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

package content

import (
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/vshop"
)

func shopTestPack() *Pack {
	empty := &AvailabilityNeeds{}
	return &Pack{
		Items:      []ItemDef{{Code: "bread", BasePrice: 40}, {Code: "pick", BasePrice: 900, BlackMarket: true}},
		Components: []ComponentDef{{Code: "timber", BasePrice: 15}},
		VillageShop: []VillageShopDef{{
			Building: "general_store",
			Lines: []VillageShopLineDef{
				{Item: "bread", Class: "food", MarkupBPS: 11_000, PerHeadMilli: 300, Floor: 5, Requires: empty},
				{Item: "timber", Class: "other", MarkupBPS: 12_000, PerHeadMilli: 200, Floor: 6, Requires: empty},
			},
		}},
	}
}

func shopProblems(p *Pack) string {
	var problems []error
	p.validateVillageShop(&problems)
	var sb strings.Builder
	for _, e := range problems {
		sb.WriteString(e.Error() + "\n")
	}
	return sb.String()
}

func TestAGoodVillageShopIsAccepted(t *testing.T) {
	if got := shopProblems(shopTestPack()); got != "" {
		t.Fatalf("a good shop was refused: %s", got)
	}
}

func TestVillageShopLinesAreHeldToTheirRules(t *testing.T) {
	cases := map[string]func(*Pack){
		"names a good the content does not have": func(p *Pack) { p.VillageShop[0].Lines[0].Item = "caviar" },
		"names a good no shop may sell":          func(p *Pack) { p.VillageShop[0].Lines[0].Item = "pick" },
		"never sells under the reference price":  func(p *Pack) { p.VillageShop[0].Lines[0].MarkupBPS = 9_000 },
		"or over 1.5 times it":                   func(p *Pack) { p.VillageShop[0].Lines[0].MarkupBPS = 16_000 },
		"class \"snacks\" is not food or other":  func(p *Pack) { p.VillageShop[0].Lines[0].Class = "snacks" },
		"has no requires":                        func(p *Pack) { p.VillageShop[0].Lines[0].Requires = nil },
		"is listed twice":                        func(p *Pack) { p.VillageShop[0].Lines[1].Item = "bread" },
		"floor 0 is out of range":                func(p *Pack) { p.VillageShop[0].Lines[0].Floor = 0 },
		"only for research and buildings":        func(p *Pack) { p.VillageShop[0].Lines[0].Requires = &AvailabilityNeeds{Staff: []string{"x"}} },
		"a building requirement without a code": func(p *Pack) {
			p.VillageShop[0].Lines[0].Requires = &AvailabilityNeeds{Buildings: []AvailabilityBuilding{{Role: "market", Tier: 1}}}
		},
		"no line is open from founding": func(p *Pack) { p.VillageShop[0].Lines = p.VillageShop[0].Lines[:0] },
		"the shop sells nothing":        func(p *Pack) { p.VillageShop[0].Lines = nil },
	}
	for want, mutate := range cases {
		p := shopTestPack()
		mutate(p)
		if got := shopProblems(p); !strings.Contains(got, want) {
			t.Errorf("%q: not refused as expected: %q", want, got)
		}
	}
}

func TestShippedVillageShopSellsOnlyWhatItMayAndStartsWithAFoundingSet(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatal(err)
	}
	def, ok := snap.VillageShop()
	if !ok {
		t.Fatal("the shipped content has no village shop")
	}
	founding := map[string]bool{}
	for _, l := range def.Lines {
		rl, ok := snap.ShopLineRule(l)
		if !ok || rl.Ref < 1 {
			t.Errorf("%s has no reference price", l.Item)
		}
		if !needsAsk(l.Requires) {
			founding[l.Item] = true
		}
		if sh, ok := snap.ShelfOf(l.Item); !ok {
			t.Errorf("%s sits on no shelf", l.Item)
		} else if sh.Restricted {
			t.Errorf("%s is on a restricted shelf", l.Item)
		}
	}
	for _, want := range []string{"bread", "water_bottle", "bag_pouch", "bag_sack"} {
		if !founding[want] {
			t.Errorf("%s is not in the founding set: ADR 0046 5.3 says the stall sells it from day one", want)
		}
	}
	if len(founding) != 4 {
		t.Errorf("the founding set is %v, want exactly bread, water, pouch and sack", founding)
	}
	if l, ok := snap.VillageShopLine("bag_daypack"); !ok || !needsAsk(l.Requires) || l.Requires.Buildings[0].Code != def.Building {
		t.Errorf("the small rucksack must need the shop building: %+v", l)
	}
}

// A brand-new village (a handful of people) must be inside the day's supply
// budget with the shipped rules, floors and all, or the plan would have to cut
// lines of a shop that has hardly anything.
func TestAFreshVillageFitsTheSupplyBudget(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatal(err)
	}
	def, _ := snap.VillageShop()
	r := vshop.Rules{MarkupMinBPS: 10_000, MarkupMaxBPS: 15_000, StockDays: 2, FoodShareBPS: 4_000, OtherShareBPS: 3_000,
		PlayerDayFood: 3, PlayerDayOther: 2, SupplyValuePerResident: 600, BuildingBoostBPS: 15_000}
	var lines []vshop.Line
	for _, l := range def.Lines {
		rl, _ := snap.ShopLineRule(l)
		lines = append(lines, rl)
	}
	served := int64(1 + 5) // a founder and the NPC pool of a new village
	var floors int64
	for _, l := range lines {
		floors += l.Floor * l.Ref
	}
	if floors*3/2 > r.Budget(served) { // with the shop building's boost
		t.Errorf("the floors are worth %d (x1.5 with a shop building), the budget of a fresh village is %d", floors*3/2, r.Budget(served))
	}
}

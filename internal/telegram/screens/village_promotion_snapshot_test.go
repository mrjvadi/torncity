package screens

import "github.com/mrjvadi/torncity/internal/telegram/presenter"

// The way forward and the promotion (docs/adr/0028 section 4.1), as their own
// snapshot area: testdata/snapshots/<language>/promotion.txt.
func init() { snapshotAreas["promotion"] = promotionSnapshots }

func townGoals(residents, literacy, buildings int64, food, edu, health int, learned, treasury int64) []PromotionCriterionView {
	k := func(kind, role string, cur, req int64) PromotionCriterionView {
		return PromotionCriterionView{Kind: kind, Role: role, Current: cur, Required: req, Met: cur >= req}
	}
	return []PromotionCriterionView{
		k("residents", "", residents, 8), k("literacy", "", literacy, 2000), k("buildings", "", buildings, 5),
		k("role", "food", int64(food), 1), k("role", "education", int64(edu), 1), k("role", "health", int64(health), 1),
		k("knowledge", "", learned, 2), k("treasury", "", treasury, 2000),
	}
}

func promotionSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)
	name := villageNameFor(c)
	partway := PromotionView{Village: name, From: "village", To: "town", Office: "town_head", CanPromote: true,
		Criteria: townGoals(6, 1200, 3, 1, 0, 0, 1, 7400)}
	ready := PromotionView{Village: name, From: "village", To: "town", Office: "town_head", CanPromote: true, Met: true,
		Criteria: townGoals(10, 2600, 7, 1, 1, 1, 3, 5100)}
	readyMember := ready
	readyMember.CanPromote = false

	add("Village overview · the way to a town, part way", VillageOverview(g, VillageOverviewView{
		Name: name, Tier: "village", Population: 6, PopulationCap: 12, FoodPercent: 40, LiteracyPercent: 12, Treasury: 7_400,
		Promotion: &partway,
	}))
	add("Village overview · ready to promote (the head)", VillageOverview(g, VillageOverviewView{
		Name: name, Tier: "village", Population: 10, PopulationCap: 12, FoodPercent: 80, LiteracyPercent: 26, Treasury: 5_100,
		Resident: true, Promotion: &ready,
	}))
	add("Village overview · a town has no promotion block at the top", VillageOverview(g, VillageOverviewView{
		Name: name, Tier: "city", Population: 90, PopulationCap: 120, LiteracyPercent: 70, Treasury: 90_000,
	}))
	add("The way forward · part way", VillagePromotion(g, partway))
	add("The way forward · ready, the head", VillagePromotion(g, ready))
	add("The way forward · ready, a member", VillagePromotion(g, readyMember))
	add("Promotion · confirm (town)", VillagePromoteAsk(g, ready))
	add("Promotion · done (town)", VillagePromoted(g, ready))

	cityStep := PromotionView{Village: name, From: "town", To: "city", Office: "mayor", CanPromote: true, Met: true,
		Criteria: []PromotionCriterionView{
			{Kind: "residents", Current: 44, Required: 40, Met: true},
			{Kind: "literacy", Current: 5600, Required: 5000, Met: true},
			{Kind: "role", Role: "education", Current: 2, Required: 2, Met: true},
			{Kind: "role", Role: "security", Current: 1, Required: 1, Met: true},
			{Kind: "treasury", Current: 25_000, Required: 20_000, Met: true},
		}}
	add("Promotion · confirm (city)", VillagePromoteAsk(g, cityStep))
	add("Promotion · done (city)", VillagePromoted(g, cityStep))
	add("Promotion · refused, the top of the ladder", VillageRefusal(g, VillageRefusalView{Kind: VillagePromotionTop}))

	news := func(items ...VillageNewsItem) *presenter.Response {
		return VillageNews(g, VillageNewsView{Village: name, Items: items})
	}
	add("Village news · promoted", news(VillageNewsItem{Kind: NewsPromoted, Tier: "town", Player: who.friend}))
	add("Village news · promoted among others", news(
		VillageNewsItem{Kind: NewsBuilt, Building: sampleNamed(c.Lang, "farm_canal", "مزرعهٔ نهری", "Canal farm")},
		VillageNewsItem{Kind: NewsPromoted, Tier: "town", Player: who.friend}))
}

package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/budget"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/recruit"
	"github.com/mrjvadi/torncity/internal/domain/war"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The job market of specialists as one command reads it: each city's pools
// refilled to now, what a specialist expects, and how a company's offer
// looks to one (docs/adr/0027-specialist-recruitment.md).

// jobMarket is the recruitment world of one command.
type jobMarket struct {
	def   content.RecruitmentDef
	snap  *content.Snapshot
	scale gametime.Scale
	now   time.Time
}

// capacity is a city's pool of a skill at a level when nobody is hired.
func (m jobMarket) capacity(city world.City, skill string, level int) int64 {
	sk, ok := m.def.Skill(skill)
	if !ok {
		return 0
	}
	var population int64
	if mk, _, ok := m.snap.CompanyMarket(city.Code); ok {
		population = mk.Population
	}
	return recruit.Capacity(population, sk.PerHundredK, m.def.EducationBPS(city.Code), m.def.LevelShare(level))
}

// pool reads a city's pool refilled to now, and its capacity. With lock it
// is locked, created when never counted, and saved as refilled.
func (m jobMarket) pool(ctx context.Context, tx application.Tx, city world.City, skill string, level int, lock bool,
) (application.SpecialistPool, int64, error) {
	capacity := m.capacity(city, skill, level)
	repo := tx.Recruitment()
	if lock {
		if err := repo.EnsurePool(ctx, application.SpecialistPool{CityID: city.ID, Skill: skill, Level: level,
			Available: capacity, RefilledAt: m.now}); err != nil {
			return application.SpecialistPool{}, 0, err
		}
	}
	stored, err := repo.Pool(ctx, city.ID, skill, level, lock)
	if err != nil {
		return application.SpecialistPool{}, 0, err
	}
	// A city that funds its education line trains specialists faster.
	education, err := budgetEffect(ctx, tx, city.ID, budget.EffectCourseFee)
	if err != nil {
		return application.SpecialistPool{}, 0, err
	}
	regen := recruit.Regen{Capacity: capacity, PerGameDayBPS: m.def.RegenBPS + recruit.Scale(m.def.RegenBPS, education),
		Scale: int64(m.scale)}
	var p recruit.Pool
	if stored != nil {
		p = recruit.Pool{Available: stored.Available, RefilledAt: stored.RefilledAt}
	}
	p = regen.Refill(p, m.now)
	out := application.SpecialistPool{CityID: city.ID, Skill: skill, Level: level, Available: p.Available,
		RefilledAt: p.RefilledAt}
	if lock {
		if err := repo.SavePool(ctx, out); err != nil {
			return out, capacity, err
		}
	}
	return out, capacity, nil
}

// expected is what a specialist of skill and level from a pool of
// available out of capacity expects per period to work in dest.
func (m jobMarket) expected(skill string, level int, dest world.City, available, capacity int64) int64 {
	sk, _ := m.def.Skill(skill)
	scarcity := recruit.ScarcityBPS(m.def.ScarcityPremiumBPS, available, capacity)
	return recruit.Expected(sk.Wage(), level, m.def.ColBPS(dest.CostOfLiving), scarcity)
}

// expectedFrom is expected for a specialist of home, reading home's pool.
func (m jobMarket) expectedFrom(ctx context.Context, tx application.Tx, skill string, level int, home, dest world.City) (int64, error) {
	p, capacity, err := m.pool(ctx, tx, home, skill, level, false)
	if err != nil {
		return 0, err
	}
	return m.expected(skill, level, dest, p.Available, capacity), nil
}

// weights are the preferences' shares, in content order.
func (m jobMarket) weights() []int64 {
	out := make([]int64, len(m.def.Preferences))
	for i, p := range m.def.Preferences {
		out[i] = p.ShareBPS
	}
	return out
}

// homeCity is where a candidate comes from, as the company's city sees it.
type homeCity struct {
	city world.City
	// same and domestic: the company's own city, its own country.
	same, domestic bool
	// blocked: a travel sanction or a war between the two countries stops
	// the move.
	blocked bool
	km      int
}

// placeBPS is what moving from o weighs.
func (m jobMarket) placeBPS(o homeCity) int64 {
	switch {
	case o.same:
		return recruit.BPS
	case o.domestic:
		return m.def.Acceptance.OtherCityBPS
	}
	return m.def.Acceptance.OtherCountryBPS
}

// moveCost is what moving from o costs a specialist.
func (m jobMarket) moveCost(o homeCity) int64 {
	return m.def.Move.Rule().Cost(o.same, o.domestic, o.km)
}

// originOf works out where a city stands to the company's city.
func (m jobMarket) originOf(ctx context.Context, tx application.Tx, home, dest world.City, destCountry string) (homeCity, error) {
	o := homeCity{city: home, same: home.ID == dest.ID}
	if o.same {
		o.domestic = true
		return o, nil
	}
	if km, err := m.snap.Routes().DistanceBetween(home.Code, dest.Code); err == nil {
		o.km = km
	} else {
		// No route: nobody moves from there.
		o.blocked = true
	}
	country, err := tx.Diplomacy().CountryOfCity(ctx, home.ID)
	if err != nil {
		return o, err
	}
	o.domestic = country == destCountry
	if o.domestic || country == "" || destCountry == "" {
		return o, nil
	}
	sanctions, err := tx.Diplomacy().SanctionsBetween(ctx, country, destCountry)
	if err != nil {
		return o, err
	}
	rules := make([]diplomacy.Sanction, len(sanctions))
	for i, s := range sanctions {
		rules[i] = s.Rule()
	}
	if _, blocked := diplomacy.Blocks(rules, country, destCountry, diplomacy.Travel, m.now); blocked {
		o.blocked = true
	}
	enemies, err := atWarBetween(ctx, tx, country, destCountry, m.now)
	if err != nil {
		return o, err
	}
	o.blocked = o.blocked || enemies
	return o, nil
}

// atWarBetween reports whether two countries fight an active war on
// opposite sides.
func atWarBetween(ctx context.Context, tx application.Tx, a, b string, now time.Time) (bool, error) {
	wars, err := tx.War().Wars(ctx, a, now)
	if err != nil {
		return false, err
	}
	for _, w := range wars {
		r := w.Rule()
		sa, okA := r.SideOf(a)
		sb, okB := r.SideOf(b)
		if okA && okB && sa != sb && r.StatusAt(now) == war.Active {
			return true, nil
		}
	}
	return false, nil
}

// appeal is what a company's city and standing weigh with candidates: its
// war damage, its country at war, and the company's reputation.
func (m jobMarket) appeal(ctx context.Context, tx application.Tx, c application.Company, dest world.City,
	destCountry string,
) (int64, error) {
	a := m.def.Acceptance
	factor := int64(recruit.BPS)
	if wd, ok := m.snap.War(); ok {
		stored, err := tx.War().CityDamage(ctx, dest.ID, false)
		if err != nil {
			return 0, err
		}
		if stored != nil {
			damage := wd.CityRules().DamageAt(stored.DamageBPS, stored.AsOf, m.now, int64(m.scale))
			factor = recruit.Scale(factor, recruit.BPS-recruit.Scale(damage, a.WarDamageBPS))
		}
	}
	if destCountry != "" {
		wars, err := tx.War().Wars(ctx, destCountry, m.now)
		if err != nil {
			return 0, err
		}
		rules := make([]war.War, len(wars))
		for i, w := range wars {
			rules[i] = w.Rule()
		}
		if war.AtWar(rules, destCountry, m.now) {
			factor = recruit.Scale(factor, a.AtWarBPS)
		}
	}
	staff, err := tx.Companies().Staff(ctx, c.ID)
	if err != nil {
		return 0, err
	}
	specialists, err := tx.Recruitment().Staff(ctx, c.ID)
	if err != nil {
		return 0, err
	}
	stars := int64(0)
	if c.RatingBPS > 0 {
		stars = int64(company.Stars(c.RatingBPS))
	}
	rep := recruit.Reputation(a.ReputationMin, a.PerStarBPS, stars, a.PerStaffBPS, a.StaffCapBPS,
		int64(1+len(staff)+len(specialists)))
	return recruit.Scale(factor, rep), nil
}

// shareValue is what shares of a company are worth today: its reference
// price (last trade, else listing, else book per share) per share.
func shareValue(ctx context.Context, tx application.Tx, c application.Company, shares int64) (int64, error) {
	stocks := tx.Stocks()
	if shares <= 0 || stocks == nil {
		return 0, nil
	}
	last, err := stocks.LastPrice(ctx, c.ID)
	if err != nil {
		return 0, err
	}
	listing, err := stocks.Listing(ctx, c.ID)
	if err != nil {
		return 0, err
	}
	book, err := stocks.Book(ctx, c.ID)
	if err != nil {
		return 0, err
	}
	return refPrice(last, listing, book, c.TotalShares) * shares, nil
}

// offerOf is a campaign's package as the rules weigh it, its phantom
// shares valued today.
func offerOf(camp application.RecruitCampaign, equity int64) recruit.Offer {
	return recruit.Offer{Salary: camp.Salary, Housing: camp.Housing, Signing: camp.Signing, Relocation: camp.Relocation,
		Term: camp.TermPeriods, Equity: equity}
}

// cityOfCompany is the company's city from the content.
func cityOfCompany(snap *content.Snapshot, c application.Company) (world.City, bool) {
	return snap.CityByID(c.CityID)
}

// skillGapOf is how a company closes a gap in a skill
// (docs/adr/0027-specialist-recruitment.md): a campaign for a specialist of
// the level, and the courses that train it, the most first — at most two.
func skillGapOf(snap *content.Snapshot, companyCode, skill string, level int) *screens.SkillGap {
	g := &screens.SkillGap{Company: companyCode, Skill: skill, Level: level}
	type course struct {
		ref screens.CourseRef
		xp  int64
	}
	var found []course
	for _, co := range snap.Courses() {
		for _, r := range co.SkillRewards {
			if r.Skill == skill && r.XP > 0 {
				found = append(found, course{ref: screens.CourseRef{Code: co.Code, Name: co.Name}, xp: r.XP})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].xp > found[j].xp })
	for i := 0; i < len(found) && i < 2; i++ {
		g.Courses = append(g.Courses, found[i].ref)
	}
	return g
}

package content

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/recruit"
)

// Recruitment returns the recruitment section, and whether the content has
// one.
func (s *Snapshot) Recruitment() (RecruitmentDef, bool) {
	if s.recruitment == nil {
		return RecruitmentDef{}, false
	}
	return *s.recruitment, true
}

// Skill is one skill's market, and whether cities have specialists of it.
func (d RecruitmentDef) Skill(code string) (RecruitSkillDef, bool) {
	for _, s := range d.Skills {
		if s.Skill == code {
			return s, true
		}
	}
	return RecruitSkillDef{}, false
}

// LevelShare is a level's share of a skill's specialists, 0 for a level
// no city has.
func (d RecruitmentDef) LevelShare(level int) int64 {
	for _, l := range d.Levels {
		if l.Level == level {
			return l.ShareBPS
		}
	}
	return 0
}

// MaxLevel is the highest level any specialist has.
func (d RecruitmentDef) MaxLevel() int {
	top := 0
	for _, l := range d.Levels {
		top = max(top, l.Level)
	}
	return top
}

// EducationBPS is a city's education, the standard for a city not listed.
func (d RecruitmentDef) EducationBPS(city string) int64 {
	for _, c := range d.Cities {
		if c.City == city {
			return c.EducationBPS
		}
	}
	return recruit.BPS
}

// ColBPS is a cost of living against the reference, in basis points.
func (d RecruitmentDef) ColBPS(costOfLiving int64) int64 {
	if d.ReferenceCostOfLiving <= 0 || costOfLiving <= 0 {
		return recruit.BPS
	}
	return costOfLiving * recruit.BPS / d.ReferenceCostOfLiving
}

// Preference is a preference by code.
func (d RecruitmentDef) Preference(code string) (RecruitPreferenceDef, bool) {
	for _, p := range d.Preferences {
		if p.Code == code {
			return p, true
		}
	}
	return RecruitPreferenceDef{}, false
}

// validateRecruitment checks recruitment.yml.
func (p *Pack) validateRecruitment(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidRecruitmentContent, fmt.Sprintf(format, args...)))
	}
	if len(p.Recruitment) == 0 {
		return
	}
	if len(p.Recruitment) > 1 {
		bad("recruitment is declared %d times", len(p.Recruitment))
		return
	}
	d := p.Recruitment[0]
	money := func(where string, v int64) {
		if v < 0 || v > recruit.MaxWage {
			bad("%s %d is outside 0..%d", where, v, recruit.MaxWage)
		}
	}
	bps := func(where string, v, lo, hi int64) {
		if v < lo || v > hi {
			bad("%s %d is outside %d..%d", where, v, lo, hi)
		}
	}
	if d.ReferenceCostOfLiving < 1 || d.ReferenceCostOfLiving > recruit.MaxWage {
		bad("reference_cost_of_living %d", d.ReferenceCostOfLiving)
	}
	money("ad_fee", d.AdFee)
	bps("pool_regen_bps_per_game_day", d.RegenBPS, 1, recruit.BPS)
	bps("scarcity_premium_bps", d.ScarcityPremiumBPS, 0, 5*recruit.BPS)
	if d.Reach < 1 || d.Reach > 100 {
		bad("reach_per_check %d (1..100)", d.Reach)
	}
	skills := map[string]bool{}
	for _, c := range player.SkillCodes() {
		skills[string(c)] = true
	}
	seen := map[string]bool{}
	for i, s := range d.Skills {
		if !skills[s.Skill] || seen[s.Skill] {
			bad("skills[%d] %q is not a skill or repeats one", i, s.Skill)
		}
		seen[s.Skill] = true
		bps(fmt.Sprintf("skills[%d] per_100k", i), s.PerHundredK, 1, 100_000)
		money(fmt.Sprintf("skills[%d] wage_base", i), s.WageBase)
		money(fmt.Sprintf("skills[%d] wage_per_level", i), s.WagePerLevel)
		if s.WageBase < 1 {
			bad("skills[%d] wage_base must be positive", i)
		}
	}
	if len(d.Skills) == 0 {
		bad("no skill has specialists")
	}
	var total int64
	levels := map[int]bool{}
	for i, l := range d.Levels {
		if l.Level < 1 || l.Level > recruit.MaxLevel || levels[l.Level] {
			bad("levels[%d] %d is outside 1..%d or repeats", i, l.Level, recruit.MaxLevel)
		}
		levels[l.Level] = true
		bps(fmt.Sprintf("levels[%d] share_bps", i), l.ShareBPS, 1, recruit.BPS)
		total += l.ShareBPS
	}
	if len(d.Levels) == 0 || total > recruit.BPS {
		bad("levels share %d bps in all (1..%d)", total, recruit.BPS)
	}
	cities := map[string]bool{}
	for _, c := range p.Cities {
		cities[c.Code] = true
	}
	seenCity := map[string]bool{}
	for i, c := range d.Cities {
		if !cities[c.City] || seenCity[c.City] {
			bad("cities[%d] %q is not a city or repeats one", i, c.City)
		}
		seenCity[c.City] = true
		bps(fmt.Sprintf("cities[%d] education_bps", i), c.EducationBPS, 0, 5*recruit.BPS)
	}
	a := d.Acceptance
	if err := a.Curve().Validate(); err != nil {
		bad("acceptance: %v", err)
	}
	for _, f := range []struct {
		name string
		v    int64
	}{{"other_city_bps", a.OtherCityBPS}, {"other_country_bps", a.OtherCountryBPS}, {"war_damage_bps", a.WarDamageBPS},
		{"at_war_bps", a.AtWarBPS}, {"reputation_min_bps", a.ReputationMin}} {
		bps("acceptance."+f.name, f.v, 0, recruit.BPS)
	}
	for _, f := range []int64{a.PerStarBPS, a.PerStaffBPS, a.StaffCapBPS} {
		bps("acceptance reputation step", f, 0, recruit.BPS)
	}
	money("move.base", d.Move.Base)
	money("move.per_km", d.Move.PerKM)
	money("move.abroad", d.Move.Abroad)
	if err := d.Staff.Rules().Validate(); err != nil {
		bad("staff: %v", err)
	}
	p.validateRecruitPreferences(d, bad)
	p.validateRecruitPresets(d, bad)
}

func (p *Pack) validateRecruitPreferences(d RecruitmentDef, bad func(string, ...any)) {
	codes := map[string]bool{}
	var share int64
	for i, pr := range d.Preferences {
		if !transportCodePattern.MatchString(pr.Code) || codes[pr.Code] || len(pr.Code) > 16 {
			bad("preferences[%d] %q is not a short code or repeats one", i, pr.Code)
		}
		codes[pr.Code] = true
		share += max(pr.ShareBPS, 0)
		for _, v := range []int64{pr.ShareBPS, pr.SalaryBPS, pr.HousingBPS, pr.SigningBPS, pr.EquityBPS, pr.MoveBPS,
			pr.TermPerPeriodBPS, pr.TermCapBPS} {
			if v < 0 || v > 3*recruit.BPS {
				bad("preferences[%d] %q weighs %d bps (0..%d)", i, pr.Code, v, 3*recruit.BPS)
			}
		}
		if pr.SalaryBPS < 1 {
			bad("preferences[%d] %q does not value a salary", i, pr.Code)
		}
	}
	if len(d.Preferences) == 0 || share < 1 {
		bad("no preference has a share")
	}
}

func (p *Pack) validateRecruitPresets(d RecruitmentDef, bad func(string, ...any)) {
	pr := d.Presets
	lists := []struct {
		name   string
		v      []int64
		lo, hi int64
	}{
		{"salary_bps", pr.SalaryBPS, 1, 5 * recruit.BPS}, {"housing_bps", pr.HousingBPS, 0, recruit.BPS},
		{"signing_bps", pr.SigningBPS, 0, 20 * recruit.BPS}, {"relocation", pr.Relocation, 0, recruit.MaxWage},
		{"shares", pr.Shares, 0, 1_000_000},
	}
	for _, l := range lists {
		if len(l.v) < 1 || len(l.v) > 4 {
			bad("presets.%s has %d choices (1..4)", l.name, len(l.v))
		}
		for _, v := range l.v {
			if v < l.lo || v > l.hi {
				bad("presets.%s %d is outside %d..%d", l.name, v, l.lo, l.hi)
			}
		}
	}
	if len(pr.Terms) < 1 || len(pr.Terms) > 4 {
		bad("presets.terms has %d choices (1..4)", len(pr.Terms))
	}
	for _, t := range pr.Terms {
		if t < 1 || t > recruit.MaxTerm {
			bad("presets.terms %d is outside 1..%d", t, recruit.MaxTerm)
		}
	}
}

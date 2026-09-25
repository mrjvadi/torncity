package content

import (
	"errors"

	"github.com/mrjvadi/torncity/internal/domain/recruit"
)

// This file holds specialist recruitment (configs/content/recruitment.yml;
// docs/adr/0027-specialist-recruitment.md): which skills each city has NPC
// specialists of and how many, what they expect to be paid, how they weigh a
// company's offer, how patient they are once hired, and the presets of the
// campaign builder. The rules are internal/domain/recruit.

// ErrInvalidRecruitmentContent means recruitment.yml is unusable.
var ErrInvalidRecruitmentContent = errors.New("content: invalid recruitment content")

// RecruitmentDef is recruitment.yml's recruitment section.
type RecruitmentDef struct {
	// ReferenceCostOfLiving is the cost of living (cities.yml) at which the
	// market wages below are quoted.
	ReferenceCostOfLiving int64 `yaml:"reference_cost_of_living" json:"reference_cost_of_living"`
	// AdFee is what a campaign pays for each city it advertises in, to
	// that city's treasury.
	AdFee int64 `yaml:"ad_fee" json:"ad_fee"`
	// Announce posts a campaign's job advertisement in its cities' groups.
	Announce bool `yaml:"announce" json:"announce"`
	// RegenBPS is the share of a pool's capacity that comes back every GAME
	// day; a city's education budget raises it.
	RegenBPS int64 `yaml:"pool_regen_bps_per_game_day" json:"pool_regen_bps_per_game_day"`
	// ScarcityPremiumBPS is what an empty pool adds to its specialists'
	// expectations.
	ScarcityPremiumBPS int64 `yaml:"scarcity_premium_bps" json:"scarcity_premium_bps"`
	// Reach is how many specialists of one city and level one check of a
	// campaign reaches.
	Reach       int                    `yaml:"reach_per_check" json:"reach_per_check"`
	Skills      []RecruitSkillDef      `yaml:"skills" json:"skills"`
	Levels      []RecruitLevelDef      `yaml:"levels" json:"levels"`
	Cities      []RecruitCityDef       `yaml:"cities" json:"cities"`
	Acceptance  RecruitAcceptanceDef   `yaml:"acceptance" json:"acceptance"`
	Move        RecruitMoveDef         `yaml:"move" json:"move"`
	Staff       RecruitStaffDef        `yaml:"staff" json:"staff"`
	Preferences []RecruitPreferenceDef `yaml:"preferences" json:"preferences"`
	Presets     RecruitPresetsDef      `yaml:"presets" json:"presets"`
}

// RecruitSkillDef is one skill a city has specialists of.
type RecruitSkillDef struct {
	Skill        string `yaml:"skill" json:"skill"`
	PerHundredK  int64  `yaml:"per_100k" json:"per_100k"`
	WageBase     int64  `yaml:"wage_base" json:"wage_base"`
	WagePerLevel int64  `yaml:"wage_per_level" json:"wage_per_level"`
}

// Wage is the skill's market.
func (s RecruitSkillDef) Wage() recruit.Wage {
	return recruit.Wage{Base: s.WageBase, PerLevel: s.WagePerLevel}
}

// RecruitLevelDef is one level's share of a skill's specialists.
type RecruitLevelDef struct {
	Level    int   `yaml:"level" json:"level"`
	ShareBPS int64 `yaml:"share_bps" json:"share_bps"`
}

// RecruitCityDef is a city's education: how many of its people are
// specialists against the standard.
type RecruitCityDef struct {
	City         string `yaml:"city" json:"city"`
	EducationBPS int64  `yaml:"education_bps" json:"education_bps"`
}

// RecruitAcceptanceDef is how a candidate decides to apply.
type RecruitAcceptanceDef struct {
	FloorBPS        int64 `yaml:"floor_bps" json:"floor_bps"`
	FullBPS         int64 `yaml:"full_bps" json:"full_bps"`
	MaxChanceBPS    int64 `yaml:"max_chance_bps" json:"max_chance_bps"`
	OtherCityBPS    int64 `yaml:"other_city_bps" json:"other_city_bps"`
	OtherCountryBPS int64 `yaml:"other_country_bps" json:"other_country_bps"`
	WarDamageBPS    int64 `yaml:"war_damage_bps" json:"war_damage_bps"`
	AtWarBPS        int64 `yaml:"at_war_bps" json:"at_war_bps"`
	ReputationMin   int64 `yaml:"reputation_min_bps" json:"reputation_min_bps"`
	PerStarBPS      int64 `yaml:"per_star_bps" json:"per_star_bps"`
	PerStaffBPS     int64 `yaml:"per_staff_bps" json:"per_staff_bps"`
	StaffCapBPS     int64 `yaml:"staff_cap_bps" json:"staff_cap_bps"`
}

// Curve is the acceptance curve.
func (a RecruitAcceptanceDef) Curve() recruit.Curve {
	return recruit.Curve{FloorBPS: a.FloorBPS, FullBPS: a.FullBPS, MaxChanceBPS: a.MaxChanceBPS}
}

// RecruitMoveDef is what a move costs a specialist.
type RecruitMoveDef struct {
	Base   int64 `yaml:"base" json:"base"`
	PerKM  int64 `yaml:"per_km" json:"per_km"`
	Abroad int64 `yaml:"abroad" json:"abroad"`
}

// Rule is the move as the rules take it.
func (m RecruitMoveDef) Rule() recruit.Move {
	return recruit.Move{Base: m.Base, PerKM: m.PerKM, Abroad: m.Abroad}
}

// RecruitStaffDef is how patient a hired specialist is.
type RecruitStaffDef struct {
	UnderpaidBPS     int64 `yaml:"underpaid_bps" json:"underpaid_bps"`
	UnderpaidPeriods int   `yaml:"underpaid_periods" json:"underpaid_periods"`
	UnpaidPeriods    int   `yaml:"unpaid_periods" json:"unpaid_periods"`
}

// Rules is the staff rules.
func (s RecruitStaffDef) Rules() recruit.StaffRules {
	return recruit.StaffRules{UnderpaidBPS: s.UnderpaidBPS, UnderpaidPeriods: s.UnderpaidPeriods,
		UnpaidPeriods: s.UnpaidPeriods}
}

// RecruitPreferenceDef is one kind of specialist and how common it is.
type RecruitPreferenceDef struct {
	Code             string `yaml:"code" json:"code"`
	ShareBPS         int64  `yaml:"share_bps" json:"share_bps"`
	SalaryBPS        int64  `yaml:"salary_bps" json:"salary_bps"`
	HousingBPS       int64  `yaml:"housing_bps" json:"housing_bps"`
	SigningBPS       int64  `yaml:"signing_bps" json:"signing_bps"`
	EquityBPS        int64  `yaml:"equity_bps" json:"equity_bps"`
	MoveBPS          int64  `yaml:"move_bps" json:"move_bps"`
	TermPerPeriodBPS int64  `yaml:"term_per_period_bps" json:"term_per_period_bps"`
	TermCapBPS       int64  `yaml:"term_cap_bps" json:"term_cap_bps"`
}

// Rule is the preference as the rules take it.
func (p RecruitPreferenceDef) Rule() recruit.Preference {
	return recruit.Preference{SalaryBPS: p.SalaryBPS, HousingBPS: p.HousingBPS, SigningBPS: p.SigningBPS,
		EquityBPS: p.EquityBPS, MoveBPS: p.MoveBPS, TermPerPeriodBPS: p.TermPerPeriodBPS, TermCapBPS: p.TermCapBPS}
}

// RecruitPresetsDef are the campaign builder's one-press choices.
type RecruitPresetsDef struct {
	// SalaryBPS: of the market's expectation for the campaign's level in
	// the company's own city.
	SalaryBPS []int64 `yaml:"salary_bps" json:"salary_bps"`
	// HousingBPS: of the salary.
	HousingBPS []int64 `yaml:"housing_bps" json:"housing_bps"`
	// SigningBPS: of one period's salary.
	SigningBPS []int64 `yaml:"signing_bps" json:"signing_bps"`
	// Relocation: amounts.
	Relocation []int64 `yaml:"relocation" json:"relocation"`
	// Terms: contract lengths, in periods.
	Terms []int `yaml:"terms" json:"terms"`
	// Shares: phantom shares granted.
	Shares []int64 `yaml:"shares" json:"shares"`
}

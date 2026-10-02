package companies

import "time"

// RecruitCityChoice is one city the campaign builder offers.
type RecruitCityChoice struct {
	Code, Name string
	On         bool
	// Abroad is a city of another country.
	Abroad bool
}

// RecruitPresets are the builder's one-press choices, worked out: amounts
// of money, contract lengths, share counts.
type RecruitPresets struct {
	Salary, Housing, Signing, Relocation []int64
	Terms                                []int
	Shares                               []int64
}

// RecruitDraftView is a campaign being built.
type RecruitDraftView struct {
	Ref     CompanyRef
	No      int64
	Section string
	// What the campaign seeks.
	Skill           string
	Level, MaxLevel int
	// Skills are those cities have specialists of.
	Skills []string
	Cities []RecruitCityChoice
	// CityCode and City are the company's city, where the market is quoted.
	CityCode, City          string
	Positions, MaxPositions int
	// The package.
	Salary, Housing, Signing, Relocation int64
	Term                                 int
	Shares                               int64
	// ShareValue is what the phantom shares are worth today.
	ShareValue int64
	Auto       bool
	// Market is what a specialist of the level expects in the company's
	// city; Reach how many of the skill at or above the level are free in
	// the chosen cities; ChanceBPS a candidate of the company's city's
	// chance of applying, before preferences.
	Market    int64
	Reach     int64
	ChanceBPS int
	// AdFee is one city's fee; Available the company's free money.
	AdFee, Available int64
	Presets          RecruitPresets
	// Checks and Every are how the campaign runs once posted: its checks
	// and the wall-clock time between two.
	Checks int
	Every  time.Duration
	// Confirm asks before posting.
	Confirm bool
	// Notice is a notice kind (recruit.draft_notice.<kind>), "" for none.
	Notice string
}

// chosen counts the cities on.
func (v RecruitDraftView) Chosen() (n int, names []Named) {
	for _, ci := range v.Cities {
		if ci.On {
			n++
			names = append(names, Named{Code: ci.Code, Name: ci.Name})
		}
	}
	return n, names
}

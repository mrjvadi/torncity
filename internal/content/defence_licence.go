package content

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/military"
)

// This file holds the defence licence (military.yml defence_licence;
// docs/adr/0022-military-and-diplomacy.md, section 2.14): which sector of
// business it guards, which career is the armed forces and the lowest rank
// of it that may found a defence company, and what a civilian company's
// standing in technology must be to apply for a defence contractor licence.
// The rules are internal/domain/military (licence.go).

// ActionDefenceLicence approves, rejects and revokes defence licences.
const ActionDefenceLicence = "country.defence_licence"

// DefenceLicenceDef is military.yml's defence_licence.
type DefenceLicenceDef struct {
	// Sector is the sector of business (companies.yml sector) whose
	// companies need a defence licence to be founded.
	Sector string `yaml:"sector" json:"sector"`
	// Career is the armed forces (jobs.yml, paid_by: defence_fund), and
	// MinRank the lowest rank of it whose holder may found one.
	Career  string `yaml:"career" json:"career"`
	MinRank string `yaml:"min_rank" json:"min_rank"`
	// Contractor is what a civilian company must stand at in technology to
	// apply for a contractor licence.
	Contractor ContractorDef `yaml:"contractor" json:"contractor"`
}

// ContractorDef is the standing in technology a contractor licence asks:
// how many technologies of its own, and how deep in the tree the deepest.
type ContractorDef struct {
	MinTechnologies int `yaml:"min_technologies" json:"min_technologies"`
	MinTier         int `yaml:"min_tier" json:"min_tier"`
}

// Rule converts the definition.
func (d ContractorDef) Rule() military.ContractorRule {
	return military.ContractorRule{MinTechnologies: d.MinTechnologies, MinTier: d.MinTier}
}

// validateDefenceLicence checks the defence licence against the careers, the
// company types and the actions.
func (p *Pack) validateDefenceLicence(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: defence_licence: %s", ErrInvalidMilitaryContent, fmt.Sprintf(format, args...)))
	}
	if len(p.DefenceLicence) > 1 {
		bad("declared %d times", len(p.DefenceLicence))
	}
	if len(p.DefenceLicence) == 0 {
		return
	}
	d := p.DefenceLicence[0]
	if !transportCodePattern.MatchString(d.Sector) {
		bad("sector %q is not a code", d.Sector)
	}
	if _, ok := d.rankTier(p.Careers); !ok {
		bad("career %q is not a career paid by the defence fund with the rank %q", d.Career, d.MinRank)
	}
	if err := d.Contractor.Rule().Validate(); err != nil {
		bad("%v", err)
	}
	found := false
	for _, a := range p.Actions {
		if a.Code == ActionDefenceLicence {
			found = a.Jurisdiction == CountryLevel
		}
	}
	if !found {
		bad("the country action %q is not in governance.yml", ActionDefenceLicence)
	}
}

// rankTier is the tier of the career that MinRank names.
func (d DefenceLicenceDef) rankTier(careers []CareerDef) (int, bool) {
	for _, c := range careers {
		if c.Code != d.Career || !c.Military() {
			continue
		}
		for i, t := range c.Tiers {
			if t.Rank == d.MinRank {
				return i, true
			}
		}
	}
	return 0, false
}

// DefenceLicence returns the defence licence, and whether the content has
// one: without it no sector needs a licence.
func (s *Snapshot) DefenceLicence() (DefenceLicenceDef, bool) {
	if s.defenceLicence == nil {
		return DefenceLicenceDef{}, false
	}
	return *s.defenceLicence, true
}

// DefenceRankTier is the tier of the armed forces whose holders and above
// may found a defence company.
func (s *Snapshot) DefenceRankTier() int { return s.defenceRankTier }

// buildDefenceLicence indexes the defence licence. The pack has been
// validated.
func (s *Snapshot) buildDefenceLicence(p *Pack) {
	if len(p.DefenceLicence) == 0 {
		return
	}
	d := p.DefenceLicence[0]
	s.defenceLicence = &d
	s.defenceRankTier, _ = d.rankTier(p.Careers)
}

// LicensedSector reports whether founding a company of this kind needs a
// defence licence.
func (s *Snapshot) LicensedSector(def CompanyTypeDef) bool {
	d, ok := s.DefenceLicence()
	return ok && def.SectorCode() == d.Sector
}

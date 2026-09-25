package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/faction"
)

// This file holds the content of factions (configs/content/factions.yml;
// docs/adr/0023-health-missions-factions.md): what founding one costs, what
// each rank may do, how an organised crime's take is split, how long a crew
// has to gather, and the organised crimes themselves — crimes of the crime
// engine (crimes.yml's shape, the same success, reward and failure models)
// that a crew of a faction commits together. The rules are
// internal/domain/faction and internal/domain/crime.

// ErrInvalidFactionContent means the faction content is unusable.
var ErrInvalidFactionContent = errors.New("content: invalid faction content")

// CrewDef is the crew an organised crime needs: at least Min and at most
// Max members at the place, each one beyond the first adding BPSPerMember to
// the chance of success.
type CrewDef struct {
	Min          int `yaml:"min" json:"min"`
	Max          int `yaml:"max" json:"max"`
	BPSPerMember int `yaml:"bps_per_member,omitempty" json:"bps_per_member,omitempty"`
}

// FactionDef is factions.yml's faction section.
type FactionDef struct {
	// FoundingFee is paid to the treasury of the city the faction is
	// founded in, minor units.
	FoundingFee int64 `yaml:"founding_fee" json:"founding_fee"`
	// Rights says what officers and members may do; the leader may do
	// everything (faction.Charter).
	Rights map[string][]string `yaml:"rights" json:"rights"`
	// Shares weigh each rank in an organised crime's take.
	Shares map[string]int64 `yaml:"shares" json:"shares"`
	// CrimeCutBPS is the faction bank's cut of a take.
	CrimeCutBPS int64 `yaml:"crime_cut_bps" json:"crime_cut_bps"`
	// Gather is how long, GAME time, a planned crime waits for its crew.
	Gather string `yaml:"gather" json:"gather"`
	// OrganisedCrimes are the crimes a faction's crew commits together.
	OrganisedCrimes []OrganisedCrimeDef `yaml:"organised_crimes" json:"organised_crimes"`
}

// OrganisedCrimeDef is one organised crime: a crime and its crew.
type OrganisedCrimeDef struct {
	CrimeDef `yaml:",inline"`
	Crew     CrewDef `yaml:"crew" json:"crew"`
}

// Charter is the domain's value; the pack has been validated.
func (d FactionDef) Charter() faction.Charter {
	c := faction.Charter{}
	for r, list := range d.Rights {
		for _, x := range list {
			c[faction.Rank(r)] = append(c[faction.Rank(r)], faction.Right(x))
		}
	}
	return c
}

// ShareWeights is the domain's value.
func (d FactionDef) ShareWeights() faction.Shares {
	s := faction.Shares{}
	for r, w := range d.Shares {
		s[faction.Rank(r)] = w
	}
	return s
}

// GatherTime is Gather parsed, GAME time.
func (d FactionDef) GatherTime() time.Duration {
	t, _ := optionalDuration(d.Gather)
	return t
}

// Organised returns an organised crime's definition and domain value.
func (d FactionDef) Organised(code string, tiers []CrimeTierDef) (OrganisedCrimeDef, crime.Crime, bool) {
	for _, o := range d.OrganisedCrimes {
		if o.Code == code {
			cr, err := o.Crime(tiers)
			return o, cr, err == nil
		}
	}
	return OrganisedCrimeDef{}, crime.Crime{}, false
}

// validateFactions checks factions.yml.
func (p *Pack) validateFactions(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidFactionContent, fmt.Sprintf(format, args...)))
	}
	if len(p.Factions) == 0 {
		return
	}
	if len(p.Factions) > 1 {
		bad("faction is declared %d times", len(p.Factions))
		return
	}
	f := p.Factions[0]
	if f.FoundingFee < 0 {
		bad("founding_fee %d", f.FoundingFee)
	}
	for r := range f.Rights {
		if faction.Rank(r) == faction.Leader {
			bad("rights: the leader may do everything; do not list it")
		}
	}
	if err := f.Charter().Validate(); err != nil {
		bad("rights: %v", err)
	}
	if err := f.ShareWeights().Validate(); err != nil {
		bad("shares: %v", err)
	}
	if f.CrimeCutBPS < 0 || f.CrimeCutBPS > 5000 {
		bad("crime_cut_bps %d is outside 0..5000", f.CrimeCutBPS)
	}
	if d, err := optionalDuration(f.Gather); err != nil || d <= 0 {
		bad("gather %q is not a positive duration", f.Gather)
	}
	places := map[string]bool{}
	for _, v := range p.Venues {
		places[v.Code] = true
	}
	regular := map[string]bool{}
	for _, c := range p.Crimes {
		regular[c.Code] = true
	}
	seen := map[string]bool{}
	for i, o := range f.OrganisedCrimes {
		where := fmt.Sprintf("organised_crimes[%d] %q", i, o.Code)
		if !transportCodePattern.MatchString(o.Code) || seen[o.Code] || regular[o.Code] {
			bad("%s is not a code, repeated, or the code of a crime of crimes.yml", where)
		}
		seen[o.Code] = true
		if o.Crew.Min < 2 || o.Crew.Max < o.Crew.Min || o.Crew.Max > 20 || o.Crew.BPSPerMember < 0 || o.Crew.BPSPerMember > 2000 {
			bad("%s: crew %+v", where, o.Crew)
		}
		if len(o.Targets) != 1 || o.Targets[0] != string(crime.TargetNPC) {
			bad("%s: an organised crime targets npc only", where)
		}
		if len(o.Venues) == 0 {
			bad("%s: an organised crime is committed somewhere: name its venues", where)
		}
		for _, v := range o.Venues {
			if !places[v] {
				bad("%s: venue %q is not a place", where, v)
			}
		}
		cr, err := o.Crime(p.CrimeTiers)
		if err != nil {
			bad("%s: %v", where, err)
			continue
		}
		if cr.Duration <= 0 {
			bad("%s: an organised crime takes time: give it a duration", where)
		}
		if err := cr.Validate(); err != nil {
			bad("%s: %v", where, err)
		}
		if o.Failure.Injury != nil {
			if err := o.Failure.Injury.Injury().Validate(); err != nil {
				bad("%s failure.injury: %v", where, err)
			}
		}
	}
}

// Faction returns the faction section, and whether the content has one.
func (s *Snapshot) Faction() (FactionDef, bool) {
	if s.faction == nil {
		return FactionDef{}, false
	}
	return *s.faction, true
}

package content

import (
	"errors"
	"fmt"

	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/player"
)

// Validation failures for crime content. Each is wrapped with the offender.
var (
	// ErrUnknownCrimeTier means a crime names a tier crime_tiers lacks.
	ErrUnknownCrimeTier = errors.New("content: crime names an unknown criminal tier")
	// ErrInvalidCrimeTiers means the tier ladder is unusable: no tiers, not
	// from zero, not rising, or a tier without a code or name.
	ErrInvalidCrimeTiers = errors.New("content: invalid criminal tiers")
	// ErrInvalidVenues means the venue list is unusable.
	ErrInvalidVenues = errors.New("content: invalid venues")
	// ErrDuplicateCrimeCategory means two categories claimed one code, or a
	// category has no code.
	ErrDuplicateCrimeCategory = errors.New("content: invalid or duplicate crime category")
	// ErrEmptyCrimeCode means a crime arrived without its code.
	ErrEmptyCrimeCode = errors.New("content: crime code is required")
	// ErrDuplicateCrimeCode means two crimes claimed one code.
	ErrDuplicateCrimeCode = errors.New("content: duplicate crime code")
	// ErrUnknownCrimeCategory means a crime names a category nobody declares.
	ErrUnknownCrimeCategory = errors.New("content: crime names an unknown category")
	// ErrUnknownCrimeVenue means a crime can be committed at a venue nobody
	// declares.
	ErrUnknownCrimeVenue = errors.New("content: crime names an unknown venue")
	// ErrUnknownCrimeFacility means a crime requires a facility transport.yml
	// does not declare.
	ErrUnknownCrimeFacility = errors.New("content: crime requires an unknown facility")
	// ErrUnknownTool means a crime requires a tool that is not a known item.
	// No items exist yet, so every tool is unknown until they do.
	ErrUnknownTool = errors.New("content: crime requires an unknown tool")
	// ErrInvalidCrimeContent means the domain refused a crime.
	ErrInvalidCrimeContent = errors.New("content: invalid crime")
	// ErrCrimeNotWholeSeconds means a crime duration or a jail term is not a
	// whole number of seconds, which is how a sentence is stored.
	ErrCrimeNotWholeSeconds = errors.New("content: crime durations are whole seconds")
)

// knownTools is the set of item codes a crime may require. Items (ADR 0005)
// are not content yet, so it is empty: a crime naming a tool fails the load
// instead of asking for something nobody could ever carry. When items land,
// this becomes the item catalogue.
var knownTools = map[string]bool{}

// validateCrimes checks the tiers, venues, categories and crimes together,
// because a crime names all three and a venue names transport modes and
// career categories.
func (p *Pack) validateCrimes(problems *[]error) {
	if len(p.Crimes) == 0 && len(p.CrimeTiers) == 0 && len(p.Venues) == 0 && len(p.CrimeCategories) == 0 {
		// Crime content is optional as a whole: a pack without any (an older
		// version, a test fixture) simply has no crimes.
		return
	}
	add := func(err error) { *problems = append(*problems, err) }

	tiers := make([]crime.Tier, 0, len(p.CrimeTiers))
	for i, t := range p.CrimeTiers {
		if t.Code == "" || t.Name == "" {
			add(fmt.Errorf("%w: crime_tiers[%d] needs a code and a name", ErrInvalidCrimeTiers, i))
		}
		tiers = append(tiers, crime.Tier{Code: t.Code, MinXP: t.MinXP})
	}
	if err := crime.ValidateTiers(tiers); err != nil {
		add(fmt.Errorf("%w: %w", ErrInvalidCrimeTiers, err))
	}

	venues := make([]crime.Venue, 0, len(p.Venues))
	venueCodes := map[string]bool{}
	for i, v := range p.Venues {
		if v.Name == "" {
			add(fmt.Errorf("%w: crime_venues[%d] %q", ErrMissingDisplayName, i, v.Code))
		}
		venues = append(venues, v.Venue())
		venueCodes[v.Code] = true
	}
	if err := crime.ValidateVenues(venues); err != nil {
		add(fmt.Errorf("%w: %w", ErrInvalidVenues, err))
	}

	categories := map[string]bool{}
	for i, c := range p.CrimeCategories {
		switch {
		case c.Code == "" || categories[c.Code]:
			add(fmt.Errorf("%w: crime_categories[%d] %q", ErrDuplicateCrimeCategory, i, c.Code))
		case c.Name == "":
			add(fmt.Errorf("%w: crime category %q", ErrMissingDisplayName, c.Code))
		}
		categories[c.Code] = true
	}

	facilities := map[string]bool{}
	for _, f := range p.Facilities {
		facilities[f] = true
	}
	certifying := map[string]bool{}
	for _, c := range p.Courses {
		if c.Certifies {
			certifying[c.Code] = true
		}
	}

	seen := map[string]bool{}
	for i, c := range p.Crimes {
		where := fmt.Sprintf("crimes[%d]", i)
		switch {
		case c.Code == "":
			add(fmt.Errorf("%w: %s", ErrEmptyCrimeCode, where))
			continue
		case seen[c.Code]:
			add(fmt.Errorf("%w: %q (%s)", ErrDuplicateCrimeCode, c.Code, where))
			continue
		}
		seen[c.Code] = true
		if c.Name == "" {
			add(fmt.Errorf("%w: crime %q", ErrMissingDisplayName, c.Code))
		}
		if !categories[c.Category] {
			add(fmt.Errorf("%w: crime %q names %q", ErrUnknownCrimeCategory, c.Code, c.Category))
		}
		for _, v := range c.Venues {
			if !venueCodes[v] {
				add(fmt.Errorf("%w: crime %q names %q", ErrUnknownCrimeVenue, c.Code, v))
			}
		}
		for _, f := range c.RequiredFacilities {
			if !facilities[f] {
				add(fmt.Errorf("%w: crime %q requires %q", ErrUnknownCrimeFacility, c.Code, f))
			}
		}
		for _, cert := range c.RequiredCertifications {
			if !certifying[cert] {
				add(fmt.Errorf("%w: crime %q requires %q", ErrUnknownCertification, c.Code, cert))
			}
		}
		for _, tool := range c.RequiredTools {
			if !knownTools[tool] {
				add(fmt.Errorf("%w: crime %q requires %q", ErrUnknownTool, c.Code, tool))
			}
		}
		for _, s := range c.RequiredSkills {
			if err := player.Validate(player.SkillCode(s.Skill)); err != nil {
				add(fmt.Errorf("%w: crime %q: %w", ErrInvalidCrimeContent, c.Code, err))
			}
		}
		for name, raw := range map[string]string{"duration": c.Duration, "jail_min": c.Failure.JailMin, "jail_max": c.Failure.JailMax} {
			if d, err := optionalDuration(raw); err == nil && d%crimeTimeUnit != 0 {
				add(fmt.Errorf("%w: crime %q %s %q", ErrCrimeNotWholeSeconds, c.Code, name, raw))
			}
		}

		cr, err := c.Crime(p.CrimeTiers)
		if err != nil {
			add(err)
			continue
		}
		if err := cr.Validate(); err != nil {
			add(fmt.Errorf("%w: %s: %w", ErrInvalidCrimeContent, where, err))
		}
	}
}

// crimeWarnings reports a venue that names a transport mode or a career
// category no content declares. It is a warning, not an error, for the
// reason a city without routes is (Warnings): the venue rule simply never
// places anybody there, which is harmless, and a pack may carry no careers
// at all (an older version, or one loaded before work existed). It is still
// almost always a typo, so it is reported.
func (p *Pack) crimeWarnings() []string {
	modes := map[string]bool{}
	for _, m := range p.TransportModes {
		modes[m.Code] = true
	}
	categories := map[string]bool{}
	for _, c := range p.Careers {
		categories[c.Category] = true
	}
	var out []string
	for _, v := range p.Venues {
		for _, m := range v.Arrivals {
			if !modes[m] {
				out = append(out, fmt.Sprintf("venue %q places arrivals by transport mode %q, which no content declares", v.Code, m))
			}
		}
		for _, c := range v.WorkCategories {
			if !categories[c] {
				out = append(out, fmt.Sprintf("venue %q places workers of career category %q, which no career has", v.Code, c))
			}
		}
	}
	return out
}

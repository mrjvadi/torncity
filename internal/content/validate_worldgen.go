package content

import (
	"fmt"
	"strings"

	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// Validate checks configs/content/world.yml at the level a human editing the
// file needs: percentages in range, codes present, geology categories
// spelled correctly against the fixed set the generator recognises. The
// deeper structural checks (does every biome/resource make sense TOGETHER —
// duplicate codes, an empty range, a missing ocean/lake pair) are the
// domain's own Content.Validate, which ToContent also calls; this pass
// exists to give a percent-based mistake ("grade_min_pct: 150") a clearer
// message than the domain layer could, since the domain never sees the
// authored percentage, only the permille it was already converted to.
func (p *WorldGenPack) Validate() error {
	var problems []error

	known := map[string]struct{}{}
	for _, g := range worldgen.KnownGeologyCategories() {
		known[g] = struct{}{}
	}

	for _, b := range p.Biomes {
		if b.Code == "" {
			problems = append(problems, fmt.Errorf("%w: a biome is missing its code", ErrInvalidWorldGenContent))
			continue
		}
		if b.IsWater {
			continue
		}
		if b.MinTempC > b.MaxTempC {
			problems = append(problems, fmt.Errorf("%w: biome %q: min_temp_c > max_temp_c", ErrInvalidWorldGenContent, b.Code))
		}
		if b.MinPrecipMM < 0 || b.MinPrecipMM > b.MaxPrecipMM {
			problems = append(problems, fmt.Errorf("%w: biome %q: invalid precipitation range", ErrInvalidWorldGenContent, b.Code))
		}
	}

	for _, r := range p.Resources {
		if r.Code == "" {
			problems = append(problems, fmt.Errorf("%w: a resource is missing its code", ErrInvalidWorldGenContent))
			continue
		}
		if len(r.Geology) == 0 {
			problems = append(problems, fmt.Errorf("%w: resource %q has no geology entries", ErrInvalidWorldGenContent, r.Code))
		}
		for _, g := range r.Geology {
			if _, ok := known[g.Category]; !ok {
				problems = append(problems, fmt.Errorf("%w: resource %q references unknown geology category %q (known: %s)",
					ErrInvalidWorldGenContent, r.Code, g.Category, strings.Join(worldgen.KnownGeologyCategories(), ", ")))
			}
			if g.Weight < 0 || g.Weight > 100 {
				problems = append(problems, fmt.Errorf("%w: resource %q: geology weight for %q must be 0..100", ErrInvalidWorldGenContent, r.Code, g.Category))
			}
		}
		if r.GradeMinPct < 0 || r.GradeMaxPct > 100 || r.GradeMinPct > r.GradeMaxPct {
			problems = append(problems, fmt.Errorf("%w: resource %q: grade percentages must be within 0..100 and min<=max", ErrInvalidWorldGenContent, r.Code))
		}
		if r.ReserveMin <= 0 || r.ReserveMax < r.ReserveMin {
			problems = append(problems, fmt.Errorf("%w: resource %q: invalid reserve range", ErrInvalidWorldGenContent, r.Code))
		}
		if r.DepositsTarget <= 0 {
			problems = append(problems, fmt.Errorf("%w: resource %q: deposits_target must be positive", ErrInvalidWorldGenContent, r.Code))
		}
		if r.MinAbsLatitude < 0 || r.MinAbsLatitude > 90 || r.MaxAbsLatitude < 0 || r.MaxAbsLatitude > 90 {
			problems = append(problems, fmt.Errorf("%w: resource %q: latitude bounds must be within 0..90", ErrInvalidWorldGenContent, r.Code))
		}
	}

	if len(p.NameSyllables) < 8 {
		problems = append(problems, fmt.Errorf("%w: need at least 8 name_syllables entries", ErrInvalidWorldGenContent))
	}
	for _, s := range p.NameSyllables {
		if s.Latin == "" || s.Fa == "" {
			problems = append(problems, fmt.Errorf("%w: a name syllable is missing its latin or fa form", ErrInvalidWorldGenContent))
		}
	}

	for _, tpl := range []struct{ name, latin, fa string }{
		{"continent", p.NamingTemplates.ContinentLatin, p.NamingTemplates.ContinentFa},
		{"sea", p.NamingTemplates.SeaLatin, p.NamingTemplates.SeaFa},
		{"mountain", p.NamingTemplates.MountainLatin, p.NamingTemplates.MountainFa},
		{"river", p.NamingTemplates.RiverLatin, p.NamingTemplates.RiverFa},
	} {
		if strings.Count(tpl.latin, "%s") != 1 || strings.Count(tpl.fa, "%s") != 1 {
			problems = append(problems, fmt.Errorf("%w: naming_templates.%s must contain exactly one %%s in each script", ErrInvalidWorldGenContent, tpl.name))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	msgs := make([]string, len(problems))
	for i, p := range problems {
		msgs[i] = p.Error()
	}
	return fmt.Errorf("%w:\n  %s", ErrInvalidWorldGenContent, strings.Join(msgs, "\n  "))
}

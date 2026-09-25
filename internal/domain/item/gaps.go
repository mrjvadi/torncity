package item

import "sort"

// DesignGaps lists the technologies a company lacks to author a design of a
// good: the good's own (needs — what it takes to design that kind of good at
// all), and for each slot a design must fill, those of the component that
// fits it with the fewest missing — so the answer is the shortest way in, not
// every way. A slot any component the company may use fills needs nothing.
// possible is false when a slot the design must fill has no component in the
// catalogue at all: no technology opens that good.
//
// The result is sorted and free of repeats; what it names is exactly what
// ValidateDesign would refuse for, on the cheapest design.
func DesignGaps(a Archetype, needs []string, comps Components, access TechAccess) (missing []string, possible bool) {
	want := Set{}
	for _, t := range needs {
		if !access.Allows(t) {
			want[t] = struct{}{}
		}
	}
	codes := make([]string, 0, len(comps))
	for code := range comps {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, s := range a.Slots {
		if s.Optional {
			continue
		}
		var best []string
		found := false
		for _, code := range codes {
			c := comps[code]
			if c.Category != s.Accepts {
				continue
			}
			var lack []string
			for _, t := range c.RequiresTechnology {
				if !access.Allows(t) {
					lack = append(lack, t)
				}
			}
			if !found || len(lack) < len(best) {
				best, found = lack, true
			}
			if len(lack) == 0 {
				break
			}
		}
		if !found {
			return nil, false
		}
		for _, t := range best {
			want[t] = struct{}{}
		}
	}
	for t := range want {
		missing = append(missing, t)
	}
	sort.Strings(missing)
	return missing, true
}

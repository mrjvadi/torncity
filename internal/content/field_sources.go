package content

import "sort"

// FieldSources lists, for every research field, what real work feeds it (docs/adr/0054, plan A6): the buildings whose
// shifts, construction crews or open days practise it. A field without a source can never give a breakthrough discount,
// which is a defect the content lint refuses (TestEveryResearchFieldHasARealSource).
//
//	shift:<building>     a finished production shift in the building (role or raw trade feeds the field)
//	build:<building>     a finished construction, repair or fit-out shift on the building
//	service:<building>   a local day the daily service of the function was open
//	trade:<building>     a market day that sold, at a market post
//	research:<building>  a day scholars worked in a research building
//	teaching:<building>  a day a class of an education building taught someone to read
//	class:<building>     a finished class a certified teacher gave in the building (once a day)
func (s *Snapshot) FieldSources() map[string][]string {
	out := map[string][]string{}
	add := func(field, src string) {
		if field != "" {
			out[field] = append(out[field], src)
		}
	}
	for _, d := range s.settlementBuildings.defs {
		field := FieldOfRole(d.Role)
		if field == "" {
			continue
		}
		if len(d.Produces) > 0 {
			add(field, "shift:"+d.Code)
		}
		add(field, "build:"+d.Code)
		if d.Role == "education" && field == "education" {
			add("education", "teaching:"+d.Code)
			add("education", "class:"+d.Code)
		}
	}
	for _, f := range s.schema.functions {
		if f.Produces != nil && f.Produces.Daily && f.Produces.Field != "" {
			code := f.Code
			if len(f.Replaces) > 0 {
				code = f.Replaces[0]
			}
			add(f.Produces.Field, "service:"+code)
		}
		if f.Trade != nil && f.Trade.Export {
			add("market", "trade:"+f.Code)
		}
		if f.Research != nil {
			add("education", "research:"+f.Code)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

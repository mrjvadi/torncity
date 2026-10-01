package clientapi

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The content catalogue (GET /api/v1/content, api/client-api.md): every
// entry a game client may have to draw, by table, each with its name in
// every language the game speaks and the asset keys the client looks its
// art up by. The client holds no game content of its own, so a place, an
// item or a company type the operator adds tomorrow reaches it here, and is
// drawn with its table's fallback art until art is shipped for its key.

// ContentCatalogue is the content catalogue.
type ContentCatalogue struct {
	Version   string `json:"version"`
	Unchanged bool   `json:"unchanged,omitempty"`
	// Langs are the languages every name below carries.
	Langs   []string                    `json:"langs,omitempty"`
	Entries map[string][]CatalogueEntry `json:"entries,omitempty"`
	// Availability is the stage and the prerequisites of what a client may
	// show (configs/content/availability.yml), so a client lists a feature only
	// where the data says it exists and never hardcodes a tier.
	Availability []content.AvailabilityDef `json:"availability,omitempty"`
}

// CatalogueEntry is one content entry.
type CatalogueEntry struct {
	Code     string            `json:"code"`
	Name     map[string]string `json:"name"`
	Asset    CatalogueAsset    `json:"asset"`
	Category string            `json:"category,omitempty"`
	Kind     string            `json:"kind,omitempty"`
	// Footprint is [width, height] in lots, for a settlement building.
	Footprint []int `json:"footprint,omitempty"`
	// CapExempt is set for a settlement building the concurrent-construction
	// cap does not count (the road), so a client may lay it many at a time.
	CapExempt bool `json:"cap_exempt,omitempty"`
}

// CatalogueAsset names the art: a model (a building, a vehicle) and an
// icon, each "<table>:<code>".
type CatalogueAsset struct {
	Model string `json:"model,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// catalogueVersion is the content version as the catalogue spells it.
func catalogueVersion(snap *content.Snapshot) string {
	return "v" + strconv.Itoa(snap.Version())
}

// Catalogue builds the catalogue, or only its version when since already
// names it.
func (w *World) Catalogue(since string) ContentCatalogue {
	snap := w.Content.Current()
	out := ContentCatalogue{Version: catalogueVersion(snap)}
	if since != "" && since == out.Version {
		out.Unchanged = true
		return out
	}
	out.Langs = append([]string(nil), w.Msgs.Languages()...)
	ctxs := make(map[string]screens.Context, len(out.Langs))
	for _, lang := range out.Langs {
		ctxs[lang] = screens.Context{Msgs: w.Msgs, Lang: lang}
	}
	names := func(name func(screens.Context) string) map[string]string {
		m := make(map[string]string, len(ctxs))
		for lang, c := range ctxs {
			m[lang] = name(c)
		}
		return m
	}
	add := func(table string, e CatalogueEntry, model bool) {
		e.Asset.Icon = table + ":" + e.Code
		if model {
			e.Asset.Model = table + ":" + e.Code
		}
		out.Entries[table] = append(out.Entries[table], e)
	}
	out.Entries = map[string][]CatalogueEntry{}
	out.Availability = snap.AvailabilityTags("faction", "government_action", "office", "treaty_type")

	for _, city := range snap.Cities() {
		add("city", CatalogueEntry{Code: city.Code, Name: names(func(c screens.Context) string { return c.CityName(city.Code, city.Name) })}, false)
	}
	for _, p := range snap.Venues() {
		add("place", CatalogueEntry{Code: p.Code, Kind: "place",
			Name: names(func(c screens.Context) string { return c.SpotName(screens.Named{Code: p.Code, Name: p.Name}) })}, true)
	}
	for _, t := range snap.CompanyTypes() {
		add("company_type", CatalogueEntry{Code: t.Code, Category: t.Category,
			Name: names(func(c screens.Context) string { return c.CompanyTypeName(screens.Named{Code: t.Code, Name: t.Name}) })}, true)
	}
	for _, it := range snap.Items() {
		add("item", CatalogueEntry{Code: it.Code, Category: it.Category,
			Name: names(func(c screens.Context) string { return c.ItemName(screens.Named{Code: it.Code, Name: it.Name}) })}, false)
	}
	for _, cp := range snap.ComponentDefs() {
		add("component", CatalogueEntry{Code: cp.Code, Category: cp.Category,
			Name: names(func(c screens.Context) string { return c.ComponentName(screens.Named{Code: cp.Code, Name: cp.Name}) })}, false)
	}
	for _, m := range snap.TransportModes() {
		add("mode", CatalogueEntry{Code: m.Code, Name: names(func(c screens.Context) string { return c.ModeName(m.Code, m.Name) })}, true)
	}
	for _, cr := range snap.Crimes() {
		add("crime", CatalogueEntry{Code: cr.Code, Category: cr.Category,
			Name: names(func(c screens.Context) string { return c.CrimeName(screens.Named{Code: cr.Code, Name: cr.Name}) })}, false)
	}
	for _, co := range snap.Courses() {
		add("course", CatalogueEntry{Code: co.Code, Name: names(func(c screens.Context) string { return c.CourseName(co.Code, co.Name) })}, false)
	}
	for _, sk := range snap.Skills() {
		add("skill", CatalogueEntry{Code: sk.Code, Category: sk.Category, Name: names(func(c screens.Context) string { return c.SkillName(sk.Code) })}, false)
	}
	for _, tc := range snap.Technologies() {
		add("technology", CatalogueEntry{Code: tc.Code, Name: names(func(c screens.Context) string { return c.TechName(screens.Named{Code: tc.Code, Name: tc.Name}) })}, false)
	}
	for _, fc := range snap.ForceClasses() {
		add("military_unit", CatalogueEntry{Code: fc.Code, Category: fc.Branch,
			Name: names(func(c screens.Context) string { return c.ForceClassName(screens.Named{Code: fc.Code, Name: fc.Name}) })}, true)
	}
	// Knowledge of a settlement, and the names of life: ranks, sleeping spots
	// and stages of age. A client words these by code from here, so a name
	// never has to be written into a client.
	for _, k := range snap.SettlementKnowledgeDefs() {
		add("settlement_knowledge", CatalogueEntry{Code: k.Code,
			Name: names(func(c screens.Context) string { return c.SettlementKnowledgeName(screens.Named{Code: k.Code, Name: k.Name}) })}, false)
	}
	if life, ok := snap.Life(); ok {
		for _, r := range life.Ranks.Ladder {
			add("life_rank", CatalogueEntry{Code: r.Code,
				Name: names(func(c screens.Context) string { return c.RankName(screens.RankRef{Code: r.Code, Name: r.Name}) })}, false)
		}
		for _, sp := range life.Sleep.Spots {
			add("sleep_spot", CatalogueEntry{Code: sp.Code,
				Name: names(func(c screens.Context) string { return c.SleepSpotName(screens.Named{Code: sp.Code, Name: sp.Name}) })}, false)
		}
		for _, av := range life.Avatars {
			add("avatar", CatalogueEntry{Code: av.Code,
				Name: names(func(c screens.Context) string { return c.AvatarName(screens.Named{Code: av.Code, Name: av.Name}) })}, false)
		}
		for _, st := range life.Age.Stages {
			add("life_stage", CatalogueEntry{Code: st.Code,
				Name: names(func(c screens.Context) string { return c.StageName(screens.Named{Code: st.Code, Name: st.Name}) })}, false)
		}
	}
	// Politics and society: the names a client words by code (offices, policies,
	// the places above a city, budget lines, treaty kinds, sanction measures and
	// grounds).
	for _, code := range snap.LeverCodes() {
		add("lever", CatalogueEntry{Code: code, Name: names(func(c screens.Context) string { return c.LeverName(code) })}, false)
	}
	for _, j := range snap.JurisdictionDefs() {
		add("jurisdiction", CatalogueEntry{Code: j.Code, Kind: j.Level,
			Name: names(func(c screens.Context) string { return c.PlaceName(screens.GovPlace{Kind: j.Level, Code: j.Code, Name: j.Name}) })}, false)
	}
	if b, ok := snap.Budget(); ok {
		for _, l := range b.Lines {
			add("budget_line", CatalogueEntry{Code: l.Code, Name: names(func(c screens.Context) string { return c.BudgetLineName(l.Code) })}, false)
		}
	}
	for _, t := range snap.TreatyTypes() {
		add("treaty_kind", CatalogueEntry{Code: t.Code, Name: names(func(c screens.Context) string { return c.TreatyName(screens.Named{Code: t.Code, Name: t.Name}) })}, false)
	}
	for _, m := range diplomacy.Measures() {
		add("sanction_measure", CatalogueEntry{Code: string(m), Name: names(func(c screens.Context) string { return c.MeasureName(string(m)) })}, false)
	}
	for _, g := range snap.SanctionGrounds() {
		add("sanction_ground", CatalogueEntry{Code: g, Name: names(func(c screens.Context) string { return c.GroundName(g) })}, false)
	}
	// What the notices (docs/adr/0039, section 8) name: a client words an
	// achievement, a mission, a property, a treaty, a bank or insurance
	// product and an office by code from here, in the player's language.
	for _, a := range snap.Achievements() {
		add("achievement", CatalogueEntry{Code: a.Code,
			Name: names(func(c screens.Context) string { return c.AchievementName(screens.Named{Code: a.Code, Name: a.Name}) })}, false)
	}
	for _, m := range snap.Missions() {
		add("mission", CatalogueEntry{Code: m.Code,
			Name: names(func(c screens.Context) string { return c.MissionName(screens.Named{Code: m.Code, Name: m.Name}) })}, false)
	}
	for _, p := range snap.PropertyTypes() {
		add("property_type", CatalogueEntry{Code: p.Code, Kind: p.Kind,
			Name: names(func(c screens.Context) string { return c.PropertyTypeName(screens.Named{Code: p.Code, Name: p.Name}) })}, false)
	}
	for _, tt := range snap.TreatyTypes() {
		add("treaty_type", CatalogueEntry{Code: tt.Code,
			Name: names(func(c screens.Context) string { return c.TreatyName(screens.Named{Code: tt.Code, Name: tt.Name}) })}, false)
	}
	// The city shops and the city budget's lines (the economy screens).
	for _, sh := range snap.Shops() {
		add("shop", CatalogueEntry{Code: sh.Code,
			Name: names(func(c screens.Context) string { return c.ShopName(screens.Named{Code: sh.Code, Name: sh.Name}) })}, false)
	}
	if b, ok := snap.Budget(); ok {
		for _, l := range b.Lines {
			add("budget_line", CatalogueEntry{Code: l.Code, Name: names(func(c screens.Context) string { return c.BudgetLineName(l.Code) })}, false)
		}
	}
	if fin, ok := snap.Finance(); ok {
		for _, l := range fin.Loans {
			add("loan_product", CatalogueEntry{Code: l.Code,
				Name: names(func(c screens.Context) string { return c.LoanProductName(screens.Named{Code: l.Code, Name: l.Name}) })}, false)
		}
		for _, in := range fin.Insurance {
			add("insurance_product", CatalogueEntry{Code: in.Code,
				Name: names(func(c screens.Context) string {
					return c.InsuranceProductName(screens.Named{Code: in.Code, Name: in.Name})
				})}, false)
		}
	}
	// Offices are not content rows: their names are the locale's own
	// ("office.<code>"), so the codes are read from the catalogue when it can
	// list a section.
	offices := make([]string, 0, 24)
	if sec, ok := w.Msgs.(interface {
		Section(lang, prefix string) map[string]string
		Default() string
	}); ok {
		for code := range sec.Section(sec.Default(), "office") {
			if !strings.Contains(code, ".") {
				offices = append(offices, code)
			}
		}
	}
	sort.Strings(offices)
	for _, code := range offices {
		code := code
		add("office", CatalogueEntry{Code: code, Name: names(func(c screens.Context) string { return c.OfficeName(code) })}, false)
	}
	for _, sh := range snap.Shops() {
		add("shop", CatalogueEntry{Code: sh.Code,
			Name: names(func(c screens.Context) string { return c.ShopName(screens.Named{Code: sh.Code, Name: sh.Name}) })}, false)
	}
	// A career, and each position in it (code "<career>.<rank>"): what a job is called.
	for _, cr := range snap.Careers() {
		add("career", CatalogueEntry{Code: cr.Code, Category: cr.Category,
			Name: names(func(c screens.Context) string { return c.CareerName(cr.Code, cr.Name) })}, false)
		for _, tier := range cr.Tiers {
			add("career_tier", CatalogueEntry{Code: cr.Code + "." + tier.Rank, Category: cr.Code,
				Name: names(func(c screens.Context) string { return c.TierTitle(cr.Code, tier.Rank, tier.Title) })}, false)
		}
	}
	for _, sb := range snap.SettlementBuildingDefs() {
		add("settlement_building", CatalogueEntry{Code: sb.Code, Category: sb.Role, Footprint: []int{sb.Footprint[0], sb.Footprint[1]}, CapExempt: sb.CapExempt,
			Name: names(func(c screens.Context) string {
				return c.SettlementBuildingName(screens.Named{Code: sb.Code, Name: sb.Name})
			})}, true)
	}
	return out
}

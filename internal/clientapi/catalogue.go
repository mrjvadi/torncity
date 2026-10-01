package clientapi

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/content"
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
			add("rank", CatalogueEntry{Code: r.Code,
				Name: names(func(c screens.Context) string { return c.RankName(screens.RankRef{Code: r.Code, Name: r.Name}) })}, false)
		}
		for _, sp := range life.Sleep.Spots {
			add("sleep_spot", CatalogueEntry{Code: sp.Code,
				Name: names(func(c screens.Context) string { return c.SleepSpotName(screens.Named{Code: sp.Code, Name: sp.Name}) })}, false)
		}
		for _, st := range life.Age.Stages {
			add("age_stage", CatalogueEntry{Code: st.Code,
				Name: names(func(c screens.Context) string { return c.StageName(screens.Named{Code: st.Code, Name: st.Name}) })}, false)
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

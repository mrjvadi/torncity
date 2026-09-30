package screens

import (
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// This file holds group founding (docs/adr/0028-world-and-settlements.md
// section 3, section 9.5): the announcement a group sees the moment the
// game places its new village on the generated planet. Founding, like every
// civic act this game already follows the rule for, is a GROUP screen —
// never private — because it is a decision, made for the whole group, that
// the whole group should see argued and decided (ADR 0028 section 9.5).

// settlementKeyPrefix is the catalogue namespace for a founding-kit
// building's display name, keyed on its content code
// (docs/adr/0028-world-and-settlements.md section 7).
const settlementBuildingKeyPrefix = "settlement_building."

// biomeKeyPrefix is the catalogue namespace for a biome's display name,
// keyed on its content code (configs/content/world.yml).
const biomeKeyPrefix = "biome."

// BuildingName resolves a founding-kit building's display name.
func (c Context) BuildingName(typeCode string) string {
	return c.coded(settlementBuildingKeyPrefix, typeCode, "")
}

// BiomeName resolves a biome's display name.
func (c Context) BiomeName(code string) string {
	return c.coded(biomeKeyPrefix, code, "")
}

// SettlementFoundedView is what a group sees the instant its new village
// lands on the map: the place name, the terrain around it, the nearby
// river or continent the world generator named, and its two free starting
// buildings (ADR 0028 section 3.1/7).
type SettlementFoundedView struct {
	Name string `json:"name"`
	// SettlementID addresses the new village for a client.
	SettlementID string `json:"settlement_id,omitempty"`
	// BiomeCode is the founding cell's biome (configs/content/world.yml).
	BiomeCode string `json:"biome_code"`
	// NearbyFeature is the display name of the nearest named river or
	// continent (internal/domain/worldgen's own naming, section 1's
	// glossary) — empty when the world generator named none nearby.
	NearbyFeature string `json:"nearby_feature"`
	// Buildings are the founding kit's placed buildings' content codes, in
	// placement order.
	Buildings []string `json:"buildings"`
	// ProtectedUntil is when the beginner-protection window ends
	// (config settlement.protection_window).
	ProtectedUntil time.Time `json:"protected_until"`
	// The founding form's choices (docs/adr/0028 section 3): the founder's
	// name, the emblem (its codes, and the emoji that stand for it in
	// Telegram), the motto and the currency the village reserved.
	Founder      string             `json:"founder,omitempty"`
	Emblem       FoundingEmblemView `json:"emblem"`
	EmblemText   string             `json:"emblem_text,omitempty"`
	Motto        string             `json:"motto,omitempty"`
	CurrencyName string             `json:"currency_name,omitempty"`
	CurrencyCode string             `json:"currency_code,omitempty"`
	CurrencySign string             `json:"currency_symbol,omitempty"`
}

// SettlementFounded announces a newly founded village to its group.
func SettlementFounded(c Context, v SettlementFoundedView) *presenter.Response {
	return c.withView(renderSettlementFounded(c, v), ScreenSettlementFounded, v)
}

// SettlementFoundedText is the announcement as plain text, for the line the
// notifier posts in the group once the founder submitted the form.
func SettlementFoundedText(c Context, v SettlementFoundedView) string {
	return renderSettlementFounded(c, v).Text
}

func renderSettlementFounded(c Context, v SettlementFoundedView) *presenter.Response {
	feature := v.NearbyFeature
	if feature == "" {
		feature = c.T("settlement.found.unnamed_feature", nil)
	}
	buildings := make([]string, 0, len(v.Buildings))
	for _, code := range v.Buildings {
		buildings = append(buildings, c.BuildingName(code))
	}
	var emblem, motto, currency, founder string
	if v.EmblemText != "" {
		emblem = c.T("settlement.found.emblem", map[string]any{"emblem": v.EmblemText})
	}
	if v.Motto != "" {
		motto = c.T("settlement.found.motto", map[string]any{"motto": v.Motto})
	}
	if v.CurrencyCode != "" {
		currency = c.T("settlement.found.currency", map[string]any{"name": v.CurrencyName, "code": v.CurrencyCode})
	}
	if v.Founder != "" {
		founder = c.T("settlement.found.founder", map[string]any{"founder": v.Founder})
	}
	text := paragraphs(
		c.T("settlement.found.title", nil),
		c.T("settlement.found.body", map[string]any{
			"name":       v.Name,
			"feature":    feature,
			"biome":      c.BiomeName(v.BiomeCode),
			"buildings":  c.joinNames(buildings),
			"protection": FormatDate(c, v.ProtectedUntil),
		}),
		body(emblem, motto),
		body(founder, currency),
	)
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{}))
	return c.respond(text, kb.Build())
}

// SettlementRefusalView is a founding command refused: not run in a group,
// no world exists yet, or this chat already founded one (in which case Name
// is the village it already has, so the group is told which one rather
// than only that it cannot found a second).
type SettlementRefusalView struct {
	// Kind is "group_only", "no_world" or "already".
	Kind string
	Name string
}

// SettlementRefusal renders a founding command's refusal.
func SettlementRefusal(c Context, v SettlementRefusalView) *presenter.Response {
	return c.withView(renderSettlementRefusal(c, v), ScreenSettlementRefusal, v)
}

func renderSettlementRefusal(c Context, v SettlementRefusalView) *presenter.Response {
	var text string
	switch v.Kind {
	case "already":
		text = paragraphs(
			c.T("settlement.found.already.title", map[string]any{"name": v.Name}),
			c.T("settlement.found.already.body", nil),
		)
	case "no_world":
		text = c.T("settlement.found.no_world", nil)
	default:
		text = c.T("settlement.found.group_only", nil)
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{}))
	return c.respond(text, kb.Build())
}

// joinNames joins display names the way this language's own short lists
// read (format.list_separator): a settlement's starting buildings, say.
func (c Context) joinNames(names []string) string {
	sep := c.T("format.list_separator", nil)
	if sep == "format.list_separator" {
		sep = ", " // catalogue has no entry for this language: ASCII default
	}
	return strings.Join(names, sep)
}

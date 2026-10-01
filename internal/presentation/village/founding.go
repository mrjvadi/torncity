package village

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The founding form (docs/adr/0028-world-and-settlements.md section 3): a
// group's «ساخت روستا» opens a draft, and the group is told where the form is;
// the game client reads the form's facts (FoundingFormView), checks and submits
// it, and only the submission founds the village. Refusals of the form carry
// coded problems that each edge words itself.

// The founding screens, by name on the wire.
const (
	ScreenFoundingDraft   = "settlement_found_draft"
	ScreenFoundingForm    = "founding_form"
	ScreenFoundingChecked = "founding_checked"
	ScreenFoundingRefusal = "founding_refusal"
	ScreenSettlementFound = "settlement_founded"
	ScreenSettlementRefus = "settlement_refusal"
)

// AddrFoundDraft opens the form of a draft (settlement.found.draft); it takes
// the draft's id.
const AddrFoundDraft = "settlement:found.draft"

var (
	screenFoundDraft     = presentation.Define[FoundDraftView](ScreenFoundingDraft, "village")
	screenFoundingForm   = presentation.Define[FoundingFormView](ScreenFoundingForm, "village")
	screenFoundChecked   = presentation.Define[FoundingCheckedView](ScreenFoundingChecked, "village")
	screenFoundRefusal   = presentation.Define[FoundingRefusalView](ScreenFoundingRefusal, "village", presentation.Refusal())
	screenFounded        = presentation.Define[SettlementFoundedView](ScreenSettlementFound, "village")
	screenFoundGroupOnly = presentation.Define[SettlementRefusalView](ScreenSettlementRefus, "village", presentation.Refusal())
)

// FoundDraftView is the group message that sends the founder to the form.
type FoundDraftView struct {
	// Founder is the display name of the player who asked to found; empty
	// when the player has none worth showing.
	Founder string
	// Pending is true when somebody other than the asker holds the draft.
	Pending bool
	// Minutes is how long the draft still waits, rounded up.
	Minutes int
	// DraftID addresses the form.
	DraftID string
}

// FoundingChoiceView is one shape, colour or icon of the emblem catalogue.
// Its name is the edge's: each edge words the code in the player's language.
type FoundingChoiceView struct {
	Code string `json:"code"`
	// Hex is a palette colour's #rrggbb.
	Hex string `json:"hex"`
}

// FoundingEmblemView is an emblem as its four codes.
type FoundingEmblemView struct {
	Shape  string `json:"shape"`
	ColorA string `json:"color_a"`
	ColorB string `json:"color_b"`
	Icon   string `json:"icon"`
}

// FoundingLimitsView are the form's bounds, so a client can check as the
// player types (the server checks again).
type FoundingLimitsView struct {
	NameMin           int `json:"name_min"`
	NameMax           int `json:"name_max"`
	MottoMax          int `json:"motto_max"`
	CurrencyNameMin   int `json:"currency_name_min"`
	CurrencyNameMax   int `json:"currency_name_max"`
	CurrencyCodeLen   int `json:"currency_code_len"`
	CurrencySymbolMax int `json:"currency_symbol_max"`
}

// Founding form states.
const (
	// FoundingMine: the viewer started the draft and may submit it.
	FoundingMine = "mine"
	// FoundingOther: somebody else started it; the viewer only reads.
	FoundingOther = "other"
	// FoundingExpired: its time ran out; nothing was founded.
	FoundingExpired = "expired"
	// FoundingDone: it became a village.
	FoundingDone = "founded"
)

// FoundingFormView is what a client needs to draw the form.
type FoundingFormView struct {
	State     string    `json:"state"`
	Draft     string    `json:"draft"`
	ExpiresAt time.Time `json:"expires_at"`
	// Founder is the display name of the player completing the form.
	Founder string `json:"founder"`
	// SuggestedName is the generated place name the form starts from.
	SuggestedName string `json:"suggested_name"`
	// DefaultEmblem is an emblem the form starts from.
	DefaultEmblem FoundingEmblemView   `json:"default_emblem"`
	Limits        FoundingLimitsView   `json:"limits"`
	Shapes        []FoundingChoiceView `json:"shapes"`
	Palette       []FoundingChoiceView `json:"palette"`
	Icons         []FoundingChoiceView `json:"icons"`
	// NeutralCurrency is the money the village uses until it becomes a
	// country (the neutral city's).
	NeutralCurrency string `json:"neutral_currency"`
	// SettlementID and SettlementName are set once the draft became a village.
	SettlementID   string `json:"settlement_id,omitempty"`
	SettlementName string `json:"settlement_name,omitempty"`
}

// FoundingCheckedView is a form that would be accepted.
type FoundingCheckedView struct {
	Name     string `json:"name"`
	Currency string `json:"currency_code"`
}

// The kinds of a refused founding form.
const (
	FoundingNoDraft    = "no_draft"
	FoundingExpiredRef = "expired"
	FoundingNotFounder = "not_founder"
	FoundingAlready    = "already"
	FoundingInvalid    = "invalid"
	FoundingNoWorld    = "no_world"
	FoundingGroupOnly  = "group_only"
	FoundingNoContent  = "unavailable"
)

// FoundingProblem is one thing wrong with the form, coded.
type FoundingProblem struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// FoundingRefusalView is a founding command refused before it founded
// anything.
type FoundingRefusalView struct {
	Kind     string            `json:"kind"`
	Problems []FoundingProblem `json:"problems,omitempty"`
	// Name is the village this group already has (Kind already).
	Name   string             `json:"name,omitempty"`
	Limits FoundingLimitsView `json:"limits"`
}

// SettlementFoundedView is what a group sees the instant its new village
// lands on the map: the place name, the terrain around it, the nearby river
// or continent the world generator named, and its free starting buildings
// (ADR 0028 section 3.1/7).
type SettlementFoundedView struct {
	Name string `json:"name"`
	// SettlementID addresses the new village.
	SettlementID string `json:"settlement_id,omitempty"`
	// BiomeCode is the founding cell's biome (configs/content/world.yml).
	BiomeCode string `json:"biome_code"`
	// NearbyFeature is the name of the nearest named river or continent (the
	// world generator's own, a proper noun in the group's language); empty
	// when it named none nearby.
	NearbyFeature string `json:"nearby_feature"`
	// Buildings are the founding kit's placed buildings' content codes, in
	// placement order.
	Buildings []string `json:"buildings"`
	// ProtectedUntil is when the beginner-protection window ends.
	ProtectedUntil time.Time `json:"protected_until"`
	// The founding form's choices: the founder's name, the emblem (its
	// codes), the motto and the currency the village reserved.
	Founder      string             `json:"founder,omitempty"`
	Emblem       FoundingEmblemView `json:"emblem"`
	Motto        string             `json:"motto,omitempty"`
	CurrencyName string             `json:"currency_name,omitempty"`
	CurrencyCode string             `json:"currency_code,omitempty"`
	CurrencySign string             `json:"currency_symbol,omitempty"`
}

// The kinds of a refused founding command (not run in a group, no world yet,
// or this chat already founded one).
const (
	SettlementGroupOnly = "group_only"
	SettlementNoWorld   = "no_world"
	SettlementAlready   = "already"
)

// SettlementRefusalView is a founding command refused. Name is, for
// SettlementAlready, the village the group already has, so the group is told
// which one rather than only that it cannot found a second.
type SettlementRefusalView struct {
	Kind string
	Name string
}

// FoundingRefusalCode is the code of a refused founding form:
// "founding_<kind>".
func FoundingRefusalCode(kind string) string { return "founding_" + kind }

// SettlementRefusalCode is the code of a refused founding command:
// "settlement_<kind>".
func SettlementRefusalCode(kind string) string { return "settlement_" + kind }

// FoundDraft is the group message that sends the founder to the form.
func FoundDraft(c presentation.Ctx, v FoundDraftView) *presentation.Response {
	return screenFoundDraft.Response(c.Lang, v, act(AddrFoundDraft, v.DraftID).Named("founding.open_form").As(presentation.RolePrimary))
}

// FoundingForm is the form's facts for a client.
func FoundingForm(c presentation.Ctx, v FoundingFormView) *presentation.Response {
	return screenFoundingForm.Response(c.Lang, v)
}

// FoundingChecked is the answer to a check: the form is fine.
func FoundingChecked(c presentation.Ctx, v FoundingCheckedView) *presentation.Response {
	return screenFoundChecked.Response(c.Lang, v)
}

// FoundingRefusal is a refused founding form. Its code is "founding_" and
// the kind; for founding_invalid the view lists the coded problems by field.
func FoundingRefusal(c presentation.Ctx, v FoundingRefusalView) *presentation.Response {
	switch v.Kind {
	case FoundingNoDraft, FoundingExpiredRef, FoundingNotFounder, FoundingAlready, FoundingInvalid,
		FoundingNoWorld, FoundingGroupOnly, FoundingNoContent:
	default:
		v.Kind = FoundingNoDraft
	}
	args := map[string]any{}
	if v.Name != "" {
		args["name"] = v.Name
	}
	return screenFoundRefusal.Response(c.Lang, v, back(AddrHome)).Refused(FoundingRefusalCode(v.Kind), args)
}

// SettlementFounded announces a newly founded village to its group.
func SettlementFounded(c presentation.Ctx, v SettlementFoundedView) *presentation.Response {
	return screenFounded.Response(c.Lang, v, act(AddrVillageOverview).Named("village.overview"), back(AddrHome))
}

// SettlementRefusal is a refused founding command.
func SettlementRefusal(c presentation.Ctx, v SettlementRefusalView) *presentation.Response {
	args := map[string]any{}
	if v.Name != "" {
		args["name"] = v.Name
	}
	return screenFoundGroupOnly.Response(c.Lang, v, back(AddrHome)).Refused(SettlementRefusalCode(v.Kind), args)
}

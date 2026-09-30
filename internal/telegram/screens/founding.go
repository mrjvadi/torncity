package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The founding form (docs/adr/0028-world-and-settlements.md section 3): a
// group's «ساخت روستا» opens a draft and posts the group message below, with
// one button that opens the game client on the form; the client reads the
// form's facts (FoundingFormView), checks and submits it, and only the
// submission founds the village. Refusals of the form carry coded problems a
// client localises itself.

// Structured screens of the founding form (clients).
const (
	ScreenFoundingDraft   = "settlement_found_draft"
	ScreenFoundingForm    = "founding_form"
	ScreenFoundingChecked = "founding_checked"
	ScreenFoundingRefusal = "founding_refusal"
)

// FoundingStartParam is the Mini App start parameter that opens the form of a
// draft: "found_" and the draft's id without dashes (38 characters, within
// Telegram's 64).
func FoundingStartParam(draftID string) string {
	b := make([]byte, 0, len(draftID))
	for i := 0; i < len(draftID); i++ {
		if draftID[i] != '-' {
			b = append(b, draftID[i])
		}
	}
	return "found_" + string(b)
}

// FoundDraftView is the group message that sends the founder to the form.
type FoundDraftView struct {
	// Founder is the display name of the player who asked to found.
	Founder string
	// Pending is true when somebody other than the asker holds the draft.
	Pending bool
	// Minutes is how long the draft still waits, rounded up.
	Minutes int
	// DraftID addresses the form for a client.
	DraftID string
}

// FoundDraft renders the group message with the «تکمیل اطلاعات روستا» button.
func FoundDraft(c Context, v FoundDraftView) *presenter.Response {
	return renderFoundDraft(c, v)
}

func renderFoundDraft(c Context, v FoundDraftView) *presenter.Response {
	args := map[string]any{"founder": v.Founder, "minutes": v.Minutes}
	var text string
	if v.Pending {
		text = paragraphs(
			c.T("founding.draft.pending_title", args),
			c.T("founding.draft.pending_body", args),
			c.T("founding.draft.hint", nil),
		)
	} else {
		text = paragraphs(
			c.T("founding.draft.title", nil),
			c.T("founding.draft.body", args),
			c.T("founding.draft.deadline", args),
			c.T("founding.draft.hint", nil),
		)
	}
	kb := &presenter.Keyboard{Rows: [][]presenter.Button{{{
		Text:         c.T("founding.draft.button", nil),
		MiniAppParam: FoundingStartParam(v.DraftID),
	}}}}
	return c.respond(text, kb)
}

// FoundingChoiceView is one shape, colour or icon of the emblem catalogue,
// with its name in the player's language.
type FoundingChoiceView struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
	// Hex is a palette colour's #rrggbb.
	Hex string `json:"hex,omitempty"`
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
	// country (SUP).
	NeutralCurrency string `json:"neutral_currency"`
	// SettlementID and SettlementName are set once the draft became a village.
	SettlementID   string `json:"settlement_id,omitempty"`
	SettlementName string `json:"settlement_name,omitempty"`
}

// FoundingForm renders the form's facts for a client. In Telegram (a shared
// group screen) it has no view, only the sentence.
func FoundingForm(c Context, v FoundingFormView) *presenter.Response {
	return c.withView(renderFoundingForm(c, v), ScreenFoundingForm, v)
}

func renderFoundingForm(c Context, v FoundingFormView) *presenter.Response {
	args := map[string]any{"founder": v.Founder, "name": v.SettlementName}
	var text string
	switch v.State {
	case FoundingMine:
		text = paragraphs(c.T("founding.form.title", nil), c.T("founding.form.mine", args))
	case FoundingOther:
		text = paragraphs(c.T("founding.form.title", nil), c.T("founding.form.other", args))
	case FoundingDone:
		text = c.T("founding.form.done", args)
	default:
		text = c.T("founding.refusal.expired", nil)
	}
	return c.respond(text, nil)
}

// FoundingCheckedView is a form that would be accepted.
type FoundingCheckedView struct {
	Name       string `json:"name"`
	EmblemText string `json:"emblem_text"`
	Currency   string `json:"currency_code"`
}

// FoundingChecked renders the answer to a check: the form is fine.
func FoundingChecked(c Context, v FoundingCheckedView) *presenter.Response {
	return c.withView(c.respond(c.T("founding.checked", map[string]any{"name": v.Name}), nil), ScreenFoundingChecked, v)
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

// FoundingRefusal renders a refused founding form. For a client its error
// code is "founding_" and the kind.
func FoundingRefusal(c Context, v FoundingRefusalView) *presenter.Response {
	return c.withView(renderFoundingRefusal(c, v), ScreenFoundingRefusal, v)
}

func renderFoundingRefusal(c Context, v FoundingRefusalView) *presenter.Response {
	switch v.Kind {
	case FoundingNoDraft, FoundingExpiredRef, FoundingNotFounder, FoundingAlready, FoundingInvalid,
		FoundingNoWorld, FoundingGroupOnly, FoundingNoContent:
	default:
		v.Kind = FoundingNoDraft
	}
	args := map[string]any{"name": v.Name}
	text := c.T("founding.refusal."+v.Kind, args)
	if v.Kind == FoundingInvalid {
		lim := map[string]any{
			"name_min": v.Limits.NameMin, "name_max": v.Limits.NameMax, "motto_max": v.Limits.MottoMax,
			"currency_name_min": v.Limits.CurrencyNameMin, "currency_name_max": v.Limits.CurrencyNameMax,
			"code_len": v.Limits.CurrencyCodeLen, "symbol_max": v.Limits.CurrencySymbolMax,
		}
		lines := []string{text}
		for _, p := range v.Problems {
			lines = append(lines, "• "+c.T("founding.problem."+p.Code, lim))
		}
		text = joinLines(lines)
	}
	kb := keyboards.New()
	if c.Shared {
		kb.Nav(c.nav(keyboards.Nav{}))
	}
	return c.respond(text, kb.Build())
}

// FoundingChoiceName is the display name of an emblem shape, colour or icon
// (kind "shape", "color" or "icon"); the code itself when the catalogue has
// none.
func (c Context) FoundingChoiceName(kind, code string) string {
	return c.coded("founding."+kind+".", code, code)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

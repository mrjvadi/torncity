package screens

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Every office, lever and jurisdiction in the shipped content has a name in
// every shipped locale. Without one the screen says "an office" or shows the
// authored English name, and a player can no longer tell two policies apart.
func TestEveryShippedOfficeLeverAndPlaceHasANameInEveryLocale(t *testing.T) {
	pack, err := content.Load(contentDir)
	if err != nil {
		t.Fatalf("load content from %s: %v", contentDir, err)
	}
	if len(pack.Levers) == 0 || len(pack.Offices) == 0 {
		t.Fatalf("no levers or offices in %s", contentDir)
	}
	c := catalogue(t)
	var keys []string
	for _, o := range pack.Offices {
		keys = append(keys, officeKeyPrefix+o.Code)
	}
	for _, l := range pack.Levers {
		keys = append(keys, leverKeyPrefix+l.Code)
	}
	for _, j := range pack.Jurisdictions {
		keys = append(keys, jurisdictionKeyPrefix+j.Code)
	}
	for _, key := range keys {
		for _, lang := range c.Languages() {
			if !c.Has(lang, key) {
				t.Errorf("%q has no name in %s.yml", key, lang)
			}
		}
	}
}

// Every address a governance button can carry for the shipped content fits
// Telegram's 64 bytes and the gateway's byte set, at the longest place code
// of the lever's level and the widest value its bounds allow.
func TestGovernanceAddressesFitForShippedContent(t *testing.T) {
	pack, err := content.Load(contentDir)
	if err != nil {
		t.Fatal(err)
	}
	longest := map[string]string{}
	for _, c := range pack.Cities {
		if len(c.Code) > len(longest["city"]) {
			longest["city"] = c.Code
		}
	}
	for _, j := range pack.Jurisdictions {
		if len(j.Code) > len(longest[j.Level]) {
			longest[j.Level] = j.Code
		}
	}
	for _, l := range pack.Levers {
		place, ok := longest[l.Jurisdiction]
		if !ok {
			continue
		}
		for _, action := range []string{AddrGovLever, AddrGovConfirm, AddrGovSet} {
			for _, v := range []int64{l.MinValue(), l.MaxValue()} {
				data := strings.Join(leverAddr(action, GovLever{Code: l.Code}, GovPlace{Code: place}, v), keyboards.Separator)
				if !keyboards.Valid(data) {
					t.Errorf("%s is not a usable callback address (%d bytes)", data, len(data))
				}
			}
		}
	}
}

func govSampleLever() GovLever {
	return GovLever{
		Code: "city.tax_rate", Type: "bps", Value: 450, Default: 450, Min: 0, Max: 2500,
		HeldBy: "mayor", Notice: 24 * time.Hour, Cooldown: 72 * time.Hour,
		FromOffice: true, SetBy: &GovPlayer{Name: "Ada", Code: "K7Q2M9A"},
		Pending: &GovPending{Value: 800, In: 23 * time.Hour, By: &GovPlayer{Name: "Ada", Code: "K7Q2M9A"}},
	}
}

var govSamplePlace = GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}

func governanceScreens() map[string]func(Context) *presenter.Response {
	lever := govSampleLever()
	fee := GovLever{Code: "city.immigration_fee", Type: "money", Value: 1500, Min: 0, Max: 100000, HeldBy: "mayor",
		Notice: 24 * time.Hour, Cooldown: 72 * time.Hour}
	tariff := GovLever{Code: "country.border_tariff", Type: "bps", Value: 0, Max: 2500, HeldBy: "president",
		Notice: 72 * time.Hour, Cooldown: 168 * time.Hour, Vote: true}
	country := GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	return map[string]func(Context) *presenter.Response{
		"city hall": func(c Context) *presenter.Response {
			return CityGovernance(c, CityGovView{
				City: govSamplePlace, HoldsOffice: true,
				Sections: []GovSection{
					{Place: govSamplePlace, Offices: []GovOffice{
						{Code: "mayor", Seats: 1},
						{Code: "deputy_mayor", Seats: 1, Holders: []GovPlayer{{Name: "Bo", Code: "B0B0B0B"}}},
						{Code: "city_council", Seats: 5, Holders: []GovPlayer{{Name: "Cy", Code: "C1C1C1C"}, {Name: "Di", Code: "D2D2D2D"}}},
					}, Levers: []GovLever{lever, fee}},
					{Place: country, Offices: []GovOffice{{Code: "president", Seats: 1}}, Levers: []GovLever{tariff}},
				},
			})
		},
		"city hall, acting deputy": func(c Context) *presenter.Response {
			return CityGovernance(c, CityGovView{City: govSamplePlace, Sections: []GovSection{{Place: govSamplePlace,
				Offices: []GovOffice{{Code: "mayor", Seats: 1, ActingCode: "deputy_mayor", Acting: []GovPlayer{{Name: "Bo", Code: "B0B0B0B"}}}}}}})
		},
		"city hall, no city": func(c Context) *presenter.Response {
			return CityGovernance(c, CityGovView{NoCity: true})
		},
		"my office": func(c Context) *presenter.Response {
			return MyOffice(c, MyOfficeView{Seats: []GovSeat{
				{Office: "deputy_mayor", Place: govSamplePlace, ActingFor: "mayor", Levers: []GovLever{lever, fee}},
				{Office: "president", Place: country, VoteLevers: []GovLever{tariff}},
				{Office: "city_council", Place: govSamplePlace},
			}})
		},
		"my office, none": func(c Context) *presenter.Response { return MyOffice(c, MyOfficeView{}) },
		"lever": func(c Context) *presenter.Response {
			return LeverEdit(c, LeverEditView{Place: govSamplePlace, Lever: lever, Draft: 800, FineStep: 25, CoarseStep: 250})
		},
		"lever, money": func(c Context) *presenter.Response {
			return LeverEdit(c, LeverEditView{Place: govSamplePlace, Lever: fee, Draft: 1500, FineStep: 1000, CoarseStep: 10000})
		},
		"lever, cooldown": func(c Context) *presenter.Response {
			return LeverEdit(c, LeverEditView{Place: govSamplePlace, Lever: lever, Draft: 800, FineStep: 25, CoarseStep: 250,
				NextChangeIn: 50 * time.Hour})
		},
		"confirm": func(c Context) *presenter.Response {
			return PolicyConfirm(c, PolicyConfirmView{Place: govSamplePlace, Lever: lever, NewValue: 825})
		},
		"announced": func(c Context) *presenter.Response {
			return PolicyAnnounced(c, PolicyAnnouncedView{Place: govSamplePlace, Lever: lever, Old: 450, New: 825, In: 24 * time.Hour})
		},
		"history": func(c Context) *presenter.Response {
			return GovHistory(c, GovHistoryView{City: govSamplePlace, Page: 1, Pages: 2, Entries: []GovHistoryEntry{
				{Place: govSamplePlace, Lever: "city.tax_rate", Type: "bps", Office: "mayor", By: &GovPlayer{Name: "Ada", Code: "K7Q2M9A"},
					Old: 450, New: 800, Ago: 3 * time.Hour, EffectiveIn: 21 * time.Hour},
				{Place: country, Lever: "country.border_tariff", Type: "bps", Office: "president",
					Old: 0, New: 250, Ago: 80 * time.Hour, EffectiveIn: -8 * time.Hour},
			}})
		},
		"history, empty": func(c Context) *presenter.Response {
			return GovHistory(c, GovHistoryView{City: govSamplePlace, Page: 1, Pages: 1})
		},
	}
}

// govInternalCodes are strings a player must never read: lever, office and
// place codes as they are spelled in content.
var govInternalCodes = []string{"city.tax_rate", "tax_rate", "immigration_fee", "border_tariff",
	"deputy_mayor", "city_council", "default_country", "bps"}

func TestGovernanceScreensRenderEveryLineAndNoCode(t *testing.T) {
	for name, render := range governanceScreens() {
		for _, lang := range []string{"fa", "en"} {
			resp := render(ctx(t, lang, 0))
			assertRendered(t, resp)
			text := transcript(resp)
			for _, code := range govInternalCodes {
				if strings.Contains(text, code) {
					t.Errorf("%s/%s shows the code %q:\n%s", name, lang, code, text)
				}
			}
			if resp.Keyboard != nil {
				for _, row := range resp.Keyboard.Rows {
					for _, b := range row {
						if b.CallbackData != "" && !keyboards.Valid(b.CallbackData) {
							t.Errorf("%s/%s: unusable address %q", name, lang, b.CallbackData)
						}
					}
				}
			}
		}
	}
}

func govButtons(resp *presenter.Response) map[string]string {
	out := map[string]string{}
	if resp.Keyboard == nil {
		return out
	}
	for _, row := range resp.Keyboard.Rows {
		for _, b := range row {
			out[b.CallbackData] = b.Text
		}
	}
	return out
}

// The step buttons stay inside the bounds, a bound already reached offers no
// step past it, and the review button appears only for an actual change.
func TestLeverEditStepsStayInBounds(t *testing.T) {
	c := ctx(t, "en", 0)
	lever := govSampleLever()
	lever.Value = 0
	resp := LeverEdit(c, LeverEditView{Place: govSamplePlace, Lever: lever, Draft: 0, FineStep: 25, CoarseStep: 250})
	got := govButtons(resp)
	for data := range got {
		if strings.HasPrefix(data, AddrGovLever+":city.tax_rate:ostmarch:-") {
			t.Errorf("a step below the minimum is offered: %s", data)
		}
	}
	for _, want := range []string{"gov:lever:city.tax_rate:ostmarch:25", "gov:lever:city.tax_rate:ostmarch:250",
		"gov:lever:city.tax_rate:ostmarch:2500", "gov:lever:city.tax_rate:ostmarch:450"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	for data := range got {
		if strings.HasPrefix(data, AddrGovConfirm) {
			t.Errorf("review offered with no change: %s", data)
		}
	}

	resp = LeverEdit(c, LeverEditView{Place: govSamplePlace, Lever: lever, Draft: 2490, FineStep: 25, CoarseStep: 250})
	got = govButtons(resp)
	if _, ok := got["gov:lever:city.tax_rate:ostmarch:2500"]; !ok {
		t.Errorf("a step past the maximum is not clamped to it: %v", got)
	}
	if _, ok := got["gov:confirm:city.tax_rate:ostmarch:2490"]; !ok {
		t.Errorf("no review button for a change: %v", got)
	}

	resp = LeverEdit(c, LeverEditView{Place: govSamplePlace, Lever: lever, Draft: 800, FineStep: 25, CoarseStep: 250,
		NextChangeIn: time.Hour})
	for data := range govButtons(resp) {
		if strings.HasPrefix(data, AddrGovConfirm) || strings.HasSuffix(data, ":825") {
			t.Errorf("a change is offered during the cooldown: %s", data)
		}
	}
}

// Every governance sentinel has its own sentence, with the numbers filled in,
// both on the governance screens and through the generic error screen.
func TestEveryGovernanceRefusalHasItsOwnSentence(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	lever := govSampleLever()
	refusals := map[string]error{
		"gov.refusal.not_holder":       application.ErrNotOfficeHolder.WithDetail("office", "mayor"),
		"gov.refusal.requires_vote":    application.ErrPolicyRequiresVote.WithDetail("body", "city_council"),
		"gov.refusal.out_of_range":     application.ErrPolicyOutOfBounds.WithDetail("min", int64(0)),
		"gov.refusal.cooldown":         application.ErrPolicyCooldown.WithDetail("available_at", now.Add(30*time.Hour)),
		"gov.refusal.unsupported":      application.ErrLeverKindUnsupported,
		"gov.refusal.unknown_lever":    application.ErrUnknownLever,
		"gov.refusal.unknown_place":    application.ErrJurisdictionNotFound,
		"gov.refusal.wrong_place":      application.ErrWrongJurisdiction,
		"gov.refusal.office_not_found": application.ErrOfficeNotFound,
		"gov.refusal.office_occupied":  application.ErrOfficeOccupied,
		"gov.refusal.office_vacant":    application.ErrOfficeVacant,
		"gov.refusal.already_holds":    application.ErrAlreadyHoldsSeat,
		"gov.refusal.incompatible":     application.ErrIncompatibleOffices,
	}
	for want, err := range refusals {
		c := ctx(t, "en", 0)
		key, _, ok := governanceRefusal(c, err, &lever, now)
		if !ok || key != want {
			t.Errorf("%v: key %q, want %q", err, key, want)
		}
		for _, lang := range []string{"fa", "en"} {
			c := ctx(t, lang, 0)
			resp := PolicyRefused(c, PolicyRefusalView{Err: err, Place: &govSamplePlace, Lever: &lever, Now: now})
			assertRendered(t, resp)
			if resp.Text != c.T(key, govArgsOf(c, err, &lever, now)) {
				t.Errorf("%s/%s rendered %q", want, lang, resp.Text)
			}
			generic := Error(c, err)
			assertRendered(t, generic)
			if strings.Contains(generic.Text, c.T("error.internal", nil)) {
				t.Errorf("%s/%s falls through to the internal error", want, lang)
			}
		}
	}
	if IsGovernanceRefusal(application.ErrCityNotFound) {
		t.Error("a non-governance sentinel is taken for a governance refusal")
	}
}

func govArgsOf(c Context, err error, lever *GovLever, now time.Time) map[string]any {
	_, args, _ := governanceRefusal(c, err, lever, now)
	return args
}

func TestLeverValuesAreFormattedInTheirUnit(t *testing.T) {
	c := ctx(t, "en", 0)
	for _, tc := range []struct {
		typ  string
		v    int64
		want string
	}{
		{"bps", 450, "4.5%"},
		{"bps", 2500, "25%"},
		{"bps", 25, "0.25%"},
		{"money", 100000, c.T("format.money", map[string]any{"amount": "100,000"})},
		{"int", 12000, "12,000"},
	} {
		if got := FormatLeverValue(c, tc.typ, tc.v); got != tc.want {
			t.Errorf("%s %d = %q, want %q", tc.typ, tc.v, got, tc.want)
		}
	}
	if got, want := FormatSpan(c, 72*time.Hour), c.T("gov.span_d", map[string]any{"days": 3}); got != want {
		t.Errorf("72h = %q, want %q", got, want)
	}
	if got, want := FormatSpan(c, 24*time.Hour), FormatDuration(c, 24*time.Hour); got != want {
		t.Errorf("24h = %q, want %q", got, want)
	}
}

package clientapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

func neutralLabor() *presentation.Response {
	return village.LaborSite(presentation.Ctx{Lang: "fa"}, village.LaborSiteView{
		Village: "v", ID: "b1", CanWork: true, Job: &village.LaborJobLine{ID: "j1"},
	})
}

// A neutral answer reaches the web as data: the view, actions by id with named
// arguments, and not one word of Telegram: no text, no label, no row.
func TestNeutralScreenCarriesNoTelegramWording(t *testing.T) {
	out := ScreenOf(neutralLabor(), "settlement.labor.site", nil, nil)
	if !out.OK || out.Screen != "labor_site" || len(out.View) == 0 {
		t.Fatalf("screen = %+v", out)
	}
	if out.Text != "" {
		t.Errorf("a neutral answer must carry no text, got %q", out.Text)
	}
	var take *Action
	for i := range out.Actions {
		a := &out.Actions[i]
		if a.Label != "" || a.Row != nil {
			t.Errorf("action %s carries Telegram layout: %+v", a.ID, a)
		}
		if a.ID == "labor.take" {
			take = a
		}
	}
	if take == nil || take.Command != "settlement.labor.take" || take.Args["id"] != "j1" {
		t.Fatalf("the take action must name its argument as the command takes it: %+v", take)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), `"label"`) || strings.Contains(string(raw), `"text"`) || strings.Contains(string(raw), `"row"`) {
		t.Errorf("the JSON still holds a Telegram field: %s", raw)
	}
}

func TestNeutralRefusalIsACodeWithNoMessage(t *testing.T) {
	r := village.VillageRefusal(presentation.Ctx{Lang: "fa"}, village.VillageRefusalView{Kind: village.VillageDonateRange, Min: 100, Max: 900})
	out := ScreenOf(r, "settlement.donate", nil, nil)
	if out.OK || out.Error == nil || out.Error.Code != "village_donate_range" || out.Error.Message != "" {
		t.Fatalf("refusal = %+v", out.Error)
	}
	if out.Error.Args["min"] != int64(100) || out.Error.Args["max"] != int64(900) {
		t.Errorf("args = %v", out.Error.Args)
	}
}

// During the compatibility period a screen listed in legacy_text_screens also
// gets Telegram's text and button labels, from the hook the process wires; a
// screen not listed does not.
func TestLegacyTextOnlyForListedScreens(t *testing.T) {
	hook := func(_ context.Context, r *presentation.Response) (LegacyRendering, error) {
		return LegacyRendering{Text: "legacy " + r.Screen, Labels: map[string]string{"settlement:labor.take:j1": "کار کن"}}, nil
	}
	resp := neutralLabor()
	for _, tc := range []struct {
		screens []string
		want    bool
	}{{[]string{"*"}, true}, {[]string{"labor_site"}, true}, {[]string{"village_overview"}, false}, {nil, false}} {
		b := &Bridge{LegacyText: hook, LegacyScreens: tc.screens}
		if got := b.legacyFor(resp.Screen); got != tc.want {
			t.Errorf("legacyFor with %v = %v, want %v", tc.screens, got, tc.want)
		}
	}
	// the label is found by the address the neutral action opens
	out := ScreenOf(resp, "settlement.labor.site", nil, nil)
	for _, a := range out.Actions {
		if a.ID == "labor.take" {
			addr := presentation.Action{Command: a.Command, Args: positional(&a)}.Address()
			if addr != "settlement:labor.take:j1" {
				t.Errorf("address = %q", addr)
			}
		}
	}
}

// With the compatibility switch off, a screen a handler still answers in
// Telegram's words reaches the client without them.
func TestTelegramTextNeverReachesTheClientOnceTheSwitchIsOff(t *testing.T) {
	s := Screen{OK: true, Screen: "x", Text: "<b>سلام</b>\n💰 پول", Actions: []Action{{ID: "a", Label: "دکمهٔ زیر"}},
		Notice: &Notice{Text: "🔔 خبر"}, Error: &APIError{Message: "⚠ خطا"}}
	if len(ScreenLeftovers(s)) == 0 {
		t.Fatal("the detector found nothing in a Telegram screen")
	}
	stripTelegramText(&s)
	if l := ScreenLeftovers(s); len(l) != 0 {
		t.Errorf("still carries Telegram text: %v", l)
	}
}

func TestTelegramLeftoverDetector(t *testing.T) {
	for in, want := range map[string]string{
		"<b>x</b>": "html", "a <a href=\"u\">l</a>": "html", "💰 موجودی": "emoji-line", "x\n⏳ مانده": "emoji-line",
		"با دکمهٔ زیر ادامه دهید": "button-below", "use the button below": "button-below",
		"سلام": "", "a < b and c > d": "", "کار کردن": "",
	} {
		if got := TelegramLeftover(in); got != want {
			t.Errorf("TelegramLeftover(%q) = %q, want %q", in, got, want)
		}
	}
}

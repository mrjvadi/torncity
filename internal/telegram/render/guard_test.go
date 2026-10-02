package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/presentation"
)

// TestNoScreenCarriesTelegramTextToTheClient is the guard of the presentation
// split (docs/adr/0039): for every screen the core defines, what the client API
// sends is the view and the actions only. No text, no label, no message, and
// nothing in the JSON reads as Telegram wording (HTML, an emoji-led line, «the
// button below»).
func TestNoScreenCarriesTelegramTextToTheClient(t *testing.T) {
	specs := presentation.Specs()
	if len(specs) == 0 {
		t.Fatal("no screen is defined")
	}
	for _, s := range specs {
		raw, err := presentation.EncodeView(fill(s.View, 0).Interface())
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		resp := &presentation.Response{Contract: presentation.ContractNeutral, Screen: s.Name, View: raw, Lang: "fa",
			Actions: []presentation.Action{presentation.Do("player:profile.get").Named("back")}}
		out := clientapi.ScreenOf(resp, "x.y", nil, nil)
		if l := clientapi.ScreenLeftovers(out); len(l) != 0 {
			t.Errorf("%s carries Telegram text to the client: %v", s.Name, l)
		}
		b, _ := json.Marshal(out)
		var doc interface{}
		_ = json.Unmarshal(b, &doc)
		walkStrings(doc, func(v string) {
			if why := clientapi.TelegramLeftover(v); why != "" {
				t.Errorf("%s sends Telegram-looking text (%s): %q", s.Name, why, v)
			}
		})
	}
}

func walkStrings(v interface{}, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case []interface{}:
		for _, e := range x {
			walkStrings(e, f)
		}
	case map[string]interface{}:
		for _, e := range x {
			walkStrings(e, f)
		}
	}
}

// TestUnmigratedListStaysEmpty is the end of the migration's ratchet: nothing
// is ever added back to the list of screens the core answers in Telegram's
// words.
func TestUnmigratedListStaysEmpty(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "unmigrated_screens.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			t.Errorf("%q is listed as unmigrated: define it in internal/presentation instead", line)
		}
	}
}

// TestLegacyTextSwitchStaysOff keeps client.legacy_text_screens empty, in the
// committed file and in the code's defaults.
func TestLegacyTextSwitchStaysOff(t *testing.T) {
	cfg, err := config.Load(filepath.Join("..", "..", "..", "configs", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Client.LegacyTextScreens) != 0 {
		t.Errorf("configs/config.yml client.legacy_text_screens = %v, must stay empty", cfg.Client.LegacyTextScreens)
	}
	if d := config.Defaults(); len(d.Client.LegacyTextScreens) != 0 {
		t.Errorf("config defaults carry legacy_text_screens %v", d.Client.LegacyTextScreens)
	}
}

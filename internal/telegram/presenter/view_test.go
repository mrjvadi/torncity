package presenter

import (
	"encoding/json"
	"testing"
	"time"
)

type named struct {
	Code string
	Name string
}

type inner struct{ Deep int }

type sampleView struct {
	Name         string
	CityCode     string
	NextLevelXP  int64
	XPBPS        int
	EnergyFullIn time.Duration
	EndsAt       time.Time
	Never        time.Time
	Place        named
	Walk         *named
	Tags         []string
	Args         map[string]any
	Tagged       int `json:"custom"`
	Skipped      int `json:"-"`
	hidden       int
	Fn           func()
	inner
}

func TestEncodeView(t *testing.T) {
	v := sampleView{
		Name: "Sara", CityCode: "tehran", NextLevelXP: 100, XPBPS: 9000,
		EnergyFullIn: 1500 * time.Millisecond,
		EndsAt:       time.Date(2026, 9, 26, 10, 0, 0, 0, time.FixedZone("x", 3600)),
		Place:        named{Code: "bazaar", Name: "Bazaar"},
		Args:         map[string]any{"n": 1},
		Tagged:       7, Skipped: 1, hidden: 2,
		inner: inner{Deep: 3},
	}
	raw, err := EncodeView(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name": "Sara", "city_code": "tehran", "next_level_xp": 100.0, "xpbps": 9000.0,
		"energy_full_in_seconds": 2.0, "ends_at": "2026-09-26T09:00:00Z", "never": nil,
		"place": map[string]any{"code": "bazaar", "name": "Bazaar"}, "walk": nil, "tags": nil,
		"args": map[string]any{"n": 1.0}, "custom": 7.0, "deep": 3.0,
	}
	if len(got) != len(want) {
		t.Errorf("keys = %v, want %v", keys(got), keys(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing %q", k)
			continue
		}
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		if string(gb) != string(wb) {
			t.Errorf("%s = %s, want %s", k, gb, wb)
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSnakeCase(t *testing.T) {
	for in, want := range map[string]string{
		"CityCode": "city_code", "XPBPS": "xpbps", "BodyBPS": "body_bps", "MaxHealth": "max_health",
		"NextLevelXP": "next_level_xp", "ID": "id", "PlayerID": "player_id", "Level2Name": "level2_name",
	} {
		if got := SnakeCase(in); got != want {
			t.Errorf("SnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithViewKeepsTheScreen(t *testing.T) {
	r := WithView(Message("hi", nil), "profile", struct{ A int }{1})
	if r.Screen != "profile" || string(r.View) != `{"a":1}` || r.Text != "hi" {
		t.Errorf("got %+v", r)
	}
	if WithView(nil, "x", nil) != nil {
		t.Error("nil response must stay nil")
	}
}

package screens

import (
	"encoding/json"
	"testing"
	"time"
)

// A screen a client can draw carries its view in the player's own chat and
// none in a group, where its text leaves the player's money out.
func TestKeyScreensCarryTheirView(t *testing.T) {
	c := Context{Msgs: catalogue(t), Lang: "en"}
	v := ProfileView{Name: "Sara", Code: "K7Q2M9A", CityCode: "tehran", City: "Tehran", Level: 2,
		Cash: 1500, Bank: 9000, EnergyFullIn: 90 * time.Second}

	r := Profile(c, v)
	if r.Screen != ScreenProfile {
		t.Fatalf("screen = %q, want %q", r.Screen, ScreenProfile)
	}
	var got map[string]any
	if err := json.Unmarshal(r.View, &got); err != nil {
		t.Fatalf("view is not JSON: %v", err)
	}
	if got["city_code"] != "tehran" || got["cash"] != 1500.0 || got["energy_full_in_seconds"] != 90.0 {
		t.Errorf("view = %v", got)
	}
	if r.Text == "" {
		t.Error("the text must still be rendered")
	}

	c.Shared = true
	if r := Profile(c, v); r.View != nil || r.Screen != "" {
		t.Errorf("a shared profile carries a view: %s", r.View)
	}

	for name, resp := range map[string]any{
		ScreenCityMap:   CityMap(Context{Msgs: c.Msgs, Lang: "en"}, CityMapView{CityCode: "tehran", Places: []PlaceLine{{Place: Named{Code: "bazaar"}}}}),
		ScreenBank:      Bank(Context{Msgs: c.Msgs, Lang: "en"}, BankView{CityCode: "tehran", Cash: 5}),
		ScreenInventory: Inventory(Context{Msgs: c.Msgs, Lang: "en"}, InventoryView{}),
		ScreenJobStatus: JobStatus(Context{Msgs: c.Msgs, Lang: "en"}, JobStatusView{}),
		ScreenLife:      Life(Context{Msgs: c.Msgs, Lang: "en"}, LifeView{}),
	} {
		b, _ := json.Marshal(resp)
		var out struct {
			Screen string          `json:"screen"`
			View   json.RawMessage `json:"view"`
		}
		_ = json.Unmarshal(b, &out)
		if out.Screen != name || len(out.View) == 0 {
			t.Errorf("%s: screen %q, view %s", name, out.Screen, out.View)
		}
	}
}

package clientapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// The map fills lots from the centre out in the order given, keeps every
// plot off the roads, pads with greens and plazas, and lays out the same
// plots the same way every time.
func TestLayOutIsCentredStableAndOffTheRoads(t *testing.T) {
	plots := []WorldPlot{
		{ID: "place:centre", Kind: "place"}, {ID: "place:bazaar", Kind: "place"},
		{ID: "place:airport", Kind: "place"}, {ID: "company:ABC", Kind: "company"},
	}
	w := layOut("ostmarch", plots)
	if w.Grid.W != w.Grid.H || w.Grid.W%lotStride != 1 {
		t.Fatalf("grid %+v", w.Grid)
	}
	if len(w.Plots) < len(plots) || w.Plots[0].ID != "place:centre" {
		t.Fatalf("plots %+v", w.Plots)
	}
	centre := w.Grid.W / 2
	dist := func(p WorldPlot) int {
		dx, dy := p.X+1-centre, p.Y+1-centre
		return dx*dx + dy*dy
	}
	for i := 1; i < len(plots); i++ {
		if dist(w.Plots[i]) < dist(w.Plots[i-1]) {
			t.Fatalf("plot %d is nearer the centre than plot %d", i, i-1)
		}
	}
	road := map[[2]int]bool{}
	for _, r := range w.Roads {
		road[r] = true
	}
	for _, p := range w.Plots {
		for x := p.X; x < p.X+p.W; x++ {
			for y := p.Y; y < p.Y+p.H; y++ {
				if road[[2]int{x, y}] {
					t.Fatalf("%s stands on a road at %d,%d", p.ID, x, y)
				}
			}
		}
	}
	if len(w.Plots) == len(plots) || w.Plots[len(w.Plots)-1].Kind != "decor" {
		t.Fatalf("no open space: %d plots", len(w.Plots))
	}
	if again := layOut("ostmarch", plots); again.Version != w.Version {
		t.Fatalf("version %d then %d", w.Version, again.Version)
	}
}

func TestContentAndWorldEndpoints(t *testing.T) {
	f := newAPIFixture(t)
	f.codes.put("ABCD2345", application.ClientLinkClaim{PlayerID: "p1", BotID: "bot-a"}, time.Minute)
	_, session := f.call(t, "POST", "/api/v1/auth/link", "", map[string]string{"code": "ABCD2345"})
	token := session["access_token"].(string)

	status, out := f.call(t, "GET", "/api/v1/content", token, nil)
	if status != http.StatusOK || out["version"] != "v7" || out["unchanged"] != nil {
		t.Fatalf("content: %d %v", status, out)
	}
	status, out = f.call(t, "GET", "/api/v1/content?since=v7", token, nil)
	if status != http.StatusOK || out["unchanged"] != true {
		t.Fatalf("content since: %d %v", status, out)
	}
	status, out = f.call(t, "GET", "/api/v1/world/city", token, nil)
	if status != http.StatusOK || out["city"] != "tehran" {
		t.Fatalf("world: %d %v", status, out)
	}
	if status, _ = f.call(t, "GET", "/api/v1/world/city", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("world without a token: %d", status)
	}
}

// The city map's company plot sends company.show {id}; the game serves
// company.view {code}.
func TestCompanyShowIsCompanyView(t *testing.T) {
	cmd, args := clientAlias("company.show", map[string]json.RawMessage{"id": json.RawMessage(`"ABC1234"`)})
	if cmd != "company.view" || string(args["code"]) != `"ABC1234"` || len(args) != 1 {
		t.Fatalf("%s %v", cmd, args)
	}
	if cmd, _ := clientAlias("bank.show", nil); cmd != "bank.show" {
		t.Fatalf("bank.show became %s", cmd)
	}
}

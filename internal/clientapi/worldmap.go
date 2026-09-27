package clientapi

import (
	"context"
	"hash/fnv"
	"math"
	"sort"
	"strconv"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The city map (GET /api/v1/world/city, api/client-api.md). The game knows
// a city's places by how long the walk there takes, not by position, so the
// map is laid out here, the same way for every client: lots of 2×2 cells
// between one-cell roads, filled from the centre out — the city's places
// first, nearest walk first (so the centre is the centre and the airport
// the outskirts, as content's move times already say), then its companies
// oldest first, then greens and plazas on what is left. Nothing about
// where a building stands is authored in code.

// CityWorld is one city's map.
type CityWorld struct {
	City    string      `json:"city"`
	Version uint32      `json:"version"`
	Grid    GridSize    `json:"grid"`
	Water   WaterEdge   `json:"water"`
	Roads   [][2]int    `json:"roads"`
	Plots   []WorldPlot `json:"plots"`
}

// GridSize is the map's size in cells.
type GridSize struct {
	W int `json:"w"`
	H int `json:"h"`
}

// WaterEdge is the side of the map the water lies on, and how wide it is.
type WaterEdge struct {
	Side  string `json:"side"`
	Width int    `json:"width"`
}

// WorldPlot is one building, or green, on the map.
type WorldPlot struct {
	ID    string            `json:"id"`
	X     int               `json:"x"`
	Y     int               `json:"y"`
	W     int               `json:"w"`
	H     int               `json:"h"`
	Kind  string            `json:"kind"`
	Model string            `json:"model"`
	Rot   int               `json:"rot"`
	Ref   PlotRef           `json:"ref"`
	Name  map[string]string `json:"name,omitempty"`
}

// PlotRef says what a plot stands for.
type PlotRef struct {
	Table     string `json:"table"`
	Code      string `json:"code"`
	CompanyID string `json:"company_id,omitempty"`
	Owner     string `json:"owner,omitempty"`
}

// CityDirectory finds a city by its code.
type CityDirectory interface {
	ByCode(ctx context.Context, code string) (*application.City, error)
}

// CompanyDirectory lists a city's active companies, oldest first.
type CompanyDirectory interface {
	InCity(ctx context.Context, cityID string) ([]application.Company, error)
}

const (
	lotCells   = 2
	lotStride  = lotCells + 1
	waterWidth = 6
)

// CityWorld lays out the map of the city with the code.
func (w *World) CityWorld(ctx context.Context, code string) (CityWorld, error) {
	snap := w.Content.Current()
	city, err := w.CityCodes.ByCode(ctx, code)
	if err != nil {
		return CityWorld{}, err
	}
	companies, err := w.Companies.InCity(ctx, city.ID)
	if err != nil {
		return CityWorld{}, err
	}
	langs := w.Msgs.Languages()
	names := func(name func(screens.Context) string) map[string]string {
		m := make(map[string]string, len(langs))
		for _, lang := range langs {
			m[lang] = name(screens.Context{Msgs: w.Msgs, Lang: lang})
		}
		return m
	}

	places := snap.CityMap(city.Code).Places
	sort.SliceStable(places, func(i, j int) bool { return places[i].MoveTime < places[j].MoveTime })
	plots := make([]WorldPlot, 0, len(places)+len(companies))
	for _, p := range places {
		authored := p.Code
		if def, ok := snap.PlaceDef(p.Code); ok && def.Name != "" {
			authored = def.Name
		}
		plots = append(plots, WorldPlot{ID: "place:" + p.Code, Kind: "place", Model: "place:" + p.Code,
			Ref:  PlotRef{Table: "place", Code: p.Code},
			Name: names(func(c screens.Context) string { return c.SpotName(screens.Named{Code: p.Code, Name: authored}) })})
	}
	owners := map[string]string{}
	for _, co := range companies {
		owner, ok := owners[co.OwnerID]
		if !ok {
			if pl, err := w.Players.GetByID(ctx, co.OwnerID); err == nil {
				owner = pl.DisplayName
			}
			owners[co.OwnerID] = owner
		}
		plots = append(plots, WorldPlot{ID: "company:" + co.Code, Kind: "company", Model: "company:" + co.TypeCode,
			Ref:  PlotRef{Table: "company_type", Code: co.TypeCode, CompanyID: co.Code, Owner: owner},
			Name: names(func(screens.Context) string { return co.Name })})
	}
	return layOut(city.Code, plots), nil
}

// layOut places the plots on lots from the centre out, fills the rest
// with greens and plazas, and draws the roads between the lots.
func layOut(city string, plots []WorldPlot) CityWorld {
	// Room for every plot and a quarter again of open space.
	n := len(plots) + max(2, len(plots)/4)
	k := int(math.Ceil(math.Sqrt(float64(n))))
	size := k*lotStride + 1

	type lot struct{ x, y, d int }
	lots := make([]lot, 0, k*k)
	centre := size / 2
	for j := range k {
		for i := range k {
			x, y := 1+i*lotStride, 1+j*lotStride
			dx, dy := x+lotCells/2-centre, y+lotCells/2-centre
			lots = append(lots, lot{x, y, dx*dx + dy*dy})
		}
	}
	sort.SliceStable(lots, func(a, b int) bool {
		if lots[a].d != lots[b].d {
			return lots[a].d < lots[b].d
		}
		if lots[a].y != lots[b].y {
			return lots[a].y < lots[b].y
		}
		return lots[a].x < lots[b].x
	})

	out := CityWorld{City: city, Grid: GridSize{W: size, H: size},
		Water: WaterEdge{Side: "south", Width: waterWidth}, Roads: [][2]int{}, Plots: make([]WorldPlot, 0, len(lots))}
	for i, l := range lots {
		var p WorldPlot
		if i < len(plots) {
			p = plots[i]
		} else {
			decor := "green"
			if i%2 == 1 {
				decor = "plaza"
			}
			p = WorldPlot{ID: "decor:" + strconv.Itoa(i), Kind: "decor", Model: "decor:" + decor,
				Ref: PlotRef{Table: "decor", Code: decor}}
		}
		p.X, p.Y, p.W, p.H = l.x, l.y, lotCells, lotCells
		out.Plots = append(out.Plots, p)
	}
	for y := range size {
		for x := range size {
			if x%lotStride == 0 || y%lotStride == 0 {
				out.Roads = append(out.Roads, [2]int{x, y})
			}
		}
	}
	h := fnv.New32a()
	for _, p := range out.Plots {
		_, _ = h.Write([]byte(p.ID + "@" + strconv.Itoa(p.X) + "," + strconv.Itoa(p.Y) + ";"))
	}
	out.Version = h.Sum32()
	return out
}

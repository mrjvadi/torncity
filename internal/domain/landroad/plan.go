package landroad

import (
	"errors"

	"github.com/mrjvadi/torncity/internal/domain/roads"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
)

// Request is one road to plan: the world router chooses the corridor over
// tiles, the lot router lays the line inside it.
type Request struct {
	W      *worldgen.World
	Src    worldgen.TileSource
	Ground *WorldGround

	// Class and Planner are roads.yml: the class the surface research opens,
	// and the planner numbers.
	Class   Class
	Planner roads.Params
	// HighMountainM/HighMountainBPS: roads.yml planner.high_mountain_*.
	HighMountainM, HighMountainBPS int

	// From is a lot that already carries the settlement's road (or its hall);
	// To is where the player wants the road to go.
	From, To Lot

	// TileBlocked closes a tile to the world router (another settlement's
	// land); Blocked and Cheap are the lot router's (see Env).
	TileBlocked func(worldgen.TileID) bool
	Blocked     func(Lot) bool
	Cheap       func(Lot) bool

	MaxLots   int
	StreamRun int
	// CorridorRing is how many tiles round the world router's line the lot
	// router may stray (1).
	CorridorRing int
}

// Plan routes the road. The returned tiles are the world router's line (for
// the quote: its cost and length are the tile-level figures).
func Plan(r Request) (Path, roads.Route, error) {
	ringN := r.CorridorRing
	if ringN < 0 {
		ringN = 0
	}
	from, to := r.Ground.Tile(r.From), r.Ground.Tile(r.To)
	terr := roads.WorldTerrain{
		W: r.W, Src: r.Src, TerrainBPS: terrainOf(r.Ground), DefaultBPS: r.Ground.DefaultBPS,
		HighMountainM: r.HighMountainM, HighMountainBPS: r.HighMountainBPS, Blocked: r.TileBlocked,
	}
	route, err := roads.Plan(terr, []worldgen.TileID{from}, []worldgen.TileID{to},
		roads.Class{Code: r.Class.Code, MaxGradeBPS: r.Class.MaxGradeBPS, BridgeMaxSpanM: r.Class.BridgeMaxSpanM, Fords: r.Class.Fords}, r.Planner)
	if err != nil {
		var nb *roads.NoBridgeError
		switch {
		case errors.As(err, &nb):
			return Path{}, roads.Route{}, &BridgeError{SpanM: nb.SpanM, ClassMaxM: nb.ClassMaxM, At: r.To}
		case errors.Is(err, roads.ErrNoRoute):
			return Path{}, roads.Route{}, ErrNoRoute
		}
		return Path{}, roads.Route{}, err
	}
	corridor := make(map[worldgen.TileID]bool, len(route.Tiles)*9)
	for _, t := range route.Tiles {
		corridor[t] = true
		frontier := []worldgen.TileID{t}
		for ring := 0; ring < ringN; ring++ {
			var next []worldgen.TileID
			for _, f := range frontier {
				for _, nb := range r.W.Neighbours8(f) {
					if !corridor[nb] {
						corridor[nb] = true
						next = append(next, nb)
					}
				}
			}
			frontier = next
		}
	}
	env := Env{
		Ground: r.Ground, Class: r.Class, LotM: r.Ground.F.LotM,
		Blocked: r.Blocked, Cheap: r.Cheap, MaxLots: r.MaxLots, StreamRun: r.StreamRun,
		Corridor: func(l Lot) bool { return corridor[r.Ground.Tile(l)] },
	}
	path, err := Route(env, r.From, r.To)
	return path, route, err
}

func terrainOf(g *WorldGround) map[string]int { return g.TerrainBPS }

package settlementbuilding

import (
	"errors"
	"reflect"
	"testing"
)

// gridOf builds a grid from rows of characters: '.' free, '#' water, 'B' a
// building, 'R' a road, 'H' the hall.
func gridOf(rows ...string) (g Grid, roads, hall [][2]int) {
	g = make(Grid, len(rows))
	for y, row := range rows {
		g[y] = make([]Lot, len(row))
		for x, ch := range row {
			g[y][x] = Lot{Buildable: ch != '#', Occupied: ch == 'B' || ch == 'R' || ch == 'H'}
			switch ch {
			case 'R':
				roads = append(roads, [2]int{x, y})
			case 'H':
				hall = append(hall, [2]int{x, y})
			}
		}
	}
	return g, roads, hall
}

func TestPlanRoadsAlreadyConnected(t *testing.T) {
	g, roads, hall := gridOf(
		".....",
		"RB...",
		".....",
	)
	path, err := PlanRoads(g, [][2]int{{1, 1}}, roads, hall)
	if err != nil || path != nil {
		t.Errorf("a building beside a road needs nothing: path %v, err %v", path, err)
	}
}

func TestPlanRoadsBesideHallCountsAsConnected(t *testing.T) {
	g, roads, hall := gridOf(
		"HH...",
		"HHB..",
	)
	if path, err := PlanRoads(g, [][2]int{{2, 1}}, roads, hall); err != nil || path != nil {
		t.Errorf("a building on the hall's frontage is connected: path %v, err %v", path, err)
	}
}

func TestPlanRoadsLaysAStraightStreet(t *testing.T) {
	g, roads, hall := gridOf(
		"R....",
		".....",
		"....B",
	)
	path, err := PlanRoads(g, [][2]int{{4, 2}}, roads, hall)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) == 0 {
		t.Fatal("a building far from the road needs a street")
	}
	// The street ends beside the road and starts beside the building.
	last, first := path[len(path)-1], path[0]
	if !adjacent(last, [2]int{0, 0}) {
		t.Errorf("the street ends at %v, not beside the road at (0,0)", last)
	}
	if !adjacent(first, [2]int{4, 2}) {
		t.Errorf("the street starts at %v, not beside the building at (4,2)", first)
	}
	// Contiguous, on free lots only.
	for i, p := range path {
		if g[p[1]][p[0]].Occupied || !g[p[1]][p[0]].Buildable {
			t.Errorf("the street runs over a closed lot %v", p)
		}
		if i > 0 && !adjacent(path[i-1], p) {
			t.Errorf("the street has a gap between %v and %v", path[i-1], p)
		}
	}
}

func TestPlanRoadsIsDeterministic(t *testing.T) {
	g, roads, hall := gridOf(
		"R......",
		".......",
		".......",
		"......B",
	)
	first, err := PlanRoads(g, [][2]int{{6, 3}}, roads, hall)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, _ := PlanRoads(g, [][2]int{{6, 3}}, roads, hall)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d planned %v, the first planned %v", i, again, first)
		}
	}
}

func TestPlanRoadsRefusesWhenNothingCanReach(t *testing.T) {
	// The building is walled in by water and other buildings.
	g, roads, hall := gridOf(
		"R.#B#",
		"..#B#",
		"..###",
	)
	_, err := PlanRoads(g, [][2]int{{3, 0}, {3, 1}}, roads, hall)
	if !errors.Is(err, ErrNoRoadAccess) {
		t.Errorf("err = %v, want ErrNoRoadAccess", err)
	}
}

func TestPlanRoadsCutsAroundWater(t *testing.T) {
	g, roads, hall := gridOf(
		"R.#..",
		"..#..",
		"...#B",
		"....."[:5],
	)
	path, err := PlanRoads(g, [][2]int{{4, 2}}, roads, hall)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range path {
		if !g[p[1]][p[0]].Buildable {
			t.Errorf("street over water at %v", p)
		}
	}
}

func adjacent(a, b [2]int) bool {
	dx, dy := a[0]-b[0], a[1]-b[1]
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx+dy == 1
}

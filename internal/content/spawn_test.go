package content

import (
	"math"
	"testing"
)

func spawnWorld() []SpawnCandidate {
	return []SpawnCandidate{
		{Code: "ostmarch", ID: "id-o", Weight: 30},
		{Code: "fenwick_span", ID: "id-f", Weight: 25},
		{Code: "brennhaven", ID: "id-b", Weight: 20},
		{Code: "aldrin_hollow", ID: "id-a", Weight: 15},
		{Code: "kessmoor", ID: "id-k", Weight: 10},
		{Code: "calderis", ID: "id-c", Weight: 0},
	}
}

// Same person, same content: same city, every time and whatever order the
// cities arrive in. This is what makes a retried or raced first contact agree
// with itself.
func TestPickSpawnCityIsDeterministic(t *testing.T) {
	world := spawnWorld()
	reversed := make([]SpawnCandidate, len(world))
	for i, c := range world {
		reversed[len(world)-1-i] = c
	}

	for _, id := range []int64{1, 42, 777000, 5_123_456_789, math.MaxInt64, -3} {
		first, ok := PickSpawnCity(id, world)
		if !ok {
			t.Fatalf("no city picked for %d", id)
		}
		for i := 0; i < 1000; i++ {
			if got, _ := PickSpawnCity(id, world); got != first {
				t.Fatalf("user %d picked %s, then %s", id, first.Code, got.Code)
			}
		}
		if got, _ := PickSpawnCity(id, reversed); got != first {
			t.Errorf("user %d picked %s, but %s when the cities were listed in reverse", id, first.Code, got.Code)
		}
	}
}

// Over many people the shares follow the weights, and a weight of zero
// receives nobody at all. Ids are consecutive on purpose: Telegram ids are
// close to sequential, and sequential sign-ups must still spread.
func TestPickSpawnCityFollowsTheWeights(t *testing.T) {
	const n = 10_000
	world := spawnWorld()
	total := 0
	for _, c := range world {
		total += c.Weight
	}

	counts := map[string]int{}
	for i := int64(0); i < n; i++ {
		c, ok := PickSpawnCity(1_000_000_000+i, world)
		if !ok {
			t.Fatal("no city picked")
		}
		counts[c.Code]++
	}

	for _, c := range world {
		got := float64(counts[c.Code]) / n
		want := float64(c.Weight) / float64(total)
		if c.Weight == 0 {
			if counts[c.Code] != 0 {
				t.Errorf("%s has weight 0 and received %d players", c.Code, counts[c.Code])
			}
			continue
		}
		// Binomial standard deviation at n=10000 is under 0.5 percentage
		// points for every share here; 2 points is a wide, stable margin.
		if math.Abs(got-want) > 0.02 {
			t.Errorf("%s received %.3f of players, want %.3f (weight %d)", c.Code, got, want, c.Weight)
		}
	}
	t.Logf("spawn counts over %d ids: %v", n, counts)
}

func TestPickSpawnCityWithNowhereToGo(t *testing.T) {
	if _, ok := PickSpawnCity(1, nil); ok {
		t.Error("a pick with no candidates succeeded")
	}
	if _, ok := PickSpawnCity(1, []SpawnCandidate{{Code: "a", Weight: 0}, {Code: "b", Weight: -5}}); ok {
		t.Error("a pick with no positive weight succeeded")
	}
}

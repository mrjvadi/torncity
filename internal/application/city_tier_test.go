package application

import "testing"

// The city-only features (the budget, the city period) are offered to the
// city tier alone; a content city has no tier and is a city.
func TestCityTierGate(t *testing.T) {
	for tier, want := range map[string]bool{"": true, "city": true, "town": false, "village": false} {
		if got := (City{Tier: tier}).IsCityTier(); got != want {
			t.Errorf("tier %q: IsCityTier = %v, want %v", tier, got, want)
		}
	}
}

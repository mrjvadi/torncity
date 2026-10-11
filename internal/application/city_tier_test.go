package application

import "testing"

// The city-only features (the budget, the city period) belong to a content
// city; a founded settlement has a stored label and is not one.
func TestContentCity(t *testing.T) {
	for tier, want := range map[string]bool{"": true, "village": false} {
		if got := (City{Tier: tier}).Content(); got != want {
			t.Errorf("tier %q: Content = %v, want %v", tier, got, want)
		}
	}
}

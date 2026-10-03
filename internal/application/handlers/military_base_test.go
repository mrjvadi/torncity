package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
)

// The army exists only where a barracks stands; with nothing known about the
// settlement it is hidden, not offered by default.
func TestHasBarracks(t *testing.T) {
	standing := func(code string) func(content.AvailabilityBuilding) bool {
		return func(b content.AvailabilityBuilding) bool { return b.Code == code }
	}
	for _, tc := range []struct {
		name string
		s    hubSettlement
		want bool
	}{
		{"a content city has every building of its tier", hubSettlement{content: true}, true},
		{"a village with no buildings known", hubSettlement{}, false},
		{"a village with a granary only", hubSettlement{stands: standing("granary")}, false},
		{"a settlement with a barracks standing", hubSettlement{stands: standing(BarracksCode)}, true},
	} {
		if got := tc.s.hasBarracks(); got != tc.want {
			t.Errorf("%s: hasBarracks = %v, want %v", tc.name, got, tc.want)
		}
	}
}

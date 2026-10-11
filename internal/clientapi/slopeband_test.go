package clientapi

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/landuse"
)

func TestBandName(t *testing.T) {
	b := landuse.Bands{Flat: 8, Gentle: 20, Unusable: 30}
	for slope, want := range map[float64]string{0: "flat", 7.9: "flat", 8: "gentle", 25: "steep", 30: "none", 90: "none"} {
		if got := bandName(b, slope); got != want {
			t.Errorf("slope %v: %q, want %q", slope, got, want)
		}
	}
	if bandName(landuse.Bands{}, 5) != "" {
		t.Error("with the bands off no band is named")
	}
}

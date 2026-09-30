package world

import "testing"

const earthKM = 6371.0

func TestGreatCircleKM(t *testing.T) {
	tests := []struct {
		name                   string
		lat1, lon1, lat2, lon2 float64
		want                   int
	}{
		{"same point", 10, 20, 10, 20, 0},
		{"one degree of latitude", 0, 0, 1, 0, 112},
		{"quarter of the equator", 0, 0, 0, 90, 10008},
		{"pole to pole", 90, 0, -90, 0, 20016},
		{"antipodes on the equator", 0, 0, 0, 180, 20016},
	}
	for _, tc := range tests {
		got := GreatCircleKM(tc.lat1, tc.lon1, tc.lat2, tc.lon2, earthKM)
		if got != tc.want {
			t.Errorf("%s: got %d km, want %d", tc.name, got, tc.want)
		}
		if back := GreatCircleKM(tc.lat2, tc.lon2, tc.lat1, tc.lon1, earthKM); back != got {
			t.Errorf("%s: distance is not symmetric: %d there, %d back", tc.name, got, back)
		}
	}
}

// Two nearly identical places are still one kilometre apart, never zero.
func TestGreatCircleKMNeverZeroBetweenDifferentPlaces(t *testing.T) {
	if got := GreatCircleKM(10, 20, 10.0000001, 20, earthKM); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

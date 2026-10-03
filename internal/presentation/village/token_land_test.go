package village

import "testing"

func TestLotTokenCarriesLandWestAndSouthOfTheGrid(t *testing.T) {
	cases := []struct {
		x, y int
		tok  string
	}{{3, 1, "3-1"}, {0, 0, "0-0"}, {-3, 7, "m3-7"}, {5, -12, "5-m12"}, {-1, -1, "m1-m1"}, {1200, -900, "1200-m900"}}
	for _, c := range cases {
		if got := LotToken(c.x, c.y, false); got != c.tok {
			t.Fatalf("LotToken(%d,%d) = %q, want %q", c.x, c.y, got, c.tok)
		}
		x, y, rot, ok := ParseLotTokenAny(c.tok)
		if !ok || rot || x != c.x || y != c.y {
			t.Fatalf("%q reads back as %d,%d (%v)", c.tok, x, y, ok)
		}
		if c.x >= 0 && c.y >= 0 {
			if x, y, _, ok := ParseLotToken(c.tok); !ok || x != c.x || y != c.y {
				t.Fatalf("the strict reader lost %q", c.tok)
			}
		} else if _, _, _, ok := ParseLotToken(c.tok); ok {
			t.Fatalf("the strict reader (for the dense grid) accepted the negative token %q", c.tok)
		}
	}
	if x, y, rot, ok := ParseLotTokenAny("m2-4-r"); !ok || !rot || x != -2 || y != 4 {
		t.Fatalf("a rotated negative token: %d %d %v %v", x, y, rot, ok)
	}
	for _, bad := range []string{"", "m0-1", "m-1-2", "a-b", "1", "1-2-3x", "-1-2", "m-3"} {
		if _, _, _, ok := ParseLotTokenAny(bad); ok {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

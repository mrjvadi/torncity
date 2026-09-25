package screens

import "testing"

func TestJalali(t *testing.T) {
	for _, c := range []struct{ gy, gm, gd, jy, jm, jd int }{
		{2026, 9, 23, 1405, 7, 1},
		{2026, 3, 21, 1405, 1, 1},
		{2025, 3, 20, 1403, 12, 30},
		{2024, 12, 31, 1403, 10, 11},
		{2026, 9, 25, 1405, 7, 3},
	} {
		if y, m, d := jalali(c.gy, c.gm, c.gd); y != c.jy || m != c.jm || d != c.jd {
			t.Errorf("jalali(%d-%d-%d) = %d/%d/%d, want %d/%d/%d", c.gy, c.gm, c.gd, y, m, d, c.jy, c.jm, c.jd)
		}
	}
}

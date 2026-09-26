package item

import (
	"errors"
	"testing"
)

func TestObsolescenceFactorBPS(t *testing.T) {
	t.Run("newest version is full value", func(t *testing.T) {
		f, err := ObsolescenceFactorBPS(5, 5, 1000, 2000)
		mustNil(t, err)
		if f != BPS {
			t.Fatalf("factor = %d, want %d", f, BPS)
		}
	})

	t.Run("declines with generations behind and floors", func(t *testing.T) {
		f1, err := ObsolescenceFactorBPS(4, 5, 1000, 2000)
		mustNil(t, err)
		if f1 != BPS-1000 {
			t.Fatalf("one behind: factor = %d, want %d", f1, BPS-1000)
		}
		f2, err := ObsolescenceFactorBPS(1, 10, 2000, 2000)
		mustNil(t, err)
		if f2 != 2000 {
			t.Fatalf("far behind: factor = %d, want the floor 2000", f2)
		}
	})

	t.Run("monotonic: never worth more the further behind", func(t *testing.T) {
		var prev int64 = BPS + 1
		for v := int64(10); v >= 1; v-- {
			f, err := ObsolescenceFactorBPS(v, 10, 300, 1500)
			mustNil(t, err)
			if f > prev {
				t.Fatalf("version %d factor %d exceeds the more-recent version's %d", v, f, prev)
			}
			if f < 1500 || f > BPS {
				t.Fatalf("version %d factor %d out of bounds", v, f)
			}
			prev = f
		}
	})

	t.Run("rejects invalid input", func(t *testing.T) {
		cases := []struct {
			v, latest, perGen, floor int64
		}{
			{0, 5, 100, 100},
			{6, 5, 100, 100},
			{1, 5, -1, 100},
			{1, 5, BPS + 1, 100},
			{1, 5, 100, -1},
			{1, 5, 100, BPS + 1},
		}
		for _, c := range cases {
			if _, err := ObsolescenceFactorBPS(c.v, c.latest, c.perGen, c.floor); !errors.Is(err, ErrInvalidObsolescence) {
				t.Fatalf("%+v: err = %v, want ErrInvalidObsolescence", c, err)
			}
		}
	})
}

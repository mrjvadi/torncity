package worldgen

import "math"

// quantize turns a float64 into the single fixed-point integer decision
// point downstream code reads. It is the one conversion the package doc
// describes: everything before it is a continuous float64 computation built
// only from portable IEEE-754 operations, everything after it is an exact
// integer comparison. math.Floor(x+0.5) is used instead of math.Round so the
// rounding rule itself (ties rounded up, not to even and not away from zero)
// is a single documented line trivial to match in another language, rather
// than deferring to a standard library's own tie-breaking convention.
func quantize(x float64) int32 {
	return int32(math.Floor(x + 0.5))
}

// pseudoAngle returns a value in [0,4) that increases monotonically with the
// true counter-clockwise angle of (x,y) around the origin (as seen from
// +z, the same convention atan2 uses) without calling atan2 or any other
// transcendental function — only division, absolute value and comparison,
// every one of them exact and portable. It exists so that "sort these cells
// by longitude" (climate.go's wind sweep) is a decision this package can
// promise is bit-for-bit reproducible, which an atan2-based angle could not
// promise across two different math libraries.
func pseudoAngle(x, y float64) float64 {
	ax, ay := math.Abs(x), math.Abs(y)
	var t float64
	if ax+ay == 0 {
		t = 0
	} else {
		t = ay / (ax + ay)
	}
	if x < 0 {
		t = 2 - t
	}
	if y < 0 {
		t = 4 - t
	}
	return t
}

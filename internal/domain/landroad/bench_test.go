package landroad

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/settlement"
)

// BenchmarkGroundInfo is the price of reading one fresh lot: it bounds how big
// a corridor the lot router can search inside one request.
func BenchmarkGroundInfo(b *testing.B) {
	t := &testing.T{}
	w := testWorld(t)
	cell := aLandCell(t, w)
	pt := w.Cells[cell].Point
	f := settlement.FrameOfGrid(w, pt.LatDeg, pt.LonDeg, 0, 0, 0, 5)
	g := NewWorldGround(w, f, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Info(Lot{X: i % 400, Y: i / 400})
	}
}

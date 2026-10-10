package handlers

import (
	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
)

// placedOf lists the complete buildings with their footprints, for the proximity rules of a placement (docs/adr/0067): a
// farm stands near its water work, a water mill near a qanat shaft.
func placedOf(snap *content.Snapshot, rows []application.SettlementBuildingInstance) []settlementbuilding.Placed {
	var out []settlementbuilding.Placed
	for _, b := range rows {
		if b.Status != "complete" {
			continue
		}
		fw, fh := 1, 1
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
			def := d.Def()
			if b.Rotated {
				def = def.Rotate()
			}
			fw, fh = def.FootprintW, def.FootprintH
		}
		out = append(out, settlementbuilding.Placed{Code: b.TypeCode, X: b.LotX, Y: b.LotY, W: fw, H: fh})
	}
	return out
}

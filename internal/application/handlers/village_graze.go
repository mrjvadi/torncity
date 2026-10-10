package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The pasture grazes open land (docs/adr/0067, ADR 0041 8.19): a herd needs lots round its pasture with no tree, no rock and
// no building on them, at least the farming block's pasture.grazing_lots of them within pasture.radius lots; with fewer the
// shift is refused (no_grazing), and the lots it grazes do not regrow trees. Clearing the land (the woodcutter, the quarry)
// is what makes room for a herd.

// grazingSites lists the standing grazing buildings of the settlement, for the lots they graze.
func (h *VillageHandler) grazingSites(snap *content.Snapshot, buildings []application.SettlementBuildingInstance) []application.LandSite {
	var out []application.LandSite
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || !d.Grazes {
			continue
		}
		def := d.Def()
		if b.Rotated {
			def = def.Rotate()
		}
		out = append(out, application.LandSite{X: b.LotX, Y: b.LotY, W: def.FootprintW, H: def.FootprintH})
	}
	return out
}

// grazedLots are the lots a herd grazes: they do not regrow trees.
func (h *VillageHandler) grazedLots(snap *content.Snapshot, buildings []application.SettlementBuildingInstance) map[land.Pos]bool {
	fd, ok := snap.Farming()
	if !ok {
		return nil
	}
	return application.GrazedLots(h.grazingSites(snap, buildings), fd.Pasture.Radius)
}

// openAround counts the lots within the radius of the footprint that a herd can graze: no tree, no rock, no building, not water.
func (h *VillageHandler) openAround(lv *application.LandView, x, y, w, ht, radius int, own map[land.Pos]bool) int {
	n := 0
	for yy := y - radius; yy < y+ht+radius; yy++ {
		for xx := x - radius; xx < x+w+radius; xx++ {
			p := land.Pos{X: xx, Y: yy}
			if own[p] {
				continue
			}
			if l, ok := lv.Lots[p]; ok && !l.Occupied && !l.Obstructed && !l.Ground.Water {
				n++
			}
		}
	}
	return n
}

// grazingLine is what a pasture has round it, for its panel; nil when the building does not graze or the land model is off.
func (h *VillageHandler) grazingLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef,
) (*village.GrazingLine, error) {
	fd, ok := snap.Farming()
	if !ok || !d.Grazes || !h.landRules.Enabled() {
		return nil, nil
	}
	w, err := h.world(ctx)
	if err != nil {
		return nil, err
	}
	open, err := tx.Citizens().OpenLots(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	lv, err := h.landViewOf(ctx, tx, w, s, open)
	if err != nil {
		return nil, err
	}
	def := d.Def()
	if b.Rotated {
		def = def.Rotate()
	}
	own := map[land.Pos]bool{}
	for _, q := range footprintOf(def, b.LotX, b.LotY) {
		own[land.Pos{X: q[0], Y: q[1]}] = true
	}
	return &village.GrazingLine{Open: h.openAround(lv, b.LotX, b.LotY, def.FootprintW, def.FootprintH, fd.Pasture.Radius, own),
		Need: fd.Pasture.GrazingLots, Radius: fd.Pasture.Radius}, nil
}

// grazingGate refuses a shift at a pasture that has too little open land round it.
func (h *VillageHandler) grazingGate(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef,
) error {
	if !d.Grazes {
		return nil
	}
	line, err := h.grazingLine(ctx, tx, snap, s, b, d)
	if err != nil || line == nil {
		return err
	}
	if line.Open < line.Need {
		return refuseVillage(village.PastureNoGrazing, village.AddrWork)
	}
	return nil
}

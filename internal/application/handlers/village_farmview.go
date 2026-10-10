package handlers

import (
	"context"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/farm"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// nodeExtras adds to the work block of a building what the farms, the water works, the mills and the pastures show
// (docs/adr/0067): the crop with its stage and factors, the water master and the farms served, the toll, the open land.
func (h *VillageHandler) nodeExtras(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, buildings []application.SettlementBuildingInstance,
	viewer string, w *village.WorkNode,
) error {
	var err error
	if w.Farm, err = h.farmLine(ctx, tx, snap, s, b, buildings, viewer); err != nil {
		return err
	}
	if w.Mill, err = h.millLine(ctx, tx, snap, s, d, viewer); err != nil {
		return err
	}
	if w.Grazing, err = h.grazingLine(ctx, tx, snap, s, b, d); err != nil {
		return err
	}
	if w.Water, err = h.waterWorkLine(ctx, tx, snap, s, b, d, buildings); err != nil {
		return err
	}
	if w.Farm != nil && !w.Farm.Legacy {
		switch farm.Stage(w.Farm.Stage) {
		case farm.StageIdle, farm.StageHarvested, farm.StageRotted:
			w.Reasons = append([]village.WorkReason{{Code: village.FarmIdle}}, w.Reasons...)
		case farm.StageGrowing:
			if w.Farm.Tended >= w.Farm.TendMax {
				w.Reasons = append([]village.WorkReason{{Code: village.FarmWaiting}}, w.Reasons...)
			}
		}
	}
	if w.Grazing != nil && w.Grazing.Open < w.Grazing.Need {
		w.Reasons = append([]village.WorkReason{{Code: village.PastureNoGrazing}}, w.Reasons...)
	}
	return nil
}

// waterWorkLine is the state of a water work: whether its master is on duty, its condition and the farms it serves.
func (h *VillageHandler) waterWorkLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, buildings []application.SettlementBuildingInstance,
) (*village.WaterWork, error) {
	fd, ok := snap.Farming()
	if !ok || b.Status != "complete" || d.Role != "water_infra" {
		return nil, nil
	}
	line := &village.WaterWork{ConditionBPS: labor.BPS - h.damageNow(b, h.decayOf(snap, d), h.now(), s.Zone())}
	open, err := h.workOpenToday(ctx, tx, snap, s, buildings, b.ID)
	if err != nil {
		return nil, err
	}
	line.Open = open
	for _, br := range fd.Branches {
		if br.Work != d.Code {
			continue
		}
		kit := farmKit{def: fd, cfg: fd.Cycle(), branch: br, base: br.Farm}
		works, farms := h.sitesOf(snap, kit, buildings)
		serving := farm.Serving(works, farms, kit.cfg.Reach, kit.cfg.Serves)
		for _, f := range farms {
			if serving[f.ID] == b.ID {
				for _, x := range buildings {
					if x.ID == f.ID {
						fdef, _ := snap.SettlementBuildingDef(x.TypeCode)
						line.Serves = append(line.Serves, named(fdef.Code, fdef.Name))
					}
				}
			}
		}
	}
	return line, nil
}

// decorateWorkplace adds the farm and the mill to a workplace line of the work screen.
func (h *VillageHandler) decorateWorkplace(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, buildings []application.SettlementBuildingInstance,
	viewer string, line *village.WorkplaceLine,
) error {
	var err error
	if line.Farm, err = h.farmLine(ctx, tx, snap, s, b, buildings, viewer); err != nil {
		return err
	}
	if line.Mill, err = h.millLine(ctx, tx, snap, s, d, viewer); err != nil {
		return err
	}
	if line.Farm != nil && !line.Farm.Legacy {
		switch farm.Stage(line.Farm.Stage) {
		case farm.StageIdle, farm.StageHarvested, farm.StageRotted:
			line.Ready = false
		}
	}
	return nil
}

// trimOwn strips the citizen suffix of a building code.
func trimOwn(code string) string { return strings.TrimSuffix(code, content.PrivateSuffix) }

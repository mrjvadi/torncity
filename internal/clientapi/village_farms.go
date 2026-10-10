package clientapi

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/farm"
	"github.com/mrjvadi/torncity/internal/domain/land"
)

// LayoutFarm is the crop of one farm in the layout: the stage the client draws on the farm's lots (bare, sown, green, ripe,
// stubble) is read from Stage and the times (docs/adr/0067). A farm that still works its flat shift is Legacy.
type LayoutFarm struct {
	Building string `json:"building"`
	// Stage is idle, sowing, growing, ripe, overripe, harvest, harvested or rotted.
	Stage   string `json:"stage"`
	Rainfed bool   `json:"rainfed,omitempty"`
	Legacy  bool   `json:"legacy,omitempty"`
	SowDone int    `json:"sow_done"`
	SowNeed int    `json:"sow_need"`
	Tended  int    `json:"tended"`
	TendMax int    `json:"tend_max"`
	// HarvestDone is the harvest shifts begun of HarvestNeed.
	HarvestDone int `json:"harvest_done"`
	HarvestNeed int `json:"harvest_need"`
	// RipeAt and SpoilAt, RFC 3339, are when the crop is ripe and when it starts to spoil.
	RipeAt  string `json:"ripe_at,omitempty"`
	SpoilAt string `json:"spoil_at,omitempty"`
}

// addFarms adds the crops of the farms to the layout.
func (v *VillageService) addFarms(ctx context.Context, out *VillageLayout, s application.FoundedSettlement, rows []application.SettlementBuildingInstance) error {
	if v.Farm == nil || !v.FarmRules.Enabled() {
		return nil
	}
	snap := v.Content.Current()
	fd, ok := snap.Farming()
	if !ok {
		return nil
	}
	cfg := fd.Cycle()
	now := time.Now().UTC()
	if v.Now != nil {
		now = v.Now().UTC()
	}
	cycles, err := v.Farm.OpenIn(ctx, s.CityID)
	if err != nil {
		return err
	}
	byBuilding := map[string]farm.Cycle{}
	for _, c := range cycles {
		byBuilding[c.BuildingID] = c
	}
	h := fnv.New64a()
	for _, b := range rows {
		if b.Status != "complete" {
			continue
		}
		br, ok := fd.BranchOf(strings.TrimSuffix(b.TypeCode, content.PrivateSuffix))
		if !ok {
			continue
		}
		f := LayoutFarm{Building: b.ID, Stage: string(farm.StageIdle), Rainfed: br.Rainfed, SowNeed: cfg.SowShifts, TendMax: cfg.TendMax, HarvestNeed: cfg.HarvestShifts}
		if !v.FarmRules.OnCycle(b, now) {
			f.Legacy = true
		} else if c, ok := byBuilding[b.ID]; ok {
			f.Stage = string(farm.StageAt(&c, cfg, now))
			f.SowDone, f.Tended, f.HarvestDone = c.SowStarted, c.Tended, c.HarvestStarted
			if t := c.RipeAt(cfg); !t.IsZero() {
				f.RipeAt, f.SpoilAt = t.UTC().Format(time.RFC3339), c.SpoilAt(cfg).UTC().Format(time.RFC3339)
			}
		}
		out.Farms = append(out.Farms, f)
		fmt.Fprintf(h, "%s,%s,%t,%d,%d,%d;", f.Building, f.Stage, f.Legacy, f.SowDone, f.Tended, f.HarvestDone)
	}
	if len(out.Farms) > 0 {
		out.farmMark = fmt.Sprintf("%010x", h.Sum64()&0xffffffffff)
	}
	return nil
}

// grazedBy are the lots a herd grazes (they do not regrow trees).
func (v *VillageService) grazedBy(rows []application.SettlementBuildingInstance) map[land.Pos]bool {
	snap := v.Content.Current()
	fd, ok := snap.Farming()
	if !ok {
		return nil
	}
	var sites []application.LandSite
	for _, b := range rows {
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
		sites = append(sites, application.LandSite{X: b.LotX, Y: b.LotY, W: def.FootprintW, H: def.FootprintH})
	}
	return application.GrazedLots(sites, fd.Pasture.Radius)
}

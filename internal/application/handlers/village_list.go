package handlers

import (
	"context"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds K2/W5's three read-only list screens: the knowledge
// list, the build menu and the construction progress queue.

// KnowledgeList handles settlement.knowledge.
func (h *VillageHandler) KnowledgeList(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view screens.KnowledgeListView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		cell := w.Cells[s.WorldCellID]
		terrain := terrainTagsFor(w, s)

		running, err := tx.SettlementKnowledge().RunningResearch(ctx, s.CityID)
		if err != nil {
			return err
		}
		st, _, err := h.knowledgeStanding(ctx, tx, snap, s, terrain, running != nil)
		if err != nil {
			return err
		}
		treasury, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}

		tree := snap.SettlementKnowledgeTree()
		view = screens.KnowledgeListView{Name: s.Name, Treasury: treasury, LiteracyPercent: st.LiteracyShareBPS / 100}
		if running != nil {
			d, _ := snap.SettlementKnowledgeDef(running.Code)
			view.Running = &screens.KnowledgeResearchLine{Knowledge: named(d.Code, d.Name), FinishAt: running.FinishAt,
				Left: countdownTo(running.FinishAt, h.now())}
		}
		for _, code := range sortedKnowledgeCodes(snap) {
			d, _ := snap.SettlementKnowledgeDef(code)
			if !d.IsModeEligible() {
				continue // never offered for research, purchase or a license (ADR 0031 section 4.3)
			}
			t := tree[code]
			line := screens.KnowledgeLine{Knowledge: named(d.Code, d.Name)}
			switch {
			case st.Owned.Has(code):
				line.State = screens.KnowledgeHeld
			case running != nil && running.Code == code:
				line.State = screens.KnowledgeResearching
			default:
				line.ResearchCost, line.ResearchTime = d.Cost, h.scale.RealWait(t.Time)
				if !d.Restricted {
					price, err := h.scarcityPrice(ctx, tx, d.Cost, code)
					if err != nil {
						return err
					}
					line.BuyPrice = price
				}
				err := settlementknowledge.CanAcquire(t, tree, st, true)
				line.TerrainOK = t.TerrainMode != settlementknowledge.TerrainRequired || st.Discounted(t) || hasAny(terrain, t.TerrainTags)
				if err == nil {
					line.State = screens.KnowledgeAvailable
				} else {
					line.State = screens.KnowledgeLocked
					line.Missing = missingNamed(snap, st.Missing(t, tree))
				}
			}
			view.Lines = append(view.Lines, line)
		}
		_ = cell
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.KnowledgeList(h.screen(meta, lang), view), nil
}

// BuildMenu handles settlement.build.
func (h *VillageHandler) BuildMenu(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view screens.BuildMenuView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		treasury, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		running, err := tx.SettlementBuildings().RunningCount(ctx, s.CityID)
		if err != nil {
			return err
		}
		st, capabilities, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
		if err != nil {
			return err
		}
		built, err := builtRoleCounts(ctx, tx, snap, s.CityID)
		if err != nil {
			return err
		}

		view = screens.BuildMenuView{Name: s.Name, Treasury: treasury, RunningBuilds: running,
			ConcurrentCap: h.concurrentBuildCap[s.Tier]}
		for _, code := range sortedBuildingCodes(snap) {
			d, _ := snap.SettlementBuildingDef(code)
			if d.Private() {
				continue // a resident's building: the citizen catalogue lists it
			}
			def := d.Def()
			line := screens.BuildLine{Building: named(d.Code, d.Name), Role: d.Role, CostMoney: d.CostMoney, BuildTime: h.scale.RealWait(def.BuildTime)}
			ok := true
			for _, k := range def.RequiresKnowledge {
				if !st.Owned.Has(k) {
					ok = false
					line.Missing = append(line.Missing, named(k, k))
				}
			}
			for _, cp := range def.RequiresKnowledgeCapability {
				if !capabilities.Has(cp) {
					ok = false
				}
			}
			if def.RequiresBuildingRole != nil && built[*def.RequiresBuildingRole] < 1 {
				ok = false
			}
			if def.MinLiteracyShareBPS > 0 && st.LiteracyShareBPS < def.MinLiteracyShareBPS {
				ok = false
			}
			if ok {
				line.State = screens.BuildAvailable
			} else {
				line.State = screens.BuildLocked
			}
			view.Lines = append(view.Lines, line)
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.BuildMenu(h.screen(meta, lang), view), nil
}

// Progress handles settlement.build.progress.
func (h *VillageHandler) Progress(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ConstructionProgressView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		view = screens.ConstructionProgressView{Name: s.Name}
		now := h.now()
		for _, b := range buildings {
			if b.Status != "building" {
				continue
			}
			d, _ := snap.SettlementBuildingDef(b.TypeCode)
			// Construction's own finish time is not stored on the row
			// (it lives on the scheduled game_action); the screen shows
			// the content's own build time counted from queued_at, which
			// is exactly what the action was scheduled for.
			finish := b.QueuedAt.Add(h.scale.RealWait(d.Def().BuildTime))
			view.Lines = append(view.Lines, screens.ConstructionLine{Building: named(d.Code, d.Name), LotX: b.LotX, LotY: b.LotY,
				State: screens.ConstructionBuilding, FinishAt: finish, Left: countdownTo(finish, now)})
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.ConstructionProgress(h.screen(meta, lang), view), nil
}

// --- shared small helpers -------------------------------------------------
// named (crime.go) already builds a screens.Named from a code and an
// authored name; K2/W5 reuses it verbatim.

func missingNamed(snap *content.Snapshot, codes []string) []screens.Named {
	out := make([]screens.Named, 0, len(codes))
	for _, c := range codes {
		d, ok := snap.SettlementKnowledgeDef(c)
		if ok {
			out = append(out, named(d.Code, d.Name))
		} else {
			out = append(out, named(c, c))
		}
	}
	return out
}

func hasAny(have, want []string) bool {
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return true
			}
		}
	}
	return false
}

func sortedKnowledgeCodes(snap *content.Snapshot) []string {
	defs := snap.SettlementKnowledgeDefs()
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Code
	}
	sort.Strings(out)
	return out
}

func sortedBuildingCodes(snap *content.Snapshot) []string {
	defs := snap.SettlementBuildingDefs()
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.Code
	}
	sort.Strings(out)
	return out
}

// terrainTagsFor is the settlement's own terrain, for a knowledge item's
// TerrainRequired/TerrainPreferred check: its founding cell's biome plus
// whatever a lot of its grid carries (any river/coastal/sloped/ore tag
// anywhere on the grid counts for the settlement as a whole — a knowledge
// item is settlement-wide, unlike a building's own footprint).
func terrainTagsFor(w interface {
	BiomeCode(cellID int32) string
}, s application.FoundedSettlement,
) []string {
	tags := []string{w.BiomeCode(s.WorldCellID)}
	return tags
}

// builtRoleCounts counts how many COMPLETE buildings a settlement has at
// each role/tier — settlementbuilding.Standing.Built's own input, and a
// tier-2 promotion's requires_building_role check.
func builtRoleCounts(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string,
) (map[settlementbuilding.RoleTier]int, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	out := map[settlementbuilding.RoleTier]int{}
	for _, b := range buildings {
		if !b.Complete() || b.Status == "demolished" {
			continue
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || d.Role == "" {
			continue
		}
		out[settlementbuilding.RoleTier{Role: d.Role, Tier: d.Tier}]++
	}
	return out, nil
}

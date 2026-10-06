package handlers

import (
	"context"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// This file holds K2/W5's three read-only list screens: the knowledge
// list, the build menu and the construction progress queue.

// KnowledgeList handles settlement.knowledge.
func (h *VillageHandler) KnowledgeList(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.KnowledgeListView
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
		view = village.KnowledgeListView{Name: s.Name, Treasury: treasury, LiteracyPercent: st.LiteracyShareBPS / 100}
		if running != nil {
			d, _ := snap.SettlementKnowledgeDef(running.Code)
			view.Running = &village.KnowledgeResearchLine{Knowledge: named(d.Code, d.Name), FinishAt: running.FinishAt,
				Left: countdownTo(running.FinishAt, h.now())}
		}
		for _, code := range sortedKnowledgeCodes(snap) {
			d, _ := snap.SettlementKnowledgeDef(code)
			if !d.IsModeEligible() {
				continue // never offered for research, purchase or a license (ADR 0031 section 4.3)
			}
			t := tree[code]
			line := village.KnowledgeLine{Knowledge: named(d.Code, d.Name)}
			switch {
			case st.Owned.Has(code):
				line.State = village.KnowledgeHeld
			case running != nil && running.Code == code:
				line.State = village.KnowledgeResearching
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
					line.State = village.KnowledgeAvailable
				} else {
					line.State = village.KnowledgeLocked
					line.Missing = missingNamed(snap, st.Missing(t, tree))
				}
			}
			if line.State == village.KnowledgeLocked && (len(line.Missing) > 0 || !line.TerrainOK) {
				// one step away only (ADR 0033 section 5): what needs unowned knowledge is not listed, and what this
				// land can never allow is never revealed (section 5.2)
				continue
			}
			line.Unlocks = knowledgeUnlocks(snap, d)
			view.Lines = append(view.Lines, line)
		}
		_ = cell
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.KnowledgeList(h.screen(meta, lang), view), nil
}

// BuildMenu handles settlement.build.
func (h *VillageHandler) BuildMenu(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.BuildMenuView
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
		running, err := h.runningJobs(ctx, tx, snap, s.CityID)
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

		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
		if err != nil {
			return err
		}
		pc := pathContext{snap: snap, tier: s.Tier, owned: st.Owned, caps: capabilities, standing: standingCodes(buildings), stock: stock.Units, markup: h.materialMarkupBPS}

		capNow, cerr := h.buildCapOf(ctx, tx, snap, s, buildings)
		if cerr != nil {
			return cerr
		}
		view = village.BuildMenuView{Name: s.Name, Treasury: treasury, RunningBuilds: running,
			ConcurrentCap: capNow}
		waitOf, werr := h.buildWaiter(ctx, tx, snap, s)
		if werr != nil {
			return werr
		}
		gg := buildingGate() // what the settlement has lists the buildings (the size label lists none)
		var gcaps wsettle.Capabilities
		var gfound bool
		if gg != nil {
			if gcaps, gfound, err = gg.InTx(ctx, tx, snap, s.CityID); err != nil {
				return err
			}
		}
		for _, code := range sortedBuildingCodes(snap) {
			d, _ := snap.SettlementBuildingDef(code)
			if d.Private() {
				continue // a resident's building: the citizen catalogue lists it
			}
			def := d.Def()
			// Progressive disclosure (ADR 0033 section 5): a building of a bigger
			// settlement, or one whose knowledge the village does not hold, is not
			// listed at all; what is listed is what the village can start or is one
			// step from.
			listed := pc.listed(d)
			if gg != nil {
				listed = gg.DecideBuilding("build_menu", s.CityID, snap, gcaps, gfound, d.Code, listed)
			}
			if !listed {
				continue
			}
			line := village.BuildLine{ExpectedWait: waitOf(def), Building: named(d.Code, d.Name), Role: d.Role, Category: snap.BuildCategoryOf(d), CostMoney: d.CostMoney, BuildTime: h.scale.RealWait(def.BuildTime),
				Materials: materialLinesOf(snap, def.CostMaterials)}
			ok := true
			if def.RequiresBuildingRole != nil && built[*def.RequiresBuildingRole] < 1 {
				ok = false
				line.MissingBuildings = pc.buildingsOfRole(*def.RequiresBuildingRole)
			}
			if def.MinLiteracyShareBPS > 0 && st.LiteracyShareBPS < def.MinLiteracyShareBPS {
				ok = false
			}
			if ok {
				line.State = village.BuildAvailable
				for _, n := range pc.materialNeeds(def.CostMaterials) {
					line.Short = append(line.Short, village.MaterialLine{Component: n.Item, Quantity: n.Need - n.Have})
				}
			} else {
				line.State = village.BuildLocked
			}
			view.Lines = append(view.Lines, line)
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.BuildMenu(h.screen(meta, lang), view), nil
}

// Progress handles settlement.build.progress.
func (h *VillageHandler) Progress(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.ConstructionProgressView
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
		view = village.ConstructionProgressView{Name: s.Name}
		now := h.now()
		for _, b := range buildings {
			if b.Status == "complete" && b.TypeCode != "road" {
				d, _ := snap.SettlementBuildingDef(b.TypeCode)
				view.Standing = append(view.Standing, village.StandingLine{ID: b.ID, Building: named(d.Code, d.Name), LotX: b.LotX, LotY: b.LotY})
			}
			if b.Status != "building" {
				continue
			}
			d, _ := snap.SettlementBuildingDef(b.TypeCode)
			// Construction's own finish time is not stored on the row
			// (it lives on the scheduled game_action); the screen shows
			// the content's own build time counted from queued_at, which
			// is exactly what the action was scheduled for.
			if b.ByWork() {
				view.Lines = append(view.Lines, village.ConstructionLine{Building: named(d.Code, d.Name), LotX: b.LotX, LotY: b.LotY,
					State: village.ConstructionBuilding, ID: b.ID, ByWork: true, ProgressBPS: labor.ProgressBPS(b.WorkDone, b.WorkRequired),
					DoneMinutes: b.WorkDone, RequiredMinutes: b.WorkRequired, LeftMinutes: b.WorkRequired - b.WorkDone})
				continue
			}
			finish := b.QueuedAt.Add(h.scale.RealWait(d.Def().BuildTime))
			view.Lines = append(view.Lines, village.ConstructionLine{ID: b.ID, Building: named(d.Code, d.Name), LotX: b.LotX, LotY: b.LotY,
				State: village.ConstructionBuilding, FinishAt: finish, Left: countdownTo(finish, now)})
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.ConstructionProgress(h.screen(meta, lang), view), nil
}

// --- shared small helpers -------------------------------------------------
// named (crime.go) already builds a presentation.Named from a code and an
// authored name; K2/W5 reuses it verbatim.

func missingNamed(snap *content.Snapshot, codes []string) []presentation.Named {
	out := make([]presentation.Named, 0, len(codes))
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

// knowledgeUnlocks is what holding a knowledge item opens: the buildings that name it (by code or by a capability it
// provides) as a requirement, the knowledge that names it, and the courses whose availability tag needs it. It reads the
// content only; nothing here is a rule.
func knowledgeUnlocks(snap *content.Snapshot, d content.SettlementKnowledgeDef) []village.KnowledgeUnlock {
	provides := d.Provides
	if len(provides) == 0 {
		provides = []string{d.Code}
	}
	has := func(list []string, code string) bool {
		for _, c := range list {
			if c == code {
				return true
			}
		}
		return false
	}
	var out []village.KnowledgeUnlock
	for _, code := range sortedBuildingCodes(snap) {
		b, _ := snap.SettlementBuildingDef(code)
		if b.Private() {
			continue
		}
		open := has(b.RequiresKnowledge, d.Code)
		for _, c := range b.RequiresKnowledgeCapability {
			open = open || has(provides, c)
		}
		if open {
			out = append(out, village.KnowledgeUnlock{Kind: village.UnlockBuilding, Item: named(b.Code, b.Name)})
		}
	}
	for _, k := range snap.SettlementKnowledgeDefs() {
		open := has(k.Requires, d.Code)
		for _, c := range k.RequiresCapability {
			open = open || has(provides, c)
		}
		if open && k.IsModeEligible() {
			out = append(out, village.KnowledgeUnlock{Kind: village.UnlockKnowledge, Item: named(k.Code, k.Name)})
		}
	}
	for _, c := range snap.Courses() {
		if tag, ok := snap.AvailabilityTag("course", c.Code); ok && tag.Requires != nil && has(tag.Requires.Knowledge, d.Code) {
			out = append(out, village.KnowledgeUnlock{Kind: village.UnlockCourse, Item: named(c.Code, c.Name)})
		}
	}
	return out
}

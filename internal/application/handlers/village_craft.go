package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/craft"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Crafting at home (docs/adr/0068, ADR 0045 4.1). A citizen makes goods from his own store at a home station: his dwelling, or a
// building of his with a module that stands for a workshop (a bench, a forge, a loom, a kiln, an oven, a quern).
//
//   - WHO WORKS THERE. The owner himself; the craft is a timed job on the game clock, up to crafting.max_jobs at once, so he can
//     go on with other work while the bread bakes. His trade's experience grows with each batch.
//   - WHAT IT CONSUMES. The recipe's inputs out of his home store when the job starts, and a share of a tool (the home store's
//     best tool of the tier the recipe needs; none: the bare-handed share).
//   - WHAT IT PROVIDES. The made goods, at crafting.home_yield_bps of the workshop's yield (a small bench loses a tenth),
//     into his home store when the job ends, as far as it has room; no room at the start, no craft.
//   - LINKS. Researched recipe (the settlement's knowledge) -> station (module) -> goods -> the workshops and the market.
//   - A hired hand at a home station is not built: the owner works it himself (ADR 0068 open item).

// stationsOfBuilding are the stations a building of a citizen stands for: its own function, and the stations the modules inside it
// stand for.
func stationsOfBuilding(snap *content.Snapshot, typeCode string, f *application.BuildingFunction) []string {
	set := map[string]bool{stationOf(snap, typeCode): true}
	if f != nil {
		set[strings.TrimSuffix(f.Function, content.PrivateSuffix)] = true
		for m, n := range f.Modules {
			if n < 1 {
				continue
			}
			if md, ok := snap.ModuleKind(m); ok {
				for _, st := range md.StandsFor {
					set[st] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for st := range set {
		out = append(out, st)
	}
	sort.Strings(out)
	return out
}

// homeRecipes are the recipes a home station of those stations makes (not the waiting ones).
func homeRecipes(snap *content.Snapshot, stations []string) ([]content.RecipeDef, map[string]string) {
	seen := map[string]bool{}
	at := map[string]string{} // recipe -> the first of the stations that makes it
	var out []content.RecipeDef
	for _, st := range stations {
		for _, code := range snap.RecipesAt(st) {
			r, ok := snap.Recipe(code)
			if !ok || r.WaitsFor != "" || seen[code] {
				continue
			}
			seen[code] = true
			at[code] = st
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, at
}

// craftLine is the home station of a lot: nil when the building makes nothing at home.
func (h *VillageHandler) craftLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, f *application.BuildingFunction, playerID string,
) (*village.LotCraftLine, error) {
	cd, _, ok := h.craftKit(snap)
	if !ok || b.Status != "complete" {
		return nil, nil
	}
	stations := stationsOfBuilding(snap, b.TypeCode, f)
	recipes, stationOf := homeRecipes(snap, stations)
	if len(recipes) == 0 {
		return nil, nil
	}
	owned, err := h.ownedKnowledge(ctx, tx, s.CityID)
	if err != nil {
		return nil, err
	}
	_, stacks, _, err := homeUsed(ctx, tx, snap, playerID)
	if err != nil {
		return nil, err
	}
	have := map[string]int64{}
	for _, st := range stacks {
		have[st.Item] += st.Qty
	}
	line := &village.LotCraftLine{Stations: stations, MaxJobs: cd.MaxJobs, MaxBatches: cd.MaxBatches, YieldBPS: int64(cd.HomeYieldBPS)}
	wanted := map[string]bool{}
	for _, r := range recipes {
		rl := village.StationRecipeLine{Code: r.Code, Name: named(r.Code, r.Name), Minutes: r.CycleMinutes(), Station: stationOf[r.Code],
			Inputs: materialLinesOf(snap, scaleQty(r.Inputs, 1)), Outputs: materialLinesOf(snap, scaleQty(r.Outputs, 1))}
		miss := missingKnowledge(r, owned)
		rl.Available = len(miss) == 0
		for _, k := range miss {
			kd, _ := snap.SettlementKnowledgeDef(k)
			rl.Missing = append(rl.Missing, named(k, kd.Name))
		}
		for it := range r.Inputs {
			wanted[it] = true
		}
		line.Recipes = append(line.Recipes, rl)
	}
	for _, it := range materialCodes(map[string]int64(boolSet(wanted))) {
		line.Have = append(line.Have, materialLineOf(snap, it, have[it]))
	}
	jobs, err := tx.Craft().Running(ctx, playerID)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j.BuildingID == b.ID {
			line.Jobs = append(line.Jobs, h.craftJobLine(snap, b, j))
		}
	}
	return line, nil
}

func boolSet(m map[string]bool) map[string]int64 {
	out := map[string]int64{}
	for k := range m {
		out[k] = 1
	}
	return out
}

func (h *VillageHandler) craftJobLine(snap *content.Snapshot, b application.SettlementBuildingInstance, j application.CraftJob) village.CraftJobLine {
	bd, _ := snap.SettlementBuildingDef(b.TypeCode)
	r, _ := snap.Recipe(j.Recipe)
	return village.CraftJobLine{ID: j.ID, Building: named(b.TypeCode, bd.Name), Recipe: named(j.Recipe, r.Name), Batches: j.Batches,
		FinishAt: j.FinishAt, Left: countdownTo(j.FinishAt, h.now()), Planned: materialLinesOf(snap, j.Planned)}
}

// VillageCraftRequest names the building to craft at, the recipe and the batches.
type VillageCraftRequest struct {
	Building string `json:"building"`
	Recipe   string `json:"recipe"`
	Batches  string `json:"batches,omitempty"`
}

// Craft handles settlement.craft: the owner starts a timed craft at a home station of his.
func (h *VillageHandler) Craft(ctx context.Context, meta envelope.Metadata, req VillageCraftRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.CraftStartedView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		cd, ladder, ok := h.craftKit(snap)
		if !ok {
			return refuseVillage(village.VillageNotAvailable, village.AddrWork)
		}
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.Building))
		if isSentinel(err, application.ErrBuildingNotFound) || (err == nil && b.SettlementID != s.CityID) {
			return refuseVillage(village.VillageNotFound, village.AddrWork)
		}
		if err != nil {
			return err
		}
		owner, err := h.privateOwnerOf(ctx, tx, b.ID)
		if err != nil {
			return err
		}
		if owner != p.ID || b.Status != "complete" {
			return refuseVillage(village.LotNotYours, village.AddrWork)
		}
		var f *application.BuildingFunction
		if fns, ferr := tx.SettlementBuildings().FunctionsOfBuildings(ctx, application.BuildingRefSettlement, []string{b.ID}); ferr != nil {
			return ferr
		} else if len(fns) > 0 {
			f = &fns[0]
		}
		stations := stationsOfBuilding(snap, b.TypeCode, f)
		r, ok := snap.Recipe(strings.TrimSpace(req.Recipe))
		here := false
		for _, st := range stations {
			here = here || (ok && recipeAt(r, st))
		}
		if !ok || r.WaitsFor != "" || !here {
			return refuseVillage(village.CraftNoStation, village.AddrWork)
		}
		owned, err := h.ownedKnowledge(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		if miss := missingKnowledge(r, owned); len(miss) > 0 {
			bs, err := tx.SettlementBuildings().List(ctx, s.CityID)
			if err != nil {
				return err
			}
			stock, err := h.stockOf(ctx, tx, snap, s.CityID, bs)
			if err != nil {
				return err
			}
			pc, err := h.pathFor(ctx, tx, snap, s, bs, stock)
			if err != nil {
				return err
			}
			return needsRefusal(village.VillagePrerequisite, village.NeedsForWork, named(r.Code, r.Name), pc.knowledgeNeeds(miss), village.AddrWork)
		}
		n, perr := strconv.Atoi(strings.TrimSpace(req.Batches))
		if strings.TrimSpace(req.Batches) == "" {
			n, perr = 1, nil
		}
		if perr != nil || n < 1 || n > cd.MaxBatches {
			return refuseVillage(village.CraftBatches, village.AddrWork)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		if err := tx.Craft().Lock(ctx, p.ID); err != nil {
			return err
		}
		running, err := tx.Craft().Running(ctx, p.ID)
		if err != nil {
			return err
		}
		if len(running) >= cd.MaxJobs {
			return refuseVillage(village.CraftTooMany, village.AddrWork)
		}
		st, err := h.loadHomeStock(ctx, tx, snap, s, p.ID)
		if err != nil {
			return err
		}
		consumed, planned := map[string]int64{}, map[string]int64{}
		for it, q := range r.Inputs {
			consumed[it] = int64(q) * int64(n)
		}
		for it, q := range r.Outputs {
			planned[it] = int64(q) * int64(n)
		}
		for _, it := range materialCodes(consumed) {
			if st.units[it] < consumed[it] {
				return needsRefusal(village.VillageMaterials, village.NeedsForWork, named(r.Code, r.Name),
					[]village.VillageNeed{{Kind: village.NeedMaterial, Item: componentNamed(snap, it), Have: st.units[it], Need: consumed[it]}}, village.AddrWork)
			}
		}
		// the room: the made goods must fit when the job ends, with what the jobs running will take
		reserved := int64(0)
		for _, j := range running {
			reserved += netGrowth(snap, j.Planned, j.Consumed)
		}
		if grow := netGrowth(snap, planned, consumed); grow > st.free()-reserved {
			r2 := refuseVillage(village.CraftNoRoom, village.AddrWork)
			r2.missing = grow - (st.free() - reserved)
			return r2
		}
		// the tool: a recipe of a trade wears a share of the best fitting tool in the home store
		now := h.now()
		factor, toolItem := int64(labor.BPS), ""
		if r.Skill != "" {
			avail := map[string]int64{}
			for k, v := range st.units {
				avail[k] = v - consumed[k]
			}
			carry, cerr := tx.SettlementTreasury().Carry(ctx, b.ID)
			if cerr != nil && !stderrors.Is(cerr, application.ErrBuildingNotFound) {
				return cerr
			}
			if carry == nil {
				carry = map[string]int64{}
			}
			need := r.ToolTier
			wear := int64(cd.HomeToolWearBPS) * int64(n)
			item, tier, got := ladder.Pick(avail, need)
			if got {
				wear = ladder.Wear(wear, tier, need)
			}
			carry[ToolWearKey] += wear
			if carry[ToolWearKey] >= labor.BPS {
				if got {
					toolItem = item
					consumed[item]++
					carry[ToolWearKey] -= labor.BPS
				} else {
					carry[ToolWearKey] = labor.BPS
				}
			}
			if best, any := ladder.Best(avail); any {
				factor = ladder.Factor(best, need)
			} else if h.realItems.BareHandsBPS > 0 {
				factor = h.realItems.BareHandsBPS
			}
			if err := tx.SettlementTreasury().SetCarry(ctx, b.ID, carry); err != nil && !stderrors.Is(err, application.ErrBuildingNotFound) {
				return err
			}
		}
		outputBPS := int64(cd.HomeYieldBPS) * factor / labor.BPS
		jobID := h.ids.NewID()
		for _, it := range materialCodes(consumed) {
			if err := tx.Items().Move(ctx, application.ItemMove{
				Item: it, Qty: consumed[it], From: p.ID, FromHolding: application.HoldHome,
				Reason: application.ItemProductionInput, ReferenceType: application.CraftJobItemReference, ReferenceID: jobID, At: now,
			}); err != nil {
				if stderrors.Is(err, application.ErrNotEnoughItems) {
					return refuseVillage(village.CraftNoInputs, village.AddrWork)
				}
				return err
			}
		}
		finish := now.Add(time.Duration(r.CycleMinutes()*n) * time.Minute)
		actionID, err := h.schedule(ctx, tx, application.CraftDoneActionType, application.CraftJobItemReference, jobID, s.CityID, now, finish)
		if err != nil {
			return err
		}
		job := application.CraftJob{ID: jobID, SettlementID: s.CityID, PlayerID: p.ID, BuildingID: b.ID, Recipe: r.Code, Batches: n,
			Consumed: consumed, Planned: planned, OutputBPS: int(max(outputBPS, 1)), ToolItem: toolItem, Status: application.CraftJobWorking,
			GameActionID: actionID, StartedAt: now, FinishAt: finish}
		if err := tx.Craft().Start(ctx, job); err != nil {
			return err
		}
		view = village.CraftStartedView{Village: s.Name, Job: h.craftJobLine(snap, *b, job)}
		return nil
	})
	if resp, ferr := h.villageFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return village.CraftStarted(h.screen(meta, lang), view), nil
}

// CraftDone handles settlement.craft.done from the SCHEDULER: a craft job ends, exactly once. The goods made (the yield of the
// job with the fraction carried by the station) come into the owner's home store as far as it has room; his trade learns.
func (h *VillageHandler) CraftDone(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		job, err := tx.Craft().Job(ctx, in.ID)
		if err != nil {
			return err
		}
		if job == nil || job.Status != application.CraftJobWorking || (req.ActionID != "" && job.GameActionID != req.ActionID) {
			return nil
		}
		now := h.now()
		if now.Before(job.FinishAt) {
			return stderrors.New("handlers: a craft job finished before its time")
		}
		s, err := tx.Settlements().ByID(ctx, job.SettlementID)
		if err != nil {
			return err
		}
		st, err := h.loadHomeStock(ctx, tx, snap, s, job.PlayerID)
		if err != nil {
			return err
		}
		room := st.free()
		carry, err := tx.SettlementTreasury().Carry(ctx, job.BuildingID)
		if err != nil && !stderrors.Is(err, application.ErrBuildingNotFound) {
			return err
		}
		if carry == nil {
			carry = map[string]int64{}
		}
		made := map[string]int64{}
		for _, it := range materialCodes(job.Planned) {
			whole, rest := craft.HomeYield(job.Planned[it], int64(job.OutputBPS), carry["craft_"+it])
			carry["craft_"+it] = rest
			bulk := max(snap.BulkOf(it), 1)
			if q := min(whole, max(room, 0)/bulk); q > 0 {
				made[it] = q
				room -= q * bulk
			}
		}
		fresh, err := tx.Craft().Finish(ctx, job.ID, made, now)
		if err != nil || !fresh {
			return err
		}
		if err := tx.SettlementTreasury().SetCarry(ctx, job.BuildingID, carry); err != nil && !stderrors.Is(err, application.ErrBuildingNotFound) {
			return err
		}
		for _, it := range materialCodes(made) {
			if err := tx.Items().Move(ctx, application.ItemMove{
				Item: it, Qty: made[it], To: job.PlayerID, ToHolding: application.HoldHome,
				Reason: application.ItemProduced, ReferenceType: application.CraftJobItemReference, ReferenceID: job.ID, At: now,
			}); err != nil {
				return err
			}
		}
		if r, ok := snap.Recipe(job.Recipe); ok && r.Skill != "" {
			if cd, _, ok := h.craftKit(snap); ok && cd.XPPerBatch > 0 {
				skills, err := tx.Skills().List(ctx, job.PlayerID)
				if err != nil {
					return err
				}
				if _, err := awardSkillXP(ctx, tx, snap, job.PlayerID, skills,
					[]skillAward{{Skill: player.SkillCode(r.Skill), XP: int64(cd.XPPerBatch * job.Batches)}}, now); err != nil {
					return err
				}
			}
		}
		return appendVillageEvent(ctx, tx, meta, "craft_done", s.CityID, map[string]any{
			"settlement_id": s.CityID, "job_id": job.ID, "player_id": job.PlayerID, "building_id": job.BuildingID, "recipe": job.Recipe, "made": made,
		})
	})
}

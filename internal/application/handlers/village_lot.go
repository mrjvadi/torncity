package handlers

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/domain/lookdesc"
	"github.com/mrjvadi/torncity/internal/domain/lotbuild"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// «مدیریت قطعهٔ من» - manage my lot (docs/adr/0045 phase B1, section 3; migration 0133).
//
// The lot owner chooses the FUNCTION of the building on his lot and what is INSIDE it (modules: bedrooms, a
// hearth, shelves ...); the game builds it and generates the LOOK. Nobody places a wall by hand (ADR 0028 6
// reconciliation, ADR 0044 5.5). Every act is an order:
//
//	WHO WORKS THERE. The builders of the hiring board (ADR 0037): the owner's own shifts, a neighbour, NPC
//	  labourers; the owner is the employer and pays the wages as the shifts are worked, with the village's levy.
//	  The settlement's crews are the concurrency: the orders draw on the same labour pool as everything else.
//	WHAT IT CONSUMES. Materials from the owner's home store (then what he carries), taken when the order is
//	  placed (no goods, no order); money to the builders' trade (the sink); the fee of a change of use (to the
//	  treasury, in the settlement's own money where it is chartered).
//	WHAT IT PRODUCES. A building that is more: bedrooms house people (the pool), storerooms and shelves add room to
//	  the owner's home store, a hearth makes a rest warm, a stall's shelves are counters of his own on the village
//	  book. The part the old catalogue already counted (the modules a level includes) is not counted twice.
//	HOW IT LINKS. Orders -> hiring board -> shifts -> the order is applied exactly once when its work is done;
//	  the stored look follows the contents.
//	OWNERSHIP AND MONEY. The owner of a private building; for a building of the settlement, the holder of
//	  public.build, paid from the treasury and the settlement's stock.

// LotRules is the tuning of the lot (config settlement.building_*, use_change_fee_bps).
type LotRules struct {
	Storey          lotbuild.StoreyRules
	StoreyKnowledge []lotbuild.StoreyKnowledge
	// SalvageBPS is the share of a removed module's materials that comes back.
	SalvageBPS int64
	// UseChangeFeeBPS is the fee of a change of use, basis points of the building's assessed value (at most 1000).
	UseChangeFeeBPS int64
	// LookRerolls bounds the re-rolls of a look to differ from a neighbour; TemplatesMax the saved layouts a player keeps.
	LookRerolls, TemplatesMax int
}

// Enabled reports whether the lot rules are configured.
func (r LotRules) Enabled() bool { return r.Storey.AreaPerCell > 0 }

// WithLotRules sets the lot's tuning; without it the manage-my-lot command is off.
func (h *VillageHandler) WithLotRules(r LotRules) *VillageHandler {
	h.lot = r
	return h
}

// VillageManageRequest is the payload of settlement.lot.manage.
type VillageManageRequest struct {
	// Building is the building to manage; empty: the first of mine (the menu when I have several).
	Building string `json:"building,omitempty"`
	Action   string `json:"action,omitempty"`
	// Code is a function, a module or a template (its id or share code) by the action; N a count; Name a template's name.
	Code    string `json:"code,omitempty"`
	N       string `json:"n,omitempty"`
	Name    string `json:"name,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	// LocalSettle asks to convert at the desk and pay the fee of a change of use in the settlement's own money.
	LocalSettle
}

func (r VillageManageRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == village.ResidenceConfirm
}

func (r VillageManageRequest) count() int {
	n, err := strconv.Atoi(strings.TrimSpace(r.N))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, 20)
}

// lotKit is the content of the lots resolved for the rules.
type lotKit struct {
	snap  *content.Snapshot
	defs  map[string]content.BuildingFunctionDef
	specs map[string]lotbuild.Spec
	mods  map[string]lotbuild.Module
	mdefs map[string]content.ModuleKindDef
}

func newLotKit(snap *content.Snapshot) lotKit {
	k := lotKit{snap: snap, defs: map[string]content.BuildingFunctionDef{}, specs: map[string]lotbuild.Spec{},
		mods: map[string]lotbuild.Module{}, mdefs: map[string]content.ModuleKindDef{}}
	for _, code := range snap.ModuleKindCodes() {
		m, _ := snap.ModuleKind(code)
		mod := lotbuild.Module{Code: code, Area: m.Area, Materials: m.CostMaterials, Shifts: m.BuildShifts, Provides: map[string]int64{}}
		for key, v := range m.Provides {
			mod.Provides[key] = int64(v)
		}
		k.mods[code], k.mdefs[code] = mod, m
	}
	for _, code := range snap.BuildingFunctionCodes() {
		f, _ := snap.BuildingFunction(code)
		sp := lotbuild.Spec{Code: code, MaxW: f.Footprint[2], MaxD: f.Footprint[3], Slots: map[string]int{}}
		for _, s := range f.Slots {
			sp.Slots[s.Module] = s.Max
		}
		for _, lv := range f.Levels {
			sp.Levels = append(sp.Levels, lotbuild.Level{Level: lv.Level, Adds: lv.Adds, Building: lv.Building, CostMoney: lv.CostMoney,
				Materials: lv.CostMaterials, Hours: lv.BuildHours})
		}
		k.defs[code], k.specs[code] = f, sp
	}
	return k
}

// managed reports whether a function can be chosen for a lot in B1: a function a resident can build (its first level
// stands as a catalogue building of the citizen catalogue).
func (k lotKit) managed(code string) bool {
	f, ok := k.defs[code]
	if !ok || len(f.Levels) == 0 || f.Levels[0].Building == "" {
		return false
	}
	d, ok := k.snap.SettlementBuildingDef(f.Levels[0].Building)
	return ok && d.Private()
}

func (k lotKit) levelOfType(fn, typeCode string) int {
	for _, l := range k.specs[fn].Levels {
		if l.Building == typeCode {
			return l.Level
		}
	}
	return 1
}

func (k lotKit) name(code string) presentation.Named {
	if f, ok := k.defs[code]; ok {
		return named(code, f.Name)
	}
	return named(code, code)
}

func (k lotKit) moduleName(code string) presentation.Named {
	if m, ok := k.mdefs[code]; ok {
		return named(code, m.Name)
	}
	return named(code, code)
}

// lotStanding is what the settlement has, for the gates.
type lotStanding struct {
	owned  item.Set
	caps   item.Set
	built  map[string]bool
	pc     pathContext
	maxSto int
}

func (h *VillageHandler) lotStanding(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, stock map[string]int64,
) (lotStanding, error) {
	st, caps, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return lotStanding{}, err
	}
	ls := lotStanding{owned: st.Owned, caps: caps, built: standingCodes(buildings)}
	ls.pc = pathContext{snap: snap, tier: s.Tier, owned: st.Owned, caps: caps, standing: ls.built, stock: stock, markup: h.materialMarkupBPS}
	ls.maxSto = lotbuild.MaxStoreys(h.lot.StoreyKnowledge, func(code string) bool { return st.Owned.Has(code) })
	return ls, nil
}

// unmet lists what a gate lacks (knowledge and standing buildings; the other kinds of gate belong to functions this
// phase does not offer, and count as unmet so nothing opens by mistake).
func (ls lotStanding) unmet(needs *content.AvailabilityNeeds, planned []string) []village.VillageNeed {
	var out []village.VillageNeed
	if needs != nil {
		var missing []string
		for _, kn := range needs.Knowledge {
			if !ls.owned.Has(kn) {
				missing = append(missing, kn)
			}
		}
		out = append(out, ls.pc.knowledgeNeeds(missing)...)
		for _, b := range needs.Buildings {
			switch {
			case b.Code != "" && !ls.built[b.Code]:
				if d, ok := ls.pc.snap.SettlementBuildingDef(b.Code); ok {
					out = append(out, village.VillageNeed{Kind: village.NeedBuilding, Item: named(d.Code, d.Name)})
				} else {
					out = append(out, village.VillageNeed{Kind: village.NeedBuilding, Item: named(b.Code, b.Code)})
				}
			case b.Code == "" && b.Role != "":
				have := false
				for code := range ls.built {
					if d, ok := ls.pc.snap.SettlementBuildingDef(code); ok && d.Role == b.Role && d.Tier >= b.Tier {
						have = true
					}
				}
				if !have {
					out = append(out, village.VillageNeed{Kind: village.NeedBuilding, Item: named(b.Role, b.Role)})
				}
			}
		}
		if len(needs.Staff) > 0 || len(needs.Personal) > 0 {
			out = append(out, village.VillageNeed{Kind: "staff", Item: named("staff", "staff")})
		}
	}
	for _, p := range planned {
		out = append(out, village.VillageNeed{Kind: village.NeedKnowledge, Item: named(p, p)})
	}
	return out
}

// ensureFunction gives a finished building its function row (and its look) when it has none: the function that
// replaces its catalogue code, at the level that code stands for, with the modules that level includes. Idempotent
// under any number of replicas (ON CONFLICT DO NOTHING). nil when the building is not a lot function (a road) or is
// not finished.
func (h *VillageHandler) ensureFunction(ctx context.Context, tx application.Tx, k lotKit, s application.FoundedSettlement,
	b application.SettlementBuildingInstance,
) (*application.BuildingFunction, error) {
	repo := tx.SettlementBuildings()
	if f, err := repo.FunctionOf(ctx, application.BuildingRefSettlement, b.ID); err != nil || f != nil {
		return f, err
	}
	code, ok := k.snap.FunctionReplacing(b.TypeCode)
	if !ok || b.Status != "complete" {
		return nil, nil
	}
	spec := k.specs[code]
	w, d := 1, 1
	if bd, ok := k.snap.SettlementBuildingDef(b.TypeCode); ok {
		df := bd.Def()
		w, d = max(df.FootprintW, 1), max(df.FootprintH, 1)
		if b.Rotated {
			w, d = d, w
		}
	}
	level := k.levelOfType(code, b.TypeCode)
	since := b.QueuedAt
	if b.CompletedAt != nil {
		since = *b.CompletedAt
	}
	nf := application.BuildingFunction{RefKind: application.BuildingRefSettlement, RefID: b.ID, SettlementID: s.CityID, Function: code,
		Level: level, Storeys: 1, W: w, D: d, Status: application.FunctionActive, Market: content.MarketLegal, Since: since,
		ConditionBPS: labor.BPS, Modules: lotbuild.Included(spec, level)}
	if _, err := repo.EnsureFunction(ctx, nf); err != nil {
		return nil, err
	}
	return repo.FunctionOf(ctx, application.BuildingRefSettlement, b.ID)
}

// composition of a stored function.
func compositionOf(f *application.BuildingFunction) lotbuild.Composition {
	c := lotbuild.Composition{Function: f.Function, Level: f.Level, Storeys: f.Storeys, W: f.W, D: f.D, Modules: map[string]int{}}
	for m, n := range f.Modules {
		c.Modules[m] = n
	}
	return c
}

// biomeOf is the biome code of the settlement's cell (the palette of the looks).
func (h *VillageHandler) biomeOf(ctx context.Context, s application.FoundedSettlement) string {
	w, err := h.world(ctx)
	if err != nil || w == nil || int(s.WorldCellID) >= len(w.Cells) || s.WorldCellID < 0 {
		return ""
	}
	return w.BiomeCode(s.WorldCellID)
}

// lookOf reads (or makes) the look of a building. force re-describes it after the contents changed; the seed's
// re-rolls are kept and bumped only while the look equals a neighbour's.
func (h *VillageHandler) lookOf(ctx context.Context, tx application.Tx, s application.FoundedSettlement, f *application.BuildingFunction,
	force bool, now time.Time,
) (lookdesc.Descriptor, error) {
	repo := tx.SettlementBuildings()
	stored, err := repo.Look(ctx, f.RefKind, f.RefID)
	if err != nil {
		return lookdesc.Descriptor{}, err
	}
	if stored != nil && !force {
		var d lookdesc.Descriptor
		if json.Unmarshal(stored.Descriptor, &d) == nil && d.Version == lookdesc.Version {
			return d, nil
		}
	}
	others, err := repo.NeighbourLooks(ctx, s.CityID, f.RefID)
	if err != nil {
		return lookdesc.Descriptor{}, err
	}
	keys := map[string]bool{}
	for _, raw := range others {
		var nd lookdesc.Descriptor
		if json.Unmarshal(raw, &nd) == nil {
			keys[nd.Key()] = true
		}
	}
	in := lookdesc.Input{ID: f.RefID, Function: f.Function, Level: f.Level, W: f.W, D: f.D, Storeys: f.Storeys, Modules: f.Modules,
		ConditionBPS: f.ConditionBPS, Biome: h.biomeOf(ctx, s)}
	if stored != nil {
		in.Reroll = stored.Reroll
	}
	d, reroll := lookdesc.Distinct(in, keys, h.lot.LookRerolls)
	raw, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	return d, repo.SaveLook(ctx, application.BuildingLook{RefKind: f.RefKind, RefID: f.RefID, Seed: d.Seed, Reroll: reroll,
		Descriptor: raw, Version: lookdesc.Version, UpdatedAt: now})
}

func lookView(d lookdesc.Descriptor) *village.LotLook {
	return &village.LotLook{Version: d.Version, Function: d.Function, Level: d.Level, W: d.W, D: d.D, Storeys: d.Storeys, Material: d.Material,
		Roof: d.Roof, Modules: d.Modules, Condition: d.Condition, Seed: int64(d.Seed), Palette: d.Palette, Wobble: d.Wobble, Windows: d.Windows,
		Door: d.Door, Hue: d.Hue, Prop: d.Prop, Chimney: d.Chimney, Awning: d.Awning}
}

// lotOwner says who owns a building and whether the viewer may order on it: the owner of a private building, or
// the holder of public.build for the settlement's own.
type lotOwner struct {
	private *application.PrivateBuilding
	mine    bool
	public  bool
}

func (o lotOwner) can() bool { return o.mine || o.public }

func (h *VillageHandler) ownerOf(ctx context.Context, tx application.Tx, s application.FoundedSettlement, p *application.Player,
	b application.SettlementBuildingInstance,
) (lotOwner, error) {
	pb, err := tx.Citizens().PrivateBuilding(ctx, b.ID)
	if err != nil {
		if !isSentinel(err, application.ErrPrivateBuildingNotFound) {
			return lotOwner{}, err
		}
		pb = nil
	}
	if pb != nil {
		return lotOwner{private: pb, mine: pb.OwnerID == p.ID}, nil
	}
	ok, err := h.mayVillage(ctx, tx, s, p.ID, charter.PublicBuild)
	if err != nil {
		return lotOwner{}, err
	}
	return lotOwner{public: ok}, nil
}

// myBuildings lists the buildings the viewer manages in the settlement: his private ones, and the settlement's own
// when he holds public.build.
func (h *VillageHandler) myBuildings(ctx context.Context, tx application.Tx, k lotKit, s application.FoundedSettlement, p *application.Player,
	buildings []application.SettlementBuildingInstance,
) ([]application.SettlementBuildingInstance, error) {
	priv, err := tx.Citizens().PrivateBuildings(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	owner := map[string]string{}
	for _, pb := range priv {
		owner[pb.BuildingID] = pb.OwnerID
	}
	public, err := h.mayVillage(ctx, tx, s, p.ID, charter.PublicBuild)
	if err != nil {
		return nil, err
	}
	var out []application.SettlementBuildingInstance
	for _, b := range buildings {
		if !b.Holds() {
			continue
		}
		if _, isFn := k.snap.FunctionReplacing(b.TypeCode); !isFn {
			continue
		}
		o, isPrivate := owner[b.ID]
		switch {
		case isPrivate && o == p.ID:
			out = append(out, b)
		case !isPrivate && public && b.Complete():
			if code, _ := k.snap.FunctionReplacing(b.TypeCode); k.defs[code].Owners != nil && containsStr(k.defs[code].Owners, "treasury") {
				out = append(out, b)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].QueuedAt.Before(out[j].QueuedAt) })
	return out, nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// holdingsOf is what a player has to build with: the home store first, then the bags.
func holdingsOf(ctx context.Context, tx application.Tx, playerID string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, holding := range []string{application.HoldHome, application.HoldCarried} {
		stacks, _, err := tx.Items().Holdings(ctx, playerID, holding)
		if err != nil {
			return nil, err
		}
		for _, s := range stacks {
			out[s.Item] += s.Qty
		}
	}
	return out, nil
}

func (h *VillageHandler) itemLines(snap *content.Snapshot, m map[string]int64) []village.WorkItemLine {
	codes := make([]string, 0, len(m))
	for c, n := range m {
		if n > 0 {
			codes = append(codes, c)
		}
	}
	sort.Strings(codes)
	out := make([]village.WorkItemLine, 0, len(codes))
	for _, c := range codes {
		out = append(out, village.WorkItemLine{Item: materialLineOf(snap, c, m[c]).Component, Qty: m[c]})
	}
	return out
}

// ManageLot handles settlement.lot.manage.
func (h *VillageHandler) ManageLot(ctx context.Context, meta envelope.Metadata, req VillageManageRequest) (*presentation.Response, error) {
	lang := meta.Language
	var (
		view  village.LotManageView
		offer *application.LocalOffer
	)
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
		if !h.lot.Enabled() {
			return refuseVillage(village.VillageNotAvailable, village.AddrMine)
		}
		snap := h.content.Current()
		k := newLotKit(snap)
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		mine, err := h.myBuildings(ctx, tx, k, s, p, buildings)
		if err != nil {
			return err
		}
		view = village.LotManageView{Village: s.Name, Stage: village.LotMenu}
		id := strings.TrimSpace(req.Building)
		if id == "" && len(mine) == 1 {
			id = mine[0].ID
		}
		if id == "" {
			for _, b := range mine {
				line, err := h.lotLine(ctx, tx, k, s, b)
				if err != nil {
					return err
				}
				view.Buildings = append(view.Buildings, line)
			}
			return nil
		}
		var target *application.SettlementBuildingInstance
		for i := range mine {
			if mine[i].ID == id {
				target = &mine[i]
			}
		}
		if target == nil {
			return refuseVillage(village.LotNotYours, village.AddrLotManage)
		}
		return h.manageOne(ctx, tx, meta, req, k, s, p, *target, buildings, &view, &offer)
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	resp := village.LotManage(h.screen(meta, lang), view)
	if view.Stage == village.LotAsk {
		resp = attachOffer(resp, offer, 6)
	}
	return resp, nil
}

func (h *VillageHandler) lotLine(ctx context.Context, tx application.Tx, k lotKit, s application.FoundedSettlement,
	b application.SettlementBuildingInstance,
) (village.LotBuildingLine, error) {
	line := village.LotBuildingLine{ID: b.ID, Building: named(b.TypeCode, h.buildingName(b.TypeCode)), X: b.LotX, Y: b.LotY, Built: b.Status == "complete"}
	f, err := h.ensureFunction(ctx, tx, k, s, b)
	if err != nil {
		return line, err
	}
	if f != nil {
		line.Function, line.FunctionName, line.Level, line.Storeys = f.Function, k.name(f.Function).Name, f.Level, f.Storeys
	}
	if w, err := tx.SettlementBuildings().OpenWork(ctx, b.ID); err != nil {
		return line, err
	} else if w != nil {
		line.HasOrder = true
	}
	return line, nil
}

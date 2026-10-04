package clientapi

import (
	"context"
	"errors"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/statesync"
)

// Per-viewer data for the map (docs/adr/0034 settlement summary, ADR 0041).
// The layout is the same for everyone of a kind of viewer and cached by its
// version; what a viewer may DO with a building, and how it stands right now
// (resources, workers), changes with money and shifts the layout version
// does not follow. So it travels in the settlement summary of state sync,
// which is projected again on every settlement event: the client keeps no
// per-kind table of who may do what and fetches no panel per building.
//
// Everything here is derived from the checks the commands make today: the
// head's office (holdsHead, authorizeVillage), ownership of a private
// building (settlement_private_buildings), residence, the content's ladders
// and the stock. It invents nothing the domain does not know.

// OverlayReader reads the facts (postgres.VillageFacts), once per projection
// of a member's view.
type OverlayReader interface {
	Facts(ctx context.Context, settlementID string) (application.SettlementFacts, error)
	// ActiveMissions lists the player's missions under way, oldest first.
	ActiveMissions(ctx context.Context, playerID string) ([]application.MissionProgress, error)
}

// OverlayViewer is who asks: the kind of viewer the summary is cut for and
// whether they live in the settlement.
type OverlayViewer struct {
	ID       string
	Kind     string // statesync.ViewerHead, ViewerMember or ViewerPublic
	Resident bool
}

// overlayInput is everything buildingOverlays decides from.
type overlayInput struct {
	Snap        *content.Snapshot
	Tier        string
	Rows        []application.SettlementBuildingInstance
	Owners      map[string]string // building id -> owner player id (private buildings)
	Viewer      OverlayViewer
	Facts       application.SettlementFacts
	StockBase   int64
	ConcurrentN int
}

// buildingOverlays is the overlay of every building the viewer is told
// about. Roads are left out unless they are going up (nothing to do with a
// standing road but look at it); a client takes a building with no entry as
// "info" only.
func buildingOverlays(in overlayInput) []statesync.BuildingOverlay {
	out := []statesync.BuildingOverlay{}
	member := in.Viewer.Kind != statesync.ViewerPublic
	rows := append([]application.SettlementBuildingInstance(nil), in.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	foot := func(b application.SettlementBuildingInstance) (int, int) {
		w, h := 1, 1
		if d, ok := in.Snap.SettlementBuildingDef(b.TypeCode); ok {
			def := d.Def()
			if b.Rotated {
				def = def.Rotate()
			}
			w, h = def.FootprintW, def.FootprintH
		}
		return w, h
	}
	// The road network: every road lot and the civic hall's footprint.
	network := map[[2]int]bool{}
	for _, b := range rows {
		if !b.Holds() || b.Status == "queued" {
			continue
		}
		if b.TypeCode == "road" || b.TypeCode == "civic_hall" {
			w, h := foot(b)
			for dx := 0; dx < w; dx++ {
				for dy := 0; dy < h; dy++ {
					network[[2]int{b.LotX + dx, b.LotY + dy}] = true
				}
			}
		}
	}
	touches := func(b application.SettlementBuildingInstance) bool {
		w, h := foot(b)
		for dx := 0; dx < w; dx++ {
			for dy := 0; dy < h; dy++ {
				x, y := b.LotX+dx, b.LotY+dy
				for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					if network[[2]int{x + d[0], y + d[1]}] {
						return true
					}
				}
			}
		}
		return false
	}

	running := 0
	built := map[settlementbuilding.RoleTier]int{}
	capacity := in.StockBase
	for _, b := range rows {
		if b.Status == "building" || b.Status == "queued" {
			if d, ok := in.Snap.SettlementBuildingDef(b.TypeCode); !ok || !d.CapExempt {
				running++
			}
		}
		if b.Status != "complete" {
			continue
		}
		if d, ok := in.Snap.SettlementBuildingDef(b.TypeCode); ok {
			capacity += d.Storage
			if d.Role != "" {
				built[settlementbuilding.RoleTier{Role: d.Role, Tier: d.Tier}]++
			}
		}
	}
	free := capacity - in.Facts.StockUsed
	if free < 0 {
		free = 0
	}
	caps := capabilitiesOf(in.Snap, in.Facts.Owned)

	for _, b := range rows {
		if !b.Holds() || b.ID == "" {
			continue
		}
		d, known := in.Snap.SettlementBuildingDef(b.TypeCode)
		if !known {
			d = content.SettlementBuildingDef{Code: b.TypeCode, Footprint: [2]int{1, 1}}
		}
		state := application.ViewState(b)
		if !member && state != application.ViewBuilt {
			continue
		}
		if d.Code == "road" && state == application.ViewBuilt {
			continue
		}
		o := statesync.BuildingOverlay{ID: b.ID, Tier: d.Tier, Role: d.Role, Reasons: []string{}, Actions: []string{statesync.ActionInfo}}
		if !member {
			o.Status = statesync.BuildingWorking
			if state != application.ViewBuilt {
				o.Status = statesync.BuildingRaising
			}
			out = append(out, o)
			continue
		}

		owner, private := in.Owners[b.ID]
		// Who manages it: the owner of a private building, the head of a civic one.
		manage := in.Viewer.Kind == statesync.ViewerHead
		if private {
			manage = owner == in.Viewer.ID
		}
		complete := b.Status == "complete"
		workplace := complete && len(d.Produces) > 0

		switch {
		case b.Status == "building" || b.Status == "queued":
			o.Status = statesync.BuildingRaising
			if b.ByWork() && in.Facts.OpenJobs[b.ID] && in.Viewer.Resident {
				o.Actions = append(o.Actions, statesync.ActionHelpBuild)
			}
			if b.ByWork() && manage {
				o.Actions = append(o.Actions, statesync.ActionWorkers)
			}
			if manage {
				o.Actions = append(o.Actions, statesync.ActionCancel)
			}
		case complete:
			if b.DamageBPS > 0 {
				o.Reasons = append(o.Reasons, statesync.ReasonDamaged)
			}
			if d.Code != "road" && d.Code != "civic_hall" && !touches(b) {
				o.Reasons = append(o.Reasons, statesync.ReasonNoRoad)
			}
			if workplace {
				need := d.Workers
				if need < 1 {
					need = 1
				}
				have := in.Facts.Shifts[b.ID]
				o.Staff = &statesync.StaffData{Have: have, Need: need}
				if have == 0 {
					o.Reasons = append(o.Reasons, statesync.ReasonNoStaff)
				}
				var inputs int64
				short := false
				for item, q := range d.Consumes {
					inputs += q
					if in.Facts.StockUnits[item] < q {
						short = true
					}
				}
				if short {
					o.Reasons = append(o.Reasons, statesync.ReasonNoInput)
				}
				if free+inputs <= 0 {
					o.Reasons = append(o.Reasons, statesync.ReasonStorageFull)
				}
				if in.Viewer.Resident {
					o.Actions = append(o.Actions, statesync.ActionTakeShift)
				}
				// A workplace's jobs are posted by the head, whatever owns the building.
				if in.Viewer.Kind == statesync.ViewerHead {
					o.Actions = append(o.Actions, statesync.ActionWorkers)
				}
			}
			o.Status = statesync.BuildingWorking
			if len(o.Reasons) > 0 {
				o.Status = statesync.BuildingIdle
			}

			if d.Code == "civic_hall" {
				o.Actions = append(o.Actions, statesync.ActionTreasury)
				if in.Viewer.Kind == statesync.ViewerHead {
					o.Actions = append(o.Actions, statesync.ActionResearch)
				}
				if in.Facts.Election != nil {
					o.Actions = append(o.Actions, statesync.ActionElections)
				}
			}
			if manage && d.Role != "" {
				if steps := in.Snap.NextRoleTier(d.Role, d.Tier); len(steps) > 0 {
					o.Actions = append(o.Actions, statesync.ActionUpgrade)
					// A private building is upgraded from its owner's cash,
					// which the summary does not read: never offered as ready.
					if !private {
						o.CanUpgrade = canUpgrade(in, steps, built, caps, running)
					}
				}
			}
			if manage && private && containsReason(o.Reasons, statesync.ReasonNoRoad) {
				o.Actions = append(o.Actions, statesync.ActionRoad)
			}
			if manage && d.Code != "civic_hall" && d.Code != "road" {
				o.Actions = append(o.Actions, statesync.ActionDemolish)
			}
		}
		out = append(out, o)
	}
	return out
}

func containsReason(rs []string, r string) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

// canUpgrade says whether any next step of the building's ladder can be
// started now: the settlement lists it, knowledge and the standing building
// it asks for are there, the treasury and the stock cover it and a builder
// is free. It mirrors the panel's upgrade lines and the placement rules.
func canUpgrade(in overlayInput, steps []content.SettlementBuildingDef, built map[settlementbuilding.RoleTier]int,
	caps map[string]bool, running int,
) bool {
	for _, o := range steps {
		def := o.Def()
		if !def.ListedAt(in.Tier) {
			continue
		}
		ok := true
		for _, k := range def.RequiresKnowledge {
			if _, has := in.Facts.Owned[k]; !has {
				ok = false
			}
		}
		for _, c := range def.RequiresKnowledgeCapability {
			if !caps[c] {
				ok = false
			}
		}
		if def.RequiresBuildingRole != nil && built[*def.RequiresBuildingRole] < 1 {
			ok = false
		}
		if def.MinLiteracyShareBPS > 0 && in.Facts.LiteracyBPS < def.MinLiteracyShareBPS {
			ok = false
		}
		if o.CostMoney > in.Facts.Treasury {
			ok = false
		}
		for item, q := range o.CostMaterials {
			if in.Facts.StockUnits[item] < q {
				ok = false
			}
		}
		if !o.CapExempt && running >= in.ConcurrentN {
			ok = false
		}
		if ok {
			return true
		}
	}
	return false
}

// capabilitiesOf is the capability tags the held knowledge provides.
func capabilitiesOf(snap *content.Snapshot, owned map[string]string) map[string]bool {
	out := map[string]bool{}
	tree := snap.SettlementKnowledgeTree()
	for code := range owned {
		t, ok := tree[code]
		if !ok {
			continue
		}
		provides := t.Provides
		if len(provides) == 0 {
			provides = []string{t.Code}
		}
		for _, c := range provides {
			out[c] = true
		}
	}
	return out
}

// SettlementOverlay is what the summary adds for this viewer: the per-building
// overlay and the open election. A visitor gets the tier and "info" only and
// no facts are read for them.
func (v *VillageService) SettlementOverlay(ctx context.Context, settlementID, playerID, kind string, resident bool,
) ([]statesync.BuildingOverlay, *statesync.ElectionData, error) {
	s, err := v.Settlements.ByID(ctx, settlementID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := v.Buildings.List(ctx, s.CityID)
	if err != nil {
		return nil, nil, err
	}
	viewer := OverlayViewer{ID: playerID, Kind: kind, Resident: resident}
	in := overlayInput{Snap: v.Content.Current(), Tier: s.Tier, Rows: rows, Viewer: viewer,
		StockBase: v.StockBaseCapacity, ConcurrentN: settlementbuilding.CrewCap(s.Tier, overlayHousing(v.Content.Current(), rows), v.HomesPerBuildCrew)}
	if viewer.Kind != statesync.ViewerPublic {
		if v.Overlay == nil {
			return nil, nil, errors.New("clientapi: no overlay reader")
		}
		if in.Facts, err = v.Overlay.Facts(ctx, s.CityID); err != nil {
			return nil, nil, err
		}
		if v.Citizens != nil {
			priv, err := v.Citizens.PrivateBuildings(ctx, s.CityID)
			if err != nil {
				return nil, nil, err
			}
			in.Owners = map[string]string{}
			for _, p := range priv {
				in.Owners[p.BuildingID] = p.OwnerID
			}
		}
	}
	var election *statesync.ElectionData
	if e := in.Facts.Election; e != nil {
		election = &statesync.ElectionData{Office: e.Office, OpensAt: e.OpensAt, CandidacyEndsAt: e.CandidacyEndsAt, VotingEndsAt: e.VotingEndsAt}
	}
	return buildingOverlays(in), election, nil
}

// Goal is the next goal of the player for the quest strip: the first
// unfinished objective of the mission they have had longest, else the next
// unmet goal of their settlement's promotion (the head is told when all are
// met). ADR 0044's goals feed in here as another source. Nil when there is
// nothing to aim at.
func (v *VillageService) Goal(ctx context.Context, playerID string) (*statesync.GoalData, error) {
	if v.Overlay == nil {
		return nil, nil
	}
	snap := v.Content.Current()
	missions, err := v.Overlay.ActiveMissions(ctx, playerID)
	if err != nil {
		return nil, err
	}
	if g := missionGoal(snap, missions); g != nil {
		return g, nil
	}
	mine, err := v.Settlements.ByPlayer(ctx, playerID)
	switch {
	case errors.Is(err, application.ErrCityNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}
	facts, err := v.Overlay.Facts(ctx, mine.CityID)
	if err != nil {
		return nil, err
	}
	rows, err := v.Buildings.List(ctx, mine.CityID)
	if err != nil {
		return nil, err
	}
	return growthGoal(snap, holdsHead(mine), rows, facts), nil
}

// missionGoal is the first unfinished objective of the first mission that
// has one.
func missionGoal(snap *content.Snapshot, missions []application.MissionProgress) *statesync.GoalData {
	for _, m := range missions {
		def, ok := snap.MissionDef(m.Code)
		if !ok {
			continue
		}
		for i, o := range def.Objectives {
			var done int64
			if i < len(m.Progress) {
				done = m.Progress[i]
			}
			if done >= o.Count {
				continue
			}
			args := map[string]string{"mission": m.Code}
			if o.Target != "" {
				args["target"] = o.Target
			}
			return &statesync.GoalData{Code: statesync.GoalSourceMission + "." + o.Kind, Args: args,
				Progress: done, Target: o.Count, GoTo: life.AddrMissions}
		}
	}
	return nil
}

// growthGoal is the head's next step from what the settlement has: the first
// research or building whose prerequisites all stand (content.NextGrowth), counted
// the way the development readout counts. Nil for a resident or when nothing new
// is within reach. It replaces the promotion goals: there is no ladder.
func growthGoal(snap *content.Snapshot, head bool, rows []application.SettlementBuildingInstance, f application.SettlementFacts,
) *statesync.GoalData {
	if !head {
		return nil
	}
	owned := make(map[string]bool, len(f.Owned))
	for code := range f.Owned {
		owned[code] = true
	}
	standing := map[string]bool{}
	levels := map[string]int{}
	for _, b := range rows {
		if b.Status != "complete" {
			continue
		}
		standing[b.TypeCode] = true
		if def, ok := snap.SettlementBuildingDef(b.TypeCode); ok && def.Role != "" && def.Tier > levels[def.Role] {
			levels[def.Role] = def.Tier
		}
	}
	next := snap.NextGrowth(owned, standing, levels, 1)
	if len(next) == 0 {
		return nil
	}
	st := next[0]
	return &statesync.GoalData{Code: statesync.GoalSourceGrowth + "." + st.Kind, Args: map[string]string{"code": st.Code},
		Progress: 0, Target: 1, GoTo: village.AddrVillageDevelopment}
}

// overlayHousing is the homes' capacity of the finished buildings, the same
// count the labour market and the build menu use.
func overlayHousing(snap *content.Snapshot, rows []application.SettlementBuildingInstance) int64 {
	var out int64
	for _, b := range rows {
		if b.Status != "complete" {
			continue
		}
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
			for _, e := range d.BuildingEffects() {
				if e.Target == "housing_capacity" && e.Op == item.EffectAdd {
					out += e.Value
				}
			}
		}
	}
	return out
}

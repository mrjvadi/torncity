package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// This file holds settlement.building.view: the panel of one placed
// building. Every building shows what it IS and what it does; the destructive
// actions are secondary and the next level of its role is disclosed only
// when asked (mode "up"), from the content's own role/tier ladder — no
// upgrade level is invented that the content does not declare.
//
// What each building type has in code today (the inventory behind the panels):
//   - storage role (granary): the village's public stock, org_stacks under
//     OrgSettlement. The panel lists its contents. There is no capacity in
//     the panel shows the store's use against its capacity (stockOf: the
//     base capacity plus every standing building's storage).
//   - education role (teaching_circle, school): literacy diffusion
//     (village_teach.go) runs on its own tick while any complete education
//     building stands. It cannot be started or stopped by the head, so the
//     panel reports its state only (TODO: a teaching switch, if the owner
//     wants one).
//   - civic_hall: the head's seat; the panel gives the village numbers and
//     the doors to the overview, knowledge, build and progress screens, and
//     the research running now.
//   - road: nothing but info.
//   - every other role: description, effects and upkeep from the content.

// VillageBuildingViewRequest names a placed building, and optionally the
// panel's mode (village.BuildingMode*).
type VillageBuildingViewRequest struct {
	BuildingID string `json:"building_id"`
	Mode       string `json:"mode,omitempty"`
}

// buildingKind picks the panel a building is drawn with.
func buildingKind(d content.SettlementBuildingDef, shopBuilding string) string {
	switch {
	case shopBuilding != "" && d.Code == shopBuilding:
		return village.BuildingKindShop
	case d.Code == "road":
		return village.BuildingKindRoad
	case d.Code == "civic_hall":
		return village.BuildingKindCivicHall
	case d.Role == "storage":
		return village.BuildingKindStorage
	case d.Role == "education":
		return village.BuildingKindSchool
	case d.Role == "security":
		return village.BuildingKindSecurity
	default:
		return village.BuildingKindGeneric
	}
}

// BuildingView handles settlement.building.view.
func (h *VillageHandler) BuildingView(ctx context.Context, meta envelope.Metadata, req VillageBuildingViewRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.BuildingView
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
		b, err := tx.SettlementBuildings().Get(ctx, strings.TrimSpace(req.BuildingID))
		if isSentinel(err, application.ErrBuildingNotFound) {
			return refuseVillage(village.VillageNotFound)
		}
		if err != nil {
			return err
		}
		if b.SettlementID != s.CityID || !b.Holds() {
			return refuseVillage(village.VillageNotFound)
		}
		head := true
		if _, aerr := h.requireVillage(ctx, tx, s, p.ID, charter.PublicBuild); aerr != nil {
			if !stderrors.Is(aerr, application.ErrNotOfficeHolder) {
				return aerr
			}
			head = false
		}
		// A resident's building is managed by its owner, never by the head.
		if pb, perr := tx.Citizens().PrivateBuilding(ctx, b.ID); perr == nil {
			head = pb.OwnerID == p.ID
		} else if !stderrors.Is(perr, application.ErrPrivateBuildingNotFound) {
			return perr
		}

		d, known := snap.SettlementBuildingDef(b.TypeCode)
		if !known {
			d = content.SettlementBuildingDef{Code: b.TypeCode, Name: b.TypeCode, Footprint: [2]int{1, 1}}
		}
		def := d.Def()
		if b.Rotated {
			def = def.Rotate()
		}
		view = village.BuildingView{
			ID: b.ID, Building: named(d.Code, d.Name), Role: d.Role, Tier: d.Tier, Kind: buildingKind(d, shopBuildingCode(snap)),
			State: village.BuildingStateComplete, X: b.LotX, Y: b.LotY, W: def.FootprintW, H: def.FootprintH,
			Rotated: b.Rotated, Upkeep: d.Upkeep, CanManage: head,
		}
		for _, e := range d.BuildingEffects() {
			view.Effects = append(view.Effects, village.BuildingEffectLine{Target: e.Target, Value: e.Value})
		}

		mode := strings.TrimSpace(req.Mode)
		if head {
			switch mode {
			case village.BuildingModeUpgrade, village.BuildingModeDemolish, village.BuildingModeCancel:
				view.Mode = mode
			}
		}

		if b.Status == "building" {
			now := h.now()
			finish := b.QueuedAt.Add(h.scale.RealWait(def.BuildTime))
			if b.FinishAt != nil {
				finish = *b.FinishAt
			}
			view.State = village.BuildingStateBuilding
			view.StartedAt, view.FinishAt, view.Left = b.QueuedAt, finish, countdownTo(finish, now)
			view.ProgressPercent = progressPercent(b.QueuedAt, finish, now)
			if view.Mode == village.BuildingModeDemolish || view.Mode == village.BuildingModeUpgrade {
				view.Mode = ""
			}
			return nil
		}
		if view.Mode == village.BuildingModeCancel {
			view.Mode = ""
		}

		switch view.Kind {
		case village.BuildingKindStorage:
			rows, err := tx.SettlementBuildings().List(ctx, s.CityID)
			if err != nil {
				return err
			}
			stock, err := h.stockOf(ctx, tx, snap, s.CityID, rows)
			if err != nil {
				return err
			}
			view.StockUsed, view.StockCapacity = stock.Used, stock.Capacity
			codes := make([]string, 0, len(stock.Units))
			for code := range stock.Units {
				codes = append(codes, code)
			}
			sort.Strings(codes)
			for _, code := range codes {
				qty := stock.Units[code]
				if qty <= 0 {
					continue
				}
				line := village.BuildingStockLine{Item: named(code, code), Kind: "item", Qty: qty}
				if cd, ok := snap.ComponentDef(code); ok {
					line.Item, line.Kind = named(cd.Code, cd.Name), "component"
				} else if id, ok := snap.ItemDef(code); ok {
					line.Item = named(id.Code, id.Name)
				}
				view.Stock = append(view.Stock, line)
			}
		case village.BuildingKindShop:
			if h.shop.enabled() {
				sv, err := h.shopView(ctx, tx, meta, p, s, h.now())
				if err != nil {
					return err
				}
				view.Shop = &sv
			}
		case village.BuildingKindSchool:
			literacyBPS, _, err := tx.SettlementKnowledge().Literacy(ctx, s.CityID)
			if err != nil {
				return err
			}
			// Teaching runs on its own tick for as long as a complete
			// education building stands, and this one does.
			view.LiteracyPercent, view.Teaching = literacyBPS/100, true
		case village.BuildingKindCivicHall:
			residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
			if err != nil {
				return err
			}
			treasury, err := treasuryBalance(ctx, tx, s.CityID)
			if err != nil {
				return err
			}
			view.Population, view.Treasury = int(residents), treasury
			running, err := tx.SettlementKnowledge().RunningResearch(ctx, s.CityID)
			if err != nil {
				return err
			}
			if running != nil {
				kd, _ := snap.SettlementKnowledgeDef(running.Code)
				view.Research = &village.BuildingResearchLine{
					Knowledge: named(kd.Code, kd.Name), FinishAt: running.FinishAt, Left: countdownTo(running.FinishAt, h.now()),
				}
			}
		}

		if all, lerr := tx.SettlementBuildings().List(ctx, s.CityID); lerr != nil {
			return lerr
		} else if view.Work, lerr = h.nodeWork(ctx, tx, snap, s, *b, d, all); lerr != nil {
			return lerr
		}

		if d.Role != "" && head {
			ups, err := h.upgradeLines(ctx, tx, snap, s, d)
			if err != nil {
				return err
			}
			view.HasUpgrade = len(ups) > 0
			if view.Mode == village.BuildingModeUpgrade {
				view.Upgrades = ups
			}
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.BuildingPanel(h.screen(meta, lang), view), nil
}

// upgradeLines are the buildings of the next tier of d's role, each with
// whether its prerequisites are met: the content's own ladder (a tier-2
// building requires a tier-1 one of its role), revealed only on request.
func (h *VillageHandler) upgradeLines(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	s application.FoundedSettlement, d content.SettlementBuildingDef,
) ([]village.BuildingUpgradeLine, error) {
	next := 0
	for _, code := range sortedBuildingCodes(snap) {
		o, _ := snap.SettlementBuildingDef(code)
		if o.Role == d.Role && o.Tier > d.Tier && (next == 0 || o.Tier < next) {
			next = o.Tier
		}
	}
	if next == 0 {
		return nil, nil
	}
	st, capabilities, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return nil, err
	}
	built, err := builtRoleCounts(ctx, tx, snap, s.CityID)
	if err != nil {
		return nil, err
	}
	waitOf, err := h.buildWaiter(ctx, tx, snap, s)
	if err != nil {
		return nil, err
	}
	var out []village.BuildingUpgradeLine
	for _, code := range sortedBuildingCodes(snap) {
		o, _ := snap.SettlementBuildingDef(code)
		def := o.Def()
		if o.Role != d.Role || o.Tier != next {
			continue
		}
		line := village.BuildingUpgradeLine{
			ExpectedWait: waitOf(def),
			Building:     named(o.Code, o.Name), Tier: o.Tier, CostMoney: o.CostMoney,
			BuildTime: h.scale.RealWait(def.BuildTime), Available: true,
		}
		// The step above the settlement's own tier is still revealed on
		// request (the owner's disclosure rule), with the tier it opens at.
		listed, lerr := ListedInTx(ctx, tx, snap, "upgrade_list", s.CityID, o.Code, def.ListedAt(s.Tier))
		if lerr != nil {
			return nil, lerr
		}
		if !listed {
			line.Available = false
			out = append(out, line)
			continue
		}
		for _, k := range def.RequiresKnowledge {
			if !st.Owned.Has(k) {
				line.Available = false
				line.Missing = append(line.Missing, named(k, k))
			}
		}
		for _, cp := range def.RequiresKnowledgeCapability {
			if !capabilities.Has(cp) {
				line.Available = false
			}
		}
		if def.RequiresBuildingRole != nil && built[*def.RequiresBuildingRole] < 1 {
			line.Available = false
		}
		if def.MinLiteracyShareBPS > 0 && st.LiteracyShareBPS < def.MinLiteracyShareBPS {
			line.Available = false
		}
		// Missing knowledge is shown by its authored name.
		for i, m := range line.Missing {
			if kd, ok := snap.SettlementKnowledgeDef(m.Code); ok {
				line.Missing[i] = named(kd.Code, kd.Name)
			}
		}
		out = append(out, line)
	}
	return out, nil
}

// progressPercent is how far between start and finish now is, 0..100.
func progressPercent(start, finish, now time.Time) int {
	span := finish.Sub(start)
	if span <= 0 || !now.After(start) {
		return 0
	}
	p := int(now.Sub(start) * 100 / span)
	if p > 100 {
		return 100
	}
	return p
}

// shopBuildingCode is the building that is the village shop's, from its content.
func shopBuildingCode(snap *content.Snapshot) string {
	if def, ok := snap.VillageShop(); ok {
		return def.Building
	}
	return ""
}

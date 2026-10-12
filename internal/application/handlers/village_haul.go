package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/haul"
	"github.com/mrjvadi/torncity/internal/domain/roads"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Carriage (docs/adr/0075 6). Goods between the stores of two settlements go by road cart over a stored road link, or by rail (village_rail.go).
//
//   - WHO WORKS THERE. A cart is built by builders (hours, paid by the treasury) and has a carter for the trip (his wage into the sink,
//     the trip's hours). A road link is laid by builders.
//   - WHAT IT CONSUMES. A cart: planks, timber, bar iron. A road: stone and builders' hours for each 30 m lot. A trip: the carter's wage and
//     the toll of the road when its owner is another settlement.
//   - WHAT IT PROVIDES. A load: goods leave the source store (haul_out) at departure and enter the destination's (haul_in) at arrival; in
//     between they are on the road in a load row, so nothing is created or lost (the verifier adds the two ends and the open loads).
//   - WHAT BREAKS. No road link, no idle cart that fits the road, a load heavier than the carts take, goods missing from the store.

// HaulArriveActionType is the game action of goods arriving.
const HaulArriveActionType = "haul_arrive"

// VillageHaulRequest is the payload of settlement.haul.
type VillageHaulRequest struct {
	// Action is "" (the screen), link, carrier or send (village.HaulAction*).
	Action string `json:"action,omitempty"`
	// Code, N and M: link: the other settlement's code, the road class, "confirm"; carrier: the haul mode; send: the item, how many, the
	// other settlement's code.
	Code string `json:"code,omitempty"`
	N    string `json:"n,omitempty"`
	M    string `json:"m,omitempty"`
}

// Haul handles settlement.haul.
func (h *VillageHandler) Haul(ctx context.Context, meta envelope.Metadata, req VillageHaulRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.HaulView
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
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.haulAct(ctx, tx, meta, snap, s, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.haulView(ctx, tx, snap, s, p)
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Haul(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) haulOpen() bool {
	return h.mining.Enabled() && !h.now().Before(h.mining.RuleAt)
}

func (h *VillageHandler) haulAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, act string, req VillageHaulRequest,
) (string, error) {
	if !h.haulOpen() {
		return "", refuseVillage(village.HaulNotYet, village.AddrHaul)
	}
	code, n, m := strings.TrimSpace(req.Code), strings.TrimSpace(req.N), strings.TrimSpace(req.M)
	switch act {
	case village.HaulActionLink:
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.RoadDraw); err != nil {
			return "", err
		}
		return village.HaulNoticeLink, h.linkRoad(ctx, tx, meta, snap, s, p, code, n)
	case village.HaulActionCarrier:
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.HaulOrder); err != nil {
			return "", err
		}
		return village.HaulNoticeCarrier, h.buildCart(ctx, tx, meta, snap, s, p, code)
	case village.HaulActionSend:
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.HaulOrder); err != nil {
			return "", err
		}
		return village.HaulNoticeSent, h.sendGoods(ctx, tx, meta, snap, s, p, code, n, m)
	}
	return "", refuseVillage(village.HaulBadArgs, village.AddrHaul)
}

// settlementByCode finds a founded settlement by its code.
func (h *VillageHandler) settlementByCode(ctx context.Context, tx application.Tx, code string) (*application.FoundedSettlement, error) {
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return nil, err
	}
	for i := range founded {
		if strings.EqualFold(founded[i].Code, code) {
			return &founded[i], nil
		}
	}
	return nil, nil
}

// routeBetween plans a line over the world's tiles between two settlements' cells for a class of way (road or rail).
func (h *VillageHandler) routeBetween(ctx context.Context, snap *content.Snapshot, from, to int32, class roads.Class) (roads.Route, error) {
	w, err := h.world(ctx)
	if err != nil {
		return roads.Route{}, err
	}
	if int(from) >= len(w.Cells) || int(to) >= len(w.Cells) || from < 0 || to < 0 {
		return roads.Route{}, roads.ErrNoRoute
	}
	pl, ok := snap.RoadPlanner()
	if !ok {
		return roads.Route{}, roads.ErrInvalid
	}
	a, b := w.Cells[from].Point, w.Cells[to].Point
	ta, tb := w.TileOfLatLon(a.LatDeg, a.LonDeg), w.TileOfLatLon(b.LatDeg, b.LonDeg)
	terr := roads.WorldTerrain{W: w, Src: worldgen.NewChunkTileSource(w, 256), TerrainBPS: pl.TerrainBPS, DefaultBPS: 10000, HighMountainM: pl.HighMountainM, HighMountainBPS: pl.HighMountainBPS}
	return roads.Plan(terr, []worldgen.TileID{ta}, []worldgen.TileID{tb}, class,
		roads.Params{SearchMarginM: pl.SearchMarginM, MaxExpansions: pl.MaxExpansions, BridgeCostRatio: pl.BridgeCostRatio, StreamCrossingSteps: pl.StreamCrossingSteps, MinTerrainBPS: 10000})
}

// routeRefusal names why a route could not be planned.
func routeRefusal(err error, back string) error {
	var nb *roads.NoBridgeError
	switch {
	case stderrors.As(err, &nb):
		return refuseVillage(village.HaulNoBridge, back)
	case stderrors.Is(err, roads.ErrNoRoute), stderrors.Is(err, roads.ErrInvalid):
		return refuseVillage(village.HaulNoRoute, back)
	}
	return err
}

// roadRank orders the road classes: a cart of a lower class runs on a better road, not the reverse.
func roadRank(class string) int {
	switch class {
	case "path":
		return 1
	case "track":
		return 2
	}
	return 0
}

// linkRoad lays a road to another settlement: the line is planned over the world's tiles, the stone and the builders are paid and the
// link opens when the works end. Without "confirm" it only quotes.
func (h *VillageHandler) linkRoad(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player, otherCode, class string) error {
	if class == "" {
		class = "path"
	}
	rc, ok := snap.RoadClass(class)
	if !ok || roadRank(class) == 0 {
		return refuseVillage(village.HaulBadArgs, village.AddrHaul)
	}
	other, err := h.settlementByCode(ctx, tx, otherCode)
	if err != nil {
		return err
	}
	if other == nil {
		return refuseVillage(village.HaulNoTarget, village.AddrHaul)
	}
	if other.CityID == s.CityID {
		return refuseVillage(village.HaulSame, village.AddrHaul)
	}
	if have, err := tx.Haul().RoadLinkBetween(ctx, s.CityID, other.CityID); err != nil {
		return err
	} else if have != nil {
		return refuseVillage(village.HaulLinkHave, village.AddrHaul)
	}
	route, err := h.routeBetween(ctx, snap, s.WorldCellID, other.WorldCellID, roads.Class{Code: rc.Code, MaxGradeBPS: rc.MaxGradeBPS, BridgeMaxSpanM: rc.BridgeMaxSpanM, Fords: rc.Fords})
	if err != nil {
		return routeRefusal(err, village.AddrHaul)
	}
	lots := haulLots(int64(route.LengthM))
	materials := map[string]int64{"stone": lots * h.mining.RoadLinkLotStone}
	wage := lots * h.mining.RoadLinkLotHours * h.mining.RailWorkHourWage
	now := h.now()
	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return err
	}
	workID, linkID := h.ids.NewID(), h.ids.NewID()
	if _, err := h.payWorks(ctx, tx, snap, s, worksPay{Materials: materials, Wage: wage, Reason: application.ReasonRailWorksWage, RefType: "build_works", RefID: workID, Back: village.AddrHaul}, now); err != nil {
		return err
	}
	if err := tx.Haul().AddRoadLink(ctx, application.RoadLink{ID: linkID, A: s.CityID, B: other.CityID, Owner: s.CityID, Class: class, LengthM: int64(route.LengthM), CreatedBy: p.ID, CreatedAt: now}); err != nil {
		return err
	}
	due := now.Add(time.Duration(lots*h.mining.RoadLinkLotMinutes) * time.Minute)
	if err := h.orderWorksAs(ctx, tx, application.BuildWork{ID: workID, SettlementID: s.CityID, Kind: application.WorkRoadLink, RefID: linkID, Materials: materials, Wage: wage, OrderedBy: p.ID, DueAt: due,
		Param: map[string]any{"class": class, "length_m": route.LengthM}}, now); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "road_link_ordered", s.CityID, map[string]any{"settlement_id": s.CityID, "other": other.CityID, "link_id": linkID, "length_m": route.LengthM, "due_at": due.UTC().Format(time.RFC3339)})
}

// haulLots is the number of 30 m lots that cover a length.
func haulLots(lengthM int64) int64 { return max((lengthM+29)/30, 1) }

// buildCart orders a cart: builders and materials now, the cart idle when the works end.
func (h *VillageHandler) buildCart(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player, mode string) error {
	hm, ok := haulMode(snap, mode)
	if !ok || hm.Kind != "cart" {
		return refuseVillage(village.HaulNoMode, village.AddrHaul)
	}
	st, _, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return err
	}
	if hm.Requires != nil {
		var missing []string
		for _, k := range hm.Requires.Knowledge {
			if _, have := st.Owned[k]; !have {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			buildings, _ := tx.SettlementBuildings().List(ctx, s.CityID)
			stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
			if err != nil {
				return err
			}
			pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
			if err != nil {
				return err
			}
			return needsRefusal(village.VillagePrerequisite, "haul", named(mode, hm.Name), pc.knowledgeNeeds(missing), village.AddrHaul)
		}
	}
	if err := tx.Items().LockOrg(ctx, application.SettlementOrg(s.CityID)); err != nil {
		return err
	}
	now := h.now()
	materials := map[string]int64{}
	for it, q := range hm.BuildMaterials {
		materials[it] = int64(q)
	}
	wage := int64(hm.BuildHours) * h.mining.RailWorkHourWage
	workID, carrierID := h.ids.NewID(), h.ids.NewID()
	if _, err := h.payWorks(ctx, tx, snap, s, worksPay{Materials: materials, Wage: wage, Reason: application.ReasonRailWorksWage, RefType: "build_works", RefID: workID, Back: village.AddrHaul}, now); err != nil {
		return err
	}
	if err := tx.Haul().AddCarrier(ctx, application.Carrier{ID: carrierID, SettlementID: s.CityID, OwnerKind: "settlement", OwnerID: s.CityID, Mode: mode, Kind: hm.Kind, CapacityKg: int64(hm.CapacityKG), Status: application.CarrierBuilding, CreatedAt: now}); err != nil {
		return err
	}
	due := now.Add(time.Duration(hm.BuildHours) * time.Hour)
	return h.orderWorksAs(ctx, tx, application.BuildWork{ID: workID, SettlementID: s.CityID, Kind: application.WorkCarrier, RefID: carrierID, Materials: materials, Wage: wage, OrderedBy: p.ID, DueAt: due,
		Param: map[string]any{"mode": mode}}, now)
}

func haulMode(snap *content.Snapshot, code string) (content.HaulModeDef, bool) {
	for _, m := range snap.HaulModes() {
		if m.Code == code {
			return m, true
		}
	}
	return content.HaulModeDef{}, false
}

// itemWeightG is what a unit of an item weighs.
func itemWeightG(snap *content.Snapshot, code string) int64 {
	if c, ok := snap.ComponentDef(code); ok && c.WeightG > 0 {
		return int64(c.WeightG)
	}
	if d, ok := snap.ItemDef(code); ok {
		return d.WeightGrams()
	}
	return content.DefaultWeightG
}

// sendGoods sends goods from this settlement's store to another's over their road: the carts that fit the road and stand here are
// loaded in order, the carters and the toll are paid, the goods leave the store and arrive when the trip is done.
func (h *VillageHandler) sendGoods(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player,
	item, qtyText, otherCode string,
) error {
	qty, err := strconv.ParseInt(qtyText, 10, 64)
	if err != nil || qty <= 0 || item == "" {
		return refuseVillage(village.HaulBadArgs, village.AddrHaul)
	}
	other, err := h.settlementByCode(ctx, tx, otherCode)
	if err != nil {
		return err
	}
	if other == nil {
		return refuseVillage(village.HaulNoTarget, village.AddrHaul)
	}
	if other.CityID == s.CityID {
		return refuseVillage(village.HaulSame, village.AddrHaul)
	}
	link, err := tx.Haul().RoadLinkBetween(ctx, s.CityID, other.CityID)
	if err != nil {
		return err
	}
	if link == nil {
		return refuseVillage(village.HaulNoLink, village.AddrHaul)
	}
	carriers, err := tx.Haul().CarriersOf(ctx, s.CityID)
	if err != nil {
		return err
	}
	var fit []application.Carrier
	var caps []int64
	for _, c := range carriers {
		if c.Kind != "cart" || c.Status != application.CarrierIdle {
			continue
		}
		if hm, ok := haulMode(snap, c.Mode); !ok || roadRank(hm.Surface) == 0 || roadRank(hm.Surface) > roadRank(link.Class) {
			continue
		}
		fit = append(fit, c)
		caps = append(caps, c.CapacityKg)
	}
	if len(fit) == 0 {
		return refuseVillage(village.HaulNoCarrier, village.AddrHaul)
	}
	units, ok := haul.Allocate(qty, itemWeightG(snap, item), caps)
	if !ok {
		r := refuseVillage(village.HaulTooHeavy, village.AddrHaul)
		var total int64
		for _, c := range caps {
			total += c
		}
		r.max = total
		return r
	}
	org := application.SettlementOrg(s.CityID)
	if err := tx.Items().LockOrg(ctx, org); err != nil {
		return err
	}
	now := h.now()
	dur := haul.Duration(link.LengthM, h.mining.CartSpeedKmh, link.ConditionBPS, h.mining.CartBrokenBPS)
	hours := int64((dur + time.Hour - 1) / time.Hour)
	treasuryAcct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
	if err != nil {
		return err
	}
	var carters int64
	for _, u := range units {
		if u > 0 {
			carters++
		}
	}
	wage := carters * hours * h.mining.RailWorkHourWage
	toll := int64(0)
	if link.Owner != s.CityID {
		toll = link.Toll
	}
	bal, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return err
	}
	if bal < wage+toll {
		return refuseVillage(village.HaulNoMoney, village.AddrHaul)
	}
	orderID := h.ids.NewID()
	if err := tx.Haul().AddOrder(ctx, application.HaulOrder{ID: orderID, From: s.CityID, To: other.CityID, OrderedBy: p.ID, PayerKind: "settlement", PayerID: s.CityID, Item: item, Qty: qty, Mode: "road",
		Fee: wage + toll, Status: application.HaulMoving, CreatedAt: now}); err != nil {
		return err
	}
	for i, c := range fit {
		if units[i] <= 0 {
			continue
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: item, Qty: units[i], FromOrg: org, FromHolding: application.HoldWarehouse, Reason: application.ItemHaulOut,
			ReferenceType: "haul_orders", ReferenceID: orderID, At: now}); err != nil {
			if stderrors.Is(err, application.ErrNotEnoughItems) {
				return refuseVillage(village.HaulNoStock, village.AddrHaul)
			}
			return err
		}
		loadID := h.ids.NewID()
		due := now.Add(dur)
		action, err := h.schedule(ctx, tx, HaulArriveActionType, "haul_loads", loadID, s.CityID, now, due)
		if err != nil {
			return err
		}
		if moved, err := tx.Haul().SetCarrierStatus(ctx, c.ID, application.CarrierIdle, application.CarrierMoving, "", now); err != nil {
			return err
		} else if !moved {
			return refuseVillage(village.HaulNoCarrier, village.AddrHaul)
		}
		if err := tx.Haul().AddLoad(ctx, application.HaulLoad{ID: loadID, OrderID: orderID, CarrierKind: "cart", CarrierID: c.ID, Item: item, Qty: units[i], Kg: units[i] * itemWeightG(snap, item) / 1000,
			From: s.CityID, To: other.CityID, DepartedAt: now, DueAt: due, GameActionID: action}); err != nil {
			return err
		}
	}
	if wage > 0 {
		if err := postToSink(ctx, tx, h.ids.NewID(), application.ReasonCarterWage, treasuryAcct.ID, wage, "haul_orders", orderID, now); err != nil {
			return err
		}
	}
	if toll > 0 {
		owner, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, link.Owner)
		if err != nil {
			return err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: h.ids.NewID(), Reason: application.ReasonRoadToll, CreatedAt: now, ReferenceType: "haul_orders", ReferenceID: orderID,
			Entries: []application.LedgerEntry{{AccountID: treasuryAcct.ID, Amount: moneyOf(-toll)}, {AccountID: owner.ID, Amount: moneyOf(toll)}}}); err != nil {
			return err
		}
	}
	return appendVillageEvent(ctx, tx, meta, "haul_sent", s.CityID, map[string]any{"settlement_id": s.CityID, "to": other.CityID, "item": item, "qty": qty, "order_id": orderID})
}

// HaulArrive handles haul.arrive from the SCHEDULER: goods reaching the end of the road. Exactly once (the load's arrival is the fence).
func (h *VillageHandler) HaulArrive(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return h.deliverLoad(ctx, tx, meta, in.ID, h.now())
	})
}

// deliverLoad puts a load into the destination store and frees its carrier; shared by the cart and the train.
func (h *VillageHandler) deliverLoad(ctx context.Context, tx application.Tx, meta envelope.Metadata, loadID string, now time.Time) error {
	l, err := tx.Haul().Load(ctx, loadID, true)
	if err != nil {
		return err
	}
	if l == nil || l.ArrivedAt != nil || now.Before(l.DueAt) {
		return nil
	}
	org := application.SettlementOrg(l.To)
	if err := tx.Items().LockOrg(ctx, org); err != nil {
		return err
	}
	fresh, err := tx.Haul().CloseLoad(ctx, l.ID, now)
	if err != nil || !fresh {
		return err
	}
	if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: l.Item, Qty: l.Qty, ToOrg: org, ToHolding: application.HoldWarehouse, Reason: application.ItemHaulIn,
		ReferenceType: "haul_loads", ReferenceID: l.ID, At: now}); err != nil {
		return err
	}
	if l.CarrierKind == "cart" {
		if _, err := tx.Haul().SetCarrierStatus(ctx, l.CarrierID, application.CarrierMoving, application.CarrierIdle, l.To, now); err != nil {
			return err
		}
	}
	if l.OrderID != "" {
		if err := h.settleOrder(ctx, tx, l.OrderID, now); err != nil {
			return err
		}
	}
	return appendVillageEvent(ctx, tx, meta, "haul_arrived", l.To, map[string]any{"settlement_id": l.To, "from": l.From, "item": l.Item, "qty": l.Qty, "load_id": l.ID})
}

// settleOrder marks a consignment delivered once none of its loads is on the road.
func (h *VillageHandler) settleOrder(ctx context.Context, tx application.Tx, orderID string, now time.Time) error {
	o, err := tx.Haul().Order(ctx, orderID, true)
	if err != nil {
		return err
	}
	if o.Status != application.HaulMoving {
		return nil
	}
	loads, err := tx.Haul().OpenLoads(ctx, o.To)
	if err != nil {
		return err
	}
	for _, l := range loads {
		if l.OrderID == orderID {
			return nil
		}
	}
	_, err = tx.Haul().SetOrderStatus(ctx, orderID, application.HaulMoving, application.HaulDelivered, now)
	return err
}

// haulView builds the haul screen.
func (h *VillageHandler) haulView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player) (village.HaulView, error) {
	v := village.HaulView{Village: s.Name, Open: h.haulOpen(), MayOrder: hasPermission(ctx, tx, s, p.ID, charter.HaulOrder), MayDraw: hasPermission(ctx, tx, s, p.ID, charter.RoadDraw)}
	if !h.mining.Enabled() {
		return v, nil
	}
	now := h.now()
	nameOf := map[string]presentation.Named{s.CityID: named(s.Code, s.Name)}
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return v, err
	}
	for _, f := range founded {
		nameOf[f.CityID] = named(f.Code, f.Name)
	}
	carriers, err := tx.Haul().CarriersOf(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	for _, c := range carriers {
		v.Carriers = append(v.Carriers, village.HaulCarrierLine{ID: c.ID, Mode: named(c.Mode, c.Mode), Kind: c.Kind, CapacityKg: c.CapacityKg, Status: c.Status, Settlement: nameOf[c.SettlementID]})
	}
	links, err := tx.Haul().RoadLinks(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	linked := map[string]bool{s.CityID: true}
	for _, l := range links {
		other := l.A
		if other == s.CityID {
			other = l.B
		}
		linked[other] = true
		v.Links = append(v.Links, village.HaulLinkLine{ID: l.ID, Other: nameOf[other], Class: l.Class, LengthM: l.LengthM, ConditionBPS: l.ConditionBPS, Toll: l.Toll, Status: l.Status, Mine: l.Owner == s.CityID})
	}
	for _, f := range founded {
		if !linked[f.CityID] {
			v.Targets = append(v.Targets, named(f.Code, f.Name))
		}
	}
	loads, err := tx.Haul().OpenLoads(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	for _, l := range loads {
		v.Loads = append(v.Loads, village.HaulLoadLine{ID: l.ID, Item: named(l.Item, snapItemName(snap, l.Item)), Qty: l.Qty, From: nameOf[l.From], To: nameOf[l.To], DueAt: l.DueAt, Left: countdownTo(l.DueAt, now), Carrier: l.CarrierID})
	}
	orders, err := tx.Haul().OrdersOf(ctx, s.CityID, 10)
	if err != nil {
		return v, err
	}
	for _, o := range orders {
		v.Orders = append(v.Orders, village.HaulOrderLine{ID: o.ID, Item: named(o.Item, snapItemName(snap, o.Item)), Qty: o.Qty, From: nameOf[o.From], To: nameOf[o.To], Mode: o.Mode, Fee: o.Fee, Status: o.Status, At: o.CreatedAt})
	}
	st, _, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return v, err
	}
	for _, m := range snap.HaulModes() {
		if m.Kind != "cart" {
			continue
		}
		line := village.HaulModeLine{Mode: named(m.Code, m.Name), CapacityKg: int64(m.CapacityKG), Surface: m.Surface, Cost: materialLinesOf(snap, intQty(m.BuildMaterials)), Wage: int64(m.BuildHours) * h.mining.RailWorkHourWage, Hours: int64(m.BuildHours)}
		if m.Requires != nil {
			for _, k := range m.Requires.Knowledge {
				if _, have := st.Owned[k]; !have {
					line.Missing = append(line.Missing, named(k, k))
				}
			}
		}
		line.CanOrder = len(line.Missing) == 0
		v.Modes = append(v.Modes, line)
	}
	return v, nil
}

func snapItemName(snap *content.Snapshot, code string) string {
	if c, ok := snap.ComponentDef(code); ok {
		return c.Name
	}
	if d, ok := snap.ItemDef(code); ok {
		return d.Name
	}
	return code
}

func intQty(m map[string]int) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = int64(v)
	}
	return out
}

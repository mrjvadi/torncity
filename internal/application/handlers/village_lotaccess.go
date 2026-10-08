package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/domain/landroad"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Lot access (docs/adr/0043). A lot is only sold when a road can reach it;
// the road that serves a sold lot is laid at the sale, over public land, and
// becomes right-of-way in the same transaction, so no later sale can landlock
// it. A resident who holds a lot that no road reaches (bought before the rule,
// or cut off since) can have it connected at a fair price, carve the road out
// of their own land, or have the sale rescinded and the money returned.
//
// Every writer takes the settlement's land lock (CitizenRepository.LockLots)
// first, so two replicas never lay a corridor over a lot being sold.

// lotAccessScanMax is the biggest grid whose free lots are each priced for the
// land map; a bigger one shows no per-lot road state (the buy popup always
// prices its own lot).
const lotAccessScanMax = 400

// VillageRepairRequest is the payload of settlement.lot.repair.
type VillageRepairRequest struct {
	Lot     string `json:"lot,omitempty"`
	Option  string `json:"option,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

// VillageAccessRequest is the payload of settlement.lot.access.
type VillageAccessRequest struct {
	Lot string `json:"lot,omitempty"`
}

func (h *VillageHandler) accessRules() settlementbuilding.AccessRules {
	return settlementbuilding.AccessRules{
		RoadLotCost: h.autoRoadCost, CrossingLotCost: h.citizen.CrossingLotCost, MaxCrossing: h.citizen.MaxCrossing,
	}
}

// landAccess is the village's land as the road router sees it.
type landAccess struct {
	h        *VillageHandler
	pic      *landPicture
	grid     settlementbuilding.Grid
	existing []application.SettlementBuildingInstance
	amap     settlementbuilding.AccessMap
	rules    settlementbuilding.AccessRules
}

// networkLots is every road lot that holds its lot plus the civic hall's
// footprint: a lot beside one is connected.
func (h *VillageHandler) networkLots(existing []application.SettlementBuildingInstance) [][2]int {
	snap := h.content.Current()
	var out [][2]int
	for _, b := range existing {
		if !b.Holds() {
			continue
		}
		switch b.TypeCode {
		case "road":
			out = append(out, [2]int{b.LotX, b.LotY})
		case "civic_hall":
			def := settlementbuilding.Def{FootprintW: 1, FootprintH: 1}
			if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
				def = d.Def()
				if b.Rotated {
					def = def.Rotate()
				}
			}
			out = append(out, footprintOf(def, b.LotX, b.LotY)...)
		}
	}
	return out
}

// landAccess reads the land. With persist, the streets a village of this size
// platts ahead are recorded as right-of-way; without it they are only counted
// in the picture (they are the same lots, being a pure function of the land).
func (h *VillageHandler) landAccess(ctx context.Context, tx application.Tx, sc *citizenScope, persist bool, now time.Time,
) (*landAccess, error) {
	w, err := h.world(ctx)
	if err != nil {
		return nil, err
	}
	pic, err := h.picture(ctx, tx, w, sc.s, sc.lots)
	if err != nil {
		return nil, err
	}
	grid, existing := pic.grid, pic.existing
	network := h.networkLots(existing)
	held := make(map[[2]int]string, len(sc.lots))
	for _, l := range sc.lots {
		if pic.inGrid(l.X, l.Y) {
			held[[2]int{l.X, l.Y}] = l.OwnerID
		}
	}
	reserved := map[[2]int]bool{}
	for y := range grid {
		for x := range grid[y] {
			if grid[y][x].Reserved {
				reserved[[2]int{x, y}] = true
			}
		}
	}
	if h.citizen.StreetPitch > 0 && len(grid) >= h.citizen.StreetPlanMinGrid && h.citizen.StreetPlanMinGrid > 0 {
		var fresh []application.RoadReserve
		for _, p := range settlementbuilding.PlanStreets(grid, network, held, h.citizen.StreetPitch) {
			if reserved[p] {
				continue
			}
			reserved[p] = true
			grid[p[1]][p[0]].Reserved = true
			fresh = append(fresh, application.RoadReserve{SettlementID: sc.s.CityID, X: p[0], Y: p[1], Kind: application.ReservePlan, CreatedAt: now})
		}
		if persist && len(fresh) > 0 {
			if err := tx.Citizens().ReserveRoad(ctx, fresh); err != nil {
				return nil, err
			}
		}
	}
	return &landAccess{
		h: h, pic: pic,
		grid: grid, existing: existing, rules: h.accessRules(),
		amap: settlementbuilding.AccessMap{Grid: grid, Network: network, Held: held, Reserved: reserved},
	}, nil
}

// onOffer reports whether a lot is for sale: bare, buildable, not right-of-way
// and without an owner.
func (la *landAccess) onOffer(x, y int) bool {
	if !la.pic.inGrid(x, y) {
		o, open := la.pic.open[landroad.Lot{X: x, Y: y}]
		_, road := la.pic.cells[landroad.Lot{X: x, Y: y}]
		_, stands := la.pic.occ[[2]int{x, y}]
		_, held := la.pic.held[[2]int{x, y}]
		return open && o.Buildable && !road && !stands && !held
	}
	g := la.grid[y][x]
	if !g.Buildable || g.Occupied || g.Reserved {
		return false
	}
	_, held := la.amap.Held[[2]int{x, y}]
	return !held
}

// lotVerdict is a lot's access by public land, and the way in through the
// asker's own land when no public road reaches it.
type lotVerdict struct {
	base  settlementbuilding.Access
	carve *settlementbuilding.Access
}

func (la *landAccess) verdict(lot [2]int, asker string) lotVerdict {
	if !la.pic.inGrid(lot[0], lot[1]) {
		return lotVerdict{base: la.h.outerAccess(la.pic, lot, asker)}
	}
	v := lotVerdict{base: la.amap.Plan(lot, asker, false, la.rules)}
	if v.base.Kind == settlementbuilding.AccessNone {
		if c := la.amap.Plan(lot, asker, true, la.rules); c.Feasible() && len(c.Carved) > 0 {
			v.carve = &c
		}
	}
	return v
}

func lotRefs(ps [][2]int) []village.LotRef {
	if len(ps) == 0 {
		return nil
	}
	out := make([]village.LotRef, len(ps))
	for i, p := range ps {
		out[i] = village.LotRef{X: p[0], Y: p[1]}
	}
	return out
}

func accessInfo(a settlementbuilding.Access) village.LotAccess {
	return village.LotAccess{
		Kind: string(a.Kind), Roads: a.Roads() + len(a.Carved), Crossings: a.Crossings, Cost: a.Cost,
		Carved: lotRefs(a.Carved), Path: lotRefs(a.Path),
	}
}

func carveInfo(a *settlementbuilding.Access) *village.LotAccess {
	if a == nil {
		return nil
	}
	v := accessInfo(*a)
	return &v
}

// nearby lists the lots on offer that can be served, the closest first.
func (h *VillageHandler) nearby(la *landAccess, lot [2]int, asker string) []village.LotNearby {
	var out []village.LotNearby
	for _, n := range la.amap.Nearby(lot, asker, la.rules, la.onOffer, 3) {
		out = append(out, village.LotNearby{X: n.X, Y: n.Y, Distance: n.Distance, Access: accessInfo(n.Access)})
	}
	return out
}

// lotAccessView is the answer for one lot: its access and what can be done.
func (h *VillageHandler) lotAccessView(ctx context.Context, tx application.Tx, sc *citizenScope, la *landAccess, x, y int,
) (village.LotAccessView, error) {
	_, cash, err := playerCash(ctx, tx, sc.p.ID)
	if err != nil {
		return village.LotAccessView{}, err
	}
	v := la.verdict([2]int{x, y}, sc.p.ID)
	view := village.LotAccessView{
		Village: sc.s.Name, SettlementID: sc.s.CityID, X: x, Y: y, Cash: cash,
		Access: accessInfo(v.base), Carve: carveInfo(v.carve),
	}
	if l, ok := sc.lotAt(x, y); ok && l.OwnerID == sc.p.ID {
		view.Own, view.Price, view.Refund = true, l.Price, l.Price
	} else if v.base.Kind == settlementbuilding.AccessNone || v.base.Kind == settlementbuilding.AccessNeedsBridge {
		view.Nearby = h.nearby(la, [2]int{x, y}, sc.p.ID)
	}
	return view, nil
}

// LotAccess handles settlement.lot.access: how a lot is served by road, and
// what a holder can do about it.
func (h *VillageHandler) LotAccess(ctx context.Context, meta envelope.Metadata, req VillageAccessRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.LotAccessView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, false)
		if err != nil {
			return err
		}
		x, y, _, ok := village.ParseLotTokenAny(req.Lot)
		if !ok {
			return refuseVillage(village.VillageNotFound, village.AddrLand)
		}
		la, err := h.landAccess(ctx, tx, sc, false, h.now())
		if err != nil {
			return err
		}
		if !la.pic.inGrid(x, y) {
			if _, open := la.pic.open[landroad.Lot{X: x, Y: y}]; !open {
				return refuseVillage(village.VillageOutOfBounds, village.AddrLand)
			}
		}
		view, err = h.lotAccessView(ctx, tx, sc, la, x, y)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.LotAccessScreen(h.screen(meta, lang), view), nil
}

// layAccess lays the road of one connection: the carved lots of the payer go
// back to the village as road, the path is laid as finished road (a culvert or
// a footbridge where it crosses water), every lot of it becomes right-of-way
// serving the lot, the payer's cash pays the fee into the system sink, and the
// journal row says what was paid. It returns the roads laid for the event and
// the payer's lots that remain.
func (h *VillageHandler) layAccess(ctx context.Context, tx application.Tx, sc *citizenScope, a settlementbuilding.Access,
	lot [2]int, origin string, cashAcct application.Account, now time.Time,
) ([]map[string]any, []application.SettlementLot, error) {
	remaining := append([]application.SettlementLot(nil), sc.lots...)
	for _, p := range a.Carved {
		for i, l := range remaining {
			if l.X != p[0] || l.Y != p[1] || l.OwnerID != sc.p.ID {
				continue
			}
			ok, err := tx.Citizens().ReleaseLot(ctx, application.LotRelease{LotID: l.ID, Kind: application.ReleaseDedicated, At: now})
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				return nil, nil, refuseVillage(village.CitizenLotTaken, village.AddrLand)
			}
			remaining = append(remaining[:i], remaining[i+1:]...)
			break
		}
	}
	laid, err := h.layAutoRoads(ctx, tx, sc.s.CityID, a.Path, now)
	if err != nil {
		return nil, nil, err
	}
	rows := make([]application.RoadReserve, 0, len(a.Path))
	for _, p := range a.Path {
		rows = append(rows, application.RoadReserve{
			SettlementID: sc.s.CityID, X: p[0], Y: p[1], Kind: application.ReserveCorridor, ServesX: lot[0], ServesY: lot[1], CreatedAt: now,
		})
	}
	if err := tx.Citizens().ReserveRoad(ctx, rows); err != nil {
		return nil, nil, err
	}
	connID, feeTx := h.ids.NewID(), ""
	if a.Cost > 0 {
		feeTx = h.ids.NewID()
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: feeTx, Reason: application.ReasonSettlementLotRoad, CreatedAt: now,
			ReferenceType: application.LotConnectionReference, ReferenceID: connID,
			Entries: []application.LedgerEntry{
				{AccountID: cashAcct.ID, Amount: money.FromMinor(-a.Cost)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(a.Cost)},
			},
		}); err != nil {
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				return nil, nil, refuseVillage(village.CitizenNoCash, village.AddrLand)
			}
			return nil, nil, err
		}
	}
	if err := tx.Citizens().RecordConnection(ctx, application.LotConnection{
		ID: connID, SettlementID: sc.s.CityID, PlayerID: sc.p.ID, X: lot[0], Y: lot[1], Origin: origin,
		RoadLots: len(a.Path) - a.Crossings, CrossingLots: a.Crossings, CarvedLots: len(a.Carved), Fee: a.Cost,
		LedgerTransactionID: feeTx, CreatedAt: now,
	}); err != nil {
		return nil, nil, err
	}
	return laid, remaining, nil
}

// BuyLot handles settlement.lot.buy: a resident buys a free lot that a road can
// reach at the village's price, which goes to the village treasury, and the
// road that serves it is laid with the sale and paid by the buyer, like the
// street an abutting owner pays for. The first press shows the price, the road
// and its cost and changes nothing; the confirmed press buys. A lot no public
// road can reach is not sold: the answer says why, offers the way in through the
// buyer's own land when there is one (Road "carve", the buyer's consent) and the
// nearest lots that can be served.
func (h *VillageHandler) BuyLot(ctx context.Context, meta envelope.Metadata, req VillageLotRequest) (*presentation.Response, error) {
	lang := meta.Language
	// the road choice rides in either of the last two arguments, so a button's
	// positional (lot, "carve") and (lot, "confirm", "carve") both read right
	carveAsked := strings.TrimSpace(req.Road) == village.RepairCarve || strings.TrimSpace(req.Confirm) == village.RepairCarve
	confirmed := strings.TrimSpace(req.Confirm) == village.ResidenceConfirm
	var (
		view  village.LotBuyView
		done  bool
		offer *application.LocalOffer
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScopeWith(ctx, tx, meta, &lang, true, confirmed)
		if err != nil {
			return err
		}
		x, y, _, ok := village.ParseLotTokenAny(req.Lot)
		if !ok {
			return refuseVillage(village.VillageNotFound, village.AddrLand)
		}
		now := h.now()
		la, err := h.landAccess(ctx, tx, sc, confirmed, now)
		if err != nil {
			return err
		}
		grid := la.grid
		outer := !la.pic.inGrid(x, y)
		if outer {
			// land beyond the first grid is for sale only where a road opened it
			l := landroad.Lot{X: x, Y: y}
			_, open := la.pic.open[l]
			_, road := la.pic.cells[l]
			if !open && !road {
				return refuseVillage(village.VillageOutOfBounds, village.AddrLand)
			}
		}
		lot := la.pic.lotAt(x, y)
		if _, taken := sc.lotAt(x, y); taken {
			return refuseVillage(village.CitizenLotTaken, village.AddrLand)
		}
		switch {
		case !lot.Buildable:
			return refuseVillage(village.VillageUnbuildable, village.AddrLand)
		case lot.Reserved:
			return refuseVillage(village.CitizenLotReserved, village.AddrLand)
		case lot.Occupied:
			return refuseVillage(village.VillageOccupied, village.AddrLand)
		}
		if sc.ownedBy(sc.p.ID) >= h.citizen.MaxLotsPerPlayer {
			return refuseVillage(village.CitizenLotLimit, village.AddrLand)
		}
		// the cap on the private share of the first grid; land the roads opened
		// is not part of it
		if !outer && !h.zoningAllows(grid, la.pic.innerHeld()) {
			return refuseVillage(village.CitizenZoning, village.AddrLand)
		}
		cashAcct, cash, err := playerCash(ctx, tx, sc.p.ID)
		if err != nil {
			return err
		}
		treasury, err := treasuryBalance(ctx, tx, sc.s.CityID)
		if err != nil {
			return err
		}

		// how the lot is served: by public land, or through the buyer's own land
		// when the buyer consents and no public road reaches it
		v := la.verdict([2]int{x, y}, sc.p.ID)
		chosen, road := v.base, ""
		if carveAsked && v.carve != nil {
			chosen, road = *v.carve, village.RepairCarve
		}
		view = village.LotBuyView{
			Village: sc.s.Name, SettlementID: sc.s.CityID, X: x, Y: y, Price: sc.price, Cash: cash, Treasury: treasury,
			Access: accessInfo(chosen), Carve: carveInfo(v.carve), Road: road, Total: sc.price + chosen.Cost,
		}
		if !chosen.Feasible() {
			view.Nearby = h.nearby(la, [2]int{x, y}, sc.p.ID)
			if confirmed {
				return refuseVillage(village.CitizenNoAccess, village.AddrLand)
			}
			return nil
		}
		if !confirmed {
			if offer, err = application.LocalOfferFor(ctx, tx, sc.s.CityID, sc.p.ID, sc.price, 0); err != nil {
				return err
			}
			need := view.Total
			if offer != nil && offer.Local {
				need -= sc.price // the lot is paid in the village's money
			}
			if cash < need {
				return refuseVillage(village.CitizenNoCash, village.AddrLand)
			}
			return nil
		}

		fresh, err := h.reserve(ctx, tx, sc.p.ID, meta)
		if err != nil {
			return err
		}
		done = true
		if !fresh {
			return nil
		}
		lotID, txID := h.ids.NewID(), h.ids.NewID()
		if err := tx.Citizens().InsertLot(ctx, application.SettlementLot{
			ID: lotID, SettlementID: sc.s.CityID, X: x, Y: y, Tenure: application.TenureFreehold, OwnerID: sc.p.ID,
			Price: sc.price, LedgerTransactionID: txID, AcquiredAt: now,
		}); err != nil {
			if stderrors.Is(err, application.ErrLotTaken) {
				return refuseVillage(village.CitizenLotTaken, village.AddrLand)
			}
			return err
		}
		// the lot is paid in the village's own money when the buyer holds the units, or converts at the
		// desk inside this confirm; otherwise in SUP, as before. The road is laid for SUP either way.
		lr, lerr := application.PayLocal(ctx, tx, h.ids.NewID, application.LocalPayment{
			SettlementID: sc.s.CityID, PlayerID: sc.p.ID, Direction: application.LocalCollect, Flow: application.ReasonSettlementLotSale,
			SUP: sc.price, TxID: txID, RefType: application.SettlementLotReference, RefID: lotID, At: now,
			Convert: req.wantsConvert(), MaxConvertSUP: req.maxSUP(),
		})
		if lerr != nil {
			if r, ok := deskRefusal(lerr); ok {
				return r
			}
			return lerr
		}
		cash -= lr.ConvertSUP
		need := view.Total
		if lr.Paid {
			need -= sc.price
		}
		if cash < need {
			return refuseVillage(village.CitizenNoCash, village.AddrLand)
		}
		treasuryAcct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, sc.s.CityID)
		if err != nil {
			return err
		}
		if !lr.Paid {
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
				ID: txID, Reason: application.ReasonSettlementLotSale, CreatedAt: now,
				ReferenceType: application.SettlementLotReference, ReferenceID: lotID,
				Entries: []application.LedgerEntry{
					{AccountID: cashAcct.ID, Amount: money.FromMinor(-sc.price)},
					{AccountID: treasuryAcct.ID, Amount: money.FromMinor(sc.price)},
				},
			}); err != nil {
				if stderrors.Is(err, application.ErrInsufficientFunds) {
					return refuseVillage(village.CitizenNoCash, village.AddrLand)
				}
				return err
			}
		}
		laid, remaining, err := h.layAccess(ctx, tx, sc, chosen, [2]int{x, y}, application.ConnectionBuy, cashAcct, now)
		if err != nil {
			return err
		}
		view.Cash, view.Treasury = cash-need, treasury+sc.price
		lots := append(remaining, application.SettlementLot{
			ID: lotID, SettlementID: sc.s.CityID, X: x, Y: y, Tenure: application.TenureFreehold, OwnerID: sc.p.ID, Price: sc.price,
		})
		return h.appendTenureEvent(ctx, tx, meta, sc.s, "lot_bought", lots, map[string]any{
			"settlement_id": sc.s.CityID, "player_id": sc.p.ID, "player_name": sc.p.DisplayName, "lot_x": x, "lot_y": y,
			"price": sc.price, "lot_id": lotID, "auto_roads": laid, "road_cost": chosen.Cost,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	c := h.screen(meta, lang)
	if done {
		return village.LotBuyDone(c, view), nil
	}
	return attachOffer(village.LotBuyConfirm(c, view), offer, 3), nil
}

// RepairLot handles settlement.lot.repair: a resident puts right a lot of their
// own that no road reaches, one of three ways: connect it (the road over
// public land at its price), carve the road out of their own land, or have the
// sale rescinded and the price returned from the treasury. Without confirm it
// answers the lot's access screen and changes nothing.
func (h *VillageHandler) RepairLot(ctx context.Context, meta envelope.Metadata, req VillageRepairRequest) (*presentation.Response, error) {
	lang := meta.Language
	confirmed := strings.TrimSpace(req.Confirm) == village.ResidenceConfirm
	var (
		access *village.LotAccessView
		result *village.LotRepairView
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScopeWith(ctx, tx, meta, &lang, true, confirmed)
		if err != nil {
			return err
		}
		x, y, _, ok := village.ParseLotToken(req.Lot)
		if !ok {
			return refuseVillage(village.VillageNotFound, village.AddrMine)
		}
		mine, ok := sc.lotAt(x, y)
		if !ok || mine.OwnerID != sc.p.ID {
			return refuseVillage(village.CitizenNotOwner, village.AddrMine)
		}
		now := h.now()
		la, err := h.landAccess(ctx, tx, sc, confirmed, now)
		if err != nil {
			return err
		}
		if y >= len(la.grid) || x >= len(la.grid[y]) || la.grid[y][x].Occupied {
			// a lot with a building on it already has its road
			return refuseVillage(village.CitizenNoOption, village.AddrMine)
		}
		if !confirmed {
			v, err := h.lotAccessView(ctx, tx, sc, la, x, y)
			access = &v
			return err
		}
		fresh, err := h.reserve(ctx, tx, sc.p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		cashAcct, cash, err := playerCash(ctx, tx, sc.p.ID)
		if err != nil {
			return err
		}
		v := la.verdict([2]int{x, y}, sc.p.ID)
		option := strings.TrimSpace(req.Option)
		result = &village.LotRepairView{Village: sc.s.Name, SettlementID: sc.s.CityID, X: x, Y: y, Option: option}
		var remaining []application.SettlementLot
		var laid []map[string]any
		switch option {
		case village.RepairConnect, village.RepairCarve:
			a := v.base
			if option == village.RepairCarve {
				if v.carve == nil {
					return refuseVillage(village.CitizenNoOption, village.AddrMine)
				}
				a = *v.carve
			} else if a.Kind != settlementbuilding.AccessNeedsRoad && a.Kind != settlementbuilding.AccessNeedsBridge {
				return refuseVillage(village.CitizenNoOption, village.AddrMine)
			}
			if cash < a.Cost {
				return refuseVillage(village.CitizenNoCash, village.AddrMine)
			}
			if laid, remaining, err = h.layAccess(ctx, tx, sc, a, [2]int{x, y}, application.ConnectionRepair, cashAcct, now); err != nil {
				return err
			}
			result.Paid, result.Cash = a.Cost, cash-a.Cost
			result.Roads, result.Crossings, result.Carved = a.Roads()+len(a.Carved), a.Crossings, len(a.Carved)
		case village.RepairRefund:
			if v.base.Kind == settlementbuilding.AccessRoad {
				return refuseVillage(village.CitizenNoOption, village.AddrMine)
			}
			treasuryAcct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, sc.s.CityID)
			if err != nil {
				return err
			}
			refundTx := h.ids.NewID()
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
				ID: refundTx, Reason: application.ReasonSettlementLotRefund, CreatedAt: now,
				ReferenceType: application.SettlementLotReference, ReferenceID: mine.ID,
				Entries: []application.LedgerEntry{
					{AccountID: treasuryAcct.ID, Amount: money.FromMinor(-mine.Price)},
					{AccountID: cashAcct.ID, Amount: money.FromMinor(mine.Price)},
				},
			}); err != nil {
				if stderrors.Is(err, application.ErrInsufficientFunds) {
					return refuseVillage(village.CitizenRefundTreasury, village.AddrMine)
				}
				return err
			}
			released, err := tx.Citizens().ReleaseLot(ctx, application.LotRelease{
				LotID: mine.ID, Kind: application.ReleaseRefund, At: now, RefundAmount: mine.Price, RefundLedgerTransactionID: refundTx,
			})
			if err != nil {
				return err
			}
			if !released {
				return refuseVillage(village.CitizenLotTaken, village.AddrMine)
			}
			for _, l := range sc.lots {
				if l.ID != mine.ID {
					remaining = append(remaining, l)
				}
			}
			result.Refund, result.Cash = mine.Price, cash+mine.Price
		default:
			return refuseVillage(village.CitizenNoOption, village.AddrMine)
		}
		return h.appendTenureEvent(ctx, tx, meta, sc.s, "lot_repaired", remaining, map[string]any{
			"settlement_id": sc.s.CityID, "player_id": sc.p.ID, "lot_x": x, "lot_y": y, "option": option,
			"paid": result.Paid, "refund": result.Refund, "auto_roads": laid,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	c := h.screen(meta, lang)
	if result != nil {
		return village.LotRepairDone(c, *result), nil
	}
	return village.LotAccessScreen(c, *access), nil
}

// setCellAccess writes a lot's road state onto its map cell.
func setCellAccess(c *village.LandCell, a settlementbuilding.Access) {
	c.Access, c.Cost = string(a.Kind), a.Cost
	c.Roads, c.Crossings = a.Roads()+len(a.Carved), a.Crossings
}

// noRoadAccess is the refusal of a private building that no road can reach: the
// lot's access and the ways to put it right, instead of a dead end. The lot
// is the cheapest of the footprint's own lots to connect.
func (h *VillageHandler) noRoadAccess(ctx context.Context, tx application.Tx, sc *citizenScope, def settlementbuilding.Def, x, y int,
	building presentation.Named,
) error {
	la, err := h.landAccess(ctx, tx, sc, false, h.now())
	if err != nil {
		return err
	}
	pick := [2]int{x, y}
	var best *settlementbuilding.Access
	for _, p := range footprintOf(def, x, y) {
		a := la.amap.Plan(p, sc.p.ID, false, la.rules)
		if !a.Feasible() {
			continue
		}
		if best == nil || a.Cost < best.Cost {
			pick, best = p, &a
		}
	}
	view, err := h.lotAccessView(ctx, tx, sc, la, pick[0], pick[1])
	if err != nil {
		return err
	}
	view.Building = building
	r := refuseVillage(village.VillageNoRoad)
	r.access = &view
	return r
}

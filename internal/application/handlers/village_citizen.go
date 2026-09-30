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
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The citizen loop (docs/adr/0033 sections 4.4 and 4.5, migration
// 0058_citizen_loop): a resident buys a free lot from the village (the price
// goes to the village treasury), builds a PRIVATE building on it from the
// citizen catalogue (citizen_buildings.yml) with their own cash, pays a
// permit to the treasury, and lives in the house. The village head sets the
// lot price, the permit and the property tax inside config bounds.
//
// It is additive: a private building is an ordinary settlement_buildings row
// (the grid, the layout and the construction timer are the village's own)
// plus a settlement_private_buildings row that names its owner. Only three
// places of the civic code know about it: the head's build menu skips citizen
// buildings, the head's placement refuses a lot someone owns, and the
// head's demolish/cancel refuse a resident's building.
//
// SEAMS left for the other phases:
//   - materials: planMaterials draws from the builder's inventory and buys
//     the rest at the reference price (a system sink). When the economy
//     phase adds a real material purchase path, replace materialSource.
//   - roads: after a private building is placed, roadsHook is called (nil
//     until the roads work exposes an automatic connection).
//   - work: Work is the page behind «کار کن»; it points at Support until
//     village producers exist.
//   - foreclosure: unpaid property tax stays a debt (settlement_property_tax
//     rows with paid_at NULL); reverting a private building to the village is
//     a later rule (docs/adr/0033 section 4.5).

// CitizenRules is the citizen loop's tuning (config settlement.citizen_*).
type CitizenRules struct {
	LotPrice, LotPriceMin, LotPriceMax int64
	PermitFee, PermitFeeMax            int64
	TaxBPS, TaxBPSMax                  int
	// TaxPeriod is GAME time.
	TaxPeriod          time.Duration
	MaterialMarkupBPS  int
	MaxLotsPerPlayer   int
	PrivateShareMaxBPS int
	// HomeRestCooldown is REAL time.
	HomeRestCooldown  time.Duration
	HomeRestHealth    int
	HomeRestHappiness int
}

func (r CitizenRules) enabled() bool { return r.LotPrice > 0 && r.TaxPeriod > 0 }

// Effective is the lot price, permit fee and tax the head's levers and the
// defaults come to, clamped into the bounds.
func (r CitizenRules) Effective(t application.LotTerms) (price, permit int64, tax int) {
	return application.CitizenBounds{
		LotPrice: r.LotPrice, LotPriceMin: r.LotPriceMin, LotPriceMax: r.LotPriceMax,
		PermitFee: r.PermitFee, PermitFeeMax: r.PermitFeeMax, TaxBPS: r.TaxBPS, TaxBPSMax: r.TaxBPSMax,
	}.Effective(t)
}

func clampInt64(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// WithCitizenRules sets the citizen loop's tuning; without it the loop is off.
func (h *VillageHandler) WithCitizenRules(r CitizenRules) *VillageHandler {
	h.citizen = r
	return h
}

// VillageLotRequest is the payload of settlement.lot.buy.
type VillageLotRequest struct {
	// Lot is "{x}-{y}" (screens.LotToken); a client sends x and y.
	Lot     string `json:"lot,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

// VillagePrivateRequest is the payload of the private building commands.
type VillagePrivateRequest struct {
	Code    string `json:"code,omitempty"`
	Lot     string `json:"lot,omitempty"`
	Rotate  string `json:"rotate,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

// VillageTermsRequest is the payload of settlement.terms: a lever to move,
// as text like every button argument.
type VillageTermsRequest struct {
	LotPrice  string `json:"lot_price,omitempty"`
	PermitFee string `json:"permit_fee,omitempty"`
	TaxBPS    string `json:"tax_bps,omitempty"`
}

// citizenScope is what every citizen command starts from: the player, the
// language, their village, the lots that have owners and the terms in force.
type citizenScope struct {
	p     *application.Player
	s     application.FoundedSettlement
	lots  []application.SettlementLot
	terms application.LotTerms
	price int64
	fee   int64
	tax   int
}

// citizenScope resolves the scope and requires the player to live in the
// village the command is about.
func (h *VillageHandler) citizenScope(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string,
	residentOnly bool,
) (*citizenScope, error) {
	if !h.citizen.enabled() {
		return nil, refuseVillage(screens.CitizenOff)
	}
	p, l, err := h.viewer(ctx, tx, meta)
	if err != nil {
		return nil, err
	}
	*lang = l
	s, err := h.settlementOf(ctx, tx, meta)
	if err != nil {
		return nil, err
	}
	if residentOnly {
		home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		if home != s.CityID {
			return nil, refuseVillage(screens.VillageNotResident)
		}
	}
	lots, err := tx.Citizens().Lots(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	terms, err := tx.Citizens().Terms(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	sc := &citizenScope{p: p, s: s, lots: lots, terms: terms}
	sc.price, sc.fee, sc.tax = h.citizen.Effective(terms)
	return sc, nil
}

func (sc *citizenScope) ownedBy(playerID string) int {
	n := 0
	for _, l := range sc.lots {
		if l.OwnerID == playerID {
			n++
		}
	}
	return n
}

func (sc *citizenScope) lotAt(x, y int) (application.SettlementLot, bool) {
	for _, l := range sc.lots {
		if l.X == x && l.Y == y {
			return l, true
		}
	}
	return application.SettlementLot{}, false
}

// cashOf reads a player's own cash.
func playerCash(ctx context.Context, tx application.Tx, playerID string) (application.Account, int64, error) {
	acct, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, playerID)
	if err != nil {
		return application.Account{}, 0, err
	}
	bal, err := tx.Ledger().Balance(ctx, acct.ID)
	if err != nil {
		return application.Account{}, 0, err
	}
	return acct, bal.Minor(), nil
}

// buildableLots counts the lots the village grid can be built on.
func buildableLots(grid settlementbuilding.Grid) int {
	n := 0
	for y := range grid {
		for x := range grid[y] {
			if grid[y][x].Buildable {
				n++
			}
		}
	}
	return n
}

// zoningAllows reports whether one more private lot keeps the private share
// of the buildable lots within the cap.
func (h *VillageHandler) zoningAllows(grid settlementbuilding.Grid, privateLots int) bool {
	b := buildableLots(grid)
	return b > 0 && int64(privateLots+1)*10_000 <= int64(b)*int64(h.citizen.PrivateShareMaxBPS)
}

// ---------------------------------------------------------------------
// Land: the grid of who owns what, and buying a free lot
// ---------------------------------------------------------------------

// ownerNames reads the display names of a set of players.
func ownerNames(ctx context.Context, tx application.Tx, lots []application.SettlementLot, self string) (map[string]string, error) {
	out := map[string]string{}
	for _, l := range lots {
		if l.OwnerID == self {
			continue
		}
		if _, ok := out[l.OwnerID]; ok {
			continue
		}
		p, err := tx.Players().GetByID(ctx, l.OwnerID)
		if err != nil {
			if stderrors.Is(err, application.ErrPlayerNotFound) {
				out[l.OwnerID] = ""
				continue
			}
			return nil, err
		}
		out[l.OwnerID] = p.DisplayName
	}
	return out, nil
}

// landView builds the land grid for one viewer.
func (h *VillageHandler) landView(ctx context.Context, tx application.Tx, sc *citizenScope) (screens.LandView, error) {
	w, err := h.world(ctx)
	if err != nil {
		return screens.LandView{}, err
	}
	grid, existing, err := h.grid(ctx, tx, w, sc.s)
	if err != nil {
		return screens.LandView{}, err
	}
	names, err := ownerNames(ctx, tx, sc.lots, sc.p.ID)
	if err != nil {
		return screens.LandView{}, err
	}
	_, cash, err := playerCash(ctx, tx, sc.p.ID)
	if err != nil {
		return screens.LandView{}, err
	}
	// which building stands on each lot
	stands := map[[2]int]string{}
	snap := h.content.Current()
	for _, b := range existing {
		if !b.Holds() {
			continue
		}
		fw, fh := 1, 1
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
			def := d.Def()
			if b.Rotated {
				def = def.Rotate()
			}
			fw, fh = def.FootprintW, def.FootprintH
		}
		for dy := 0; dy < fh; dy++ {
			for dx := 0; dx < fw; dx++ {
				stands[[2]int{b.LotX + dx, b.LotY + dy}] = b.TypeCode
			}
		}
	}
	owned := sc.ownedBy(sc.p.ID)
	v := screens.LandView{
		Village: sc.s.Name, SettlementID: sc.s.CityID, GridLots: len(grid),
		Price: sc.price, Cash: cash, Owned: owned, Max: h.citizen.MaxLotsPerPlayer,
	}
	for y := range grid {
		row := make([]screens.LandCell, 0, len(grid[y]))
		for x := range grid[y] {
			cell := screens.LandCell{X: x, Y: y, Building: stands[[2]int{x, y}]}
			lot := grid[y][x]
			owner, isOwned := sc.lotAt(x, y)
			switch {
			case isOwned && owner.OwnerID == sc.p.ID:
				cell.State = screens.LandMine
			case isOwned:
				cell.State = screens.LandTaken
				cell.Owner = names[owner.OwnerID]
			case !lot.Buildable && lotState(lot, false) == screens.LotSteep:
				cell.State = screens.LandSteep
			case !lot.Buildable:
				cell.State = screens.LandWater
			case lot.Occupied && cell.Building == "road":
				cell.State = screens.LandRoad
			case lot.Occupied:
				cell.State = screens.LandBuilding
			default:
				cell.State = screens.LandFree
				v.FreeLots++
			}
			row = append(row, cell)
		}
		v.Rows = append(v.Rows, row)
	}
	v.CanBuy = v.FreeLots > 0 && owned < h.citizen.MaxLotsPerPlayer && cash >= sc.price && h.zoningAllows(grid, len(sc.lots))
	return v, nil
}

// Land handles settlement.land: the grid of the village's lots, who holds
// what and which are on sale.
func (h *VillageHandler) Land(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.LandView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, false)
		if err != nil {
			return err
		}
		view, err = h.landView(ctx, tx, sc)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.LandGrid(h.screen(meta, lang), view), nil
}

// BuyLot handles settlement.lot.buy: a resident buys a free, buildable lot
// at the village's price, which goes to the village treasury. The first
// press shows the price and changes nothing; the confirmed press buys.
func (h *VillageHandler) BuyLot(ctx context.Context, meta envelope.Metadata, req VillageLotRequest) (*presenter.Response, error) {
	lang := meta.Language
	var (
		view screens.LotBuyView
		done bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, true)
		if err != nil {
			return err
		}
		x, y, _, ok := screens.ParseLotToken(req.Lot)
		if !ok {
			return refuseVillage(screens.VillageNotFound, screens.AddrLand)
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		grid, _, err := h.grid(ctx, tx, w, sc.s)
		if err != nil {
			return err
		}
		if y >= len(grid) || x >= len(grid[y]) {
			return refuseVillage(screens.VillageOutOfBounds, screens.AddrLand)
		}
		lot := grid[y][x]
		if _, taken := sc.lotAt(x, y); taken {
			return refuseVillage(screens.CitizenLotTaken, screens.AddrLand)
		}
		switch {
		case !lot.Buildable:
			return refuseVillage(screens.VillageUnbuildable, screens.AddrLand)
		case lot.Occupied:
			return refuseVillage(screens.VillageOccupied, screens.AddrLand)
		}
		if sc.ownedBy(sc.p.ID) >= h.citizen.MaxLotsPerPlayer {
			return refuseVillage(screens.CitizenLotLimit, screens.AddrLand)
		}
		if !h.zoningAllows(grid, len(sc.lots)) {
			return refuseVillage(screens.CitizenZoning, screens.AddrLand)
		}
		cashAcct, cash, err := playerCash(ctx, tx, sc.p.ID)
		if err != nil {
			return err
		}
		treasury, err := treasuryBalance(ctx, tx, sc.s.CityID)
		if err != nil {
			return err
		}
		view = screens.LotBuyView{Village: sc.s.Name, SettlementID: sc.s.CityID, X: x, Y: y, Price: sc.price, Cash: cash, Treasury: treasury}
		if cash < sc.price {
			return refuseVillage(screens.CitizenNoCash, screens.AddrLand)
		}
		if strings.TrimSpace(req.Confirm) != screens.ResidenceConfirm {
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
		now := h.now()
		lotID, txID := h.ids.NewID(), h.ids.NewID()
		if err := tx.Citizens().InsertLot(ctx, application.SettlementLot{
			ID: lotID, SettlementID: sc.s.CityID, X: x, Y: y, Tenure: application.TenureFreehold, OwnerID: sc.p.ID,
			Price: sc.price, LedgerTransactionID: txID, AcquiredAt: now,
		}); err != nil {
			if stderrors.Is(err, application.ErrLotTaken) {
				return refuseVillage(screens.CitizenLotTaken, screens.AddrLand)
			}
			return err
		}
		treasuryAcct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, sc.s.CityID)
		if err != nil {
			return err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: txID, Reason: application.ReasonSettlementLotSale, CreatedAt: now,
			ReferenceType: application.SettlementLotReference, ReferenceID: lotID,
			Entries: []application.LedgerEntry{
				{AccountID: cashAcct.ID, Amount: money.FromMinor(-sc.price)},
				{AccountID: treasuryAcct.ID, Amount: money.FromMinor(sc.price)},
			},
		}); err != nil {
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				return refuseVillage(screens.CitizenNoCash, screens.AddrLand)
			}
			return err
		}
		view.Cash, view.Treasury = cash-sc.price, treasury+sc.price
		lots := append(append([]application.SettlementLot(nil), sc.lots...), application.SettlementLot{
			ID: lotID, SettlementID: sc.s.CityID, X: x, Y: y, Tenure: application.TenureFreehold, OwnerID: sc.p.ID, Price: sc.price,
		})
		return h.appendTenureEvent(ctx, tx, meta, sc.s, "lot_bought", lots, map[string]any{
			"settlement_id": sc.s.CityID, "player_id": sc.p.ID, "player_name": sc.p.DisplayName, "lot_x": x, "lot_y": y,
			"price": sc.price, "lot_id": lotID,
		})
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	c := h.screen(meta, lang)
	if done {
		return screens.LotBuyDone(c, view), nil
	}
	return screens.LotBuyConfirm(c, view), nil
}

// appendTenureEvent writes an event that changes who owns what: like
// appendBuildingEvent, it carries the layout versions the picture will have
// once the transaction commits, and those include the tenure marks.
func (h *VillageHandler) appendTenureEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	s application.FoundedSettlement, name string, lots []application.SettlementLot, payload map[string]any,
) error {
	rows, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return err
	}
	priv, err := tx.Citizens().PrivateBuildings(ctx, s.CityID)
	if err != nil {
		return err
	}
	snap := h.content.Current()
	footprint := func(code string, rotated bool) (int, int) {
		if d, ok := snap.SettlementBuildingDef(code); ok {
			def := d.Def()
			if rotated {
				def = def.Rotate()
			}
			return def.FootprintW, def.FootprintH
		}
		return 1, 1
	}
	payload["layout_version"] = application.LayoutVersionsWithTenure(s.CityID, s.Tier, s.Name,
		gridLotsFor(h, s), rows, footprint, application.TenureMark(lots, priv))
	return appendVillageEvent(ctx, tx, meta, name, s.CityID, payload)
}

func gridLotsFor(h *VillageHandler, s application.FoundedSettlement) int {
	if n := h.gridLotsByTier[s.Tier]; n > 0 {
		return n
	}
	return h.villageGridLots
}

// ---------------------------------------------------------------------
// The citizen catalogue and building on one's own lot
// ---------------------------------------------------------------------

// citizenStanding is the standing a private building is judged with: the
// village's knowledge and literacy. Its own build cap does not apply to a
// resident's building (the village's cap is the head's construction queue).
func (h *VillageHandler) citizenStanding(ctx context.Context, tx application.Tx, s application.FoundedSettlement) (settlementbuilding.Standing, error) {
	snap := h.content.Current()
	st, capabilities, err := h.knowledgeStanding(ctx, tx, snap, s, nil, false)
	if err != nil {
		return settlementbuilding.Standing{}, err
	}
	built, err := builtRoleCounts(ctx, tx, snap, s.CityID)
	if err != nil {
		return settlementbuilding.Standing{}, err
	}
	return settlementbuilding.Standing{
		Knowledge: st.Owned, KnowledgeCapabilities: capabilities, Built: built,
		RunningBuilds: 0, ConcurrentCap: 1, LiteracyShareBPS: st.LiteracyShareBPS,
	}, nil
}

// privateAvailable reports whether the village can build a citizen building
// now: the progressive-disclosure rule (only what can be built is shown).
func privateAvailable(d content.SettlementBuildingDef, st settlementbuilding.Standing) bool {
	def := d.Def()
	for _, k := range def.RequiresKnowledge {
		if !st.Knowledge.Has(k) {
			return false
		}
	}
	for _, cp := range def.RequiresKnowledgeCapability {
		if !st.KnowledgeCapabilities.Has(cp) {
			return false
		}
	}
	return def.MinLiteracyShareBPS <= 0 || st.LiteracyShareBPS >= def.MinLiteracyShareBPS
}

// materialPlan is how one material of a private building is met.
type materialPlan struct {
	code            string
	name            string
	need, have, buy int64
	unit            int64
}

// materialsBill is the plan of a whole building.
type materialsBill struct {
	lines []materialPlan
	// boughtCost is what the bought units cost in cash; referenceValue is
	// what all the materials are worth at the reference price (assessed).
	boughtCost, referenceValue int64
}

// planMaterials meets a building's materials from what the builder carries
// and buys the rest at the reference price. It is the seam for the economy
// phase's material purchase: replace the pricing here, nothing else changes.
func (h *VillageHandler) planMaterials(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID string,
	def settlementbuilding.Def,
) (materialsBill, error) {
	var bill materialsBill
	if len(def.CostMaterials) == 0 {
		return bill, nil
	}
	stacks, _, err := tx.Items().Holdings(ctx, playerID, application.HoldCarried)
	if err != nil {
		return bill, err
	}
	carried := map[string]int64{}
	for _, s := range stacks {
		carried[s.Item] += s.Qty
	}
	for _, code := range sortedMaterialCodes(def) {
		need := def.CostMaterials[code]
		cd, _ := snap.ComponentDef(code)
		unit := cd.BasePrice * int64(h.citizen.MaterialMarkupBPS) / 10_000
		if cd.BasePrice > 0 && unit < 1 {
			unit = 1
		}
		have := carried[code]
		if have > need {
			have = need
		}
		buy := need - have
		bill.lines = append(bill.lines, materialPlan{code: code, name: cd.Name, need: need, have: have, buy: buy, unit: unit})
		bill.boughtCost += buy * unit
		bill.referenceValue += need * cd.BasePrice
	}
	return bill, nil
}

func (b materialsBill) screen() []screens.PrivateMaterial {
	out := make([]screens.PrivateMaterial, 0, len(b.lines))
	for _, l := range b.lines {
		out = append(out, screens.PrivateMaterial{
			Component: named(l.code, l.name), Need: l.need, Have: l.have, Buy: l.buy, BuyCost: l.buy * l.unit,
		})
	}
	return out
}

// PrivateMenu handles settlement.private: the citizen catalogue, showing
// only what the village can build now, with what each costs the player.
func (h *VillageHandler) PrivateMenu(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	lang := meta.Language
	snap := h.content.Current()
	var view screens.PrivateMenuView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, true)
		if err != nil {
			return err
		}
		st, err := h.citizenStanding(ctx, tx, sc.s)
		if err != nil {
			return err
		}
		_, cash, err := playerCash(ctx, tx, sc.p.ID)
		if err != nil {
			return err
		}
		free, err := h.freeOwnLots(ctx, tx, sc)
		if err != nil {
			return err
		}
		view = screens.PrivateMenuView{
			Village: sc.s.Name, SettlementID: sc.s.CityID, Cash: cash, OwnedLots: sc.ownedBy(sc.p.ID), FreeLots: len(free),
		}
		for _, code := range sortedBuildingCodes(snap) {
			d, _ := snap.SettlementBuildingDef(code)
			if !d.Private() || !privateAvailable(d, st) {
				continue
			}
			def := d.Def()
			bill, err := h.planMaterials(ctx, tx, snap, sc.p.ID, def)
			if err != nil {
				return err
			}
			total := d.CostMoney + sc.fee + bill.boughtCost
			view.Lines = append(view.Lines, screens.PrivateLine{
				Building: named(d.Code, d.Name), Home: d.Home, Class: d.PermitClass, CostMoney: d.CostMoney, PermitFee: sc.fee,
				Materials: bill.screen(), BuildTime: h.scale.RealWait(def.BuildTime),
				FootprintW: def.FootprintW, FootprintH: def.FootprintH, Total: total, Affordable: cash >= total,
			})
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.PrivateMenu(h.screen(meta, lang), view), nil
}

// freeOwnLots lists the player's own lots that carry no building.
func (h *VillageHandler) freeOwnLots(ctx context.Context, tx application.Tx, sc *citizenScope) ([][2]int, error) {
	w, err := h.world(ctx)
	if err != nil {
		return nil, err
	}
	grid, _, err := h.grid(ctx, tx, w, sc.s)
	if err != nil {
		return nil, err
	}
	var out [][2]int
	for _, l := range sc.lots {
		if l.OwnerID == sc.p.ID && l.Y < len(grid) && l.X < len(grid[l.Y]) && grid[l.Y][l.X].Buildable && !grid[l.Y][l.X].Occupied {
			out = append(out, [2]int{l.X, l.Y})
		}
	}
	return out, nil
}

// footprintFitsOwn reports whether every lot of a footprint at (x, y) is the
// player's own.
func footprintFitsOwn(sc *citizenScope, def settlementbuilding.Def, x, y int) bool {
	for dy := 0; dy < def.FootprintH; dy++ {
		for dx := 0; dx < def.FootprintW; dx++ {
			l, ok := sc.lotAt(x+dx, y+dy)
			if !ok || l.OwnerID != sc.p.ID {
				return false
			}
		}
	}
	return true
}

// PrivateLots handles settlement.private.lots: the grid a private building's
// lot is chosen from; a cell fits only where the whole footprint is the
// player's own free land.
func (h *VillageHandler) PrivateLots(ctx context.Context, meta envelope.Metadata, req VillagePrivateRequest) (*presenter.Response, error) {
	lang := meta.Language
	snap := h.content.Current()
	var view screens.PrivateLotsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, true)
		if err != nil {
			return err
		}
		d, ok := snap.SettlementBuildingDef(strings.TrimSpace(req.Code))
		if !ok || !d.Private() {
			return refuseVillage(screens.VillageNotFound, screens.AddrPrivateMenu)
		}
		def := d.Def()
		rotated := strings.TrimSpace(req.Rotate) == "1" && def.CanRotate()
		if rotated {
			def = def.Rotate()
		}
		st, err := h.citizenStanding(ctx, tx, sc.s)
		if err != nil {
			return err
		}
		if !privateAvailable(d, st) {
			return refuseVillage(screens.VillagePrerequisite, screens.AddrPrivateMenu)
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		grid, existing, err := h.grid(ctx, tx, w, sc.s)
		if err != nil {
			return err
		}
		view = screens.PrivateLotsView{
			Village: sc.s.Name, Building: named(d.Code, d.Name), CanRotate: d.Def().CanRotate(), Rotated: rotated, GridLots: len(grid),
		}
		for y := range grid {
			row := make([]screens.LotCell, 0, len(grid[y]))
			for x := range grid[y] {
				lot := grid[y][x]
				road := false
				for _, b := range existing {
					if b.Holds() && b.LotX == x && b.LotY == y && b.TypeCode == "road" {
						road = true
					}
				}
				cell := screens.LotCell{X: x, Y: y, State: lotState(lot, lot.Occupied && road)}
				cell.Fits = settlementbuilding.CanPlace(def, grid, x, y, st) == nil && footprintFitsOwn(sc, def, x, y)
				row = append(row, cell)
			}
			view.Rows = append(view.Rows, row)
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.PrivateLots(h.screen(meta, lang), view), nil
}

// PrivatePlace handles settlement.private.place: a resident builds a private
// building on their own lot. The first press shows the bill (cost, permit,
// bought materials) and changes nothing; the confirmed press pays, draws the
// materials the player carries, records the building as theirs and starts
// the construction timer.
func (h *VillageHandler) PrivatePlace(ctx context.Context, meta envelope.Metadata, req VillagePrivateRequest) (*presenter.Response, error) {
	lang := meta.Language
	snap := h.content.Current()
	var confirmView *screens.PrivateConfirmView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, true)
		if err != nil {
			return err
		}
		d, ok := snap.SettlementBuildingDef(strings.TrimSpace(req.Code))
		if !ok || !d.Private() {
			return refuseVillage(screens.VillageNotFound, screens.AddrPrivateMenu)
		}
		x, y, rotated, ok := screens.ParseLotToken(req.Lot)
		if !ok {
			return refuseVillage(screens.VillageNotFound, screens.AddrPrivateMenu)
		}
		def := d.Def()
		rotated = rotated && def.CanRotate()
		if rotated {
			def = def.Rotate()
		}
		st, err := h.citizenStanding(ctx, tx, sc.s)
		if err != nil {
			return err
		}
		if !privateAvailable(d, st) {
			return refuseVillage(screens.VillagePrerequisite, screens.AddrPrivateMenu)
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		grid, _, err := h.grid(ctx, tx, w, sc.s)
		if err != nil {
			return err
		}
		if cerr := settlementbuilding.CanPlace(def, grid, x, y, st); cerr != nil {
			return buildingRefusalTo(cerr, screens.AddrPrivateMenu)
		}
		if !footprintFitsOwn(sc, def, x, y) {
			return refuseVillage(screens.CitizenNotOwner, screens.AddrPrivateMenu)
		}

		if !strings.EqualFold(strings.TrimSpace(req.Confirm), screens.VillageBuildConfirm) {
			bill, err := h.planMaterials(ctx, tx, snap, sc.p.ID, def)
			if err != nil {
				return err
			}
			_, cash, err := playerCash(ctx, tx, sc.p.ID)
			if err != nil {
				return err
			}
			total := d.CostMoney + sc.fee + bill.boughtCost
			confirmView = &screens.PrivateConfirmView{
				Village: sc.s.Name, Building: named(d.Code, d.Name), X: x, Y: y, Rotated: rotated,
				CostMoney: d.CostMoney, PermitFee: sc.fee, Materials: bill.screen(), MaterialsCost: bill.boughtCost,
				Total: total, Cash: cash, BuildTime: h.scale.RealWait(def.BuildTime),
			}
			if cash < total {
				return refuseVillage(screens.CitizenNoCash, screens.AddrPrivateMenu)
			}
			return nil
		}

		fresh, err := h.reserve(ctx, tx, sc.p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		if err := tx.Items().LockOwner(ctx, sc.p.ID); err != nil {
			return err
		}
		bill, err := h.planMaterials(ctx, tx, snap, sc.p.ID, def)
		if err != nil {
			return err
		}
		cashAcct, cash, err := playerCash(ctx, tx, sc.p.ID)
		if err != nil {
			return err
		}
		total := d.CostMoney + sc.fee + bill.boughtCost
		if cash < total {
			return refuseVillage(screens.CitizenNoCash, screens.AddrPrivateMenu)
		}

		now := h.now()
		id := h.ids.NewID()
		for _, m := range bill.lines {
			if m.have <= 0 {
				continue
			}
			if err := tx.Items().Move(ctx, application.ItemMove{
				Item: m.code, Qty: m.have, From: sc.p.ID, FromHolding: application.HoldCarried,
				Reason: application.ItemSettlementConstruction, ReferenceType: "settlement_building", ReferenceID: id, At: now,
			}); err != nil {
				if stderrors.Is(err, application.ErrNotEnoughItems) {
					return refuseVillage(screens.VillageMaterials, screens.AddrPrivateMenu)
				}
				return err
			}
		}
		finish := now.Add(h.scale.RealWait(def.BuildTime))
		if err := tx.SettlementBuildings().Place(ctx, application.SettlementBuildingInstance{
			ID: id, SettlementID: sc.s.CityID, TypeCode: d.Code, LotX: x, LotY: y, Status: "building", QueuedAt: now,
			Rotated: rotated, FinishAt: &finish,
		}); err != nil {
			if stderrors.Is(err, application.ErrLotOccupied) {
				return refuseVillage(screens.VillageOccupied, screens.AddrPrivateMenu)
			}
			return err
		}
		if _, err := h.schedule(ctx, tx, application.SettlementBuildActionType, "settlement_building", id, sc.s.CityID, now, finish); err != nil {
			return err
		}
		permitTx := ""
		if sc.fee > 0 {
			permitTx = h.ids.NewID()
		}
		if err := tx.Citizens().RecordPrivateBuilding(ctx, application.PrivateBuilding{
			BuildingID: id, SettlementID: sc.s.CityID, OwnerID: sc.p.ID, PermitFee: sc.fee, ConstructionPaid: d.CostMoney,
			MaterialsPaid: bill.boughtCost, AssessedValue: d.CostMoney + bill.referenceValue, LedgerTransactionID: permitTx, CreatedAt: now,
		}); err != nil {
			return err
		}
		treasuryAcct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, sc.s.CityID)
		if err != nil {
			return err
		}
		pay := func(txID string, reason application.Reason, to string, amount int64) error {
			if amount <= 0 {
				return nil
			}
			_, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
				ID: txID, Reason: reason, CreatedAt: now,
				ReferenceType: application.PrivateBuildingReference, ReferenceID: id,
				Entries: []application.LedgerEntry{
					{AccountID: cashAcct.ID, Amount: money.FromMinor(-amount)},
					{AccountID: to, Amount: money.FromMinor(amount)},
				},
			})
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				return refuseVillage(screens.CitizenNoCash, screens.AddrPrivateMenu)
			}
			return err
		}
		if err := pay(permitTx, application.ReasonSettlementPermitFee, treasuryAcct.ID, sc.fee); err != nil {
			return err
		}
		if err := pay("", application.ReasonCitizenConstruction, application.SystemSinkAccountID, d.CostMoney); err != nil {
			return err
		}
		if err := pay("", application.ReasonCitizenMaterials, application.SystemSinkAccountID, bill.boughtCost); err != nil {
			return err
		}
		if h.roadsHook != nil {
			if err := h.roadsHook(ctx, tx, sc.s, id); err != nil {
				return err
			}
		}
		priv, err := tx.Citizens().PrivateBuildings(ctx, sc.s.CityID)
		if err != nil {
			return err
		}
		rows, err := tx.SettlementBuildings().List(ctx, sc.s.CityID)
		if err != nil {
			return err
		}
		footprint := func(code string, rot bool) (int, int) {
			if bd, ok := snap.SettlementBuildingDef(code); ok {
				df := bd.Def()
				if rot {
					df = df.Rotate()
				}
				return df.FootprintW, df.FootprintH
			}
			return 1, 1
		}
		payload := map[string]any{
			"settlement_id": sc.s.CityID, "building_id": id, "type_code": d.Code, "name": d.Name, "lot_x": x, "lot_y": y,
			"rotated": rotated, "finish_at": finish.UTC().Format(time.RFC3339), "private": true, "owner_id": sc.p.ID,
			"layout_version": application.LayoutVersionsWithTenure(sc.s.CityID, sc.s.Tier, sc.s.Name, gridLotsFor(h, sc.s), rows,
				footprint, application.TenureMark(sc.lots, priv)),
		}
		return appendVillageEvent(ctx, tx, meta, "build_started", sc.s.CityID, payload)
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirmView != nil {
		return screens.PrivateConfirm(h.screen(meta, lang), *confirmView), nil
	}
	return h.mine(ctx, meta, "")
}

// RoadsHook connects a new private building to the road network; nil until
// the roads work exposes it. It runs in the placement's transaction.
type RoadsHook func(ctx context.Context, tx application.Tx, s application.FoundedSettlement, buildingID string) error

// WithRoadsHook sets the automatic-roads seam.
func (h *VillageHandler) WithRoadsHook(f RoadsHook) *VillageHandler {
	h.roadsHook = f
	return h
}

func buildingRefusalTo(err error, back string) *villageRefusal {
	r := buildingRefusal(err)
	r.back = back
	return r
}

// privateLotSet is the set of lots that have an owner, for the head's
// placement to stay off them.
func privateLotSet(ctx context.Context, tx application.Tx, settlementID string) (map[[2]int]bool, error) {
	lots, err := tx.Citizens().Lots(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]int]bool, len(lots))
	for _, l := range lots {
		out[[2]int{l.X, l.Y}] = true
	}
	return out, nil
}

// footprintTouches reports whether a footprint at (x, y) covers an owned lot.
func footprintTouches(owned map[[2]int]bool, def settlementbuilding.Def, x, y int) bool {
	for dy := 0; dy < def.FootprintH; dy++ {
		for dx := 0; dx < def.FootprintW; dx++ {
			if owned[[2]int{x + dx, y + dy}] {
				return true
			}
		}
	}
	return false
}

// mayChangeBuilding decides who may cancel or demolish a building: the owner
// of a private one, the village's head for a civic one; never the head for a
// resident's building.
func (h *VillageHandler) mayChangeBuilding(ctx context.Context, tx application.Tx, s application.FoundedSettlement,
	playerID, buildingID string,
) error {
	pb, err := tx.Citizens().PrivateBuilding(ctx, buildingID)
	switch {
	case err == nil:
		if pb.OwnerID == playerID {
			return nil
		}
		return refuseVillage(screens.CitizenLotPrivate)
	case stderrors.Is(err, application.ErrPrivateBuildingNotFound):
		return authorizeVillage(ctx, tx, s, playerID)
	}
	return err
}

// ---------------------------------------------------------------------
// Mine: the resident's own property, living at home, the tax
// ---------------------------------------------------------------------

// homeOf finds the finished house the player lives in: the oldest finished
// private building of theirs that the catalogue marks as a home.
func (h *VillageHandler) homeOf(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string,
) (*application.PrivateBuilding, *application.SettlementBuildingInstance, error) {
	snap := h.content.Current()
	priv, err := tx.Citizens().PrivateBuildings(ctx, s.CityID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]application.SettlementBuildingInstance{}
	for _, b := range rows {
		byID[b.ID] = b
	}
	for i := range priv {
		pb := priv[i]
		if pb.OwnerID != playerID {
			continue
		}
		b, ok := byID[pb.BuildingID]
		if !ok || !b.Holds() || b.Status != "complete" {
			continue
		}
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok && d.Home {
			return &pb, &b, nil
		}
	}
	return nil, nil, nil
}

// Mine handles settlement.mine.
func (h *VillageHandler) Mine(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.mine(ctx, meta, "")
}

func (h *VillageHandler) mine(ctx context.Context, meta envelope.Metadata, notice string) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.MineView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		view, err = h.mineView(ctx, tx, meta, &lang, notice)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Mine(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) mineView(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string, notice string,
) (screens.MineView, error) {
	sc, err := h.citizenScope(ctx, tx, meta, lang, true)
	if err != nil {
		return screens.MineView{}, err
	}
	snap := h.content.Current()
	now := h.now()
	_, cash, err := playerCash(ctx, tx, sc.p.ID)
	if err != nil {
		return screens.MineView{}, err
	}
	priv, err := tx.Citizens().PrivateBuildings(ctx, sc.s.CityID)
	if err != nil {
		return screens.MineView{}, err
	}
	rows, err := tx.SettlementBuildings().List(ctx, sc.s.CityID)
	if err != nil {
		return screens.MineView{}, err
	}
	byID := map[string]application.SettlementBuildingInstance{}
	for _, b := range rows {
		byID[b.ID] = b
	}
	view := screens.MineView{Village: sc.s.Name, SettlementID: sc.s.CityID, Cash: cash, TaxBPS: sc.tax, Notice: notice}
	standing := map[[2]int]application.SettlementBuildingInstance{}
	for _, pb := range priv {
		if pb.OwnerID != sc.p.ID {
			continue
		}
		b, ok := byID[pb.BuildingID]
		if !ok || !b.Holds() {
			continue
		}
		fw, fh := 1, 1
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
			def := d.Def()
			if b.Rotated {
				def = def.Rotate()
			}
			fw, fh = def.FootprintW, def.FootprintH
		}
		for dy := 0; dy < fh; dy++ {
			for dx := 0; dx < fw; dx++ {
				standing[[2]int{b.LotX + dx, b.LotY + dy}] = b
			}
		}
	}
	for _, l := range sc.lots {
		if l.OwnerID != sc.p.ID {
			continue
		}
		ml := screens.MineLot{X: l.X, Y: l.Y}
		if b, ok := standing[[2]int{l.X, l.Y}]; ok {
			ml.Building, ml.State = b.TypeCode, application.ViewState(b)
			if b.FinishAt != nil {
				ml.FinishAt, ml.Left = *b.FinishAt, countdownTo(*b.FinishAt, now)
			}
		}
		view.Lots = append(view.Lots, ml)
	}
	alive := func(id string) bool { b, ok := byID[id]; return ok && b.Holds() }
	for _, a := range application.AssessOwners(sc.lots, priv, alive) {
		if a.PlayerID == sc.p.ID {
			view.Assessed = a.Value
		}
	}
	view.TaxPerPeriod = application.PropertyTaxDue(view.Assessed, sc.tax)
	unpaid, err := tx.Citizens().UnpaidTax(ctx, sc.s.CityID, sc.p.ID)
	if err != nil {
		return view, err
	}
	for _, t := range unpaid {
		view.Debt += t.Due
	}
	view.DebtPeriods = len(unpaid)
	pb, b, err := h.homeOf(ctx, tx, sc.s, sc.p.ID)
	if err != nil {
		return view, err
	}
	if pb != nil {
		n := named(b.TypeCode, h.buildingName(b.TypeCode))
		view.Home = &n
		view.CanRest = true
		if pb.LastRestAt != nil {
			if ready := pb.LastRestAt.Add(h.citizen.HomeRestCooldown); ready.After(now) {
				view.CanRest, view.RestWait = false, ready.Sub(now)
			}
		}
	}
	return view, nil
}

// HomeRest handles settlement.home.rest: the owner of a finished house rests
// in it, once per cooldown, and gets a little health and mood back. It pays
// nothing: living in one's own house is comfort, never income.
func (h *VillageHandler) HomeRest(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, true)
		if err != nil {
			return err
		}
		pb, _, err := h.homeOf(ctx, tx, sc.s, sc.p.ID)
		if err != nil {
			return err
		}
		if pb == nil {
			return refuseVillage(screens.CitizenNoHouse, screens.AddrMine)
		}
		now := h.now()
		if pb.LastRestAt != nil {
			if ready := pb.LastRestAt.Add(h.citizen.HomeRestCooldown); ready.After(now) {
				r := refuseVillage(screens.CitizenRestWait, screens.AddrMine)
				r.remaining = ready.Sub(now)
				return r
			}
		}
		fresh, err := h.reserve(ctx, tx, sc.p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		st, err := tx.Stats().EnsureDefaults(ctx, sc.p.ID, defaultStats(sc.p.ID, now))
		if err != nil {
			return err
		}
		next := *st
		next.Health = minInt(st.MaxHealth, st.Health+h.citizen.HomeRestHealth)
		next.Happiness = minInt(100, st.Happiness+h.citizen.HomeRestHappiness)
		if err := tx.Stats().Save(ctx, next); err != nil {
			return err
		}
		return tx.Citizens().MarkRested(ctx, pb.BuildingID, now)
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.mine(ctx, meta, "rested")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------
// Property tax
// ---------------------------------------------------------------------

// taxPeriodNo numbers the tax periods since the epoch, so every replica
// names the same period for the same instant.
func (h *VillageHandler) taxPeriodNo(now time.Time) int64 {
	real := h.scale.RealWait(h.citizen.TaxPeriod)
	if real <= 0 {
		return 0
	}
	return now.UnixNano() / int64(real)
}

// SettleTax charges a settlement's owners the property tax of the current
// period and collects what they can pay, once per owner and period (the
// unique key of settlement_property_tax is the fence). It runs from the
// village's own periodic tick and is safe to run on any replica, any number
// of times. What an owner cannot pay stays a debt, collected as soon as they
// can; foreclosure of a debt is a later rule (docs/adr/0033 section 4.5).
func (h *VillageHandler) SettleTax(ctx context.Context, tx application.Tx, settlementID string, now time.Time) error {
	if !h.citizen.enabled() {
		return nil
	}
	terms, err := tx.Citizens().Terms(ctx, settlementID)
	if err != nil {
		return err
	}
	_, _, bps := h.citizen.Effective(terms)
	lots, err := tx.Citizens().Lots(ctx, settlementID)
	if err != nil {
		return err
	}
	priv, err := tx.Citizens().PrivateBuildings(ctx, settlementID)
	if err != nil {
		return err
	}
	if len(lots) == 0 && len(priv) == 0 {
		return nil
	}
	rows, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return err
	}
	holds := map[string]bool{}
	for _, b := range rows {
		holds[b.ID] = b.Holds()
	}
	period := h.taxPeriodNo(now)
	owners := map[string]bool{}
	for _, a := range application.AssessOwners(lots, priv, func(id string) bool { return holds[id] }) {
		owners[a.PlayerID] = true
		due := application.PropertyTaxDue(a.Value, bps)
		if due > 0 {
			if _, err := tx.Citizens().InsertTax(ctx, application.PropertyTax{
				ID: h.ids.NewID(), SettlementID: settlementID, PlayerID: a.PlayerID, PeriodNo: period,
				AssessedValue: a.Value, TaxBPS: bps, Due: due, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
	}
	debts, err := tx.Citizens().SettlementDebt(ctx, settlementID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, d := range debts {
		if seen[d.PlayerID] {
			continue
		}
		seen[d.PlayerID] = true
		if _, err := h.collectDebt(ctx, tx, settlementID, d.PlayerID, now); err != nil {
			return err
		}
	}
	return nil
}

// collectDebt pays a player's unpaid tax rows, oldest first, from their cash;
// a row is paid whole or not at all. It returns how much was paid.
func (h *VillageHandler) collectDebt(ctx context.Context, tx application.Tx, settlementID, playerID string, now time.Time) (int64, error) {
	unpaid, err := tx.Citizens().UnpaidTax(ctx, settlementID, playerID)
	if err != nil || len(unpaid) == 0 {
		return 0, err
	}
	cashAcct, cash, err := playerCash(ctx, tx, playerID)
	if err != nil {
		return 0, err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, settlementID)
	if err != nil {
		return 0, err
	}
	var paid int64
	for _, t := range unpaid {
		if cash < t.Due {
			break
		}
		txID := h.ids.NewID()
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: txID, Reason: application.ReasonSettlementPropertyTax, CreatedAt: now,
			ReferenceType: application.SettlementPropertyTaxTable, ReferenceID: t.ID,
			Entries: []application.LedgerEntry{
				{AccountID: cashAcct.ID, Amount: money.FromMinor(-t.Due)},
				{AccountID: treasury.ID, Amount: money.FromMinor(t.Due)},
			},
		}); err != nil {
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				break
			}
			return paid, err
		}
		if err := tx.Citizens().MarkTaxPaid(ctx, t.ID, txID, now); err != nil {
			return paid, err
		}
		cash -= t.Due
		paid += t.Due
	}
	return paid, nil
}

// PayTax handles settlement.tax.pay: the player pays their unpaid property
// tax now, as far as their cash goes.
func (h *VillageHandler) PayTax(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	lang := meta.Language
	notice := "tax_none"
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, true)
		if err != nil {
			return err
		}
		unpaid, err := tx.Citizens().UnpaidTax(ctx, sc.s.CityID, sc.p.ID)
		if err != nil {
			return err
		}
		if len(unpaid) == 0 {
			return refuseVillage(screens.CitizenNoDebt, screens.AddrMine)
		}
		fresh, err := h.reserve(ctx, tx, sc.p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		paid, err := h.collectDebt(ctx, tx, sc.s.CityID, sc.p.ID, h.now())
		if err != nil {
			return err
		}
		if paid > 0 {
			notice = "tax_paid"
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.mine(ctx, meta, notice)
}

// ---------------------------------------------------------------------
// The head's levers
// ---------------------------------------------------------------------

func presetsInt64(lo, def, hi int64) []int64 {
	set := map[int64]bool{}
	var out []int64
	for _, v := range []int64{lo, def, def * 2} {
		v = clampInt64(v, lo, hi)
		if !set[v] {
			set[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Terms handles settlement.terms: the village head reads and moves the lot
// price, the permit fee and the property tax inside their bounds.
func (h *VillageHandler) Terms(ctx context.Context, meta envelope.Metadata, req VillageTermsRequest) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.TermsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScope(ctx, tx, meta, &lang, false)
		if err != nil {
			return err
		}
		if err := authorizeVillage(ctx, tx, sc.s, sc.p.ID); err != nil {
			return err
		}
		next := sc.terms
		changed := false
		parse := func(text string) (int64, error) {
			n, perr := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if perr != nil {
				return 0, refuseVillage(screens.CitizenTermsRange, screens.AddrVillageTerms)
			}
			return n, nil
		}
		if strings.TrimSpace(req.LotPrice) != "" {
			n, err := parse(req.LotPrice)
			if err != nil {
				return err
			}
			if n < h.citizen.LotPriceMin || n > h.citizen.LotPriceMax {
				return refuseVillage(screens.CitizenTermsRange, screens.AddrVillageTerms)
			}
			next.LotPrice, changed = n, true
		}
		if strings.TrimSpace(req.PermitFee) != "" {
			n, err := parse(req.PermitFee)
			if err != nil {
				return err
			}
			if n < 0 || n > h.citizen.PermitFeeMax {
				return refuseVillage(screens.CitizenTermsRange, screens.AddrVillageTerms)
			}
			next.PermitFee, next.HasPermit, changed = n, true, true
		}
		if strings.TrimSpace(req.TaxBPS) != "" {
			n, err := parse(req.TaxBPS)
			if err != nil {
				return err
			}
			if n < 0 || n > int64(h.citizen.TaxBPSMax) {
				return refuseVillage(screens.CitizenTermsRange, screens.AddrVillageTerms)
			}
			next.TaxBPS, next.HasTax, changed = int(n), true, true
		}
		if changed {
			fresh, err := h.reserve(ctx, tx, sc.p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if err := tx.Citizens().SetTerms(ctx, sc.s.CityID, next, sc.p.ID, h.now()); err != nil {
					return err
				}
				if err := appendVillageEvent(ctx, tx, meta, "terms_changed", sc.s.CityID, map[string]any{
					"settlement_id": sc.s.CityID, "lot_price": next.LotPrice, "permit_fee": next.PermitFee, "tax_bps": next.TaxBPS,
				}); err != nil {
					return err
				}
			}
			sc.terms = next
			sc.price, sc.fee, sc.tax = h.citizen.Effective(next)
		}
		r := h.citizen
		view = screens.TermsView{
			Village: sc.s.Name, SettlementID: sc.s.CityID,
			LotPrice: sc.price, LotPriceMin: r.LotPriceMin, LotPriceMax: r.LotPriceMax,
			PermitFee: sc.fee, PermitFeeMax: r.PermitFeeMax, TaxBPS: sc.tax, TaxBPSMax: r.TaxBPSMax,
			LotPresets: presetsInt64(r.LotPriceMin, r.LotPrice, r.LotPriceMax), PermitPresets: presetsInt64(0, r.PermitFee, r.PermitFeeMax),
			TaxPresets:      presetsInt(0, r.TaxBPS, r.TaxBPSMax),
			DefaultLotPrice: r.LotPrice, DefaultPermit: r.PermitFee, DefaultTaxBPS: r.TaxBPS,
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Terms(h.screen(meta, lang), view), nil
}

func presetsInt(lo, def, hi int) []int {
	var out []int
	for _, v := range presetsInt64(int64(lo), int64(def), int64(hi)) {
		out = append(out, int(v))
	}
	return out
}

// Work handles settlement.work: the page behind «کار کن». Until the village
// has producers and workers (the economy phase), it tells the player where
// work is today: the Support city.
func (h *VillageHandler) Work(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	lang := meta.Language
	var view screens.WorkView
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
		home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
		if err != nil {
			return err
		}
		if home != s.CityID {
			return refuseVillage(screens.VillageNotResident)
		}
		view = screens.WorkView{Village: s.Name}
		if h.homeCityCode != "" {
			if support, err := h.cities.ByCode(ctx, h.homeCityCode); err == nil {
				view.Support, view.SupportCode = support.Name, support.Code
			}
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.VillageWork(h.screen(meta, lang), view), nil
}

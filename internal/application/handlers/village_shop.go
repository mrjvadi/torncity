package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/carry"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/shop"
	"github.com/mrjvadi/torncity/internal/domain/vshop"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// This file is the village shop (docs/adr/0046-bags-merchants-currency-
// exchange.md section 5, phase M2; rules internal/domain/vshop; what it sells is
// configs/content/village_shop.yml).
//
// # The working mechanic (CLAUDE.md rule 1c)
//
//   - WHO WORKS THERE. A shopkeeper: an NPC citizen of the village's labour pool
//     (labor.Rules.PoolSize: the homes it has less the players living in them,
//     times the share who work). No one in the pool: the shop is frozen.
//   - WHAT IT CONSUMES. A day's NPC wage from the treasury at each morning
//     delivery (ledger reason shopkeeper_wage, treasury to the sink). An empty
//     treasury: the shopkeeper stays home, the shop is frozen that day. The goods
//     it sells are a limited delivery from outside (vshop: units follow the people
//     served, a shelf holds two days, the whole delivery is held to a supply
//     budget): the faucet of goods the verifier checks.
//   - WHAT IT PROVIDES. Basic goods at the reference price plus a markup the head
//     may cap, into the buyer's bags (no room in the bags, no sale). The price
//     leaves the economy (shop_purchase, to the sink); the village's sales tax on it
//     goes to the treasury, which pays the shopkeeper. Money-wise the village
//     shop is a way for the treasury to turn a tax into a wage, nothing more.
//   - HOW IT LINKS. The shop building raises the delivery, stocks the lines that
//     need a building, and mends bags at a counter. Local lines (timber) need the
//     woodcutter's camp. The lines the content says are imported need the barter
//     post: ADR 0046 5.4 asks for a road or cart route to a supplier, and no world
//     road network exists yet (gap, documented in the ADR as built).
//   - WHAT BREAKS. No shopkeeper, or no wage in the treasury: no delivery and no
//     sale. A full shelf trims the delivery. A full bag stops the sale.
//
// # When the morning delivery happens
//
// Once per game day at merchant.restock_hour (06:00 game clock), keyed by the day
// number (village_shop_days is the fence, so two replicas, the village's tick
// and a player's first look of the day cannot deliver twice). The village's own
// tick (Taught) settles it, and so does the first look or purchase of the day, so
// a tick that drifted across a morning never loses the day.

// ShopRules is the shop's tuning (config merchant.*, game.*).
type ShopRules struct {
	Rules       vshop.Rules
	RestockHour int
	CapPresets  []int64
	BuyPresets  []int64
	TaxPresets  []int64
	TaxDefault  int64
	TaxMax      int64
	// NilUnitSup is the Nil display constant (config premium.nil_unit_sup),
	// NilExamples the amounts the money panel spells out, OutputDays the game days
	// it reads the village's output over.
	NilUnitSup  int64
	NilExamples []int64
	OutputDays  int
	Carry       carry.Rules
	Clock       gametime.Clock
}

func (r ShopRules) enabled() bool {
	return r.Rules.Validate() == nil && r.Clock.Validate() == nil && r.Carry.Base > 0
}

// WithShop gives the village handler the village shop.
func (h *VillageHandler) WithShop(r ShopRules) *VillageHandler {
	h.shop = r
	return h
}

// ShopRequest names a good on the shelf, how many, the way to pay and a one-time
// token (the village shop's counterpart of the city shops' ShopRequest).
type VillageShopRequest struct {
	Item   string `json:"item,omitempty"`
	Qty    string `json:"qty,omitempty"`
	Method string `json:"method,omitempty"`
	Nonce  string `json:"nonce,omitempty"`
	// BPS is a price cap or a tax, in basis points.
	BPS string `json:"bps,omitempty"`
}

// shopRefusal carries a refusal of the village shop out of a unit of work.
type villageShopRefusal struct{ view village.ShopRefusalView }

func (r *villageShopRefusal) Error() string { return "handlers: village shop refused: " + r.view.Kind }

func (h *VillageHandler) shopRefused(v village.ShopRefusalView) error {
	return &villageShopRefusal{view: v}
}

// shopFinish answers a refusal of the shop; everything else goes to the village's
// own finish (a settlement that does not exist, a payment that was declined).
func (h *VillageHandler) shopFinish(meta envelope.Metadata, lang string, err error) (*presentation.Response, error) {
	if err == nil {
		return nil, nil
	}
	var r *villageShopRefusal
	if stderrors.As(err, &r) {
		return village.VillageShopRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asDeclined(err, economy.PaymentDeclinedView{}); ok {
		return economy.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	return h.villageFinish(meta, lang, err)
}

// shopState is what a settlement has that decides what its shop sells.
type shopState struct {
	def       content.VillageShopDef
	buildings []application.SettlementBuildingInstance
	standing  map[string]bool
	owned     map[string]bool
	// boosted: the shop building stands.
	boosted         bool
	residents, pool int64
	served          int64
	wage            int64
	open            []shopOpenLine
	locked          []content.VillageShopLineDef
	plan            map[string]vshop.Planned
	value, budget   int64
	// rules are the config's rules with the content's demand step.
	rules vshop.Rules
}

type shopOpenLine struct {
	def  content.VillageShopLineDef
	line vshop.Line
	kind string
}

func shopKindOf(snap *content.Snapshot, code string) string {
	if _, ok := snap.ItemDef(code); ok {
		return "item"
	}
	return "component"
}

func (h *VillageHandler) shopNameOf(snap *content.Snapshot, code string) presentation.Named {
	if d, ok := snap.ItemDef(code); ok {
		return named(d.Code, d.Name)
	}
	if c, ok := snap.ComponentDef(code); ok {
		return named(c.Code, c.Name)
	}
	return named(code, code)
}

// shopStateOf reads the settlement's buildings, research and people and works
// out the lines it carries and the day's plan.
func (h *VillageHandler) shopStateOf(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	s application.FoundedSettlement,
) (shopState, error) {
	def, ok := snap.VillageShop()
	if !ok {
		return shopState{}, errors.Internal(stderrors.New("handlers: the content has no village shop"))
	}
	st := shopState{def: def, standing: map[string]bool{}, owned: map[string]bool{}, rules: h.shop.Rules}
	st.rules.FreeSales, st.rules.StepBPS, st.rules.MaxExtraBPS = def.Demand.FreeSales, def.Demand.StepBPS, def.Demand.MaxExtraBPS
	var err error
	if st.buildings, err = tx.SettlementBuildings().List(ctx, s.CityID); err != nil {
		return st, err
	}
	for _, b := range st.buildings {
		if b.Status == "complete" {
			st.standing[b.TypeCode] = true
		}
	}
	st.boosted = st.standing[def.Building]
	owned, err := tx.SettlementKnowledge().Owned(ctx, s.CityID)
	if err != nil {
		return st, err
	}
	for _, o := range owned {
		st.owned[o.Code] = true
	}
	if st.residents, err = tx.Settlements().ResidentCount(ctx, s.CityID); err != nil {
		return st, err
	}
	market, err := h.laborMarket(ctx, tx, snap, s, st.buildings)
	if err != nil {
		return st, err
	}
	st.pool, st.wage = market.pool, market.line.NPCWage
	st.served = st.residents + st.pool

	var lines []vshop.Line
	for _, l := range def.Lines {
		rl, ok := snap.ShopLineRule(l)
		if !ok {
			continue
		}
		if !shopLineOpen(l, st) {
			st.locked = append(st.locked, l)
			continue
		}
		st.open = append(st.open, shopOpenLine{def: l, line: rl, kind: shopKindOf(snap, l.Item)})
		lines = append(lines, rl)
	}
	plan := st.rules.Plan(lines, st.served, st.boosted)
	st.plan = make(map[string]vshop.Planned, len(plan))
	for _, p := range plan {
		st.plan[p.Line.Code] = p
	}
	st.value, st.budget = vshop.Value(plan), st.rules.Budget(st.served)
	return st, nil
}

// shopLineOpen reports whether the settlement has what a line needs.
func shopLineOpen(l content.VillageShopLineDef, st shopState) bool {
	if l.Requires == nil {
		return false
	}
	for _, k := range l.Requires.Knowledge {
		if !st.owned[k] {
			return false
		}
	}
	for _, b := range l.Requires.Buildings {
		if !st.standing[b.Code] {
			return false
		}
	}
	return true
}

// restockDay is the number of the restock day at now, -1 before the first
// morning.
func (h *VillageHandler) restockDay(now time.Time) int64 {
	return h.shop.Clock.DayAtHour(now, h.shop.RestockHour)
}

func (h *VillageHandler) nextDelivery(now time.Time) time.Time {
	return h.shop.Clock.RealAtHour(h.restockDay(now)+1, h.shop.RestockHour)
}

// SettleShopDay brings a village's shop up to the current morning: if the day's
// delivery has not been done, it does it (or records why it could not). It is
// idempotent; it returns the day's record, nil before the first morning.
func (h *VillageHandler) SettleShopDay(ctx context.Context, tx application.Tx, s application.FoundedSettlement,
	now time.Time,
) (*application.VillageShopDay, error) {
	if !h.shop.enabled() {
		return nil, nil
	}
	day := h.restockDay(now)
	if day < 0 {
		return nil, nil
	}
	repo := tx.VillageShop()
	if d, err := repo.Day(ctx, s.CityID, day); err != nil || d != nil {
		return d, err
	}
	snap := h.content.Current()
	if _, ok := snap.VillageShop(); !ok {
		return nil, nil
	}
	st, err := h.shopStateOf(ctx, tx, snap, s)
	if err != nil {
		return nil, err
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return nil, err
	}
	d := application.VillageShopDay{SettlementID: s.CityID, Day: day, Budget: st.budget, At: now}
	switch {
	case st.pool < 1:
		d.Outcome = application.VillageShopNoShopkeeper
	case treasury < st.wage:
		d.Outcome = application.VillageShopUnpaid
	default:
		d.Outcome, d.Wage = application.VillageShopDelivered, st.wage
		d.DeliveredValue = st.value
		if d.Wage > 0 {
			d.LedgerTransactionID = h.ids.NewID()
		}
	}
	// The fence first: only the transaction that writes the day's row delivers.
	fresh, err := repo.RecordDay(ctx, d)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return repo.Day(ctx, s.CityID, day)
	}
	if d.Outcome != application.VillageShopDelivered {
		return &d, nil
	}
	if d.Wage > 0 {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: d.LedgerTransactionID, Reason: application.ReasonShopkeeperWage, CreatedAt: now,
			ReferenceType: application.VillageShopDayReference, ReferenceID: s.CityID,
			Entries: []application.LedgerEntry{
				{AccountID: acct.ID, Amount: money.FromMinor(-d.Wage)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(d.Wage)},
			},
		}); err != nil {
			return nil, err
		}
	}
	for _, ol := range st.open {
		p := st.plan[ol.line.Code]
		row, err := repo.Line(ctx, s.CityID, ol.line.Code)
		if err != nil {
			return nil, err
		}
		after, added, trimmed := vshop.Deliver(row.Stock, p.Units, p.Cap)
		row.Stock, row.DeliveredDay, row.SoldToday = after, day, 0
		row.DeliveredTotal += added
		row.TrimmedTotal += trimmed
		if err := repo.SaveLine(ctx, *row); err != nil {
			return nil, err
		}
	}
	// delivered_value is the day's plan at reference prices: what the shelf's cap
	// trimmed is counted in trimmed_total, so the row is the bound the supply
	// budget is checked against, never less than what really arrived.
	return &d, nil
}

// shopClosed says why a day's record leaves the shop shut, "" when it is open.
func shopClosed(d *application.VillageShopDay) string {
	switch {
	case d == nil:
		return village.ShopNotYet
	case d.Outcome == application.VillageShopNoShopkeeper:
		return village.ShopNoShopkeeper
	case d.Outcome == application.VillageShopUnpaid:
		return village.ShopUnpaid
	}
	return village.ShopOpen
}

// shopTerms reads the head's levers, the defaults where none was set.
func (h *VillageHandler) shopTerms(ctx context.Context, tx application.Tx, s application.FoundedSettlement) (capBPS, taxBPS int64, err error) {
	t, err := tx.VillageShop().Terms(ctx, s.CityID)
	if err != nil {
		return 0, 0, err
	}
	capBPS, taxBPS = h.shop.Rules.MarkupMaxBPS, h.shop.TaxDefault
	if t.HasCap {
		capBPS = t.PriceCapBPS
	}
	if t.HasTax {
		taxBPS = t.TaxBPS
	}
	return capBPS, taxBPS, nil
}

// fitsHow many units of a good the room left can take.
func fitsOf(room carry.Room, bulk, grams int64) int64 {
	fits := int64(1 << 40)
	if bulk > 0 {
		fits = min(fits, room.FreeSpace/bulk)
	}
	if grams > 0 {
		fits = min(fits, room.FreeG/grams)
	}
	return fits
}

// shopView builds the shop screen for a viewer.
func (h *VillageHandler) shopView(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.Player,
	s application.FoundedSettlement, now time.Time,
) (village.ShopView, error) {
	snap := h.content.Current()
	day, err := h.SettleShopDay(ctx, tx, s, now)
	if err != nil {
		return village.ShopView{}, err
	}
	st, err := h.shopStateOf(ctx, tx, snap, s)
	if err != nil {
		return village.ShopView{}, err
	}
	capBPS, taxBPS, err := h.shopTerms(ctx, tx, s)
	if err != nil {
		return village.ShopView{}, err
	}
	present, err := h.presentHere(ctx, tx, p, s)
	if err != nil {
		return village.ShopView{}, err
	}
	head := authorizeVillage(ctx, tx, s, p.ID) == nil
	_, cash, err := playerCash(ctx, tx, p.ID)
	if err != nil {
		return village.ShopView{}, err
	}
	cenv := carryEnv{rules: h.shop.Carry, clock: h.shop.Clock}
	if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
		return village.ShopView{}, err
	}
	cst, err := cenv.load(ctx, tx, snap, p.ID, now, true)
	if err != nil {
		return village.ShopView{}, err
	}
	room := cst.room(h.shop.Carry)
	view := village.ShopView{
		Village: s.Name, Building: st.boosted, Closed: shopClosed(day),
		NextDelivery: h.nextDelivery(now), DeliveryHour: h.shop.RestockHour, Wage: st.wage, TaxBPS: taxBPS,
		TaxMaxBPS: h.shop.TaxMax, TaxPresets: h.shop.TaxPresets,
		PriceCapBPS: capBPS, CapMinBPS: h.shop.Rules.MarkupMinBPS, CapMaxBPS: h.shop.Rules.MarkupMaxBPS,
		CapPresets: h.shop.CapPresets, CanSetCap: head, Presets: h.shop.BuyPresets, Resident: present,
		FreeSpace: room.FreeSpace, Capacity: cst.capacity, FreeG: room.FreeG, CanRepair: st.boosted, Cash: cash,
	}
	stock := map[string]application.VillageShopLine{}
	rows, err := tx.VillageShop().Lines(ctx, s.CityID)
	if err != nil {
		return view, err
	}
	for _, r := range rows {
		stock[r.Line] = r
	}
	dayNo := int64(-1)
	if day != nil {
		dayNo = day.Day
	}
	for _, ol := range st.open {
		row := stock[ol.line.Code]
		line := village.ShopLine{
			Item: h.shopNameOf(snap, ol.line.Code), Kind: ol.kind, Shelf: shelfRefOf(snap, ol.line.Code),
			Price: st.rules.Price(ol.line, row.SoldToday, capBPS), Reference: ol.line.Ref, Stock: row.Stock,
			Tradable: ol.def.Tradable,
		}
		bought, err := tx.VillageShop().PlayerDay(ctx, p.ID, s.CityID, ol.line.Code, dayNo)
		if err != nil {
			return view, err
		}
		planned := st.plan[ol.line.Code]
		line.LeftToday = max(st.rules.PlayerDayCap(ol.line, planned.Units)-bought, 0)
		line.Fits = fitsOf(room, snap.BulkOf(ol.line.Code), snap.WeightOf(ol.line.Code))
		if present && view.Closed == village.ShopOpen {
			line.MaxBuy = min(line.Stock, line.LeftToday, line.Fits)
		}
		view.Lines = append(view.Lines, line)
	}
	for _, l := range st.locked {
		lk := village.ShopLockedLine{Item: h.shopNameOf(snap, l.Item), Kind: shopKindOf(snap, l.Item), Shelf: shelfRefOf(snap, l.Item)}
		if l.Requires != nil {
			for _, b := range l.Requires.Buildings {
				if !st.standing[b.Code] {
					bd, _ := snap.SettlementBuildingDef(b.Code)
					lk.NeedsBuildings = append(lk.NeedsBuildings, named(b.Code, bd.Name))
				}
			}
			for _, k := range l.Requires.Knowledge {
				if !st.owned[k] {
					kd, _ := snap.SettlementKnowledgeDef(k)
					lk.NeedsKnowledge = append(lk.NeedsKnowledge, named(k, kd.Name))
				}
			}
		}
		view.Locked = append(view.Locked, lk)
	}
	if st.boosted {
		view.Repairs = h.repairLines(snap, cst, mustCarriedPieces(ctx, tx, p.ID))
	}
	return view, nil
}

func mustCarriedPieces(ctx context.Context, tx application.Tx, playerID string) []application.Piece {
	_, pieces, _, err := carried(ctx, tx, playerID)
	if err != nil {
		return nil
	}
	return pieces
}

// repairLines are the carried bags that need mending, with the price.
func (h *VillageHandler) repairLines(snap *content.Snapshot, cst carryState, pieces []application.Piece) []village.ShopRepairLine {
	var out []village.ShopRepairLine
	for _, pc := range pieces {
		def, ok := snap.ItemDef(pc.Item)
		if !ok || def.Bag == nil {
			continue
		}
		cost := h.shop.Carry.RepairCost(def.BasePrice, pc.UsesLeft, def.Durability)
		if cost < 1 {
			continue
		}
		out = append(out, village.ShopRepairLine{Item: named(def.Code, def.Name), Serial: pc.Serial, Slot: def.Bag.Slot,
			Wear: pc.UsesLeft, WearMax: def.Durability, Torn: pc.UsesLeft <= 0, Cost: cost})
	}
	return out
}

// Shop handles settlement.shop: the village's shop, its shelf and the viewer's room.
func (h *VillageHandler) Shop(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.shopScreen(ctx, meta, nil, nil)
}

func (h *VillageHandler) shopScreen(ctx context.Context, meta envelope.Metadata, bought *village.ShopBought, mended *village.ShopMended) (*presentation.Response, error) {
	lang := meta.Language
	var view village.ShopView
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
		view, err = h.shopView(ctx, tx, meta, p, s, h.now())
		view.Bought, view.Mended = bought, mended
		return err
	})
	if resp, err := h.shopFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.VillageShop(h.screen(meta, lang), view), nil
}

// ShopBuy handles settlement.shop.buy. Without a way to pay it answers the
// checkout (the price, the tax, the room it takes) and changes nothing; with one
// it buys, in one transaction: the shelf, the player's daily count, the money, the
// goods, the sale and the event.
func (h *VillageHandler) ShopBuy(ctx context.Context, meta envelope.Metadata, req VillageShopRequest) (*presentation.Response, error) {
	if !h.shop.enabled() {
		return nil, errors.Internal(stderrors.New("handlers: the village shop is not wired"))
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	qty, perr := strconv.ParseInt(strings.TrimSpace(req.Qty), 10, 64)
	if perr != nil || qty < 1 || qty > 1_000 {
		return nil, errors.InvalidInput("a quantity is a whole number from 1 to 1000")
	}
	var (
		checkout *village.ShopCheckoutView
		bought   *village.ShopBought
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		now := h.now()
		if chosen {
			key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
			if isNonce(req.Nonce) {
				key = idempotency.Derive(p.ID, meta.Command, req.Nonce)
			}
			fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		present, err := h.presentHere(ctx, tx, p, s)
		if err != nil {
			return err
		}
		if !present {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedNotHere})
		}
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		// The owner's lock first, then the shop's rows: every writer queues in this order.
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		day, err := h.SettleShopDay(ctx, tx, s, now)
		if err != nil {
			return err
		}
		if closed := shopClosed(day); closed != village.ShopOpen {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedClosed, Closed: closed, NextDelivery: h.nextDelivery(now)})
		}
		st, err := h.shopStateOf(ctx, tx, snap, s)
		if err != nil {
			return err
		}
		var ol *shopOpenLine
		for i := range st.open {
			if st.open[i].line.Code == strings.TrimSpace(req.Item) {
				ol = &st.open[i]
			}
		}
		code := strings.TrimSpace(req.Item)
		name := h.shopNameOf(snap, code)
		if ol == nil {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedNotThere, Item: name})
		}
		row, err := tx.VillageShop().Line(ctx, s.CityID, code)
		if err != nil {
			return err
		}
		boughtToday, err := tx.VillageShop().PlayerDay(ctx, p.ID, s.CityID, code, day.Day)
		if err != nil {
			return err
		}
		planned := st.plan[code]
		if err := st.rules.CanSell(ol.line, planned.Units, row.Stock, boughtToday, qty); err != nil {
			left := max(st.rules.PlayerDayCap(ol.line, planned.Units)-boughtToday, 0)
			if stderrors.Is(err, vshop.ErrPlayerCap) {
				return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedCap, Item: name, LeftToday: left, Stock: row.Stock,
					NextDelivery: h.nextDelivery(now)})
			}
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedSoldOut, Item: name, Stock: row.Stock,
				NextDelivery: h.nextDelivery(now)})
		}
		// No room, no sale.
		cenv := carryEnv{rules: h.shop.Carry, clock: h.shop.Clock}
		cst, err := cenv.load(ctx, tx, snap, p.ID, now, true)
		if err != nil {
			return err
		}
		bulk, grams := snap.BulkOf(code)*qty, snap.WeightOf(code)*qty
		room := cst.room(h.shop.Carry)
		if err := room.Fits(bulk, grams); err != nil {
			kind := village.ShopRefusedNoSpace
			if stderrors.Is(err, carry.ErrTooHeavy) {
				kind = village.ShopRefusedTooHeavy
			}
			return h.shopRefused(village.ShopRefusalView{Kind: kind, Item: name, FreeSpace: room.FreeSpace, NeedSpace: bulk,
				FreeG: room.FreeG, NeedG: grams})
		}
		capBPS, taxBPS, err := h.shopTerms(ctx, tx, s)
		if err != nil {
			return err
		}
		unit := st.rules.Price(ol.line, row.SoldToday, capBPS)
		total, err := shop.Total(money.FromMinor(unit), qty)
		if err != nil {
			return errors.Internal(err)
		}
		tax, err := shop.SalesTax(total, int(taxBPS))
		if err != nil {
			return errors.Internal(err)
		}
		due, err := total.Add(tax)
		if err != nil {
			return errors.Internal(err)
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(due, snap.ShopAccepts("village_shop"))
		backTo := []string{village.AddrShop}
		if !chosen {
			checkout = &village.ShopCheckoutView{
				Village: s.Name, Item: name, Kind: ol.kind, Qty: qty, Unit: unit, Total: total.Minor(), Tax: tax.Minor(), TaxBPS: taxBPS,
				Stock: row.Stock, Space: bulk, FreeSpace: room.FreeSpace, Grams: grams,
				Payment: paymentChoice(plan, wallet), Nonce: h.shopNonce(),
			}
			return nil
		}
		if err := checkMethod(plan, method, wallet, "village.shop.button.back", backTo...); err != nil {
			return err
		}

		saleID := h.ids.NewID()
		// The shelf first: its row is the lock two buyers of the last unit meet at.
		row.Stock -= qty
		row.SoldToday += qty
		row.SoldTotal += qty
		if err := tx.VillageShop().SaveLine(ctx, *row); err != nil {
			return err
		}
		if err := tx.VillageShop().AddPlayerDay(ctx, p.ID, s.CityID, code, day.Day, qty); err != nil {
			return err
		}
		txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
			Method: method, Accepted: plan.Accepted, Reason: application.ReasonShopPurchase,
			ReferenceType: application.VillageShopSaleReference, ReferenceID: saleID,
			To:        []application.LedgerEntry{{AccountID: application.SystemSinkAccountID, Amount: total}},
			CreatedAt: now,
		})
		if err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "village.shop.button.back", backTo...)
			}
			return err
		}
		if !tax.IsZero() {
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
			if err != nil {
				return err
			}
			if _, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
				Method: method, Accepted: plan.Accepted, Reason: application.ReasonSalesTax,
				ReferenceType: application.VillageShopSaleReference, ReferenceID: saleID,
				To:        []application.LedgerEntry{{AccountID: treasury.ID, Amount: tax}},
				CreatedAt: now,
			}); err != nil {
				if stderrors.Is(err, application.ErrPaymentDeclined) {
					return declined(plan, wallet, "village.shop.button.back", backTo...)
				}
				return err
			}
		}
		if err := tx.VillageShop().RecordSale(ctx, application.VillageShopSale{
			ID: saleID, SettlementID: s.CityID, PlayerID: p.ID, Line: code, Day: day.Day, Quantity: qty,
			UnitPrice: unit, ReferencePrice: ol.line.Ref, Total: total.Minor(), Tax: tax.Minor(), Method: string(method),
			LedgerTransactionID: txID, At: now,
		}); err != nil {
			return err
		}
		if err := h.deliverToBuyer(ctx, tx, snap, p.ID, code, qty, saleID, now); err != nil {
			return err
		}
		bought = &village.ShopBought{Item: name, Kind: ol.kind, Qty: qty, Total: total.Minor(), Tax: tax.Minor(), Method: string(method)}
		return appendVillageEvent(ctx, tx, meta, "shop_sold", s.CityID, map[string]any{
			"settlement_id": s.CityID, "player_id": p.ID, "item": code, "quantity": qty, "unit_price": unit,
			"total": total.Minor(), "tax": tax.Minor(), "method": string(method), "sale_id": saleID, "day": day.Day,
			"ledger_transaction_id": txID, "content_version": snap.Version(),
		})
	})
	if resp, ferr := h.shopFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	switch {
	case replayed:
		return h.shopScreen(ctx, meta, nil, nil)
	case checkout != nil:
		return village.VillageShopCheckout(h.screen(meta, lang), *checkout), nil
	}
	return h.shopScreen(ctx, meta, bought, nil)
}

func (h *VillageHandler) shopNonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	return id[:min(len(id), nonceLength)]
}

// deliverToBuyer puts the sold units in the buyer's pack. The sale is their
// origin: a good enters the world named after it (like a city shop's), a material
// a player carries (timber, stone) enters as a stack of the material.
func (h *VillageHandler) deliverToBuyer(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID, code string,
	qty int64, saleID string, now time.Time,
) error {
	if _, isItem := snap.ItemDef(code); isItem {
		_, err := bring(ctx, tx, snap, h.ids, nil, playerID, code, qty, -1, origin{
			kind: application.OriginSupply, reason: application.ItemShopPurchase, refType: application.VillageShopSaleReference, refID: saleID,
		}, now)
		return err
	}
	return tx.Items().Move(ctx, application.ItemMove{
		ID: h.ids.NewID(), Item: code, Qty: qty, To: playerID, ToHolding: application.HoldCarried,
		Reason: application.ItemShopPurchase, ReferenceType: application.VillageShopSaleReference, ReferenceID: saleID, At: now,
	})
}

// ShopCap handles settlement.shop.cap: the head sets the ceiling of the shop's
// prices, in basis points of the reference price, inside the bounds of the config.
func (h *VillageHandler) ShopCap(ctx context.Context, meta envelope.Metadata, req VillageShopRequest) (*presentation.Response, error) {
	return h.shopTerm(ctx, meta, req, true)
}

// ShopTax handles settlement.shop.tax: the head sets the village's sales tax.
func (h *VillageHandler) ShopTax(ctx context.Context, meta envelope.Metadata, req VillageShopRequest) (*presentation.Response, error) {
	return h.shopTerm(ctx, meta, req, false)
}

func (h *VillageHandler) shopTerm(ctx context.Context, meta envelope.Metadata, req VillageShopRequest, isCap bool) (*presentation.Response, error) {
	if !h.shop.enabled() {
		return nil, errors.Internal(stderrors.New("handlers: the village shop is not wired"))
	}
	bps, perr := strconv.ParseInt(strings.TrimSpace(req.BPS), 10, 64)
	if perr != nil {
		return nil, errors.InvalidInput("a price cap or a tax is a whole number of basis points")
	}
	lang := meta.Language
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
		if err := authorizeVillage(ctx, tx, s, p.ID); err != nil {
			return err
		}
		lo, hi, field := h.shop.Rules.MarkupMinBPS, h.shop.Rules.MarkupMaxBPS, "price_cap_bps"
		if !isCap {
			lo, hi, field = 0, h.shop.TaxMax, "tax_bps"
		}
		if bps < lo || bps > hi {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedCapRange, Min: lo, Max: hi})
		}
		now := h.now()
		if isCap {
			err = tx.VillageShop().SetPriceCap(ctx, s.CityID, bps, p.ID, now)
		} else {
			err = tx.VillageShop().SetTax(ctx, s.CityID, bps, p.ID, now)
		}
		if err != nil {
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "shop_terms_set", s.CityID, map[string]any{
			"settlement_id": s.CityID, "field": field, "bps": bps, "set_by": p.ID,
		})
	})
	if resp, ferr := h.shopFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return h.shopScreen(ctx, meta, nil, nil)
}

// ShopRepair handles settlement.shop.repair: the shop building's counter mends a
// bag the player carries, for a share of its price, paid at the counter. The
// bag is whole again (its wear points restored); nothing else about it changes.
func (h *VillageHandler) ShopRepair(ctx context.Context, meta envelope.Metadata, req VillageShopRequest) (*presentation.Response, error) {
	if !h.shop.enabled() {
		return nil, errors.Internal(stderrors.New("handlers: the village shop is not wired"))
	}
	snap := h.content.Current()
	lang := meta.Language
	var mended *village.ShopMended
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
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			return nil // a redelivered press: already mended
		}
		now := h.now()
		present, err := h.presentHere(ctx, tx, p, s)
		if err != nil {
			return err
		}
		if !present {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedNotHere})
		}
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		day, err := h.SettleShopDay(ctx, tx, s, now)
		if err != nil {
			return err
		}
		if closed := shopClosed(day); closed != village.ShopOpen {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedClosed, Closed: closed, NextDelivery: h.nextDelivery(now)})
		}
		st, err := h.shopStateOf(ctx, tx, snap, s)
		if err != nil {
			return err
		}
		if !st.boosted {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedNoBuilding})
		}
		code, _, piece, err := held(ctx, tx, p.ID, strings.TrimSpace(req.Item))
		if err != nil {
			return err
		}
		def, _ := snap.ItemDef(code)
		if piece == nil || def.Bag == nil {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedNothingToMend, Item: named(code, def.Name)})
		}
		// The wear is settled first, so a bag is mended for what it really lost.
		cenv := carryEnv{rules: h.shop.Carry, clock: h.shop.Clock}
		if _, err := cenv.load(ctx, tx, snap, p.ID, now, true); err != nil {
			return err
		}
		if piece, err = tx.Items().Piece(ctx, piece.ID); err != nil {
			return err
		}
		cost := h.shop.Carry.RepairCost(def.BasePrice, piece.UsesLeft, def.Durability)
		if cost < 1 {
			return h.shopRefused(village.ShopRefusalView{Kind: village.ShopRefusedNothingToMend, Item: named(def.Code, def.Name)})
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(money.FromMinor(cost), snap.ShopAccepts("village_shop"))
		if len(plan.Usable) == 0 {
			return declined(plan, wallet, "village.shop.button.back", village.AddrShop)
		}
		repairID := h.ids.NewID()
		if _, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
			Method: plan.Usable[0], Accepted: plan.Accepted, Reason: application.ReasonBagRepair,
			ReferenceType: application.BagRepairReference, ReferenceID: repairID,
			To:        []application.LedgerEntry{{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(cost)}},
			CreatedAt: now,
		}); err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "village.shop.button.back", village.AddrShop)
			}
			return err
		}
		if err := tx.Items().SetUses(ctx, piece.ID, def.Durability); err != nil {
			return err
		}
		mended = &village.ShopMended{Item: named(def.Code, def.Name), Cost: cost}
		return appendVillageEvent(ctx, tx, meta, "bag_mended", s.CityID, map[string]any{
			"settlement_id": s.CityID, "player_id": p.ID, "item": code, "serial": piece.Serial, "cost": cost,
			"repair_id": repairID, "content_version": snap.Version(),
		})
	})
	if resp, ferr := h.shopFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return h.shopScreen(ctx, meta, nil, mended)
}

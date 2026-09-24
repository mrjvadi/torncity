package handlers

import (
	"context"
	"encoding/binary"
	stderrors "errors"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/shop"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// ProductionRules is the tuning of the production economy (config company,
// and the bank's bounds on one amount of money).
type ProductionRules struct {
	MaxRunningOrders int
	MaxDesigns       int
	MaxListings      int
	DesignMinSkill   int
	// ReverseTime is taking a sample apart, GAME time.
	ReverseTime time.Duration
	// NameMin and NameMax bound a design's name, like a company's.
	NameMin, NameMax int
	// Limits bounds a license price and an amount bought.
	Limits bank.Limits
	// Citizens is citizen labour: the openings no player has taken count
	// as crew for as many citizens as the company can pay for a period.
	Citizens company.CitizenRules
}

// ProductionHandler serves the production economy
// (docs/adr/0021-production-economy.md): a company's warehouse and its NPC
// suppliers, its research lab and its technologies — researched once,
// shared as private, licensed or published — its design studio, its
// production orders, its reverse-engineering lab and its goods for sale; the
// city's company goods players and companies buy; and, from the scheduler, a
// research, an order and a reverse engineering finishing — each once.
//
// # What is whose
//
// A company's goods are an Org's in the item journal: counted units in its
// warehouse (org_stacks), pieces with the company as holder. Its money is its
// treasury, and everything it spends by choice — research, a supplier's
// delivery, a license, goods it buys — comes out of the money its running
// shifts have not reserved (company.Books.Available).
//
// # Who may do what
//
// The owner and the manager run the floor (company.RightProduce); only the
// owner spends on research and decides how a technology is shared or buys a
// license (company.RightResearch). Designing and reverse engineering need a
// member — owner, manager or employee — with the craft skill: the company's
// engineer is its most skilled member in it.
type ProductionHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	scale   gametime.Scale
	rules   ProductionRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewProductionHandler wires the handler.
func NewProductionHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, rules ProductionRules,
	idempotencyTTL time.Duration, now func() time.Time,
) *ProductionHandler {
	if source == nil || cities == nil || policy == nil || ids == nil {
		panic("handlers: NewProductionHandler requires content, cities, a policy reader and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || rules.MaxRunningOrders < 1 || rules.MaxDesigns < 1 ||
		rules.MaxListings < 1 || rules.ReverseTime <= 0 || rules.NameMin < 1 || rules.NameMax < rules.NameMin ||
		rules.Limits.Max.Minor() <= 0 {
		panic("handlers: NewProductionHandler requires a game clock, an idempotency ttl and valid rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &ProductionHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy,
		scale: scale, rules: rules, idempotencyTTL: idempotencyTTL, now: now}
}

// ProductionRequest is the payload of the production commands; which fields a
// command reads is its own. The names are those internal/gateway/routing
// gives the arguments.
type ProductionRequest struct {
	Company   string `json:"company,omitempty"`
	Tech      string `json:"tech,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Price     string `json:"price,omitempty"`
	Confirm   string `json:"confirm,omitempty"`
	From      string `json:"from,omitempty"`
	Item      string `json:"item,omitempty"`
	No        string `json:"no,omitempty"`
	Slot      string `json:"slot,omitempty"`
	Component string `json:"component,omitempty"`
	Qty       string `json:"qty,omitempty"`
	Name      string `json:"name,omitempty"`
	Target    string `json:"target,omitempty"`
	Serial    string `json:"serial,omitempty"`
	Method    string `json:"method,omitempty"`
}

func (r ProductionRequest) code() string { return playercode.Normalize(r.Company) }

func (r ProductionRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == screens.ProductionConfirm
}

// number reads a public number argument.
func number(raw string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return n, err == nil && n > 0
}

// quantityArg reads a typed or pressed quantity: Persian digits and
// separators were made ASCII by the gateway; commas are dropped here.
func quantityArg(raw string) (int64, bool) {
	return number(strings.NewReplacer(",", "", " ", "").Replace(raw))
}

func (h *ProductionHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// productionRefusal carries a refused production command out of a unit of
// work.
type productionRefusal struct{ view screens.ProductionRefusalView }

func (r *productionRefusal) Error() string { return "handlers: production refused: " + r.view.Kind }

// refuseProduction is a refusal about c (nil when none is known).
func refuseProduction(kind string, c *application.Company, snap *content.Snapshot) *productionRefusal {
	r := &productionRefusal{view: screens.ProductionRefusalView{Kind: kind}}
	if c != nil {
		r.view.Ref = companyRef(snap, *c)
	}
	return r
}

// back sets where the refusal's back button leads.
func (r *productionRefusal) back(addr ...string) *productionRefusal {
	r.view.Back = addr
	return r
}

// finish turns a refusal into its screen.
func (h *ProductionHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *productionRefusal
	if stderrors.As(err, &r) {
		return screens.ProductionRefusal(c, r.view), nil
	}
	if v, ok := asCompanyRefusal(err); ok {
		return screens.CompanyRefusal(c, v), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(c, v), nil
	}
	if v, ok := asBlocked(err); ok {
		return screens.SanctionBlocked(c, v), nil
	}
	return nil, err
}

// reserve takes the idempotency key of a command that writes.
func (h *ProductionHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// player reads the player behind a command and the language to answer in.
func (h *ProductionHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

// managed reads (and locks) an active company the player may use right on.
func (h *ProductionHandler) managed(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	code string, right company.Right,
) (*application.Company, error) {
	if !playercode.Valid(code) {
		return nil, refuseCompany(screens.CompanyRefusedNotFound, nil, snap)
	}
	c, err := tx.Companies().ByCode(ctx, code)
	if isSentinel(err, application.ErrCompanyNotFound) {
		return nil, refuseCompany(screens.CompanyRefusedNotFound, nil, snap)
	}
	if err != nil {
		return nil, err
	}
	if c, err = tx.Companies().Lock(ctx, c.ID); err != nil {
		return nil, err
	}
	role := company.RoleOf(p.ID, c.OwnerID, c.ManagerID)
	switch {
	case role == company.RoleNone:
		return nil, refuseCompany(screens.CompanyRefusedNotAllowed, c, snap)
	case !c.Active():
		return nil, refuseCompany(screens.CompanyRefusedDissolved, c, snap)
	case role.Check(right) != nil:
		return nil, refuseCompany(screens.CompanyRefusedNotAllowed, c, snap)
	}
	return c, nil
}

// floor is a company as the production rules read it: its kind, what it may
// build on, and its people with their skills.
type floor struct {
	c       *application.Company
	def     content.CompanyTypeDef
	owned   []application.CompanyTech
	ownSet  item.Set
	public  []string
	pubSet  item.Set
	licence []application.License
	access  item.TechAccess
	// members are the owner, the manager and the employees.
	members []string
	staff   int
	// citizens is how many citizens work its untaken openings; set by
	// withCitizens, which production orders call before sizing a crew.
	citizens int
	skills   map[string]map[string]int
}

// readFloor reads a company's floor.
func readFloor(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company) (*floor, error) {
	def, _, err := companyType(snap, *c)
	if err != nil {
		return nil, err
	}
	f := &floor{c: c, def: def, skills: map[string]map[string]int{}}
	if f.owned, err = tx.Production().Technologies(ctx, c.ID); err != nil {
		return nil, err
	}
	if f.public, err = tx.Production().Published(ctx); err != nil {
		return nil, err
	}
	if f.licence, err = tx.Production().Licenses(ctx, c.ID); err != nil {
		return nil, err
	}
	var own, lic []string
	for _, t := range f.owned {
		own = append(own, t.Tech)
	}
	for _, l := range f.licence {
		lic = append(lic, l.Tech)
	}
	f.ownSet, f.pubSet = item.NewSet(own...), item.NewSet(f.public...)
	f.access = technology.Access(own, f.public, lic)
	staff, err := tx.Companies().Staff(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	f.staff = len(staff)
	seen := map[string]bool{}
	for _, id := range append([]string{c.OwnerID, c.ManagerID}, staffIDs(staff)...) {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		f.members = append(f.members, id)
	}
	return f, nil
}

func staffIDs(staff []application.CompanyEmployee) []string {
	out := make([]string, 0, len(staff))
	for _, s := range staff {
		out = append(out, s.PlayerID)
	}
	return out
}

// best is the company's best level in a skill and who has it: its engineer
// for the craft.
func (f *floor) best(ctx context.Context, tx application.Tx, skill string) (int, string, error) {
	level, who := 0, ""
	if skill == "" {
		return 0, "", nil
	}
	for _, id := range f.members {
		byID, ok := f.skills[id]
		if !ok {
			byID = map[string]int{}
			f.skills[id] = byID
		}
		l, ok := byID[skill]
		if !ok {
			s, err := tx.Skills().Get(ctx, id, skill)
			switch {
			case err == nil:
				l = s.Level
			case isSentinel(err, application.ErrSkillNotFound):
			default:
				return 0, "", err
			}
			byID[skill] = l
		}
		if l > level || who == "" {
			level, who = l, id
		}
	}
	return min(max(level, 0), item.MaxSkillLevel), who, nil
}

// crew is how many work an order: the owner and every employee.
func (f *floor) crew() int { return 1 + f.staff + f.citizens }

// books reads a company's money under its lock.
func (f *floor) books(ctx context.Context, tx application.Tx) (company.Books, application.Account, error) {
	return companyBooks(ctx, tx, *f.c)
}

// spend moves amount out of the company's free money to an account under
// reason, or refuses when the company cannot spare it.
func (h *ProductionHandler) spend(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor,
	reason application.Reason, to string, amount money.Amount, now time.Time,
) (string, error) {
	if amount.IsZero() {
		return "", nil
	}
	b, acct, err := f.books(ctx, tx)
	if err != nil {
		return "", err
	}
	if b.Available().Minor() < amount.Minor() {
		r := refuseProduction(screens.ProductionRefusedFunds, f.c, snap)
		r.view.Need, r.view.HaveMoney = amount.Minor(), b.Available().Minor()
		return "", r
	}
	return post(ctx, tx.Ledger(), reason, f.c.ID, acct.ID, to, amount, now)
}

// rollFrom is a roll in [0, item.RollScale) drawn from a seed: the same seed
// always rolls the same, so a replayed completion cannot roll again, and a
// test can reproduce any outcome.
func rollFrom(seed string, index int) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(index))
	_, _ = h.Write(b[:])
	return int(h.Sum64() % uint64(item.RollScale))
}

// ---------------------------------------------------------------------------
// The warehouse.

// goodOf names a warehouse code for a screen: a component or a good.
func goodOf(snap *content.Snapshot, code string) screens.Good {
	if c, ok := snap.ComponentDef(code); ok {
		return screens.Good{Component: true, Item: named(c.Code, c.Name)}
	}
	return screens.Good{Item: itemNamed(snap, code)}
}

// designGood names a good of a design.
func designGood(snap *content.Snapshot, d application.Design) screens.Good {
	return screens.Good{Item: itemNamed(snap, d.Item), Design: designName(d), DesignNo: d.No}
}

// designName is the name a design goes by.
func designName(d application.Design) string { return d.Name }

// Warehouse handles company.warehouse: a company's goods, the hub of its
// floor.
func (h *ProductionHandler) Warehouse(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	return h.warehouseWith(ctx, meta, req, "")
}

func (h *ProductionHandler) warehouseWith(ctx context.Context, meta envelope.Metadata, req ProductionRequest, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.WarehouseView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		view = screens.WarehouseView{Ref: companyRef(snap, *c),
			CanResearch: company.RoleOf(p.ID, c.OwnerID, c.ManagerID).Can(company.RightResearch)}
		if view.Lines, err = h.stock(ctx, tx, snap, *c); err != nil {
			return err
		}
		if view.Running, err = tx.Production().RunningOrders(ctx, c.ID); err != nil {
			return err
		}
		running, err := tx.Production().RunningResearch(ctx, c.ID)
		if err != nil {
			return err
		}
		view.Researching = running != nil
		listings, err := tx.Production().CompanyListings(ctx, c.ID)
		view.Listings = len(listings)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	c := h.screen(meta, lang)
	view.Notice = notice
	return screens.Warehouse(c, view), nil
}

// stock reads a company's warehouse as lines: counted units by code, pieces
// grouped by good and design.
func (h *ProductionHandler) stock(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company) ([]screens.WarehouseLine, error) {
	org := application.CompanyOrg(c.ID)
	stacks, pieces, err := tx.Items().OrgHoldings(ctx, org, application.HoldWarehouse)
	if err != nil {
		return nil, err
	}
	listedStacks, listedPieces, err := tx.Items().OrgHoldings(ctx, org, application.HoldListed)
	if err != nil {
		return nil, err
	}
	listed := map[string]int64{}
	for _, s := range listedStacks {
		listed[s.Item] += s.Qty
	}
	var out []screens.WarehouseLine
	for _, s := range stacks {
		g := goodOf(snap, s.Item)
		out = append(out, screens.WarehouseLine{Good: g, Qty: s.Qty, Listed: listed[s.Item], Sellable: h.sellable(snap, s.Item)})
		delete(listed, s.Item)
	}
	for code, q := range listed {
		out = append(out, screens.WarehouseLine{Good: goodOf(snap, code), Listed: q})
	}
	type group struct {
		line    screens.WarehouseLine
		quality int
	}
	groups := map[string]*group{}
	var order []string
	designs := map[string]*application.Design{}
	key := func(p application.Piece) string { return p.Item + "|" + p.DesignID }
	add := func(p application.Piece, listedPiece bool) error {
		k := key(p)
		g, ok := groups[k]
		if !ok {
			good := screens.Good{Item: itemNamed(snap, p.Item)}
			if p.DesignID != "" {
				d, seen := designs[p.DesignID]
				if !seen {
					var err error
					if d, err = tx.Production().DesignByID(ctx, p.DesignID); err != nil {
						return err
					}
					designs[p.DesignID] = d
				}
				good = designGood(snap, *d)
			}
			g = &group{line: screens.WarehouseLine{Good: good, Sellable: h.sellable(snap, p.Item)}}
			groups[k] = g
			order = append(order, k)
		}
		if listedPiece {
			g.line.Listed++
			return nil
		}
		g.line.Qty++
		g.quality += p.Quality
		return nil
	}
	for _, p := range pieces {
		if err := add(p, false); err != nil {
			return nil, err
		}
	}
	for _, p := range listedPieces {
		if err := add(p, true); err != nil {
			return nil, err
		}
	}
	for _, k := range order {
		g := groups[k]
		if g.line.Qty > 0 {
			g.line.Quality = max(g.quality/int(g.line.Qty), 1)
		} else {
			g.line.Sellable = false
		}
		out = append(out, g.line)
	}
	for i := range out {
		if out[i].Qty == 0 {
			out[i].Sellable = false
		}
	}
	return out, nil
}

// sellable reports whether a warehouse code may be listed: a component, or a
// tradeable good.
func (h *ProductionHandler) sellable(snap *content.Snapshot, code string) bool {
	if _, ok := snap.ComponentDef(code); ok {
		return true
	}
	def, ok := snap.ItemDef(code)
	return ok && def.Item().Tradeable
}

// ---------------------------------------------------------------------------
// NPC suppliers.

// supplierShop is the shop_shelves code of a supplier's stock.
func supplierShop(code string) string { return "supplier:" + code }

// offers reads the suppliers of a company's city with their stock now.
func (h *ProductionHandler) offers(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City,
	now time.Time,
) ([]screens.SupplyOffer, error) {
	var out []screens.SupplyOffer
	for _, sp := range snap.CitySuppliers(city.Code) {
		for _, sh := range sp.Shelves {
			stock, _, err := h.shelf(ctx, tx, city.ID, sp, sh, now)
			if err != nil {
				return nil, err
			}
			comp, _ := snap.ComponentDef(sh.Component)
			out = append(out, screens.SupplyOffer{Supplier: named(sp.Code, sp.Name), Component: named(comp.Code, comp.Name),
				Price: sh.Price, Stock: stock})
		}
	}
	return out, nil
}

// shelf reads (locked) a supplier's shelf in a city, restocked to now on the
// game clock, and returns its stock and the row to save.
func (h *ProductionHandler) shelf(ctx context.Context, tx application.Tx, cityID string, sp content.SupplierDef,
	sh content.SupplyShelfDef, now time.Time,
) (int64, *application.ShopShelf, error) {
	row, err := tx.Shops().Shelf(ctx, cityID, supplierShop(sp.Code), sh.Component)
	if err != nil {
		return 0, nil, err
	}
	refilled := sh.RestockRule().Refill(shop.Shelf{Stock: row.Stock, RestockedAt: row.RestockedAt}, now, h.scale)
	row.Stock, row.RestockedAt = refilled.Stock, refilled.RestockedAt
	return row.Stock, row, nil
}

// Suppliers handles company.suppliers: what the NPC suppliers of the
// company's city sell it.
func (h *ProductionHandler) Suppliers(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	return h.suppliersWith(ctx, meta, req, nil)
}

func (h *ProductionHandler) suppliersWith(ctx context.Context, meta envelope.Metadata, req ProductionRequest,
	bought *screens.SupplyNotice,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.SuppliersView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, c.CityID)
		if err != nil {
			return err
		}
		view = screens.SuppliersView{Ref: companyRef(snap, *c), CityCode: city.Code, City: city.Name, Bought: bought}
		b, _, err := companyBooks(ctx, tx, *c)
		if err != nil {
			return err
		}
		view.Available = b.Available().Minor()
		view.Offers, err = h.offers(ctx, tx, snap, city, h.now())
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Suppliers(h.screen(meta, lang), view), nil
}

// Supply handles company.supply: buying a basic input from an NPC supplier
// into the company's warehouse. The money leaves the economy
// (supplier_purchase); the goods enter it (supplied), never more than the
// supplier holds in the city.
func (h *ProductionHandler) Supply(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	qty, ok := quantityArg(req.Qty)
	if !ok {
		return h.Suppliers(ctx, meta, req)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		bought   *screens.SupplyNotice
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			replayed = !fresh
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, c.CityID)
		if err != nil {
			return err
		}
		code := strings.TrimSpace(req.Component)
		var (
			sp    content.SupplierDef
			sh    content.SupplyShelfDef
			found bool
		)
		for _, s := range snap.CitySuppliers(city.Code) {
			if shelf, ok := s.Shelf(code); ok {
				sp, sh, found = s, shelf, true
				break
			}
		}
		if !found {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrSuppliers, c.Code)
		}
		if qty > h.rules.Limits.Max.Minor()/max(sh.Price, 1) {
			return refuseProduction(screens.ProductionRefusedAmount, c, snap).back(screens.AddrSuppliers, c.Code)
		}
		now := h.now()
		stock, row, err := h.shelf(ctx, tx, city.ID, sp, sh, now)
		if err != nil {
			return err
		}
		if stock < qty {
			r := refuseProduction(screens.ProductionRefusedSupplierEmpty, c, snap).back(screens.AddrSuppliers, c.Code)
			r.view.Max = int(stock)
			return r
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		total := money.FromMinor(qty * sh.Price)
		txID, err := h.spend(ctx, tx, snap, f, application.ReasonSupplierPurchase, application.SystemSinkAccountID, total, now)
		if err != nil {
			return err
		}
		row.Stock -= qty
		if err := tx.Shops().SaveShelf(ctx, *row); err != nil {
			return err
		}
		purchase := application.SupplyPurchase{ID: h.ids.NewID(), CompanyID: c.ID, CityID: city.ID, Supplier: sp.Code,
			Component: code, Qty: qty, UnitPrice: sh.Price, Total: total.Minor(), LedgerTransactionID: txID, BoughtBy: p.ID, At: now}
		if err := tx.Production().RecordSupply(ctx, purchase); err != nil {
			return err
		}
		org := application.CompanyOrg(c.ID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: code, Qty: qty, ToOrg: org,
			ToHolding: application.HoldWarehouse, Reason: application.ItemSupplied, ReferenceType: "supply_purchases",
			ReferenceID: purchase.ID, At: now}); err != nil {
			return err
		}
		comp, _ := snap.ComponentDef(code)
		bought = &screens.SupplyNotice{Component: named(comp.Code, comp.Name), Qty: qty, Total: total.Minor()}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		bought = nil
	}
	return h.suppliersWith(ctx, meta, req, bought)
}

// sortedTechs orders technologies as the tree lists them.
func sortedTechs(snap *content.Snapshot, codes []string) []string {
	pos := map[string]int{}
	for i, t := range snap.Technologies() {
		pos[t.Code] = i
	}
	out := append([]string(nil), codes...)
	sort.SliceStable(out, func(i, j int) bool { return pos[out[i]] < pos[out[j]] })
	return out
}

// hasCode reports whether list holds v.
func hasCode(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// internalf is a fault that is the content's or the code's, never a player's.
func internalf(msg string) error { return errors.Internal(stderrors.New("handlers: " + msg)) }

// withCitizens counts the citizens on a company's untaken openings into its
// floor's crew: as many as its free money pays for a full period, the same
// rule the period's settlement pays them by.
func (h *ProductionHandler) withCitizens(ctx context.Context, tx application.Tx, f *floor) error {
	if h.rules.Citizens.Validate() != nil {
		return nil
	}
	vacancies, err := citizenVacancies(ctx, tx, f.c.ID)
	if err != nil || len(vacancies) == 0 {
		return err
	}
	books, _, err := companyBooks(ctx, tx, *f.c)
	if err != nil {
		return err
	}
	positions := 0
	for _, v := range vacancies {
		positions += v.Positions
	}
	plan, err := company.PlanCitizens(vacancies, h.rules.Citizens, 10000, positions, books.Available())
	if err != nil {
		return errors.Internal(err)
	}
	f.citizens = plan.Workers
	return nil
}

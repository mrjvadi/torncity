package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/inventory"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/shop"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// A company's goods for sale (docs/adr/0021-production-economy.md): a line of
// its warehouse put up at a fixed unit price in its city — the goods set
// aside in the 'listed' holding until sold or withdrawn — and bought by a
// player (cash or card) or by another company (its free money). The money
// goes to the seller's treasury (company_sale) and the city's sales tax from
// there to the city (sales_tax); the goods go to the buyer's bag or
// warehouse (company_sale in the journal). Export control is checked on
// every sale. Materials and components are sold to companies only.

// listingReference is the ledger and journal reference_type of a sale.
const listingReference = "company_listings"

// lineOf is what a sale target names in a company's warehouse.
type lineOf struct {
	good   screens.Good
	code   string
	design *application.Design
	// pieces is true for unique goods, sold piece by piece.
	pieces bool
	// reference is the good's reference price.
	reference int64
}

// saleLine resolves a sale target: «d12» for pieces of a design, else an
// item or component code.
func (h *ProductionHandler) saleLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
	raw string,
) (lineOf, error) {
	raw = strings.TrimSpace(raw)
	back := []string{screens.AddrWarehouse, c.Code}
	if no, ok := strings.CutPrefix(raw, screens.DesignTargetPrefix); ok {
		n, ok := number(no)
		if !ok {
			return lineOf{}, refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(back...)
		}
		d, err := tx.Production().Design(ctx, n, false)
		if isSentinel(err, application.ErrDesignNotFound) {
			return lineOf{}, refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(back...)
		}
		if err != nil {
			return lineOf{}, err
		}
		def, _ := snap.ItemDef(d.Item)
		ref := def.BasePrice
		if recipe, err := item.DeriveRecipe(domainDesign(*d)); err == nil {
			if cost, err := item.ItemizedCost(recipe, prices(snap)); err == nil && cost.Minor() > 0 {
				ref = cost.Minor()
			}
		}
		return lineOf{good: designGood(snap, *d), code: d.Item, design: d, pieces: def.Form == string(inventory.Unique),
			reference: max(ref, 1)}, nil
	}
	if comp, ok := snap.ComponentDef(raw); ok {
		return lineOf{good: screens.Good{Component: true, Item: named(comp.Code, comp.Name)}, code: raw, reference: comp.BasePrice}, nil
	}
	def, ok := snap.ItemDef(raw)
	if !ok || !def.Item().Tradeable {
		return lineOf{}, refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(back...)
	}
	return lineOf{good: screens.Good{Item: named(def.Code, def.Name)}, code: raw, pieces: def.Form == string(inventory.Unique),
		reference: def.BasePrice}, nil
}

// held is what the company holds of a line in a holding: the count, and the
// pieces themselves for a unique good, best first.
func (l lineOf) held(ctx context.Context, tx application.Tx, org application.Org, holding string) (int64, []application.Piece, error) {
	stacks, pieces, err := tx.Items().OrgHoldings(ctx, org, holding)
	if err != nil {
		return 0, nil, err
	}
	if !l.pieces {
		for _, s := range stacks {
			if s.Item == l.code {
				return s.Qty, nil, nil
			}
		}
		return 0, nil, nil
	}
	designID := ""
	if l.design != nil {
		designID = l.design.ID
	}
	var out []application.Piece
	for _, p := range pieces {
		if p.Item == l.code && p.DesignID == designID {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Quality > out[j].Quality })
	return int64(len(out)), out, nil
}

// moveLine moves qty of a line between holdings: units of a stack, or the
// first qty pieces.
func (h *ProductionHandler) moveLine(ctx context.Context, tx application.Tx, l lineOf, pieces []application.Piece, qty int64,
	m application.ItemMove,
) error {
	m.Item = l.code
	if !l.pieces {
		m.ID, m.Qty = h.ids.NewID(), qty
		return tx.Items().Move(ctx, m)
	}
	if int64(len(pieces)) < qty {
		return application.ErrNotEnoughItems
	}
	for _, p := range pieces[:qty] {
		pm := m
		pm.ID, pm.PieceID, pm.Qty = h.ids.NewID(), p.ID, 1
		if err := tx.Items().Move(ctx, pm); err != nil {
			return err
		}
	}
	return nil
}

// Sell handles company.sell: putting a line of the warehouse up for sale —
// the quantity, then a typed price — as an open listing in the company's
// city.
func (h *ProductionHandler) Sell(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	qty, _ := quantityArg(req.Qty)
	price, priced := quantityArg(req.Price)
	var (
		view   screens.SellView
		listed *screens.ListingLine
		seller *application.Company
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		place := qty > 0 && strings.TrimSpace(req.Price) != ""
		if place {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil || !fresh {
				place = false
				if err != nil {
					return err
				}
			}
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		seller = c
		l, err := h.saleLine(ctx, tx, snap, c, req.Target)
		if err != nil {
			return err
		}
		org := application.CompanyOrg(c.ID)
		if place {
			if err := tx.Items().LockOrg(ctx, org); err != nil {
				return err
			}
		}
		have, pieces, err := l.held(ctx, tx, org, application.HoldWarehouse)
		if err != nil {
			return err
		}
		view = screens.SellView{Ref: companyRef(snap, *c), Good: l.good, Have: have, Qty: min(qty, have), Reference: l.reference}
		back := []string{screens.AddrWarehouse, c.Code}
		if have == 0 {
			return refuseProduction(screens.ProductionRefusedStock, c, snap).back(back...)
		}
		if qty > have {
			r := refuseProduction(screens.ProductionRefusedStock, c, snap).back(back...)
			r.view.Max = int(have)
			return r
		}
		if !place {
			return nil
		}
		if !priced || price > shop.MaxPrice {
			return refuseProduction(screens.ProductionRefusedAmount, c, snap).back(screens.AddrSell, c.Code, l.good.TargetArg(),
				strconv.FormatInt(qty, 10))
		}
		open, err := tx.Production().CompanyListings(ctx, c.ID)
		if err != nil {
			return err
		}
		if len(open) >= h.rules.MaxListings {
			r := refuseProduction(screens.ProductionRefusedMaxListings, c, snap).back(screens.AddrListings, c.Code)
			r.view.Max = h.rules.MaxListings
			return r
		}
		now := h.now()
		listing := application.Listing{ID: h.ids.NewID(), CompanyID: c.ID, CityID: c.CityID, Item: l.code, Qty: qty,
			UnitPrice: price, CreatedAt: now}
		if l.design != nil {
			listing.DesignID = l.design.ID
		}
		if listing, err = tx.Production().OpenListing(ctx, listing); err != nil {
			if isSentinel(err, application.ErrListingOpen) {
				return refuseProduction(screens.ProductionRefusedListed, c, snap).back(screens.AddrListings, c.Code)
			}
			return err
		}
		if err := h.moveLine(ctx, tx, l, pieces, qty, application.ItemMove{FromOrg: org, FromHolding: application.HoldWarehouse,
			ToOrg: org, ToHolding: application.HoldListed, Reason: application.ItemListingEscrow,
			ReferenceType: listingReference, ReferenceID: listing.ID, At: now}); err != nil {
			return err
		}
		listed = &screens.ListingLine{No: listing.No, Good: l.good, Left: qty, Price: price}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if listed != nil {
		c := h.screen(meta, lang)
		return h.listingsWith(ctx, meta, ProductionRequest{Company: seller.Code},
			screens.ListingNotice(c, screens.ListingNoticeListed, *listed))
	}
	return screens.Sell(h.screen(meta, lang), view), nil
}

// Listings handles company.listings: the company's open listings.
func (h *ProductionHandler) Listings(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	return h.listingsWith(ctx, meta, req, "")
}

func (h *ProductionHandler) listingsWith(ctx context.Context, meta envelope.Metadata, req ProductionRequest, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ListingsView
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
		view = screens.ListingsView{Ref: companyRef(snap, *c), CityCode: city.Code, City: city.Name, Notice: notice}
		open, err := tx.Production().CompanyListings(ctx, c.ID)
		if err != nil {
			return err
		}
		for _, l := range open {
			good, err := h.listingGood(ctx, tx, snap, l)
			if err != nil {
				return err
			}
			view.Listings = append(view.Listings, screens.ListingLine{No: l.No, Good: good, Left: l.Left(), Price: l.UnitPrice})
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Listings(h.screen(meta, lang), view), nil
}

// listingGood names a listing's good.
func (h *ProductionHandler) listingGood(ctx context.Context, tx application.Tx, snap *content.Snapshot, l application.Listing) (screens.Good, error) {
	if l.DesignID == "" {
		return goodOf(snap, l.Item), nil
	}
	d, err := tx.Production().DesignByID(ctx, l.DesignID)
	if err != nil {
		return screens.Good{}, err
	}
	return designGood(snap, *d), nil
}

// listingLine resolves a listing's line of goods.
func (h *ProductionHandler) listingLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, l application.Listing) (lineOf, error) {
	out := lineOf{code: l.Item}
	if l.DesignID != "" {
		d, err := tx.Production().DesignByID(ctx, l.DesignID)
		if err != nil {
			return out, err
		}
		out.design, out.good = d, designGood(snap, *d)
	} else {
		out.good = goodOf(snap, l.Item)
	}
	if def, ok := snap.ItemDef(l.Item); ok {
		out.pieces = def.Form == string(inventory.Unique)
	}
	return out, nil
}

// Unlist handles company.unlist: an open listing withdrawn, what is left of
// its goods back in the warehouse.
func (h *ProductionHandler) Unlist(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		code   string
		notice *screens.ListingLine
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		no, ok := number(req.No)
		if !ok {
			return refuseProduction(screens.ProductionRefusedNotFound, nil, snap)
		}
		l, err := tx.Production().Listing(ctx, no, false)
		if isSentinel(err, application.ErrListingNotFound) {
			return refuseProduction(screens.ProductionRefusedNotFound, nil, snap)
		}
		if err != nil {
			return err
		}
		owner, err := tx.Companies().ByID(ctx, l.CompanyID)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, owner.Code, company.RightProduce)
		if err != nil {
			return err
		}
		code = c.Code
		org := application.CompanyOrg(c.ID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		if l, err = tx.Production().Listing(ctx, no, true); err != nil {
			return err
		}
		if l.Status != application.ListingOpen {
			return nil
		}
		line, err := h.listingLine(ctx, tx, snap, *l)
		if err != nil {
			return err
		}
		_, pieces, err := line.held(ctx, tx, org, application.HoldListed)
		if err != nil {
			return err
		}
		now := h.now()
		left := l.Left()
		if left > 0 {
			if err := h.moveLine(ctx, tx, line, pieces, left, application.ItemMove{FromOrg: org, FromHolding: application.HoldListed,
				ToOrg: org, ToHolding: application.HoldWarehouse, Reason: application.ItemListingRelease,
				ReferenceType: listingReference, ReferenceID: l.ID, At: now}); err != nil {
				return err
			}
		}
		l.Status, l.UpdatedAt, l.ClosedAt = application.ListingWithdrawn, now, &now
		if err := tx.Production().SaveListing(ctx, *l); err != nil {
			return err
		}
		notice = &screens.ListingLine{No: l.No, Good: line.good, Left: left, Price: l.UnitPrice}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	text := ""
	if notice != nil {
		text = screens.ListingNotice(h.screen(meta, lang), screens.ListingNoticeWithdrawn, *notice)
	}
	return h.listingsWith(ctx, meta, ProductionRequest{Company: code}, text)
}

// goodsLine is a listing as the city's buyers see it.
func (h *ProductionHandler) goodsLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, l application.Listing) (screens.GoodsLine, error) {
	seller, err := tx.Companies().ByID(ctx, l.CompanyID)
	if err != nil {
		return screens.GoodsLine{}, err
	}
	line := screens.GoodsLine{No: l.No, Company: companyRef(snap, *seller), Left: l.Left(), Price: l.UnitPrice}
	if line.Good, err = h.listingGood(ctx, tx, snap, l); err != nil {
		return line, err
	}
	if l.DesignID != "" {
		d, err := tx.Production().DesignByID(ctx, l.DesignID)
		if err != nil {
			return line, err
		}
		if a, ok := snap.Archetype(d.Archetype); ok {
			// What a competitor may see: the observable attributes, never
			// the bill of materials (ADR 0005 §5).
			if attrs, err := item.ObservableAttributes(a, domainDesign(*d), snap.Components()); err == nil {
				for _, at := range a.Attributes {
					if v, ok := attrs[at.Name]; ok {
						line.Attributes = append(line.Attributes, screens.AttributeLine{Name: at.Name, Value: v, Observable: true})
					}
				}
			}
		}
	}
	return line, nil
}

// Goods handles company.goods: what the companies of the player's city
// sell.
func (h *ProductionHandler) Goods(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.GoodsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		city, err := h.hereCity(ctx, tx, p)
		if err != nil {
			return err
		}
		if city == nil {
			view.NoCity = true
			return nil
		}
		view.CityCode, view.City = city.Code, city.Name
		open, err := tx.Production().CityListings(ctx, city.ID)
		if err != nil {
			return err
		}
		for _, l := range open {
			if _, military := snap.ClassOfItem(l.Item); military {
				// Arms go to states only: procurement, not the city's goods.
				continue
			}
			line, err := h.goodsLine(ctx, tx, snap, l)
			if err != nil {
				return err
			}
			view.Lines = append(view.Lines, line)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.CompanyGoods(h.screen(meta, lang), view), nil
}

// hereCity is the city a player stands in, nil while travelling or nowhere.
func (h *ProductionHandler) hereCity(ctx context.Context, tx application.Tx, p *application.Player) (*application.City, error) {
	if p.CityID == nil || *p.CityID == "" {
		return nil, nil
	}
	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		return nil, nil
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return nil, err
	}
	return h.cities.ByID(ctx, *p.CityID)
}

// buyers are the companies a player may buy for: those they run, bar the
// seller.
func (h *ProductionHandler) buyers(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	sellerID string,
) ([]application.Company, error) {
	mine, err := tx.Companies().Of(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var out []application.Company
	for _, c := range mine {
		if c.ID != sellerID && c.Active() && company.RoleOf(p.ID, c.OwnerID, c.ManagerID).Can(company.RightProduce) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Buy handles company.buy: buying from a company's listing — for the player,
// paid by cash or card, or for a company the player runs, paid from its free
// money. Without a way to pay, it shows the listing and the ways.
func (h *ProductionHandler) Buy(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	qty := int64(1)
	if strings.TrimSpace(req.Qty) != "" {
		var ok bool
		if qty, ok = quantityArg(req.Qty); !ok {
			return nil, errors.InvalidInput("a quantity is a whole number")
		}
	}
	way := strings.TrimSpace(req.Method)
	var view screens.BuyView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		buy := way != ""
		if buy {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			buy = fresh
		}
		no, ok := number(req.No)
		if !ok {
			return refuseProduction(screens.ProductionRefusedNotFound, nil, snap).back(screens.AddrCompanyGoods)
		}
		l, err := tx.Production().Listing(ctx, no, false)
		if isSentinel(err, application.ErrListingNotFound) || (err == nil && l.Status != application.ListingOpen) {
			return refuseProduction(screens.ProductionRefusedNotFound, nil, snap).back(screens.AddrCompanyGoods)
		}
		if err != nil {
			return err
		}
		line, err := h.goodsLine(ctx, tx, snap, *l)
		if err != nil {
			return err
		}
		view = screens.BuyView{Line: line, Qty: min(qty, max(l.Left(), 1))}
		companies, err := h.buyers(ctx, tx, snap, p, l.CompanyID)
		if err != nil {
			return err
		}
		_, component := snap.ComponentDef(l.Item)
		if !buy {
			for _, c := range companies {
				view.Companies = append(view.Companies, companyRef(snap, c))
			}
			if !component {
				wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
				if err != nil {
					return err
				}
				choice := paymentChoice(wallet.Plan(money.FromMinor(view.Qty*l.UnitPrice), snap.Accepts(content.ServiceShop)), wallet)
				view.Payment = &choice
			}
			return nil
		}
		return h.buy(ctx, tx, snap, meta, p, l, qty, way, companies, component, &view)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyBuy(h.screen(meta, lang), view), nil
}

// buy makes one purchase from a listing, everything under the locks: the
// companies in id order, then the seller's goods, then the listing.
func (h *ProductionHandler) buy(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	p *application.Player, l *application.Listing, qty int64, way string, companies []application.Company, component bool,
	view *screens.BuyView,
) error {
	back := []string{screens.AddrCompanyBuy, strconv.FormatInt(l.No, 10)}
	var buyer *application.Company
	method, isMethod, _ := chosenMethod(way)
	if !isMethod {
		code := playercode.Normalize(way)
		for i := range companies {
			if companies[i].Code == code {
				buyer = &companies[i]
			}
		}
		if buyer == nil {
			return refuseCompany(screens.CompanyRefusedNotAllowed, nil, snap)
		}
	}
	if component && buyer == nil {
		return refuseProduction(screens.ProductionRefusedNotCleared, nil, snap).back(back...)
	}
	// Lock the companies in id order, so two companies buying from each
	// other cannot deadlock.
	ids := []string{l.CompanyID}
	if buyer != nil {
		ids = append(ids, buyer.ID)
	}
	sort.Strings(ids)
	locked := map[string]*application.Company{}
	for _, id := range ids {
		c, err := tx.Companies().Lock(ctx, id)
		if err != nil {
			return err
		}
		locked[id] = c
	}
	seller := locked[l.CompanyID]
	if buyer != nil {
		buyer = locked[buyer.ID]
		if !buyer.Active() {
			return refuseCompany(screens.CompanyRefusedDissolved, buyer, snap)
		}
	}
	sellerOrg := application.CompanyOrg(seller.ID)
	if err := tx.Items().LockOrg(ctx, sellerOrg); err != nil {
		return err
	}
	if buyer != nil {
		if err := tx.Items().LockOrg(ctx, application.CompanyOrg(buyer.ID)); err != nil {
			return err
		}
	}
	l, err := tx.Production().Listing(ctx, l.No, true)
	if err != nil {
		return err
	}
	if l.Status != application.ListingOpen || !seller.Active() {
		return refuseProduction(screens.ProductionRefusedNotFound, nil, snap).back(screens.AddrCompanyGoods)
	}
	if qty < 1 || qty > l.Left() {
		r := refuseProduction(screens.ProductionRefusedStock, nil, snap).back(back...)
		r.view.Max = int(l.Left())
		return r
	}
	if buyer == nil && p.ID == seller.OwnerID {
		return refuseProduction(screens.ProductionRefusedOwnListing, nil, snap).back(back...)
	}
	// Export control: the one place a sale of goods is cleared.
	control := technology.Control{}
	if def, ok := snap.ItemDef(l.Item); ok {
		control = def.ExportControl.Control()
	}
	who := technology.Buyer{Kind: "player"}
	if buyer != nil {
		def, _, err := companyType(snap, *buyer)
		if err != nil {
			return err
		}
		who = technology.Buyer{Kind: application.OrgCompany, Sector: def.SectorCode()}
	}
	if err := technology.Cleared(control, who); err != nil {
		return refuseProduction(screens.ProductionRefusedNotCleared, nil, snap).back(back...)
	}
	now := h.now()
	// A trade embargo between the buyer's country and the seller's
	// (docs/adr/0022): the one sanctions check.
	sellerCountry, err := tx.Diplomacy().CountryOfCity(ctx, l.CityID)
	if err != nil {
		return err
	}
	var buyerCountry string
	if buyer != nil {
		buyerCountry, err = tx.Diplomacy().CountryOfCity(ctx, buyer.CityID)
	} else {
		buyerCountry, err = nationality(ctx, tx, p)
	}
	if err != nil {
		return err
	}
	if err := checkSanctions(ctx, tx, application.CheckSanctions(ctx, tx, diplomacy.Trade, buyerCountry, sellerCountry, now),
		back...); err != nil {
		return err
	}
	total := money.FromMinor(qty * l.UnitPrice)
	sellerAcct, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, seller.ID)
	if err != nil {
		return err
	}
	var txID string
	if buyer != nil {
		f, err := readFloor(ctx, tx, snap, buyer)
		if err != nil {
			return err
		}
		if txID, err = h.spend(ctx, tx, snap, f, application.ReasonCompanySale, sellerAcct.ID, total, now); err != nil {
			var r *productionRefusal
			if stderrors.As(err, &r) {
				r.back(back...)
			}
			return err
		}
	} else {
		city, err := h.hereCity(ctx, tx, p)
		if err != nil {
			return err
		}
		if city == nil || city.ID != l.CityID {
			listingCity, err := h.cities.ByID(ctx, l.CityID)
			if err != nil {
				return err
			}
			r := refuseProduction(screens.ProductionRefusedAway, nil, snap).back(back...)
			r.view.CityCode, r.view.City = listingCity.Code, listingCity.Name
			return r
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(total, snap.Accepts(content.ServiceShop))
		if err := checkMethod(plan, method, wallet, "production.button.goods", screens.AddrCompanyGoods); err != nil {
			return err
		}
		if txID, err = wallet.Pay(ctx, tx.Ledger(), application.Charge{Method: method, Accepted: plan.Accepted,
			Reason: application.ReasonCompanySale, ReferenceType: listingReference, ReferenceID: l.ID,
			To: []application.LedgerEntry{{AccountID: sellerAcct.ID, Amount: total}}, CreatedAt: now}); err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "production.button.goods", screens.AddrCompanyGoods)
			}
			return err
		}
	}
	city, err := h.cities.ByID(ctx, l.CityID)
	if err != nil {
		return err
	}
	rate, err := h.policy.Get(ctx, city.JurisdictionID, LeverSalesTax)
	if err != nil {
		return err
	}
	tax, err := company.Prorate(total, int(rate.Value))
	if err != nil {
		return errors.Internal(err)
	}
	if !tax.IsZero() {
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
		if err != nil {
			return err
		}
		if _, err := post(ctx, tx.Ledger(), application.ReasonSalesTax, seller.ID, sellerAcct.ID, treasury.ID, tax, now); err != nil {
			return err
		}
	}
	line, err := h.listingLine(ctx, tx, snap, *l)
	if err != nil {
		return err
	}
	_, pieces, err := line.held(ctx, tx, sellerOrg, application.HoldListed)
	if err != nil {
		return err
	}
	m := application.ItemMove{FromOrg: sellerOrg, FromHolding: application.HoldListed, Reason: application.ItemCompanySale,
		ReferenceType: listingReference, ReferenceID: l.ID, At: now}
	sale := application.CompanySale{ID: h.ids.NewID(), ListingID: l.ID, CompanyID: seller.ID, Item: l.Item, Qty: qty,
		UnitPrice: l.UnitPrice, Total: total.Minor(), Tax: tax.Minor(), LedgerTransactionID: txID, At: now}
	if buyer != nil {
		m.ToOrg, m.ToHolding = application.CompanyOrg(buyer.ID), application.HoldWarehouse
		sale.BuyerOrg = application.CompanyOrg(buyer.ID)
	} else {
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		m.To, m.ToHolding = p.ID, application.HoldCarried
		sale.BuyerPlayerID, sale.Method = p.ID, string(method)
	}
	if err := h.moveLine(ctx, tx, line, pieces, qty, m); err != nil {
		return err
	}
	l.Sold += qty
	l.UpdatedAt = now
	if l.Left() == 0 {
		l.Status, l.ClosedAt = application.ListingSold, &now
	}
	if err := tx.Production().SaveListing(ctx, *l); err != nil {
		return err
	}
	if err := tx.Production().RecordSale(ctx, sale); err != nil {
		return err
	}
	view.Bought = &screens.BoughtView{Qty: qty, Total: total.Minor()}
	buyerName := shownName(p)
	if buyer != nil {
		view.Bought.For, view.Bought.ForCode, buyerName = buyer.Name, buyer.Code, buyer.Name
	}
	payload := map[string]any{"company_id": seller.ID, "code": seller.Code, "name": seller.Name, "type": seller.TypeCode,
		"owner_id": seller.OwnerID, "item": l.Item, "qty": qty, "price": total.Minor(), "buyer": buyerName,
		"component": line.good.Component}
	if line.design != nil {
		payload["design"] = line.design.Name
	}
	return appendCompanyEvent(ctx, tx, meta, "sold", seller.ID, payload)
}

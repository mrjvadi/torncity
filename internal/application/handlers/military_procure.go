package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Arms procurement (docs/adr/0022-military-and-diplomacy.md §2.6): the
// holder of country.procure — the defence minister, or the deputy acting
// for the seat — buys military goods from any company's listing, paid from
// the defence fund (arms_procurement) and delivered to the state's depot
// (procured in the journal), each piece recorded as a military asset.
// Export control clears the state as the buyer; a foreign seller's country
// must allow the export (country.arms_exports); and the one sanctions check
// refuses a sale under an arms embargo. A state's purchase pays no sales
// tax.

// procurementReference is the ledger and journal reference of a purchase.
const procurementReference = "procurements"

// armsItems lists every military good.
func armsItems(snap *content.Snapshot) []string {
	var out []string
	for _, c := range snap.ForceClasses() {
		out = append(out, c.Items...)
	}
	sort.Strings(out)
	return out
}

// exportBlock says whether the buyer's state may buy from a seller in
// sellerCountry: "" when it may, else why not. The sanctions check is the
// one application.CheckSanctions.
func (h *MilitaryHandler) exportBlock(ctx context.Context, tx application.Tx, snap *content.Snapshot, buyer, sellerCountry string) (string, error) {
	if sellerCountry == "" || sellerCountry == buyer {
		return "", nil
	}
	err := application.CheckSanctions(ctx, tx, diplomacy.Arms, buyer, sellerCountry, h.now())
	var s *application.SanctionedError
	if stderrors.As(err, &s) {
		return screens.ProcureBlockedEmbargo, nil
	}
	if err != nil {
		return "", err
	}
	denied, err := armsExportDenied(ctx, tx, h.policy, snap, buyer, sellerCountry, h.now())
	if err != nil || !denied {
		return "", err
	}
	return screens.ProcureBlockedExport, nil
}

// armsExportDenied reports whether the seller country's arms export policy
// (country.arms_exports) keeps its arms — and its controlled technology —
// from the buyer country: at home never; to an arms partner (a treaty in
// force whose type makes one) under «partners»; to anyone under «open».
func armsExportDenied(ctx context.Context, tx application.Tx, policy application.PolicyReader, snap *content.Snapshot,
	buyer, seller string, now time.Time,
) (bool, error) {
	if buyer == "" || seller == "" || buyer == seller {
		return false, nil
	}
	v, err := policy.Get(ctx, seller, LeverArmsExports)
	if err != nil {
		return false, err
	}
	treaties, err := tx.Diplomacy().TreatiesBetween(ctx, buyer, seller)
	if err != nil {
		return false, err
	}
	rules := make([]diplomacy.Treaty, len(treaties))
	for i, t := range treaties {
		rules[i] = t.Rule()
	}
	partner := diplomacy.Partners(rules, snap.TreatyRules(), buyer, seller, now,
		func(t diplomacy.TreatyType) bool { return t.ArmsPartner })
	return military.CheckExport(military.ExportPolicy(v.Value), false, partner) != nil, nil
}

// procureOffer reads a listing as a state's buyer sees it.
func (h *MilitaryHandler) procureOffer(ctx context.Context, tx application.Tx, snap *content.Snapshot, buyer string,
	l application.Listing,
) (screens.ProcureOffer, *application.Design, string, error) {
	seller, err := tx.Companies().ByID(ctx, l.CompanyID)
	if err != nil {
		return screens.ProcureOffer{}, nil, "", err
	}
	city, err := h.cities.ByID(ctx, l.CityID)
	if err != nil {
		return screens.ProcureOffer{}, nil, "", err
	}
	sellerCountry, err := tx.Diplomacy().CountryOfCity(ctx, l.CityID)
	if err != nil {
		return screens.ProcureOffer{}, nil, "", err
	}
	place, err := placeOf(ctx, tx, sellerCountry)
	if err != nil {
		return screens.ProcureOffer{}, nil, "", err
	}
	good, d, err := groupGood(ctx, tx, snap, l.Item, l.DesignID, map[string]*application.Design{})
	if err != nil {
		return screens.ProcureOffer{}, nil, "", err
	}
	o := screens.ProcureOffer{No: l.No, Good: good, Company: companyRef(snap, *seller), CityCode: city.Code, City: city.Name,
		Country: place, Left: l.Left(), Price: l.UnitPrice}
	if o.Blocked, err = h.exportBlock(ctx, tx, snap, buyer, sellerCountry); err != nil {
		return o, nil, "", err
	}
	return o, d, sellerCountry, nil
}

// procurer authorises the player to buy for the country, or refuses.
func (h *MilitaryHandler) procurer(ctx context.Context, tx application.Tx, snap *content.Snapshot, country *application.Jurisdiction,
	p *application.Player,
) (application.Office, error) {
	seat, ok, err := mayAct(ctx, tx, snap, country.ID, content.ActionProcure, p)
	if err != nil {
		return seat, err
	}
	if !ok {
		r := refuseMilitary(screens.MilitaryRefusedNotHolder, country)
		r.view.Office = actionOffice(snap, content.ActionProcure)
		return seat, r
	}
	return seat, nil
}

// Procure handles military.procure: the military goods for sale that the
// country's state may buy, and why not the others.
func (h *MilitaryHandler) Procure(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	return h.procureWith(ctx, meta, req, "")
}

func (h *MilitaryHandler) procureWith(ctx context.Context, meta envelope.Metadata, req MilitaryRequest, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ProcureView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.country(ctx, tx, p, req.Country)
		if err != nil {
			return err
		}
		if _, err := h.procurer(ctx, tx, snap, country, p); err != nil {
			return err
		}
		fund, err := tx.Ledger().AccountFor(ctx, application.AccountDefenceFund, country.ID)
		if err != nil {
			return err
		}
		view = screens.ProcureView{Country: countryPlace(*country), Fund: fund.Balance.Minor(), Notice: notice}
		listings, err := tx.Military().ArmsListings(ctx, armsItems(snap))
		if err != nil {
			return err
		}
		for _, l := range listings {
			o, _, _, err := h.procureOffer(ctx, tx, snap, country.ID, l)
			if err != nil {
				return err
			}
			view.Offers = append(view.Offers, o)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Procure(h.screen(meta, lang), view), nil
}

// ArmsBuy handles military.buy: one listing — how many, then confirm, then
// the purchase, once (idempotent on the confirming update).
func (h *MilitaryHandler) ArmsBuy(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.ArmsBuyView
		bought  string
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm := req.confirmed()
		if confirm {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			confirm = fresh
		}
		if country, err = h.country(ctx, tx, p, req.Country); err != nil {
			return err
		}
		seat, err := h.procurer(ctx, tx, snap, country, p)
		if err != nil {
			return err
		}
		back := []string{screens.AddrProcure, country.Code}
		no, ok := number(req.No)
		if !ok {
			return refuseMilitary(screens.MilitaryRefusedNotFound, country).back(back...)
		}
		l, err := tx.Production().Listing(ctx, no, false)
		if isSentinel(err, application.ErrListingNotFound) || (err == nil && l.Status != application.ListingOpen) {
			return refuseMilitary(screens.MilitaryRefusedNotFound, country).back(back...)
		}
		if err != nil {
			return err
		}
		if _, ok := snap.ClassOfItem(l.Item); !ok {
			return refuseMilitary(screens.MilitaryRefusedNotArms, country).back(back...)
		}
		offer, d, sellerCountry, err := h.procureOffer(ctx, tx, snap, country.ID, *l)
		if err != nil {
			return err
		}
		fund, err := tx.Ledger().AccountFor(ctx, application.AccountDefenceFund, country.ID)
		if err != nil {
			return err
		}
		view = screens.ArmsBuyView{Country: countryPlace(*country), Offer: offer, Fund: fund.Balance.Minor()}
		if d != nil {
			if a, ok := snap.Archetype(d.Archetype); ok {
				if attrs, err := item.ObservableAttributes(a, domainDesign(*d), snap.Components()); err == nil {
					for _, at := range a.Attributes {
						if v, ok := attrs[at.Name]; ok && at.Name != "quality" {
							view.Attributes = append(view.Attributes, screens.AttributeLine{Name: at.Name, Value: v, Observable: true})
						}
					}
				}
			}
		}
		if err := h.refuseBlocked(ctx, tx, offer, country, sellerCountry, back); err != nil {
			return err
		}
		qty, ok := quantityArg(req.Qty)
		if !ok {
			return nil
		}
		if qty > l.Left() {
			r := refuseMilitary(screens.MilitaryRefusedStock, country).back(screens.AddrArmsBuy, country.Code, qtyText(no))
			r.view.Max = l.Left()
			return r
		}
		view.Qty, view.Confirm, view.Total = qty, true, qty*l.UnitPrice
		if !confirm {
			return nil
		}
		name, err := h.purchase(ctx, tx, snap, meta, p, seat, country, sellerCountry, no, qty)
		bought = name
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if bought != "" {
		c := h.screen(meta, lang)
		notice := c.T("military.buy.done", map[string]any{"count": screens.FormatNumber(c, view.Qty),
			"good": c.GoodName(view.Offer.Good), "total": screens.FormatMoney(c, view.Total)})
		return h.procureWith(ctx, meta, MilitaryRequest{Country: country.Code}, notice)
	}
	return screens.ArmsBuy(h.screen(meta, lang), view), nil
}

// refuseBlocked refuses a purchase the seller's export policy or a
// sanction forbids, with the sanction's own screen for an embargo.
func (h *MilitaryHandler) refuseBlocked(ctx context.Context, tx application.Tx, offer screens.ProcureOffer,
	country *application.Jurisdiction, sellerCountry string, back []string,
) error {
	switch offer.Blocked {
	case screens.ProcureBlockedEmbargo:
		return checkSanctions(ctx, tx, application.CheckSanctions(ctx, tx, diplomacy.Arms, country.ID, sellerCountry, h.now()), back...)
	case screens.ProcureBlockedExport:
		r := refuseMilitary(screens.MilitaryRefusedExport, country).back(back...)
		r.view.Country = offer.Country
		return r
	}
	return nil
}

// purchase makes one purchase, everything under the locks: the seller
// company, its goods and the state's, then the listing. It returns the
// seller's name.
func (h *MilitaryHandler) purchase(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	p *application.Player, seat application.Office, country *application.Jurisdiction, sellerCountry string, no, qty int64,
) (string, error) {
	back := []string{screens.AddrProcure, country.Code}
	l, err := tx.Production().Listing(ctx, no, false)
	if err != nil {
		return "", err
	}
	seller, err := tx.Companies().Lock(ctx, l.CompanyID)
	if err != nil {
		return "", err
	}
	sellerOrg, stateOrg := application.CompanyOrg(seller.ID), application.StateOrg(country.ID)
	for _, org := range []application.Org{sellerOrg, stateOrg} {
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return "", err
		}
	}
	if l, err = tx.Production().Listing(ctx, no, true); err != nil {
		return "", err
	}
	if l.Status != application.ListingOpen || !seller.Active() {
		return "", refuseMilitary(screens.MilitaryRefusedNotFound, country).back(back...)
	}
	if qty < 1 || qty > l.Left() {
		r := refuseMilitary(screens.MilitaryRefusedStock, country).back(back...)
		r.view.Max = l.Left()
		return "", r
	}
	class, ok := snap.ClassOfItem(l.Item)
	if !ok {
		return "", refuseMilitary(screens.MilitaryRefusedNotArms, country).back(back...)
	}
	// Export control: the one place a sale of goods is cleared, the state
	// as the buyer.
	def, _ := snap.ItemDef(l.Item)
	if technology.Cleared(def.ExportControl.Control(), technology.Buyer{Kind: content.StateBuyerClass}) != nil {
		return "", refuseMilitary(screens.MilitaryRefusedNotArms, country).back(back...)
	}
	if block, err := h.exportBlock(ctx, tx, snap, country.ID, sellerCountry); err != nil {
		return "", err
	} else if block != "" {
		place, err := placeOf(ctx, tx, sellerCountry)
		if err != nil {
			return "", err
		}
		return "", h.refuseBlocked(ctx, tx, screens.ProcureOffer{Blocked: block, Country: place}, country, sellerCountry, back)
	}
	now := h.now()
	total := money.FromMinor(qty * l.UnitPrice)
	fund, err := tx.Ledger().AccountFor(ctx, application.AccountDefenceFund, country.ID)
	if err != nil {
		return "", err
	}
	if fund.Balance.Minor() < total.Minor() {
		r := refuseMilitary(screens.MilitaryRefusedFunds, country).back(back...)
		r.view.Need, r.view.Have = total.Minor(), fund.Balance.Minor()
		return "", r
	}
	sellerAcct, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, seller.ID)
	if err != nil {
		return "", err
	}
	txID, err := postRef(ctx, tx.Ledger(), application.ReasonArmsProcurement, listingReference, l.ID, fund.ID, sellerAcct.ID,
		total, now)
	if err != nil {
		if isSentinel(err, application.ErrInsufficientFunds) {
			r := refuseMilitary(screens.MilitaryRefusedFunds, country).back(back...)
			r.view.Need, r.view.Have = total.Minor(), fund.Balance.Minor()
			return "", r
		}
		return "", err
	}
	proc, err := tx.Military().RecordProcurement(ctx, application.Procurement{ID: h.ids.NewID(), CountryID: country.ID,
		ListingID: l.ID, CompanyID: seller.ID, Item: l.Item, DesignID: l.DesignID, Qty: qty, UnitPrice: l.UnitPrice,
		Total: total.Minor(), LedgerTransactionID: txID, BoughtBy: p.ID, OfficeCode: seat.OfficeCode, At: now})
	if err != nil {
		return "", err
	}
	_, pieces, err := tx.Items().OrgHoldings(ctx, sellerOrg, application.HoldListed)
	if err != nil {
		return "", err
	}
	var listed []application.Piece
	for _, pc := range pieces {
		if pc.Item == l.Item && pc.DesignID == l.DesignID {
			listed = append(listed, pc)
		}
	}
	sort.SliceStable(listed, func(i, j int) bool { return listed[i].Quality > listed[j].Quality })
	if int64(len(listed)) < qty {
		return "", errors.Internal(stderrors.New("handlers: a listing holds fewer pieces than it has left"))
	}
	for _, pc := range listed[:qty] {
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: l.Item, PieceID: pc.ID, Qty: 1,
			FromOrg: sellerOrg, FromHolding: application.HoldListed, ToOrg: stateOrg, ToHolding: application.HoldWarehouse,
			Reason: application.ItemProcured, ReferenceType: procurementReference, ReferenceID: proc.ID, At: now}); err != nil {
			return "", err
		}
		if err := tx.Military().AddAsset(ctx, application.MilitaryAsset{PieceID: pc.ID, CountryID: country.ID,
			Branch: class.Branch, ClassCode: class.Code, ProcurementID: proc.ID, AcquiredAt: now}); err != nil {
			return "", err
		}
	}
	l.Sold += qty
	l.UpdatedAt = now
	if l.Left() == 0 {
		l.Status, l.ClosedAt = application.ListingSold, &now
	}
	if err := tx.Production().SaveListing(ctx, *l); err != nil {
		return "", err
	}
	// The seller's owner hears of the sale, as of any; the countries'
	// groups read that the state armed, in a band.
	payload := map[string]any{"company_id": seller.ID, "code": seller.Code, "name": seller.Name, "type": seller.TypeCode,
		"owner_id": seller.OwnerID, "item": l.Item, "qty": qty, "price": total.Minor(), "buyer": country.Name,
		"component": false}
	if l.DesignID != "" {
		d, err := tx.Production().DesignByID(ctx, l.DesignID)
		if err != nil {
			return "", err
		}
		payload["design"] = d.Name
	}
	if err := appendCompanyEvent(ctx, tx, meta, "sold", seller.ID, payload); err != nil {
		return "", err
	}
	cities, err := cityIDsOf(ctx, tx, country.ID)
	if err != nil {
		return "", err
	}
	if err := appendDomainEvent(ctx, tx, meta, "military", "procured", proc.ID, map[string]any{
		"country_code": country.Code, "country_name": country.Name, "class": class.Code, "class_name": class.Name,
		"band": military.BandOf(snap.StrengthBands(), qty), "city_ids": cities,
	}); err != nil {
		return "", err
	}
	return seller.Name, nil
}

package handlers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Product generations, the military stage's own two steps
// (docs/adr/0022-military-and-diplomacy.md, generations addendum): the
// defence minister (country.procure, the same office and action arms
// procurement already uses) orders upgrade kits from a licensed contractor
// exactly like arms, minus becoming a stationed asset; a branch's commander
// (the branch's own Command action, the same one Station uses) applies one
// to a state asset. Both reuse planRetrofit/item.Retrofit unchanged from the
// production economy's own retrofit — a state's equipment is item_pieces
// like a company's goods are, and the mechanism does not know the
// difference.

// purchaseKit is purchase() (military_procure.go) without becoming a
// stationed asset: a kit is delivered to the state's warehouse to be
// consumed by a retrofit, never itself an asset with a branch and a
// garrison.
func (h *MilitaryHandler) purchaseKit(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	p *application.Player, seat application.Office, country *application.Jurisdiction, no, qty int64,
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
	// A kit listing, not a combat-ready unit: its pieces must come from an
	// upgrade_kit order, never a plain design order — the same good and
	// design code can be either, so the order alone tells them apart.
	if kitListed, err := kitOriginListing(ctx, tx, l); err != nil {
		return "", err
	} else if !kitListed {
		return "", refuseMilitary(screens.MilitaryRefusedNotArms, country).back(back...)
	}
	def, _ := snap.ItemDef(l.Item)
	if err := checkExportForState(def); err != nil {
		return "", refuseMilitary(screens.MilitaryRefusedNotArms, country).back(back...)
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
	moved := int64(0)
	for _, pc := range pieces {
		if moved >= qty {
			break
		}
		if pc.Item != l.Item || pc.DesignID != l.DesignID {
			continue
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: l.Item, PieceID: pc.ID, Qty: 1,
			FromOrg: sellerOrg, FromHolding: application.HoldListed, ToOrg: stateOrg, ToHolding: application.HoldWarehouse,
			Reason: application.ItemProcured, ReferenceType: procurementReference, ReferenceID: proc.ID, At: now}); err != nil {
			return "", err
		}
		moved++
	}
	if moved < qty {
		return "", internalf("a kit listing held fewer pieces than it had left")
	}
	return seller.Name, nil
}

// kitOriginListing reports whether a listing's good is an upgrade kit: its
// pieces were made by an OrderKindUpgradeKit order, not a plain design order.
func kitOriginListing(ctx context.Context, tx application.Tx, l *application.Listing) (bool, error) {
	_, pieces, err := tx.Items().OrgHoldings(ctx, application.CompanyOrg(l.CompanyID), application.HoldListed)
	if err != nil {
		return false, err
	}
	for _, pc := range pieces {
		if pc.Item != l.Item || pc.DesignID != l.DesignID || pc.Origin != application.OriginProduction {
			continue
		}
		order, err := tx.Production().Order(ctx, pc.OriginRef)
		if isSentinel(err, application.ErrProductionOrderNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		return order.Kind == application.OrderKindUpgradeKit, nil
	}
	return false, nil
}

// errKitNotCleared means a kit's good is export-controlled and the state is
// not a class its control clears.
var errKitNotCleared = item.ErrTechnologyLocked

// checkExportForState is the one export-control gate a sale of goods to the
// state passes through (technology.Cleared with the state buyer class),
// mirroring military_procure.go's own check for arms.
func checkExportForState(def content.ItemDef) error {
	if !def.ExportControl.Control().Restricted {
		return nil
	}
	for _, c := range def.ExportControl.Control().BuyerClasses {
		if c == content.StateBuyerClass {
			return nil
		}
	}
	return errKitNotCleared
}

// ProcureKit handles military.kitbuy: the defence minister buys upgrade
// kits from a licensed contractor's listing, delivered to the state's
// warehouse — everything arms procurement already does (office, funds,
// export control, payment), minus becoming a stationed asset.
func (h *MilitaryHandler) ProcureKit(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		seller string
		bought bool
	)
	no, ok := number(req.No)
	qty, qok := number(req.Qty)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm := req.confirmed() && ok && qok
		if confirm {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			confirm = fresh
		}
		country, err := h.country(ctx, tx, p, req.Country)
		if err != nil {
			return err
		}
		seat, err := h.procurer(ctx, tx, snap, country, p)
		if err != nil {
			return err
		}
		if !ok || !qok {
			return refuseMilitary(screens.MilitaryRefusedNotFound, country).back(screens.AddrProcure, country.Code)
		}
		if !confirm {
			return nil
		}
		if seller, err = h.purchaseKit(ctx, tx, snap, meta, p, seat, country, no, qty); err != nil {
			return err
		}
		bought = true
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.KitPurchase(h.screen(meta, lang), screens.KitPurchaseView{Bought: bought, Seller: seller, Country: req.Country}), nil
}

// RetrofitState handles military.retrofit: a branch's commander applies an
// upgrade kit the state holds to one of its own assets. args reused for
// lack of dedicated fields: req.Target is the kit's serial, req.City the
// target unit's serial (docs/adr/0021 generations addendum).
func (h *MilitaryHandler) RetrofitState(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.RetrofitView
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		kitSerial, targetSerial := strings.TrimSpace(req.Target), strings.TrimSpace(req.City)
		place := req.confirmed() && kitSerial != "" && targetSerial != ""
		if place {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				place = false
			}
		}
		if country, err = h.country(ctx, tx, p, req.Country); err != nil {
			return err
		}
		org := application.StateOrg(country.ID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		if kitSerial == "" || targetSerial == "" {
			return refuseMilitary(screens.MilitaryRefusedNotFound, country)
		}
		// The commander of the TARGET's own branch, not the kit's: a kit
		// bought for one branch is still a good in the state's shared
		// warehouse, but only the branch that owns the unit may retrofit it.
		assets, err := tx.Military().Assets(ctx, country.ID)
		if err != nil {
			return err
		}
		var branch string
		for _, a := range assets {
			if a.Serial == targetSerial {
				branch = a.Branch
				break
			}
		}
		if branch == "" {
			return refuseMilitary(screens.MilitaryRefusedNotFound, country)
		}
		b, ok := snap.Branch(branch)
		if !ok {
			return internalf("an asset of a branch the content does not have: " + branch)
		}
		if _, ok, err := mayAct(ctx, tx, snap, country.ID, b.Command, p); err != nil {
			return err
		} else if !ok {
			r := refuseMilitary(screens.MilitaryRefusedNotHolder, country)
			r.view.Office = actionOffice(snap, b.Command)
			return r
		}
		plan, err := planRetrofit(ctx, tx, snap, org, kitSerial, targetSerial)
		if err != nil {
			return err
		}
		view = screens.RetrofitView{Good: plan.good, FromVer: max(plan.current.Version, 1), ToVer: plan.toDesign.Version,
			Duration: h.scale.RealWait(h.rules.RetrofitTime)}
		if !place {
			return nil
		}
		now := h.now()
		id := h.ids.NewID()
		finish := now.Add(h.scale.RealWait(h.rules.RetrofitTime))
		payload, err := json.Marshal(ProductionActionPayload{ID: id})
		if err != nil {
			return err
		}
		actionID := h.ids.NewID()
		if err := tx.GameActions().Schedule(ctx, application.GameAction{ID: actionID, ActionType: application.RetrofitActionType,
			ActorType: "system", ReferenceType: retrofitReference, ReferenceID: id, Payload: payload, StartedAt: now,
			FinishAt: finish}); err != nil {
			return err
		}
		if err := tx.Production().StartRetrofit(ctx, application.RetrofitJob{ID: id, OrgKind: org.Kind, OrgID: org.ID,
			PieceID: plan.target.ID, KitPieceID: plan.kit.ID, FromDesignID: plan.current.ID, ToDesignID: plan.toDesign.ID,
			GameActionID: actionID, StartedBy: p.ID, StartedAt: now, FinishAt: finish}); err != nil {
			if isSentinel(err, application.ErrRetrofitBusy) {
				return refuseMilitary(screens.MilitaryRefusedStock, country)
			}
			return err
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: plan.kit.Item, PieceID: plan.kit.ID,
			Qty: 1, FromOrg: org, FromHolding: application.HoldWarehouse, Reason: application.ItemRetrofitKit,
			ReferenceType: retrofitReference, ReferenceID: id, At: now}); err != nil {
			return err
		}
		view.Started, view.FinishAt = true, finish
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Retrofit(h.screen(meta, lang), view), nil
}

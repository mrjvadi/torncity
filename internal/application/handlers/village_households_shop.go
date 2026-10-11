package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/household"
	"github.com/mrjvadi/torncity/internal/domain/shop"
	"github.com/mrjvadi/torncity/internal/domain/vshop"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// A household at the village shop (ADR 0073 4.5): the food the stock could not give is bought from the shelf's food lines. The
// shelf keeps PlayerReserveBPS of the day's delivery that households cannot touch, so a household never takes what a player came
// for; the price and the head's ceiling are the players', the price goes to the sink as theirs does (the goods come from outside),
// and the village's sales tax goes to the treasury. The sale is a row of village_shop_sales with a household for a buyer.

// householdShopState reads the shelf once for the batch.
func (h *VillageHandler) householdShopState(ctx context.Context, tx application.Tx, d *houseDay) error {
	if d.shop != nil || d.shopOff {
		return nil
	}
	if !h.shop.enabled() {
		d.shopOff = true
		return nil
	}
	day, err := h.SettleShopDay(ctx, tx, d.s, d.now)
	if err != nil {
		return err
	}
	if day == nil || shopClosed(day) != village.ShopOpen {
		d.shopOff = true
		return nil
	}
	st, err := h.shopStateOf(ctx, tx, d.snap, d.s)
	if err != nil {
		return errors.Internal(err)
	}
	capBPS, taxBPS, err := h.shopTerms(ctx, tx, d.s)
	if err != nil {
		return err
	}
	d.shop, d.shopDay, d.shopCap, d.shopTax = &st, day.Day, capBPS, taxBPS
	return nil
}

// householdShop buys up to needPts food points for a household from the shelf with at most balance money. It returns the points it
// got and the money it paid (price and tax).
func (h *VillageHandler) householdShop(ctx context.Context, tx application.Tx, d *houseDay, hh application.Household, wallet application.Account,
	needPts, balance int64,
) (pts, paid int64, err error) {
	if needPts <= 0 || balance <= 0 {
		return 0, 0, nil
	}
	if err := h.householdShopState(ctx, tx, d); err != nil || d.shop == nil {
		return 0, 0, err
	}
	st := d.shop
	type cand struct {
		i      int
		points int64
	}
	var cands []cand
	for i, ol := range st.open {
		if ol.line.Class != vshop.ClassFood {
			continue
		}
		if it, ok := d.snap.ItemStorage(ol.line.Code); ok && it.FoodPoints > 0 {
			cands = append(cands, cand{i: i, points: int64(it.FoodPoints)})
		}
	}
	// the cheapest point first
	for a := 1; a < len(cands); a++ {
		for b := a; b > 0; b-- {
			x, y := st.open[cands[b].i].line, st.open[cands[b-1].i].line
			if x.Ref*cands[b-1].points < y.Ref*cands[b].points {
				cands[b], cands[b-1] = cands[b-1], cands[b]
			}
		}
	}
	for _, c := range cands {
		if pts >= needPts {
			break
		}
		ol := st.open[c.i]
		row, err := tx.VillageShop().Line(ctx, d.s.CityID, ol.line.Code)
		if err != nil {
			return pts, paid, err
		}
		reserve := (st.plan[ol.line.Code].Units*h.house.PlayerReserveBPS + household.BPS - 1) / household.BPS
		avail := row.Stock - reserve
		if avail <= 0 {
			continue
		}
		unit := st.rules.Price(ol.line, row.SoldToday, d.shopCap)
		perUnit := unit + unit*d.shopTax/household.BPS
		qty := min((needPts-pts+c.points-1)/c.points, avail, (balance-paid)/max(perUnit, 1))
		if qty <= 0 {
			continue
		}
		total, err := shop.Total(money.FromMinor(unit), qty)
		if err != nil {
			return pts, paid, errors.Internal(err)
		}
		tax, err := shop.SalesTax(total, int(d.shopTax))
		if err != nil {
			return pts, paid, errors.Internal(err)
		}
		if total.Minor()+tax.Minor() > balance-paid {
			continue
		}
		saleID, saleTx := h.ids.NewID(), h.ids.NewID()
		row.Stock -= qty
		row.SoldToday += qty
		row.SoldTotal += qty
		if err := tx.VillageShop().SaveLine(ctx, *row); err != nil {
			return pts, paid, err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: saleTx, Reason: application.ReasonShopPurchase, CreatedAt: d.now,
			ReferenceType: application.VillageShopSaleReference, ReferenceID: saleID, Entries: []application.LedgerEntry{
				{AccountID: wallet.ID, Amount: money.FromMinor(-total.Minor())},
				{AccountID: application.SystemSinkAccountID, Amount: total},
			}}); err != nil {
			return pts, paid, err
		}
		if !tax.IsZero() {
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonSalesTax, CreatedAt: d.now,
				ReferenceType: application.VillageShopSaleReference, ReferenceID: saleID, Entries: []application.LedgerEntry{
					{AccountID: wallet.ID, Amount: money.FromMinor(-tax.Minor())},
					{AccountID: d.treasury.ID, Amount: tax},
				}}); err != nil {
				return pts, paid, err
			}
		}
		if err := tx.VillageShop().RecordSale(ctx, application.VillageShopSale{ID: saleID, SettlementID: d.s.CityID, HouseholdID: hh.ID, Line: ol.line.Code,
			Day: d.shopDay, Quantity: qty, UnitPrice: unit, ReferencePrice: ol.line.Ref, Total: total.Minor(), Tax: tax.Minor(), Method: "household",
			LedgerTransactionID: saleTx, At: d.now}); err != nil {
			return pts, paid, err
		}
		pts += qty * c.points
		paid += total.Minor() + tax.Minor()
	}
	return pts, paid, nil
}

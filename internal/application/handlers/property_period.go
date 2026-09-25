package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/property"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// A city period's property (docs/adr/0024-property-and-politics.md): every
// owned property of the city pays its upkeep and its tax, tax first, from
// its owner's bank and then their cash, as far as they go; the rest is its
// debt, and a property in debt foreclosure_periods periods in a row goes
// back to the city — its debt written off, its lease and offer ended. Then
// every lease still running pays the period's rent, all or nothing, to its
// landlord's bank; a tenant owing eviction_periods periods in a row is
// evicted. It runs inside the city's settlement, under its clock lock, so
// once per period; the primary keys of the period's records are the
// backstop.

// settleCity charges a city's property for one period.
func (h *PropertyHandler) settleCity(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	city *application.City, periodNo int64, now time.Time,
) error {
	props, err := tx.Property().InCity(ctx, city.ID)
	if err != nil {
		return err
	}
	var rate int64
	if len(props) > 0 {
		if rate, err = h.taxRate(ctx, city); err != nil {
			return err
		}
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
	if err != nil {
		return err
	}
	for i := range props {
		if err := h.charge(ctx, tx, snap, meta, city, treasury.ID, &props[i], rate, periodNo, now); err != nil {
			return err
		}
	}
	leases, err := tx.Property().LeasesInCity(ctx, city.ID)
	if err != nil {
		return err
	}
	for i := range leases {
		if err := h.collectRent(ctx, tx, snap, meta, city, &leases[i], periodNo, now); err != nil {
			return err
		}
	}
	return nil
}

// charge takes one property's upkeep and tax for a period.
func (h *PropertyHandler) charge(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	city *application.City, treasuryID string, pr *application.Property, rate, periodNo int64, now time.Time,
) error {
	t, ok := snap.PropertyType(pr.TypeCode)
	upkeep := int64(0)
	if ok {
		upkeep = t.Upkeep
	}
	owed := property.Owed{Upkeep: upkeep, Tax: property.Tax(pr.Value, rate), UpkeepDebt: pr.UpkeepDebt, TaxDebt: pr.TaxDebt}
	wallet, err := application.OpenWallet(ctx, tx.Ledger(), pr.OwnerID)
	if err != nil {
		return err
	}
	paid := property.Charge(owed, wallet.Bank.Balance.Minor()+wallet.Cash.Balance.Minor())
	if err := drawFrom(ctx, tx, &wallet, paid.TaxPaid, application.ReasonPropertyTax, application.PropertyReference,
		pr.ID, treasuryID, now); err != nil {
		return err
	}
	if err := drawFrom(ctx, tx, &wallet, paid.UpkeepPaid, application.ReasonPropertyUpkeep, application.PropertyReference,
		pr.ID, application.SystemSinkAccountID, now); err != nil {
		return err
	}
	owner := pr.OwnerID
	pr.TaxDebt, pr.UpkeepDebt = paid.TaxDebt, paid.UpkeepDebt
	if paid.InDebt() {
		pr.UnpaidPeriods++
	} else {
		pr.UnpaidPeriods = 0
	}
	foreclosed := property.Foreclosed(pr.UnpaidPeriods, h.rules.ForeclosurePeriods)
	if err := tx.Property().RecordCharge(ctx, application.PropertyCharge{PropertyID: pr.ID, PeriodNo: periodNo,
		CityID: city.ID, OwnerID: owner, Upkeep: owed.Upkeep, Tax: owed.Tax, UpkeepPaid: paid.UpkeepPaid,
		TaxPaid: paid.TaxPaid, UpkeepDebt: paid.UpkeepDebt, TaxDebt: paid.TaxDebt, Foreclosed: foreclosed,
		ChargedAt: now}); err != nil {
		return err
	}
	if foreclosed {
		if err := h.repossess(ctx, tx, pr, now); err != nil {
			return err
		}
		if err := appendDomainEvent(ctx, tx, meta, "property", "foreclosed", pr.ID, map[string]any{
			"kind": "foreclosed", "player_id": owner, "property_no": pr.No, "type": pr.TypeCode, "type_name": t.Name,
			"city_code": city.Code, "city_name": city.Name, "amount": owed.TaxDebt + owed.UpkeepDebt + owed.Tax + owed.Upkeep,
		}); err != nil {
			return err
		}
	}
	return tx.Property().Save(ctx, *pr)
}

// repossess takes a property back into the city's stock: no owner, its
// debt written off, its lease ended and its offer withdrawn.
func (h *PropertyHandler) repossess(ctx context.Context, tx application.Tx, pr *application.Property, now time.Time) error {
	lease, err := tx.Property().LeaseOf(ctx, pr.ID)
	if err != nil {
		return err
	}
	if lease != nil {
		lease.Status, lease.EndedAt, lease.EndReason = application.LeaseEnded, &now, application.LeaseRepossessed
		if err := tx.Property().SaveLease(ctx, *lease); err != nil {
			return err
		}
	}
	offer, err := tx.Property().ListingOf(ctx, pr.ID)
	if err != nil {
		return err
	}
	if offer != nil {
		if _, err := tx.Property().CloseListing(ctx, offer.ID, application.OfferCancelled, "", now); err != nil {
			return err
		}
	}
	pr.OwnerID, pr.Status, pr.RepossessedAt = "", application.PropertyRepossessed, &now
	pr.TaxDebt, pr.UpkeepDebt, pr.UnpaidPeriods = 0, 0, 0
	return nil
}

// collectRent takes one lease's rent for a period, once: all of it from the
// tenant's bank and then their cash, or none; after eviction_periods
// unpaid periods in a row the lease ends.
func (h *PropertyHandler) collectRent(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	city *application.City, lease *application.PropertyLease, periodNo int64, now time.Time,
) error {
	done, err := tx.Property().RentRecorded(ctx, lease.ID, periodNo)
	if err != nil || done {
		return err
	}
	wallet, err := application.OpenWallet(ctx, tx.Ledger(), lease.TenantID)
	if err != nil {
		return err
	}
	paid := property.Rent(lease.Rent, wallet.Bank.Balance.Minor()+wallet.Cash.Balance.Minor())
	if paid > 0 {
		landlord, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, lease.LandlordID)
		if err != nil {
			return err
		}
		if err := drawFrom(ctx, tx, &wallet, paid, application.ReasonRent, application.PropertyLeaseReference, lease.ID,
			landlord.ID, now); err != nil {
			return err
		}
		lease.Arrears = 0
	} else {
		lease.Arrears++
	}
	evicted := property.Evicted(lease.Arrears, h.rules.EvictionPeriods)
	if err := tx.Property().RecordRent(ctx, application.RentPayment{LeaseID: lease.ID, PeriodNo: periodNo, Rent: lease.Rent,
		Paid: paid, Evicted: evicted, At: now}); err != nil {
		return err
	}
	if evicted {
		lease.Status, lease.EndedAt, lease.EndReason = application.LeaseEnded, &now, application.LeaseEvicted
		pr, err := tx.Property().ByID(ctx, lease.PropertyID, false)
		if err != nil {
			return err
		}
		t, _ := snap.PropertyType(pr.TypeCode)
		for _, who := range []string{lease.TenantID, lease.LandlordID} {
			kind := "evicted"
			if who == lease.LandlordID {
				kind = "evicted_tenant"
			}
			if err := appendDomainEvent(ctx, tx, meta, "property", kind, lease.ID, map[string]any{
				"kind": kind, "player_id": who, "property_no": pr.No, "type": pr.TypeCode, "type_name": t.Name,
				"city_code": city.Code, "city_name": city.Name, "amount": lease.Rent}); err != nil {
				return err
			}
		}
	}
	return tx.Property().SaveLease(ctx, *lease)
}

// drawFrom takes amount from a player's bank and then their cash, into to,
// as one transaction under reason. The caller has made sure the two cover
// it; the wallet's balances follow.
func drawFrom(ctx context.Context, tx application.Tx, w *application.Wallet, amount int64, reason application.Reason,
	refType, refID, to string, now time.Time,
) error {
	if amount <= 0 {
		return nil
	}
	fromBank := min(amount, max(w.Bank.Balance.Minor(), 0))
	fromCash := amount - fromBank
	if fromCash > w.Cash.Balance.Minor() {
		return errors.Internal(application.ErrInsufficientFunds)
	}
	entries := []application.LedgerEntry{{AccountID: to, Amount: money.FromMinor(amount)}}
	if fromBank > 0 {
		entries = append(entries, application.LedgerEntry{AccountID: w.Bank.ID, Amount: money.FromMinor(-fromBank)})
	}
	if fromCash > 0 {
		entries = append(entries, application.LedgerEntry{AccountID: w.Cash.ID, Amount: money.FromMinor(-fromCash)})
	}
	if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: reason, ReferenceType: refType,
		ReferenceID: refID, Entries: entries, CreatedAt: now}); err != nil {
		return err
	}
	w.Bank.Balance, _ = w.Bank.Balance.Sub(money.FromMinor(fromBank))
	w.Cash.Balance, _ = w.Cash.Balance.Sub(money.FromMinor(fromCash))
	return nil
}

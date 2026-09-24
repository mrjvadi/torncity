package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The settlement of a city's companies.
//
// Every period (config company.period, GAME time, waited through the game
// clock) each city with an active company is settled ONCE, in one unit of
// work, from one scheduled action (game_actions 'company_period'):
//
//  1. the city's NPC population pays its companies (npc_purchase, from
//     system_source): internal/domain/company.Settle divides the city's
//     demand among them by quality — the shifts worked for each in the
//     period — and price, bounded by each one's capacity and by the city's
//     budget for the period (content, then config);
//  2. the city's sales tax (city.sales_tax) is paid on that revenue, to the
//     city's treasury;
//  3. each company pays its upkeep (maintenance, to system_sink) from the
//     money its running shifts have not reserved; what it cannot pay it
//     owes, and a company that ends InsolvencyPeriods settlements in a row
//     owing is dissolved — once no shift is running for it;
//  4. every company's books for the period are recorded, its owner is sent
//     the report, and the next settlement is scheduled while any company is
//     left in the city.
//
// Exactly once: the city's clock row is locked first and names the action
// that settles its period — a stale or repeated delivery finds another
// action or another period there and does nothing — and the period's record
// (company_market_periods) has the city and the period as its primary key.
// A company founded during the period takes part for the share of it it
// existed: that share of its upkeep and of its unstaffed capacity.

// CompanyPeriodPayload is the jsonb a settlement's scheduled action carries.
type CompanyPeriodPayload struct {
	CityID   string `json:"city_id"`
	PeriodNo int64  `json:"period_no"`
}

// startClock makes sure a city's settlement clock is running: a city whose
// clock is idle (no company since its last settlement, or never one) starts
// a period now and schedules its end.
func (h *CompaniesHandler) startClock(ctx context.Context, tx application.Tx, cityID string, now time.Time) error {
	clock, err := tx.Companies().MarketClock(ctx, cityID, now)
	if err != nil {
		return err
	}
	if clock.ActionID != "" {
		return nil
	}
	clock.PeriodStartedAt = now
	return h.schedule(ctx, tx, clock, now)
}

// schedule puts the end of clock's period on the schedule and saves it.
func (h *CompaniesHandler) schedule(ctx context.Context, tx application.Tx, clock *application.CompanyMarketClock, now time.Time) error {
	next := clock.PeriodStartedAt.Add(h.periodWait())
	if !next.After(now) {
		next = now.Add(h.periodWait())
	}
	payload, err := json.Marshal(CompanyPeriodPayload{CityID: clock.CityID, PeriodNo: clock.PeriodNo})
	if err != nil {
		return err
	}
	actionID := h.ids.NewID()
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: actionID, ActionType: application.CompanyPeriodActionType, ActorType: "system",
		ReferenceType: application.CompanyMarketReference, ReferenceID: clock.CityID, Payload: payload,
		StartedAt: now, FinishAt: next,
	}); err != nil {
		return err
	}
	clock.NextAt, clock.ActionID, clock.UpdatedAt = &next, actionID, now
	return tx.Companies().SaveMarketClock(ctx, *clock)
}

// Settle handles company.settle from the SCHEDULER: one period of one
// city's companies. See the file comment.
func (h *CompaniesHandler) Settle(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in CompanyPeriodPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("settlement payload is unreadable").WithCause(err)
		}
	}
	if in.CityID == "" {
		in.CityID = req.ReferenceID
	}
	if in.CityID == "" || in.PeriodNo < 1 {
		return nil, errors.InvalidInput("settlement names no city or no period")
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.Companies().MarketClock(ctx, in.CityID, now)
		if err != nil {
			return err
		}
		if clock.PeriodNo != in.PeriodNo || (req.ActionID != "" && clock.ActionID != req.ActionID) || clock.ActionID == "" {
			// Settled already, or an action this clock no longer runs.
			return nil
		}
		if clock.NextAt != nil && now.Before(*clock.NextAt) {
			// Early by a clock step: a fault the broker's backoff retries.
			return errors.Internal(stderrors.New("handlers: a settlement ran before its period ended"))
		}
		city, err := h.cities.ByID(ctx, in.CityID)
		if err != nil {
			return err
		}
		return h.settle(ctx, tx, meta, snap, city, clock, now)
	})
}

// settle runs one period of one city, the clock locked.
func (h *CompaniesHandler) settle(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	city *application.City, clock *application.CompanyMarketClock, now time.Time,
) error {
	start, end := clock.PeriodStartedAt, now
	length := end.Sub(start)
	if length <= 0 {
		length = time.Nanosecond
	}
	companies, err := tx.Companies().LockActiveInCity(ctx, city.ID)
	if err != nil {
		return err
	}
	market := company.Market{}
	marketDef, m, ok := snap.CompanyMarket(city.Code)
	if ok {
		market = m
	}
	market.Cap = money.FromMinor(h.rules.NPCCityPeriodCap)

	type member struct {
		c     *application.Company
		ty    company.Type
		shift int
		wages int64
		share int
	}
	var (
		members []member
		sellers []company.Seller
		skipped int
	)
	for i := range companies {
		c := &companies[i]
		_, ty, ok := snap.CompanyType(c.TypeCode)
		if !ok {
			// A kind the content dropped sells nothing and costs nothing
			// until the content is fixed; it keeps the clock running.
			skipped++
			continue
		}
		shifts, wages, err := tx.Companies().Activity(ctx, c.ID, start, end)
		if err != nil {
			return err
		}
		share := 10000
		if c.FoundedAt.After(start) {
			// Milliseconds, so a long period cannot overflow the product.
			share = int(int64(max(end.Sub(c.FoundedAt), 0)/time.Millisecond) * 10000 / max(int64(length/time.Millisecond), 1))
		}
		price := min(max(c.PriceBPS, ty.PriceMinBPS), ty.PriceMaxBPS)
		members = append(members, member{c: c, ty: ty, shift: shifts, wages: wages, share: share})
		sellers = append(sellers, company.Seller{ID: c.ID, Type: ty, PriceBPS: price, Shifts: shifts, PresenceBPS: share})
	}
	settlement, err := company.Settle(market, sellers)
	if err != nil {
		return errors.Internal(err)
	}
	salesTax, err := h.policy.Get(ctx, city.JurisdictionID, LeverSalesTax)
	if err != nil {
		return err
	}
	ledger := tx.Ledger()
	treasury, err := ledger.AccountFor(ctx, application.AccountCityTreasury, city.ID)
	if err != nil {
		return err
	}
	remaining := skipped
	for i, mb := range members {
		c, sale := mb.c, settlement.Sales[i]
		acct, err := ledger.AccountFor(ctx, application.AccountCompanyTreasury, c.ID)
		if err != nil {
			return err
		}
		balance := acct.Balance
		revenue := sale.Revenue
		var tax money.Amount
		if !revenue.IsZero() {
			if _, err := post(ctx, ledger, application.ReasonNPCPurchase, c.ID, application.SystemSourceAccountID,
				acct.ID, revenue, now); err != nil {
				return err
			}
			if tax, err = company.Prorate(revenue, int(salesTax.Value)); err != nil {
				return errors.Internal(err)
			}
			if !tax.IsZero() {
				if _, err := post(ctx, ledger, application.ReasonSalesTax, c.ID, acct.ID, treasury.ID, tax, now); err != nil {
					return err
				}
			}
			balance, _ = balance.Add(revenue)
			balance, _ = balance.Sub(tax)
		}
		reserved, err := tx.Companies().Reserved(ctx, c.ID)
		if err != nil {
			return err
		}
		upkeep, err := company.Prorate(mb.ty.Upkeep, mb.share)
		if err != nil {
			return errors.Internal(err)
		}
		books := company.Books{Balance: balance, Reserved: money.FromMinor(reserved), Debt: money.FromMinor(c.Debt)}
		u, err := company.ChargeUpkeep(books, upkeep, c.Arrears, h.rules.InsolvencyPeriods)
		if err != nil {
			return errors.Internal(err)
		}
		if !u.Paid.IsZero() {
			if _, err := post(ctx, ledger, application.ReasonMaintenance, c.ID, acct.ID, application.SystemSinkAccountID,
				u.Paid, now); err != nil {
				return err
			}
			balance, _ = balance.Sub(u.Paid)
		}
		c.Debt, c.Arrears, c.RatingBPS, c.UpdatedAt = u.Debt.Minor(), u.Arrears, sale.QualityBPS, now
		// A company still paying running shifts is dissolved at the next
		// settlement instead: their wages are promised.
		dissolve := u.Insolvent && reserved == 0
		if err := tx.Companies().RecordPeriod(ctx, application.CompanyPeriod{
			CompanyID: c.ID, PeriodNo: clock.PeriodNo, CityID: city.ID, StartedAt: start, EndedAt: end,
			PresenceBPS: mb.share, PriceBPS: sellers[i].PriceBPS, QualityBPS: sale.QualityBPS, Shifts: mb.shift,
			WantedUnits: sale.Wanted, CapacityUnits: sale.Capacity, SoldUnits: sale.Sold, Revenue: revenue.Minor(),
			SalesTax: tax.Minor(), Wages: mb.wages, UpkeepDue: u.Due.Minor(), UpkeepPaid: u.Paid.Minor(),
			Debt: u.Debt.Minor(), BalanceAfter: balance.Minor(), Insolvent: dissolve, SettledAt: now,
		}); err != nil {
			return err
		}
		if err := appendCompanyEvent(ctx, tx, meta, "period_settled", c.ID, map[string]any{
			"company_id": c.ID, "code": c.Code, "name": c.Name, "owner_id": c.OwnerID, "city_id": city.ID,
			"period_no": clock.PeriodNo, "revenue": revenue.Minor(), "sales_tax": tax.Minor(), "wages": mb.wages,
			"upkeep_due": u.Due.Minor(), "upkeep_paid": u.Paid.Minor(), "debt": u.Debt.Minor(),
			"arrears": u.Arrears, "grace": h.rules.InsolvencyPeriods, "dissolved": dissolve,
			"shifts": mb.shift, "quality_bps": sale.QualityBPS, "sold": sale.Sold, "wanted": sale.Wanted,
			"capacity": sale.Capacity, "balance": balance.Minor(),
		}); err != nil {
			return err
		}
		if dissolve {
			acct.Balance = balance
			closing, err := company.Close(company.Books{Balance: balance, Debt: u.Debt}, 0)
			if err != nil {
				return errors.Internal(err)
			}
			// What little is left pays the debt; nothing reaches the owner
			// of a company dissolved for its debt.
			if !closing.Payout.Gross.IsZero() {
				closing.DebtPaid, _ = closing.DebtPaid.Add(closing.Payout.Gross)
				closing.Payout = company.Withdrawal{}
			}
			if err := h.dissolve(ctx, tx, meta, snap, c, acct, closing, company.ClosedInsolvent, now); err != nil {
				return err
			}
			continue
		}
		if err := tx.Companies().Save(ctx, *c); err != nil {
			return err
		}
		remaining++
	}
	fresh, err := tx.Companies().RecordMarketPeriod(ctx, application.CompanyMarketPeriod{
		CityID: city.ID, PeriodNo: clock.PeriodNo, StartedAt: start, EndedAt: end, Population: marketDef.Population,
		Budget: settlement.Budget.Minor(), Asked: settlement.Asked.Minor(), Paid: settlement.Paid.Minor(),
		Companies: len(members), SettledAt: now,
	})
	if err != nil {
		return err
	}
	if !fresh {
		return errors.Internal(stderrors.New("handlers: a city's period was settled twice"))
	}
	clock.PeriodNo++
	clock.PeriodStartedAt = end
	if remaining == 0 {
		clock.NextAt, clock.ActionID, clock.UpdatedAt = nil, "", now
		return tx.Companies().SaveMarketClock(ctx, *clock)
	}
	return h.schedule(ctx, tx, clock, now)
}

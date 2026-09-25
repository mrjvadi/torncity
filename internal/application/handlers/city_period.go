package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/budget"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// CityHandler serves a city's period (docs/adr/0024-property-and-politics.md):
// the budget screen, and — from the scheduler — the end of each city period,
// settled exactly once: the treasury pays the budget's lines by the city's
// allocation (budget.yml; the allocation lever, confirmed by the council),
// war damage is repaired by what the infrastructure line bought, property
// pays its upkeep and tax, and rent falls due (property.go).
//
// A period is GAME time (config city.period), waited through the game clock.
// Exactly once: the clock row is locked first and must name this action and
// period, and the period's budget record has the city and the period as its
// primary key.
type CityHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	scale   gametime.Scale
	period  time.Duration

	// property settles each property of the city at the period's end; nil
	// before property is wired.
	property *PropertyHandler

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewCityHandler builds the handler.
func NewCityHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, period time.Duration,
	idempotencyTTL time.Duration, now func() time.Time,
) *CityHandler {
	if source == nil || cities == nil || policy == nil || ids == nil {
		panic("handlers: NewCityHandler requires content, cities, a policy reader and ids")
	}
	if scale.Validate() != nil || period <= 0 || idempotencyTTL <= 0 {
		panic("handlers: NewCityHandler requires a game clock, a period and an idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &CityHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy, scale: scale,
		period: period, idempotencyTTL: idempotencyTTL, now: now}
}

// WithProperty lets the period settle the city's property.
func (h *CityHandler) WithProperty(p *PropertyHandler) *CityHandler {
	h.property = p
	return h
}

// CityPeriodPayload is the jsonb a city period's scheduled action carries.
type CityPeriodPayload struct {
	CityID   string `json:"city_id"`
	PeriodNo int64  `json:"period_no"`
}

// ensureClock makes sure a city's clock is running and returns it, locked.
func (h *CityHandler) ensureClock(ctx context.Context, tx application.Tx, cityID string, now time.Time) (*application.CityClock, error) {
	clock, err := tx.CityPeriods().Clock(ctx, cityID, now)
	if err != nil {
		return nil, err
	}
	if clock.ActionID != "" {
		return clock, nil
	}
	clock.PeriodStartedAt = now
	return clock, h.schedule(ctx, tx, clock, now)
}

// schedule puts the end of the clock's period on the schedule and saves it.
func (h *CityHandler) schedule(ctx context.Context, tx application.Tx, clock *application.CityClock, now time.Time) error {
	wait := h.scale.RealWait(h.period)
	next := clock.PeriodStartedAt.Add(wait)
	if !next.After(now) {
		next = now.Add(wait)
	}
	payload, err := json.Marshal(CityPeriodPayload{CityID: clock.CityID, PeriodNo: clock.PeriodNo})
	if err != nil {
		return err
	}
	actionID := h.ids.NewID()
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: actionID, ActionType: application.CityPeriodActionType, ActorType: "system",
		ReferenceType: application.CityClockReference, ReferenceID: clock.CityID, Payload: payload,
		StartedAt: now, FinishAt: next,
	}); err != nil {
		return err
	}
	clock.NextAt, clock.ActionID, clock.UpdatedAt = &next, actionID, now
	return tx.CityPeriods().SaveClock(ctx, *clock)
}

// StartClocks runs at start-up: every city's clock is running. It is
// idempotent.
func (h *CityHandler) StartClocks(ctx context.Context) error {
	cities, err := h.cities.List(ctx)
	if err != nil {
		return err
	}
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		for _, c := range cities {
			if _, err := h.ensureClock(ctx, tx, c.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// StartClock makes sure one city's clock is running: a city a content load
// added, or one a test runs. It is idempotent.
func (h *CityHandler) StartClock(ctx context.Context, cityID string) error {
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, err := h.ensureClock(ctx, tx, cityID, h.now())
		return err
	})
}

// Settle handles city.settle from the SCHEDULER: one period of one city,
// exactly once.
func (h *CityHandler) Settle(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in CityPeriodPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("city period payload is unreadable").WithCause(err)
		}
	}
	if in.CityID == "" {
		in.CityID = req.ReferenceID
	}
	if in.CityID == "" || in.PeriodNo < 1 {
		return nil, errors.InvalidInput("city period names no city or no period")
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.CityPeriods().Clock(ctx, in.CityID, now)
		if err != nil {
			return err
		}
		if clock.PeriodNo != in.PeriodNo || clock.ActionID == "" || (req.ActionID != "" && clock.ActionID != req.ActionID) {
			return nil
		}
		if clock.NextAt != nil && now.Before(*clock.NextAt) {
			return errors.Internal(stderrors.New("handlers: a city period ran before it ended"))
		}
		return h.settle(ctx, tx, snap, meta, clock, now)
	})
}

// settle runs one period of one city, its clock locked.
func (h *CityHandler) settle(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	clock *application.CityClock, now time.Time,
) error {
	city, err := h.cities.ByID(ctx, clock.CityID)
	if err != nil {
		return err
	}
	if err := h.spendBudget(ctx, tx, snap, city, clock, now); err != nil {
		return err
	}
	if h.property != nil {
		if err := h.property.settleCity(ctx, tx, snap, meta, city, clock.PeriodNo, now); err != nil {
			return err
		}
	}
	clock.PeriodNo++
	clock.PeriodStartedAt = now
	clock.NextAt, clock.ActionID = nil, ""
	return h.schedule(ctx, tx, clock, now)
}

// spendBudget pays the budget's lines from the treasury by the allocation in
// force, repairs war damage by what the infrastructure line bought, and
// records the period. A content without a budget spends nothing.
func (h *CityHandler) spendBudget(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City,
	clock *application.CityClock, now time.Time,
) error {
	def, ok := snap.Budget()
	if !ok || city.JurisdictionID == "" {
		return nil
	}
	alloc, err := h.policy.Get(ctx, city.JurisdictionID, def.Lever)
	if err != nil {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
	if err != nil {
		return err
	}
	out := budget.Spend(def.Rules(), treasury.Balance.Minor(), alloc.Allocation)
	ref := clock.ActionID
	if drain := out.Spent - out.Defence; drain > 0 {
		if _, err := postRef(ctx, tx.Ledger(), application.ReasonBudgetSpending, application.CityClockReference, ref,
			treasury.ID, application.SystemSinkAccountID, money.FromMinor(drain), now); err != nil {
			return err
		}
	}
	if out.Defence > 0 {
		country, err := tx.Diplomacy().CountryOfCity(ctx, city.ID)
		if err != nil {
			return err
		}
		if country == "" {
			// No country to contribute to: the line's money stays.
			out.Spent -= out.Defence
			for i := range out.Lines {
				if out.Lines[i].Effect == budget.EffectDefenceFund {
					out.Lines[i].Spent = 0
				}
			}
			out.Defence = 0
		} else {
			fund, err := tx.Ledger().AccountFor(ctx, application.AccountDefenceFund, country)
			if err != nil {
				return err
			}
			if _, err := postRef(ctx, tx.Ledger(), application.ReasonDefenceContribution, application.CityClockReference,
				ref, treasury.ID, fund.ID, money.FromMinor(out.Defence), now); err != nil {
				return err
			}
		}
	}
	record := application.CityBudgetPeriod{CityID: city.ID, PeriodNo: clock.PeriodNo, StartedAt: clock.PeriodStartedAt,
		EndedAt: now, Treasury: out.Treasury, Spendable: out.Spendable, Spent: out.Spent, Defence: out.Defence}
	for _, l := range out.Lines {
		record.Lines = append(record.Lines, application.CityBudgetLine{Line: l.Code, Effect: l.Effect,
			ShareBPS: l.ShareBPS, Spent: l.Spent, EffectBPS: l.EffectBPS})
	}
	if err := tx.CityPeriods().RecordBudget(ctx, record); err != nil {
		return err
	}
	return h.repair(ctx, tx, snap, city.ID, record.EffectBPS(budget.EffectWarRepair), now)
}

// repair takes the bps the infrastructure line bought off the city's war
// damage, on top of what time has healed.
func (h *CityHandler) repair(ctx context.Context, tx application.Tx, snap *content.Snapshot, cityID string, bps int64,
	now time.Time,
) error {
	if bps <= 0 {
		return nil
	}
	def, ok := snap.War()
	if !ok {
		return nil
	}
	d, err := tx.War().CityDamage(ctx, cityID, true)
	if err != nil || d == nil {
		return err
	}
	current := def.CityRules().DamageAt(d.DamageBPS, d.AsOf, now, int64(h.scale))
	if current <= 0 {
		return nil
	}
	d.DamageBPS, d.AsOf = max(current-bps, 0), now
	return tx.War().SaveCityDamage(ctx, *d)
}

// BudgetRequest names a city by code; empty for the player's own.
type BudgetRequest struct {
	City string `json:"city,omitempty"`
}

// Budget handles city.budget: a city's budget — the allocation in force and
// announced, what the treasury holds, what the last period spent on each
// line and the effect it bought, and when the next period ends.
func (h *CityHandler) Budget(ctx context.Context, meta envelope.Metadata, req BudgetRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, hasBudget := snap.Budget()
	lang := meta.Language
	var view screens.BudgetView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		var city *application.City
		switch {
		case req.City != "":
			city, err = h.cities.ByCode(ctx, req.City)
		case p.CityID != nil && *p.CityID != "":
			city, err = h.cities.ByID(ctx, *p.CityID)
		}
		if err != nil {
			return err
		}
		if city == nil || !hasBudget || city.JurisdictionID == "" {
			view.NoCity = true
			return nil
		}
		now := h.now()
		view.City = screens.GovPlace{Kind: "city", Code: city.Code, Name: city.Name}
		view.SpendShareBPS = def.SpendShareBPS
		for _, l := range def.Lines {
			view.Order = append(view.Order, l.Code)
		}
		alloc, err := h.policy.Get(ctx, city.JurisdictionID, def.Lever)
		if err != nil {
			return err
		}
		view.Allocation = alloc.Allocation
		if alloc.Pending != nil {
			view.Pending = alloc.Pending.Allocation
			view.PendingIn = max(alloc.Pending.EffectiveAt.Sub(now), 0)
		}
		if alloc.Acting != nil {
			view.CanPropose = holds(alloc.Acting, p.ID)
		}
		view.Lever = def.Lever
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
		if err != nil {
			return err
		}
		view.Treasury = treasury.Balance.Minor()
		last, err := tx.CityPeriods().LatestBudget(ctx, city.ID)
		if err != nil {
			return err
		}
		if last != nil {
			view.Last = &screens.BudgetPeriodView{Spent: last.Spent, Spendable: last.Spendable}
			for _, l := range last.Lines {
				view.Last.Lines = append(view.Last.Lines, screens.BudgetLineView{Code: l.Line, Effect: l.Effect,
					Spent: l.Spent, EffectBPS: l.EffectBPS})
			}
		}
		clock, err := tx.CityPeriods().Clock(ctx, city.ID, now)
		if err != nil {
			return err
		}
		if clock.NextAt != nil {
			view.NextAt = *clock.NextAt
			view.NextIn = max(clock.NextAt.Sub(now), 0)
		}
		return nil
	})
	if err != nil {
		if isSentinel(err, application.ErrCityNotFound) {
			return screens.Budget(screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta)},
				screens.BudgetView{NoCity: true}), nil
		}
		return nil, err
	}
	return screens.Budget(screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta),
		Shared: meta.InGroup()}, view), nil
}

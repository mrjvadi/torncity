package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The finance period (docs/adr/0026 section 1): one clock, on the game
// clock, and one scheduled action per period. Settling a period is exactly
// once — the clock row locked first and naming this action and period, the
// period recorded by its number, and every loan, premium and interest of it
// recorded by (thing, period) — and does, in order: the gold price moves;
// the national treasuries fund their banks; every loan collects its
// instalment; every policy its premium; every savings account earns its
// interest; share orders past their time expire.

// FinancePayload is the jsonb a finance period's action carries.
type FinancePayload struct {
	PeriodNo int64 `json:"period_no"`
}

// StartClock makes sure the finance clock is running. It is idempotent.
func (h *FinanceHandler) StartClock(ctx context.Context) error {
	def, ok := h.content.Current().Finance()
	if !ok {
		return nil
	}
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.Finance().Clock(ctx, now)
		if err != nil || clock.ActionID != "" {
			return err
		}
		clock.PeriodStartedAt = now
		return h.schedule(ctx, tx, def, clock, now)
	})
}

// schedule puts the end of the clock's period on the schedule.
func (h *FinanceHandler) schedule(ctx context.Context, tx application.Tx, def content.FinanceDef, clock *application.FinanceClock,
	now time.Time,
) error {
	wait := h.periodWait(def)
	next := clock.PeriodStartedAt.Add(wait)
	if !next.After(now) {
		next = now.Add(wait)
	}
	payload, err := json.Marshal(FinancePayload{PeriodNo: clock.PeriodNo})
	if err != nil {
		return err
	}
	actionID := h.ids.NewID()
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: actionID, ActionType: application.FinanceActionType, ActorType: "system",
		ReferenceType: application.FinanceReference, ReferenceID: actionID, Payload: payload,
		StartedAt: now, FinishAt: next,
	}); err != nil {
		return err
	}
	clock.NextAt, clock.ActionID, clock.UpdatedAt = &next, actionID, now
	return tx.Finance().SaveClock(ctx, *clock)
}

// Settle handles finance.settle from the SCHEDULER: one finance period,
// exactly once.
func (h *FinanceHandler) Settle(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in FinancePayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("finance payload is unreadable").WithCause(err)
		}
	}
	if in.PeriodNo < 1 {
		return nil, errors.InvalidInput("finance period names no period")
	}
	snap := h.content.Current()
	def, ok := snap.Finance()
	if !ok {
		return nil, nil
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.Finance().Clock(ctx, now)
		if err != nil {
			return err
		}
		if clock.PeriodNo != in.PeriodNo || clock.ActionID == "" || (req.ActionID != "" && clock.ActionID != req.ActionID) {
			return nil
		}
		if clock.NextAt != nil && now.Before(*clock.NextAt) {
			return errors.Internal(stderrors.New("handlers: a finance period ran before it ended"))
		}
		fresh, err := tx.Finance().RecordPeriod(ctx, clock.PeriodNo, now)
		if err != nil {
			return err
		}
		if fresh {
			if err := h.settle(ctx, tx, snap, def, meta, clock, now); err != nil {
				return err
			}
		}
		clock.PeriodNo++
		clock.PeriodStartedAt = now
		clock.NextAt, clock.ActionID = nil, ""
		return h.schedule(ctx, tx, def, clock, now)
	})
}

// settle runs one period.
func (h *FinanceHandler) settle(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	meta envelope.Metadata, clock *application.FinanceClock, now time.Time,
) error {
	period := clock.PeriodNo
	if err := h.moveGold(ctx, tx, def, period, clock.PeriodStartedAt, now); err != nil {
		return err
	}
	if err := h.fundBanks(ctx, tx, def, period, now); err != nil {
		return err
	}
	nextDue := now.Add(h.periodWait(def))
	if err := h.collectLoans(ctx, tx, snap, def, meta, period, now, nextDue); err != nil {
		return err
	}
	if err := h.collectPremiums(ctx, tx, snap, def, meta, period, now); err != nil {
		return err
	}
	if err := h.payInterest(ctx, tx, def, period, now); err != nil {
		return err
	}
	return h.expireOrders(ctx, tx, meta, now)
}

// moveGold sets the dealer's price for the next period, once.
func (h *FinanceHandler) moveGold(ctx context.Context, tx application.Tx, def content.FinanceDef, period int64,
	since, now time.Time,
) error {
	d, err := tx.Finance().Dealer(ctx, h.dealerStart(def, now), true)
	if err != nil {
		return err
	}
	net, err := tx.Finance().GoldNetSince(ctx, since)
	if err != nil {
		return err
	}
	price := def.GoldRules().Next(d.Price, finance.Roll(uint64(period)), net)
	fresh, err := tx.Finance().RecordGoldPrice(ctx, application.GoldPrice{PeriodNo: period, Price: price, NetGrams: net, At: now})
	if err != nil || !fresh {
		return err
	}
	d.Price, d.UpdatedAt = price, now
	return tx.Finance().SaveDealer(ctx, *d)
}

// dealerStart is the dealer as the content opens it.
func (h *FinanceHandler) dealerStart(def content.FinanceDef, now time.Time) application.GoldDealer {
	return application.GoldDealer{Price: def.Gold.StartPrice, Reserve: def.Gold.Reserve, UpdatedAt: now}
}

// fundBanks moves each country's funding from its national treasury into
// its national bank, up to the bank's cap, once a period.
func (h *FinanceHandler) fundBanks(ctx context.Context, tx application.Tx, def content.FinanceDef, period int64, now time.Time) error {
	countries, err := tx.Diplomacy().Countries(ctx)
	if err != nil {
		return err
	}
	for _, c := range countries {
		bps, err := h.lever(ctx, c.ID, LeverBankFunding)
		if err != nil {
			return err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountStateTreasury, c.ID)
		if err != nil {
			return err
		}
		bank, err := tx.Ledger().AccountFor(ctx, application.AccountNationalBank, c.ID)
		if err != nil {
			return err
		}
		amount := min(finance.OfBPS(treasury.Balance.Minor(), bps), max(def.Bank.CapitalCap-bank.Balance.Minor(), 0))
		txID := ""
		if amount > 0 {
			txID = h.ids.NewID()
		}
		fresh, err := tx.Finance().RecordFunding(ctx, c.ID, period, amount, txID, now)
		if err != nil || !fresh || amount == 0 {
			if err != nil {
				return err
			}
			continue
		}
		if _, err := move(ctx, tx, treasury.ID, bank.ID, amount, application.ReasonBankCapital,
			application.BankFundingReference, c.ID, txID, now); err != nil {
			return err
		}
	}
	return nil
}

package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Savings (docs/adr/0026 section 2.5): a player's own account beside the
// bank, earning the deposit rate — the policy rate less a spread — paid by
// the national bank of their country once a period, on the least of what
// the account held at the last period's end and what it holds now, so money
// parked for a moment earns nothing.

// depositRate is a country's deposit rate.
func (h *FinanceHandler) depositRate(ctx context.Context, def content.FinanceDef, countryID string) (int64, error) {
	policy, err := h.lever(ctx, countryID, LeverBaseInterestRate)
	if err != nil {
		return 0, err
	}
	return max(policy-def.Savings.SpreadBPS, 0), nil
}

// payInterest pays every savings account its period's interest, once.
func (h *FinanceHandler) payInterest(ctx context.Context, tx application.Tx, def content.FinanceDef, period int64, now time.Time) error {
	savers, err := tx.Finance().Savers(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(savers))
	for _, s := range savers {
		ids = append(ids, s.PlayerID)
	}
	countries, err := tx.Diplomacy().CountriesOfPlayers(ctx, ids)
	if err != nil {
		return err
	}
	rates := map[string]int64{}
	for _, s := range savers {
		balance := s.Balance
		country := countries[s.PlayerID]
		if country != "" && s.HasMark {
			rate, ok := rates[country]
			if !ok {
				if rate, err = h.depositRate(ctx, def, country); err != nil {
					return err
				}
				rates[country] = rate
			}
			bank, err := tx.Ledger().AccountFor(ctx, application.AccountNationalBank, country)
			if err != nil {
				return err
			}
			amount := min(finance.PeriodInterest(min(s.Balance, s.Mark.Balance), rate, def.PeriodsPerYear),
				max(bank.Balance.Minor(), 0))
			row := application.SavingsInterest{PlayerID: s.PlayerID, PeriodNo: period, CountryID: country,
				Balance: min(s.Balance, s.Mark.Balance), RateBPS: rate, Amount: amount, At: now}
			if amount > 0 {
				row.LedgerTx = h.ids.NewID()
			}
			fresh, err := tx.Finance().RecordSavingsInterest(ctx, row)
			if err != nil {
				return err
			}
			if fresh && amount > 0 {
				acct, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerSavings, s.PlayerID)
				if err != nil {
					return err
				}
				if _, err := move(ctx, tx, bank.ID, acct.ID, amount, application.ReasonSavingsInterest,
					application.SavingsReference, s.PlayerID, row.LedgerTx, now); err != nil {
					return err
				}
				balance += amount
			}
		}
		if err := tx.Finance().SaveSavingsMark(ctx, application.SavingsMark{PlayerID: s.PlayerID, Balance: balance,
			Period: period, UpdatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

// Savings handles save.show.
func (h *FinanceHandler) Savings(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.savings(ctx, meta, "", nil)
}

func (h *FinanceHandler) savings(ctx context.Context, meta envelope.Metadata, notice string, args map[string]any) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	view := screens.SavingsView{Notice: notice, NoticeArgs: args}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.countryOf(ctx, tx, p)
		if err != nil {
			return err
		}
		if view.RateBPS, err = h.depositRate(ctx, def, country.ID); err != nil {
			return err
		}
		w, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerSavings, p.ID)
		if err != nil {
			return err
		}
		view.Balance, view.Bank = acct.Balance.Minor(), w.Bank.Balance.Minor()
		if view.Earned, err = tx.Finance().SavingsEarned(ctx, p.ID); err != nil {
			return err
		}
		mark, err := tx.Finance().SavingsMark(ctx, p.ID)
		if err != nil {
			return err
		}
		if mark != nil {
			view.Next = finance.PeriodInterest(min(mark.Balance, view.Balance), view.RateBPS, def.PeriodsPerYear)
		}
		view.NextAt = nextAt(ctx, tx)
		room := def.Savings.MaxBalance - view.Balance
		for _, bps := range def.Bank.OfferBPS {
			if a := finance.OfBPS(view.Bank, bps); a >= def.Savings.MinAmount && a <= room {
				view.Deposits = append(view.Deposits, a)
			}
			if a := finance.OfBPS(view.Balance, bps); a > 0 {
				view.Withdrawals = append(view.Withdrawals, a)
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Savings(h.screen(meta, lang), view), nil
}

// Deposit handles save.deposit: bank to savings.
func (h *FinanceHandler) Deposit(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	return h.moveSavings(ctx, meta, req, true)
}

// Withdraw handles save.withdraw: savings to bank.
func (h *FinanceHandler) Withdraw(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	return h.moveSavings(ctx, meta, req, false)
}

// moveSavings moves money between a player's bank and savings, once per
// press. A withdrawal lowers the mark interest is counted on, so money taken
// out and put back earns nothing until a period has passed.
func (h *FinanceHandler) moveSavings(ctx context.Context, meta envelope.Metadata, req FinanceRequest, in bool) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	amount, err := parseCount(req.Amount)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	notice := ""
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p, meta, req.Nonce)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, p.ID)
		if err != nil {
			return err
		}
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerSavings, p.ID)
		if err != nil {
			return err
		}
		from, to, reason := bank, acct, application.ReasonSavingsDeposit
		if !in {
			from, to, reason = acct, bank, application.ReasonSavingsWithdrawal
		}
		switch {
		case in && amount < def.Savings.MinAmount:
			r := refuseFinance(screens.FinanceRefusedAmount, screens.AddrSavings)
			r.view.Amount = def.Savings.MinAmount
			return r
		case in && acct.Balance.Minor()+amount > def.Savings.MaxBalance:
			r := refuseFinance(screens.FinanceRefusedSavingsCap, screens.AddrSavings)
			r.view.Amount = def.Savings.MaxBalance
			return r
		}
		if _, err := move(ctx, tx, from.ID, to.ID, amount, reason, application.SavingsReference, p.ID, "", now); err != nil {
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				r := refuseFinance(screens.FinanceRefusedShort, screens.AddrSavings)
				r.view.Amount = amount
				return r
			}
			return err
		}
		if !in {
			mark, err := tx.Finance().SavingsMark(ctx, p.ID)
			if err != nil {
				return err
			}
			if mark != nil {
				mark.Balance, mark.UpdatedAt = min(mark.Balance, acct.Balance.Minor()-amount), now
				if err := tx.Finance().SaveSavingsMark(ctx, *mark); err != nil {
					return err
				}
			}
		}
		notice = "withdrawn"
		if in {
			notice = "deposited"
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	var args map[string]any
	if notice != "" {
		args = map[string]any{"amount": amount}
	}
	return h.savings(ctx, meta, notice, args)
}

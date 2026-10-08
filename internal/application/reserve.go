package application

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/currency"
	"github.com/mrjvadi/torncity/internal/domain/fx"
	"github.com/mrjvadi/torncity/internal/domain/reserve"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The Reserve Bank and the head's tools over a settlement money's reserve (roadmap 2.19 phase 4, docs/adr/0033
// sections 6.3 to 6.8, 6.11 to 6.13 and 7).
//
// The Reserve Bank's levers are held by the reserve_governor office of the support_reserve_bank jurisdiction
// (docs/adr/0015 governance: bounds, a cooldown, a public notice) and are read through policy.Get; a vacant seat
// runs the defaults. What the head of a settlement does with ITS money is the charter's: the permission
// currency.issue (mint more against a deposit, burn, retire the money) and bank.policy (the intervention, an
// excess withdrawal), delegable like any permission. The code, not a lever, fixes: issuance only against a
// deposit; the pot moves only by the reasons below; the valuation; the wind-down claim.
//
// Ledger (each one transaction of one currency, ADR 0009 section 2):
//
//	currency_mint        system source (VC) -> depositor's holding        mint more against a deposit
//	reserve_deposit      depositor's SUP    -> reserve pot                the deposit
//	currency_burn        holder's holding   -> system sink (VC)           the treasury burns, or a claim burns
//	reserve_release      reserve pot        -> treasury (SUP)             the excess a notice allowed; the remainder at retirement
//	intervention_buy     reserve pot        -> treasury's SUP escrow      the head's purchase on the book
//	wind_down_claim      reserve pot        -> holder's cash              a holder's share of the pot

// Ledger reasons of the reserve tools.
const (
	ReasonReserveRelease Reason = "reserve_release"
	ReasonWindDownClaim  Reason = "wind_down_claim"
)

// Levers of the Reserve Bank (configs/content/governance.yml), held by the reserve_governor.
const (
	LeverReserveMintFee        = "reserve.mint_fee_bps"
	LeverReserveFXFee          = "reserve.fx_fee_bps"
	LeverReserveGoldHaircut    = "reserve.gold_haircut_bps"
	LeverReserveMaxMove        = "reserve.max_move_bps"
	LeverReserveWithdrawNotice = "reserve.withdraw_notice"
	LeverReservePolicyRate     = "reserve.policy_rate"
)

// Refusals of the reserve tools, coded.
var (
	ErrReserveNoMoney  = errors.Sentinel(errors.CodeConflict, "application.ErrReserveNoMoney", "the settlement has no chartered money")
	ErrReserveInvalid  = errors.Sentinel(errors.CodeInvalidInput, "application.ErrReserveInvalid", "the request is malformed")
	ErrReserveFunds    = errors.Sentinel(errors.CodeConflict, "application.ErrReserveFunds", "the payer holds too little")
	ErrReserveNoUnits  = errors.Sentinel(errors.CodeConflict, "application.ErrReserveNoUnits", "the treasury holds too few units")
	ErrReserveNoExcess = errors.Sentinel(errors.CodeConflict, "application.ErrReserveNoExcess", "the pot holds no excess over the basis for that")
	ErrReserveBudget   = errors.Sentinel(errors.CodeConflict, "application.ErrReserveBudget", "the intervention's limits allow nothing now")
	ErrReserveNotFound = errors.Sentinel(errors.CodeNotFound, "application.ErrReserveNotFound", "no such request")
	ErrReserveWindDown = errors.Sentinel(errors.CodeConflict, "application.ErrReserveWindDown", "the money is winding down")
	ErrReserveNotWind  = errors.Sentinel(errors.CodeConflict, "application.ErrReserveNotWind", "the money is not winding down, or its window is over")
	ErrReserveNothing  = errors.Sentinel(errors.CodeConflict, "application.ErrReserveNothing", "there is nothing to claim")
)

// ReserveTerms are the Reserve Bank's levers as they stand.
type ReserveTerms struct {
	MintFeeBPS, FXFeeBPS, GoldHaircutBPS, MaxMoveBPS int64
	// WithdrawNoticeHours is the public notice before an excess withdrawal; PolicyRateBPS the base of sovereign
	// loans (not used before they exist).
	WithdrawNoticeHours, PolicyRateBPS int64
}

// WithdrawNotice is the notice as a duration.
func (t ReserveTerms) WithdrawNotice() time.Duration {
	return time.Duration(t.WithdrawNoticeHours) * time.Hour
}

// ReserveRules are the reserve tools' rules: the levers' fallbacks (used only where the content has no such
// lever yet), the limits of the intervention and the model constants (config currency.*).
type ReserveRules struct {
	// Policy resolves the levers (policy.Get); nil means the fallbacks.
	Policy   PolicyReader
	Fallback ReserveTerms
	// InterventionCapBPS is the share of the pot one period's purchases may take, PotFloorBPS the share of the
	// basis the pot may not fall below by intervention, InterventionDelay how long a request waits.
	InterventionCapBPS, PotFloorBPS int64
	InterventionDelay               time.Duration
	// WindDownDays is the claim window of a money winding down.
	WindDownDays int64
	// Macro are the constants of the macro tick.
	Macro reserve.MacroRules
	// Presets are the SUP amounts the reserve screen offers.
	Presets []int64
}

// Enabled reports whether the reserve tools are configured.
func (r ReserveRules) Enabled() bool {
	return r.InterventionCapBPS > 0 && r.WindDownDays > 0 && r.Macro.WTradableBPS > 0
}

// Terms reads the levers. A lever the content does not have yet (or a Reserve Bank not yet loaded) is the
// fallback; any other failure is returned.
func (r ReserveRules) Terms(ctx context.Context, tx Tx) (ReserveTerms, error) {
	t := r.Fallback
	if r.Policy == nil {
		return t, nil
	}
	jur, err := tx.Currency().ReserveBankJurisdiction(ctx)
	if err != nil || jur == "" {
		return t, err
	}
	for _, l := range []struct {
		code string
		into *int64
	}{
		{LeverReserveMintFee, &t.MintFeeBPS}, {LeverReserveFXFee, &t.FXFeeBPS}, {LeverReserveGoldHaircut, &t.GoldHaircutBPS},
		{LeverReserveMaxMove, &t.MaxMoveBPS}, {LeverReserveWithdrawNotice, &t.WithdrawNoticeHours}, {LeverReservePolicyRate, &t.PolicyRateBPS},
	} {
		v, err := r.Policy.Get(ctx, jur, l.code)
		switch {
		case err == nil:
			*l.into = v.Value
		case stderrors.Is(err, ErrUnknownLever), stderrors.Is(err, ErrJurisdictionNotFound):
			// the content has no such lever: the fallback stands
		default:
			return t, err
		}
	}
	return t, nil
}

// reservePot reads the pot account and the figures the head's screens and limits use.
type ReserveReading struct {
	State CurrencyState
	// PotSUP is the pot's balance; Basis the backing basis; Excess the pot above it; Supply the units in
	// existence; Stabilisation the intervention stock; MarketCap the supply's market value in SUP; Coverage the pot over
	// it in basis points (CoverageOK false with no supply).
	PotSUP, Basis, Excess, Supply, Stabilisation, MarketCap int64
	Coverage                                                int64
	CoverageOK                                              bool
	// Treasury is the treasury's own holding of the money; Pending the SUP pending withdrawals ask for.
	Treasury, PendingWithdrawals int64
}

// ReadReserve reads the reserve of a settlement's money.
func ReadReserve(ctx context.Context, tx Tx, settlementID string) (*ReserveReading, error) {
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil {
		return nil, err
	}
	pot, err := tx.Ledger().AccountForCurrency(ctx, AccountReservePot, settlementID, DefaultCurrency)
	if err != nil {
		return nil, err
	}
	hold, err := tx.Ledger().AccountForCurrency(ctx, AccountForeignHolding, settlementID, st.Code)
	if err != nil {
		return nil, err
	}
	stab, err := tx.Currency().StabilisationUnits(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	pend, err := tx.Currency().PendingWithdrawalSUP(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	rd := &ReserveReading{State: *st, PotSUP: pot.Balance.Minor(), Basis: st.BasisSUP, Supply: st.Supply(), Stabilisation: min(stab, hold.Balance.Minor()),
		Treasury: hold.Balance.Minor(), PendingWithdrawals: pend}
	rd.Excess = reserve.Excess(rd.PotSUP, rd.Basis)
	rd.MarketCap = reserve.MarketCap(rd.Supply, st.XRefPPM, st.R0)
	rd.Coverage, rd.CoverageOK = reserve.Coverage(rd.PotSUP, rd.Supply, st.XRefPPM, st.R0)
	return rd, nil
}

// burnFrom destroys units of a holding: one burn row in the issuance log keyed by the flow row, the supply and
// the basis (by the burnt units' share) falling, the pot untouched.
func burnFrom(ctx context.Context, tx Tx, newID func() string, st CurrencyState, from Account, units int64, refType, refID, by string, at time.Time) (string, error) {
	sink, err := tx.Ledger().AccountForCurrency(ctx, AccountSystemSink, "", st.Code)
	if err != nil {
		return "", err
	}
	txID := newID()
	if _, err := tx.Ledger().Post(ctx, LedgerTransaction{
		ID: txID, Reason: ReasonCurrencyBurn, CreatedAt: at, ReferenceType: refType, ReferenceID: refID,
		Entries: []LedgerEntry{
			{AccountID: from.ID, Amount: money.FromMinor(-units)},
			{AccountID: sink.ID, Amount: money.FromMinor(units)},
		},
	}); err != nil {
		return "", err
	}
	if err := tx.Currency().RecordBurn(ctx, CurrencyIssue{
		ID: newID(), SettlementID: st.SettlementID, Kind: "burn", Units: units, XRefPPM: st.XRefPPM,
		LedgerTransactionID: txID, By: by, At: at, ReferenceType: refType, ReferenceID: refID,
	}, currency.BurnBasis(st.BasisSUP, st.Supply(), units)); err != nil {
		return "", err
	}
	return txID, nil
}

// MintMoreRequest asks for more units against a deposit (docs/adr/0033 6.4).
type MintMoreRequest struct {
	SettlementID string
	// Depositor is a player whose purse pays and receives the units, or empty for the treasury.
	DepositorID string
	DepositSUP  int64
	By          string
	At          time.Time
}

// MintMoreResult says what a mint did.
type MintMoreResult struct{ Units, BasisSUP, FeeSUP, DepositSUP int64 }

// MintMore issues units against a deposit at the reference rate: the SUP goes into the reserve pot, the
// units are made in the money's own system source and arrive in the depositor's holding, the basis grows by
// the deposit less the issuance fee, and the fee stays in the pot as excess. Nothing is printed without a
// deposit, and a money winding down issues nothing.
func MintMore(ctx context.Context, tx Tx, newID func() string, terms ReserveTerms, r MintMoreRequest) (MintMoreResult, error) {
	var res MintMoreResult
	st, err := tx.Currency().State(ctx, r.SettlementID)
	if err != nil || st == nil {
		return res, ErrReserveNoMoney
	}
	if st.Status != CurrencyChartered {
		return res, ErrReserveWindDown
	}
	if r.DepositSUP < 1 {
		return res, ErrReserveInvalid
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(r.SettlementID)); err != nil {
		return res, err
	}
	mint, err := currency.Mint(r.DepositSUP, st.Rate(), terms.MintFeeBPS)
	if err != nil || mint.Units <= 0 {
		return res, ErrReserveInvalid
	}
	l := tx.Ledger()
	var from Account
	to := Account{}
	if r.DepositorID == "" {
		if from, err = l.AccountFor(ctx, AccountCityTreasury, r.SettlementID); err != nil {
			return res, err
		}
		to, err = l.AccountForCurrency(ctx, AccountForeignHolding, r.SettlementID, st.Code)
	} else {
		if from, err = l.AccountFor(ctx, AccountPlayerCash, r.DepositorID); err != nil {
			return res, err
		}
		to, err = l.AccountForCurrency(ctx, AccountForeignHolding, r.DepositorID, st.Code)
	}
	if err != nil {
		return res, err
	}
	if from.Balance.Minor() < r.DepositSUP {
		return res, ErrReserveFunds
	}
	pot, err := l.AccountForCurrency(ctx, AccountReservePot, r.SettlementID, DefaultCurrency)
	if err != nil {
		return res, err
	}
	source, err := l.AccountForCurrency(ctx, AccountSystemSource, "", st.Code)
	if err != nil {
		return res, err
	}
	if _, err := l.Post(ctx, LedgerTransaction{ID: newID(), Reason: ReasonReserveDeposit, CreatedAt: r.At, ReferenceType: CurrencyCharterReference, ReferenceID: r.SettlementID,
		Entries: []LedgerEntry{{AccountID: from.ID, Amount: money.FromMinor(-r.DepositSUP)}, {AccountID: pot.ID, Amount: money.FromMinor(r.DepositSUP)}}}); err != nil {
		return res, err
	}
	mintTx := newID()
	if _, err := l.Post(ctx, LedgerTransaction{ID: mintTx, Reason: ReasonCurrencyMint, CreatedAt: r.At, ReferenceType: CurrencyCharterReference, ReferenceID: r.SettlementID,
		Entries: []LedgerEntry{{AccountID: source.ID, Amount: money.FromMinor(-mint.Units)}, {AccountID: to.ID, Amount: money.FromMinor(mint.Units)}}}); err != nil {
		return res, err
	}
	if err := tx.Currency().RecordMint(ctx, CurrencyIssue{ID: newID(), SettlementID: r.SettlementID, Kind: "mint", DepositSUP: r.DepositSUP, Units: mint.Units,
		XRefPPM: st.XRefPPM, MintFeeBPS: terms.MintFeeBPS, LedgerTransactionID: mintTx, By: r.By, At: r.At}, mint.BasisSUP); err != nil {
		return res, err
	}
	return MintMoreResult{Units: mint.Units, BasisSUP: mint.BasisSUP, FeeSUP: mint.FeeSUP, DepositSUP: r.DepositSUP}, nil
}

// BurnTreasury destroys units the treasury holds (docs/adr/0033 6.7): the supply falls, the basis by the burnt
// units' share, the pot is untouched; the difference between what the units cost and their basis becomes
// excess. Logged as a burn.
func BurnTreasury(ctx context.Context, tx Tx, newID func() string, settlementID string, units int64, by string, at time.Time) (string, error) {
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil {
		return "", ErrReserveNoMoney
	}
	if st.Status != CurrencyChartered {
		return "", ErrReserveWindDown
	}
	if units < 1 {
		return "", ErrReserveInvalid
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(settlementID)); err != nil {
		return "", err
	}
	hold, err := tx.Ledger().AccountForCurrency(ctx, AccountForeignHolding, settlementID, st.Code)
	if err != nil {
		return "", err
	}
	if hold.Balance.Minor() < units {
		return "", ErrReserveNoUnits
	}
	return burnFrom(ctx, tx, newID, *st, hold, units, "currency_burns", newID(), by, at)
}

// RequestWithdrawal announces a withdrawal of excess (docs/adr/0033 6.7): only the excess over the basis, less what
// earlier requests already ask for, and only with the Reserve Bank's public notice; it executes at the first
// period close after the notice. Nothing moves now.
func RequestWithdrawal(ctx context.Context, tx Tx, newID func() string, terms ReserveTerms, settlementID, by string, sup int64, at time.Time) (Withdrawal, error) {
	var w Withdrawal
	rd, err := ReadReserve(ctx, tx, settlementID)
	if err != nil || rd == nil {
		return w, ErrReserveNoMoney
	}
	if rd.State.Status != CurrencyChartered {
		return w, ErrReserveWindDown
	}
	if sup < 1 {
		return w, ErrReserveInvalid
	}
	if sup > rd.Excess-rd.PendingWithdrawals {
		return w, ErrReserveNoExcess
	}
	w = Withdrawal{ID: newID(), SettlementID: settlementID, SUP: sup, RequestedBy: by, RequestedAt: at, ExecuteAfter: at.Add(terms.WithdrawNotice()), Status: ReservePending}
	return w, tx.Currency().InsertWithdrawal(ctx, w)
}

// ExecuteWithdrawal carries out one due withdrawal, once: the excess must still be there and the money still
// chartered, else the request is refused for good. The status is the fence.
func ExecuteWithdrawal(ctx context.Context, tx Tx, newID func() string, w Withdrawal, now time.Time) error {
	if w.Status != ReservePending {
		return nil
	}
	done := now
	w.ExecutedAt = &done
	rd, err := ReadReserve(ctx, tx, w.SettlementID)
	if err != nil {
		return err
	}
	switch {
	case rd == nil || rd.State.Status != CurrencyChartered:
		w.Status, w.Refusal = ReserveRefused, "not_chartered"
	case w.SUP > reserve.Excess(rd.PotSUP, rd.Basis):
		w.Status, w.Refusal = ReserveRefused, "no_excess"
	default:
		if err := tx.Items().LockOrg(ctx, SettlementOrg(w.SettlementID)); err != nil {
			return err
		}
		pot, err := tx.Ledger().AccountForCurrency(ctx, AccountReservePot, w.SettlementID, DefaultCurrency)
		if err != nil {
			return err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, AccountCityTreasury, w.SettlementID)
		if err != nil {
			return err
		}
		txID := newID()
		if _, err := tx.Ledger().Post(ctx, LedgerTransaction{ID: txID, Reason: ReasonReserveRelease, CreatedAt: now, ReferenceType: "currency_withdrawals", ReferenceID: w.ID,
			Entries: []LedgerEntry{{AccountID: pot.ID, Amount: money.FromMinor(-w.SUP)}, {AccountID: treasury.ID, Amount: money.FromMinor(w.SUP)}}}); err != nil {
			return err
		}
		if err := tx.Currency().AddReleased(ctx, w.SettlementID, w.SUP); err != nil {
			return err
		}
		w.Status, w.TransactionID = ReserveDone, txID
	}
	return tx.Currency().SaveWithdrawal(ctx, w)
}

// PostIntervention records the head's request to buy the money on the book with the pot, or to sell units the
// treasury holds. It is public and waits one delay before it executes, so the head cannot front-run their own
// defence; its limits are applied when it executes.
func PostIntervention(ctx context.Context, tx Tx, newID func() string, rules ReserveRules, settlementID, by, side string, units, price int64, at time.Time) (Intervention, error) {
	var i Intervention
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil {
		return i, ErrReserveNoMoney
	}
	if st.Status != CurrencyChartered {
		return i, ErrReserveWindDown
	}
	if units < 1 || price < 1 || (side != FXBuy && side != FXSell) {
		return i, ErrReserveInvalid
	}
	i = Intervention{ID: newID(), SettlementID: settlementID, Side: side, Units: units, Price: price, PostedBy: by, PostedAt: at,
		ExecuteAfter: at.Add(rules.InterventionDelay), Status: ReservePending}
	return i, tx.Currency().InsertIntervention(ctx, i)
}

// ExecuteIntervention carries out one due request within the ADR's limits: a purchase takes at most the cap of
// the pot per period and never so much that the pot falls below the floor of the basis; a sale offers at most
// the cap of the units the treasury holds. A request that exceeds a limit is clipped to it; one the limits
// leave nothing of is refused. The order is the treasury's, funded from (and paying into) the pot.
func ExecuteIntervention(ctx context.Context, tx Tx, newID func() string, rules ReserveRules, fxRules FXRules, i Intervention, periodStart, now time.Time) error {
	if i.Status != ReservePending {
		return nil
	}
	refuse := func(why string) error {
		t := now
		i.Status, i.Refusal, i.ExecutedAt = ReserveRefused, why, &t
		return tx.Currency().SaveIntervention(ctx, i)
	}
	rd, err := ReadReserve(ctx, tx, i.SettlementID)
	if err != nil {
		return err
	}
	if rd == nil || rd.State.Status != CurrencyChartered {
		return refuse("not_chartered")
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(i.SettlementID)); err != nil {
		return err
	}
	usedSUP, usedUnits, err := tx.Currency().InterventionUse(ctx, i.SettlementID, periodStart)
	if err != nil {
		return err
	}
	units := i.Units
	if i.Side == FXBuy {
		budget := reserve.BuyBudget(rd.PotSUP, rd.Basis, usedSUP, rules.InterventionCapBPS, rules.PotFloorBPS)
		// the largest quantity the budget buys (the escrow grows with the quantity)
		lo, hi := int64(0), units
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if fx.BuyEscrow(mid, i.Price, fxRules.ReserveFeeBPS) <= budget {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		units = lo
		if units < 1 {
			return refuse("budget")
		}
	} else {
		units = min(units, reserve.SellBudget(rd.Treasury, usedUnits, rules.InterventionCapBPS))
		if units < 1 {
			return refuse("budget")
		}
	}
	placed, err := PlaceFX(ctx, tx, newID, fxRules, FXPlace{SettlementID: i.SettlementID, Owner: FXOwner{Kind: FXOwnerSettlement, ID: i.SettlementID},
		Side: i.Side, Units: units, Price: i.Price, At: now, Purpose: FXPurposeIntervention})
	if err != nil {
		switch {
		case stderrors.Is(err, ErrFXOutOfBand):
			return refuse("out_of_band")
		case stderrors.Is(err, ErrFXTooSmall):
			return refuse("too_small")
		case stderrors.Is(err, ErrFXFunds):
			return refuse("funds")
		case stderrors.Is(err, ErrFXTooMany):
			return refuse("too_many")
		}
		return err
	}
	t := now
	i.Status, i.ExecutedAt, i.OrderID, i.Units = ReserveDone, &t, placed.Order.ID, units
	if i.Side == FXBuy {
		i.SUPUsed = fx.BuyEscrow(units, i.Price, fxRules.ReserveFeeBPS)
	}
	return tx.Currency().SaveIntervention(ctx, i)
}

// BeginWindDown puts a settlement's money into wind-down (docs/adr/0033 6.13): it issues nothing more, the head
// no longer defends it, every open order is cancelled and its escrow given back, pending requests are
// cancelled, and holders may claim a pro-rata share of the pot for the window. Fenced by the state row:
// a second call changes nothing.
func BeginWindDown(ctx context.Context, tx Tx, newID func() string, rules ReserveRules, settlementID, reason string, at time.Time) (bool, error) {
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil || st.Status != CurrencyChartered {
		return false, err
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(settlementID)); err != nil {
		return false, err
	}
	ends := at.Add(time.Duration(rules.WindDownDays) * 24 * time.Hour)
	ok, err := tx.Currency().BeginWindDown(ctx, settlementID, reason, at, ends)
	if err != nil || !ok {
		return false, err
	}
	// a book of orders that escrow units would hide them from the claim: close them all
	for _, side := range []string{FXBuy, FXSell} {
		orders, err := tx.FX().OpenOrders(ctx, settlementID, side, 100_000)
		if err != nil {
			return false, err
		}
		for k := range orders {
			closeFX(&orders[k], FXCancelled, at)
			if err := releaseFX(ctx, tx, newID, *st, &orders[k], at); err != nil {
				return false, err
			}
			if err := tx.FX().SaveOrder(ctx, orders[k]); err != nil {
				return false, err
			}
		}
	}
	pend, err := tx.Currency().InterventionsOf(ctx, settlementID, 1000)
	if err != nil {
		return false, err
	}
	for _, p := range pend {
		if p.Status == ReservePending {
			t := at
			p.Status, p.Refusal, p.ExecutedAt = ReserveCancelled, "wind_down", &t
			if err := tx.Currency().SaveIntervention(ctx, p); err != nil {
				return false, err
			}
		}
	}
	ws, err := tx.Currency().WithdrawalsOf(ctx, settlementID, 1000)
	if err != nil {
		return false, err
	}
	for _, w := range ws {
		if w.Status == ReservePending {
			t := at
			w.Status, w.Refusal, w.ExecutedAt = ReserveCancelled, "wind_down", &t
			if err := tx.Currency().SaveWithdrawal(ctx, w); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// ClaimResult says what a claim did.
type ClaimResult struct{ Units, SUP int64 }

// ClaimPot lets a holder exchange the whole of their units for their share of what the pot holds now
// (docs/adr/0033 6.13): the pot remaining times their units over the units holders other than the treasury
// still hold, rounded down; the units are burnt. Taking each claim from what is left makes the shares equal
// whatever the order of the claims.
func ClaimPot(ctx context.Context, tx Tx, newID func() string, settlementID, playerID string, at time.Time) (ClaimResult, error) {
	var res ClaimResult
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil {
		return res, ErrReserveNoMoney
	}
	if st.Status != CurrencyWindDown || st.WindDownEndsAt == nil || !at.Before(*st.WindDownEndsAt) {
		return res, ErrReserveNotWind
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(settlementID)); err != nil {
		return res, err
	}
	l := tx.Ledger()
	mine, err := l.AccountForCurrency(ctx, AccountForeignHolding, playerID, st.Code)
	if err != nil {
		return res, err
	}
	units := mine.Balance.Minor()
	if units < 1 || playerID == settlementID {
		return res, ErrReserveNothing
	}
	pot, err := l.AccountForCurrency(ctx, AccountReservePot, settlementID, DefaultCurrency)
	if err != nil {
		return res, err
	}
	claimable, err := tx.Currency().ClaimableUnits(ctx, settlementID, st.Code)
	if err != nil {
		return res, err
	}
	share := reserve.ClaimShare(pot.Balance.Minor(), claimable, units)
	claimID := newID()
	burnTx, err := burnFrom(ctx, tx, newID, *st, mine, units, "currency_claims", claimID, "player:"+playerID, at)
	if err != nil {
		return res, err
	}
	c := Claim{ID: claimID, SettlementID: settlementID, PlayerID: playerID, Units: units, SUP: share, PotBefore: pot.Balance.Minor(),
		ClaimableBefore: claimable, BurnTransactionID: burnTx, At: at}
	if share > 0 {
		cash, err := l.AccountFor(ctx, AccountPlayerCash, playerID)
		if err != nil {
			return res, err
		}
		c.SUPTransactionID = newID()
		if _, err := l.Post(ctx, LedgerTransaction{ID: c.SUPTransactionID, Reason: ReasonWindDownClaim, CreatedAt: at, ReferenceType: "currency_claims", ReferenceID: claimID,
			Entries: []LedgerEntry{{AccountID: pot.ID, Amount: money.FromMinor(-share)}, {AccountID: cash.ID, Amount: money.FromMinor(share)}}}); err != nil {
			return res, err
		}
		if err := tx.Currency().AddReleased(ctx, settlementID, share); err != nil {
			return res, err
		}
	}
	if err := tx.Currency().InsertClaim(ctx, c); err != nil {
		return res, err
	}
	return ClaimResult{Units: units, SUP: share}, nil
}

// FinishWindDown retires a money whose claim window is over: what the holders left unclaimed in the pot goes to
// the settlement's treasury, the book closes, and the units that remain are worth nothing. Fenced by the state.
func FinishWindDown(ctx context.Context, tx Tx, newID func() string, settlementID string, now time.Time) (bool, error) {
	st, err := tx.Currency().State(ctx, settlementID)
	if err != nil || st == nil || st.Status != CurrencyWindDown || st.WindDownEndsAt == nil || now.Before(*st.WindDownEndsAt) {
		return false, err
	}
	if err := tx.Items().LockOrg(ctx, SettlementOrg(settlementID)); err != nil {
		return false, err
	}
	ok, err := tx.Currency().Retire(ctx, settlementID, now)
	if err != nil || !ok {
		return false, err
	}
	for _, side := range []string{FXBuy, FXSell} {
		orders, err := tx.FX().OpenOrders(ctx, settlementID, side, 100_000)
		if err != nil {
			return false, err
		}
		for k := range orders {
			closeFX(&orders[k], FXCancelled, now)
			if err := releaseFX(ctx, tx, newID, *st, &orders[k], now); err != nil {
				return false, err
			}
			if err := tx.FX().SaveOrder(ctx, orders[k]); err != nil {
				return false, err
			}
		}
	}
	pot, err := tx.Ledger().AccountForCurrency(ctx, AccountReservePot, settlementID, DefaultCurrency)
	if err != nil {
		return false, err
	}
	if left := pot.Balance.Minor(); left > 0 {
		treasury, err := tx.Ledger().AccountFor(ctx, AccountCityTreasury, settlementID)
		if err != nil {
			return false, err
		}
		if _, err := tx.Ledger().Post(ctx, LedgerTransaction{ID: newID(), Reason: ReasonReserveRelease, CreatedAt: now, ReferenceType: CurrencyCharterReference, ReferenceID: settlementID,
			Entries: []LedgerEntry{{AccountID: pot.ID, Amount: money.FromMinor(-left)}, {AccountID: treasury.ID, Amount: money.FromMinor(left)}}}); err != nil {
			return false, err
		}
		if err := tx.Currency().AddReleased(ctx, settlementID, left); err != nil {
			return false, err
		}
	}
	return true, nil
}

// MacroTick takes one period's macro reading of a money (docs/adr/0033 7.1, 7.3): the price indices, coverage and
// supply growth. It reads the ledger and the book only, and writes one append-only row keyed by (money, period),
// so any replica's repeat changes nothing. It returns whether it wrote.
func MacroTick(ctx context.Context, tx Tx, rules ReserveRules, settlementID string, periodNo int64, from, to, now time.Time) (bool, error) {
	rd, err := ReadReserve(ctx, tx, settlementID)
	if err != nil || rd == nil || rd.State.Status == CurrencyRetired {
		return false, err
	}
	st := rd.State
	last, err := tx.Currency().MacroRows(ctx, settlementID, 1)
	if err != nil {
		return false, err
	}
	y, err := tx.Currency().Output(ctx, settlementID, from, to)
	if err != nil {
		return false, err
	}
	m := reserve.Macro{SupplyUnits: rd.Supply, Stabilisation: rd.Stabilisation, XRefPPM: st.XRefPPM, PrevXRefPPM: st.XRefPPM, Y: y,
		M: (rd.Supply - rd.Stabilisation) / max(st.R0, 1), Tradable: reserve.PPM, NonTradable: reserve.PPM, Price: reserve.PPM, PrevSupply: rd.Supply}
	if len(last) > 0 {
		p := last[0]
		if p.PeriodNo >= periodNo {
			return false, nil
		}
		m.PrevXRefPPM, m.Tradable, m.NonTradable, m.Price, m.PrevSupply = p.XRefPPM, p.Tradable, p.NonTradable, p.Price, p.SupplyUnits
	}
	rd2 := reserve.Tick(m, rules.Macro)
	row := MacroRow{SettlementID: settlementID, PeriodNo: periodNo, SupplyUnits: rd.Supply, Stabilisation: rd.Stabilisation, M: m.M, Y: y, XRefPPM: st.XRefPPM,
		Tradable: rd2.Tradable, NonTradable: rd2.NonTradable, Price: rd2.Price, PiLocalBPS: rd2.PiLocalBPS, SupplyGrowthBPS: rd2.SupplyGrowthBPS,
		PotSUP: rd.PotSUP, BasisSUP: rd.Basis, At: now}
	if rd.CoverageOK {
		c := rd.Coverage
		row.CoverageBPS = &c
	}
	return tx.Currency().RecordMacro(ctx, row)
}

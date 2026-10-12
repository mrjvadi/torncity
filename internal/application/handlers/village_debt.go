package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/domain/sovereign"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Loans between polities (docs/adr/0076 11). A settlement, a union or a country lends from its treasury to another. A loan creates no money:
// the principal moves from the lender's treasury to the borrower's, the instalments move back, and a default is written off, restructured by
// agreement or taken to diplomacy; no citizen's property is ever taken.
//
//   - WHO ACTS. The holder of debt.lend offers and may restructure or write off; the holder of debt.borrow accepts. Each in their own
//     charter (a settlement's, or a union's).
//   - WHAT IT CONSUMES. Nothing: a loan moves money; an instalment missed costs a late fee.
//   - WHAT BREAKS. A lender that cannot spare the principal; a borrower that could not carry the service; instalments unpaid (arrears, then a
//     default, a public mark).

// VillageDebtRequest is the payload of settlement.debt.
type VillageDebtRequest struct {
	// Action is "" (the screen) or one of village.DebtAction*.
	Action string `json:"action,omitempty"`
	// ID names a loan. Union names the union that acts or that is the borrower; Code a borrower settlement's code (offer).
	ID    string `json:"id,omitempty"`
	Union string `json:"union,omitempty"`
	Code  string `json:"code,omitempty"`
	// ToUnion is a union borrower (offer).
	ToUnion string `json:"to_union,omitempty"`
	// Principal, Rate (yearly, basis points) and Periods are the terms.
	Principal string `json:"principal,omitempty"`
	Rate      string `json:"rate,omitempty"`
	Periods   string `json:"periods,omitempty"`
}

type debtParty struct {
	kind, id string
	name     string
	account  application.Account
	// power checks
	settlement application.FoundedSettlement
	union      *application.Union
}

// Debt handles settlement.debt.
func (h *VillageHandler) Debt(ctx context.Context, meta envelope.Metadata, req VillageDebtRequest) (*presentation.Response, error) {
	lang := meta.Language
	var view village.DebtView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, has, err := h.settlementOpt(ctx, tx, meta)
		if err != nil {
			return err
		}
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.debtAct(ctx, tx, meta, s, has, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.debtView(ctx, tx, s, has, p, strings.TrimSpace(req.Union))
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Debt(h.screen(meta, lang), view), nil
}

// actingParty resolves who the player acts for: the union named, or their settlement.
func (h *VillageHandler) actingParty(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, unionID string) (debtParty, error) {
	if unionID != "" {
		u, err := tx.Unions().ByID(ctx, unionID, false)
		if err != nil || u == nil || u.Status != "active" {
			return debtParty{}, refuseVillage(village.UnionNoUnion, village.AddrDebt)
		}
		acct, err := unionPurse(ctx, tx, u)
		if err != nil {
			return debtParty{}, err
		}
		return debtParty{kind: "union", id: u.ID, name: u.Name, account: acct, union: u}, nil
	}
	if !has {
		return debtParty{}, refuseVillage(village.VillageNoSettlement)
	}
	acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
	if err != nil {
		return debtParty{}, err
	}
	return debtParty{kind: "settlement", id: s.CityID, name: s.Name, account: acct, settlement: s}, nil
}

func (h *VillageHandler) partyPower(ctx context.Context, tx application.Tx, pt debtParty, p *application.Player, perm charter.Permission) (int64, error) {
	if pt.union != nil {
		return h.requireUnionPower(ctx, tx, pt.union, p.ID, perm)
	}
	return requirePermission(ctx, tx, pt.settlement, p.ID, perm)
}

// partyOf loads a party of a stored loan.
func (h *VillageHandler) partyOf(ctx context.Context, tx application.Tx, kind, id string) (debtParty, error) {
	if kind == "union" {
		u, err := tx.Unions().ByID(ctx, id, false)
		if err != nil || u == nil {
			return debtParty{}, refuseVillage(village.UnionNoUnion, village.AddrDebt)
		}
		acct, err := unionPurse(ctx, tx, u)
		return debtParty{kind: kind, id: id, name: u.Name, account: acct, union: u}, err
	}
	st, err := tx.Settlements().ByID(ctx, id)
	if err != nil {
		return debtParty{}, err
	}
	acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, id)
	return debtParty{kind: kind, id: id, name: st.Name, account: acct, settlement: st}, err
}

func (h *VillageHandler) debtAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, act string, req VillageDebtRequest) (string, error) {
	if !h.polityOpen() {
		return "", refuseVillage(village.DebtNotYet, village.AddrDebt)
	}
	now := h.now()
	switch act {
	case village.DebtActionOffer:
		return village.DebtNoticeOffered, h.debtOffer(ctx, tx, meta, s, has, p, req, now, "")
	case village.DebtActionRestructure:
		return village.DebtNoticeRestructured, h.debtOffer(ctx, tx, meta, s, has, p, req, now, strings.TrimSpace(req.ID))
	case village.DebtActionAccept:
		return village.DebtNoticeAccepted, h.debtAccept(ctx, tx, meta, s, has, p, req, now)
	case village.DebtActionDecline:
		return village.DebtNoticeDeclined, h.debtClose(ctx, tx, meta, s, has, p, req, now, "declined", false)
	case village.DebtActionWithdraw:
		return village.DebtNoticeWithdrawn, h.debtClose(ctx, tx, meta, s, has, p, req, now, "withdrawn", true)
	case village.DebtActionWriteOff:
		return village.DebtNoticeWrittenOff, h.debtWriteOff(ctx, tx, meta, s, has, p, req, now)
	}
	return "", refuseVillage(village.DebtBadArgs, village.AddrDebt)
}

func (h *VillageHandler) periodsPerYear() int64 {
	if h.polity.DebtPeriod <= 0 {
		return 52
	}
	return max(int64(365*24*time.Hour/h.polity.DebtPeriod), 1)
}

// polityLoans lists the loans of a party and works out what it owes and is owed in principal, and what it pays each period.
func (h *VillageHandler) polityLoans(ctx context.Context, tx application.Tx, kind, id string) (loans []application.SovereignLoan, owedOut, owedIn, service int64, err error) {
	loans, err = tx.Debts().Of(ctx, kind, id, 200)
	if err != nil {
		return
	}
	for _, l := range loans {
		if l.Status != "active" && l.Status != "defaulted" {
			continue
		}
		sched, e := h.loanTerms(l).Schedule()
		if e != nil {
			continue
		}
		pr, _ := sched.Remaining(int64(l.Paid))
		if l.BorrowerKind == kind && l.BorrowerID == id {
			owedOut += pr
			if l.Status == "active" {
				service += sched.Amount(int64(l.Paid) + 1)
			}
		}
		if l.LenderKind == kind && l.LenderID == id {
			owedIn += pr
		}
	}
	return
}

func (h *VillageHandler) loanTerms(l application.SovereignLoan) finance.LoanTerms {
	return finance.LoanTerms{Principal: l.Principal, RateBPS: l.RateBPS, Periods: int64(l.Periods), PeriodsPerYear: int64(l.PeriodsPerYear)}
}

func (h *VillageHandler) debtBounds() sovereign.Bounds {
	return sovereign.Bounds{MaxInstalments: h.polity.DebtMaxInstalments, MaxRateBPS: h.polity.DebtMaxRateBPS, ServiceCapBPS: h.polity.DebtServiceCapBPS, LenderReserveBPS: 1000}
}

// partyIncome is what came into a party's treasury over the last period: the base the debt service is weighed against.
func (h *VillageHandler) partyIncome(ctx context.Context, tx application.Tx, pt debtParty, now time.Time) (int64, error) {
	if pt.kind == "settlement" {
		return tx.Unions().TreasuryIncome(ctx, pt.id, now.Add(-h.polity.DebtPeriod), now)
	}
	// a union lives on its dues: what it was paid in the last period
	return tx.Unions().PurseIncome(ctx, pt.account.ID, now.Add(-h.polity.DebtPeriod), now)
}

// debtOffer offers a loan, or (replaces != "") a replacement of one loan by agreement.
func (h *VillageHandler) debtOffer(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, req VillageDebtRequest, now time.Time, replaces string) error {
	lender, err := h.actingParty(ctx, tx, s, has, strings.TrimSpace(req.Union))
	if err != nil {
		return err
	}
	perm := charter.DebtLend
	if replaces != "" {
		perm = charter.DebtRestructure
	}
	limit, err := h.partyPower(ctx, tx, lender, p, perm)
	if err != nil {
		return err
	}
	principal, rate, periods := atoi64(req.Principal), atoi64(req.Rate), atoi64(req.Periods)
	var borrower debtParty
	var old *application.SovereignLoan
	if replaces != "" {
		if old, err = tx.Debts().ByID(ctx, replaces, true); err != nil {
			return err
		}
		if old == nil || old.LenderKind != lender.kind || old.LenderID != lender.id || (old.Status != "active" && old.Status != "defaulted") {
			return refuseVillage(village.DebtNoLoan, village.AddrDebt)
		}
		if borrower, err = h.partyOf(ctx, tx, old.BorrowerKind, old.BorrowerID); err != nil {
			return err
		}
		sched, e := h.loanTerms(*old).Schedule()
		if e != nil {
			return refuseVillage(village.DebtTerms, village.AddrDebt)
		}
		owed, _ := sched.Remaining(int64(old.Paid))
		if _, e := sovereign.Restructure(owed, finance.LoanTerms{Principal: principal, RateBPS: rate, Periods: periods, PeriodsPerYear: h.periodsPerYear()}, h.debtBounds()); e != nil {
			if stderrors.Is(e, sovereign.ErrRestructure) {
				return refuseVillage(village.DebtRaise, village.AddrDebt)
			}
			return refuseVillage(village.DebtTerms, village.AddrDebt)
		}
	} else {
		switch {
		case strings.TrimSpace(req.ToUnion) != "":
			u, e := tx.Unions().ByID(ctx, strings.TrimSpace(req.ToUnion), false)
			if e != nil || u == nil || u.Status != "active" {
				return refuseVillage(village.DebtNoTarget, village.AddrDebt)
			}
			if borrower, err = h.partyOf(ctx, tx, "union", u.ID); err != nil {
				return err
			}
		default:
			o, e := h.settlementByCode(ctx, tx, req.Code)
			if e != nil {
				return e
			}
			if o == nil {
				return refuseVillage(village.DebtNoTarget, village.AddrDebt)
			}
			if borrower, err = h.partyOf(ctx, tx, "settlement", o.CityID); err != nil {
				return err
			}
		}
		if borrower.kind == lender.kind && borrower.id == lender.id {
			return refuseVillage(village.DebtSelf, village.AddrDebt)
		}
		if limit > 0 && principal > limit {
			return refuseVillage(village.DebtTerms, village.AddrDebt)
		}
		balance, err := accountBalance(ctx, tx, lender.account)
		if err != nil {
			return err
		}
		_, _, outIn, _, err := h.polityLoans(ctx, tx, lender.kind, lender.id)
		if err != nil {
			return err
		}
		income, err := h.partyIncome(ctx, tx, borrower, now)
		if err != nil {
			return err
		}
		_, _, _, service, err := h.polityLoans(ctx, tx, borrower.kind, borrower.id)
		if err != nil {
			return err
		}
		if _, e := sovereign.Check(sovereign.Offer{Lender: lender.id, Borrower: borrower.id, Terms: finance.LoanTerms{Principal: principal, RateBPS: rate, Periods: periods, PeriodsPerYear: h.periodsPerYear()}},
			h.debtBounds(), balance, outIn, income, service); e != nil {
			switch {
			case stderrors.Is(e, sovereign.ErrLender):
				return refuseVillage(village.DebtLender, village.AddrDebt)
			case stderrors.Is(e, sovereign.ErrService):
				return refuseVillage(village.DebtService, village.AddrDebt)
			case stderrors.Is(e, sovereign.ErrSelf):
				return refuseVillage(village.DebtSelf, village.AddrDebt)
			}
			return refuseVillage(village.DebtTerms, village.AddrDebt)
		}
	}
	l := application.SovereignLoan{ID: h.ids.NewID(), LenderKind: lender.kind, LenderID: lender.id, BorrowerKind: borrower.kind, BorrowerID: borrower.id, Principal: principal, RateBPS: rate,
		Periods: int(periods), PeriodsPerYear: int(h.periodsPerYear()), OfferedBy: p.ID, OfferedAt: now, ExpiresAt: now.AddDate(0, 0, int(h.polity.RatifyDays)), Replaces: replaces}
	if err := tx.Debts().Insert(ctx, l); err != nil {
		return err
	}
	if err := h.schedulePolity(ctx, tx, application.TickLoanOffer, application.DebtReference, l.ID, "", p.ID, now, l.ExpiresAt); err != nil {
		return err
	}
	if err := tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, "offered", map[string]any{"principal": principal, "rate_bps": rate, "periods": periods, "replaces": replaces}, now); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "debt_offered", s.CityID, map[string]any{"loan": l.ID, "lender": lender.id, "borrower": borrower.id, "principal": principal})
}

func (h *VillageHandler) debtAccept(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, req VillageDebtRequest, now time.Time) error {
	l, err := tx.Debts().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || l == nil {
		return refuseVillage(village.DebtNoLoan, village.AddrDebt)
	}
	borrower, err := h.actingParty(ctx, tx, s, has, strings.TrimSpace(req.Union))
	if err != nil {
		return err
	}
	if borrower.kind != l.BorrowerKind || borrower.id != l.BorrowerID {
		return refuseVillage(village.DebtNotParty, village.AddrDebt)
	}
	if _, err := h.partyPower(ctx, tx, borrower, p, charter.DebtBorrow); err != nil {
		return err
	}
	if l.Status != "offered" {
		return refuseVillage(village.DebtState, village.AddrDebt)
	}
	if !now.Before(l.ExpiresAt) {
		return refuseVillage(village.DebtExpired, village.AddrDebt)
	}
	lender, err := h.partyOf(ctx, tx, l.LenderKind, l.LenderID)
	if err != nil {
		return err
	}
	next := now.Add(h.polity.DebtPeriod)
	if l.Replaces != "" {
		// a replacement by agreement: no money moves, the old loan is closed and linked
		old, err := tx.Debts().ByID(ctx, l.Replaces, true)
		if err != nil || old == nil || (old.Status != "active" && old.Status != "defaulted") {
			return refuseVillage(village.DebtState, village.AddrDebt)
		}
		if ok, err := tx.Debts().Accept(ctx, l.ID, p.ID, now, next); err != nil || !ok {
			if err != nil {
				return err
			}
			return refuseVillage(village.DebtState, village.AddrDebt)
		}
		if ok, err := tx.Debts().SetStatus(ctx, old.ID, old.Status, "restructured", now); err != nil || !ok {
			return err
		}
		if err := tx.Debts().Link(ctx, old.ID, l.ID); err != nil {
			return err
		}
		if err := h.schedulePolity(ctx, tx, application.TickLoanDue, application.DebtReference, l.ID, "", p.ID, now, next); err != nil {
			return err
		}
		if err := tx.Debts().AddEvent(ctx, h.ids.NewID(), old.ID, "restructured", map[string]any{"by": l.ID}, now); err != nil {
			return err
		}
		return tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, "accepted", map[string]any{"replaces": old.ID}, now)
	}
	sched, e := h.loanTerms(*l).Schedule()
	if e != nil {
		return refuseVillage(village.DebtTerms, village.AddrDebt)
	}
	balance, err := accountBalance(ctx, tx, lender.account)
	if err != nil {
		return err
	}
	_, _, outIn, _, err := h.polityLoans(ctx, tx, lender.kind, lender.id)
	if err != nil {
		return err
	}
	income, err := h.partyIncome(ctx, tx, borrower, now)
	if err != nil {
		return err
	}
	_, _, _, service, err := h.polityLoans(ctx, tx, borrower.kind, borrower.id)
	if err != nil {
		return err
	}
	if _, e := sovereign.Check(sovereign.Offer{Lender: l.LenderID, Borrower: l.BorrowerID, Terms: h.loanTerms(*l)}, h.debtBounds(), balance, outIn, income, service); e != nil {
		switch {
		case stderrors.Is(e, sovereign.ErrLender):
			return refuseVillage(village.DebtLender, village.AddrDebt)
		case stderrors.Is(e, sovereign.ErrService):
			return refuseVillage(village.DebtService, village.AddrDebt)
		}
		return refuseVillage(village.DebtTerms, village.AddrDebt)
	}
	_ = sched
	if ok, err := tx.Debts().Accept(ctx, l.ID, p.ID, now, next); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.DebtState, village.AddrDebt)
	}
	if _, err := move(ctx, tx, lender.account.ID, borrower.account.ID, l.Principal, application.ReasonSovereignDisbursement, application.DebtReference, l.ID, "", now); err != nil {
		return err
	}
	if err := h.schedulePolity(ctx, tx, application.TickLoanDue, application.DebtReference, l.ID, "", p.ID, now, next); err != nil {
		return err
	}
	if err := tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, "accepted", map[string]any{"principal": l.Principal}, now); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "debt_accepted", l.BorrowerID, map[string]any{"loan": l.ID, "principal": l.Principal})
}

// debtClose closes an offer: the borrower declines it, or the lender withdraws it.
func (h *VillageHandler) debtClose(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, req VillageDebtRequest, now time.Time, to string, asLender bool) error {
	l, err := tx.Debts().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || l == nil {
		return refuseVillage(village.DebtNoLoan, village.AddrDebt)
	}
	pt, err := h.actingParty(ctx, tx, s, has, strings.TrimSpace(req.Union))
	if err != nil {
		return err
	}
	if asLender {
		if pt.kind != l.LenderKind || pt.id != l.LenderID {
			return refuseVillage(village.DebtNotParty, village.AddrDebt)
		}
		if _, err := h.partyPower(ctx, tx, pt, p, charter.DebtLend); err != nil {
			return err
		}
	} else {
		if pt.kind != l.BorrowerKind || pt.id != l.BorrowerID {
			return refuseVillage(village.DebtNotParty, village.AddrDebt)
		}
		if _, err := h.partyPower(ctx, tx, pt, p, charter.DebtBorrow); err != nil {
			return err
		}
	}
	if ok, err := tx.Debts().SetStatus(ctx, l.ID, "offered", to, now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.DebtState, village.AddrDebt)
	}
	return tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, to, nil, now)
}

func (h *VillageHandler) debtWriteOff(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, has bool, p *application.Player, req VillageDebtRequest, now time.Time) error {
	l, err := tx.Debts().ByID(ctx, strings.TrimSpace(req.ID), true)
	if err != nil || l == nil {
		return refuseVillage(village.DebtNoLoan, village.AddrDebt)
	}
	pt, err := h.actingParty(ctx, tx, s, has, strings.TrimSpace(req.Union))
	if err != nil {
		return err
	}
	if pt.kind != l.LenderKind || pt.id != l.LenderID {
		return refuseVillage(village.DebtNotParty, village.AddrDebt)
	}
	if _, err := h.partyPower(ctx, tx, pt, p, charter.DebtRestructure); err != nil {
		return err
	}
	if l.Status != "defaulted" {
		return refuseVillage(village.DebtNotDefault, village.AddrDebt)
	}
	if ok, err := tx.Debts().SetStatus(ctx, l.ID, "defaulted", "written_off", now); err != nil {
		return err
	} else if !ok {
		return refuseVillage(village.DebtState, village.AddrDebt)
	}
	return tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, "written_off", nil, now) // a book entry: no money moves
}

func (h *VillageHandler) tickLoanOffer(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	l, err := tx.Debts().ByID(ctx, id, true)
	if err != nil || l == nil || l.Status != "offered" || now.Before(l.ExpiresAt) {
		return err
	}
	if ok, err := tx.Debts().SetStatus(ctx, l.ID, "offered", "expired", now); err != nil || !ok {
		return err
	}
	return tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, "expired", nil, now)
}

// tickLoan collects the instalment of a period from the borrower's treasury, as far as it goes, and chases arrears and fees. The row of
// each instalment is the fence: a replayed tick pays nothing twice.
func (h *VillageHandler) tickLoan(ctx context.Context, tx application.Tx, meta envelope.Metadata, id string, now time.Time) error {
	l, err := tx.Debts().ByID(ctx, id, true)
	if err != nil || l == nil || (l.Status != "active" && l.Status != "defaulted") || l.NextDueAt == nil || now.Before(*l.NextDueAt) {
		return err
	}
	lender, err := h.partyOf(ctx, tx, l.LenderKind, l.LenderID)
	if err != nil {
		return err
	}
	borrower, err := h.partyOf(ctx, tx, l.BorrowerKind, l.BorrowerID)
	if err != nil {
		return err
	}
	sched, e := h.loanTerms(*l).Schedule()
	if e != nil {
		return nil
	}
	avail, err := accountBalance(ctx, tx, borrower.account)
	if err != nil {
		return err
	}
	dueNow := int64(1)
	if l.Status == "defaulted" {
		dueNow = 0 // a called loan only chases what is owed already
	}
	c := finance.Collect(finance.LoanState{Schedule: sched, Paid: int64(l.Paid), Arrears: int64(l.Arrears), FeesDue: l.FeesDue}, dueNow, avail,
		finance.LoanRules{LateFeeBPS: h.polity.DebtLateFeeBPS, DefaultAfter: h.polity.DebtDefaultAfter})
	paid := int64(l.Paid)
	for _, in := range c.Instalments {
		txID := h.ids.NewID()
		fresh, err := tx.Debts().PayPeriod(ctx, application.SovereignPeriod{LoanID: l.ID, K: int(in.K), Principal: in.Principal, Interest: in.Interest, LedgerTx: txID, At: now})
		if err != nil {
			return err
		}
		if !fresh {
			continue
		}
		if _, err := move(ctx, tx, borrower.account.ID, lender.account.ID, in.Principal, application.ReasonSovereignRepayment, application.DebtReference, l.ID, txID, now); err != nil {
			return err
		}
		if in.Interest > 0 {
			if _, err := move(ctx, tx, borrower.account.ID, lender.account.ID, in.Interest, application.ReasonSovereignInterest, application.DebtReference, l.ID, "", now); err != nil {
				return err
			}
		}
		paid = in.K
	}
	if c.Fees > 0 {
		if _, err := move(ctx, tx, borrower.account.ID, lender.account.ID, c.Fees, application.ReasonSovereignPenalty, application.DebtReference, l.ID, "", now); err != nil {
			return err
		}
		if err := tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, "fee_paid", map[string]any{"fee": c.Fees}, now); err != nil {
			return err
		}
	}
	standing := sovereign.StandingOf(c)
	var next *time.Time
	switch standing {
	case sovereign.Repaid:
		if _, err := tx.Debts().SetStatus(ctx, l.ID, l.Status, "repaid", now); err != nil {
			return err
		}
	case sovereign.Defaulted:
		if l.Status != "defaulted" {
			if _, err := tx.Debts().SetStatus(ctx, l.ID, "active", "defaulted", now); err != nil {
				return err
			}
			if err := tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, "defaulted", map[string]any{"arrears": c.Arrears}, now); err != nil {
				return err
			}
		}
		n := now.Add(h.polity.DebtPeriod)
		next = &n
	default:
		n := now.Add(h.polity.DebtPeriod)
		next = &n
		if c.Missed {
			if err := tx.Debts().AddEvent(ctx, h.ids.NewID(), l.ID, "missed", map[string]any{"arrears": c.Arrears, "fee": c.NewFee}, now); err != nil {
				return err
			}
		}
	}
	if err := tx.Debts().Collected(ctx, l.ID, int(paid), int(c.Arrears), c.FeesDue, next); err != nil {
		return err
	}
	if next != nil {
		return h.schedulePolity(ctx, tx, application.TickLoanDue, application.DebtReference, l.ID, "", l.OfferedBy, now, *next)
	}
	return nil
}

func (h *VillageHandler) debtView(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, unionID string) (village.DebtView, error) {
	v := village.DebtView{Open: h.polityOpen(), MaxInstalments: h.polity.DebtMaxInstalments, MaxRateBPS: h.polity.DebtMaxRateBPS}
	pt, err := h.actingParty(ctx, tx, s, has, unionID)
	if err != nil {
		if has || unionID != "" {
			return v, err
		}
		return v, nil
	}
	v.Party, v.PartyKind = named(pt.id, pt.name), pt.kind
	if has {
		v.Village = s.Name
	}
	var perms [3]bool
	for i, perm := range []charter.Permission{charter.DebtLend, charter.DebtBorrow, charter.DebtRestructure} {
		_, e := h.partyPower(ctx, tx, pt, p, perm)
		perms[i] = e == nil
	}
	v.MayLend, v.MayBorrow, v.MayRestructure = perms[0], perms[1], perms[2]
	loans, owedOut, owedIn, service, err := h.polityLoans(ctx, tx, pt.kind, pt.id)
	if err != nil {
		return v, err
	}
	bal, err := accountBalance(ctx, tx, pt.account)
	if err != nil {
		return v, err
	}
	v.Lendable = finance.Lendable(bal, owedIn, 1000)
	v.Service = service
	_ = owedOut
	if v.Income, err = h.partyIncome(ctx, tx, pt, h.now()); err != nil {
		return v, err
	}
	seen := map[string]bool{}
	for _, l := range loans {
		lender, e1 := h.partyOf(ctx, tx, l.LenderKind, l.LenderID)
		borrower, e2 := h.partyOf(ctx, tx, l.BorrowerKind, l.BorrowerID)
		if e1 != nil || e2 != nil {
			continue
		}
		sched, _ := h.loanTerms(l).Schedule()
		pr, in := sched.Remaining(int64(l.Paid))
		line := village.DebtLoanLine{ID: l.ID, Lender: named(lender.id, lender.name), Borrower: named(borrower.id, borrower.name), LenderKind: l.LenderKind, BorrowerKind: l.BorrowerKind,
			Status: l.Status, Principal: l.Principal, RateBPS: l.RateBPS, Periods: l.Periods, Paid: l.Paid, Arrears: l.Arrears, Instalment: sched.Amount(int64(l.Paid) + 1),
			Owed: pr + in, FeesDue: l.FeesDue, NextDueAt: l.NextDueAt, ExpiresAt: l.ExpiresAt}
		if l.LenderKind == pt.kind && l.LenderID == pt.id {
			line.Mine = "lender"
			line.MayWithdraw = l.Status == "offered" && v.MayLend
			line.MayRestructure = (l.Status == "active" || l.Status == "defaulted") && v.MayRestructure
			line.MayWriteOff = l.Status == "defaulted" && v.MayRestructure
		} else {
			line.Mine = "borrower"
			line.MayAccept = l.Status == "offered" && v.MayBorrow && h.now().Before(l.ExpiresAt)
		}
		v.Loans = append(v.Loans, line)
		other := l.BorrowerKind + ":" + l.BorrowerID
		if line.Mine == "borrower" {
			other = l.LenderKind + ":" + l.LenderID
		}
		_ = other
		if line.Mine == "lender" && !seen[l.BorrowerID] {
			seen[l.BorrowerID] = true
			rec, err := tx.Debts().Record(ctx, l.BorrowerKind, l.BorrowerID)
			if err != nil {
				return v, err
			}
			v.Others = append(v.Others, village.DebtRecordLine{Party: line.Borrower, Taken: rec.Taken, Repaid: rec.Repaid, Late: rec.Late, Defaulted: rec.Defaulted})
		}
	}
	rec, err := tx.Debts().Record(ctx, pt.kind, pt.id)
	if err != nil {
		return v, err
	}
	v.Record = village.DebtRecordLine{Party: v.Party, Taken: rec.Taken, Repaid: rec.Repaid, Late: rec.Late, Defaulted: rec.Defaulted}
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return v, err
	}
	for _, f := range founded {
		if pt.kind != "settlement" || f.CityID != pt.id {
			v.Targets = append(v.Targets, named(f.Code, f.Name))
		}
	}
	return v, nil
}

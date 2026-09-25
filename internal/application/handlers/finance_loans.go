package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The national bank's loans (docs/adr/0026 section 2): the bank's counter
// with the player's score, what each product would lend them and the loans
// they run; a product's amounts and terms; taking a loan; a loan; paying one
// off.

// bankState is a country's bank as a loan sees it.
type bankState struct {
	country     application.Jurisdiction
	account     application.Account
	outstanding int64
	policyBPS   int64
	reserveBPS  int64
}

// lendable is what the bank may still lend.
func (b bankState) lendable() int64 {
	return finance.Lendable(b.account.Balance.Minor(), b.outstanding, b.reserveBPS)
}

// bankOf reads a country's national bank.
func (h *FinanceHandler) bankOf(ctx context.Context, tx application.Tx, country application.Jurisdiction) (bankState, error) {
	b := bankState{country: country}
	var err error
	if b.account, err = tx.Ledger().AccountFor(ctx, application.AccountNationalBank, country.ID); err != nil {
		return b, err
	}
	if b.outstanding, err = tx.Finance().Outstanding(ctx, country.ID); err != nil {
		return b, err
	}
	if b.policyBPS, err = h.lever(ctx, country.ID, LeverBaseInterestRate); err != nil {
		return b, err
	}
	b.reserveBPS, err = h.lever(ctx, country.ID, LeverBankReserveRatio)
	return b, err
}

// productKind is what a product lends against, for the screens.
func productKind(l content.LoanDef) string {
	switch {
	case l.Borrower == content.BorrowerCompany:
		return "company"
	case l.Collateral == content.CollateralProperty:
		return "mortgage"
	}
	return "player"
}

// quote is what a product would lend a player of this score, before any
// pledge: its rate and its most. Zero most: not now.
func quote(l content.LoanDef, def content.FinanceDef, score, policyBPS int64) (rate, most int64) {
	band, ok := finance.BandFor(def.CreditBands(), score)
	rate = finance.RateOf(policyBPS, l.SpreadBPS, band.PremiumBPS)
	if !ok || score < l.MinScore {
		return rate, 0
	}
	most = finance.OfBPS(l.MaxAmount, band.LimitBPS)
	most -= most % l.Step
	if most < l.MinAmount {
		return rate, 0
	}
	return rate, most
}

// pledges are what a player may borrow against with a product: their
// properties not already pledged (a mortgage), or the companies they own (a
// business loan).
func (h *FinanceHandler) pledges(ctx context.Context, tx application.Tx, snap *content.Snapshot, l content.LoanDef,
	p *application.Player, most int64,
) ([]screens.PledgeLine, error) {
	var out []screens.PledgeLine
	switch productKind(l) {
	case "mortgage":
		props, err := tx.Property().OfOwner(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for _, pr := range props {
			pledged, err := tx.Finance().PledgedProperty(ctx, pr.ID)
			if err != nil {
				return nil, err
			}
			if pledged || pr.Status != application.PropertyOwned {
				continue
			}
			line, err := h.propertyLine(ctx, snap, pr)
			if err != nil {
				return nil, err
			}
			limit := min(finance.OfBPS(pr.Value, l.LTVBPS), most)
			line.Limit = limit - limit%l.Step
			if line.Limit >= l.MinAmount {
				out = append(out, line)
			}
		}
	case "company":
		companies, err := tx.Companies().Of(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range companies {
			if c.OwnerID != p.ID {
				continue
			}
			out = append(out, screens.PledgeLine{Code: c.Code, Type: named(c.Code, c.Name), Limit: most})
		}
	}
	return out, nil
}

// propertyLine names a property for a screen.
func (h *FinanceHandler) propertyLine(ctx context.Context, snap *content.Snapshot, pr application.Property) (screens.PledgeLine, error) {
	line := screens.PledgeLine{No: pr.No, Value: pr.Value, Type: named(pr.TypeCode, pr.TypeCode)}
	if t, ok := snap.PropertyType(pr.TypeCode); ok {
		line.Type.Name = t.Name
	}
	city, err := h.cities.ByID(ctx, pr.CityID)
	if err != nil {
		return line, err
	}
	line.City = cityPlace(*city)
	return line, nil
}

// loanLine is a loan for a list.
func (h *FinanceHandler) loanLine(ctx context.Context, tx application.Tx, def content.FinanceDef, l application.Loan) (screens.LoanLine, error) {
	line := screens.LoanLine{No: l.No, Product: named(l.Product, l.Product), Status: l.Status, Next: l.Next(),
		Owed: l.Owed(), Left: l.Periods - l.PaidPeriods, Arrears: l.Arrears}
	if d, ok := def.Loan(l.Product); ok {
		line.Product.Name = d.Name
	}
	if l.CompanyID != "" {
		c, err := tx.Companies().ByID(ctx, l.CompanyID)
		if err != nil {
			return line, err
		}
		line.Company = named(c.Code, c.Name)
	}
	return line, nil
}

// Hub handles loan.hub: the national bank's counter.
func (h *FinanceHandler) Hub(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.hub(ctx, meta, "", nil)
}

func (h *FinanceHandler) hub(ctx context.Context, meta envelope.Metadata, notice string, args map[string]any) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	view := screens.FinanceHubView{Notice: notice, Args: args}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.countryOf(ctx, tx, p)
		if err != nil {
			return err
		}
		now := h.now()
		bank, err := h.bankOf(ctx, tx, country)
		if err != nil {
			return err
		}
		c, err := h.creditOf(ctx, tx, snap, def, p, now)
		if err != nil {
			return err
		}
		view.Country, view.Credit, view.PolicyBPS, view.Lendable = countryPlace(country), c.view(), bank.policyBPS,
			bank.lendable()
		for _, l := range def.Loans {
			rate, most := quote(l, def, c.score.Score, bank.policyBPS)
			most = min(most, view.Lendable)
			if most < l.MinAmount {
				most = 0
			}
			view.Products = append(view.Products, screens.LoanProductLine{Product: named(l.Code, l.Name),
				Kind: productKind(l), RateBPS: rate, Limit: most, MinScore: l.MinScore})
		}
		loans, err := tx.Finance().LoansOf(ctx, p.ID, 8)
		if err != nil {
			return err
		}
		for _, l := range loans {
			line, err := h.loanLine(ctx, tx, def, l)
			if err != nil {
				return err
			}
			view.Loans = append(view.Loans, line)
		}
		if len(loans) > 0 {
			view.NextAt = nextAt(ctx, tx)
		}
		savings, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerSavings, p.ID)
		if err != nil {
			return err
		}
		view.Savings = savings.Balance.Minor()
		view.SavingsBPS = max(bank.policyBPS-def.Savings.SpreadBPS, 0)
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.FinanceHub(h.screen(meta, lang), view), nil
}

// firstDue is when a loan taken now pays its first instalment: the end of
// the next full period.
func firstDue(next time.Time, wait time.Duration, now time.Time) time.Time {
	if next.IsZero() {
		return now.Add(2 * wait)
	}
	return next.Add(wait)
}

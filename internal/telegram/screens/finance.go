package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Finance (docs/adr/0026-finance.md): the national bank — the player's
// credit score, the loans it offers and the ones they run, savings — and
// insurance. The stock exchange is stocks.go, the gold dealer gold.go.

// Addresses of the bank, savings and insurance screens.
const (
	AddrLoanHub         = "loan:hub"
	AddrLoanOffer       = "loan:offer"
	AddrLoanTake        = "loan:take"
	AddrLoanView        = "loan:view"
	AddrLoanRepay       = "loan:repay"
	AddrSavings         = "save:show"
	AddrSavingsDeposit  = "save:deposit"
	AddrSavingsWithdraw = "save:withdraw"
	AddrInsurance       = "insure:list"
	AddrInsureBuy       = "insure:buy"
	AddrInsureCancel    = "insure:cancel"
)

// Commands a typed amount fills (configs/commands.yml, input).
const (
	CommandSavingsDeposit  = "save.deposit"
	CommandSavingsWithdraw = "save.withdraw"
)

// FinanceYes confirms a cancellation.
const FinanceYes = "yes"

// LoanProductName names a loan product.
func (c Context) LoanProductName(n Named) string { return c.named("loan_product."+n.Code, n.Name) }

// InsuranceProductName names an insurance product.
func (c Context) InsuranceProductName(n Named) string {
	return c.named("insurance_product."+n.Code, n.Name)
}

// CreditView is a credit score and what it is made of.
type CreditView struct {
	Score, Min, Max int64
	// Factors, 0..10000 each.
	PaymentBPS, DebtBPS, HistoryBPS, IncomeBPS, WorthBPS, NewCreditBPS int64
	// Missed and Defaults are what the record remembers.
	Missed, Defaults int64
}

// band is the word for a score: poor, fair, good, very good, excellent.
func (v CreditView) band() string {
	span := max(v.Max-v.Min, 1)
	switch at := (v.Score - v.Min) * 100 / span; {
	case at < 30:
		return "poor"
	case at < 50:
		return "fair"
	case at < 70:
		return "good"
	case at < 85:
		return "very_good"
	default:
		return "excellent"
	}
}

// creditLines are the score, its band and its factors.
func creditLines(c Context, v CreditView, factors bool) string {
	lines := []string{c.T("finance.credit.score", map[string]any{"score": FormatNumber(c, v.Score),
		"max": FormatNumber(c, v.Max), "band": c.T("finance.credit.band."+v.band(), nil)})}
	if factors {
		pct := func(bps int64) string { return PercentFromBPS(c, int(bps)) }
		lines = append(lines, c.T("finance.credit.factors", map[string]any{"payment": pct(v.PaymentBPS),
			"debt": pct(v.DebtBPS), "history": pct(v.HistoryBPS), "income": pct(v.IncomeBPS)}))
		if v.Missed > 0 || v.Defaults > 0 {
			lines = append(lines, c.T("finance.credit.record", map[string]any{"missed": FormatNumber(c, v.Missed),
				"defaults": FormatNumber(c, v.Defaults)}))
		}
	}
	return body(lines...)
}

// LoanProductLine is a loan product as the bank offers it to this player.
type LoanProductLine struct {
	Product Named
	// Kind is player, mortgage or company: who borrows and against what.
	Kind string
	// RateBPS is the yearly rate this player would pay; Limit the most
	// they may borrow (0: not now); MinScore what it asks.
	RateBPS  int64
	Limit    int64
	MinScore int64
}

// LoanLine is one of the player's loans.
type LoanLine struct {
	No      int64
	Product Named
	// Company names the company that borrowed, for a business loan.
	Company Named
	Status  string
	// Next is the next instalment, Left the instalments still to pay,
	// Owed everything still owed, Arrears the instalments overdue.
	Next, Owed    int64
	Left, Arrears int64
}

// FinanceHubView is the national bank.
type FinanceHubView struct {
	Country    GovPlace
	Credit     CreditView
	PolicyBPS  int64
	Products   []LoanProductLine
	Loans      []LoanLine
	Savings    int64
	SavingsBPS int64
	// Lendable is what the bank may still lend, all borrowers together.
	Lendable int64
	// NextAt is when the next instalments fall due.
	NextAt time.Time
	Notice string
	Args   map[string]any
}

// FinanceHub renders the national bank.
func FinanceHub(c Context, v FinanceHubView) *presenter.Response {
	var notice string
	if v.Notice != "" {
		notice = c.T("finance.notice."+v.Notice, moneyArgs(c, v.Args))
	}
	head := body(
		c.T("finance.hub.title", map[string]any{"country": c.PlaceName(v.Country)}),
		c.T("finance.hub.rate", map[string]any{"rate": PercentFromBPS(c, int(v.PolicyBPS))}),
	)
	var offers []string
	offers = append(offers, c.T("finance.hub.offers", nil))
	for _, p := range v.Products {
		args := map[string]any{"product": c.LoanProductName(p.Product), "rate": PercentFromBPS(c, int(p.RateBPS)),
			"limit": FormatMoney(c, p.Limit), "score": FormatNumber(c, p.MinScore)}
		key := "finance.hub.offer"
		if p.Limit <= 0 {
			key = "finance.hub.offer_closed"
		}
		offers = append(offers, c.T(key, args))
	}
	if v.Lendable <= 0 {
		offers = append(offers, c.T("finance.hub.bank_dry", nil))
	}
	var loans []string
	if len(v.Loans) > 0 {
		loans = append(loans, c.T("finance.hub.loans", nil))
		for _, l := range v.Loans {
			loans = append(loans, loanLine(c, l))
		}
		if !v.NextAt.IsZero() {
			loans = append(loans, c.T("finance.hub.next_due", map[string]any{"time": FormatClock(c, v.NextAt)}))
		}
	}
	savings := c.T("finance.hub.savings", map[string]any{"amount": FormatMoney(c, v.Savings),
		"rate": PercentFromBPS(c, int(v.SavingsBPS))})
	text := paragraphs(notice, head, creditLines(c, v.Credit, true), body(offers...), body(loans...), savings)

	kb := keyboards.New()
	var row []presenter.Button
	for _, p := range v.Products {
		if p.Limit <= 0 {
			continue
		}
		if btn, ok := keyboards.Button(c.T("finance.button.product", map[string]any{"product": c.LoanProductName(p.Product)}),
			AddrLoanOffer, p.Product.Code); ok {
			row = append(row, btn)
		}
	}
	kb.Grid(2, row...)
	for _, l := range v.Loans {
		if l.Status == "active" {
			kb.Add(c.T("finance.button.loan", map[string]any{"no": FormatNumber(c, l.No),
				"product": c.LoanProductName(l.Product)}), AddrLoanView, strconv.FormatInt(l.No, 10))
		}
	}
	savingsBtn, _ := keyboards.Button(c.T("finance.button.savings", nil), AddrSavings)
	insurance, _ := keyboards.Button(c.T("finance.button.insurance", nil), AddrInsurance)
	kb.Row(savingsBtn, insurance)
	exchange, _ := keyboards.Button(c.T("finance.button.exchange", nil), AddrExchange)
	gold, _ := keyboards.Button(c.T("finance.button.gold", nil), AddrGold)
	kb.Row(exchange, gold)
	kb.Add(c.T("finance.button.portfolio", nil), AddrPortfolio)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBank, RefreshData: AddrLoanHub}))
	return c.respond(text, kb.Build()).MarkPrivate()
}

// loanLine is one loan in a list.
func loanLine(c Context, l LoanLine) string {
	args := map[string]any{"no": FormatNumber(c, l.No), "product": c.LoanProductName(l.Product),
		"next": FormatMoney(c, l.Next), "left": FormatNumber(c, l.Left), "owed": FormatMoney(c, l.Owed),
		"arrears": FormatNumber(c, l.Arrears), "company": c.named("company_name."+l.Company.Code, l.Company.Name)}
	switch {
	case l.Status != "active":
		return c.T("finance.loan.line_"+l.Status, args)
	case l.Arrears > 0:
		return c.T("finance.loan.line_arrears", args)
	case l.Company.Code != "":
		return c.T("finance.loan.line_company", args)
	}
	return c.T("finance.loan.line", args)
}

// LoanOption is one amount and term the bank would lend, with what it
// costs.
type LoanOption struct {
	Amount     int64
	Term       int64
	Instalment int64
}

// PledgeLine is a property that may secure a mortgage, or a company that
// may borrow.
type PledgeLine struct {
	No    int64
	Code  string
	Type  Named
	City  GovPlace
	Value int64
	// Limit is the most it secures.
	Limit int64
}

// LoanOfferView is one product: the amounts and terms offered.
type LoanOfferView struct {
	Product Named
	Kind    string
	RateBPS int64
	Limit   int64
	Terms   []int64
	Options []LoanOption
	// Pledges are the properties (mortgage) or companies (business loan)
	// to choose from; Pledge the one chosen.
	Pledges []PledgeLine
	Pledge  *PledgeLine
	// LateFeeBPS and DefaultAfter are what missing instalments costs.
	LateFeeBPS   int64
	DefaultAfter int64
}

// pledgeArg is the argument a pledge travels as: a property's number, a
// company's code.
func pledgeArg(p *PledgeLine) string {
	if p == nil {
		return "0"
	}
	if p.Code != "" {
		return p.Code
	}
	return strconv.FormatInt(p.No, 10)
}

// pledgeName is a pledge as a line names it.
func (c Context) pledgeName(p PledgeLine) string {
	if p.Code != "" {
		return c.T("finance.offer.company", map[string]any{"company": c.named("company_name."+p.Code, p.Type.Name)})
	}
	return c.T("finance.offer.property", map[string]any{"no": FormatNumber(c, p.No),
		"type": c.PropertyTypeName(p.Type), "city": c.PlaceName(p.City), "value": FormatMoney(c, p.Value)})
}

// LoanOffer renders a product's offer.
func LoanOffer(c Context, v LoanOfferView) *presenter.Response {
	lines := []string{
		c.T("finance.offer.title", map[string]any{"product": c.LoanProductName(v.Product)}),
		c.T("finance.offer.kind."+v.Kind, nil),
		c.T("finance.offer.rate", map[string]any{"rate": PercentFromBPS(c, int(v.RateBPS))}),
		c.T("finance.offer.rules", map[string]any{"fee": PercentFromBPS(c, int(v.LateFeeBPS)),
			"after": FormatNumber(c, v.DefaultAfter)}),
	}
	kb := keyboards.New()
	switch {
	case len(v.Pledges) > 0 && v.Pledge == nil:
		lines = append(lines, c.T("finance.offer.choose_pledge."+v.Kind, nil))
		for _, p := range v.Pledges {
			lines = append(lines, "▫️ "+c.pledgeName(p))
			kb.Add(c.T("finance.button.pledge", map[string]any{"pledge": c.pledgeName(p)}), AddrLoanOffer,
				v.Product.Code, pledgeArg(&p))
		}
	case len(v.Options) == 0:
		lines = append(lines, c.T("finance.offer.nothing", nil))
	default:
		if v.Pledge != nil {
			lines = append(lines, c.T("finance.offer.pledged", map[string]any{"pledge": c.pledgeName(*v.Pledge)}))
		}
		lines = append(lines, c.T("finance.offer.limit", map[string]any{"limit": FormatMoney(c, v.Limit)}))
		var buttons []presenter.Button
		for _, o := range v.Options {
			label := c.T("finance.button.option", map[string]any{"amount": FormatMoney(c, o.Amount),
				"term": FormatNumber(c, o.Term), "instalment": FormatMoney(c, o.Instalment)})
			if btn, ok := keyboards.Button(label, AddrLoanTake, v.Product.Code, strconv.FormatInt(o.Amount, 10),
				strconv.FormatInt(o.Term, 10), pledgeArg(v.Pledge)); ok {
				buttons = append(buttons, btn)
			}
		}
		kb.Grid(1, buttons...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLoanHub}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// LoanConfirmView is a loan about to be taken.
type LoanConfirmView struct {
	Product    Named
	Amount     int64
	Term       int64
	RateBPS    int64
	Interest   int64
	Instalment int64
	Total      int64
	// FirstAt is when the first instalment falls due.
	FirstAt time.Time
	Pledge  *PledgeLine
	Nonce   string
}

// LoanConfirm renders the terms of a loan with the button that takes it.
func LoanConfirm(c Context, v LoanConfirmView) *presenter.Response {
	lines := []string{
		c.T("finance.confirm.title", map[string]any{"product": c.LoanProductName(v.Product)}),
		c.T("finance.confirm.terms", map[string]any{"amount": FormatMoney(c, v.Amount), "term": FormatNumber(c, v.Term),
			"rate": PercentFromBPS(c, int(v.RateBPS))}),
		c.T("finance.confirm.cost", map[string]any{"interest": FormatMoney(c, v.Interest),
			"instalment": FormatMoney(c, v.Instalment), "total": FormatMoney(c, v.Total)}),
		c.T("finance.confirm.first", map[string]any{"time": FormatClock(c, v.FirstAt)}),
	}
	if v.Pledge != nil {
		lines = append(lines, c.T("finance.offer.pledged", map[string]any{"pledge": c.pledgeName(*v.Pledge)}))
	}
	lines = append(lines, c.T("finance.confirm.warning", nil))
	kb := keyboards.New()
	kb.Add(c.T("finance.button.take", map[string]any{"amount": FormatMoney(c, v.Amount)}), AddrLoanTake, v.Product.Code,
		strconv.FormatInt(v.Amount, 10), strconv.FormatInt(v.Term, 10), pledgeArg(v.Pledge), v.Nonce)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrLoanOffer, v.Product.Code)}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// LoanDetailView is one loan.
type LoanDetailView struct {
	Loan        LoanLine
	Principal   int64
	Interest    int64
	RateBPS     int64
	Periods     int64
	Paid        int64
	FeesDue     int64
	Payoff      int64
	Pledge      *PledgeLine
	OpenedAt    time.Time
	NextAt      time.Time
	Missed      int64
	Recovered   int64
	WrittenOff  int64
	Nonce       string
	Notice      string
	NoticeArgs  map[string]any
	ConfirmOpen bool
}

// LoanDetail renders one loan: its terms, what is paid and owed, and the
// payoff.
func LoanDetail(c Context, v LoanDetailView) *presenter.Response {
	var notice string
	if v.Notice != "" {
		notice = c.T("finance.notice."+v.Notice, moneyArgs(c, v.NoticeArgs))
	}
	l := v.Loan
	lines := []string{
		c.T("finance.loan.title", map[string]any{"no": FormatNumber(c, l.No), "product": c.LoanProductName(l.Product)}),
		c.T("finance.loan.terms", map[string]any{"principal": FormatMoney(c, v.Principal),
			"interest": FormatMoney(c, v.Interest), "rate": PercentFromBPS(c, int(v.RateBPS)),
			"periods": FormatNumber(c, v.Periods)}),
		c.T("finance.loan.opened", map[string]any{"date": FormatDate(c, v.OpenedAt)}),
		c.T("finance.loan.progress", map[string]any{"paid": FormatNumber(c, v.Paid), "periods": FormatNumber(c, v.Periods),
			"missed": FormatNumber(c, v.Missed)}),
	}
	if v.Pledge != nil {
		lines = append(lines, c.T("finance.offer.pledged", map[string]any{"pledge": c.pledgeName(*v.Pledge)}))
	}
	switch l.Status {
	case "active":
		lines = append(lines, c.T("finance.loan.owed", map[string]any{"owed": FormatMoney(c, l.Owed),
			"next": FormatMoney(c, l.Next)}))
		if l.Arrears > 0 || v.FeesDue > 0 {
			lines = append(lines, c.T("finance.loan.arrears", map[string]any{"arrears": FormatNumber(c, l.Arrears),
				"fees": FormatMoney(c, v.FeesDue)}))
		}
		if !v.NextAt.IsZero() {
			lines = append(lines, c.T("finance.hub.next_due", map[string]any{"time": FormatClock(c, v.NextAt)}))
		}
	case "defaulted":
		lines = append(lines, c.T("finance.loan.defaulted", map[string]any{"recovered": FormatMoney(c, v.Recovered),
			"written_off": FormatMoney(c, v.WrittenOff)}))
	default:
		lines = append(lines, c.T("finance.loan.repaid", nil))
	}
	kb := keyboards.New()
	if l.Status == "active" && v.Payoff > 0 {
		if v.ConfirmOpen {
			lines = append(lines, c.T("finance.loan.payoff_confirm", map[string]any{"amount": FormatMoney(c, v.Payoff)}))
			kb.Add(c.T("finance.button.payoff_yes", map[string]any{"amount": FormatMoney(c, v.Payoff)}), AddrLoanRepay,
				strconv.FormatInt(l.No, 10), v.Nonce)
		} else {
			kb.Add(c.T("finance.button.payoff", map[string]any{"amount": FormatMoney(c, v.Payoff)}), AddrLoanRepay,
				strconv.FormatInt(l.No, 10))
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLoanHub, RefreshData: keyboards.Data(AddrLoanView, strconv.FormatInt(l.No, 10))}))
	return c.respond(paragraphs(notice, body(lines...)), kb.Build()).MarkPrivate()
}

// SavingsView is a savings account.
type SavingsView struct {
	Balance int64
	RateBPS int64
	// Earned is all the interest it earned; Next what the next period
	// pays on what it holds now (counted from the last period's balance).
	Earned, Next int64
	Bank         int64
	// Deposits and Withdrawals are the amounts offered.
	Deposits, Withdrawals []int64
	NextAt                time.Time
	Notice                string
	NoticeArgs            map[string]any
}

// Savings renders a savings account.
func Savings(c Context, v SavingsView) *presenter.Response {
	var notice string
	if v.Notice != "" {
		notice = c.T("finance.notice."+v.Notice, moneyArgs(c, v.NoticeArgs))
	}
	lines := []string{
		c.T("finance.savings.title", nil),
		c.T("finance.savings.balance", map[string]any{"amount": FormatMoney(c, v.Balance),
			"rate": PercentFromBPS(c, int(v.RateBPS))}),
		c.T("finance.savings.earned", map[string]any{"earned": FormatMoney(c, v.Earned), "next": FormatMoney(c, v.Next)}),
		c.T("finance.savings.bank", map[string]any{"bank": FormatMoney(c, v.Bank)}),
		c.T("finance.savings.hint", nil),
	}
	if !v.NextAt.IsZero() {
		lines = append(lines, c.T("finance.savings.next", map[string]any{"time": FormatClock(c, v.NextAt)}))
	}
	kb := keyboards.New()
	var in, out []presenter.Button
	for _, a := range v.Deposits {
		if btn, ok := keyboards.Button(c.T("finance.button.deposit", map[string]any{"amount": FormatMoney(c, a)}),
			AddrSavingsDeposit, strconv.FormatInt(a, 10)); ok {
			in = append(in, btn)
		}
	}
	for _, a := range v.Withdrawals {
		if btn, ok := keyboards.Button(c.T("finance.button.withdraw", map[string]any{"amount": FormatMoney(c, a)}),
			AddrSavingsWithdraw, strconv.FormatInt(a, 10)); ok {
			out = append(out, btn)
		}
	}
	kb.Grid(3, in...)
	kb.Grid(3, out...)
	typedIn, _ := keyboards.Button(c.T("finance.button.deposit_custom", nil), AddrAsk, CommandSavingsDeposit)
	typedOut, _ := keyboards.Button(c.T("finance.button.withdraw_custom", nil), AddrAsk, CommandSavingsWithdraw)
	kb.Row(typedIn, typedOut)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLoanHub, RefreshData: AddrSavings}))
	return c.respond(paragraphs(notice, body(lines...)), kb.Build()).MarkPrivate()
}

// InsuranceProductLine is an insurance product as it is offered.
type InsuranceProductLine struct {
	Product  Named
	Covers   string
	Premium  int64
	PremBPS  int64
	CoverBPS int64
	MaxClaim int64
	// Waiting is the real time before it pays a claim.
	Waiting time.Duration
	// Targets are the properties it may insure (a property's policy); a
	// product with none to insure offers nothing.
	Targets []PledgeLine
	// Held says the player holds it already (a health policy).
	Held bool
}

// PolicyLine is one of the player's policies.
type PolicyLine struct {
	No        int64
	Product   Named
	Covers    string
	Property  *PledgeLine
	Status    string
	Reason    string
	Premium   int64
	Paid      int64
	From      time.Time
	Started   time.Time
	Claimable bool
}

// InsuranceView is the insurance fund's counter.
type InsuranceView struct {
	Country    GovPlace
	Fund       int64
	Products   []InsuranceProductLine
	Policies   []PolicyLine
	Notice     string
	NoticeArgs map[string]any
	// Cancel is the policy a cancellation asks about.
	Cancel *PolicyLine
}

// Insurance renders the insurance counter: products and the player's
// policies.
func Insurance(c Context, v InsuranceView) *presenter.Response {
	var notice string
	if v.Notice != "" {
		notice = c.T("finance.notice."+v.Notice, moneyArgs(c, v.NoticeArgs))
	}
	head := body(c.T("finance.insurance.title", map[string]any{"country": c.PlaceName(v.Country)}),
		c.T("finance.insurance.fund", map[string]any{"fund": FormatMoney(c, v.Fund)}))
	var products []string
	kb := keyboards.New()
	for _, p := range v.Products {
		args := map[string]any{"product": c.InsuranceProductName(p.Product), "premium": FormatMoney(c, p.Premium),
			"bps": PercentFromBPS(c, int(p.PremBPS)), "cover": PercentFromBPS(c, int(p.CoverBPS)),
			"max": FormatMoney(c, p.MaxClaim), "wait": FormatDuration(c, p.Waiting)}
		key := "finance.insurance.product." + p.Covers
		if p.PremBPS > 0 {
			key += "_valued"
		}
		products = append(products, c.T(key, args))
		switch {
		case p.Covers == "war_damage":
			for _, t := range p.Targets {
				kb.Add(c.T("finance.button.insure_property", map[string]any{"product": c.InsuranceProductName(p.Product),
					"pledge": c.pledgeName(t)}), AddrInsureBuy, p.Product.Code, strconv.FormatInt(t.No, 10))
			}
		case !p.Held:
			kb.Add(c.T("finance.button.insure", map[string]any{"product": c.InsuranceProductName(p.Product)}),
				AddrInsureBuy, p.Product.Code, "0")
		}
	}
	var policies []string
	if len(v.Policies) > 0 {
		policies = append(policies, c.T("finance.insurance.mine", nil))
		for _, p := range v.Policies {
			policies = append(policies, policyLine(c, p))
			if p.Status == "active" && v.Cancel == nil {
				kb.Add(c.T("finance.button.cancel_policy", map[string]any{"no": FormatNumber(c, p.No)}), AddrInsureCancel,
					strconv.FormatInt(p.No, 10))
			}
		}
	}
	var confirm string
	if v.Cancel != nil {
		confirm = c.T("finance.insurance.cancel_confirm", map[string]any{"no": FormatNumber(c, v.Cancel.No),
			"product": c.InsuranceProductName(v.Cancel.Product)})
		kb.Add(c.T("finance.button.cancel_yes", nil), AddrInsureCancel, strconv.FormatInt(v.Cancel.No, 10), FinanceYes)
	}
	text := paragraphs(notice, head, body(products...), body(policies...), confirm, c.T("finance.insurance.hint", nil))
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrLoanHub, RefreshData: AddrInsurance}))
	return c.respond(text, kb.Build()).MarkPrivate()
}

// policyLine is one policy in a list.
func policyLine(c Context, p PolicyLine) string {
	args := map[string]any{"no": FormatNumber(c, p.No), "product": c.InsuranceProductName(p.Product),
		"premium": FormatMoney(c, p.Premium), "paid": FormatMoney(c, p.Paid), "time": FormatClock(c, p.From),
		"date": FormatDate(c, p.Started)}
	if p.Property != nil {
		args["pledge"] = c.pledgeName(*p.Property)
	}
	switch {
	case p.Status != "active":
		return c.T("finance.insurance.policy_ended."+p.Reason, args)
	case !p.Claimable:
		return c.T("finance.insurance.policy_waiting", args)
	case p.Property != nil:
		return c.T("finance.insurance.policy_property", args)
	}
	return c.T("finance.insurance.policy", args)
}

// InsureConfirmView is a policy about to be bought: its first premium.
type InsureConfirmView struct {
	Product  Named
	Covers   string
	Property *PledgeLine
	Premium  int64
	CoverBPS int64
	MaxClaim int64
	Waiting  time.Duration
	Payment  PaymentChoice
}

// InsureConfirm renders a policy's price with the ways to pay it.
func InsureConfirm(c Context, v InsureConfirmView) *presenter.Response {
	lines := []string{
		c.T("finance.insure.title", map[string]any{"product": c.InsuranceProductName(v.Product)}),
		c.T("finance.insure.terms", map[string]any{"premium": FormatMoney(c, v.Premium),
			"cover": PercentFromBPS(c, int(v.CoverBPS)), "max": FormatMoney(c, v.MaxClaim),
			"wait": FormatDuration(c, v.Waiting)}),
	}
	prop := "0"
	if v.Property != nil {
		lines = append(lines, c.T("finance.insure.property", map[string]any{"pledge": c.pledgeName(*v.Property)}))
		prop = strconv.FormatInt(v.Property.No, 10)
	}
	lines = append(lines, c.T("finance.insure.every_period", nil))
	kb := keyboards.New()
	c.paymentButtons(kb, v.Payment, func(m string) []string {
		return []string{AddrInsureBuy, v.Product.Code, prop, m}
	})
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrInsurance}))
	return c.respond(paragraphs(body(lines...), c.paymentNote(v.Payment)), kb.Build()).MarkPrivate()
}

// Refusals of finance.
const (
	FinanceRefusedNoBank      = "no_bank"
	FinanceRefusedScore       = "score"
	FinanceRefusedTooMany     = "too_many"
	FinanceRefusedBankDry     = "bank_dry"
	FinanceRefusedAmount      = "amount"
	FinanceRefusedPledge      = "pledge"
	FinanceRefusedNotOwner    = "not_owner"
	FinanceRefusedShort       = "short"
	FinanceRefusedSavingsCap  = "savings_cap"
	FinanceRefusedNoProduct   = "no_product"
	FinanceRefusedHeld        = "held"
	FinanceRefusedPledged     = "pledged"
	FinanceRefusedNotListed   = "not_listed"
	FinanceRefusedListed      = "listed"
	FinanceRefusedTooYoung    = "too_young"
	FinanceRefusedTooSmall    = "too_small"
	FinanceRefusedInDebt      = "in_debt"
	FinanceRefusedFloat       = "bad_float"
	FinanceRefusedNoShares    = "no_shares"
	FinanceRefusedNoMoney     = "no_money"
	FinanceRefusedTooManyOrd  = "too_many_orders"
	FinanceRefusedOrder       = "order"
	FinanceRefusedGoldStock   = "gold_stock"
	FinanceRefusedGoldHeld    = "gold_held"
	FinanceRefusedNotYours    = "not_yours"
	FinanceRefusedOwnCompany  = "own_company"
	FinanceRefusedFundClosed  = "fund_closed"
	FinanceRefusedNoPlayerFor = "no_player"
)

// FinanceRefusalView is a refused finance request.
type FinanceRefusalView struct {
	Kind   string
	Amount int64
	Score  int64
	Count  int64
	Wait   time.Duration
	// Back is where the way back leads.
	Back []string
}

// FinanceRefusal renders a refusal.
func FinanceRefusal(c Context, v FinanceRefusalView) *presenter.Response {
	text := c.T("finance.refused."+v.Kind, map[string]any{"amount": FormatMoney(c, v.Amount),
		"score": FormatNumber(c, v.Score), "count": FormatNumber(c, v.Count), "wait": FormatDuration(c, v.Wait)})
	kb := keyboards.New()
	back := AddrLoanHub
	if len(v.Back) > 0 {
		back = keyboards.Data(v.Back...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(text, kb.Build()).MarkPrivate()
}

// FinanceNoticeView is a notice of the bank or the insurance fund.
type FinanceNoticeView struct {
	// Kind: due, missed, defaulted, repaid, claimed, lapsed, gone.
	Kind    string
	No      int64
	Product Named
	Amount  int64
	Other   int64
	Count   int64
	At      time.Time
	// Pledge is the property a default took, or a claim was on.
	Pledge *PledgeLine
}

// FinanceNotice renders a notice of the bank or the insurance fund.
func FinanceNotice(c Context, v FinanceNoticeView) *presenter.Response {
	args := map[string]any{"no": FormatNumber(c, v.No), "amount": FormatMoney(c, v.Amount),
		"other": FormatMoney(c, v.Other), "count": FormatNumber(c, v.Count), "time": FormatClock(c, v.At)}
	switch v.Kind {
	case "claimed", "lapsed", "gone":
		args["product"] = c.InsuranceProductName(v.Product)
	default:
		args["product"] = c.LoanProductName(v.Product)
	}
	lines := []string{c.T("finance.event."+v.Kind, args)}
	if v.Pledge != nil {
		args["pledge"] = c.pledgeName(*v.Pledge)
		lines = append(lines, c.T("finance.event.pledge_"+v.Kind, args))
	}
	kb := keyboards.New()
	switch v.Kind {
	case "claimed", "lapsed", "gone":
		kb.Add(c.T("finance.button.insurance", nil), AddrInsurance)
	default:
		if v.Kind != "defaulted" && v.Kind != "repaid" {
			kb.Add(c.T("finance.button.loan", map[string]any{"no": FormatNumber(c, v.No), "product": args["product"]}),
				AddrLoanView, strconv.FormatInt(v.No, 10))
		}
		kb.Add(c.T("finance.button.bank", nil), AddrLoanHub)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// moneyArgs are a notice's arguments with its sums written as money: the
// handler hands over minor units, the screen words them.
func moneyArgs(c Context, args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		switch k {
		case "amount", "total", "price":
			if n, ok := v.(int64); ok {
				out[k] = FormatMoney(c, n)
				continue
			}
		}
		out[k] = v
	}
	return out
}

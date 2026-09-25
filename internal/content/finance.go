package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/finance"
)

// This file holds the game's finance (configs/content/finance.yml;
// docs/adr/0026-finance.md): the finance period on the game clock, the credit
// score and the bands it unlocks, the loan products of the national banks,
// savings, the insurance products, the stock exchange's listing rules, the
// gold dealer, and the (disabled) hook of a future TON exchange. The rates
// themselves are levers held by offices (governance.yml:
// country.base_interest_rate, country.bank_reserve_ratio,
// country.bank_funding); the rules are internal/domain/finance.

// ErrInvalidFinanceContent means finance.yml is unusable.
var ErrInvalidFinanceContent = errors.New("content: invalid finance content")

// Who borrows a loan product.
const (
	BorrowerPlayer  = "player"
	BorrowerCompany = "company"
)

// What secures a loan product.
const (
	CollateralNone     = ""
	CollateralProperty = "property"
)

// What an insurance product covers: a closed set, each an event the code
// pays a claim on.
const (
	CoverHospital  = "hospital"
	CoverWarDamage = "war_damage"
)

// FinanceDef is finance.yml's finance section.
type FinanceDef struct {
	// Period is the finance period, GAME time: instalments, premiums,
	// interest and the gold price move once per period.
	Period string `yaml:"period" json:"period"`
	// PeriodsPerYear is how many finance periods make the year every rate
	// is written per.
	PeriodsPerYear int64          `yaml:"periods_per_year" json:"periods_per_year"`
	Bank           BankDef        `yaml:"bank" json:"bank"`
	Credit         CreditDef      `yaml:"credit" json:"credit"`
	Bands          []BandDef      `yaml:"bands" json:"bands"`
	Loans          []LoanDef      `yaml:"loans" json:"loans"`
	Savings        SavingsDef     `yaml:"savings" json:"savings"`
	Insurance      []InsuranceDef `yaml:"insurance" json:"insurance"`
	Stocks         StocksDef      `yaml:"stocks" json:"stocks"`
	Gold           GoldDef        `yaml:"gold" json:"gold"`
	TON            TONExchangeDef `yaml:"ton_exchange" json:"ton_exchange"`
}

// BankDef is how the national banks are run.
type BankDef struct {
	// CapitalCap is the most a national bank holds before the national
	// treasury stops funding it (country.bank_funding).
	CapitalCap int64 `yaml:"capital_cap" json:"capital_cap"`
	// RecoveryBPS of a repossessed property's value is paid back to the
	// bank by the city that takes it, as far as its treasury goes.
	RecoveryBPS int64 `yaml:"recovery_bps" json:"recovery_bps"`
	// MaxActiveLoans a player may have running at once, business loans of
	// their companies included.
	MaxActiveLoans int `yaml:"max_active_loans" json:"max_active_loans"`
	// OfferBPS are the amounts a product's screen offers, as shares of the
	// most the player may borrow (rounded down to the product's step).
	OfferBPS []int64 `yaml:"offer_bps" json:"offer_bps"`
}

// CreditDef is the credit score's content (finance.CreditRules).
type CreditDef struct {
	Min               int64            `yaml:"min" json:"min"`
	Max               int64            `yaml:"max" json:"max"`
	Weights           CreditWeightsDef `yaml:"weights" json:"weights"`
	NeutralPaymentBPS int64            `yaml:"neutral_payment_bps" json:"neutral_payment_bps"`
	// HistoryFull, IncomeWindow, NewCreditWindow and Memory are GAME time.
	HistoryFull      string `yaml:"history_full" json:"history_full"`
	IncomeWindow     string `yaml:"income_window" json:"income_window"`
	IncomeFull       int64  `yaml:"income_full" json:"income_full"`
	WorthFull        int64  `yaml:"worth_full" json:"worth_full"`
	NewCreditWindow  string `yaml:"new_credit_window" json:"new_credit_window"`
	NewCreditStepBPS int64  `yaml:"new_credit_step_bps" json:"new_credit_step_bps"`
	Memory           string `yaml:"memory" json:"memory"`
	MissedPoints     int64  `yaml:"missed_points" json:"missed_points"`
	DefaultPoints    int64  `yaml:"default_points" json:"default_points"`
}

// CreditWeightsDef are the factors' weights, bps summing to 10000.
type CreditWeightsDef struct {
	Payment   int64 `yaml:"payment" json:"payment"`
	Debt      int64 `yaml:"debt" json:"debt"`
	History   int64 `yaml:"history" json:"history"`
	Income    int64 `yaml:"income" json:"income"`
	Worth     int64 `yaml:"worth" json:"worth"`
	NewCredit int64 `yaml:"new_credit" json:"new_credit"`
}

// BandDef is one band of scores.
type BandDef struct {
	MinScore   int64 `yaml:"min_score" json:"min_score"`
	LimitBPS   int64 `yaml:"limit_bps" json:"limit_bps"`
	PremiumBPS int64 `yaml:"premium_bps" json:"premium_bps"`
}

// LoanDef is one loan product.
type LoanDef struct {
	Code string `yaml:"code" json:"code"`
	Name string `yaml:"name" json:"name"`
	// Borrower is player or company; Collateral "" or property, with the
	// most lent against it LTVBPS of its value.
	Borrower   string `yaml:"borrower" json:"borrower"`
	Collateral string `yaml:"collateral,omitempty" json:"collateral,omitempty"`
	LTVBPS     int64  `yaml:"ltv_bps,omitempty" json:"ltv_bps,omitempty"`
	// SpreadBPS is added to the policy rate, yearly.
	SpreadBPS int64 `yaml:"spread_bps" json:"spread_bps"`
	// MinAmount..MaxAmount bound one loan; amounts are offered in Step.
	MinAmount int64 `yaml:"min_amount" json:"min_amount"`
	MaxAmount int64 `yaml:"max_amount" json:"max_amount"`
	Step      int64 `yaml:"step" json:"step"`
	// Terms are the terms offered, finance periods.
	Terms []int64 `yaml:"terms" json:"terms"`
	// MinScore is the least score the product lends at.
	MinScore     int64 `yaml:"min_score" json:"min_score"`
	LateFeeBPS   int64 `yaml:"late_fee_bps" json:"late_fee_bps"`
	DefaultAfter int64 `yaml:"default_after" json:"default_after"`
}

// SavingsDef is the savings account.
type SavingsDef struct {
	// SpreadBPS is taken off the policy rate for the deposit rate, yearly.
	SpreadBPS  int64 `yaml:"spread_bps" json:"spread_bps"`
	MinAmount  int64 `yaml:"min_amount" json:"min_amount"`
	MaxBalance int64 `yaml:"max_balance" json:"max_balance"`
}

// InsuranceDef is one insurance product.
type InsuranceDef struct {
	Code   string `yaml:"code" json:"code"`
	Name   string `yaml:"name" json:"name"`
	Covers string `yaml:"covers" json:"covers"`
	// Premium per finance period: fixed, plus PremiumBPS of the value
	// insured (a property's value).
	Premium    int64 `yaml:"premium,omitempty" json:"premium,omitempty"`
	PremiumBPS int64 `yaml:"premium_bps,omitempty" json:"premium_bps,omitempty"`
	// CoverBPS of a loss is paid, up to MaxClaim per claim.
	CoverBPS int64 `yaml:"cover_bps" json:"cover_bps"`
	MaxClaim int64 `yaml:"max_claim" json:"max_claim"`
	// Waiting is the GAME time after buying before it pays a claim.
	Waiting string `yaml:"waiting" json:"waiting"`
}

// StocksDef is the stock exchange's listing rules.
type StocksDef struct {
	// MinAge is GAME time since founding.
	MinAge      string `yaml:"min_age" json:"min_age"`
	MinRevenue  int64  `yaml:"min_revenue" json:"min_revenue"`
	MinFloatBPS int64  `yaml:"min_float_bps" json:"min_float_bps"`
	MaxFloatBPS int64  `yaml:"max_float_bps" json:"max_float_bps"`
	// FloatOptions and PriceOptions are the choices offered at listing:
	// shares of the company, and prices as bps of its book value per share.
	FloatOptions []int64 `yaml:"float_options" json:"float_options"`
	PriceOptions []int64 `yaml:"price_options" json:"price_options"`
	// ListingFee is paid from the company's treasury to its city's.
	ListingFee  int64 `yaml:"listing_fee" json:"listing_fee"`
	TakeoverBPS int64 `yaml:"takeover_bps" json:"takeover_bps"`
	// PriceSteps are the order prices offered around the last price, bps
	// of it; QtyOptions the quantities.
	PriceSteps []int64 `yaml:"price_steps" json:"price_steps"`
	QtyOptions []int64 `yaml:"qty_options" json:"qty_options"`
	// DividendOptions are the dividends offered, bps of the company's free
	// money.
	DividendOptions []int64 `yaml:"dividend_options" json:"dividend_options"`
	// BookDepth and Trades are how many price levels and last trades a
	// company's page shows.
	BookDepth int `yaml:"book_depth" json:"book_depth"`
	Trades    int `yaml:"trades" json:"trades"`
}

// GoldDef is the gold dealer.
type GoldDef struct {
	StartPrice     int64 `yaml:"start_price" json:"start_price"`
	MinPrice       int64 `yaml:"min_price" json:"min_price"`
	MaxPrice       int64 `yaml:"max_price" json:"max_price"`
	StepBPS        int64 `yaml:"step_bps" json:"step_bps"`
	DemandBPSPerKG int64 `yaml:"demand_bps_per_kg" json:"demand_bps_per_kg"`
	DemandCapBPS   int64 `yaml:"demand_cap_bps" json:"demand_cap_bps"`
	SpreadBPS      int64 `yaml:"spread_bps" json:"spread_bps"`
	// Reserve is all the gold there is, grams, all at the dealer at first.
	Reserve int64 `yaml:"reserve" json:"reserve"`
	// GramOptions are the amounts offered in one trade; MaxGrams the most.
	GramOptions []int64 `yaml:"gram_options" json:"gram_options"`
	MaxGrams    int64   `yaml:"max_grams" json:"max_grams"`
	// History is how many past prices the dealer's screen shows.
	History int `yaml:"history" json:"history"`
}

// TONExchangeDef is the hook of a future TON↔Nil exchange through the
// treasury (docs/adr/0010, docs/adr/0026 section 8). Nothing reads it but
// validation: it must stay disabled until the owner's design is built.
type TONExchangeDef struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

func parseGame(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

// PeriodDuration is the finance period, GAME time.
func (d FinanceDef) PeriodDuration() time.Duration { return parseGame(d.Period) }

// CreditRules is the domain's score.
func (d FinanceDef) CreditRules() finance.CreditRules {
	c := d.Credit
	return finance.CreditRules{Min: c.Min, Max: c.Max,
		Weights: finance.CreditWeights{Payment: c.Weights.Payment, Debt: c.Weights.Debt, History: c.Weights.History,
			Income: c.Weights.Income, Worth: c.Weights.Worth, NewCredit: c.Weights.NewCredit},
		NeutralPaymentBPS: c.NeutralPaymentBPS, HistoryFull: parseGame(c.HistoryFull), IncomeFull: c.IncomeFull,
		WorthFull: c.WorthFull, NewCreditStepBPS: c.NewCreditStepBPS, MissedPoints: c.MissedPoints,
		DefaultPoints: c.DefaultPoints}
}

// CreditBands are the domain's bands.
func (d FinanceDef) CreditBands() []finance.Band {
	out := make([]finance.Band, 0, len(d.Bands))
	for _, b := range d.Bands {
		out = append(out, finance.Band{MinScore: b.MinScore, LimitBPS: b.LimitBPS, PremiumBPS: b.PremiumBPS})
	}
	return out
}

// Rules is a product's collection rules.
func (l LoanDef) Rules() finance.LoanRules {
	return finance.LoanRules{LateFeeBPS: l.LateFeeBPS, DefaultAfter: l.DefaultAfter}
}

// Loan is one product.
func (d FinanceDef) Loan(code string) (LoanDef, bool) {
	for _, l := range d.Loans {
		if l.Code == code {
			return l, true
		}
	}
	return LoanDef{}, false
}

// InsuranceProduct is one insurance product.
func (d FinanceDef) InsuranceProduct(code string) (InsuranceDef, bool) {
	for _, p := range d.Insurance {
		if p.Code == code {
			return p, true
		}
	}
	return InsuranceDef{}, false
}

// WaitingDuration is a product's waiting time, GAME time.
func (p InsuranceDef) WaitingDuration() time.Duration { return parseGame(p.Waiting) }

// ListingRules are the domain's listing rules.
func (d FinanceDef) ListingRules() finance.ListingRules {
	s := d.Stocks
	return finance.ListingRules{MinAge: parseGame(s.MinAge), MinRevenue: s.MinRevenue, MinFloatBPS: s.MinFloatBPS,
		MaxFloatBPS: s.MaxFloatBPS, TakeoverBPS: s.TakeoverBPS}
}

// GoldRules are the dealer's rules.
func (d FinanceDef) GoldRules() finance.GoldRules {
	g := d.Gold
	return finance.GoldRules{Min: g.MinPrice, Max: g.MaxPrice, StepBPS: g.StepBPS, DemandBPSPerKG: g.DemandBPSPerKG,
		DemandCapBPS: g.DemandCapBPS, SpreadBPS: g.SpreadBPS}
}

// Finance returns the finance section, and whether the content has one.
func (s *Snapshot) Finance() (FinanceDef, bool) {
	if s.finance == nil {
		return FinanceDef{}, false
	}
	return *s.finance, true
}

// validateFinance checks finance.yml.
func (p *Pack) validateFinance(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidFinanceContent, fmt.Sprintf(format, args...)))
	}
	if len(p.Finance) == 0 {
		return
	}
	if len(p.Finance) > 1 {
		bad("finance is declared %d times", len(p.Finance))
		return
	}
	d := p.Finance[0]
	gameSpan := func(where, s string, lo, hi time.Duration) {
		if v, err := time.ParseDuration(s); err != nil || v < lo || v > hi {
			bad("%s %q is not a game duration of %s..%s", where, s, lo, hi)
		}
	}
	gameSpan("period", d.Period, time.Hour, 30*24*time.Hour)
	if d.PeriodsPerYear < 1 || d.PeriodsPerYear > 1000 {
		bad("periods_per_year %d (1..1000)", d.PeriodsPerYear)
	}
	if d.Bank.CapitalCap < 0 || d.Bank.RecoveryBPS < 0 || d.Bank.RecoveryBPS > finance.BPSWhole ||
		d.Bank.MaxActiveLoans < 1 || d.Bank.MaxActiveLoans > 20 {
		bad("bank capital_cap %d, recovery_bps %d, max_active_loans %d", d.Bank.CapitalCap, d.Bank.RecoveryBPS,
			d.Bank.MaxActiveLoans)
	}
	c := d.Credit
	gameSpan("credit.history_full", c.HistoryFull, time.Hour, 10*365*24*time.Hour)
	gameSpan("credit.income_window", c.IncomeWindow, time.Hour, 365*24*time.Hour)
	gameSpan("credit.new_credit_window", c.NewCreditWindow, time.Hour, 365*24*time.Hour)
	gameSpan("credit.memory", c.Memory, time.Hour, 10*365*24*time.Hour)
	rules := d.CreditRules()
	if err := rules.Validate(); err != nil {
		bad("credit: %v", err)
	} else if err := finance.ValidateBands(d.CreditBands(), rules); err != nil {
		bad("bands: %v", err)
	}
	codes := map[string]bool{}
	for i, l := range d.Loans {
		where := fmt.Sprintf("loans[%d] %q", i, l.Code)
		if !transportCodePattern.MatchString(l.Code) || codes[l.Code] || len(l.Code) > 12 {
			bad("%s: code is not a short code or repeats one", where)
		}
		codes[l.Code] = true
		if l.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: finance %s", ErrMissingDisplayName, where))
		}
		if l.Borrower != BorrowerPlayer && l.Borrower != BorrowerCompany {
			bad("%s: borrower %q is not player or company", where, l.Borrower)
		}
		switch l.Collateral {
		case CollateralNone:
		case CollateralProperty:
			if l.Borrower != BorrowerPlayer || l.LTVBPS < 1 || l.LTVBPS > finance.BPSWhole {
				bad("%s: a property loan is a player's, lending 1..10000 bps of the value (ltv %d)", where, l.LTVBPS)
			}
		default:
			bad("%s: collateral %q is not property", where, l.Collateral)
		}
		if l.SpreadBPS < -finance.BPSWhole || l.SpreadBPS > finance.BPSWhole {
			bad("%s: spread %d", where, l.SpreadBPS)
		}
		if l.MinAmount < 1 || l.MaxAmount < l.MinAmount || l.MaxAmount > 1_000_000_000 || l.Step < 1 ||
			l.Step > l.MaxAmount {
			bad("%s: amounts %d..%d step %d", where, l.MinAmount, l.MaxAmount, l.Step)
		}
		if len(l.Terms) == 0 || len(l.Terms) > 4 {
			bad("%s: 1..4 terms", where)
		}
		for _, t := range l.Terms {
			if t < 1 || t > 1000 {
				bad("%s: term %d periods", where, t)
			}
		}
		if l.MinScore < c.Min || l.MinScore > c.Max {
			bad("%s: min_score %d outside the score range", where, l.MinScore)
		}
		if l.LateFeeBPS < 0 || l.LateFeeBPS > finance.BPSWhole || l.DefaultAfter < 1 || l.DefaultAfter > 100 {
			bad("%s: late fee %d, default after %d", where, l.LateFeeBPS, l.DefaultAfter)
		}
	}
	if s := d.Savings; s.SpreadBPS < 0 || s.SpreadBPS > finance.BPSWhole || s.MinAmount < 1 || s.MaxBalance < s.MinAmount {
		bad("savings spread %d, min %d, max %d", s.SpreadBPS, s.MinAmount, s.MaxBalance)
	}
	products := map[string]bool{}
	for i, in := range d.Insurance {
		where := fmt.Sprintf("insurance[%d] %q", i, in.Code)
		if !transportCodePattern.MatchString(in.Code) || products[in.Code] || len(in.Code) > 12 {
			bad("%s: code is not a short code or repeats one", where)
		}
		products[in.Code] = true
		if in.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: finance %s", ErrMissingDisplayName, where))
		}
		if in.Covers != CoverHospital && in.Covers != CoverWarDamage {
			bad("%s: covers %q is not hospital or war_damage", where, in.Covers)
		}
		if in.Premium < 0 || in.PremiumBPS < 0 || in.PremiumBPS > finance.BPSWhole || in.Premium+in.PremiumBPS == 0 {
			bad("%s: premium %d + %d bps must be something", where, in.Premium, in.PremiumBPS)
		}
		if in.PremiumBPS > 0 && in.Covers != CoverWarDamage {
			bad("%s: a premium by value needs something valued (war_damage insures a property)", where)
		}
		if in.CoverBPS < 1 || in.CoverBPS > finance.BPSWhole || in.MaxClaim < 0 {
			bad("%s: cover %d bps, max claim %d", where, in.CoverBPS, in.MaxClaim)
		}
		gameSpan(where+" waiting", in.Waiting, 0, 365*24*time.Hour)
	}
	st := d.Stocks
	gameSpan("stocks.min_age", st.MinAge, 0, 365*24*time.Hour)
	if err := d.ListingRules().Validate(); err != nil {
		bad("stocks: %v", err)
	}
	for _, f := range st.FloatOptions {
		if f < st.MinFloatBPS || f > st.MaxFloatBPS {
			bad("stocks.float_options %d outside %d..%d", f, st.MinFloatBPS, st.MaxFloatBPS)
		}
	}
	optionLists := []struct {
		name string
		list []int64
		lo   int64
		hi   int64
	}{
		{"stocks.float_options", st.FloatOptions, 1, finance.BPSWhole},
		{"stocks.price_options", st.PriceOptions, 1, 100 * finance.BPSWhole},
		{"stocks.price_steps", st.PriceSteps, 1, 100 * finance.BPSWhole},
		{"stocks.qty_options", st.QtyOptions, 1, 1_000_000},
		{"stocks.dividend_options", st.DividendOptions, 1, finance.BPSWhole},
		{"gold.gram_options", d.Gold.GramOptions, 1, d.Gold.MaxGrams},
		{"bank.offer_bps", d.Bank.OfferBPS, 1, finance.BPSWhole},
	}
	for _, o := range optionLists {
		if len(o.list) == 0 || len(o.list) > 4 {
			bad("%s: 1..4 options", o.name)
		}
		for _, v := range o.list {
			if v < o.lo || v > o.hi {
				bad("%s: %d outside %d..%d", o.name, v, o.lo, o.hi)
			}
		}
	}
	if st.ListingFee < 0 || st.BookDepth < 1 || st.BookDepth > 10 || st.Trades < 1 || st.Trades > 10 {
		bad("stocks listing_fee %d, book_depth %d, trades %d", st.ListingFee, st.BookDepth, st.Trades)
	}
	g := d.Gold
	if err := d.GoldRules().Validate(); err != nil {
		bad("gold: %v", err)
	}
	if g.StartPrice < g.MinPrice || g.StartPrice > g.MaxPrice || g.Reserve < 1 || g.MaxGrams < 1 || g.History < 1 ||
		g.History > 30 {
		bad("gold start %d, reserve %d, max_grams %d, history %d", g.StartPrice, g.Reserve, g.MaxGrams, g.History)
	}
	if d.TON.Enabled {
		bad("ton_exchange cannot be enabled: the TON exchange is not built (docs/adr/0026 section 8)")
	}
}

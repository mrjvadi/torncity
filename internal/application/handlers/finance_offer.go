package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// offer is what a product lends one player now, worked out once for the
// offer screen and again, from scratch, when the loan is taken.
type offer struct {
	product content.LoanDef
	bank    bankState
	credit  credit
	rate    int64
	// most is the most it lends: the band's share of the product, the
	// pledge's worth, what the bank may lend.
	most    int64
	pledges []screens.PledgeLine
	pledge  *screens.PledgeLine
}

// offerFor works out a product's offer; pledgeArg names the pledge chosen
// ("" or "0" for none yet).
func (h *FinanceHandler) offerFor(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	p *application.Player, code, pledgeArg string, now time.Time,
) (offer, error) {
	l, ok := def.Loan(code)
	if !ok {
		return offer{}, refuseFinance(screens.FinanceRefusedNoProduct)
	}
	o := offer{product: l}
	country, err := h.countryOf(ctx, tx, p)
	if err != nil {
		return o, err
	}
	if o.bank, err = h.bankOf(ctx, tx, country); err != nil {
		return o, err
	}
	if o.credit, err = h.creditOf(ctx, tx, snap, def, p, now); err != nil {
		return o, err
	}
	o.rate, o.most = quote(l, def, o.credit.score.Score, o.bank.policyBPS)
	if o.most == 0 {
		r := refuseFinance(screens.FinanceRefusedScore)
		r.view.Score = max(l.MinScore, def.CreditBands()[0].MinScore)
		for _, b := range def.CreditBands() {
			if b.LimitBPS > 0 && b.MinScore > o.credit.score.Score {
				r.view.Score = max(b.MinScore, l.MinScore)
				break
			}
		}
		return o, r
	}
	if productKind(l) != "player" {
		if o.pledges, err = h.pledges(ctx, tx, snap, l, p, o.most); err != nil {
			return o, err
		}
		if len(o.pledges) == 0 {
			return o, refuseFinance(screens.FinanceRefusedPledge)
		}
		pledgeArg = strings.TrimSpace(pledgeArg)
		for i := range o.pledges {
			if pledgeArg != "" && pledgeArg != "0" && (pledgeArg == o.pledges[i].Code ||
				pledgeArg == strconv.FormatInt(o.pledges[i].No, 10)) {
				o.pledge = &o.pledges[i]
			}
		}
		if o.pledge != nil {
			o.most = min(o.most, o.pledge.Limit)
		}
	}
	o.most = min(o.most, o.bank.lendable())
	o.most -= o.most % l.Step
	return o, nil
}

// options are the amounts and terms offered.
func (o offer) options(def content.FinanceDef) []screens.LoanOption {
	var out []screens.LoanOption
	seen := map[int64]bool{}
	for _, bps := range def.Bank.OfferBPS {
		a := finance.OfBPS(o.most, bps)
		a -= a % o.product.Step
		if a < o.product.MinAmount || seen[a] {
			continue
		}
		seen[a] = true
		for _, term := range o.product.Terms {
			s, err := finance.LoanTerms{Principal: a, RateBPS: o.rate, Periods: term, PeriodsPerYear: def.PeriodsPerYear}.Schedule()
			if err != nil {
				continue
			}
			out = append(out, screens.LoanOption{Amount: a, Term: term, Instalment: s.Amount(1)})
		}
	}
	return out
}

// allows reports whether an amount and a term are ones this offer makes.
func (o offer) allows(def content.FinanceDef, amount, term int64) bool {
	for _, opt := range o.options(def) {
		if opt.Amount == amount && opt.Term == term {
			return true
		}
	}
	return false
}

// Offer handles loan.offer: a product's amounts and terms, after its pledge.
func (h *FinanceHandler) Offer(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	var view screens.LoanOfferView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		o, err := h.offerFor(ctx, tx, snap, def, p, req.Product, req.Pledge, h.now())
		if err != nil {
			return err
		}
		l := o.product
		view = screens.LoanOfferView{Product: named(l.Code, l.Name), Kind: productKind(l), RateBPS: o.rate,
			Limit: o.most, Terms: l.Terms, Pledges: o.pledges, Pledge: o.pledge, LateFeeBPS: l.LateFeeBPS,
			DefaultAfter: l.DefaultAfter}
		if len(o.pledges) == 0 || o.pledge != nil {
			view.Options = o.options(def)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.LoanOffer(h.screen(meta, lang), view), nil
}

package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Insurance handles insure.list: the country's insurance fund, what it
// sells and the player's policies.
func (h *FinanceHandler) Insurance(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.insurance(ctx, meta, "", nil, 0)
}

// insurance renders the counter, opening with a notice, or asking about
// cancelling policy cancel.
func (h *FinanceHandler) insurance(ctx context.Context, meta envelope.Metadata, notice string, args map[string]any,
	cancel int64,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	view := screens.InsuranceView{Notice: notice, NoticeArgs: args}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.countryOf(ctx, tx, p)
		if err != nil {
			return err
		}
		fund, err := tx.Ledger().AccountFor(ctx, application.AccountInsuranceFund, country.ID)
		if err != nil {
			return err
		}
		view.Country, view.Fund = countryPlace(country), fund.Balance.Minor()
		policies, err := tx.Finance().PoliciesOf(ctx, p.ID, 8)
		if err != nil {
			return err
		}
		insured := map[string]bool{}
		now := h.now()
		for _, pol := range policies {
			line := screens.PolicyLine{No: pol.No, Product: named(pol.Product, pol.Product), Covers: pol.Covers,
				Status: pol.Status, Reason: pol.EndReason, From: pol.ClaimsFrom, Started: pol.StartedAt,
				Claimable: !now.Before(pol.ClaimsFrom)}
			product, ok := def.InsuranceProduct(pol.Product)
			if ok {
				line.Product.Name = product.Name
			}
			if pol.PropertyID != "" {
				pr, err := tx.Property().ByID(ctx, pol.PropertyID, false)
				if err != nil {
					return err
				}
				pl, err := h.propertyLine(ctx, snap, *pr)
				if err != nil {
					return err
				}
				line.Property = &pl
			}
			if pol.Status == application.PolicyActive {
				insured[pol.Product+"/"+pol.PropertyID] = true
				if line.Premium, _, err = premiumOf(ctx, tx, product, pol); err != nil {
					return err
				}
			}
			if line.Paid, err = tx.Finance().ClaimsPaid(ctx, pol.ID); err != nil {
				return err
			}
			if pol.No == cancel && pol.Status == application.PolicyActive {
				l := line
				view.Cancel = &l
			}
			view.Policies = append(view.Policies, line)
		}
		props, err := tx.Property().OfOwner(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, d := range def.Insurance {
			line := screens.InsuranceProductLine{Product: named(d.Code, d.Name), Covers: d.Covers, Premium: d.Premium,
				PremBPS: d.PremiumBPS, CoverBPS: d.CoverBPS, MaxClaim: d.MaxClaim,
				Waiting: h.scale.RealWait(d.WaitingDuration()), Held: insured[d.Code+"/"]}
			if d.Covers == content.CoverWarDamage {
				for _, pr := range props {
					if pr.Status != application.PropertyOwned || insured[d.Code+"/"+pr.ID] {
						continue
					}
					pl, err := h.propertyLine(ctx, snap, pr)
					if err != nil {
						return err
					}
					line.Targets = append(line.Targets, pl)
				}
			}
			view.Products = append(view.Products, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Insurance(h.screen(meta, lang), view), nil
}

// Buy handles insure.buy: without a method, the policy's first premium and
// the ways to pay it; with one, the policy — its first premium paid for
// this period, its claims waiting on the game clock.
func (h *FinanceHandler) Buy(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	product, ok := def.InsuranceProduct(req.Product)
	if !ok {
		return h.finish(meta, meta.Language, refuseFinance(screens.FinanceRefusedNoProduct, screens.AddrInsurance))
	}
	method := payment.Method(strings.TrimSpace(req.Method))
	chosen := method != ""
	lang := meta.Language
	var (
		confirm  *screens.InsureConfirmView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		if chosen {
			fresh, err := h.reserve(ctx, tx, p, meta, req.Nonce)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		now := h.now()
		country, err := h.countryOf(ctx, tx, p)
		if err != nil {
			return err
		}
		pol := application.InsurancePolicy{ID: h.ids.NewID(), PlayerID: p.ID, Product: product.Code, Covers: product.Covers,
			CountryID: country.ID, StartedAt: now, ClaimsFrom: now.Add(h.scale.RealWait(product.WaitingDuration()))}
		var target *screens.PledgeLine
		if product.Covers == content.CoverWarDamage {
			no, err := parseCount(req.Property)
			if err != nil {
				return refuseFinance(screens.FinanceRefusedPledge, screens.AddrInsurance)
			}
			pr, err := tx.Property().ByNo(ctx, no, false)
			if err != nil || pr.OwnerID != p.ID || pr.Status != application.PropertyOwned {
				return refuseFinance(screens.FinanceRefusedNotYours, screens.AddrInsurance)
			}
			pol.PropertyID = pr.ID
			pl, err := h.propertyLine(ctx, snap, *pr)
			if err != nil {
				return err
			}
			target = &pl
		}
		amount, _, err := premiumOf(ctx, tx, product, pol)
		if err != nil {
			return err
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(moneyOf(amount), snap.Accepts(content.ServiceInsurance))
		if !chosen {
			confirm = &screens.InsureConfirmView{Product: named(product.Code, product.Name), Covers: product.Covers,
				Property: target, Premium: amount, CoverBPS: product.CoverBPS, MaxClaim: product.MaxClaim,
				Waiting: h.scale.RealWait(product.WaitingDuration()), Payment: paymentChoice(plan, wallet)}
			return nil
		}
		if err := checkMethod(plan, method, wallet, "finance.button.insurance", screens.AddrInsurance); err != nil {
			return err
		}
		if pol, err = tx.Finance().CreatePolicy(ctx, pol); err != nil {
			if isSentinel(err, application.ErrPolicyExists) {
				return refuseFinance(screens.FinanceRefusedHeld, screens.AddrInsurance)
			}
			return err
		}
		fund, err := tx.Ledger().AccountFor(ctx, application.AccountInsuranceFund, country.ID)
		if err != nil {
			return err
		}
		current, _, err := tx.Finance().CurrentPeriod(ctx)
		if err != nil {
			return err
		}
		txID := h.ids.NewID()
		if _, err := tx.Finance().RecordPremium(ctx, pol.ID, current, amount, txID, now); err != nil {
			return err
		}
		if _, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{Method: method, Accepted: plan.Accepted,
			Reason: application.ReasonInsurancePremium, ReferenceType: application.InsurancePolicyReference,
			ReferenceID: pol.ID, To: []application.LedgerEntry{{AccountID: fund.ID, Amount: moneyOf(amount)}},
			CreatedAt: now, ID: txID}); err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "finance.button.insurance", screens.AddrInsurance)
			}
			return err
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	switch {
	case confirm != nil:
		return screens.InsureConfirm(h.screen(meta, lang), *confirm), nil
	case replayed:
		return h.Insurance(ctx, meta)
	}
	return h.insurance(ctx, meta, "insured", map[string]any{"product": product.Name}, 0)
}

// Cancel handles insure.cancel: without "yes", the question; with it, the
// policy ends. Premiums paid are not returned.
func (h *FinanceHandler) Cancel(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := strconv.ParseInt(strings.TrimSpace(req.No), 10, 64)
	if err != nil || no < 1 {
		return nil, err
	}
	if req.Confirm != screens.FinanceYes {
		return h.insurance(ctx, meta, "", nil, no)
	}
	lang := meta.Language
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		pol, err := tx.Finance().PolicyByNo(ctx, no)
		if isSentinel(err, application.ErrPolicyNotFound) || (err == nil && pol.PlayerID != p.ID) {
			return refuseFinance(screens.FinanceRefusedNotYours, screens.AddrInsurance)
		}
		if err != nil || pol.Status != application.PolicyActive {
			return err
		}
		now := h.now()
		pol.Status, pol.EndedAt, pol.EndReason, pol.UpdatedAt = application.PolicyEnded, &now, application.PolicyCancelled, now
		return tx.Finance().SavePolicy(ctx, *pol)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.insurance(ctx, meta, "cancelled", nil, 0)
}

package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// Insurance (docs/adr/0026 section 4): premiums every period into the
// country's insurance fund, and claims out of it on a real loss.

// premiumOf is a policy's premium for a period: its product's, on the
// value of the property it insures.
func premiumOf(ctx context.Context, tx application.Tx, product content.InsuranceDef, p application.InsurancePolicy) (int64, *application.Property, error) {
	if p.PropertyID == "" {
		return finance.Premium(product.Premium, 0, 0), nil, nil
	}
	pr, err := tx.Property().ByID(ctx, p.PropertyID, false)
	if err != nil {
		return 0, nil, err
	}
	return finance.Premium(product.Premium, product.PremiumBPS, pr.Value), pr, nil
}

// collectPremiums charges every running policy its premium for the period,
// once, from its holder's bank and then cash; a policy that cannot be paid
// lapses, and one whose property is no longer its holder's ends.
func (h *FinanceHandler) collectPremiums(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FinanceDef,
	meta envelope.Metadata, period int64, now time.Time,
) error {
	policies, err := tx.Finance().ActivePolicies(ctx)
	if err != nil {
		return err
	}
	for i := range policies {
		p := &policies[i]
		paid, err := tx.Finance().PremiumPaid(ctx, p.ID, period)
		if err != nil {
			return err
		}
		if paid {
			continue
		}
		product, ok := def.InsuranceProduct(p.Product)
		amount, pr, err := premiumOf(ctx, tx, product, *p)
		if err != nil {
			return err
		}
		switch {
		case !ok:
			if err := h.endPolicy(ctx, tx, meta, p, application.PolicyLapsed, "lapsed", product, 0, now); err != nil {
				return err
			}
			continue
		case pr != nil && (pr.Status != application.PropertyOwned || pr.OwnerID != p.PlayerID):
			if err := h.endPolicy(ctx, tx, meta, p, application.PolicyPropertyGone, "gone", product, 0, now); err != nil {
				return err
			}
			continue
		}
		pu, err := playerPurse(ctx, tx, p.PlayerID)
		if err != nil {
			return err
		}
		if pu.total() < amount {
			if err := h.endPolicy(ctx, tx, meta, p, application.PolicyLapsed, "lapsed", product, amount, now); err != nil {
				return err
			}
			continue
		}
		fund, err := tx.Ledger().AccountFor(ctx, application.AccountInsuranceFund, p.CountryID)
		if err != nil {
			return err
		}
		txID := h.ids.NewID()
		fresh, err := tx.Finance().RecordPremium(ctx, p.ID, period, amount, txID, now)
		if err != nil || !fresh {
			return err
		}
		if _, err := pu.pay(ctx, tx, amount, application.ReasonInsurancePremium, application.InsurancePolicyReference,
			p.ID, fund.ID, txID, now); err != nil {
			return err
		}
	}
	return nil
}

// endPolicy ends a policy and tells its holder.
func (h *FinanceHandler) endPolicy(ctx context.Context, tx application.Tx, meta envelope.Metadata, p *application.InsurancePolicy,
	reason, kind string, product content.InsuranceDef, amount int64, now time.Time,
) error {
	p.Status, p.EndedAt, p.EndReason, p.UpdatedAt = application.PolicyEnded, &now, reason, now
	if err := tx.Finance().SavePolicy(ctx, *p); err != nil {
		return err
	}
	name := product.Name
	if name == "" {
		name = p.Product
	}
	return appendDomainEvent(ctx, tx, meta, "insurance", kind, p.ID, map[string]any{"kind": kind,
		"player_id": p.PlayerID, "no": p.No, "product": p.Product, "product_name": name, "amount": amount})
}

// ClaimInsurance pays a claim on a real loss, once per source: the holder's
// running policy of that cover (on that property, for war damage), past its
// waiting time, pays its share of the loss up to its cap, from the fund, as
// far as the fund holds. It returns what was paid. Handlers call it in the
// transaction of the loss itself: a hospital treatment paid, a strike.
func ClaimInsurance(ctx context.Context, tx application.Tx, snap *content.Snapshot, ids IDGenerator, meta envelope.Metadata,
	policy application.InsurancePolicy, source string, loss int64, now time.Time,
) (int64, error) {
	def, ok := snap.Finance()
	if !ok || loss <= 0 || policy.Status != application.PolicyActive || now.Before(policy.ClaimsFrom) {
		return 0, nil
	}
	product, ok := def.InsuranceProduct(policy.Product)
	if !ok {
		return 0, nil
	}
	fund, err := tx.Ledger().AccountFor(ctx, application.AccountInsuranceFund, policy.CountryID)
	if err != nil {
		return 0, err
	}
	due, paid := finance.Claim(loss, product.CoverBPS, product.MaxClaim, fund.Balance.Minor())
	if due <= 0 {
		return 0, nil
	}
	claim := application.InsuranceClaim{ID: ids.NewID(), PolicyID: policy.ID, PlayerID: policy.PlayerID, Source: source,
		Loss: loss, Due: due, Paid: paid, At: now}
	if paid > 0 {
		claim.LedgerTx = ids.NewID()
	}
	fresh, err := tx.Finance().RecordClaim(ctx, claim)
	if err != nil || !fresh {
		return 0, err
	}
	if paid > 0 {
		bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, policy.PlayerID)
		if err != nil {
			return 0, err
		}
		if _, err := move(ctx, tx, fund.ID, bank.ID, paid, application.ReasonInsuranceClaim,
			application.InsuranceClaimReference, claim.ID, claim.LedgerTx, now); err != nil {
			return 0, err
		}
	}
	return paid, appendDomainEvent(ctx, tx, meta, "insurance", "claimed", claim.ID, map[string]any{"kind": "claimed",
		"player_id": policy.PlayerID, "no": policy.No, "product": policy.Product, "product_name": product.Name,
		"amount": paid, "other": due})
}

// ClaimHospital claims a hospital treatment on the patient's health policy.
func ClaimHospital(ctx context.Context, tx application.Tx, snap *content.Snapshot, ids IDGenerator, meta envelope.Metadata,
	playerID, stayID string, price int64, now time.Time,
) (int64, error) {
	if _, ok := snap.Finance(); !ok || price <= 0 {
		return 0, nil
	}
	p, err := tx.Finance().CoveringPolicy(ctx, playerID, content.CoverHospital)
	if err != nil || p == nil {
		return 0, err
	}
	return ClaimInsurance(ctx, tx, snap, ids, meta, *p, "stay:"+stayID, price, now)
}

// ClaimStrike claims a strike on a city on every running policy of a
// property there: the property's value times the damage the strike added.
func ClaimStrike(ctx context.Context, tx application.Tx, snap *content.Snapshot, ids IDGenerator, meta envelope.Metadata,
	cityID, operationID string, damageBPS int64, now time.Time,
) error {
	if _, ok := snap.Finance(); !ok || damageBPS <= 0 {
		return nil
	}
	policies, err := tx.Finance().PropertyPolicies(ctx, cityID)
	if err != nil {
		return err
	}
	for _, p := range policies {
		pr, err := tx.Property().ByID(ctx, p.PropertyID, false)
		if err != nil {
			return err
		}
		if _, err := ClaimInsurance(ctx, tx, snap, ids, meta, p, "strike:"+operationID,
			finance.OfBPS(pr.Value, min(damageBPS, finance.BPSWhole)), now); err != nil {
			return err
		}
	}
	return nil
}

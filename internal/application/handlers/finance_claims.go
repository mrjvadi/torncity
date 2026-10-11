package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// Claims of falls and raids (docs/adr/0074 5): a deductible, and one fund paying several claims of one raid pro rata when it is short.

// buildingValue is what a building cost to raise (its content cost), the value a raid_damage policy insures; zero for a building the
// content does not price.
func buildingValue(ctx context.Context, tx application.Tx, snap *content.Snapshot, id string) int64 {
	b, err := tx.SettlementBuildings().Get(ctx, id)
	if err != nil || b == nil {
		return 0
	}
	if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
		return d.CostMoney
	}
	return 0
}

// buildingStands reports whether a building still holds its lot.
func buildingStands(ctx context.Context, tx application.Tx, id string) bool {
	b, err := tx.SettlementBuildings().Get(ctx, id)
	return err == nil && b != nil && b.Status == "complete"
}

// ClaimItem is one claim of a batch: a policy, the source of the loss (a fall, a raid) and the loss.
type ClaimItem struct {
	Policy application.InsurancePolicy
	Source string
	Loss   int64
}

// ClaimBatch pays the claims of one event. Each policy pays its share of the loss above its deductible, up to its cap, once per source;
// the claims that fall on one fund are paid pro rata when the fund cannot pay them all (the shortfall is the same share for every
// claimant and the fund never goes below zero). It returns what each policy was paid, by policy id.
func ClaimBatch(ctx context.Context, tx application.Tx, snap *content.Snapshot, ids IDGenerator, meta envelope.Metadata,
	items []ClaimItem, now time.Time,
) (map[string]int64, error) {
	paidBy := map[string]int64{}
	def, ok := snap.Finance()
	if !ok || len(items) == 0 {
		return paidBy, nil
	}
	type due struct {
		item    ClaimItem
		product content.InsuranceDef
		due     int64
	}
	byFund := map[string][]due{}
	var funds []string
	for _, it := range items {
		p := it.Policy
		if it.Loss <= 0 || p.Status != application.PolicyActive || now.Before(p.ClaimsFrom) {
			continue
		}
		product, ok := def.InsuranceProduct(p.Product)
		if !ok {
			continue
		}
		if done, err := tx.Finance().ClaimedBefore(ctx, p.ID, it.Source); err != nil {
			return nil, err
		} else if done {
			continue
		}
		d := finance.ClaimDue(it.Loss, product.CoverBPS, product.DeductibleBPS, product.MaxClaim)
		if d <= 0 {
			continue
		}
		if _, seen := byFund[p.CountryID]; !seen {
			funds = append(funds, p.CountryID)
		}
		byFund[p.CountryID] = append(byFund[p.CountryID], due{item: it, product: product, due: d})
	}
	sort.Strings(funds)
	for _, fundOwner := range funds {
		group := byFund[fundOwner]
		sort.Slice(group, func(i, j int) bool { return group[i].item.Policy.ID < group[j].item.Policy.ID })
		fund, err := tx.Ledger().AccountFor(ctx, application.AccountInsuranceFund, fundOwner)
		if err != nil {
			return nil, err
		}
		dues := make([]int64, len(group))
		for i, g := range group {
			dues[i] = g.due
		}
		paid := finance.ProRata(dues, fund.Balance.Minor())
		for i, g := range group {
			p := g.item.Policy
			claim := application.InsuranceClaim{ID: ids.NewID(), PolicyID: p.ID, PlayerID: p.PlayerID, Source: g.item.Source,
				Loss: g.item.Loss, Due: g.due, Paid: paid[i], At: now}
			if paid[i] > 0 {
				claim.LedgerTx = ids.NewID()
			}
			fresh, err := tx.Finance().RecordClaim(ctx, claim)
			if err != nil {
				return nil, err
			}
			if !fresh {
				continue
			}
			if paid[i] > 0 {
				bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, p.PlayerID)
				if err != nil {
					return nil, err
				}
				if _, err := move(ctx, tx, fund.ID, bank.ID, paid[i], application.ReasonInsuranceClaim,
					application.InsuranceClaimReference, claim.ID, claim.LedgerTx, now); err != nil {
					return nil, err
				}
				paidBy[p.ID] += paid[i]
			}
			if err := appendDomainEvent(ctx, tx, meta, "insurance", "claimed", claim.ID, map[string]any{"kind": "claimed",
				"player_id": p.PlayerID, "no": p.No, "product": p.Product, "product_name": g.product.Name, "amount": paid[i], "other": g.due}); err != nil {
				return nil, err
			}
		}
	}
	return paidBy, nil
}

package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// Tier promotion (docs/adr/0028-world-and-settlements.md section 4.1): a
// settlement grows village -> town -> city by development - its people, its
// learning, what stands in it, what it knows, what is in its treasury - never
// by land. What each step asks for is content (settlement_tiers.yml); this
// file counts what the settlement has, lines the two up for the way-forward
// screen, and, when the head confirms a step whose goals are all met, takes
// it.
//
// WHAT PROMOTION DOES, in one transaction:
//
//   - cities.tier and the settlement's jurisdiction kind move to the new tier
//     (the tier's own levers, grid and build cap apply from then on; the land
//     the settlement holds is untouched);
//   - the new tier's offices get their seats, and the sitting head SUCCEEDS
//     into the new head office (village_head -> town_head -> mayor): the same
//     player keeps leading, acquired by founding again, with no election
//     forced by growth alone. ADR 0028 section 4.1 leaves the alternative -
//     opening the new office's first election - to an operator lever
//     (settlement.promotion_mode); only succession is built, and the lever is
//     a later addition. The old seat is vacated and kept, never deleted (a
//     lever change may reference it);
//   - one settlement.promoted event: the news in the group, the realtime
//     publication for game clients, and the audit row beside it.
//
// There is no demotion: a tier, once reached, is kept (ADR 0028 section 4.1).
// The step costs nothing and takes no time; the goals are the price.

// VillagePromoteRequest is the payload of settlement.promote.
type VillagePromoteRequest struct {
	// Confirm is village.VillagePromoteConfirm on the second press.
	Confirm string `json:"confirm,omitempty"`
}

func (r VillagePromoteRequest) confirmed() bool { return r.Confirm == village.VillagePromoteConfirm }

// tierStanding counts what a settlement has, the input the ladder's rules
// judge. Roads and demolished or unfinished buildings do not count as
// buildings; a role's tier is the highest finished building of it.
func tierStanding(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement) (wsettle.Standing, error) {
	st := wsettle.Standing{RoleTiers: map[string]int{}}
	var err error
	if st.Residents, err = tx.Settlements().ResidentCount(ctx, s.CityID); err != nil {
		return st, err
	}
	literacy, _, err := tx.SettlementKnowledge().Literacy(ctx, s.CityID)
	if err != nil {
		return st, err
	}
	st.LiteracyBPS = int64(literacy)
	if st.Treasury, err = treasuryBalance(ctx, tx, s.CityID); err != nil {
		return st, err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return st, err
	}
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		if b.TypeCode != "road" {
			st.Buildings++
		}
		if def, ok := snap.SettlementBuildingDef(b.TypeCode); ok && def.Role != "" && def.Tier > st.RoleTiers[def.Role] {
			st.RoleTiers[def.Role] = def.Tier
		}
	}
	owned, err := tx.SettlementKnowledge().Owned(ctx, s.CityID)
	if err != nil {
		return st, err
	}
	for _, o := range owned {
		if o.AcquiredVia != "founding" {
			st.KnowledgeLearned++
		}
	}
	return st, nil
}

// tierRule is the step up from a tier, as the rules take it; false at the top
// of the ladder.
func tierRule(snap *content.Snapshot, tier string) (wsettle.TierRule, bool) {
	d, ok := snap.SettlementTierStep(tier)
	if !ok {
		return wsettle.TierRule{}, false
	}
	r := wsettle.TierRule{
		From: d.From, To: d.Code, Residents: d.Residents, LiteracyBPS: d.LiteracyBPS, Buildings: d.Buildings,
		KnowledgeLearned: d.KnowledgeLearned, Treasury: d.Treasury,
	}
	for _, n := range d.Roles {
		r.Roles = append(r.Roles, wsettle.RoleNeed{Role: n.Role, Tier: n.Tier})
	}
	return r, true
}

// isHead reports whether the player holds (or acts for) the settlement's top
// office.
func isHead(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string) (bool, error) {
	err := authorizeVillage(ctx, tx, s, playerID)
	switch {
	case err == nil:
		return true, nil
	case stderrors.Is(err, application.ErrNotOfficeHolder):
		return false, nil
	}
	return false, err
}

// promotionOf is the way forward from the settlement's tier for the viewer,
// or nil at the top of the ladder.
func (h *VillageHandler) promotionOf(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	s application.FoundedSettlement, viewerID string,
) (*village.PromotionView, error) {
	rule, ok := tierRule(snap, s.Tier)
	if !ok {
		return nil, nil
	}
	standing, err := tierStanding(ctx, tx, snap, s)
	if err != nil {
		return nil, err
	}
	head, err := isHead(ctx, tx, s, viewerID)
	if err != nil {
		return nil, err
	}
	return promotionView(s, rule.Evaluate(standing), head), nil
}

func promotionView(s application.FoundedSettlement, p wsettle.Progress, head bool) *village.PromotionView {
	v := &village.PromotionView{
		Village: s.Name, From: p.From, To: p.To, Met: p.Met, CanPromote: head,
		Office: wsettle.HeadOffice(p.To), SettlementID: s.CityID,
	}
	for _, k := range p.Criteria {
		v.Criteria = append(v.Criteria, village.PromotionCriterionView{
			Kind: k.Kind, Role: k.Role, Current: k.Current, Required: k.Required, Met: k.Met,
		})
	}
	return v
}

// PromotionView handles settlement.promotion.view. The ladder is retired (a
// settlement grows by what it researches and builds, never by a promotion): the
// command stays for old clients and answers the development readout.
func (h *VillageHandler) PromotionView(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.DevelopmentView(ctx, meta)
}

// Promote handles settlement.promote. It no longer promotes anything; it answers
// the development readout so an old client's button lands somewhere true.
func (h *VillageHandler) Promote(ctx context.Context, meta envelope.Metadata, _ VillagePromoteRequest) (*presentation.Response, error) {
	return h.DevelopmentView(ctx, meta)
}

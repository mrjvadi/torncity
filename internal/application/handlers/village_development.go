package handlers

import (
	"context"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The development readout (docs/adr/0044-organic-growth-alliances-countries.md
// section 4.5, phase G1): what the settlement carries against what it can
// carry, the service buildings it has, and the goals still ahead. It sits
// IN PLACE OF the promotion screen (retired 2026-10-04, owner scope rule "no
// forced promotion ladder"): settlement.promotion.view and settlement.promote
// answer this same screen for old clients. It has no act.
//
// The dimensions are the ones whose load and capacity already exist in the
// data (residents over the homes' capacity, finished buildings, knowledge held).
// The others of ADR 0044 section 4.1 (land, zones, roads, services, administration,
// construction crews) have no capacity model yet; each is added with the node
// model and the land work, never guessed here.

// developmentNextLimit is how many research and building steps of each kind the
// readout names.
const developmentNextLimit = 4

// developmentOf builds the readout of one settlement.
func (h *VillageHandler) developmentOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	_ string,
) (*village.DevelopmentView, error) {
	residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	owned, err := tx.SettlementKnowledge().Owned(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	var standing int64
	levels := map[string]int{}
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		if b.TypeCode != "road" {
			standing++
		}
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok && d.Role != "" && d.Tier > levels[d.Role] {
			levels[d.Role] = d.Tier
		}
	}
	housing, herr := h.housingNow(ctx, tx, snap, s.CityID, buildings)
	if herr != nil {
		return nil, herr
	}
	v := &village.DevelopmentView{
		Village: s.Name, SettlementID: s.CityID,
		Dimensions: []village.DevelopmentDimension{
			{Code: village.DevelopmentPeople, Load: residents, Capacity: housing},
			{Code: village.DevelopmentBuildings, Load: standing},
			{Code: village.DevelopmentKnowledge, Load: int64(len(owned))},
		},
	}
	roles := make([]string, 0, len(levels))
	for r := range levels {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		v.Roles = append(v.Roles, village.DevelopmentRole{Role: r, Level: levels[r]})
	}
	// What could be taken next: research and buildings whose prerequisites the
	// settlement already holds (no stage, no size).
	ownedSet := map[string]bool{}
	for _, o := range owned {
		ownedSet[o.Code] = true
	}
	standingSet := map[string]bool{}
	for _, b := range buildings {
		if b.Status == "complete" {
			standingSet[b.TypeCode] = true
		}
	}
	for _, st := range snap.NextGrowth(ownedSet, standingSet, levels, developmentNextLimit) {
		v.Next = append(v.Next, village.DevelopmentNext{Kind: st.Kind, Code: st.Code, Name: st.Name})
	}
	return v, nil
}

// DevelopmentView handles settlement.development.view.
func (h *VillageHandler) DevelopmentView(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view *village.DevelopmentView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		view, err = h.developmentOf(ctx, tx, snap, s, p.ID)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.VillageDevelopment(h.screen(meta, lang), *view), nil
}

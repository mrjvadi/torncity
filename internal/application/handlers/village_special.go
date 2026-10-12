package handlers

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
)

// Special projects, intelligence and the licence market (docs/adr/0077): the pieces the three screens share.

// WithSpecial sets the rules of the item (config projects.*, intel.*, licence.*). Until they are set, and before their dates, every act of the
// item is refused.
func (h *VillageHandler) WithSpecial(r application.SpecialRules) *VillageHandler {
	h.special = r
	return h
}

func (h *VillageHandler) since(at time.Time, grace time.Duration) bool {
	return !at.IsZero() && !h.now().Before(at.Add(grace))
}

// projectsOpen: special projects have begun. intelOpen: the bureaus and their agents have. sabotageOpen: sabotage has, after its grace.
// licenceOpen: the licence market has.
func (h *VillageHandler) projectsOpen() bool { return h.since(h.special.ProjectsAt, 0) }
func (h *VillageHandler) intelOpen() bool    { return h.since(h.special.IntelAt, 0) }
func (h *VillageHandler) sabotageOpen() bool {
	return h.since(h.special.SabotageAt, h.special.Intel.Grace)
}
func (h *VillageHandler) licenceOpen() bool { return h.since(h.special.LicenceAt, 0) }

// gameDayNumber is the day of the real clock, UTC: the fence of a once-a-day row.
func gameDayNumber(now time.Time) int64 { return now.UTC().Unix() / 86400 }

// knowledgeMissing lists, in order, the codes of need that the settlement has not researched.
func knowledgeMissing(ctx context.Context, tx application.Tx, settlementID string, need []string) ([]string, error) {
	if len(need) == 0 {
		return nil, nil
	}
	owned, err := tx.SettlementKnowledge().Owned(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, k := range owned {
		have[k.Code] = true
	}
	var out []string
	for _, c := range need {
		if !have[c] {
			out = append(out, c)
		}
	}
	return out, nil
}

// needsKnowledge is the knowledge a row's requires block asks.
func needsKnowledge(n *content.AvailabilityNeeds) []string {
	if n == nil {
		return nil
	}
	return n.Knowledge
}

// researchRefusal is a refusal that names the first research a settlement lacks.
func researchRefusal(kind, back string, missing []string, snap *content.Snapshot) *villageRefusal {
	r := refuseVillage(kind, back)
	if len(missing) > 0 {
		name := missing[0]
		if k, ok := snap.SettlementKnowledgeDef(missing[0]); ok && k.Name != "" {
			name = k.Name
		}
		r.subject = presentation.Named{Code: missing[0], Name: name}
	}
	return r
}

// permissionHolders are the players who hold a permission in a settlement's charter, the head included, sorted.
func permissionHolders(ctx context.Context, tx application.Tx, s application.FoundedSettlement, perm charter.Permission) ([]string, error) {
	st, err := loadCharterState(ctx, tx, s, "head")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, o := range st.offices {
		if o.Closed {
			continue
		}
		has := false
		for _, g := range o.Grants {
			has = has || g.Permission == perm
		}
		if !has {
			continue
		}
		if o.Acquisition == charter.AcquireHead {
			add(st.headHeld)
			continue
		}
		for _, seat := range st.seats[o.ID] {
			add(seat.HolderID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// holderAccount is the purse of a licence holder or a bidder: a player's cash or a company's treasury.
func holderAccount(ctx context.Context, tx application.Tx, kind, id string) (application.Account, error) {
	if kind == "company" {
		return tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, id)
	}
	return tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, id)
}

// ownsCompany reports whether the player owns the company or manages it (a company of theirs).
func ownsCompany(ctx context.Context, tx application.Tx, playerID, companyCode string) (*application.Company, error) {
	mine, err := tx.Companies().Of(ctx, playerID)
	if err != nil {
		return nil, err
	}
	for i := range mine {
		if strings.EqualFold(mine[i].Code, strings.TrimSpace(companyCode)) {
			return &mine[i], nil
		}
	}
	return nil, nil
}

func specialEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, settlementID string, payload map[string]any) error {
	return appendVillageEvent(ctx, tx, meta, name, settlementID, payload)
}

// projectWorks are the finished special projects of a settlement as the buildings the service day, the detectors and the research desk read.
// A work has no lot on the map: it is a post of the settlement, kept and paid every local day, idle when nobody keeps it.
func projectWorks(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string, centre [2]int) ([]application.SettlementBuildingInstance, error) {
	done, err := tx.Projects().Finished(ctx, settlementID)
	if err != nil || len(done) == 0 {
		return nil, err
	}
	var out []application.SettlementBuildingInstance
	for _, p := range done {
		k, ok := snap.SpecialProject(p.Kind)
		if !ok {
			continue
		}
		var at time.Time
		if p.FinishedAt != nil {
			at = *p.FinishedAt
		}
		out = append(out, application.SettlementBuildingInstance{ID: p.ID, SettlementID: p.SettlementID, TypeCode: k.Code, LotX: centre[0], LotY: centre[1], Status: "complete",
			QueuedAt: at, CompletedAt: &at})
	}
	return out, nil
}

// withWorks adds the works of a settlement to the buildings a system reads.
func (h *VillageHandler) withWorks(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string, buildings []application.SettlementBuildingInstance) ([]application.SettlementBuildingInstance, error) {
	if !h.projectsOpen() {
		return buildings, nil // before the rule date no work stands
	}
	works, err := projectWorks(ctx, tx, snap, settlementID, centreOf(buildings))
	if err != nil || len(works) == 0 {
		return buildings, err
	}
	return append(append([]application.SettlementBuildingInstance(nil), buildings...), works...), nil
}

// workHeld reports whether a finished work with the effect stands open in the settlement today, and the size of the effect: the sum of the
// amounts of the works of the kind that are open. A work is open when its keepers are paid and its fuel burnt; the day is judged now if nobody
// has judged it.
func (h *VillageHandler) workHeld(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, effect string) (amount int64, held bool, err error) {
	if !h.projectsOpen() {
		return 0, false, nil
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return 0, false, err
	}
	works, err := projectWorks(ctx, tx, snap, s.CityID, centreOf(buildings))
	if err != nil || len(works) == 0 {
		return 0, false, err
	}
	var mine []application.SettlementBuildingInstance
	for _, w := range works {
		if k, ok := snap.SpecialProject(w.TypeCode); ok && k.Effect == effect {
			mine = append(mine, w)
		}
	}
	if len(mine) == 0 {
		return 0, false, nil
	}
	day, err := h.SettleServiceDay(ctx, tx, snap, s, buildings)
	if err != nil || day == nil {
		return 0, false, err
	}
	for _, w := range mine {
		if day.HeldPost(w.ID) {
			k, _ := snap.SpecialProject(w.TypeCode)
			amount += k.Amount
			held = true
		}
	}
	return amount, held, nil
}

// workHeldToday is workHeld without the amount.
func (h *VillageHandler) workHeldToday(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, effect string) (bool, error) {
	_, held, err := h.workHeld(ctx, tx, snap, s, effect)
	return held, err
}

// centreOf is where a work stands for the systems that measure: the settlement's civic hall (the centre of its roads), or the origin.
func centreOf(buildings []application.SettlementBuildingInstance) [2]int {
	for _, b := range buildings {
		if b.TypeCode == "civic_hall" && b.Status == "complete" {
			return [2]int{b.LotX, b.LotY}
		}
	}
	return [2]int{}
}

// buildingsWithWorks lists the buildings of a settlement and its finished works.
func (h *VillageHandler) buildingsWithWorks(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string) ([]application.SettlementBuildingInstance, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	return h.withWorks(ctx, tx, snap, settlementID, buildings)
}

// cityTreasury is a settlement's treasury account.
func cityTreasury(ctx context.Context, tx application.Tx, settlementID string) (application.Account, error) {
	return tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, settlementID)
}

package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/land"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The work on the land (docs/adr/0065). A woodcutter's camp fells one tree of the land each shift, a forester's lodge plants one
// sapling, a pit breaks the field rocks before it works its face. The lot a crew works is chosen the same way on every replica:
// the lots with a clearing order first, then the nearest, then the most wooded, then by position; and only lots the crew may
// touch: the commons, or a lot somebody ordered cleared.
//
//   - WHO WORKS THERE. The crew of the building (an NPC of the pool or a player), as for any workplace.
//   - WHAT IT CONSUMES. As before (meals, tools); a shift on a citizen's lot is paid for by him: the wage leaves his cash into
//     the treasury (ledger reason clearing_fee) when the shift starts, and the goods are his.
//   - WHAT IT PROVIDES. The usual goods of the shift; the lot changes (a tree less, a rock less, a sapling more) and the layout
//     version moves.
//   - WHAT BREAKS. No tree in reach: the felling shift is refused (no_trees_in_reach) once the grace of the live camps is over;
//     no lot to plant: the forester's shift is refused (no_plot_in_reach); a pit with no rock in reach works its face.

// landKindOf is what a building does to the land: "tree", "sapling", "rock" or "" for a building that does nothing to it.
func landKindOf(d content.SettlementBuildingDef) string {
	switch {
	case d.Fells:
		return application.LandKindTree
	case d.Plants:
		return application.LandKindSapling
	case d.Quarries:
		return application.LandKindRock
	}
	return ""
}

// landJob is the lot a shift will work and what follows from it.
type landJob struct {
	kind  string
	pos   land.Pos
	owner string // a citizen whose lot it is: he pays the wage and gets the goods
	// abstract is a shift with no lot to work (a camp in its grace, a pit working its face): it changes no lot.
	abstract bool
	// rockWork is the shifts already spent on the rock in hand.
	rockWork int
}

// planLand chooses the lot a shift will work, or refuses the shift. Nothing is written but the land lock. nil, nil: the
// building does nothing to the land (or the land model is off).
func (h *VillageHandler) planLand(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	b application.SettlementBuildingInstance, d content.SettlementBuildingDef, wage int64, back string,
) (*landJob, error) {
	kind := landKindOf(d)
	if kind == "" || !h.landRules.Enabled() {
		return nil, nil
	}
	if err := tx.Land().Lock(ctx, s.CityID); err != nil {
		return nil, err
	}
	w, err := h.world(ctx)
	if err != nil {
		return nil, err
	}
	open, err := tx.Citizens().OpenLots(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	view, err := h.landViewOf(ctx, tx, w, s, open)
	if err != nil {
		return nil, err
	}
	def, _ := snap.Land()
	lots, err := tx.Citizens().Lots(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	owners := map[land.Pos]string{}
	for _, l := range lots {
		owners[land.Pos{X: l.X, Y: l.Y}] = l.OwnerID
	}
	radius := h.landRules.FellRadius
	if kind == application.LandKindRock {
		radius = h.landRules.QuarryRadius
	}
	camp := land.Pos{X: b.LotX, Y: b.LotY}
	var targets []land.Target
	for p, l := range view.Lots {
		if l.Occupied || land.Dist(camp, p) > radius {
			continue
		}
		ordered := false
		switch kind {
		case application.LandKindTree:
			ordered = l.Delta.ClearTrees
			if l.Trees == 0 || !(l.Commons || ordered) {
				continue
			}
		case application.LandKindRock:
			ordered = l.Delta.ClearRocks
			if l.Rocks == 0 || !(l.Commons || ordered) {
				continue
			}
		case application.LandKindSapling:
			if !l.Commons || l.Ground.Water || l.Delta.TreesCut == 0 || l.Trees+len(l.Growing) >= def.MaxTreesRing {
				continue
			}
		}
		if owner := owners[p]; owner != "" && wage > 0 {
			if _, cash, err := playerCash(ctx, tx, owner); err != nil {
				return nil, err
			} else if cash < wage {
				continue // he cannot pay for the shift: the crew goes to another lot
			}
		}
		targets = append(targets, land.Target{Pos: p, Dist: land.Dist(camp, p), Ordered: ordered, Wood: l.Trees})
	}
	if len(targets) > 0 {
		land.ByReach(targets)
		t := targets[0]
		return &landJob{kind: kind, pos: t.Pos, owner: owners[t.Pos], rockWork: view.Lots[t.Pos].Delta.RockWork}, nil
	}
	switch kind {
	case application.LandKindTree:
		if h.landRules.InGrace(h.now()) {
			return &landJob{kind: kind, abstract: true}, nil // the camps that stood before the model cut as they did until the grace ends
		}
		return nil, refuseVillage(village.LandNoTrees, back)
	case application.LandKindSapling:
		return nil, refuseVillage(village.LandNoPlot, back)
	}
	return &landJob{kind: kind, abstract: true}, nil // a pit with no rock in reach works its face
}

// applyLand writes what the shift does to the lot, the clearing fee of a citizen's lot and the event.
func (h *VillageHandler) applyLand(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement,
	job *landJob, shiftID string, wage int64, now time.Time,
) error {
	if job == nil || job.abstract {
		return nil
	}
	repo := tx.Land()
	switch job.kind {
	case application.LandKindTree:
		if err := repo.Apply(ctx, s.CityID, job.pos, application.LandChange{Trees: 1, Anchor: &now}, now); err != nil {
			return err
		}
	case application.LandKindRock:
		if job.rockWork+1 >= max(h.landRules.RockShifts, 1) {
			if err := repo.Apply(ctx, s.CityID, job.pos, application.LandChange{Rocks: 1, SetRockWork: true, RockWork: 0}, now); err != nil {
				return err
			}
		} else if err := repo.Apply(ctx, s.CityID, job.pos, application.LandChange{SetRockWork: true, RockWork: job.rockWork + 1}, now); err != nil {
			return err
		}
	case application.LandKindSapling:
		if err := repo.Plant(ctx, s.CityID, job.pos, h.ids.NewID(), shiftID, now, now.Add(h.landRules.SaplingFor)); err != nil {
			return err
		}
	}
	if job.owner != "" && wage > 0 {
		cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, job.owner)
		if err != nil {
			return err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: h.ids.NewID(), Reason: application.ReasonClearingFee, CreatedAt: now,
			ReferenceType: application.SettlementShiftReference, ReferenceID: shiftID,
			Entries: []application.LedgerEntry{
				{AccountID: cash.ID, Amount: money.FromMinor(-wage)},
				{AccountID: treasury.ID, Amount: money.FromMinor(wage)},
			},
		}); err != nil {
			return err
		}
	}
	return h.appendBuildingEvent(ctx, tx, meta, s, "land_changed", map[string]any{
		"settlement_id": s.CityID, "kind": landEventKind(job.kind), "x": job.pos.X, "y": job.pos.Y,
	})
}

func landEventKind(kind string) string {
	switch kind {
	case application.LandKindTree:
		return "trees_cut"
	case application.LandKindRock:
		return "rocks_cut"
	}
	return "planted"
}

// ownerShare is how much of what a shift made goes to the citizen whose lot it worked: all of it as far as his home store has
// room (what does not fit goes to the village's stock); nothing for a shift on the commons.
func (h *VillageHandler) ownerShare(ctx context.Context, tx application.Tx, snap *content.Snapshot, sh *application.SettlementShift, made map[string]int64) (map[string]int64, error) {
	out := map[string]int64{}
	if sh.LandOwner == "" || len(made) == 0 {
		return out, nil
	}
	s, err := tx.Settlements().ByID(ctx, sh.SettlementID)
	if err != nil {
		return nil, err
	}
	st, err := h.loadHomeStock(ctx, tx, snap, s, sh.LandOwner)
	if err != nil {
		return nil, err
	}
	room := st.free()
	for _, c := range materialCodes(made) {
		bulk := max(snap.BulkOf(c), 1)
		q := min(made[c], max(room, 0)/bulk)
		if q > 0 {
			out[c] = q
			room -= q * bulk
		}
	}
	return out, nil
}

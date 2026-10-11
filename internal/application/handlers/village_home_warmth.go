package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
)

// HomeWarmth is the insulation a player's dwelling in the settlement gives through the night (docs/adr/0070): the dwelling's own,
// and a lit hearth's when he has the wood. Each local night since the last read burns a firewood from his home store (burn);
// without a night to burn, a hearth counts as lit if the store holds firewood. A player with no dwelling here has none.
func (h *VillageHandler) HomeWarmth(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string, nights int,
	def content.SurvivalDef, burn bool, now time.Time,
) (int, error) {
	pb, b, err := h.homeOf(ctx, tx, s, playerID)
	if err != nil || pb == nil || b == nil {
		return 0, err
	}
	bonus := def.HomeBonus
	f, err := tx.SettlementBuildings().FunctionOf(ctx, application.BuildingRefSettlement, b.ID)
	if err != nil || f == nil || f.Modules["hearth"] < 1 {
		return bonus, err
	}
	lit := false
	if burn && nights > 0 {
		for i := 0; i < nights; i++ {
			ok, err := h.burnHearth(ctx, tx, playerID, b.ID, now)
			if err != nil {
				return bonus, err
			}
			lit = lit || ok
		}
	} else if h.lot.HearthFuel != "" {
		stacks, _, err := tx.Items().Holdings(ctx, playerID, application.HoldHome)
		if err != nil {
			return bonus, err
		}
		for _, st := range stacks {
			lit = lit || (st.Item == h.lot.HearthFuel && st.Qty > 0)
		}
	}
	if lit {
		bonus += def.HearthBonus
	}
	return bonus, nil
}

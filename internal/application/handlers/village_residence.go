package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// Residence: settlement.join and settlement.leave.
//
// A player has exactly ONE home (players.residence_city_id). A village's
// founder is its first resident (SettlementsHandler.Found); every other
// member of the group becomes one by pressing the join button, which moves
// their home from Support (or another village) to this village. Leaving
// sends them back to the neutral home city (config settlement.home_city_code).
//
// Rules, the same set for both directions:
//
//   - a two-step act: the command asks (village.ResidenceAsk), the confirm
//     button moves (village.ResidenceDone);
//   - refused while the player is travelling, at work on a shift, in jail,
//     in hospital or in the middle of a timed crime (the rules travel
//     already applies: a home is not moved from under a wage, a sentence or
//     a journey);
//   - refused during the cool-down (config settlement.residence_cooldown,
//     real time) that runs from the last change of residence, whatever
//     caused it (players.residence_since);
//   - a village head cannot leave: the head of a village lives in it.
//
// WHAT A MOVE DOES NOT TOUCH. Residence is not location: the player stays
// where they stand, and their properties, companies, orders and jobs keep
// working exactly as before. What follows the residence is: income tax (paid
// to the treasury of the city they live in), the jurisdiction chain whose
// elections they vote and stand in, the residence requirement of a job (a
// job in Support then needs a work permit, like any non-resident's), the
// country they count as a citizen of, and the settlement channel and roster
// of the village they live in (realtime). Each is read from residence_city_id
// on every use, so nothing needs migrating on a move.

// VillageJoinRequest is the payload of settlement.join and settlement.leave.
type VillageJoinRequest struct {
	// Confirm is village.ResidenceConfirm on the second press.
	Confirm string `json:"confirm,omitempty"`
	// Settlement names the village for a game client; in a group it is
	// ignored (the group's own village is used).
	Settlement string `json:"settlement,omitempty"`
}

func (r VillageJoinRequest) confirmed() bool { return r.Confirm == village.ResidenceConfirm }

// residenceTarget is the village a join is about.
func (h *VillageHandler) residenceTarget(ctx context.Context, tx application.Tx, meta envelope.Metadata, req VillageJoinRequest,
) (application.FoundedSettlement, error) {
	if meta.FromClient() {
		if req.Settlement == "" {
			return application.FoundedSettlement{}, refuseVillage(village.VillageNoSettlement)
		}
		return tx.Settlements().ByID(ctx, req.Settlement)
	}
	if !meta.InGroup() {
		return application.FoundedSettlement{}, refuseVillage(village.VillageNoSettlement)
	}
	return tx.Settlements().ByFoundingGroup(ctx, meta.TelegramChatID)
}

// residenceGate applies the rules that make a move impossible right now.
func (h *VillageHandler) residenceGate(ctx context.Context, tx application.Tx, playerID string, now time.Time) error {
	if _, err := tx.Travels().Active(ctx, playerID); err == nil {
		return application.ErrAlreadyTravelling
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return err
	}
	if err := refuseAtWork(ctx, tx, playerID); err != nil {
		return err
	}
	if err := RefuseDetained(ctx, tx, playerID, now); err != nil {
		return err
	}
	if h.residenceCooldown > 0 {
		since, err := tx.Property().ResidenceSince(ctx, playerID)
		if err != nil {
			return err
		}
		if since != nil {
			if left := since.Add(h.residenceCooldown).Sub(now); left > 0 {
				r := refuseVillage(village.VillageResidenceWait)
				r.remaining = left
				return r
			}
		}
	}
	return nil
}

// Join handles settlement.join.
func (h *VillageHandler) Join(ctx context.Context, meta envelope.Metadata, req VillageJoinRequest) (*presentation.Response, error) {
	lang := meta.Language
	var (
		view    village.ResidenceView
		confirm bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.residenceTarget(ctx, tx, meta, req)
		if err != nil {
			return err
		}
		view = village.ResidenceView{Village: s.Name, Cooldown: h.residenceCooldown, SettlementID: s.CityID}
		home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
		if err != nil {
			return err
		}
		if home == s.CityID {
			return refuseVillage(village.VillageAlreadyResident)
		}
		now := h.now()
		if !req.confirmed() {
			confirm = true
			return h.residenceGate(ctx, tx, p.ID, now)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			// A redelivered confirm: already done.
			view.Population, err = tx.Settlements().ResidentCount(ctx, s.CityID)
			return err
		}
		if err := h.residenceGate(ctx, tx, p.ID, now); err != nil {
			return err
		}
		if _, err := moveHome(ctx, tx, meta, p.ID, s.CityID, now, "join"); err != nil {
			return err
		}
		view.Population, err = tx.Settlements().ResidentCount(ctx, s.CityID)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	c := h.screen(meta, lang)
	if confirm {
		return village.ResidenceAsk(c, view), nil
	}
	return village.ResidenceDone(c, view), nil
}

// Leave handles settlement.leave: the player goes back to the neutral home
// city.
func (h *VillageHandler) Leave(ctx context.Context, meta envelope.Metadata, req VillageJoinRequest) (*presentation.Response, error) {
	lang := meta.Language
	var (
		view    village.ResidenceView
		confirm bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
		if err != nil {
			return err
		}
		var s application.FoundedSettlement
		if meta.FromClient() && req.Settlement == "" && home != "" {
			s, err = tx.Settlements().ByID(ctx, home)
		} else {
			s, err = h.residenceTarget(ctx, tx, meta, req)
		}
		if err != nil {
			if meta.FromClient() && stderrors.Is(err, application.ErrCityNotFound) {
				return refuseVillage(village.VillageNotResident)
			}
			return err
		}
		if home != s.CityID {
			return refuseVillage(village.VillageNotResident)
		}
		ps, err := tx.Settlements().ByPlayer(ctx, p.ID)
		if err != nil && !stderrors.Is(err, application.ErrCityNotFound) {
			return err
		}
		if ps.CityID == s.CityID && len(ps.Offices) > 0 {
			return refuseVillage(village.VillageHoldsOffice)
		}
		if h.homeCityCode == "" {
			return refuseVillage(village.VillageNoHome)
		}
		dest, err := h.cities.ByCode(ctx, h.homeCityCode)
		if err != nil || dest == nil {
			if err == nil || stderrors.Is(err, application.ErrCityNotFound) {
				return refuseVillage(village.VillageNoHome)
			}
			return err
		}
		view = village.ResidenceView{Leaving: true, Village: s.Name, Home: dest.Name, Cooldown: h.residenceCooldown, SettlementID: s.CityID}
		now := h.now()
		if !req.confirmed() {
			confirm = true
			return h.residenceGate(ctx, tx, p.ID, now)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}
		if err := h.residenceGate(ctx, tx, p.ID, now); err != nil {
			return err
		}
		if _, err := moveResidence(ctx, tx, meta, p.ID, dest.ID, now, "leave"); err != nil {
			return err
		}
		view.Population, err = tx.Settlements().ResidentCount(ctx, s.CityID)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	c := h.screen(meta, lang)
	if confirm {
		return village.ResidenceAsk(c, view), nil
	}
	return village.ResidenceDone(c, view), nil
}

package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// BarracksCode is the settlement building that is the base of an armed force
// (docs/research/2026-10-03-missing-prerequisites.md item 8: a standing force
// is quartered, stored and drilled in a barracks; ADR 0044 App. A rows 7, 124,
// 336 to 339 make the building itself the gate).
const BarracksCode = "barracks"

// hasBarracks says a barracks stands (complete) in the settlement. A content
// city (the neutral one and the old cities) has no settlement buildings to
// judge by and counts every building of its tier as standing.
func (s hubSettlement) hasBarracks() bool {
	if s.content {
		return true
	}
	if s.stands == nil {
		return false
	}
	return s.stands(content.AvailabilityBuilding{Code: BarracksCode})
}

// militaryOpenAt reads whether the army, the war board and everything military
// exist for a player standing where they stand. A player on the road has no
// settlement and sees none of it. Any error hides it: military is never
// offered by default.
func militaryOpenAt(ctx context.Context, uow application.UnitOfWork, cities application.CityRepository,
	src ContentSource, homeCityCode string, playerID string,
) (bool, error) {
	if src == nil || cities == nil {
		return false, nil
	}
	snap := src.Current()
	open := false
	err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByID(ctx, playerID)
		if err != nil {
			return err
		}
		if p.CityID == nil {
			return nil
		}
		city, err := cities.ByID(ctx, *p.CityID)
		if err != nil {
			return nil
		}
		here, err := judgeSettlementOf(ctx, tx, snap, city, homeCityCode)
		if err != nil {
			return err
		}
		open = here.hasBarracks()
		return nil
	})
	return open, err
}

// MilitaryOpen is the server-side gate of every military and war command: the
// settlement the player stands in must have a barracks standing. The screens
// do not list the army or the war board without it, and the commands refuse as
// if they did not exist, so a typed or replayed command gets nowhere either.
func (h *VillageHandler) MilitaryOpen(ctx context.Context, meta envelope.Metadata) error {
	if err := validPlayerRequest(meta); err != nil {
		return err
	}
	var playerID string
	if err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		playerID = p.ID
		return nil
	}); err != nil {
		return err
	}
	open, err := militaryOpenAt(ctx, h.uow, h.cities, h.content, h.homeCityCode, playerID)
	if err != nil {
		return err
	}
	if !open {
		return errors.NotFound("there is no such place here")
	}
	return nil
}

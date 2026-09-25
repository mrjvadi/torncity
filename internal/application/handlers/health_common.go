package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/health"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file is what hurts a player, shared by everything that can: a failed
// crime (CrimeHandler), an accident at work (JobsHandler), a war strike on
// the city they stand in (WarHandler) and a failed organised crime
// (FactionsHandler). Each calls injure inside its own unit of work, so the
// injury commits with the event that caused it — once, because that event
// settles once. Whether it hurts at all, and how much, the caller rolled with
// health.Injury.Roll on the event's own id; injure applies it
// (docs/adr/0023-health-missions-factions.md).

// hurt is one injury to apply.
type hurt struct {
	playerID string
	// cityID is where it happened: a patient is admitted there.
	cityID string
	// cause is one of application.Cause*, causeRef the row that caused it.
	cause, causeRef string
	damage          int
}

// injured is what an injury did, for the caller's screen or notice.
type injured struct {
	Damage int
	Health int
	Max    int
	// Stay is the admission it caused, nil when the player stays on their
	// feet.
	Stay *application.HospitalStay
}

// View is the injury as a screen shows it.
func (i *injured) View() *screens.InjuryView {
	if i == nil {
		return nil
	}
	v := &screens.InjuryView{Damage: i.Damage, Health: i.Health, Max: i.Max}
	if i.Stay != nil {
		v.Hospital = true
		v.EndsAt = i.Stay.EndsAt
	}
	return v
}

// payload is the injury as an event carries it.
func (i *injured) payload() map[string]any {
	if i == nil {
		return nil
	}
	p := map[string]any{"damage": i.Damage, "health": i.Health, "max_health": i.Max}
	if i.Stay != nil {
		p["hospital"], p["ends_at"] = true, i.Stay.EndsAt
	}
	return p
}

// HospitalActionPayload is the jsonb a stay writes onto its game_actions
// row, repeating the row's columns as every scheduled action does.
type HospitalActionPayload struct {
	ReferenceID string `json:"reference_id"`
	PlayerID    string `json:"player_id"`
}

// currentHealth reads a player's health as it stands at now: during a stay,
// where recovery has brought it; out of one, with the rest the game clock
// gave back since it was last counted. It changes nothing. restSince is the
// instant the rest is then counted from.
func currentHealth(ctx context.Context, tx application.Tx, def content.HealthDef, scale gametime.Scale,
	row application.Stats, now time.Time,
) (hp int, stay *application.HospitalStay, restSince time.Time, err error) {
	s, err := tx.Health().ActiveStay(ctx, row.PlayerID)
	switch {
	case err == nil && s.Status == application.StayAdmitted:
		return health.Recovering(s.HealthIn, s.HealthOut, s.AdmittedAt, s.EndsAt, now), s, s.EndsAt, nil
	case err != nil && !isSentinel(err, application.ErrNotHospitalised):
		return 0, nil, time.Time{}, err
	}
	since, err := tx.Health().RestSince(ctx, row.PlayerID)
	if err != nil {
		return 0, nil, time.Time{}, err
	}
	if since.IsZero() {
		return row.Health, nil, now, nil
	}
	hp, since = def.Rules().Rest(row.Health, row.MaxHealth, since, now, scale)
	return hp, nil, since, nil
}

// injure applies one injury in the caller's unit of work. The player's
// stats row is locked first (the same lock every activity takes), their
// health brought up to now, the damage taken — never below the floor — and,
// at or below the hospital line, they are admitted in the city it happened
// in: moved to its hospital place, a walk under way cut short, the
// discharge put on the game clock. An injury while already admitted
// re-admits them from the lower health. It returns nil when the content has
// no health, or damage is nothing.
func injure(ctx context.Context, tx application.Tx, snap *content.Snapshot, ids IDGenerator, scale gametime.Scale,
	meta envelope.Metadata, h hurt, now time.Time,
) (*injured, error) {
	def, ok := snap.Health()
	if !ok || h.damage <= 0 || h.playerID == "" {
		return nil, nil
	}
	rules := def.Rules()
	row, err := tx.Stats().EnsureDefaults(ctx, h.playerID, defaultStats(h.playerID, now))
	if err != nil {
		return nil, err
	}
	before, stay, since, err := currentHealth(ctx, tx, def, scale, *row, now)
	if err != nil {
		return nil, err
	}
	after, admit := rules.Hurt(before, h.damage)
	out := &injured{Damage: before - after, Health: after, Max: row.MaxHealth}
	next := *row
	next.Health = after
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = now
	}
	if stay != nil {
		// Hurt again in hospital: the stay starts over from here.
		if err := tx.Health().Discharge(ctx, stay.ID, now); err != nil && !isSentinel(err, application.ErrNotHospitalised) {
			return nil, err
		}
		admit, h.cityID = true, stay.CityID
	}
	if !admit {
		if err := tx.Stats().Save(ctx, next); err != nil {
			return nil, err
		}
		return out, tx.Health().SetRestSince(ctx, h.playerID, since)
	}
	if h.cityID == "" {
		// Nowhere to be admitted (on the road): they stay on their feet,
		// at the floor if need be.
		if err := tx.Stats().Save(ctx, next); err != nil {
			return nil, err
		}
		return out, tx.Health().SetRestSince(ctx, h.playerID, since)
	}
	s := application.HospitalStay{
		ID: ids.NewID(), PlayerID: h.playerID, CityID: h.cityID, Cause: h.cause, CauseRef: h.causeRef,
		Status: application.StayAdmitted, HealthIn: after, HealthOut: max(rules.Discharge(row.MaxHealth), after),
		AdmittedAt: now, EndsAt: now.Add(scale.RealWait(rules.Stay(after, row.MaxHealth))),
	}
	if s.GameActionID, err = scheduleDischarge(ctx, tx, ids, s, now); err != nil {
		return nil, err
	}
	if err := tx.Health().Admit(ctx, s); err != nil {
		return nil, err
	}
	if err := tx.Stats().Save(ctx, next); err != nil {
		return nil, err
	}
	// Recovery at rest begins when the stay ends.
	if err := tx.Health().SetRestSince(ctx, h.playerID, s.EndsAt); err != nil {
		return nil, err
	}
	// A patient lies at the hospital: a walk under way ends there.
	if walk, err := tx.Places().ActiveMove(ctx, h.playerID); err == nil {
		if err := tx.Places().CancelMove(ctx, walk.ID, now); err != nil && !isSentinel(err, application.ErrNotMoving) {
			return nil, err
		}
	} else if !isSentinel(err, application.ErrNotMoving) {
		return nil, err
	}
	if def.Place != "" {
		if err := tx.Places().Put(ctx, h.playerID, def.Place, now); err != nil {
			return nil, err
		}
	}
	out.Stay = &s
	var name string
	if p, err := tx.Players().GetByID(ctx, h.playerID); err == nil {
		name = shownName(p)
	} else if !isSentinel(err, application.ErrPlayerNotFound) {
		return nil, err
	}
	return out, appendDomainEvent(ctx, tx, meta, "health", "hospitalised", s.ID, map[string]any{
		"stay_id": s.ID, "player_id": h.playerID, "player_name": name, "city_id": s.CityID, "cause": s.Cause,
		"damage": out.Damage, "health": after, "max_health": row.MaxHealth, "ends_at": s.EndsAt,
	})
}

// scheduleDischarge puts a stay's discharge on the schedule.
func scheduleDischarge(ctx context.Context, tx application.Tx, ids IDGenerator, s application.HospitalStay, now time.Time) (string, error) {
	payload, err := json.Marshal(HospitalActionPayload{ReferenceID: s.ID, PlayerID: s.PlayerID})
	if err != nil {
		return "", err
	}
	id := ids.NewID()
	return id, tx.GameActions().Schedule(ctx, application.GameAction{
		ID: id, ActionType: application.HospitalDischargeActionType, ActorType: "player", ActorID: s.PlayerID,
		ReferenceType: application.HospitalReference, ReferenceID: s.ID, Payload: payload,
		StartedAt: now, FinishAt: s.EndsAt,
	})
}

// rollInjury rolls an injury on an event's id (and a coordinate, one per
// player of the event) and applies it; nil when it does not happen.
func rollInjury(ctx context.Context, tx application.Tx, snap *content.Snapshot, ids IDGenerator, scale gametime.Scale,
	meta envelope.Metadata, inj health.Injury, eventID string, coord int64, h hurt, now time.Time,
) (*injured, error) {
	dmg, ok := inj.Roll(health.Seed(eventID), coord)
	if !ok {
		return nil, nil
	}
	h.damage = dmg
	return injure(ctx, tx, snap, ids, scale, meta, h, now)
}

// hospitalised returns the player's stay while it keeps them in hospital at
// now, nil otherwise. It takes no lock.
func hospitalised(ctx context.Context, tx application.Tx, playerID string, now time.Time) (*application.HospitalStay, error) {
	s, err := tx.Health().ActiveStay(ctx, playerID)
	switch {
	case isSentinel(err, application.ErrNotHospitalised):
		return nil, nil
	case err != nil:
		return nil, err
	case !s.Admitted(now):
		return nil, nil
	}
	return s, nil
}

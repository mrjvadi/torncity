package handlers

import (
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The Activities hub, the work home and the health home (ADR 0038 sections 3,
// 4.1 and 4.3). They read what the settlement the player stands in or belongs
// to has, and list or show only that: an activity nothing offers is not
// mentioned, and one that is offered but has nothing to start says why and
// names the one step that helps. All three only read; the writes they lead to
// are the commands that already exist (settlement.work, settlement.labor.take,
// settlement.home.rest, life.sleep).

// WithActivities sets the Activities hub's rules (the neutral city).
func (h *VillageHandler) WithActivities(r ActivityRules) *VillageHandler {
	h.activity = r
	return h
}

// placeOf is the settlement a player stands in as the activities name it. A
// player on the road, standing in no city, has the zero place.
func (h *VillageHandler) placeOf(city *application.City) plife.ActivityPlace {
	if city == nil {
		return plife.ActivityPlace{}
	}
	return plife.ActivityPlace{Code: city.Code, Name: city.Name, Tier: tierStage(city.Tier),
		Neutral: h.homeCityCode != "" && city.Code == h.homeCityCode}
}

// whereIs is the city a player stands in, nil while they are on the road.
func (h *VillageHandler) whereIs(ctx context.Context, p *application.Player) (*application.City, error) {
	if p.CityID == nil {
		return nil, nil
	}
	c, err := h.cities.ByID(ctx, *p.CityID)
	if stderrors.Is(err, application.ErrCityNotFound) {
		return nil, nil
	}
	return c, err
}

// energyOf is the viewer's energy now.
func (h *VillageHandler) energyOf(ctx context.Context, tx application.Tx, p *application.Player) (energy, most int, err error) {
	row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, h.now()))
	if err != nil {
		return 0, 0, err
	}
	st, _ := regenerateEnergy(*row, h.now())
	return st.Energy, st.MaxEnergy, nil
}

// ActivitiesHub handles activities.hub: the activities the player is offered
// where they stand. Work, learning, health, missions and the rankings are every
// player's; crime only from crime.min_level and in a settlement that has
// reached crime.min_stage, and never in the neutral city.
func (h *VillageHandler) ActivitiesHub(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	var view plife.ActivitiesHubView
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		city, err := h.whereIs(ctx, p)
		if err != nil {
			return err
		}
		row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, h.now()))
		if err != nil {
			return err
		}
		view.Place = h.placeOf(city)
		view.Entries = []plife.ActivityEntry{
			{Code: plife.ActivityWork, Command: "work.home"},
			{Code: plife.ActivityLearn, Command: "education.list"},
			{Code: plife.ActivityHealth, Command: "health.home"},
			// bodyweight exercise needs nothing; the screen says what a training ground adds
			{Code: plife.ActivityTraining, Command: "training.home"},
		}
		rules := h.activity
		rules.NeutralCity = h.homeCityCode
		verdict, err := rules.crimeListing(ctx, tx, h.content.Current(), row.Level, city)
		if err != nil {
			return err
		}
		if verdict.Empty == "" {
			view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.ActivityCrime, Command: "crime.hub"})
		}
		view.Entries = append(view.Entries,
			plife.ActivityEntry{Code: plife.ActivityMissions, Command: "mission.board"},
			plife.ActivityEntry{Code: plife.ActivityRankings, Command: "life.top"})
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return plife.ActivitiesHub(h.screen(meta, lang), view), nil
}

// WorkHome handles work.home: what the player is working at, the jobs on the
// village board and the workplaces they can start a shift at, or why there is
// nothing to start and the one step that helps. A player who belongs to no
// village has the careers of the city for work: the view says so.
func (h *VillageHandler) WorkHome(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	var view village.WorkHomeView
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		if view.Energy, view.MaxEnergy, err = h.energyOf(ctx, tx, p); err != nil {
			return err
		}
		s, err := h.settlementOf(ctx, tx, meta)
		if stderrors.Is(err, application.ErrCityNotFound) {
			view.Empty = village.WorkEmptyNoSettlement
			return nil
		}
		if err != nil {
			return err
		}
		board, err := h.boardView(ctx, tx, p, s)
		if err != nil {
			return err
		}
		places, err := h.workView(ctx, tx, p, s)
		if err != nil {
			return err
		}
		view.Place = village.WorkHomePlace{Code: s.Code, Name: s.Name, Tier: tierStage(s.Tier)}
		view.Resident = board.Resident
		view.Working, view.Jobs, view.Workplaces, view.Market = board.Working, board.Jobs, places.Places, board.Market
		view.IsHead = hasPermission(ctx, tx, s, p.ID, charter.PublicBuild)
		switch {
		case !view.Resident:
			view.Empty, view.Next = village.WorkEmptyNotResident, village.WorkNextJoin
		case len(view.Jobs) == 0 && len(view.Workplaces) == 0:
			view.Empty = village.WorkEmptyNoJobs
			view.Next = village.WorkNextAskHead
			if view.IsHead {
				view.Next = village.WorkNextBuild
			}
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.WorkHome(h.screen(meta, lang), view), nil
}

// HealthHome handles health.home: the player's health, resting and sleeping,
// the places of care standing where they are and, in a village or town that has
// no hospital, the neutral city to be sent to. A hospital stay in progress is
// shown first.
func (h *VillageHandler) HealthHome(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	var view plife.HealthHomeView
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		now := h.now()
		row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
		if err != nil {
			return err
		}
		view.Health, view.MaxHealth = row.Health, row.MaxHealth
		city, err := h.whereIs(ctx, p)
		if err != nil {
			return err
		}
		view.Place = h.placeOf(city)
		stay, err := hospitalised(ctx, tx, p.ID, now)
		if err != nil {
			return err
		}
		if stay != nil {
			view.Admitted = &plife.HealthStay{Remaining: countdownTo(stay.EndsAt, now), EndsAt: stay.EndsAt}
		}
		if city == nil {
			return nil
		}
		s, err := tx.Settlements().ByID(ctx, city.ID)
		switch {
		case stderrors.Is(err, application.ErrCityNotFound):
			// a content city (the neutral one): its hospital is health.hospital
			return nil
		case err != nil:
			return err
		}
		if err := h.fillHealthHere(ctx, tx, p, s, &view, now); err != nil {
			return err
		}
		if view.Place.Tier != application.TierCity && h.homeCityCode != "" {
			if home, err := h.cities.ByCode(ctx, h.homeCityCode); err == nil && home != nil {
				view.Refer = &presentation.Named{Code: home.Code, Name: home.Name}
			}
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return plife.HealthHome(h.screen(meta, lang), view), nil
}

// fillHealthHere adds what the settlement the player stands in has for their
// health: the player's own home to rest at and the places of care that stand.
func (h *VillageHandler) fillHealthHere(ctx context.Context, tx application.Tx, p *application.Player,
	s application.FoundedSettlement, view *plife.HealthHomeView, now time.Time,
) error {
	snap := h.content.Current()
	pb, _, err := h.homeOf(ctx, tx, s, p.ID)
	if err != nil {
		return err
	}
	if pb != nil {
		view.Rest.Has = true
		view.Rest.CanRest = true
		if pb.LastRestAt != nil {
			if ready := pb.LastRestAt.Add(h.citizen.HomeRestCooldown); ready.After(now) {
				view.Rest.CanRest, view.Rest.Wait = false, ready.Sub(now)
			}
		}
	}
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return err
	}
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok || d.Def().Role != "health" {
			continue
		}
		kind := plife.FacilityHealthHouse
		if d.Def().Tier >= 2 {
			kind = plife.FacilityClinic
		}
		view.Facilities = append(view.Facilities, plife.HealthFacility{Kind: kind, Building: named(d.Code, d.Name)})
	}
	if len(view.Facilities) == 0 {
		view.Empty = plife.HealthEmptyNoFacility
	}
	return nil
}

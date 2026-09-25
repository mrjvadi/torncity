package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/faction"
	"github.com/mrjvadi/torncity/internal/domain/health"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds a faction's organised crimes: one of the organised crimes
// of factions.yml, planned by a member whose rank allows at a place where it
// can be committed; members standing there join its crew while it gathers;
// launched once the crew is large enough — each crew member still there
// paying the crime's nerve — it runs on the game clock and is settled once,
// from the scheduler, through the crime engine's own rules
// (crime.Resolve): the chance from the crew's best skills and its size, the
// take from the NPC economy under the crime's cap and the economy's daily
// cap, an arrest jailing and fining every crew member, a failure maybe
// hurting them. The faction's bank takes its cut, and the rest is split by
// rank (faction.Split).

// operationLine reads an organised crime for a screen.
func (h *FactionsHandler) operationLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FactionDef,
	op application.FactionOperation, now time.Time,
) (screens.FactionOperationLine, error) {
	v := screens.FactionOperationLine{No: op.No, Status: op.Status, Place: placeNamed(snap, op.Place), ChanceBPS: op.ChanceBPS}
	if od, _, ok := def.Organised(op.Crime, snap.CrimeTiers()); ok {
		v.Crime = named(od.Code, od.Name)
		v.Min, v.Max, v.Nerve = od.Crew.Min, od.Crew.Max, od.Nerve
	} else {
		v.Crime = named(op.Crime, op.Crime)
	}
	city, err := h.cities.ByID(ctx, op.CityID)
	if err != nil {
		return v, err
	}
	v.CityCode, v.City = city.Code, city.Name
	crew, err := tx.Factions().Crew(ctx, op.ID)
	if err != nil {
		return v, err
	}
	for _, c := range crew {
		who, err := playerNamed(ctx, tx, c.PlayerID)
		if err != nil {
			return v, err
		}
		v.Crew = append(v.Crew, screens.FactionMemberLine{Player: who, Rank: c.Rank})
	}
	switch {
	case op.Status == application.HeistGathering:
		v.Left, v.At = op.GatherUntil.Sub(now), op.GatherUntil
		v.Expired = !now.Before(op.GatherUntil)
	case op.ResolvesAt != nil:
		v.Left, v.At = op.ResolvesAt.Sub(now), *op.ResolvesAt
	}
	return v, nil
}

// openOperation reads a faction's organised crime still open, closing a
// gathering whose time ran out (called off: nobody paid anything yet).
func (h *FactionsHandler) openOperation(ctx context.Context, tx application.Tx, factionID string, now time.Time) (*application.FactionOperation, error) {
	op, err := tx.Factions().OpenOperation(ctx, factionID)
	if isSentinel(err, application.ErrNoOperation) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if op, err = tx.Factions().Operation(ctx, op.ID); err != nil {
		return nil, err
	}
	if op.Status == application.HeistGathering && !now.Before(op.GatherUntil) {
		op.Status = application.HeistCalledOff
		if err := tx.Factions().SaveOperation(ctx, *op); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return op, nil
}

// CrimeBoard handles faction.crime: the organised crime under way or
// gathering, and the ones a member whose rank plans may plan.
func (h *FactionsHandler) CrimeBoard(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.board(ctx, meta, "")
}

func (h *FactionsHandler) board(ctx context.Context, meta envelope.Metadata, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.FactionCrimeView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		mb, err := h.membership(ctx, tx, p.ID, false)
		if err != nil {
			return err
		}
		now := h.now()
		charter := def.Charter()
		view = screens.FactionCrimeView{Ref: factionRef(*mb.faction), Notice: notice,
			CanPlan: charter.Can(mb.rank, faction.Plan), CanLaunch: charter.Can(mb.rank, faction.Launch),
			CanJoin: charter.Can(mb.rank, faction.Join), CutBPS: int(def.CrimeCutBPS)}
		op, err := tx.Factions().OpenOperation(ctx, mb.faction.ID)
		switch {
		case err == nil:
			line, err := h.operationLine(ctx, tx, snap, def, *op, now)
			if err != nil {
				return err
			}
			if !line.Expired {
				view.Operation = &line
				crew, err := tx.Factions().Crew(ctx, op.ID)
				if err != nil {
					return err
				}
				for _, c := range crew {
					view.InCrew = view.InCrew || c.PlayerID == p.ID
				}
			}
		case !isSentinel(err, application.ErrNoOperation):
			return err
		}
		for _, od := range def.OrganisedCrimes {
			line := screens.FactionPlanLine{Crime: named(od.Code, od.Name), Min: od.Crew.Min, Max: od.Crew.Max,
				Nerve: od.Nerve, MinLevel: od.MinLevel, Duration: h.scale.RealWait(durationOf(od.Duration))}
			for _, v := range od.Venues {
				line.Places = append(line.Places, placeNamed(snap, v))
			}
			view.Crimes = append(view.Crimes, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.FactionCrime(h.screen(meta, lang), view), nil
}

// heistNeed names the organised crime on a "not here" refusal, whose walk
// button opens the board on arrival.
func heistNeed(err error, od content.OrganisedCrimeDef) error {
	var n *notHere
	if stderrors.As(err, &n) {
		n.view.Crime = named(od.Code, od.Name)
	}
	return thenFor(err, "faction.crime")
}

// durationOf parses a validated content duration.
func durationOf(raw string) time.Duration {
	d, _ := time.ParseDuration(raw)
	return d
}

// standsAt refuses a player who is not standing, free, at a place of a
// city: travelling, walking, detained (jail, a timed crime, hospital), or
// elsewhere. It returns where they stand.
func (h *FactionsHandler) standsAt(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	now time.Time,
) (whereabouts, error) {
	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		return whereabouts{}, application.ErrAlreadyTravelling
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return whereabouts{}, err
	}
	if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
		return whereabouts{}, err
	}
	w, err := locate(ctx, tx, h.cities, snap, p)
	if err != nil {
		return w, err
	}
	if w.city == nil {
		return w, application.ErrCityNotFound
	}
	return w, refuseWalking(w, snap, now)
}

// Plan handles faction.plan: planning an organised crime where the planner
// stands, for a member whose rank plans. The planner is its first crew
// member; the others have until the gathering ends to join.
func (h *FactionsHandler) Plan(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	replayed := false
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		mb, err := h.membership(ctx, tx, p.ID, true)
		if err != nil {
			return err
		}
		if err := mb.may(def, faction.Plan); err != nil {
			return err
		}
		od, cr, ok := def.Organised(req.Crime, snap.CrimeTiers())
		if !ok {
			return refuseFaction(screens.FactionRefusedNoSuchCrime)
		}
		now := h.now()
		open, err := h.openOperation(ctx, tx, mb.faction.ID, now)
		if err != nil {
			return err
		}
		if open != nil {
			return refuseFaction(screens.FactionRefusedOperationOpen)
		}
		stand, err := loadStanding(ctx, tx, p, now)
		if err != nil {
			return err
		}
		if stand.stats.Level < od.MinLevel {
			r := refuseFaction(screens.FactionRefusedLevel)
			r.view.Level = od.MinLevel
			return r
		}
		w, err := h.standsAt(ctx, tx, snap, p, now)
		if err != nil {
			return err
		}
		if w.placed() && !cr.CommittableAt(w.here.Code) {
			target, ok := w.cmap.Find(od.Venues[0])
			if !ok {
				return refuseFaction(screens.FactionRefusedNoPlaceHere)
			}
			return heistNeed(needAt(w, snap, target, "place.need.heist", nil, h.scale, now), od)
		}
		here := w.here.Code
		op, err := tx.Factions().Plan(ctx, application.FactionOperation{ID: h.ids.NewID(), FactionID: mb.faction.ID,
			Crime: od.Code, CityID: w.city.ID, Place: here, PlannedBy: p.ID,
			GatherUntil: now.Add(h.scale.RealWait(def.GatherTime())), ContentVersion: snap.Version(), CreatedAt: now})
		if isSentinel(err, application.ErrOperationOpen) {
			return refuseFaction(screens.FactionRefusedOperationOpen)
		}
		if err != nil {
			return err
		}
		if err := tx.Factions().AddCrew(ctx, application.CrewMember{OperationID: op.ID, PlayerID: p.ID,
			Rank: string(mb.rank), JoinedAt: now}); err != nil {
			return err
		}
		return appendFactionEvent(ctx, tx, meta, "planned", op.ID, groupFields(*mb.faction, map[string]any{
			"no": op.No, "crime": od.Code, "crime_name": od.Name, "place": here, "place_name": placeNamed(snap, here).Name,
			"player_name": shownName(p), "gather_until": op.GatherUntil, "min": od.Crew.Min}))
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	_ = replayed
	return h.board(ctx, meta, screens.FactionNoticePlanned)
}

// Join handles faction.join: a member standing at the planned crime's
// place joins its crew while it gathers.
func (h *FactionsHandler) Join(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		mb, err := h.membership(ctx, tx, p.ID, true)
		if err != nil {
			return err
		}
		if err := mb.may(def, faction.Join); err != nil {
			return err
		}
		now := h.now()
		op, err := h.openOperation(ctx, tx, mb.faction.ID, now)
		if err != nil {
			return err
		}
		if op == nil || op.Status != application.HeistGathering {
			return refuseFaction(screens.FactionRefusedNoOperation)
		}
		od, _, ok := def.Organised(op.Crime, snap.CrimeTiers())
		if !ok {
			return refuseFaction(screens.FactionRefusedNoSuchCrime)
		}
		crew, err := tx.Factions().Crew(ctx, op.ID)
		if err != nil {
			return err
		}
		if len(crew) >= od.Crew.Max {
			return refuseFaction(screens.FactionRefusedCrewFull)
		}
		w, err := h.standsAt(ctx, tx, snap, p, now)
		if err != nil {
			return err
		}
		if w.city.ID != op.CityID {
			return refuseFaction(screens.FactionRefusedElsewhere)
		}
		if w.placed() && w.here.Code != op.Place {
			target, ok := w.cmap.Find(op.Place)
			if !ok {
				return refuseFaction(screens.FactionRefusedElsewhere)
			}
			return heistNeed(needAt(w, snap, target, "place.need.heist", nil, h.scale, now), od)
		}
		if err := tx.Factions().AddCrew(ctx, application.CrewMember{OperationID: op.ID, PlayerID: p.ID,
			Rank: string(mb.rank), JoinedAt: now}); err != nil {
			if isSentinel(err, application.ErrAlreadyInCrew) {
				return nil
			}
			return err
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.board(ctx, meta, screens.FactionNoticeJoined)
}

// CallOff handles faction.calloff: a member whose rank plans calls off a
// crime still gathering. Nobody has paid anything yet.
func (h *FactionsHandler) CallOff(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		mb, err := h.membership(ctx, tx, p.ID, true)
		if err != nil {
			return err
		}
		if err := mb.may(def, faction.Plan); err != nil {
			return err
		}
		op, err := h.openOperation(ctx, tx, mb.faction.ID, h.now())
		if err != nil {
			return err
		}
		if op == nil || op.Status != application.HeistGathering {
			return refuseFaction(screens.FactionRefusedNoOperation)
		}
		op.Status = application.HeistCalledOff
		return tx.Factions().SaveOperation(ctx, *op)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.board(ctx, meta, screens.FactionNoticeCalledOff)
}

// Launch handles faction.launch: a member whose rank launches sets the
// gathered crew going. Each crew member must still be a member, standing at
// the place, free, with the crime's nerve; who is not is left behind, and a
// crew left too small does not go. The nerve is paid now; the chance is
// fixed now from the crew's best skills, its highest heat, the place's
// security and its size; the end is put on the game clock.
func (h *FactionsHandler) Launch(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		mb, err := h.membership(ctx, tx, p.ID, true)
		if err != nil {
			return err
		}
		if err := mb.may(def, faction.Launch); err != nil {
			return err
		}
		now := h.now()
		op, err := h.openOperation(ctx, tx, mb.faction.ID, now)
		if err != nil {
			return err
		}
		if op == nil || op.Status != application.HeistGathering {
			return refuseFaction(screens.FactionRefusedNoOperation)
		}
		od, cr, ok := def.Organised(op.Crime, snap.CrimeTiers())
		if !ok {
			return refuseFaction(screens.FactionRefusedNoSuchCrime)
		}
		crew, err := tx.Factions().Crew(ctx, op.ID)
		if err != nil {
			return err
		}
		// Profiles in id order: the lock order every crime command keeps.
		sort.Slice(crew, func(i, j int) bool { return crew[i].PlayerID < crew[j].PlayerID })
		type going struct {
			c       application.CrewMember
			profile *application.CriminalProfile
			skills  []application.Skill
		}
		var ready []going
		var dropped []application.CrewMember
		for _, c := range crew {
			ok, prof, skills, err := h.canGo(ctx, tx, snap, c, *op, od.Nerve, now)
			if err != nil {
				return err
			}
			if ok {
				ready = append(ready, going{c: c, profile: prof, skills: skills})
			} else {
				dropped = append(dropped, c)
			}
		}
		if len(ready) < od.Crew.Min {
			r := refuseFaction(screens.FactionRefusedCrewShort)
			r.view.Need, r.view.Have = od.Crew.Min, len(ready)
			return r
		}
		for _, c := range dropped {
			if err := tx.Factions().RemoveCrew(ctx, op.ID, c.PlayerID); err != nil {
				return err
			}
		}
		best := map[player.SkillCode]int{}
		heat := 0
		for _, g := range ready {
			nerve, err := h.crime.rules.Nerve.Spend(crime.Nerve{Current: g.profile.Nerve, UpdatedAt: g.profile.NerveUpdatedAt}, od.Nerve)
			if err != nil {
				return errors.Internal(err)
			}
			g.profile.Nerve, g.profile.NerveUpdatedAt, g.profile.UpdatedAt = nerve.Current, nerve.UpdatedAt, now
			if err := tx.Crime().SaveProfile(ctx, *g.profile); err != nil {
				return err
			}
			heat = max(heat, g.profile.Heat)
			for _, s := range g.skills {
				code := player.SkillCode(s.Code)
				best[code] = max(best[code], s.Level)
			}
		}
		var skills []player.Skill
		for code, level := range best {
			skills = append(skills, player.Skill{Code: code, Level: level})
		}
		sort.Slice(skills, func(i, j int) bool { return skills[i].Code < skills[j].Code })
		security := 0
		for _, v := range snap.Venues() {
			if v.Code == op.Place {
				security = v.Security
			}
		}
		chance := cr.SuccessChance(crime.Situation{Skills: skills, Heat: heat, Victim: crime.TargetNPC, VenueSecurity: security})
		chance = min(chance+od.Crew.BPSPerMember*(len(ready)-1), crime.ChanceCeilingBPS)
		ends := now.Add(h.scale.RealWait(cr.Duration))
		payload, err := json.Marshal(CrimeActionPayload{ReferenceID: op.ID, PlayerID: op.PlannedBy})
		if err != nil {
			return err
		}
		actionID := h.ids.NewID()
		if err := tx.GameActions().Schedule(ctx, application.GameAction{ID: actionID,
			ActionType: application.FactionCrimeActionType, ActorType: "player", ActorID: op.PlannedBy,
			ReferenceType: application.FactionOperationReference, ReferenceID: op.ID, Payload: payload,
			StartedAt: now, FinishAt: ends}); err != nil {
			return err
		}
		op.Status, op.ChanceBPS, op.GameActionID = application.HeistRunning, chance, actionID
		op.LaunchedAt, op.ResolvesAt = &now, &ends
		if err := tx.Factions().SaveOperation(ctx, *op); err != nil {
			return err
		}
		return appendFactionEvent(ctx, tx, meta, "launched", op.ID, groupFields(*mb.faction, map[string]any{
			"no": op.No, "crime": od.Code, "crime_name": od.Name, "crew": len(ready), "ends_at": ends}))
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.board(ctx, meta, screens.FactionNoticeLaunched)
}

// canGo says whether a crew member can go on the job now: still a member of
// the faction, standing at its place, free, and with the nerve. Their
// profile comes back locked, their nerve brought up to now.
func (h *FactionsHandler) canGo(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.CrewMember,
	op application.FactionOperation, nerve int, now time.Time,
) (bool, *application.CriminalProfile, []application.Skill, error) {
	m, err := tx.Factions().Membership(ctx, c.PlayerID)
	if isSentinel(err, application.ErrNotInFaction) || (err == nil && m.FactionID != op.FactionID) {
		return false, nil, nil, nil
	}
	if err != nil {
		return false, nil, nil, err
	}
	p, err := tx.Players().GetByID(ctx, c.PlayerID)
	if err != nil {
		return false, nil, nil, err
	}
	w, err := h.standsAt(ctx, tx, snap, p, now)
	if err != nil {
		// On the road, detained, walking or nowhere: left behind. A fault
		// is a fault.
		var nh *notHere
		if stderrors.As(err, &nh) || errors.CodeOf(err) != errors.CodeInternal {
			return false, nil, nil, nil
		}
		return false, nil, nil, err
	}
	if w.city.ID != op.CityID || (w.placed() && w.here.Code != op.Place) {
		return false, nil, nil, nil
	}
	if a, err := tx.Crime().ActiveAttempt(ctx, p.ID); err == nil && a != nil {
		return false, nil, nil, nil
	}
	prof, err := h.crime.profileAt(ctx, tx, p.ID, now)
	if err != nil {
		return false, nil, nil, err
	}
	if prof.Nerve < nerve {
		return false, nil, nil, nil
	}
	skills, err := tx.Skills().List(ctx, p.ID)
	if err != nil {
		return false, nil, nil, err
	}
	return true, prof, skills, nil
}

// seededDice is the crime engine's dice for an organised crime: every roll
// drawn from the operation's own id and the roll's order, so a replay of the
// settlement rolls the same.
type seededDice struct {
	seed int64
	i    int64
}

func (d *seededDice) Roll(n int64) int64 {
	d.i++
	if n <= 0 {
		return 0
	}
	return int64(health.Mix(d.seed, d.i) % uint64(n))
}

// Resolve settles an organised crime whose time is up. It arrives from the
// SCHEDULER and settles once: the key is derived from the operation and its
// action, the operation is locked, and only one still running and still
// ended by this action moves.
func (h *FactionsHandler) Resolve(ctx context.Context, meta envelope.Metadata, req FactionScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	actorID, opID, err := req.ids()
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(actorID, meta.Command, opID+":"+req.ActionID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), actorID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		op, err := tx.Factions().Operation(ctx, opID)
		if isSentinel(err, application.ErrNoOperation) {
			return nil
		}
		if err != nil {
			return err
		}
		if op.Status != application.HeistRunning || (req.ActionID != "" && op.GameActionID != req.ActionID) {
			return nil
		}
		if op.ResolvesAt == nil || now.Before(*op.ResolvesAt) {
			return errors.Internal(stderrors.New("handlers: an organised crime resolved before its end"))
		}
		f, err := tx.Factions().Lock(ctx, op.FactionID)
		if err != nil {
			return err
		}
		return h.settle(ctx, tx, snap, meta, f, op, now)
	})
}

// settle rolls an organised crime and applies everything it decides.
func (h *FactionsHandler) settle(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	f *application.Faction, op *application.FactionOperation, now time.Time,
) error {
	def, _ := snap.Faction()
	od, cr, ok := def.Organised(op.Crime, snap.CrimeTiers())
	if !ok {
		// The crime left the content under a running job: it is off, and
		// the nerve spent is the whole of its cost.
		op.Status, op.ResolvedAt = application.HeistEscaped, &now
		return tx.Factions().SaveOperation(ctx, *op)
	}
	city, err := h.cities.ByID(ctx, op.CityID)
	if err != nil {
		return err
	}
	pol, err := h.crime.readJusticePolicy(ctx, *city)
	if err != nil {
		return err
	}
	crew, err := tx.Factions().Crew(ctx, op.ID)
	if err != nil {
		return err
	}
	sort.Slice(crew, func(i, j int) bool { return crew[i].PlayerID < crew[j].PlayerID })
	paid, err := tx.Crime().LockNPCProceeds(ctx, now, now)
	if err != nil {
		return err
	}
	a := crime.Attempt{Crime: cr, Victim: crime.TargetNPC, Chance: op.ChanceBPS, Policy: pol.JusticePolicy,
		NPCAllowance: money.FromMinor(max(h.crime.rules.NPCDailyCap.Minor()-paid, 0))}
	out, err := crime.Resolve(a, &seededDice{seed: health.Seed(op.ID)})
	if err != nil {
		return errors.Internal(err)
	}
	ranks := make([]faction.Rank, len(crew))
	for i, c := range crew {
		ranks[i] = faction.Rank(c.Rank)
	}
	shares := make([]int64, len(crew))
	switch out.Result {
	case crime.Succeeded:
		op.Status = application.HeistSucceeded
		take := out.Take.Minor()
		cut, each := faction.Split(take, def.CrimeCutBPS, ranks, def.ShareWeights())
		shares = each
		op.Take, op.FactionCut = take, cut
		if take > 0 {
			entries := []application.LedgerEntry{{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-take)}}
			if cut > 0 {
				treasury, err := tx.Ledger().AccountFor(ctx, application.AccountFactionTreasury, f.ID)
				if err != nil {
					return err
				}
				entries = append(entries, application.LedgerEntry{AccountID: treasury.ID, Amount: money.FromMinor(cut)})
			}
			for i, c := range crew {
				if each[i] <= 0 {
					continue
				}
				cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, c.PlayerID)
				if err != nil {
					return err
				}
				entries = append(entries, application.LedgerEntry{AccountID: cash.ID, Amount: money.FromMinor(each[i])})
			}
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonCrimeProceeds,
				ReferenceType: application.FactionOperationReference, ReferenceID: op.ID, Entries: entries,
				CreatedAt: now}); err != nil {
				return err
			}
			if err := tx.Crime().AddNPCProceeds(ctx, now, take, now); err != nil {
				return err
			}
		}
	case crime.Escaped:
		op.Status = application.HeistEscaped
	default:
		op.Status = application.HeistCaught
	}
	op.ResolvedAt = &now
	if err := tx.Factions().SaveOperation(ctx, *op); err != nil {
		return err
	}
	for i, c := range crew {
		if err := tx.Factions().SetShare(ctx, op.ID, c.PlayerID, shares[i]); err != nil {
			return err
		}
		if err := h.settleMember(ctx, tx, snap, meta, f, op, od, out, c, i, shares[i], city, now); err != nil {
			return err
		}
	}
	return appendFactionEvent(ctx, tx, meta, "crime_resolved", op.ID, groupFields(*f, map[string]any{
		"no": op.No, "crime": od.Code, "crime_name": od.Name, "result": op.Status, "crew": len(crew),
		"city_id": op.CityID}))
}

// settleMember applies an organised crime's outcome to one crew member:
// their XP, criminal XP, skill XP and heat, an arrest's sentence and fine,
// a failure's injury, and their private notice with their share.
func (h *FactionsHandler) settleMember(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	f *application.Faction, op *application.FactionOperation, od content.OrganisedCrimeDef, out crime.Outcome,
	c application.CrewMember, index int, share int64, city *application.City, now time.Time,
) error {
	prof, err := h.crime.profileAt(ctx, tx, c.PlayerID, now)
	if err != nil {
		return err
	}
	prof.Attempts++
	heat := h.crime.rules.Heat.Add(crime.Heat{Level: prof.Heat, UpdatedAt: prof.HeatUpdatedAt}, out.Heat)
	prof.Heat, prof.HeatUpdatedAt = heat.Level, heat.UpdatedAt
	prof.CriminalXP += out.CriminalXP
	notice := map[string]any{"player_id": c.PlayerID, "no": op.No, "crime": od.Code, "crime_name": od.Name,
		"result": op.Status, "share": share, "take": op.Take, "cut": op.FactionCut, "faction_code": f.Code,
		"faction_name": f.Name, "xp": out.XP}
	switch out.Result {
	case crime.Succeeded:
		prof.Successes++
	case crime.Caught:
		prof.Arrests++
		s, err := h.crime.jail(ctx, tx, meta, c.PlayerID, city.ID, "", application.SentenceForArrest, out.JailTerm, now)
		if err != nil {
			return err
		}
		notice["jail_seconds"], notice["jail_ends_at"] = int64(s.EndsAt.Sub(now)/time.Second), s.EndsAt
		if out.Fine.Minor() > 0 {
			cash, bankAcct, err := playerAccounts(ctx, tx.Ledger(), c.PlayerID)
			if err != nil {
				return err
			}
			split := crime.Settle(money.Amount{}, out.Fine, cash.Balance, bankAcct.Balance)
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
			if err != nil {
				return err
			}
			if _, err := h.crime.post(ctx, tx, application.ReasonCrimeFine, application.FactionOperationReference, op.ID,
				legs(cash, bankAcct, split.FineFromCash, split.FineFromBank, treasury.ID)); err != nil {
				return err
			}
			prof.UnpaidFines += split.FineShortfall.Minor()
			notice["fine"], notice["fine_paid"] = out.Fine.Minor(), split.FinePaid().Minor()
		}
	}
	prof.UpdatedAt = now
	if err := tx.Crime().SaveProfile(ctx, *prof); err != nil {
		return err
	}
	if out.XP > 0 {
		row, err := tx.Stats().EnsureDefaults(ctx, c.PlayerID, defaultStats(c.PlayerID, now))
		if err != nil {
			return err
		}
		next, _ := domainStats(*row).AddXP(moodXP(snap, row.Happiness, out.XP))
		stats := storedStats(*row, next)
		if err := tx.Stats().Save(ctx, stats); err != nil {
			return err
		}
	}
	if len(out.SkillXP) > 0 {
		skills, err := tx.Skills().List(ctx, c.PlayerID)
		if err != nil {
			return err
		}
		awards := make([]skillAward, 0, len(out.SkillXP))
		for _, s := range out.SkillXP {
			awards = append(awards, skillAward{Skill: player.SkillCode(s.Skill), XP: s.XP})
		}
		if _, err := awardSkillXP(ctx, tx, snap, c.PlayerID, skills, awards, now); err != nil {
			return err
		}
	}
	if out.Result != crime.Succeeded && od.Failure.Injury != nil {
		inj, err := rollInjury(ctx, tx, snap, h.ids, h.scale, meta, od.Failure.Injury.Injury(), op.ID, int64(index+1),
			hurt{playerID: c.PlayerID, cityID: city.ID, cause: application.CauseFactionCrime, causeRef: op.ID}, now)
		if err != nil {
			return err
		}
		if inj != nil {
			notice["injury"] = inj.payload()
		}
	}
	return appendFactionEvent(ctx, tx, meta, "crime_settled", op.ID, notice)
}

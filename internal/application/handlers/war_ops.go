package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/health"
	"github.com/mrjvadi/torncity/internal/domain/war"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// ---------------------------------------------------------------------------
// The war room.

// commander refuses a player who holds no office cleared to see the
// country's forces; the war room is theirs.
func (h *WarHandler) commander(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
) (*application.Jurisdiction, error) {
	country, err := h.country(ctx, tx, p, "")
	if err != nil {
		return nil, err
	}
	ok, err := cleared(ctx, tx, snap, country.ID, p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, refuseWar("not_cleared", country, screens.AddrWarBoard, country.Code)
	}
	return country, nil
}

// enemyCities lists the cities of every country fighting the country now or
// soon, with the war they are fought in.
func (h *WarHandler) enemyCities(ctx context.Context, tx application.Tx, countryID string, now time.Time,
) ([]application.City, map[string]application.War, error) {
	wars, err := tx.War().Wars(ctx, countryID, now)
	if err != nil {
		return nil, nil, err
	}
	var out []application.City
	of := map[string]application.War{}
	for _, w := range wars {
		r := w.Rule()
		if s := r.StatusAt(now); s != war.Active && s != war.Declared {
			continue
		}
		for _, p := range w.Parties {
			if !r.Opposed(countryID, p.CountryID) {
				continue
			}
			cities, err := tx.Diplomacy().CitiesOf(ctx, p.CountryID)
			if err != nil {
				return nil, nil, err
			}
			for _, c := range cities {
				if _, seen := of[c.ID]; !seen {
					of[c.ID] = w
					out = append(out, c)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, of, nil
}

// roomTarget reads an enemy city for the room: its country, damage, and the
// road distance from the nearest garrison of ours.
func (h *WarHandler) roomTarget(ctx context.Context, tx application.Tx, snap *content.Snapshot, city application.City,
	w application.War, garrisons []string, now time.Time,
) (screens.RoomTarget, error) {
	owner, err := tx.Diplomacy().CountryOfCity(ctx, city.ID)
	if err != nil {
		return screens.RoomTarget{}, err
	}
	place, err := placeOf(ctx, tx, owner)
	if err != nil {
		return screens.RoomTarget{}, err
	}
	t := screens.RoomTarget{CityCode: city.Code, City: city.Name, Country: place, WarNo: w.No, DistanceKM: -1}
	for _, g := range garrisons {
		if km, err := snap.Routes().DistanceBetween(g, city.Code); err == nil && (t.DistanceKM < 0 || int64(km) < t.DistanceKM) {
			t.DistanceKM = int64(km)
		}
	}
	t.DistanceKM = max(t.DistanceKM, 0)
	d, err := tx.War().CityDamage(ctx, city.ID, false)
	if err != nil {
		return t, err
	}
	if def, ok := snap.War(); ok {
		t.DamageBand = war.BandOf(def.Bands(), h.damageNow(snap, d, now))
	}
	return t, nil
}

// garrisonCodes lists the codes of the cities the country has forces in.
func (h *WarHandler) garrisonCodes(ctx context.Context, tx application.Tx, countryID string) ([]string, error) {
	assets, err := tx.Military().Assets(ctx, countryID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range assets {
		if a.GarrisonCityID == "" || seen[a.GarrisonCityID] {
			continue
		}
		seen[a.GarrisonCityID] = true
		c, err := h.cities.ByID(ctx, a.GarrisonCityID)
		if err != nil {
			return nil, err
		}
		out = append(out, c.Code)
	}
	return out, nil
}

// Room handles war.room: the enemy's cities, how far each is from our
// nearest garrison, and our operations under way.
func (h *WarHandler) Room(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
	return h.roomWith(ctx, meta, "")
}

func (h *WarHandler) roomWith(ctx context.Context, meta envelope.Metadata, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.WarRoomView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.commander(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		now := h.now()
		view = screens.WarRoomView{Country: countryPlace(*country), Notice: notice}
		if view.Readiness, err = tx.War().Readiness(ctx, country.ID); err != nil {
			return err
		}
		cities, of, err := h.enemyCities(ctx, tx, country.ID, now)
		if err != nil {
			return err
		}
		garrisons, err := h.garrisonCodes(ctx, tx, country.ID)
		if err != nil {
			return err
		}
		if len(garrisons) == 0 {
			own, err := tx.Diplomacy().CitiesOf(ctx, country.ID)
			if err != nil {
				return err
			}
			for _, c := range own {
				garrisons = append(garrisons, c.Code)
			}
		}
		for _, c := range cities {
			t, err := h.roomTarget(ctx, tx, snap, c, of[c.ID], garrisons, now)
			if err != nil {
				return err
			}
			view.Targets = append(view.Targets, t)
		}
		wars, err := tx.War().Wars(ctx, country.ID, now)
		if err != nil {
			return err
		}
		for _, w := range wars {
			running, err := tx.War().RunningOperations(ctx, w.ID)
			if err != nil {
				return err
			}
			for _, o := range running {
				if o.CountryID != country.ID {
					continue
				}
				line, err := h.operationLine(ctx, tx, snap, o, now)
				if err != nil {
					return err
				}
				view.Running = append(view.Running, line)
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.WarRoom(h.screen(meta, lang), view), nil
}

// targetFor resolves an enemy city: the city, the war it is fought in, and
// its country; a city of no enemy is refused.
func (h *WarHandler) targetFor(ctx context.Context, tx application.Tx, country *application.Jurisdiction, code string,
	now time.Time,
) (*application.City, *application.War, error) {
	city, err := h.cities.ByCode(ctx, strings.ToLower(strings.TrimSpace(code)))
	if err != nil {
		return nil, nil, err
	}
	owner, err := tx.Diplomacy().CountryOfCity(ctx, city.ID)
	if err != nil {
		return nil, nil, err
	}
	w, err := warBetween(ctx, tx, country.ID, owner, now)
	if err != nil {
		return nil, nil, err
	}
	if w == nil {
		return nil, nil, refuseWar(screens.WarRefusedNotEnemy, country, screens.AddrWarRoom)
	}
	return city, w, nil
}

// Target handles war.target: an enemy city and the operations our forces
// in reach of it could mount.
func (h *WarHandler) Target(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, ok := snap.War()
	lang := meta.Language
	var view screens.WarTargetView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.commander(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		if !ok {
			return refuseWar(screens.WarRefusedState, country)
		}
		now := h.now()
		city, w, err := h.targetFor(ctx, tx, country, req.City, now)
		if err != nil {
			return err
		}
		opts, err := h.forceOptions(ctx, tx, snap, def, country.ID, city, p)
		if err != nil {
			return err
		}
		view = screens.WarTargetView{Country: countryPlace(*country)}
		near := []string{}
		for _, o := range opts {
			near = append(near, o.from.Code)
			view.Options = append(view.Options, o.view)
		}
		if len(near) == 0 {
			if near, err = h.garrisonCodes(ctx, tx, country.ID); err != nil {
				return err
			}
		}
		if view.Target, err = h.roomTarget(ctx, tx, snap, *city, *w, near, now); err != nil {
			return err
		}
		ctrl, err := tx.War().Control(ctx, city.ID)
		if err != nil {
			return err
		}
		if ctrl != nil {
			deJure, err := placeOf(ctx, tx, ctrl.DeJureCountryID)
			if err != nil {
				return err
			}
			view.Occupied = &screens.OccupationLine{DeJure: deJure}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.WarTarget(h.screen(meta, lang), view), nil
}

// WarOperationPayload is the jsonb an operation's scheduled action carries.
type WarOperationPayload struct {
	OperationID string `json:"operation_id"`
}

// Launch handles war.launch: the objective, how many, the estimate, then
// confirm; on confirm, once, the pieces (and an air strike's munitions) are
// committed and the operation strikes after its preparation, on the game
// clock.
func (h *WarHandler) Launch(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, hasWar := snap.War()
	lang := meta.Language
	var (
		view screens.LaunchView
		done bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm, err := h.confirm(ctx, tx, p, meta, req)
		if err != nil {
			return err
		}
		country, err := h.commander(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		if !hasWar {
			return refuseWar(screens.WarRefusedState, country)
		}
		now := h.now()
		city, w, err := h.targetFor(ctx, tx, country, req.City, now)
		if err != nil {
			return err
		}
		opts, err := h.forceOptions(ctx, tx, snap, def, country.ID, city, p)
		if err != nil {
			return err
		}
		kind, class := strings.TrimSpace(req.Kind), strings.TrimSpace(req.Class)
		var opt *forceOption
		for i := range opts {
			if opts[i].view.Kind == kind && (opts[i].view.Class.Code == class || kind == content.OperationGround) {
				opt = &opts[i]
			}
		}
		back := []string{screens.AddrWarTarget, city.Code}
		if opt == nil {
			return refuseWar(screens.WarRefusedNoForces, country, back...)
		}
		if !opt.view.CanLaunch {
			r := refuseWar(screens.WarRefusedNotHolder, country, back...)
			r.view.Office = opt.view.Office
			return r
		}
		opDef, _ := def.Operation(kind)
		view = screens.LaunchView{Country: countryPlace(*country), Option: opt.view, Prepare: h.scale.RealWait(opDef.PrepareTime())}
		if view.Target, err = h.roomTarget(ctx, tx, snap, *city, *w, []string{opt.from.Code}, now); err != nil {
			return err
		}
		objective := strings.TrimSpace(req.Objective)
		if kind == content.OperationGround {
			objective = application.ObjectiveTake
		} else if objective != application.ObjectiveCity && objective != application.ObjectiveDefences {
			view.Objectives = []string{application.ObjectiveCity, application.ObjectiveDefences}
			return nil
		}
		view.Objective = objective
		ready := int64(len(opt.pieces))
		qty, ok := quantityArg(req.Qty)
		if kind == content.OperationGround {
			qty, ok = ready, true
		}
		if !ok {
			seen := map[int64]bool{}
			for _, n := range []int64{1, 2, 4, 8, ready} {
				if n >= 1 && n <= ready && !seen[n] {
					seen[n] = true
					view.Quantities = append(view.Quantities, n)
				}
			}
			return nil
		}
		if qty > ready {
			r := refuseWar(screens.WarRefusedStock, country, back...)
			r.view.Max = ready
			return r
		}
		view.Qty, view.Confirm = qty, true
		pieces := opt.pieces[:qty]
		var bombs []combatPiece
		if kind == content.OperationAir {
			load := int64(0)
			for _, pc := range pieces {
				load += pc.combat.Load
			}
			bombs = opt.bombs[:min(int64(len(opt.bombs)), load)]
			view.Munitions = int64(len(bombs))
			if len(bombs) == 0 {
				return refuseWar(screens.WarRefusedNoMunition, country, back...)
			}
		}
		// The estimate: the defence as it stands, over the content's dice.
		defenders, err := h.defendersOf(ctx, tx, snap, *w, country.ID, city.ID)
		if err != nil {
			return err
		}
		d := buildDefence(defenders, def.DoctrineRules())
		damage, err := tx.War().CityDamage(ctx, city.ID, false)
		if err != nil {
			return err
		}
		attReady, err := tx.War().Readiness(ctx, country.ID)
		if err != nil {
			return err
		}
		owner, err := tx.Diplomacy().CountryOfCity(ctx, city.ID)
		if err != nil {
			return err
		}
		defReady, err := tx.War().Readiness(ctx, owner)
		if err != nil {
			return err
		}
		view.Estimate = h.estimate(snap, def, kind, objective, pieces, bombs, d, h.damageNow(snap, damage, now), attReady, defReady)
		if !confirm {
			return nil
		}
		if s := w.Rule().StatusAt(now); s != war.Active {
			r := refuseWar(screens.WarRefusedNotYet, country, back...)
			if s == war.Declared {
				r.view.In = w.ActiveAt.Sub(now)
			} else {
				r.view.Kind = screens.WarRefusedState
			}
			return r
		}
		if err := lockCountries(ctx, tx, country.ID, owner); err != nil {
			return err
		}
		seat, _, err := mayAct(ctx, tx, snap, country.ID, opt.branch.Command, p)
		if err != nil {
			return err
		}
		op := application.WarOperation{ID: h.ids.NewID(), WarID: w.ID, Kind: kind, Objective: objective, CountryID: country.ID,
			TargetCountryID: owner, FromCityID: opt.from.ID, TargetCityID: city.ID, ClassCode: opt.view.Class.Code,
			Committed: int(qty), Munitions: len(bombs), GameActionID: h.ids.NewID(), OrderedBy: p.ID,
			OfficeCode: seat.OfficeCode, LaunchedAt: now, StrikesAt: now.Add(view.Prepare)}
		op.Seed = seedOf(op.ID)
		payload, err := json.Marshal(WarOperationPayload{OperationID: op.ID})
		if err != nil {
			return err
		}
		if err := tx.GameActions().Schedule(ctx, application.GameAction{ID: op.GameActionID,
			ActionType: application.WarOperationActionType, ActorType: "player", ActorID: p.ID,
			ReferenceType: application.WarOperationReference, ReferenceID: op.ID, Payload: payload,
			StartedAt: now, FinishAt: op.StrikesAt}); err != nil {
			return err
		}
		if _, err := tx.War().Launch(ctx, op); err != nil {
			return err
		}
		var ids []string
		for _, pc := range append(append([]combatPiece(nil), pieces...), bombs...) {
			ids = append(ids, pc.asset.PieceID)
		}
		n, err := tx.War().Commit(ctx, ids, op.ID, now)
		if err != nil {
			return err
		}
		if n != int64(len(ids)) {
			// Some went elsewhere meanwhile: nothing is launched.
			return refuseWar(screens.WarRefusedStock, country, back...)
		}
		done = true
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done {
		c := h.screen(meta, lang)
		notice := c.T("war.launch.done", map[string]any{"op": c.OperationName(view.Option.Kind),
			"city": c.CityName(view.Target.CityCode, view.Target.City), "time": screens.FormatDuration(c, view.Prepare)})
		return h.roomWith(ctx, meta, notice)
	}
	return screens.WarLaunch(h.screen(meta, lang), view), nil
}

// defendersOf reads the pieces of the side hostile to attacker stationed in
// a city, locked.
func (h *WarHandler) defendersOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, w application.War,
	attacker, cityID string,
) ([]combatPiece, error) {
	garrison, err := tx.War().GarrisonAssets(ctx, cityID)
	if err != nil {
		return nil, err
	}
	pieces, err := combatPieces(ctx, tx, snap, garrison, designCache{})
	if err != nil {
		return nil, err
	}
	return hostilePieces(pieces, w.Rule(), attacker), nil
}

// ---------------------------------------------------------------------------
// Resolution.

// operationReport is what an operation's report keeps beyond its columns.
type operationReport struct {
	SeenAtKM  int64 `json:"seen_at_km"`
	Fired     int64 `json:"fired"`
	Liberated bool  `json:"liberated,omitempty"`
}

// Resolve handles war.resolve from the SCHEDULER: an operation reaching its
// target, once — the war and the operation are locked and the operation
// must still be launched under this action. An operation whose war is no
// longer fought (a ceasefire, a peace) or whose target changed hands is
// called off: its forces stand down, nothing is spent.
func (h *WarHandler) Resolve(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in WarOperationPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("operation payload is unreadable").WithCause(err)
		}
	}
	if in.OperationID == "" {
		in.OperationID = req.ReferenceID
	}
	if in.OperationID == "" {
		return nil, errors.InvalidInput("operation names no operation")
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		peek, err := tx.War().Operation(ctx, in.OperationID)
		if isSentinel(err, application.ErrOperationNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if peek.Status != application.OperationLaunched {
			return nil
		}
		// Lock order: the countries, the war, then the operation again.
		if err := lockCountries(ctx, tx, peek.CountryID, peek.TargetCountryID); err != nil {
			return err
		}
		w, err := tx.War().WarByID(ctx, peek.WarID, true)
		if err != nil {
			return err
		}
		op, err := tx.War().Operation(ctx, in.OperationID)
		if err != nil {
			return err
		}
		if op.Status != application.OperationLaunched || (req.ActionID != "" && op.GameActionID != req.ActionID) {
			return nil
		}
		if now.Before(op.StrikesAt) {
			return errors.Internal(stderrors.New("handlers: an operation struck before its time"))
		}
		owner, err := tx.Diplomacy().CountryOfCity(ctx, op.TargetCityID)
		if err != nil {
			return err
		}
		def, ok := snap.War()
		if !ok || !w.Rule().Hostile(op.CountryID, owner, now) || owner != op.TargetCountryID {
			return h.callOff(ctx, tx, meta, op, now)
		}
		return h.resolve(ctx, tx, snap, def, meta, w, op, now)
	})
}

// callOff stands an operation's forces down with nothing spent.
func (h *WarHandler) callOff(ctx context.Context, tx application.Tx, meta envelope.Metadata, op *application.WarOperation,
	now time.Time,
) error {
	if err := tx.War().StandDown(ctx, op.ID, "", now); err != nil {
		return err
	}
	op.Status, op.ResolvedAt = application.OperationCalledOff, &now
	if err := tx.War().SaveOperation(ctx, *op); err != nil {
		return err
	}
	return h.reportTo(ctx, tx, h.content.Current(), meta, op, op.OrderedBy, true)
}

// lose ends pieces: destroyed in battle or spent, out of the item journal
// and out of service.
func (h *WarHandler) lose(ctx context.Context, tx application.Tx, pieces []combatPiece, status string, op *application.WarOperation,
	now time.Time,
) error {
	if len(pieces) == 0 {
		return nil
	}
	reason := application.ItemDestroyed
	if status == application.AssetExpended {
		reason = application.ItemExpended
	}
	var ids []string
	for _, pc := range pieces {
		ids = append(ids, pc.asset.PieceID)
		if err := tx.Items().Move(ctx, application.ItemMove{Item: pc.asset.Item, PieceID: pc.asset.PieceID, Qty: 1,
			FromOrg: application.StateOrg(pc.asset.CountryID), FromHolding: application.HoldWarehouse, Reason: reason,
			ReferenceType: application.WarOperationReference, ReferenceID: op.ID, At: now}); err != nil {
			return err
		}
	}
	return tx.War().Lose(ctx, ids, status, op.ID, now)
}

func pieceIDs(pieces []combatPiece) []string {
	out := make([]string, len(pieces))
	for i, pc := range pieces {
		out[i] = pc.asset.PieceID
	}
	return out
}

// resolve fights an operation and applies what it did: equipment lost and
// damaged on both sides, munitions spent, a city damaged, a city taken.
func (h *WarHandler) resolve(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.WarDef,
	meta envelope.Metadata, w *application.War, op *application.WarOperation, now time.Time,
) error {
	committed, err := tx.War().OperationAssets(ctx, op.ID)
	if err != nil {
		return err
	}
	all, err := combatPieces(ctx, tx, snap, committed, designCache{})
	if err != nil {
		return err
	}
	var attackers, bombs []combatPiece
	for _, pc := range all {
		if pc.combat.Role == content.RoleMunition {
			bombs = append(bombs, pc)
		} else {
			attackers = append(attackers, pc)
		}
	}
	defenders, err := h.defendersOf(ctx, tx, snap, *w, op.CountryID, op.TargetCityID)
	if err != nil {
		return err
	}
	d := buildDefence(defenders, def.DoctrineRules())
	attReady, err := tx.War().Readiness(ctx, op.CountryID)
	if err != nil {
		return err
	}
	defReady, err := tx.War().Readiness(ctx, op.TargetCountryID)
	if err != nil {
		return err
	}
	stored, err := tx.War().CityDamage(ctx, op.TargetCityID, true)
	if err != nil {
		return err
	}
	damageBefore := h.damageNow(snap, stored, now)
	report := operationReport{}
	var (
		standTo    string
		damageBPS  int64
		taken      bool
		liberated  bool
		lostOurs   []combatPiece
		damagedOur []combatPiece
		lostTheirs []combatPiece
		dmgTheirs  []combatPiece
	)
	if op.Kind == content.OperationGround {
		out, err := war.Fight(assaultOf(def, op.Seed, attackers, d, damageBefore, attReady, defReady))
		if err != nil {
			return errors.Internal(err)
		}
		for i, f := range out.Attackers {
			switch f {
			case war.UnitDestroyed:
				lostOurs = append(lostOurs, attackers[i])
			case war.UnitDamaged:
				damagedOur = append(damagedOur, attackers[i])
			}
		}
		var ready []combatPiece
		for _, pc := range d.ground {
			if pc.asset.Condition == application.AssetReady {
				ready = append(ready, pc)
			}
		}
		for i, f := range out.Defenders {
			if i >= len(ready) {
				break // the militia
			}
			switch f {
			case war.UnitDestroyed:
				lostTheirs = append(lostTheirs, ready[i])
			case war.UnitDamaged:
				dmgTheirs = append(dmgTheirs, ready[i])
			}
		}
		taken = out.Taken && d.airDefence == 0
	} else {
		out, err := war.Resolve(strikeOf(def, op.Seed, attackers, bombs, d, attReady, defReady))
		if err != nil {
			return errors.Internal(err)
		}
		var spent []combatPiece
		for i, f := range out.Threats {
			report.SeenAtKM = max(report.SeenAtKM, f.SeenAtKM)
			switch {
			case attackers[i].combat.Role == content.RoleMissile:
				spent = append(spent, attackers[i])
				if f.Lost {
					op.AttackerLost++
				}
			case f.Lost:
				lostOurs = append(lostOurs, attackers[i])
			}
		}
		for li, lf := range out.Layers {
			report.Fired += lf.Fired
			if lf.Swept > 0 {
				lostTheirs = append(lostTheirs, d.layerPieces[li][:min(int(lf.Swept), len(d.layerPieces[li]))]...)
			}
		}
		// Every bomb loaded is spent: dropped, or lost with its aircraft.
		spent = append(spent, bombs...)
		op.MunitionsUsed = min(int(out.MunitionsUsed), len(bombs))
		op.Hits = int(out.Hits)
		if err := h.lose(ctx, tx, spent, application.AssetExpended, op, now); err != nil {
			return err
		}
		switch op.Objective {
		case application.ObjectiveDefences:
			swept := map[string]bool{}
			for _, pc := range lostTheirs {
				swept[pc.asset.PieceID] = true
			}
			for _, hit := range war.Suppress(op.Seed, out.Hits, len(d.sead), def.DefenceHitDestroysBPS) {
				pc := d.sead[hit.Asset]
				if swept[pc.asset.PieceID] {
					continue
				}
				if hit.Destroyed {
					lostTheirs = append(lostTheirs, pc)
				} else {
					dmgTheirs = append(dmgTheirs, pc)
				}
			}
		default:
			damageBPS = def.CityRules().Strike(damageBefore, out.Points) - damageBefore
		}
	}
	if err := h.lose(ctx, tx, lostOurs, application.AssetDestroyed, op, now); err != nil {
		return err
	}
	if err := h.lose(ctx, tx, lostTheirs, application.AssetDestroyed, op, now); err != nil {
		return err
	}
	if err := tx.War().Damage(ctx, pieceIDs(damagedOur), now); err != nil {
		return err
	}
	if err := tx.War().Damage(ctx, pieceIDs(dmgTheirs), now); err != nil {
		return err
	}
	op.AttackerLost += len(lostOurs)
	op.AttackerDamaged, op.DefenderLost, op.DefenderDamaged = len(damagedOur), len(lostTheirs), len(dmgTheirs)
	city, err := h.cities.ByID(ctx, op.TargetCityID)
	if err != nil {
		return err
	}
	if damageBPS > 0 {
		dmg := application.CityDamage{CityID: city.ID, DamageBPS: damageBefore + damageBPS, AsOf: now, LastStruckAt: now,
			ClosedUntil: now}
		if closed := def.ClosedForTime(); closed > 0 {
			dmg.ClosedUntil = now.Add(h.scale.RealWait(closed))
		}
		if stored != nil && stored.ClosedUntil.After(dmg.ClosedUntil) {
			dmg.ClosedUntil = stored.ClosedUntil
		}
		if err := tx.War().SaveCityDamage(ctx, dmg); err != nil {
			return err
		}
		op.DamageBPS = int(damageBPS)
	}
	if taken {
		if liberated, err = h.conquer(ctx, tx, snap, def, meta, w, op, city, now); err != nil {
			return err
		}
		standTo = city.ID
		op.Captured = true
		report.Liberated = liberated
	}
	if err := tx.War().StandDown(ctx, op.ID, standTo, now); err != nil {
		return err
	}
	if op.Report, err = json.Marshal(report); err != nil {
		return err
	}
	op.Status, op.ResolvedAt = application.OperationResolved, &now
	if err := tx.War().SaveOperation(ctx, *op); err != nil {
		return err
	}
	if err := tx.War().RecordEvent(ctx, application.WarEvent{ID: h.ids.NewID(), Kind: application.WarEventOperation,
		WarID: w.ID, CountryID: op.CountryID, OtherCountryID: op.TargetCountryID, CityID: city.ID, OperationID: op.ID,
		PlayerID: op.OrderedBy, OfficeCode: op.OfficeCode, At: now}); err != nil {
		return err
	}
	return h.announceOperation(ctx, tx, snap, def, meta, w, op, city, taken, now)
}

// announceOperation tells the groups of every party's cities what happened,
// in bands; the commander and the defender's head of state the exact
// report; and the players in a city that was hit that it was.
func (h *WarHandler) announceOperation(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.WarDef,
	meta envelope.Metadata, w *application.War, op *application.WarOperation, city *application.City, taken bool,
	now time.Time,
) error {
	if err := h.reportTo(ctx, tx, snap, meta, op, op.OrderedBy, true); err != nil {
		return err
	}
	target, err := tx.Governance().Jurisdiction(ctx, op.TargetCountryID)
	if err != nil {
		return err
	}
	chain, err := tx.Governance().ActingChain(ctx, actionOffice(snap, content.ActionWar), target.ID)
	if err != nil {
		return err
	}
	if acting := application.ActingForChain(chain); acting != nil {
		for _, s := range acting.Holders {
			if err := h.reportTo(ctx, tx, snap, meta, op, s.HolderPlayerID, false); err != nil {
				return err
			}
		}
	}
	attacker, err := tx.Governance().Jurisdiction(ctx, op.CountryID)
	if err != nil {
		return err
	}
	cities, err := allCityIDs(ctx, tx, *w)
	if err != nil {
		return err
	}
	band := war.BandOf(def.Bands(), int64(op.DamageBPS))
	if taken {
		var rep operationReport
		_ = json.Unmarshal(op.Report, &rep)
		return appendDomainEvent(ctx, tx, meta, "war", "taken", op.ID, map[string]any{"liberated": rep.Liberated,
			"country_code": attacker.Code, "country_name": attacker.Name, "other_code": target.Code,
			"other_name": target.Name, "city_code": city.Code, "city_name": city.Name, "city_ids": cities})
	}
	result := "repelled"
	switch {
	case op.Kind == content.OperationGround:
		result = "held"
	case band != "":
		result = application.ObjectiveCity
	case op.Objective == application.ObjectiveDefences && op.DefenderLost+op.DefenderDamaged > 0:
		result = application.ObjectiveDefences
	}
	if err := appendDomainEvent(ctx, tx, meta, "war", "struck", op.ID, map[string]any{"kind": op.Kind, "result": result,
		"country_code": attacker.Code, "country_name": attacker.Name, "other_code": target.Code, "other_name": target.Name,
		"city_code": city.Code, "city_name": city.Name, "band": band, "city_ids": cities}); err != nil {
		return err
	}
	if band == "" || h.rules.NoticeCap == 0 {
		return nil
	}
	players, err := tx.War().PlayersIn(ctx, city.ID, h.rules.NoticeCap)
	if err != nil {
		return err
	}
	var strike health.Injury
	if hd, ok := snap.Health(); ok {
		strike = hd.Injuries.WarStrike.Injury()
	}
	for _, id := range players {
		// A strike may hurt who stands in the city (health.yml
		// injuries.war_strike): each on their own die, of the operation and
		// the player, so a replay hurts the same people the same
		// (docs/adr/0023). Nobody dies.
		inj, err := rollInjury(ctx, tx, snap, h.ids, h.scale, meta, strike, op.ID, health.Seed(id),
			hurt{playerID: id, cityID: city.ID, cause: application.CauseWar, causeRef: op.ID}, now)
		if err != nil {
			return err
		}
		if err := appendDomainEvent(ctx, tx, meta, "war", "city_struck", op.ID, map[string]any{"player_id": id,
			"kind": "struck", "country_code": target.Code, "country_name": target.Name, "other_code": attacker.Code,
			"other_name": attacker.Name, "city_code": city.Code, "city_name": city.Name, "band": band,
			"injury": inj.payload()}); err != nil {
			return err
		}
	}
	return nil
}

// reportTo sends an operation's exact report to one player, privately:
// the numbers travel in the event, so the notice needs no read.
func (h *WarHandler) reportTo(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	op *application.WarOperation, playerID string, ours bool,
) error {
	v, err := h.reportView(ctx, tx, snap, op, ours)
	if err != nil {
		return err
	}
	return appendDomainEvent(ctx, tx, meta, "war", "report", op.ID, map[string]any{"player_id": playerID, "report": v})
}

// conquer passes a city to the country whose forces took it, once, in the
// operation's transaction: its jurisdiction moves under the new country
// (every country-level rule follows it: levies, sanctions, tariffs), its
// offices are emptied and the commander who took it governs it, the other
// side's forces left in it fall back to their depots, and its residents
// stay. A country retaking its own city liberates it: the occupation ends.
func (h *WarHandler) conquer(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.WarDef,
	meta envelope.Metadata, w *application.War, op *application.WarOperation, city *application.City, now time.Time,
) (bool, error) {
	ctrl, err := tx.War().Control(ctx, city.ID)
	if err != nil {
		return false, err
	}
	deJure := op.TargetCountryID
	if ctrl != nil {
		deJure = ctrl.DeJureCountryID
	}
	liberated := op.CountryID == deJure
	if liberated {
		err = tx.War().ClearControl(ctx, city.ID)
	} else {
		err = tx.War().SetControl(ctx, application.CityControl{CityID: city.ID, DeJureCountryID: deJure,
			ControllerCountryID: op.CountryID, WarID: w.ID, OperationID: op.ID, Since: now})
	}
	if err != nil {
		return false, err
	}
	if err := tx.War().MoveCity(ctx, city.ID, op.CountryID); err != nil {
		return false, err
	}
	if err := tx.War().Withdraw(ctx, city.ID, op.CountryID, now); err != nil {
		return false, err
	}
	defs, err := tx.Governance().OfficeDefinitions(ctx)
	if err != nil {
		return false, err
	}
	seats := map[string]int{}
	for _, d := range defs {
		seats[d.Code] = d.Seats
	}
	vacate := append([]string(nil), def.Occupation.Vacate...)
	if def.Occupation.Governor != "" {
		vacate = append(vacate, def.Occupation.Governor)
	}
	for _, code := range vacate {
		for seat := 1; seat <= seats[code]; seat++ {
			if _, _, err := application.VacateOffice(ctx, tx, code, city.JurisdictionID, seat, now); err != nil &&
				!isSentinel(err, application.ErrOfficeVacant) && !isSentinel(err, application.ErrOfficeNotFound) {
				return false, err
			}
		}
	}
	if !liberated && def.Occupation.Governor != "" {
		// The commander who took the city governs it. One who may not hold
		// the seat (a mayor elsewhere) leaves it vacant: the defaults rule.
		if _, _, err := application.ConquerToOffice(ctx, tx, def.Occupation.Governor, city.JurisdictionID, 1, op.OrderedBy,
			now); err != nil && !isSentinel(err, application.ErrIncompatibleOffices) &&
			!isSentinel(err, application.ErrAlreadyHoldsSeat) && !isSentinel(err, application.ErrOfficeOccupied) &&
			!isSentinel(err, application.ErrOfficeNotFound) {
			return false, err
		}
	}
	kind := application.WarEventCaptured
	if liberated {
		kind = application.WarEventLiberated
	}
	return liberated, tx.War().RecordEvent(ctx, application.WarEvent{ID: h.ids.NewID(), Kind: kind, WarID: w.ID,
		CountryID: op.CountryID, OtherCountryID: op.TargetCountryID, CityID: city.ID, OperationID: op.ID,
		PlayerID: op.OrderedBy, OfficeCode: op.OfficeCode, At: now})
}

// reportView reads an operation's exact report.
func (h *WarHandler) reportView(ctx context.Context, tx application.Tx, snap *content.Snapshot, op *application.WarOperation,
	ours bool,
) (screens.StrikeReportView, error) {
	country, err := placeOf(ctx, tx, op.CountryID)
	if err != nil {
		return screens.StrikeReportView{}, err
	}
	target, err := placeOf(ctx, tx, op.TargetCountryID)
	if err != nil {
		return screens.StrikeReportView{}, err
	}
	city, err := h.cities.ByID(ctx, op.TargetCityID)
	if err != nil {
		return screens.StrikeReportView{}, err
	}
	var rep operationReport
	_ = json.Unmarshal(op.Report, &rep)
	v := screens.StrikeReportView{No: op.No, Kind: op.Kind, Objective: op.Objective, Country: country, Target: target,
		CityCode: city.Code, City: city.Name, Class: named(op.ClassCode, op.ClassCode), Ours: ours,
		CalledOff: op.Status == application.OperationCalledOff, Committed: int64(op.Committed),
		Lost: int64(op.AttackerLost), Damaged: int64(op.AttackerDamaged), EnemyLost: int64(op.DefenderLost),
		EnemyDmg: int64(op.DefenderDamaged), SeenAtKM: rep.SeenAtKM, Fired: rep.Fired, Munitions: int64(op.MunitionsUsed),
		Hits: int64(op.Hits), DamageBPS: int64(op.DamageBPS), Captured: op.Captured && !rep.Liberated,
		Liberated: rep.Liberated}
	if cl, ok := snap.ForceClass(op.ClassCode); ok {
		v.Class.Name = cl.Name
	}
	if def, ok := snap.War(); ok {
		v.DamageBand = war.BandOf(def.Bands(), int64(op.DamageBPS))
	}
	return v, nil
}

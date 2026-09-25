package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/mission"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// MissionRules is the tuning of missions (config missions.*).
type MissionRules struct {
	MaxActive       int
	PlayerDailyCap  int64
	EconomyDailyCap int64
}

// MissionsHandler serves missions (docs/adr/0023-health-missions-factions.md):
// a city's boards and their missions, taking one at its board, the player's
// missions and their progress, handing goods in, and giving one up; and, as
// a consumer of the game's own events, moving missions on — each event once
// (the missions' inbox) — and paying a mission the moment its last
// objective is met.
//
// # Money
//
// A mission's cash reward enters the economy (mission_reward, a faucet),
// recorded as a reward grant (source mission). Every payout is held inside
// two caps a UTC day — the player's and the whole economy's — read and spent
// under one lock, so no two completions can both spend the last of a cap.
// What a cap withholds is shown and never paid later. Goods a mission gives
// come into the world as a grant, the mission's row their provenance.
type MissionsHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	scale   gametime.Scale
	rules   MissionRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewMissionsHandler wires the handler.
func NewMissionsHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, scale gametime.Scale, rules MissionRules, idempotencyTTL time.Duration,
	now func() time.Time,
) *MissionsHandler {
	if uow == nil || ids == nil || source == nil || cities == nil {
		panic("handlers: NewMissionsHandler requires a unit of work, ids, content and cities")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || rules.MaxActive < 1 || rules.PlayerDailyCap < 0 ||
		rules.EconomyDailyCap < 0 {
		panic("handlers: NewMissionsHandler requires a game clock, an idempotency ttl and valid mission rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &MissionsHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, scale: scale, rules: rules,
		idempotencyTTL: idempotencyTTL, now: now}
}

// MissionRequest is the payload of the mission commands.
type MissionRequest struct {
	Board   string `json:"board,omitempty"`
	Mission string `json:"mission,omitempty"`
	No      string `json:"no,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

func (h *MissionsHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// missionRefusal carries a refused mission command out of a unit of work.
type missionRefusal struct{ view screens.MissionRefusalView }

func (r *missionRefusal) Error() string { return "handlers: mission refused: " + r.view.Kind }

func refuseMission(kind string) *missionRefusal {
	return &missionRefusal{view: screens.MissionRefusalView{Kind: kind}}
}

func (h *MissionsHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *missionRefusal
	if stderrors.As(err, &r) {
		return screens.MissionRefusal(c, r.view), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(c, v), nil
	}
	return nil, err
}

// wait is a GAME duration on the wall clock.
func (h *MissionsHandler) wait(d time.Duration) time.Duration { return h.scale.RealWait(d) }

// target names what an objective's target is, for a screen.
func missionTarget(snap *content.Snapshot, o content.ObjectiveDef) screens.MissionTarget {
	t := screens.MissionTarget{Code: o.Target}
	if o.Target == "" {
		return t
	}
	switch mission.Kind(o.Kind) {
	case mission.Travel:
		t.Kind = screens.TargetCity
		if c, ok := snap.City(o.Target); ok {
			t.Name = c.Name
		}
	case mission.Work:
		if def, ok := snap.CareerDef(o.Target); ok {
			t.Kind, t.Name = screens.TargetCareer, def.Name
		} else {
			t.Kind = screens.TargetCareerCategory
		}
	case mission.Course:
		t.Kind = screens.TargetCourse
		if c, ok := snap.CourseDef(o.Target); ok {
			t.Name = c.Name
		}
	case mission.Buy, mission.Sell, mission.Deliver:
		t.Kind = screens.TargetItem
		if d, ok := snap.ItemDef(o.Target); ok {
			t.Name = d.Name
		}
	case mission.Use:
		if d, ok := snap.ItemDef(o.Target); ok {
			t.Kind, t.Name = screens.TargetItem, d.Name
		} else {
			t.Kind = screens.TargetItemCategory
		}
	case mission.Crime:
		if d, ok := snap.CrimeDef(o.Target); ok {
			t.Kind, t.Name = screens.TargetCrime, d.Name
		} else {
			t.Kind = screens.TargetCrimeCategory
			for _, c := range snap.CrimeCategories() {
				if c.Code == o.Target {
					t.Name = c.Name
				}
			}
		}
	}
	return t
}

// objectives lays a mission's objectives out with their progress.
func missionObjectives(snap *content.Snapshot, def content.MissionDef, progress []int64) []screens.MissionObjective {
	out := make([]screens.MissionObjective, 0, len(def.Objectives))
	for i, o := range def.Objectives {
		var done int64
		if i < len(progress) {
			done = progress[i]
		}
		out = append(out, screens.MissionObjective{Kind: o.Kind, Target: missionTarget(snap, o), Count: o.Count, Done: done})
	}
	return out
}

// reward lays a mission's reward out.
func missionReward(snap *content.Snapshot, def content.MissionDef) screens.MissionReward {
	r := screens.MissionReward{Cash: def.Reward.Cash, XP: def.Reward.XP}
	for _, it := range def.Reward.Items {
		r.Items = append(r.Items, screens.LootLine{Item: itemNamed(snap, it.Item), Qty: it.Qty})
	}
	return r
}

// history reads what decides whether a player may take missions now.
func (h *MissionsHandler) history(ctx context.Context, tx application.Tx, p *application.Player, now time.Time) (mission.History, []application.MissionAssignment, error) {
	var hs mission.History
	row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
	if err != nil {
		return hs, nil, err
	}
	hs.Level = row.Level
	if hs.Completed, err = tx.Missions().Completions(ctx, p.ID); err != nil {
		return hs, nil, err
	}
	active, err := tx.Missions().Active(ctx, p.ID)
	return hs, active, err
}

// availability says whether a mission may be taken, and if not why.
func (h *MissionsHandler) availability(def content.MissionDef, hs mission.History, active []application.MissionAssignment,
	now time.Time,
) (string, time.Duration) {
	hs.Active = false
	for _, a := range active {
		if a.Mission == def.Code {
			hs.Active = true
		}
	}
	why, left := def.Mission().Available(hs, now, h.wait)
	if why == mission.BlockedNone && len(active) >= h.rules.MaxActive {
		why = mission.BlockedTooMany
	}
	return why, left
}

// Board handles mission.board: a board of the player's city and the
// missions on it — or, with no board named, the city's boards.
func (h *MissionsHandler) Board(ctx context.Context, meta envelope.Metadata, req MissionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.MissionBoardView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if w.city == nil {
			return application.ErrCityNotFound
		}
		view.CityCode, view.City = w.city.Code, w.city.Name
		for _, b := range snap.MissionBoards() {
			if len(snap.BoardMissions(b.Code, w.city.Code)) == 0 {
				continue
			}
			if _, ok := w.cmap.Find(b.Place); !ok && w.placed() {
				continue
			}
			view.Boards = append(view.Boards, screens.MissionBoardRef{Code: b.Code, Name: b.Name, Place: placeNamed(snap, b.Place)})
		}
		board, ok := snap.MissionBoard(strings.TrimSpace(req.Board))
		if !ok {
			return nil
		}
		view.Board = &screens.MissionBoardRef{Code: board.Code, Name: board.Name, Place: placeNamed(snap, board.Place)}
		view.Here = !w.placed() || w.here.Code == board.Place
		now := h.now()
		hs, active, err := h.history(ctx, tx, p, now)
		if err != nil {
			return err
		}
		for _, def := range snap.BoardMissions(board.Code, w.city.Code) {
			why, left := h.availability(def, hs, active, now)
			view.Missions = append(view.Missions, screens.MissionLine{Mission: named(def.Code, def.Name),
				Reward: missionReward(snap, def), Blocked: why, Wait: left, Repeatable: def.Repeat == content.RepeatAgain})
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.MissionBoard(h.screen(meta, lang), view), nil
}

// View handles mission.view: one mission — what it asks, gives and needs.
func (h *MissionsHandler) View(ctx context.Context, meta envelope.Metadata, req MissionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.MissionView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, ok := snap.MissionDef(strings.TrimSpace(req.Mission))
		if !ok {
			return refuseMission(screens.MissionRefusedNotFound)
		}
		now := h.now()
		hs, active, err := h.history(ctx, tx, p, now)
		if err != nil {
			return err
		}
		why, left := h.availability(def, hs, active, now)
		board, _ := snap.MissionBoard(def.Board)
		view = screens.MissionView{Mission: named(def.Code, def.Name), Board: screens.MissionBoardRef{Code: board.Code,
			Name: board.Name, Place: placeNamed(snap, board.Place)}, Objectives: missionObjectives(snap, def, nil),
			Reward: missionReward(snap, def), MinLevel: def.MinLevel, Blocked: why, Wait: left,
			Repeatable: def.Repeat == content.RepeatAgain, Max: h.rules.MaxActive}
		m := def.Mission()
		if m.Cooldown > 0 {
			view.Cooldown = h.wait(m.Cooldown)
		}
		if m.TimeLimit > 0 {
			view.TimeLimit = h.wait(m.TimeLimit)
		}
		for _, r := range def.Requires {
			if rd, ok := snap.MissionDef(r); ok {
				view.Requires = append(view.Requires, named(rd.Code, rd.Name))
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Mission(h.screen(meta, lang), view), nil
}

// Accept handles mission.accept: taking a mission, at its board. Only what
// happens from now on counts toward it.
func (h *MissionsHandler) Accept(ctx context.Context, meta envelope.Metadata, req MissionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		taken    *application.MissionAssignment
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, ok := snap.MissionDef(strings.TrimSpace(req.Mission))
		if !ok {
			return refuseMission(screens.MissionRefusedNotFound)
		}
		fresh, err := tx.Idempotency().Reserve(ctx, string(idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)),
			p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		if err := tx.Missions().Lock(ctx, p.ID); err != nil {
			return err
		}
		now := h.now()
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if w.city == nil {
			return application.ErrCityNotFound
		}
		if len(def.Cities) > 0 && !containsString(def.Cities, w.city.Code) {
			return refuseMission(screens.MissionRefusedNotHere)
		}
		board, _ := snap.MissionBoard(def.Board)
		if w.placed() {
			target, ok := w.cmap.Find(board.Place)
			if !ok {
				return refuseMission(screens.MissionRefusedNotHere)
			}
			if err := needAt(w, snap, target, "place.need.board", map[string]any{"board": board.Name}, h.scale, now); err != nil {
				return thenFor(err, "mission.board", board.Code)
			}
		}
		hs, active, err := h.history(ctx, tx, p, now)
		if err != nil {
			return err
		}
		if why, left := h.availability(def, hs, active, now); why != mission.BlockedNone {
			r := refuseMission(screens.MissionRefusedBlocked)
			r.view.Blocked, r.view.Wait, r.view.Level, r.view.Max = why, left, def.MinLevel, h.rules.MaxActive
			return r
		}
		a := application.MissionAssignment{ID: h.ids.NewID(), PlayerID: p.ID, Mission: def.Code, CityID: w.city.ID,
			Board: board.Code, Progress: make([]int64, len(def.Objectives)), AcceptedAt: now, ContentVersion: snap.Version()}
		if m := def.Mission(); m.TimeLimit > 0 {
			a.ExpiresAt = now.Add(h.wait(m.TimeLimit))
		}
		a, err = tx.Missions().Accept(ctx, a)
		if isSentinel(err, application.ErrMissionActive) {
			r := refuseMission(screens.MissionRefusedBlocked)
			r.view.Blocked = mission.BlockedActive
			return r
		}
		if err != nil {
			return err
		}
		taken = &a
		return appendDomainEvent(ctx, tx, meta, "mission", "accepted", a.ID, map[string]any{
			"player_id": p.ID, "mission": def.Code, "no": a.No})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed || taken == nil {
		return h.Mine(ctx, meta)
	}
	return h.mineWith(ctx, meta, &screens.MissionNotice{Kind: screens.MissionNoticeAccepted,
		Mission: named(taken.Mission, missionName(snap, taken.Mission))})
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func missionName(snap *content.Snapshot, code string) string {
	if d, ok := snap.MissionDef(code); ok {
		return d.Name
	}
	return code
}

// Mine handles mission.mine: the player's missions and their progress.
func (h *MissionsHandler) Mine(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.mineWith(ctx, meta, nil)
}

func (h *MissionsHandler) mineWith(ctx context.Context, meta envelope.Metadata, notice *screens.MissionNotice) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	view := screens.MissionsMineView{Notice: notice, Max: h.rules.MaxActive}
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		now := h.now()
		recent, err := tx.Missions().Recent(ctx, p.ID, 12)
		if err != nil {
			return err
		}
		for _, a := range recent {
			def, ok := snap.MissionDef(a.Mission)
			if !ok {
				continue
			}
			line := screens.MissionProgressLine{No: a.No, Mission: named(def.Code, def.Name), Status: a.Status,
				Objectives: missionObjectives(snap, def, a.Progress), Cash: a.RewardCash, Withheld: a.RewardWithheld}
			if a.Status == application.MissionActive {
				if mission.Expired(a.ExpiresAt, now) {
					line.Status = application.MissionExpired
				} else if !a.ExpiresAt.IsZero() {
					line.Left = a.ExpiresAt.Sub(now)
				}
				for _, o := range def.Objectives {
					if mission.Kind(o.Kind) == mission.Deliver {
						line.Deliver = true
					}
				}
				view.Active = append(view.Active, line)
			} else if len(view.Recent) < 5 {
				view.Recent = append(view.Recent, line)
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.MissionsMine(h.screen(meta, lang), view), nil
}

// assignment reads one of the player's missions by the number a press
// carries, under the lock of their missions.
func (h *MissionsHandler) assignment(ctx context.Context, tx application.Tx, playerID, raw string) (*application.MissionAssignment, error) {
	no, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || no <= 0 {
		return nil, refuseMission(screens.MissionRefusedNotFound)
	}
	if err := tx.Missions().Lock(ctx, playerID); err != nil {
		return nil, err
	}
	a, err := tx.Missions().ByNo(ctx, playerID, no)
	if isSentinel(err, application.ErrMissionNotFound) {
		return nil, refuseMission(screens.MissionRefusedNotFound)
	}
	return a, err
}

// Deliver handles mission.deliver: handing a mission's goods in at its
// board. What the objectives still need of what the player carries is taken
// — and leaves the world — and the mission completes when that was the last.
func (h *MissionsHandler) Deliver(ctx context.Context, meta envelope.Metadata, req MissionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var notice *screens.MissionNotice
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := tx.Idempotency().Reserve(ctx, string(idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)),
			p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		a, err := h.assignment(ctx, tx, p.ID, req.No)
		if err != nil {
			return err
		}
		now := h.now()
		def, ok := snap.MissionDef(a.Mission)
		if !ok || a.Status != application.MissionActive {
			return refuseMission(screens.MissionRefusedNotActive)
		}
		if mission.Expired(a.ExpiresAt, now) {
			return h.expire(ctx, tx, a, now)
		}
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		board, _ := snap.MissionBoard(a.Board)
		if w.city == nil || w.city.ID != a.CityID {
			return refuseMission(screens.MissionRefusedNotHere)
		}
		if w.placed() {
			if target, ok := w.cmap.Find(board.Place); ok {
				if err := needAt(w, snap, target, "place.need.board", map[string]any{"board": board.Name}, h.scale, now); err != nil {
					return thenFor(err, "mission.mine")
				}
			}
		}
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		stacks, _, err := tx.Items().Holdings(ctx, p.ID, application.HoldCarried)
		if err != nil {
			return err
		}
		m := def.Mission()
		progress := append([]int64(nil), a.Progress...)
		delivered := int64(0)
		for _, o := range m.Objectives {
			if o.Kind != mission.Deliver {
				continue
			}
			var held int64
			for _, s := range stacks {
				if s.Item == o.Target {
					held += s.Qty
				}
			}
			take := mission.Deliverable(m.Objectives, progress, o.Target, held)
			var units int64
			for i, n := range take {
				if n > 0 {
					progress = padProgress(progress, len(m.Objectives))
					progress[i] += n
					units += n
				}
			}
			if units == 0 {
				continue
			}
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: o.Target, Qty: units,
				From: p.ID, FromHolding: application.HoldCarried, Reason: application.ItemMissionDelivery,
				ReferenceType: application.MissionReference, ReferenceID: a.ID, At: now}); err != nil {
				if isSentinel(err, application.ErrNotEnoughItems) {
					return refuseMission(screens.MissionRefusedNothingToDeliver)
				}
				return err
			}
			delivered += units
			for i := range stacks {
				if stacks[i].Item == o.Target {
					stacks[i].Qty -= min(stacks[i].Qty, units)
				}
			}
		}
		if delivered == 0 {
			return refuseMission(screens.MissionRefusedNothingToDeliver)
		}
		a.Progress = progress
		if !mission.Done(m.Objectives, progress) {
			notice = &screens.MissionNotice{Kind: screens.MissionNoticeDelivered, Mission: named(def.Code, def.Name), Qty: delivered}
			return tx.Missions().SaveProgress(ctx, a.ID, progress)
		}
		done, err := h.complete(ctx, tx, snap, meta, a, def, now)
		if err != nil {
			return err
		}
		notice = &screens.MissionNotice{Kind: screens.MissionNoticeCompleted, Mission: named(def.Code, def.Name),
			Cash: done.RewardCash, Withheld: done.RewardWithheld, XP: done.RewardXP}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.mineWith(ctx, meta, notice)
}

func padProgress(p []int64, n int) []int64 {
	for len(p) < n {
		p = append(p, 0)
	}
	return p
}

// Abandon handles mission.abandon: giving a mission up, confirmed first.
func (h *MissionsHandler) Abandon(ctx context.Context, meta envelope.Metadata, req MissionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		confirm *screens.MissionView
		notice  *screens.MissionNotice
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		yes := strings.TrimSpace(req.Confirm) == screens.MissionYes
		if yes {
			fresh, err := tx.Idempotency().Reserve(ctx, string(idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)),
				p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
			if err != nil || !fresh {
				return err
			}
		}
		a, err := h.assignment(ctx, tx, p.ID, req.No)
		if err != nil {
			return err
		}
		if a.Status != application.MissionActive {
			return refuseMission(screens.MissionRefusedNotActive)
		}
		def, _ := snap.MissionDef(a.Mission)
		if !yes {
			confirm = &screens.MissionView{Mission: named(a.Mission, def.Name), No: a.No, Abandoning: true}
			return nil
		}
		now := h.now()
		a.Status, a.EndedAt = application.MissionAbandoned, &now
		notice = &screens.MissionNotice{Kind: screens.MissionNoticeAbandoned, Mission: named(a.Mission, def.Name)}
		return tx.Missions().End(ctx, *a)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if confirm != nil {
		return screens.Mission(h.screen(meta, lang), *confirm), nil
	}
	return h.mineWith(ctx, meta, notice)
}

// expire closes a mission whose time ran out.
func (h *MissionsHandler) expire(ctx context.Context, tx application.Tx, a *application.MissionAssignment, now time.Time) error {
	a.Status, a.EndedAt = application.MissionExpired, &now
	if err := tx.Missions().End(ctx, *a); err != nil && !isSentinel(err, application.ErrMissionNotFound) {
		return err
	}
	return refuseMission(screens.MissionRefusedExpired)
}

// dayStart is the start of the UTC day at now: the caps' day.
func dayStart(now time.Time) time.Time {
	y, m, d := now.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// complete pays a mission whose last objective was just met, and closes it:
// its cash within the day's caps (a reward grant and its ledger
// transaction), its XP and its goods, once — the caller holds the player's
// missions lock and the row moves only from active.
func (h *MissionsHandler) complete(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	a *application.MissionAssignment, def content.MissionDef, now time.Time,
) (*application.MissionAssignment, error) {
	reward := def.Mission().Reward
	if reward.Cash > 0 {
		if err := tx.Missions().LockDay(ctx); err != nil {
			return nil, err
		}
		day := dayStart(now)
		mine, err := tx.Missions().PaidSince(ctx, a.PlayerID, day)
		if err != nil {
			return nil, err
		}
		all, err := tx.Missions().PaidSince(ctx, "", day)
		if err != nil {
			return nil, err
		}
		paid, withheld := mission.CapCash(reward.Cash, h.rules.PlayerDailyCap-mine, h.rules.EconomyDailyCap-all)
		a.RewardCash, a.RewardWithheld = paid, withheld
		if paid > 0 {
			grant, _, err := tx.Ledger().RecordGrant(ctx, application.RewardGrant{PlayerID: a.PlayerID,
				Source: application.RewardMission, SourceReferenceID: a.ID, Amount: money.FromMinor(paid),
				GrantedBy: "mission:" + def.Code, CreatedAt: now})
			if err != nil {
				return nil, err
			}
			cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, a.PlayerID)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: grant.LedgerTransactionID,
				Reason: application.ReasonMissionReward, ReferenceType: "reward_grants", ReferenceID: grant.ID,
				Entries: []application.LedgerEntry{{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-paid)},
					{AccountID: cash.ID, Amount: money.FromMinor(paid)}}, CreatedAt: now}); err != nil {
				return nil, err
			}
			a.RewardGrantID = grant.ID
		}
	}
	if reward.XP > 0 {
		row, err := tx.Stats().EnsureDefaults(ctx, a.PlayerID, defaultStats(a.PlayerID, now))
		if err != nil {
			return nil, err
		}
		next, _ := domainStats(*row).AddXP(reward.XP)
		if err := tx.Stats().Save(ctx, storedStats(*row, next)); err != nil {
			return nil, err
		}
		a.RewardXP = reward.XP
	}
	if len(reward.Items) > 0 {
		if err := tx.Items().LockOwner(ctx, a.PlayerID); err != nil {
			return nil, err
		}
		grant := origin{kind: application.OriginGrant, reason: application.ItemGrant, refType: application.MissionReference, refID: a.ID}
		for _, it := range reward.Items {
			if _, err := bring(ctx, tx, snap, h.ids, nil, a.PlayerID, it.Item, it.Qty, -1, grant, now); err != nil {
				return nil, err
			}
		}
	}
	a.Status, a.EndedAt = application.MissionCompleted, &now
	if err := tx.Missions().End(ctx, *a); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(reward.Items))
	for _, it := range reward.Items {
		items = append(items, map[string]any{"item": it.Item, "item_name": itemNamed(snap, it.Item).Name, "qty": it.Qty})
	}
	return a, appendDomainEvent(ctx, tx, meta, "mission", "completed", a.ID, map[string]any{
		"player_id": a.PlayerID, "mission": def.Code, "mission_name": def.Name, "cash": a.RewardCash,
		"withheld": a.RewardWithheld, "xp": a.RewardXP, "items": items})
}

// Mission progress from the game's own events.

// MissionEventSubjects are the events that move missions, and the objective
// each counts toward: the consumer cmd/game subscribes to each.
var MissionEventSubjects = map[string]mission.Kind{
	subjects.Event("travel", "completed"):    mission.Travel,
	subjects.Event("job", "shift_worked"):    mission.Work,
	subjects.Event("education", "completed"): mission.Course,
	subjects.Event("inventory", "bought"):    mission.Buy,
	subjects.Event("inventory", "sold"):      mission.Sell,
	subjects.Event("market", "traded"):       mission.Sell,
	subjects.Event("crime", "attempted"):     mission.Crime,
	subjects.Event("inventory", "used"):      mission.Use,
}

// missionEvent is the fields a mission reads off an event's payload.
type missionEvent struct {
	PlayerID string `json:"player_id"`
	SellerID string `json:"seller_id"`
	BuyerID  string `json:"buyer_id"`
	ToCityID string `json:"to_city_id"`
	Career   string `json:"career"`
	Course   string `json:"course"`
	Item     string `json:"item"`
	Qty      int64  `json:"qty"`
	Crime    string `json:"crime"`
	Result   string `json:"result"`
	Status   string `json:"status"`
}

// OnEvent moves the missions of the player an event is about. It runs once
// per event and player: the event's id is recorded in the missions' inbox
// in the same transaction as the progress. Only what happened after a
// mission was taken counts. A sale to an account the watch links to the
// seller does not count (anti-farming). An error means "not yet": the event
// is redelivered.
func (h *MissionsHandler) OnEvent(ctx context.Context, env *envelope.Envelope, subject string) error {
	kind, ok := MissionEventSubjects[subject]
	if !ok {
		return nil
	}
	var ev missionEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil // unreadable: it never will be
	}
	snap := h.content.Current()
	playerID := ev.PlayerID
	if subject == subjects.Event("market", "traded") {
		playerID = ev.SellerID
	}
	if playerID == "" {
		return nil
	}
	e := mission.Event{Kind: kind, Qty: 1}
	switch kind {
	case mission.Travel:
		if city, err := h.cities.ByID(ctx, ev.ToCityID); err == nil {
			e.Targets = []string{city.Code}
		}
	case mission.Work:
		e.Targets = []string{ev.Career}
		if def, ok := snap.CareerDef(ev.Career); ok {
			e.Targets = append(e.Targets, def.Category)
		}
	case mission.Course:
		e.Targets = []string{ev.Course}
	case mission.Buy, mission.Sell, mission.Use:
		e.Targets = []string{ev.Item}
		if d, ok := snap.ItemDef(ev.Item); ok {
			e.Targets = append(e.Targets, d.Category)
		}
		if ev.Qty > 0 {
			e.Qty = ev.Qty
		}
	case mission.Crime:
		if ev.Result != "succeeded" {
			return nil
		}
		e.Targets = []string{ev.Crime}
		if d, ok := snap.CrimeDef(ev.Crime); ok {
			e.Targets = append(e.Targets, d.Category)
		}
	}
	when := env.Metadata.ReceivedAt
	eventID := env.Metadata.MessageID() + ":" + subject
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		if err := tx.Missions().Lock(ctx, playerID); err != nil {
			return err
		}
		active, err := tx.Missions().Active(ctx, playerID)
		if err != nil || len(active) == 0 {
			return err
		}
		fresh, err := tx.Missions().MarkEvent(ctx, eventID, playerID, subject, h.now())
		if err != nil || !fresh {
			return err
		}
		if subject == subjects.Event("market", "traded") && ev.BuyerID != "" {
			// A sale to an account the watch links to the seller is
			// self-dealing: it moves nothing.
			if _, err := tx.Watch().Linking(ctx, playerID, ev.BuyerID); err == nil {
				return nil
			} else if !isSentinel(err, application.ErrFlagNotFound) {
				return err
			}
		}
		now := h.now()
		meta := env.Metadata
		meta.PlayerID = playerID
		for i := range active {
			a := &active[i]
			def, ok := snap.MissionDef(a.Mission)
			if !ok {
				continue
			}
			if mission.Expired(a.ExpiresAt, now) {
				a.Status, a.EndedAt = application.MissionExpired, &now
				if err := tx.Missions().End(ctx, *a); err != nil && !isSentinel(err, application.ErrMissionNotFound) {
					return err
				}
				continue
			}
			if !when.IsZero() && when.Before(a.AcceptedAt) {
				continue
			}
			m := def.Mission()
			next, moved := mission.Advance(m.Objectives, a.Progress, e)
			if !moved {
				continue
			}
			a.Progress = next
			if !mission.Done(m.Objectives, next) {
				if err := tx.Missions().SaveProgress(ctx, a.ID, next); err != nil {
					return err
				}
				continue
			}
			if _, err := h.complete(ctx, tx, snap, meta, a, def, now); err != nil {
				return err
			}
		}
		return nil
	})
}

package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/life"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// LifeHandler serves a character's life (docs/adr/0025-life-and-legacy.md):
// «🧬 زندگی من» with the needs, mood, age, intelligence, rank and worth; the
// public player card with avatar and bio; the life history; sleeping at a
// hostel or on a bench; the leaderboards and — from the scheduler — their
// refresh once a period on the game clock; and, beside the commands, the
// consumer that lets the game's own events touch a life once each.
type LifeHandler struct {
	uow            application.UnitOfWork
	ids            IDGenerator
	msgs           Translator
	content        ContentSource
	cities         application.CityRepository
	search         application.PlayerSearch
	scale          gametime.Scale
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewLifeHandler builds the handler.
func NewLifeHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, search application.PlayerSearch, scale gametime.Scale, idempotencyTTL time.Duration,
	now func() time.Time,
) *LifeHandler {
	if source == nil || cities == nil || search == nil || ids == nil {
		panic("handlers: NewLifeHandler requires content, cities, a player search and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 {
		panic("handlers: NewLifeHandler requires a game clock and an idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &LifeHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, search: search, scale: scale,
		idempotencyTTL: idempotencyTTL, now: now}
}

// LifeRequest is every life command's payload.
type LifeRequest struct {
	Code   string `json:"code,omitempty"`
	Page   string `json:"page,omitempty"`
	Text   string `json:"text,omitempty"`
	Clear  string `json:"clear,omitempty"`
	Choice string `json:"choice,omitempty"`
	Spot   string `json:"spot,omitempty"`
	Method string `json:"method,omitempty"`
	Board  string `json:"board,omitempty"`
}

// lifeRefusal carries a refusal out of a unit of work.
type lifeRefusal struct{ view screens.LifeRefusalView }

func (r *lifeRefusal) Error() string { return "handlers: life refused: " + r.view.Kind }

func refuseLife(kind string) *lifeRefusal {
	return &lifeRefusal{view: screens.LifeRefusalView{Kind: kind}}
}

// sleepPayment carries the price screen of a night out of a unit of work.
type sleepPayment struct{ view screens.SleepPayView }

func (s *sleepPayment) Error() string { return "handlers: a night to pay for" }

func (h *LifeHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// finish turns what a unit of work ended with into a screen.
func (h *LifeHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *lifeRefusal
	if stderrors.As(err, &r) {
		return screens.LifeRefusal(h.screen(meta, lang), r.view), nil
	}
	var pay *sleepPayment
	if stderrors.As(err, &pay) {
		return screens.SleepPay(h.screen(meta, lang), pay.view), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	return nil, err
}

func (h *LifeHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

func (h *LifeHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// def is the life content; a world without it has no life to show.
func (h *LifeHandler) def(snap *content.Snapshot) (content.LifeDef, error) {
	d, ok := snap.Life()
	if !ok {
		return d, errors.NotFound("the content has no life")
	}
	return d, nil
}

// Me handles life.me: «🧬 زندگی من».
func (h *LifeHandler) Me(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.me(ctx, meta, "", nil)
}

// me renders the life screen, opening with a notice.
func (h *LifeHandler) me(ctx context.Context, meta envelope.Metadata, notice string, args map[string]any) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	view := screens.LifeView{Notice: notice, NoticeArgs: args}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		now := h.now()
		l, err := touchLife(ctx, tx, snap, h.scale, p, now, nil)
		if err != nil || l == nil {
			return err
		}
		worth, err := worthOf(ctx, tx, snap, h.cities, p.ID)
		if err != nil {
			return err
		}
		if err := judgeRank(ctx, tx, snap, meta, l.row, worth.Total(), "look", now); err != nil {
			return err
		}
		if err := tx.Life().Save(ctx, *l.row); err != nil {
			return err
		}
		view.Needs = *needsView(l)
		h.fillLife(def, l.row, now, &view)
		view.Worth = screens.WorthView{Cash: worth.Cash, Bank: worth.Bank, Escrow: worth.Escrow, Equity: worth.Equity,
			Property: worth.Property, Goods: worth.Goods, Debts: worth.Debts, Savings: worth.Savings, Gold: worth.Gold,
			Loans: worth.Loans, Total: worth.Total()}
		ladder := def.Ladder()
		for i, r := range ladder.Ranks {
			if r.Code == l.row.Rank && i+1 < len(ladder.Ranks) {
				next := def.Ranks.Ladder[i+1]
				view.Next = &screens.RankRef{Code: next.Code, Name: next.Name, Emoji: next.Emoji}
				view.NextNeed = max(next.Min-worth.Total(), 1)
			}
		}
		view.Home = l.factors.Home
		return h.spots(ctx, tx, snap, def, p, l.row, now, &view)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Life(h.screen(meta, lang), view), nil
}

// fillLife writes age, intelligence and rank onto the view.
func (h *LifeHandler) fillLife(def content.LifeDef, row *application.PlayerLife, now time.Time, view *screens.LifeView) {
	aging := def.Aging()
	view.Age = aging.Age(row.BornAt, now, h.scale)
	if st, ok := def.Stage(aging.StageOf(view.Age)); ok {
		view.Stage = named(st.Code, st.Name)
	}
	mind := def.Mind()
	view.Intelligence, view.IntelligenceMax = row.Intelligence, mind.Max
	view.CourseBPS, view.SkillBPS = 10000-mind.CourseBPS(row.Intelligence), mind.SkillBPS(row.Intelligence)-10000
	view.Rank = rankRef(def, row.Rank)
}

// spots are where the player may sleep in the city they are in.
func (h *LifeHandler) spots(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.LifeDef,
	p *application.Player, row *application.PlayerLife, now time.Time, view *screens.LifeView,
) error {
	if row.LastSleepAt != nil {
		if ready := row.LastSleepAt.Add(h.scale.RealWait(def.Sleep.CooldownDuration())); ready.After(now) {
			view.SleepIn = ready.Sub(now)
		}
	}
	w, err := locate(ctx, tx, h.cities, snap, p)
	if err != nil || w.city == nil {
		return err
	}
	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		return nil
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return err
	}
	for _, s := range def.Sleep.Spots {
		line := screens.SleepSpotLine{Spot: named(s.Code, s.Name), Place: placeNamed(snap, s.Place), Price: s.Price,
			Rest: s.Rest, Relief: s.Relief}
		if w.placed() {
			target, ok := w.cmap.Find(s.Place)
			if !ok {
				continue
			}
			if w.walk == nil && w.here.Code != target.Code {
				line.Way = &screens.Way{Place: placeNamed(snap, target.Code), Walk: h.scale.RealWait(target.MoveTime)}
			}
		}
		view.Spots = append(view.Spots, line)
	}
	return nil
}

// Sleep handles life.sleep: a night at a hostel or on a bench, where the
// player stands, once a cooldown; a paid bed shows its price first.
func (h *LifeHandler) Sleep(ctx context.Context, meta envelope.Metadata, req LifeRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	var slept map[string]any
	replayed := false
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		spot, ok := def.Spot(strings.ToLower(strings.TrimSpace(req.Spot)))
		if !ok {
			return refuseLife(screens.LifeRefusedNoSpot)
		}
		method, chosen, err := chosenMethod(req.Method)
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
		now := h.now()
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
			return refuseLife(screens.LifeRefusedNoCity)
		} else if !isSentinel(err, application.ErrNoActiveTravel) {
			return err
		}
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if w.city == nil {
			return refuseLife(screens.LifeRefusedNoCity)
		}
		if w.placed() {
			target, ok := w.cmap.Find(spot.Place)
			if !ok {
				return refuseLife(screens.LifeRefusedNoSpot)
			}
			if err := needAt(w, snap, target, "place.need.sleep", nil, h.scale, now); err != nil {
				return thenFor(err, "life.me")
			}
		}
		// The stats row, then the life row, lock first — the order every
		// change to a life takes — so two presses sleep once.
		if _, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now)); err != nil {
			return err
		}
		row, err := tx.Life().Ensure(ctx, lifeDefaults(def, p.ID, p.CreatedAt, now))
		if err != nil {
			return err
		}
		if row.LastSleepAt != nil {
			if ready := row.LastSleepAt.Add(h.scale.RealWait(def.Sleep.CooldownDuration())); ready.After(now) {
				r := refuseLife(screens.LifeRefusedTooSoon)
				r.view.Wait = ready.Sub(now)
				return r
			}
		}
		night := application.LifeSleep{ID: h.ids.NewID(), PlayerID: p.ID, Spot: spot.Code, CityID: w.city.ID,
			Price: spot.Price, SleptAt: now}
		if spot.Price > 0 {
			wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
			if err != nil {
				return err
			}
			plan := wallet.Plan(money.FromMinor(spot.Price), snap.Accepts(content.ServiceLodging))
			if !chosen {
				return &sleepPayment{view: screens.SleepPayView{Spot: named(spot.Code, spot.Name), Rest: spot.Rest,
					Relief: spot.Relief, Payment: paymentChoice(plan, wallet)}}
			}
			if err := checkMethod(plan, method, wallet, "life.button.open", screens.AddrLife); err != nil {
				return err
			}
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, w.city.ID)
			if err != nil {
				return err
			}
			txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
				Method: method, Accepted: plan.Accepted, Reason: application.ReasonLodgingFee,
				ReferenceType: application.SleepReference, ReferenceID: night.ID,
				To: []application.LedgerEntry{{AccountID: treasury.ID, Amount: money.FromMinor(spot.Price)}}, CreatedAt: now,
			})
			if err != nil {
				if stderrors.Is(err, application.ErrPaymentDeclined) {
					return declined(plan, wallet, "life.button.open", screens.AddrLife)
				}
				return err
			}
			night.Method, night.LedgerTx = string(method), txID
		}
		if err := tx.Life().RecordSleep(ctx, night); err != nil {
			return err
		}
		if _, err := touchLife(ctx, tx, snap, h.scale, p, now, func(l *lifeNow) {
			l.needs = l.needs.Change(0, -spot.Rest, -spot.Relief)
			l.row.LastSleepAt = &now
		}); err != nil {
			return err
		}
		slept = map[string]any{"spot": spot.Code, "rest": spot.Rest}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed || slept == nil {
		return h.Me(ctx, meta)
	}
	c := h.screen(meta, lang)
	return h.me(ctx, meta, screens.LifeNoticeSlept, map[string]any{
		"spot": c.SleepSpotName(named(slept["spot"].(string), "")), "rest": slept["rest"]})
}

// Card handles life.card: a player's card — the player's own without a
// code, else the one a code or a username names.
func (h *LifeHandler) Card(ctx context.Context, meta envelope.Metadata, req LifeRequest) (*presenter.Response, error) {
	return h.card(ctx, meta, req.Code, "")
}

func (h *LifeHandler) card(ctx context.Context, meta envelope.Metadata, code, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	view := screens.CardView{Notice: notice}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		me, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		target, err := h.target(ctx, me, code)
		if err != nil {
			return err
		}
		now := h.now()
		view.Self = target.ID == me.ID
		view.Name, view.Code, view.JoinedAt = shownName(target), target.PublicCode, target.CreatedAt
		row, err := tx.Life().Get(ctx, target.ID)
		if err != nil {
			return err
		}
		if row == nil {
			d := lifeDefaults(def, target.ID, target.CreatedAt, now)
			row = &d
		}
		aging := def.Aging()
		view.Age = aging.Age(row.BornAt, now, h.scale)
		if st, ok := def.Stage(aging.StageOf(view.Age)); ok {
			view.Stage = named(st.Code, st.Name)
		}
		view.Rank, view.Bio = rankRef(def, row.Rank), row.Bio
		switch row.Avatar {
		case "":
		case content.AvatarPhoto:
			view.Avatar.Photo = true
			photo := &presenter.Photo{UserID: target.TelegramUserID, PlayerID: target.ID}
			if file, at, err := tx.Life().Photo(ctx, target.ID, meta.BotID); err != nil {
				return err
			} else if file != "" && now.Sub(at) < def.PhotoTTLDuration() {
				photo.FileID = file
			}
			view.Photo = photo
		default:
			if a, ok := def.Avatar(row.Avatar); ok {
				view.Avatar = screens.AvatarRef{Code: a.Code, Emoji: a.Emoji}
			}
		}
		if st, err := tx.Stats().Get(ctx, target.ID); err == nil {
			view.Level = st.Level
		}
		if view.Achievements, err = EarnedCount(ctx, tx, target.ID); err != nil {
			return err
		}
		_, view.Entries, err = tx.Life().History(ctx, target.ID, true, 0, 1)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Card(h.screen(meta, lang), view), nil
}

// target is the player a code or username names, the asker without one.
func (h *LifeHandler) target(ctx context.Context, me *application.Player, code string) (*application.Player, error) {
	code = strings.TrimSpace(code)
	if code == "" || strings.EqualFold(code, me.PublicCode) {
		return me, nil
	}
	q, ok := ClassifyPlayerQuery(code)
	if !ok {
		return nil, refuseLife(screens.LifeRefusedNoPlayer)
	}
	p, err := h.search.Find(ctx, q)
	if isSentinel(err, application.ErrPlayerNotFound) {
		return nil, refuseLife(screens.LifeRefusedNoPlayer)
	}
	return p, err
}

// History handles life.history: the player's own timeline, or another's
// public one, a page at a time.
func (h *LifeHandler) History(ctx context.Context, meta envelope.Metadata, req LifeRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	var view screens.HistoryView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		me, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		target, err := h.target(ctx, me, req.Code)
		if err != nil {
			return err
		}
		view.Self = target.ID == me.ID
		view.Name, view.Code = shownName(target), target.PublicCode
		size := def.History.PageSize
		page, _ := strconv.Atoi(strings.TrimSpace(req.Page))
		page = max(page, 1)
		entries, total, err := tx.Life().History(ctx, target.ID, !view.Self, (page-1)*size, size)
		if err != nil {
			return err
		}
		pages := max((total+size-1)/size, 1)
		if page > pages {
			page = pages
			if entries, _, err = tx.Life().History(ctx, target.ID, !view.Self, (page-1)*size, size); err != nil {
				return err
			}
		}
		view.Page, view.Pages, view.Total = page, pages, total
		for _, e := range entries {
			view.Lines = append(view.Lines, historyLine(e))
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.History(h.screen(meta, lang), view), nil
}

// historyLine is an entry as the timeline shows it.
func historyLine(e application.HistoryEntry) screens.HistoryLine {
	d := e.Data
	return screens.HistoryLine{Kind: e.Kind, At: e.At, Code: d.Code, Name: d.Name, Sub: d.Sub, SubName: d.SubName,
		PlaceKind: d.PlaceKind, Place: named(d.PlaceCode, d.PlaceName), Amount: d.Amount, Number: d.Number,
		Backfilled: e.Backfilled, Private: !e.Public}
}

// Bio handles life.bio: the typed bio saved once it passes the rules, or
// the bio cleared.
func (h *LifeHandler) Bio(ctx context.Context, meta envelope.Metadata, req LifeRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	clear := strings.EqualFold(strings.TrimSpace(req.Clear), "yes")
	if !clear && strings.TrimSpace(req.Text) == "" {
		return h.card(ctx, meta, "", "")
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	notice := screens.LifeNoticeBio
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		bio := ""
		if !clear {
			rules := def.BioRules()
			cleaned, err := rules.Clean(req.Text)
			if err != nil {
				kind := map[error]string{life.ErrBioLength: screens.LifeRefusedBioLength, life.ErrBioLink: screens.LifeRefusedBioLink,
					life.ErrBioBlocked: screens.LifeRefusedBioBlocked, life.ErrBioChars: screens.LifeRefusedBioChars}[err]
				if kind == "" {
					kind = screens.LifeRefusedBioChars
				}
				r := refuseLife(kind)
				r.view.Min, r.view.Max = rules.MinRunes, rules.MaxRunes
				return r
			}
			bio = cleaned
		}
		if bio == "" {
			notice = screens.LifeNoticeBioGone
		}
		now := h.now()
		row, err := tx.Life().Ensure(ctx, lifeDefaults(def, p.ID, p.CreatedAt, now))
		if err != nil {
			return err
		}
		row.Bio, row.UpdatedAt = bio, now
		return tx.Life().Save(ctx, *row)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.card(ctx, meta, "", notice)
}

// Avatar handles life.avatar: the choice of avatar, or choosing one — a
// content avatar, the player's Telegram photo, or none.
func (h *LifeHandler) Avatar(ctx context.Context, meta envelope.Metadata, req LifeRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	lang := meta.Language
	choice := strings.ToLower(strings.TrimSpace(req.Choice))
	var picker *screens.AvatarsView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		now := h.now()
		if choice == "" {
			row, err := tx.Life().Get(ctx, p.ID)
			if err != nil {
				return err
			}
			v := screens.AvatarsView{}
			if row != nil {
				if row.Avatar == content.AvatarPhoto {
					v.Current.Photo = true
				} else if a, ok := def.Avatar(row.Avatar); ok {
					v.Current = screens.AvatarRef{Code: a.Code, Emoji: a.Emoji}
				}
			}
			for _, a := range def.Avatars {
				v.Avatars = append(v.Avatars, screens.AvatarChoice{Code: a.Code, Name: a.Name, Emoji: a.Emoji})
			}
			picker = &v
			return nil
		}
		avatar := choice
		switch choice {
		case content.AvatarNone:
			avatar = ""
		case content.AvatarPhoto:
			// Fetched afresh: the photo may have changed since a bot kept it.
			if err := tx.Life().ForgetPhotos(ctx, p.ID); err != nil {
				return err
			}
		default:
			if _, ok := def.Avatar(choice); !ok {
				return refuseLife(screens.LifeRefusedNoAvatar)
			}
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		row, err := tx.Life().Ensure(ctx, lifeDefaults(def, p.ID, p.CreatedAt, now))
		if err != nil {
			return err
		}
		row.Avatar, row.UpdatedAt = avatar, now
		return tx.Life().Save(ctx, *row)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if picker != nil {
		return screens.Avatars(h.screen(meta, lang), *picker), nil
	}
	return h.card(ctx, meta, "", screens.LifeNoticeAvatar)
}

// Top handles life.top: one leaderboard as it was last refreshed.
func (h *LifeHandler) Top(ctx context.Context, meta envelope.Metadata, req LifeRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	board := strings.ToLower(strings.TrimSpace(req.Board))
	known := false
	for _, b := range application.Boards {
		known = known || b == board
	}
	if !known {
		board = application.BoardRichest
	}
	lang := meta.Language
	view := screens.BoardView{Board: board, Ranks: map[string]screens.RankRef{}}
	for _, r := range def.Ranks.Ladder {
		view.Ranks[r.Code] = screens.RankRef{Code: r.Code, Name: r.Name, Emoji: r.Emoji}
	}
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		lines, _, at, err := tx.Life().Board(ctx, board)
		if err != nil {
			return err
		}
		view.At = at
		for _, l := range lines {
			bl := screens.BoardLine{Position: l.Position, Code: l.Code, Name: l.Name, Tag: l.Tag, TagName: l.TagName,
				Value: l.Value, Extra: l.Extra, Extra2: l.Extra2}
			switch board {
			case application.BoardCompanies:
				// The tag is the company's city and kind: «city|type».
				city, kind, _ := strings.Cut(l.Tag, "|")
				bl.City, bl.Tag = named(city, l.TagName), kind
				if t, _, ok := snap.CompanyType(kind); ok {
					bl.TagName = t.Name
				}
			case application.BoardWorkers:
				if d, ok := snap.CareerDef(l.Tag); ok {
					bl.TagName = d.Name
				}
			}
			bl.Mine = (board == application.BoardRichest || board == application.BoardWorkers ||
				board == application.BoardInvestors) && l.Code == p.PublicCode
			view.Lines = append(view.Lines, bl)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.Leaderboard(h.screen(meta, lang), view), nil
}

// LeaderboardPayload is the jsonb a leaderboard period's action carries.
type LeaderboardPayload struct {
	PeriodNo int64 `json:"period_no"`
}

// StartClock makes sure the leaderboards' clock is running. It is
// idempotent.
func (h *LifeHandler) StartClock(ctx context.Context) error {
	def, ok := h.content.Current().Life()
	if !ok {
		return nil
	}
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.Life().Clock(ctx, now)
		if err != nil || clock.ActionID != "" {
			return err
		}
		clock.PeriodStartedAt = now
		return h.schedule(ctx, tx, def, clock, now)
	})
}

// schedule puts the end of the clock's period on the schedule.
func (h *LifeHandler) schedule(ctx context.Context, tx application.Tx, def content.LifeDef, clock *application.LeaderboardClock,
	now time.Time,
) error {
	wait := h.scale.RealWait(def.Leaderboards.PeriodDuration())
	next := clock.PeriodStartedAt.Add(wait)
	if !next.After(now) {
		next = now.Add(wait)
	}
	payload, err := json.Marshal(LeaderboardPayload{PeriodNo: clock.PeriodNo})
	if err != nil {
		return err
	}
	actionID := h.ids.NewID()
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: actionID, ActionType: application.LeaderboardActionType, ActorType: "system",
		ReferenceType: application.LeaderboardReference, ReferenceID: actionID, Payload: payload,
		StartedAt: now, FinishAt: next,
	}); err != nil {
		return err
	}
	clock.NextAt, clock.ActionID, clock.UpdatedAt = &next, actionID, now
	return tx.Life().SaveClock(ctx, *clock)
}

// Refresh handles life.refresh from the SCHEDULER: one leaderboard period,
// exactly once — the clock row locked first and naming this action and
// period, the period recorded by its number — every rank judged anew, the
// boards written, the next period scheduled.
func (h *LifeHandler) Refresh(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in LeaderboardPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("leaderboard payload is unreadable").WithCause(err)
		}
	}
	if in.PeriodNo < 1 {
		return nil, errors.InvalidInput("leaderboard period names no period")
	}
	snap := h.content.Current()
	def, ok := snap.Life()
	if !ok {
		return nil, nil
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		now := h.now()
		clock, err := tx.Life().Clock(ctx, now)
		if err != nil {
			return err
		}
		if clock.PeriodNo != in.PeriodNo || clock.ActionID == "" || (req.ActionID != "" && clock.ActionID != req.ActionID) {
			return nil
		}
		if clock.NextAt != nil && now.Before(*clock.NextAt) {
			return errors.Internal(stderrors.New("handlers: a leaderboard period ran before it ended"))
		}
		fresh, err := tx.Life().RecordPeriod(ctx, clock.PeriodNo, now)
		if err != nil {
			return err
		}
		if fresh {
			if err := h.refresh(ctx, tx, snap, def, meta, clock.PeriodNo, now); err != nil {
				return err
			}
		}
		clock.PeriodNo++
		clock.PeriodStartedAt = now
		clock.NextAt, clock.ActionID = nil, ""
		return h.schedule(ctx, tx, def, clock, now)
	})
}

// refresh judges every rank and writes one period's boards.
func (h *LifeHandler) refresh(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.LifeDef,
	meta envelope.Metadata, period int64, now time.Time,
) error {
	size := def.Leaderboards.Size
	prices, err := netWorthPrices(ctx, tx, snap, h.cities, nil)
	if err != nil {
		return err
	}
	worths, err := tx.Life().NetWorth(ctx, prices, "")
	if err != nil {
		return err
	}
	byID := make(map[string]*application.PlayerLife, len(worths))
	source := "period:" + strconv.FormatInt(period, 10)
	for _, n := range worths {
		// Each life is read locked, as it stands now: a need a player's
		// action just moved is never written back stale.
		row, err := tx.Life().Ensure(ctx, lifeDefaults(def, n.PlayerID, n.JoinedAt, now))
		if err != nil {
			return err
		}
		byID[n.PlayerID] = row
		row.Equity = n.Worth.Equity
		if err := judgeRank(ctx, tx, snap, meta, row, n.Worth.Total(), source, now); err != nil {
			return err
		}
		row.UpdatedAt = now
		if err := tx.Life().Save(ctx, *row); err != nil {
			return err
		}
	}
	shown := func(n application.NetWorth) string {
		if n.Name == fallbackDisplayName(n.TelegramUserID) {
			return ""
		}
		return n.Name
	}
	var lines []application.LeaderLine
	sort.SliceStable(worths, func(i, j int) bool { return worths[i].Worth.Total() > worths[j].Worth.Total() })
	for i, n := range worths[:min(size, len(worths))] {
		rank := ""
		if row := byID[n.PlayerID]; row != nil {
			rank = row.Rank
		} else {
			rank, _ = def.Ladder().Next("", n.Worth.Total())
		}
		lines = append(lines, application.LeaderLine{Board: application.BoardRichest, Position: i + 1, Code: n.Code,
			Name: shown(n), Tag: rank, Value: n.Worth.Total()})
	}
	investors, err := h.investors(ctx, tx, snap, prices.GoldBid, size, now)
	if err != nil {
		return err
	}
	lines = append(lines, investors...)
	companies, err := tx.Life().TopCompanies(ctx, size)
	if err != nil {
		return err
	}
	for i, l := range companies {
		l.Position = i + 1
		lines = append(lines, l)
	}
	standings, err := tx.Life().Cities(ctx)
	if err != nil {
		return err
	}
	score := def.CityScore()
	sort.SliceStable(standings, func(i, j int) bool {
		a, b := standings[i], standings[j]
		return score.Score(a.Residents, a.Companies, a.Treasury, a.DamageBPS) > score.Score(b.Residents, b.Companies, b.Treasury, b.DamageBPS)
	})
	for i, c := range standings[:min(size, len(standings))] {
		lines = append(lines, application.LeaderLine{Board: application.BoardCities, Position: i + 1, Code: c.Code,
			Name: c.Name, Value: score.Score(c.Residents, c.Companies, c.Treasury, c.DamageBPS), Extra: c.Residents,
			Extra2: c.Companies})
	}
	workers, err := tx.Life().TopWorkers(ctx, size)
	if err != nil {
		return err
	}
	for i, l := range workers {
		l.Position = i + 1
		lines = append(lines, l)
	}
	if err := tx.Life().SaveBoard(ctx, period, lines); err != nil {
		return err
	}
	return tx.Life().Prune(ctx, period-int64(def.Leaderboards.Keep)+1)
}

// investors is the investors' board (docs/adr/0026): what each player's
// portfolio — shares at market price, gold at the dealer's price, savings —
// gained over what they put into it since the last board, the best first.
// Every portfolio is marked for the next board.
func (h *LifeHandler) investors(ctx context.Context, tx application.Tx, snap *content.Snapshot, goldBid int64, size int,
	now time.Time,
) ([]application.LeaderLine, error) {
	if _, ok := snap.Finance(); !ok {
		return nil, nil
	}
	ports, err := tx.Finance().Portfolios(ctx, "", goldBid)
	if err != nil {
		return nil, err
	}
	marks, err := tx.Finance().Marks(ctx)
	if err != nil {
		return nil, err
	}
	type growth struct {
		p     application.Portfolio
		grown int64
	}
	var grew []growth
	next := make([]application.PortfolioMark, 0, len(ports))
	for _, p := range ports {
		if m, ok := marks[p.PlayerID]; ok && p.Gain() > m.Gain {
			grew = append(grew, growth{p: p, grown: p.Gain() - m.Gain})
		}
		next = append(next, application.PortfolioMark{PlayerID: p.PlayerID, Gain: p.Gain(), Value: p.Value(), At: now})
	}
	if err := tx.Finance().SaveMarks(ctx, next); err != nil {
		return nil, err
	}
	sort.SliceStable(grew, func(i, j int) bool { return grew[i].grown > grew[j].grown })
	var lines []application.LeaderLine
	for i, g := range grew[:min(size, len(grew))] {
		name := g.p.Name
		if name == fallbackDisplayName(g.p.TelegramUserID) {
			name = ""
		}
		lines = append(lines, application.LeaderLine{Board: application.BoardInvestors, Position: i + 1, Code: g.p.Code,
			Name: name, Value: g.grown, Extra: g.p.Value()})
	}
	return lines, nil
}

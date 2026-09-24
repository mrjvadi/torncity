// Package handlers holds the command use cases.
//
// A handler orchestrates: it validates idempotency, opens one unit of work,
// asks repositories for state, applies domain rules, appends the outbox record
// and returns a presentation model. It never renders Telegram output and never
// publishes to the broker directly — the outbox worker does that, so an event
// cannot survive a rolled-back transaction or be lost after a committed one.
package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// How long a command key blocks a replay is NOT declared here any more. It is
// game.idempotency_ttl in configs/config.yml, injected through
// NewProfileHandler, and internal/config refuses to load a value that does not
// outlast the broker's redelivery schedule — a check this package could not
// make, because it cannot see the broker's settings.

// IDGenerator produces identifiers. It is a port so tests get deterministic ids.
type IDGenerator interface {
	NewID() string
}

// Translator resolves a message key for a language.
//
// It is declared here, at the point of use, so the handler depends on the
// one method it needs rather than on a concrete catalogue. Both a loaded
// catalogue and a hot-reloadable store satisfy it, which is what lets the
// text change under a running process without this package knowing.
type Translator interface {
	T(lang, key string, args map[string]any) string
}

// ProfileHandler serves player.profile.get, the phase 0 command.
//
// It also performs first contact: a Telegram user who has never played gets a
// player record here. That record is keyed on telegram_user_id alone, so the
// same person reaching the game through any bot in the fleet is the same
// player. See docs/adr/0001-telegram-bot-fleet.md.
type ProfileHandler struct {
	uow            application.UnitOfWork
	ids            IDGenerator
	msgs           Translator
	cities         application.CityRepository
	defaultL       string
	idempotencyTTL time.Duration
	now            func() time.Time

	// content and policy, when set by WithWork, let the profile show the
	// player's job and studies.
	content ContentSource
	policy  application.PolicyReader
}

// NewProfileHandler wires the handler.
//
// msgs is injected rather than read from a package global so that a test, a
// second deployment or a future per-bot catalogue can supply its own text.
// It may be nil, in which case messages resolve to their keys: an unwired
// catalogue then shows "profile.title" in the chat, which is wrong in an
// obvious way instead of crashing a player's session.
//
// now may be nil, in which case UTC wall clock is used; tests inject a fixed
// clock.
// defaultLanguage is the language stamped on a player record when Telegram
// told us nothing. It is injected rather than fixed here because it is a
// product setting, and because a player's stored language is not the same
// thing as the message catalogue's fallback: the catalogue falls back so a
// screen still renders, while this decides what a new account IS. An empty
// value is rejected, so a caller cannot forget to make the choice.
//
// idempotencyTTL is how long a reserved key blocks a replay. It is injected
// for the same reason and rejected at zero for a sharper one: a zero TTL is
// read by the idempotency store as an expiry that has already passed, so every
// redelivery would be executed as if it were new and a player would be charged
// twice. It must outlast the broker's redelivery schedule, which
// internal/config validates.
//
// cities is the one phase 1 repository injected here: cities are read-only
// content. Stats are written on every read (see condition), so they are
// reached through the unit of work's Tx instead.
func NewProfileHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	cities application.CityRepository,
	defaultLanguage string,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *ProfileHandler {
	if defaultLanguage == "" {
		panic("handlers: NewProfileHandler requires a default language")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewProfileHandler requires a positive idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &ProfileHandler{
		uow:            uow,
		ids:            ids,
		msgs:           msgs,
		cities:         cities,
		defaultL:       defaultLanguage,
		idempotencyTTL: idempotencyTTL,
		now:            now,
	}
}

// WithWork lets the profile show the player's job — its title, where, and
// what a shift pays under the city's minimum wage — and their studies: the
// course in progress and the certificates held. The home screen is where a
// player looks for these first. Without it the profile says nothing about
// work either way. It returns h so it can be chained onto the constructor.
func (h *ProfileHandler) WithWork(source ContentSource, policy application.PolicyReader) *ProfileHandler {
	h.content, h.policy = source, policy
	return h
}

// keyTranslator is the no-catalogue fallback described on NewProfileHandler.
type keyTranslator struct{}

func (keyTranslator) T(_, key string, _ map[string]any) string { return key }

// Handle processes one player.profile.get command.
func (h *ProfileHandler) Handle(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	var (
		record *application.Player
		view   screens.ProfileView
	)

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.ensurePlayer(ctx, tx, meta)
		if err != nil {
			return err
		}
		record = p

		// Reserve after the player exists, because the key is scoped to the
		// player id. A replay of the same request finds the key taken and
		// skips the side effect, but still returns the profile below.
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if fresh {
			if err := tx.Players().LinkBot(ctx, application.BotLink{
				PlayerID:       p.ID,
				BotID:          meta.BotID,
				TelegramChatID: meta.TelegramChatID,
				IsReachable:    true,
			}); err != nil {
				return err
			}
		}

		// The condition is read on EVERY delivery, replay or not. It is a
		// read, so repeating it changes nothing, and suppressing it would
		// answer a redelivered request with a blank profile.
		view, err = h.condition(ctx, tx, p)
		return err
	})
	if err != nil {
		return nil, err
	}

	return h.renderProfile(meta, record, view), nil
}

// condition loads the player's live state, catching their energy up to now.
//
// # Reading your profile is how energy catches up
//
// There is no ticker anywhere that tops players up. Energy accrues as a pure
// function of elapsed time (player.RegenerateEnergy), and the elapsed time is
// measured from the row's own updated_at, so the amount a player has is fully
// determined by when they last looked — not by whether a background job was
// running, whether the process restarted, or how many players exist. A
// hundred thousand idle accounts cost nothing, because nobody pays to
// regenerate energy for a player who is not there to spend it.
//
// The price is that a read WRITES. That is deliberate and it is the whole
// mechanism: if the caught-up value were not persisted, the next read would
// measure from the same old timestamp and regenerate the same energy again,
// and a player refreshing the screen would watch their bar refill for free.
//
// Both stats calls go through tx. On first contact the player row was created
// earlier in this same transaction and is not yet visible to any other
// connection, so a stats row written on a separate connection could not even
// satisfy its foreign key to players; on tx it can, and a failed request
// leaves neither a player nor stats behind.
func (h *ProfileHandler) condition(ctx context.Context, tx application.Tx, p *application.Player) (screens.ProfileView, error) {
	// The record's id, stored language and account status are not copied
	// onto the view: none of them means anything to a player. Nor is the
	// placeholder name a record gets when no real one was on hand: it is
	// derived from the Telegram account number, and showing it would put an
	// identifier on screen dressed up as a name.
	var view screens.ProfileView
	if p.DisplayName != fallbackDisplayName(p.TelegramUserID) {
		view.Name = p.DisplayName
	}
	// The public code IS copied: unlike the record's id it is meant to be
	// seen, and it is how a friend finds this player.
	view.Code = p.PublicCode

	row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, h.now()))
	if err != nil {
		return view, err
	}

	regenerated, changed := regenerateEnergy(*row, h.now())
	if changed {
		if err := tx.Stats().Save(ctx, regenerated); err != nil {
			return view, err
		}
	}

	view.Level = regenerated.Level
	view.XP = regenerated.XP
	if regenerated.Level < player.MaxLevel {
		view.NextLevelXP = player.XPForLevel(regenerated.Level + 1)
	}
	view.Energy = regenerated.Energy
	view.MaxEnergy = regenerated.MaxEnergy
	view.EnergyFullIn = energyFullIn(regenerated, h.now())
	view.Health = regenerated.Health
	view.MaxHealth = regenerated.MaxHealth

	// Both of the player's accounts, opened on first sight so a new player
	// reads zero rather than nothing.
	cash, bankAcct, err := playerAccounts(ctx, tx.Ledger(), p.ID)
	if err != nil {
		return view, err
	}
	view.Cash, view.Bank = cash.Balance.Minor(), bankAcct.Balance.Minor()

	// A player on the road is shown the journey rather than the city they
	// left, and the home screen offers the journey instead of the map.
	if t, err := tx.Travels().Active(ctx, p.ID); err == nil {
		view.Travelling = true
		view.TravelRemaining = travel.Remaining(travel.Journey{
			FromCityID: t.FromCityID,
			ToCityID:   t.ToCityID,
			DepartedAt: t.DepartedAt,
			ArrivesAt:  t.ArrivesAt,
			Status:     travel.Status(t.Status),
		}, h.now())
		if to, err := h.cities.ByID(ctx, t.ToCityID); err == nil {
			view.TravelToCode = to.Code
			view.TravelTo = to.Name
		} else if !isSentinel(err, application.ErrCityNotFound) {
			return view, err
		}
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return view, err
	}

	if h.content != nil {
		work, err := h.work(ctx, tx, p)
		if err != nil {
			return view, err
		}
		view.Work = work
	}

	// A player in jail sees it first, with the time left and when they are
	// free; the home screen then offers the jail instead of the map.
	jail, err := h.jail(ctx, tx, p.ID)
	if err != nil {
		return view, err
	}
	view.Jail = jail

	if p.CityID != nil && *p.CityID != "" {
		city, err := h.cities.ByID(ctx, *p.CityID)
		if err != nil {
			// A player whose city has gone missing from content still has a
			// profile. The screen shows that they are nowhere, which is
			// visibly odd and therefore gets noticed, instead of failing the
			// one screen a player opens to find out what is wrong.
			if !isSentinel(err, application.ErrCityNotFound) {
				return view, err
			}
		} else {
			view.CityCode = city.Code
			view.City = city.Name
			if err := h.place(ctx, tx, p, city, &view); err != nil {
				return view, err
			}
		}
	}
	return view, nil
}

// place fills in where in their city the player stands, or the walk they are
// on. A city without places, or a profile served without content, says only
// the city.
func (h *ProfileHandler) place(ctx context.Context, tx application.Tx, p *application.Player, city *application.City,
	view *screens.ProfileView,
) error {
	if h.content == nil || view.Travelling {
		return nil
	}
	snap := h.content.Current()
	cmap := snap.CityMap(city.Code)
	if len(cmap.Places) == 0 {
		return nil
	}
	walk, err := tx.Places().ActiveMove(ctx, p.ID)
	switch {
	case err == nil:
		now := h.now()
		view.Walk = &screens.WalkView{
			To: placeNamed(snap, walk.To), Remaining: walk.ArrivesAt.Sub(now), ArrivesAt: walk.ArrivesAt,
		}
		return nil
	case !isSentinel(err, application.ErrNotMoving):
		return err
	}
	code, err := tx.Places().Where(ctx, p.ID)
	if err != nil {
		return err
	}
	if here, ok := cmap.Current(code); ok {
		view.Place = placeNamed(snap, here.Code)
	}
	return nil
}

// jail reads the sentence the player is serving, or nil when they are free.
func (h *ProfileHandler) jail(ctx context.Context, tx application.Tx, playerID string) (*screens.ProfileJail, error) {
	now := h.now()
	s, err := tx.Crime().ActiveSentence(ctx, playerID)
	switch {
	case isSentinel(err, application.ErrNotJailed):
		return nil, nil
	case err != nil:
		return nil, err
	case !s.Serving(now):
		return nil, nil
	}
	j := &screens.ProfileJail{Remaining: s.EndsAt.Sub(now), EndsAt: s.EndsAt}
	if city, err := h.cities.ByID(ctx, s.CityID); err == nil {
		j.CityCode, j.City = city.Code, city.Name
	} else if !isSentinel(err, application.ErrCityNotFound) {
		return nil, err
	}
	return j, nil
}

// work reads the player's job and studies for the home screen. It only
// reads: nothing is paid, finished or promoted by looking at the profile.
func (h *ProfileHandler) work(ctx context.Context, tx application.Tx, p *application.Player) (*screens.ProfileWork, error) {
	snap := h.content.Current()
	w := &screens.ProfileWork{}

	emp, err := tx.Employment().Current(ctx, p.ID)
	switch {
	case isSentinel(err, application.ErrNotEmployed):
	case err != nil:
		return nil, err
	default:
		def, ok := snap.CareerDef(emp.CareerCode)
		if !ok {
			// A job in a career the content no longer has cannot be named;
			// the job screen explains it, the home screen stays up.
			break
		}
		job := &screens.ProfileJob{Job: jobRef(def, emp.Tier), Pay: emp.Rate}
		city, err := h.cities.ByID(ctx, emp.CityID)
		if err != nil && !isSentinel(err, application.ErrCityNotFound) {
			return nil, err
		}
		if city != nil {
			job.CityCode, job.City = city.Code, city.Name
			if h.policy != nil {
				// What a shift pays is the stored rate raised to the city's
				// minimum wage, exactly as the job screen and the payroll
				// compute it.
				if pol, err := readLabourPolicy(ctx, h.policy, *city); err == nil {
					job.Pay = max(emp.Rate, pol.MinimumWage.Minor())
				}
			}
		}
		// A shift in progress, so the home screen says the player is at
		// work and until when.
		shift, err := tx.Employment().ActiveShift(ctx, p.ID)
		switch {
		case isSentinel(err, application.ErrNoShiftInProgress):
		case err != nil:
			return nil, err
		default:
			job.ShiftEndsIn = max(shift.EndsAt.Sub(h.now()), time.Nanosecond)
		}
		w.Job = job
	}

	enrolment, err := tx.Education().Active(ctx, p.ID)
	switch {
	case isSentinel(err, application.ErrNoActiveEnrollment):
	case err != nil:
		return nil, err
	default:
		d := domainEnrollment(*enrolment)
		w.Course = &screens.ProfileCourse{
			Course:    courseRef(snap, enrolment.CourseCode),
			Remaining: d.Remaining(h.now()),
			Paused:    d.IsPaused(),
		}
	}

	certs, err := tx.Education().Certifications(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	w.Certificates = len(certs)
	return w, nil
}

// ensurePlayer loads the player, creating one on first contact and emitting
// player.created through the outbox in the same transaction.
func (h *ProfileHandler) ensurePlayer(ctx context.Context, tx application.Tx, meta envelope.Metadata) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err == nil {
		return p, nil
	}
	if !stderrors.Is(err, application.ErrPlayerNotFound) {
		return nil, err
	}

	lang := meta.Language
	if lang == "" {
		lang = h.defaultL
	}
	p = &application.Player{
		ID:             h.ids.NewID(),
		TelegramUserID: meta.TelegramUserID,
		DisplayName:    fallbackDisplayName(meta.TelegramUserID),
		Language:       lang,
		Status:         "active",
		CreatedAt:      h.now(),
	}
	if err := tx.Players().Create(ctx, p); err != nil {
		return nil, err
	}

	ev, err := events.New("player.created", "player", p.ID, map[string]any{
		"player_id":        p.ID,
		"telegram_user_id": p.TelegramUserID,
		"language":         p.Language,
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID:  ev.ID,
		Subject:  subjects.Event("player", "created"),
		Metadata: meta,
		Payload:  ev.Payload,
	}); err != nil {
		return nil, err
	}
	return p, nil
}

// fallbackDisplayName is the name written on a record this handler creates.
//
// The request envelope carries no Telegram first or last name, so there is
// nothing better available here. In practice the gateway resolves identity
// before publishing and creates the player with the real name, which makes
// this the fallback for a command that somehow reached the core first.
//
// It deliberately does NOT return a constant like "player": every such record
// would then be indistinguishable in an admin screen or a support request. The
// Telegram user id is the one identifying fact on hand, so the name is derived
// from it and is at least unique and traceable back to the account. It is for
// records and support, never for the player's own screen, which leaves it out
// (see condition).
func fallbackDisplayName(telegramUserID int64) string {
	return "player-" + strconv.FormatInt(telegramUserID, 10)
}

// renderProfile hands the view to the screen that lays it out.
//
// The layout lives in internal/telegram/screens, not here: a use case decides
// what is true and a screen decides what it looks like. Every word of it
// still comes from the catalogue.
func (h *ProfileHandler) renderProfile(meta envelope.Metadata, p *application.Player, view screens.ProfileView) *presenter.Response {
	c := screens.Context{Msgs: h.msgs, Lang: RenderLanguage(meta, p), MessageID: editableMessageID(meta), Shared: meta.InGroup()}
	if p == nil {
		return presenter.Message(c.T("profile.unavailable", nil), nil)
	}
	return screens.Profile(c, view)
}

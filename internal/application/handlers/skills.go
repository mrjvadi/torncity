package handlers

import (
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation"
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// SkillsHandler serves skills.list.
type SkillsHandler struct {
	uow    application.UnitOfWork
	msgs   Translator
	skills application.SkillRepository
	now    func() time.Time
	// content, cities and home are set by WithPlace: with them a skill the
	// player has not trained is listed only where the settlement they stand
	// in can teach or use it (CLAUDE.md section 2).
	content ContentSource
	cities  application.CityRepository
	home    string
}

// WithPlace makes the list follow the place: a skill is shown when the player
// holds it or the settlement they stand in has what the skill's availability
// tag asks for (research, buildings).
func (h *SkillsHandler) WithPlace(c ContentSource, cities application.CityRepository, homeCityCode string) *SkillsHandler {
	h.content, h.cities, h.home = c, cities, homeCityCode
	return h
}

// NewSkillsHandler wires the handler.
//
// There is no idempotency TTL here and no id generator, because listing
// skills writes nothing. A read needs no replay window: there is no side
// effect for a second delivery to repeat.
func NewSkillsHandler(
	uow application.UnitOfWork,
	msgs Translator,
	skills application.SkillRepository,
	now func() time.Time,
) *SkillsHandler {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &SkillsHandler{uow: uow, msgs: msgs, skills: skills, now: now}
}

// List handles skills.list.
//
// Every skill the DOMAIN knows about is listed, not only the rows storage
// happens to hold. A player who has never written a line of code should see
// that Programming exists and that they are at level zero in it; showing only
// what has been trained would hide the whole game from a new player and make
// the screen's contents depend on which rows a migration created.
func (h *SkillsHandler) List(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}

	var view plife.SkillsView
	lang := meta.Language

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)

		rows, err := h.skills.List(ctx, p.ID)
		if err != nil {
			return err
		}
		stored := make(map[string]application.Skill, len(rows))
		for _, row := range rows {
			stored[row.Code] = row
		}

		codes := player.SkillCodes()
		var teaches func(code string) bool
		if h.content != nil && h.cities != nil {
			if snap := h.content.Current(); snap != nil {
				var city *application.City
				if p.CityID != nil {
					if c, err := h.cities.ByID(ctx, *p.CityID); err == nil {
						city = c
					}
				}
				here, err := judgeSettlementOf(ctx, tx, snap, city, h.home)
				if err != nil {
					return err
				}
				teaches = func(code string) bool { return here.offered(snap, "skill", code) }
			}
		}
		lines := make([]plife.SkillLine, 0, len(codes))
		for _, code := range codes {
			row := stored[string(code)]
			if teaches != nil && row.Level == 0 && row.XP == 0 && !teaches(string(code)) {
				continue // not trained and not taught here: not mentioned
			}
			next, percent := skillProgress(row.Level, row.XP)
			lines = append(lines, plife.SkillLine{
				Code:    string(code),
				Level:   row.Level,
				XP:      row.XP,
				Next:    next,
				Percent: percent,
			})
		}
		view = plife.SkillsView{Lines: lines}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return plife.Skills(presentation.Ctx{Lang: lang}, view), nil
}

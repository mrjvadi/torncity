package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// SkillsHandler serves skills.list.
type SkillsHandler struct {
	uow    application.UnitOfWork
	msgs   Translator
	skills application.SkillRepository
	now    func() time.Time
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

	var view screens.SkillsView

	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}

		rows, err := h.skills.List(ctx, p.ID)
		if err != nil {
			return err
		}
		stored := make(map[string]application.Skill, len(rows))
		for _, row := range rows {
			stored[row.Code] = row
		}

		codes := player.SkillCodes()
		lines := make([]screens.SkillLine, 0, len(codes))
		for _, code := range codes {
			row := stored[string(code)]
			next, percent := skillProgress(row.Level, row.XP)
			lines = append(lines, screens.SkillLine{
				Code:    string(code),
				Level:   row.Level,
				XP:      row.XP,
				Next:    next,
				Percent: percent,
			})
		}
		view = screens.SkillsView{Lines: lines}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return screens.Skills(screens.Context{
		Msgs:      h.msgs,
		Lang:      meta.Language,
		MessageID: editableMessageID(meta),
	}, view), nil
}

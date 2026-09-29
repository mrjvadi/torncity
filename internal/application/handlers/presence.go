package handlers

import (
	"context"
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// PresenceHandler serves the settlement's «who is around» group screen
// (settlement.who, docs/adr/0030-realtime-interest-and-presence.md R2). It is
// a group screen like every village screen: the settlement is the one the
// group founded, resolved by the chat, never by an argument.
type PresenceHandler struct {
	uow      application.UnitOfWork
	msgs     Translator
	presence *application.PresenceService
}

// NewPresenceHandler wires the handler.
func NewPresenceHandler(uow application.UnitOfWork, msgs Translator, svc *application.PresenceService) *PresenceHandler {
	if uow == nil || svc == nil {
		panic("handlers: NewPresenceHandler requires a unit of work and a presence service")
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &PresenceHandler{uow: uow, msgs: msgs, presence: svc}
}

// Who handles settlement.who.
//
// The unit of work only resolves who is asking and which settlement the group
// belongs to; the roster is read after it closes, from the pool and Redis,
// never inside the transaction.
func (h *PresenceHandler) Who(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validPlayerRequest(meta); err != nil {
		return nil, err
	}
	lang := meta.Language
	var s application.FoundedSettlement
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if !meta.InGroup() {
			return refuseVillage(screens.VillageNoSettlement)
		}
		s, err = tx.Settlements().ByFoundingGroup(ctx, meta.TelegramChatID)
		return err
	})
	c := screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
	if err != nil {
		if resp := refusalFor(c, err); resp != nil {
			return resp, nil
		}
		return nil, err
	}

	roster, err := h.presence.Civic(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	view := screens.SettlementWhoView{Name: s.Name}
	for _, p := range roster.Players {
		if p.Online {
			view.Online = append(view.Online, screens.WhoLine{Name: p.DisplayName, Activity: string(p.Activity), Place: p.Place})
		} else {
			view.Offline++
		}
	}
	return screens.SettlementWho(c, view), nil
}

// refusalFor renders the two refusals a village screen can hit before it has
// a settlement: the group has none, or the command was not sent in a group.
func refusalFor(c screens.Context, err error) *presenter.Response {
	var r *villageRefusal
	if stderrors.As(err, &r) {
		return screens.VillageRefusal(c, screens.VillageRefusalView{Kind: r.kind, Back: r.back})
	}
	if isSentinel(err, application.ErrCityNotFound) {
		return screens.VillageRefusal(c, screens.VillageRefusalView{Kind: screens.VillageNoSettlement})
	}
	return nil
}

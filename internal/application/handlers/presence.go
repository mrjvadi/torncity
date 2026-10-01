package handlers

import (
	"context"
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
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
func (h *PresenceHandler) Who(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
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
		if meta.FromClient() {
			// A game client has no group to name its settlement by: it is
			// the player's own (as VillageHandler.settlementOf does).
			ps, err := tx.Settlements().ByPlayer(ctx, p.ID)
			if err != nil {
				return err
			}
			s = ps.FoundedSettlement
			return nil
		}
		if !meta.InGroup() {
			return refuseVillage(village.VillageNoSettlement)
		}
		s, err = tx.Settlements().ByFoundingGroup(ctx, meta.TelegramChatID)
		return err
	})
	c := presentation.Ctx{Lang: lang}
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
	view := village.SettlementWhoView{Name: s.Name}
	for _, p := range roster.Players {
		if p.Online {
			view.Online = append(view.Online, village.WhoLine{Name: p.DisplayName, Activity: string(p.Activity), Place: p.Place})
		} else {
			view.Offline++
		}
	}
	return village.SettlementWho(c, view), nil
}

// refusalFor renders the two refusals a village screen can hit before it has
// a settlement: the group has none, or the command was not sent in a group.
func refusalFor(c presentation.Ctx, err error) *presentation.Response {
	var r *villageRefusal
	if stderrors.As(err, &r) {
		return village.VillageRefusal(c, village.VillageRefusalView{Kind: r.kind, Back: presentation.RefOfAddress(r.back)})
	}
	if isSentinel(err, application.ErrCityNotFound) {
		return village.VillageRefusal(c, village.VillageRefusalView{Kind: village.VillageNoSettlement})
	}
	return nil
}

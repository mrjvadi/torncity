package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	mview "github.com/mrjvadi/torncity/internal/presentation/military"
)

// The armed forces, war, procurement and the defence licences are
// country-level (CLAUDE.md section 2): a village or a town does not have them
// yet. Whether the settlement a player stands in does is the tag of
// availability.yml, never a rule written here; the screen of a service it does
// not offer carries economy.Unavailable instead of its facts.

// tagGate asks the service gate about a tag for the settlement the player of
// the command stands in. It reads in a unit of work of its own and calls the
// gate after it, since the gate reads a repository of its own (no pool call
// runs inside a transaction). It returns the language to answer in as well, so
// the caller can draw the answer without opening another.
func tagGate(ctx context.Context, uow application.UnitOfWork, gates *ServiceGate, meta envelope.Metadata,
	snap *content.Snapshot, kind, code, service string,
) (*economy.Unavailable, string, error) {
	lang := meta.Language
	if gates == nil {
		return nil, lang, nil
	}
	var city string
	err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		facts, err := tx.Presence().Facts(ctx, []string{p.ID})
		if err != nil {
			return err
		}
		city = facts[p.ID].CityID
		return nil
	})
	if err != nil {
		return nil, lang, err
	}
	un, err := gates.CheckTag(ctx, snap, city, kind, code, service)
	return un, lang, err
}

// forcesGate is tagGate for the ministry and the forces: the defence minister's
// office, whose stage is the country's.
func (h *MilitaryHandler) forcesGate(ctx context.Context, meta envelope.Metadata, snap *content.Snapshot) (*economy.Unavailable, string, error) {
	return tagGate(ctx, h.uow, h.gates, meta, snap, mview.TagKindOffice, mview.TagDefenceHead, mview.ServiceArmedForces)
}

// procureGate is tagGate for procurement.
func (h *MilitaryHandler) procureGate(ctx context.Context, meta envelope.Metadata, snap *content.Snapshot) (*economy.Unavailable, string, error) {
	return tagGate(ctx, h.uow, h.gates, meta, snap, mview.TagKindAction, mview.TagProcure, mview.ServiceProcurement)
}

// licenceGate is tagGate for the registry of defence licences.
func (h *MilitaryHandler) licenceGate(ctx context.Context, meta envelope.Metadata, snap *content.Snapshot) (*economy.Unavailable, string, error) {
	return tagGate(ctx, h.uow, h.gates, meta, snap, mview.TagKindAction, mview.TagLicence, mview.ServiceDefenceLicence)
}

// warGate is tagGate for the war board, its flows and the war room.
func (h *WarHandler) warGate(ctx context.Context, meta envelope.Metadata, snap *content.Snapshot) (*economy.Unavailable, string, error) {
	return tagGate(ctx, h.uow, h.gates, meta, snap, mview.TagKindAction, mview.TagWar, mview.ServiceWar)
}

// WithServiceGate has the military screens say so when the settlement the
// player stands in is not one that has them.
func (h *MilitaryHandler) WithServiceGate(g *ServiceGate) *MilitaryHandler {
	h.gates = g
	return h
}

// WithServiceGate has the war screens say so when the settlement the player
// stands in is not one that has them.
func (h *WarHandler) WithServiceGate(g *ServiceGate) *WarHandler {
	h.gates = g
	return h
}

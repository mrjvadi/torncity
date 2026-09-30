package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// SettlementsHandler serves group founding (docs/adr/0028-world-and-
// settlements.md section 3): a Telegram group registers, and the game — not
// the group — places a new village on the generated planet.
//
// Founding is two steps. «ساخت روستا» in a group (Found) checks who may found
// and opens a short-lived draft, answering in the group with a button to the
// game client; the founder completes the village's details there (FoundDraft
// reads the form, Submit checks and submits it), and only the submission
// founds the village, announced in the group by the notifier
// (settlements_form.go). Founding is a GROUP act, never private (ADR 0028
// section 9.5): the command only runs where the whole group already sees it.
type SettlementsHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	worlds  *application.WorldCache
	content ContentSource
	scale   gametimeScale

	spawnParams      wsettle.Params
	protectionWindow time.Duration
	villageGridLots  int
	teachPeriod      time.Duration
	founding         FoundingConfig

	now func() time.Time
}

// NewSettlementsHandler builds the handler. worlds is this replica's
// in-memory copy of the active planet (application.WorldCache); spawnParams,
// protectionWindow, villageGridLots and teachPeriod are config.Settlement's
// own values, copied in rather than imported so this package stays free of
// internal/config, like every other handler. content and scale are what
// Found needs to grant the founding kit's knowledge (ADR 0031 section 4.3)
// and start the settlement's own literacy tick (section 4.4) in the same
// transaction as founding itself.
func NewSettlementsHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, worlds *application.WorldCache,
	source ContentSource, scale gametimeScale,
	spawnParams wsettle.Params, protectionWindow time.Duration, villageGridLots int, teachPeriod time.Duration,
	founding FoundingConfig, now func() time.Time,
) *SettlementsHandler {
	if uow == nil || ids == nil || worlds == nil || source == nil || scale == nil {
		panic("handlers: NewSettlementsHandler requires a unit of work, an id generator, a world cache, content and a game clock")
	}
	if protectionWindow <= 0 || villageGridLots < 2 || teachPeriod <= 0 {
		panic("handlers: NewSettlementsHandler requires a positive protection window, a village grid of at least 2 lots and a positive teach period")
	}
	if founding.DraftTTL <= 0 {
		panic("handlers: NewSettlementsHandler requires a positive founding draft ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &SettlementsHandler{uow: uow, ids: ids, msgs: msgs, worlds: worlds, content: source, scale: scale,
		spawnParams: spawnParams, protectionWindow: protectionWindow, villageGridLots: villageGridLots,
		teachPeriod: teachPeriod, founding: founding, now: now}
}

// maxSpawnAttempts bounds how many candidate spots one Found call tries
// against a concurrent founding racing it to the same cell
// (application.ErrSpawnCellTaken) before giving up — separate from, and
// much smaller than, internal/domain/settlement.Params.SearchMaxAttempts,
// which bounds ONE candidate's own search of the map.
const maxSpawnAttempts = 8

// tierWeight is internal/domain/settlement.FindSpawn's threat score input
// for one existing settlement, by tier: a town or city steers a new spawn
// away more than a village does (ADR 0028 section 3.2 step 3's "no spawn
// next to a strong neighbour"). Tuning internal to the algorithm, like the
// domain package's own scoring weights — not a lever, not content.
func tierWeight(tier string) float64 {
	switch tier {
	case "town":
		return 3
	case "city":
		return 6
	default: // village
		return 1
	}
}

func (h *SettlementsHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// viewer reads the player who sent the command, the same short read-only
// unit of work every other handler's own viewer helper uses.
func (h *SettlementsHandler) viewer(ctx context.Context, meta envelope.Metadata) (*application.Player, string, error) {
	if err := validPlayerRequest(meta); err != nil {
		return nil, meta.Language, err
	}
	var p *application.Player
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		var err error
		p, err = tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		return err
	})
	if err != nil {
		return nil, meta.Language, err
	}
	return p, RenderLanguage(meta, p), nil
}

// settlementCode derives a short, stable cities.code for a founded
// settlement: unique by construction (drawn from a fresh id), never
// authored, unlike a content city's code.
func settlementCode(ids IDGenerator) string {
	id := ids.NewID()
	clean := make([]byte, 0, 12)
	for i := 0; i < len(id) && len(clean) < 12; i++ {
		if id[i] != '-' {
			clean = append(clean, id[i])
		}
	}
	return "v-" + string(clean)
}

// foundingTerrainPriority is the order settlement_knowledge.yml's own
// arable_farming/pastoral_husbandry branches are tried in for ADR 0031
// section 4.3's one terrain-matched founding grant: every arable branch
// before the pastoral one, matching the ADR's own "a village founded in a
// desert starts knowing shaft_irrigation... one on grassland starts
// knowing open_range_herding instead of an arable branch at all" example.
var foundingTerrainPriority = []string{
	"canal_irrigation", "shaft_irrigation", "terrace_irrigation", "paddy_cultivation",
	"open_range_herding",
}

// grantFoundingKit grants the founding kit's knowledge component (ADR 0031
// section 4.3): the four universal baselines plus one terrain-matched item
// chosen deterministically from the founding cell's own biome, and starts
// the settlement's own literacy tick (section 4.4) — all in the same
// transaction as founding itself.
func (h *SettlementsHandler) grantFoundingKit(ctx context.Context, tx application.Tx, settlementID, biomeCode string, at time.Time) error {
	for _, code := range settlementknowledge.FoundingUniversalGrants {
		if _, err := tx.SettlementKnowledge().Grant(ctx, application.SettlementKnowledgeOwned{
			SettlementID: settlementID, Code: code, AcquiredVia: "founding", AcquiredAt: at,
		}); err != nil {
			return fmt.Errorf("handlers: granting founding knowledge %s: %w", code, err)
		}
	}

	snap := h.content.Current()
	tree := snap.SettlementKnowledgeTree()
	if code, ok := settlementknowledge.TerrainGrant(tree, []string{biomeCode}, foundingTerrainPriority); ok {
		if _, err := tx.SettlementKnowledge().Grant(ctx, application.SettlementKnowledgeOwned{
			SettlementID: settlementID, Code: code, AcquiredVia: "founding", AcquiredAt: at,
		}); err != nil {
			return fmt.Errorf("handlers: granting terrain-matched founding knowledge %s: %w", code, err)
		}
	}

	return ensureSettlementTeaching(ctx, tx, h.ids, h.scale, h.teachPeriod, settlementID, at)
}

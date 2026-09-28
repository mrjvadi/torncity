package handlers

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// SettlementsHandler serves group founding (docs/adr/0028-world-and-
// settlements.md section 3): a Telegram group registers, and the game — not
// the group — places a new village on the generated planet.
//
// Founding is a GROUP screen, never private (ADR 0028 section 9.5): the
// announcement is the direct reply to the founding command itself, which
// only runs where the whole group already sees it (meta.InGroup()).
type SettlementsHandler struct {
	uow    application.UnitOfWork
	ids    IDGenerator
	msgs   Translator
	worlds *application.WorldCache

	spawnParams      wsettle.Params
	protectionWindow time.Duration
	villageGridLots  int

	now func() time.Time
}

// NewSettlementsHandler builds the handler. worlds is this replica's
// in-memory copy of the active planet (application.WorldCache); spawnParams,
// protectionWindow and villageGridLots are config.Settlement's own values,
// copied in rather than imported so this package stays free of
// internal/config, like every other handler.
func NewSettlementsHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, worlds *application.WorldCache,
	spawnParams wsettle.Params, protectionWindow time.Duration, villageGridLots int, now func() time.Time,
) *SettlementsHandler {
	if uow == nil || ids == nil || msgs == nil || worlds == nil {
		panic("handlers: NewSettlementsHandler requires a unit of work, an id generator, a translator and a world cache")
	}
	if protectionWindow <= 0 || villageGridLots < 2 {
		panic("handlers: NewSettlementsHandler requires a positive protection window and a village grid of at least 2 lots")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &SettlementsHandler{uow: uow, ids: ids, msgs: msgs, worlds: worlds,
		spawnParams: spawnParams, protectionWindow: protectionWindow, villageGridLots: villageGridLots, now: now}
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

// Found handles the founding command. It is idempotent under at-least-once
// delivery: a second arrival for the same Telegram chat is answered from the
// settlement that already exists (application.ErrGroupAlreadyFounded,
// backed by migration 0042's own unique index), never a second village.
func (h *SettlementsHandler) Found(ctx context.Context, playerID string, meta envelope.Metadata, lang string) (*presenter.Response, error) {
	c := h.screen(meta, lang)
	if !meta.InGroup() {
		return screens.SettlementRefusal(c, screens.SettlementRefusalView{Kind: "group_only"}), nil
	}
	chatID := meta.TelegramChatID

	worldRow, world, err := h.worlds.Active(ctx)
	if stderrors.Is(err, application.ErrNoActiveWorld) {
		return screens.SettlementRefusal(c, screens.SettlementRefusalView{Kind: "no_world"}), nil
	}
	if err != nil {
		return nil, err
	}

	var (
		founded     application.FoundedSettlement
		foundedCell int32
		alreadyName string
		done        bool
	)
	for attempt := 0; attempt < maxSpawnAttempts && !done; attempt++ {
		err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			if existing, err := tx.Settlements().ByFoundingGroup(ctx, chatID); err == nil {
				alreadyName, done = existing.Name, true
				return nil
			} else if !stderrors.Is(err, application.ErrCityNotFound) {
				return err
			}

			cells, err := tx.Settlements().ExistingForWorld(ctx, worldRow.ID)
			if err != nil {
				return err
			}
			existing := make([]wsettle.ExistingSettlement, len(cells))
			for i, e := range cells {
				existing[i] = wsettle.ExistingSettlement{CellID: e.WorldCellID, TierWeight: tierWeight(e.Tier)}
			}

			n, err := tx.Worlds().ReserveSpawnNumber(ctx)
			if err != nil {
				return err
			}
			cand, err := wsettle.FindSpawn(world, existing, n, h.spawnParams)
			if err != nil {
				return fmt.Errorf("handlers: founding settlement: %w", err)
			}

			kit := wsettle.PlaceFoundingKit(world, cand.LatDeg, cand.LonDeg, h.villageGridLots)
			buildings := make([]application.SettlementBuilding, len(kit))
			for i, b := range kit {
				buildings[i] = application.SettlementBuilding{TypeCode: b.TypeCode, LotX: b.LotX, LotY: b.LotY}
			}

			now := h.now()
			name := wsettle.GenerateName(world, cand.CellID, n)
			displayName := name.Latin
			if lang == "fa" && name.Persian != "" {
				displayName = name.Persian
			}
			if displayName == "" {
				displayName = h.ids.NewID()[:8]
			}

			f := application.Founding{
				WorldID: worldRow.ID, WorldCellID: cand.CellID, LatDeg: cand.LatDeg, LonDeg: cand.LonDeg,
				Tier: "village", Code: settlementCode(h.ids), Name: displayName,
				CountryCode: application.DefaultFoundingCountryCode, FounderPlayerID: playerID,
				FoundedByGroupChatID: chatID, FoundedByBotID: meta.BotID, GroupLanguage: lang,
				FoundedAt: now, ProtectedUntil: now.Add(h.protectionWindow), Buildings: buildings,
			}
			out, err := tx.Settlements().Found(ctx, f)
			switch {
			case stderrors.Is(err, application.ErrSpawnCellTaken):
				return err // the outer loop retries with a fresh candidate
			case stderrors.Is(err, application.ErrGroupAlreadyFounded):
				existing2, err2 := tx.Settlements().ByFoundingGroup(ctx, chatID)
				if err2 != nil {
					return err2
				}
				alreadyName, done = existing2.Name, true
				return nil
			case err != nil:
				return err
			}

			if _, _, err := application.FoundOffice(ctx, tx, "village_head", out.JurisdictionID, 1, playerID, now); err != nil {
				return err
			}
			if err := h.appendFoundedEvent(ctx, tx, meta, out, cand.LatDeg, cand.LonDeg); err != nil {
				return err
			}

			founded, foundedCell, done = out, cand.CellID, true
			return nil
		})
		if err != nil {
			if stderrors.Is(err, application.ErrSpawnCellTaken) {
				continue
			}
			return nil, err
		}
	}
	if !done {
		return nil, fmt.Errorf("handlers: founding settlement: no eligible spot after %d attempts", maxSpawnAttempts)
	}
	if alreadyName != "" {
		return screens.SettlementRefusal(c, screens.SettlementRefusalView{Kind: "already", Name: alreadyName}), nil
	}

	feature := wsettle.NearbyFeature(world, foundedCell)
	featureName := feature.Latin
	if lang == "fa" && feature.Persian != "" {
		featureName = feature.Persian
	}
	buildingCodes := make([]string, len(founded.Buildings))
	for i, b := range founded.Buildings {
		buildingCodes[i] = b.TypeCode
	}
	view := screens.SettlementFoundedView{
		Name: founded.Name, BiomeCode: world.BiomeCode(foundedCell), NearbyFeature: featureName,
		Buildings: buildingCodes, ProtectedUntil: founded.ProtectedUntil,
	}
	return screens.SettlementFounded(c, view), nil
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

// appendFoundedEvent writes settlement.founded to the outbox (ADR 0028
// section 9.2), in the same transaction as the founding itself.
func (h *SettlementsHandler) appendFoundedEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata,
	out application.FoundedSettlement, latDeg, lonDeg float64,
) error {
	ev, err := events.New("settlement.founded", "settlement", out.CityID, map[string]any{
		"settlement_id":   out.CityID,
		"code":            out.Code,
		"name":            out.Name,
		"tier":            out.Tier,
		"jurisdiction_id": out.JurisdictionID,
		"world_cell_id":   out.WorldCellID,
		"lat_deg":         latDeg,
		"lon_deg":         lonDeg,
		"founded_at":      out.FoundedAt,
		"protected_until": out.ProtectedUntil,
		"chat_id":         meta.TelegramChatID,
	})
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("settlement", "founded"), Metadata: meta, Payload: ev.Payload,
	})
}

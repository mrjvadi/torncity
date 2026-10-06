package notification

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// A village founded through the founding form is announced in the group that
// asked for it (docs/adr/0028-world-and-settlements.md section 3): the
// founder submitted the form in the game client, so the group cannot be
// answered by the command itself, and the announcement is posted from the
// settlement.founded event alone. The event names the group, its bot and its
// language, so the line is written and sent without reading anything else.

// settlementFounded is the payload appendFoundedEvent writes.
type settlementFounded struct {
	SettlementID string    `json:"settlement_id"`
	Name         string    `json:"name"`
	ChatID       int64     `json:"chat_id"`
	BotID        string    `json:"bot_id"`
	Language     string    `json:"language"`
	FounderName  string    `json:"founder_name"`
	Motto        string    `json:"motto"`
	Protected    time.Time `json:"protected_until"`
	Emblem       struct {
		Shape  string `json:"shape"`
		ColorA string `json:"color_a"`
		ColorB string `json:"color_b"`
		Icon   string `json:"icon"`
	} `json:"emblem"`
	Currency struct {
		Code   string `json:"code"`
		Name   string `json:"name"`
		Symbol string `json:"symbol"`
	} `json:"currency"`
	BiomeCode    string   `json:"biome_code"`
	FeatureLatin string   `json:"feature_latin"`
	FeaturePers  string   `json:"feature_fa"`
	Buildings    []string `json:"building_codes"`
}

// foundedAnnouncement: a village was founded (settlement.founded).
func foundedAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	var ev settlementFounded
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("settlement.founded payload is unreadable").WithCause(err)
	}
	if ev.ChatID == 0 || ev.Name == "" {
		return nil, apperrors.InvalidInput("settlement.founded names no group or no village")
	}
	return &Announcement{
		ChatID: ev.ChatID, BotID: ev.BotID, Language: ev.Language,
		Notice: func(c presentation.Ctx, _ string) *presentation.Response {
			feature := ev.FeatureLatin
			if c.Lang == "fa" && ev.FeaturePers != "" {
				feature = ev.FeaturePers
			}
			return village.SettlementFounded(c, village.SettlementFoundedView{
				Name: ev.Name, SettlementID: ev.SettlementID, BiomeCode: ev.BiomeCode, NearbyFeature: feature,
				Buildings: ev.Buildings, ProtectedUntil: ev.Protected, Founder: ev.FounderName,
				Emblem: village.FoundingEmblemView{Shape: ev.Emblem.Shape, ColorA: ev.Emblem.ColorA,
					ColorB: ev.Emblem.ColorB, Icon: ev.Emblem.Icon},
				Motto:        ev.Motto,
				CurrencyName: ev.Currency.Name, CurrencyCode: ev.Currency.Code, CurrencySign: ev.Currency.Symbol,
			})
		},
	}, nil
}

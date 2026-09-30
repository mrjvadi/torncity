//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Helpers for the tests that found a village: since the founding form,
// «ساخت روستا» only opens a draft, and the village exists once the founder
// submits it.

// testFoundingConfig is the founding form's tuning as configs/config.yml has it.
func testFoundingConfig() handlers.FoundingConfig {
	return handlers.FoundingConfig{
		DraftTTL: 30 * time.Minute,
		Bounds: wsettle.FormRules{NameMin: 3, NameMax: 24, MottoMax: 60,
			CurrencyNameMin: 3, CurrencyNameMax: 24, CurrencyCodeLen: 3, CurrencySymbolMax: 3},
	}
}

// openDraftID is the id of the open founding draft of a chat.
func openDraftID(t *testing.T, pool *postgres.Pool, chat int64) string {
	t.Helper()
	var id string
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT id::text FROM settlement_founding_drafts WHERE chat_id = $1 AND status = 'open'`, chat).Scan(&id); err != nil {
		t.Fatalf("reading the open founding draft of chat %d: %v", chat, err)
	}
	return id
}

// validFoundingRequest is a form that is acceptable: a unique Latin name and
// a unique three-letter currency code.
func validFoundingRequest(t *testing.T, draft string) handlers.FoundSubmitRequest {
	t.Helper()
	return handlers.FoundSubmitRequest{
		Draft: draft, Name: "Vil" + randomToken(t, 9), Motto: "Together we stand",
		CurrencyName: "Mark " + randomToken(t, 8), CurrencyCode: randomTokenUpper(t, 3), CurrencySymbol: "M",
		Shape: "shield", ColorA: "crimson", ColorB: "gold", Icon: "wheat",
	}
}

func randomTokenUpper(t *testing.T, n int) string {
	t.Helper()
	b := []byte(randomToken(t, n))
	for i := range b {
		b[i] -= 'a' - 'A'
	}
	return string(b)
}

// clientMeta is the metadata of a command a game client sends: the player's
// own private channel, whatever chat the request came in on.
func clientMeta(m envelope.Metadata, command, action string) envelope.Metadata {
	m.ChatType = "private"
	m.TelegramChatID = m.TelegramUserID
	m.Command, m.Action = command, action
	m.RequestID = "req_" + m.RequestID
	return m
}

// foundVillage does both steps for the group in meta: it asks in the group,
// then submits the draft with a valid form. It returns the submit's answer.
func foundVillage(t *testing.T, pool *postgres.Pool, h *handlers.SettlementsHandler, meta envelope.Metadata) *presenter.Response {
	t.Helper()
	ctx := testCtx(t)
	if _, err := h.Found(ctx, meta); err != nil {
		t.Fatalf("Found: %v", err)
	}
	req := validFoundingRequest(t, openDraftID(t, pool, meta.TelegramChatID))
	resp, err := h.Submit(ctx, clientMeta(meta, "settlement.found.submit", "found.submit"), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return resp
}

// viewOf decodes a response's structured view.
func viewOf(t *testing.T, resp *presenter.Response) map[string]any {
	t.Helper()
	if resp == nil || len(resp.View) == 0 {
		t.Fatalf("the response carries no view: %+v", resp)
	}
	var v map[string]any
	if err := json.Unmarshal(resp.View, &v); err != nil {
		t.Fatalf("the view is not JSON: %v", err)
	}
	return v
}

// cleanupFounding removes everything the founding of the villages of these
// chats wrote, so a test leaves the database as it found it.
func cleanupFounding(t *testing.T, pool *postgres.Pool, chats func() []int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		for _, chat := range chats() {
			var cityID, jurisdictionID string
			_ = pool.Raw().QueryRow(ctx, `SELECT id::text, jurisdiction_id::text FROM cities WHERE founded_by_group_id = $1`, chat).
				Scan(&cityID, &jurisdictionID)
			if cityID != "" {
				for _, stmt := range []string{
					`UPDATE players SET residence_city_id = NULL, residence_since = NULL WHERE residence_city_id = $1::uuid`,
					`DELETE FROM outbox WHERE payload->>'settlement_id' = $1 OR payload->>'to_city_id' = $1`,
					`DELETE FROM settlement_research WHERE settlement_id = $1::uuid`,
					`DELETE FROM game_actions WHERE reference_type = 'settlement' AND reference_id = $1::uuid`,
					`DELETE FROM settlement_literacy WHERE settlement_id = $1::uuid`,
					`DELETE FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid`,
					`DELETE FROM settlement_property_tax WHERE settlement_id = $1::uuid`,
					`DELETE FROM settlement_private_buildings WHERE settlement_id = $1::uuid`,
					`DELETE FROM settlement_lots WHERE settlement_id = $1::uuid`,
					`DELETE FROM settlement_lot_terms WHERE settlement_id = $1::uuid`,
					`DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid`,
					`DELETE FROM city_group_links WHERE city_id = $1::uuid`,
					`DELETE FROM village_currency_reservations WHERE settlement_id = $1::uuid`,
				} {
					if _, err := pool.Raw().Exec(ctx, stmt, cityID); err != nil {
						t.Errorf("cleanup %q: %v", stmt, err)
					}
				}
			}
			if _, err := pool.Raw().Exec(ctx, `DELETE FROM settlement_founding_drafts WHERE chat_id = $1`, chat); err != nil {
				t.Errorf("cleaning up founding drafts: %v", err)
			}
			if cityID == "" {
				continue
			}
			_, _ = pool.Raw().Exec(ctx, `DELETE FROM offices WHERE jurisdiction_id = $1::uuid`, jurisdictionID)
			if _, err := pool.Raw().Exec(ctx, `DELETE FROM cities WHERE id = $1::uuid`, cityID); err != nil {
				t.Errorf("cleaning up the founded city: %v", err)
			}
			_, _ = pool.Raw().Exec(ctx, `DELETE FROM jurisdictions WHERE id = $1::uuid`, jurisdictionID)
		}
	})
}

var _ = application.DraftOpen

package handlers

import (
	"testing"
	"time"
)

// TestEffectiveMaxCompanies covers the three states of an operator's
// override (migrations/0032_player_limits, `admin player limit`): no
// override, so the config default applies; a numeric cap, which replaces
// it; and unlimited, which lifts it entirely. The full founding path — the
// refusal at the cap and the unblocking once granted — is exercised against
// PostgreSQL in tests/player_limits_integration_test.go; this is the one
// decision the operator's grant changes, isolated from city, content and
// payment plumbing.
func TestEffectiveMaxCompanies(t *testing.T) {
	const configDefault = 2
	h := &CompaniesHandler{rules: CompanyRules{MaxPerPlayer: configDefault}}
	ctx := t.Context()
	const playerID = "player-1"

	t.Run("no override falls back to the config default", func(t *testing.T) {
		tx := newFakeTx()
		max, unlimited, err := h.effectiveMaxCompanies(ctx, tx, playerID)
		if err != nil {
			t.Fatalf("effectiveMaxCompanies: %v", err)
		}
		if unlimited || max != configDefault {
			t.Fatalf("max, unlimited = %d, %v; want %d, false", max, unlimited, configDefault)
		}
	})

	t.Run("a numeric override replaces the config default", func(t *testing.T) {
		tx := newFakeTx()
		granted := 7
		if err := tx.PlayerLimits().Set(ctx, playerID, &granted, false, "admin:ada", "granted for a test", time.Now()); err != nil {
			t.Fatalf("Set: %v", err)
		}
		max, unlimited, err := h.effectiveMaxCompanies(ctx, tx, playerID)
		if err != nil {
			t.Fatalf("effectiveMaxCompanies: %v", err)
		}
		if unlimited || max != granted {
			t.Fatalf("max, unlimited = %d, %v; want %d, false", max, unlimited, granted)
		}
	})

	t.Run("unlimited lifts the cap entirely", func(t *testing.T) {
		tx := newFakeTx()
		if err := tx.PlayerLimits().Set(ctx, playerID, nil, true, "admin:ada", "the owner's grant", time.Now()); err != nil {
			t.Fatalf("Set: %v", err)
		}
		max, unlimited, err := h.effectiveMaxCompanies(ctx, tx, playerID)
		if err != nil {
			t.Fatalf("effectiveMaxCompanies: %v", err)
		}
		if !unlimited {
			t.Fatalf("unlimited = false, want true (max reported as %d)", max)
		}
	})
}

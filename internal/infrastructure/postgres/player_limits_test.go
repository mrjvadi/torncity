package postgres

import (
	"strings"
	"testing"
)

// The override is a single current row per player (migrations/
// 0032_player_limits): a re-grant replaces it rather than appending, and
// DO NOTHING would leave a stale unlimited grant in force after a later
// numeric one.
func TestUpsertPlayerLimitReplacesEveryColumn(t *testing.T) {
	sql := normalize(upsertPlayerLimit)

	if !strings.Contains(sql, "ON CONFLICT (player_id) DO UPDATE SET") {
		t.Fatalf("player limit upsert does not replace the existing row:\n%s", sql)
	}
	if strings.Contains(sql, "DO NOTHING") {
		t.Errorf("player limit upsert uses DO NOTHING, which would keep a stale grant:\n%s", sql)
	}
	for _, want := range []string{
		"max_companies = EXCLUDED.max_companies",
		"unlimited = EXCLUDED.unlimited",
		"granted_by = EXCLUDED.granted_by",
		"reason = EXCLUDED.reason",
		"updated_at = EXCLUDED.updated_at",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("player limit upsert does not refresh %q:\n%s", want, sql)
		}
	}
	// created_at is written once, at INSERT time ($6 twice: created_at and
	// updated_at start equal), and never touched by the ON CONFLICT branch.
	if strings.Contains(sql, "created_at = EXCLUDED") {
		t.Errorf("player limit upsert overwrites created_at on conflict:\n%s", sql)
	}
}

// Clearing an override is a hard delete: no row means the config default,
// and a soft "cleared" flag would leave the table growing forever.
func TestClearPlayerLimitDeletes(t *testing.T) {
	sql := normalize(clearPlayerLimit)
	if !strings.HasPrefix(sql, "DELETE FROM player_limits WHERE player_id = $1::uuid") {
		t.Errorf("clearing a player limit is not a plain delete by player:\n%s", sql)
	}
}

// The read names every column the domain type carries, so a column added to
// one and not the other is caught here rather than by a runtime scan
// mismatch.
func TestGetPlayerLimitSelectsEveryColumn(t *testing.T) {
	sql := normalize(getPlayerLimit)
	for _, want := range []string{
		"player_id::text", "max_companies", "unlimited", "granted_by", "reason", "created_at", "updated_at",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("player limit read does not select %q:\n%s", want, sql)
		}
	}
	if !strings.Contains(sql, "WHERE player_id = $1::uuid") {
		t.Errorf("player limit read is not keyed on player_id:\n%s", sql)
	}
}

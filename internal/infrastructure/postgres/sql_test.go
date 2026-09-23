package postgres

import (
	"strings"
	"testing"
)

// normalize collapses a multi-line statement to single-spaced text so the
// assertions below read the same as the SQL a server would receive.
func normalize(sql string) string { return strings.Join(strings.Fields(sql), " ") }

// The race-safety of first contact is a property of this one statement. Both
// of the alternatives rejected in the comment on insertPlayer would still
// compile; only the SQL text distinguishes them.
func TestInsertPlayerIsRaceSafe(t *testing.T) {
	sql := normalize(insertPlayer)

	if !strings.Contains(sql, "ON CONFLICT (telegram_user_id) DO UPDATE SET") {
		t.Fatalf("player insert does not upsert on the natural key:\n%s", sql)
	}
	// DO NOTHING does not block on the conflicting row, so a loser's
	// read-back can miss a still-uncommitted winner.
	if strings.Contains(sql, "DO NOTHING") {
		t.Errorf("player insert uses DO NOTHING, which cannot return the surviving row:\n%s", sql)
	}
	// Without RETURNING the loser of the race keeps its own generated id and
	// two code paths believe in two different players.
	if !strings.Contains(sql, "RETURNING id, created_at") {
		t.Errorf("player insert does not return the surviving identity:\n%s", sql)
	}
	// The conflicting row's identity and birth date belong to the winner.
	if strings.Contains(sql, "created_at = EXCLUDED") {
		t.Errorf("player upsert overwrites created_at on conflict:\n%s", sql)
	}
	if strings.Contains(sql, "id = EXCLUDED") {
		t.Errorf("player upsert overwrites the surviving id on conflict:\n%s", sql)
	}
}

// A new player is placed in their spawn city by the insert itself, and that
// city is also their residence. A player placed nowhere cannot travel, which
// is the gap this closes.
func TestInsertPlayerPlacesAndHousesNewPlayers(t *testing.T) {
	sql := normalize(insertPlayer)

	// One value, written to both columns: born there, lives there.
	if !strings.Contains(sql, "city_id, residence_city_id, status, created_at, updated_at, public_code) VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $6::uuid, $7, $8, $8, $9)") {
		t.Errorf("player insert does not write the spawn city to both city_id and residence_city_id:\n%s", sql)
	}
	// The pick is made in Go, deterministically. A random choice inside the
	// statement would let a retried first contact disagree with itself.
	if strings.Contains(strings.ToLower(sql), "random(") {
		t.Errorf("player insert picks a city at random:\n%s", sql)
	}
	// On conflict both are only ever filled in, never replaced: first contact
	// must not move or rehome a returning player.
	if !strings.Contains(sql, "city_id = COALESCE(players.city_id, EXCLUDED.city_id)") {
		t.Errorf("player upsert does not preserve an existing city:\n%s", sql)
	}
	if !strings.Contains(sql, "residence_city_id = COALESCE(players.residence_city_id, players.city_id, EXCLUDED.residence_city_id)") {
		t.Errorf("player upsert does not preserve an existing residence:\n%s", sql)
	}
	if strings.Contains(sql, "city_id = EXCLUDED.") {
		t.Errorf("player upsert overwrites an existing city or residence on conflict:\n%s", sql)
	}
	// The caller learns where the player stands.
	if !strings.Contains(sql, "RETURNING id, created_at, city_id::text") {
		t.Errorf("player insert does not return the city:\n%s", sql)
	}
}

// New players are only ever picked from the active version's cities, and only
// from those with a positive weight: a retired city, or one an author set to
// 0, must receive nobody.
func TestSpawnCandidatesComeFromTheActiveVersion(t *testing.T) {
	sql := normalize(selectSpawnCandidates)
	if !strings.Contains(sql, "JOIN content_versions v ON v.id = c.content_version_id AND v.status = 'active'") {
		t.Errorf("spawn candidates are not restricted to the active version:\n%s", sql)
	}
	if !strings.HasSuffix(sql, "WHERE c.spawn_weight > 0") {
		t.Errorf("spawn candidates are not restricted to positive weights:\n%s", sql)
	}
}

// first_seen_at answers "when did this person first start this bot". A later
// /start must refresh the chat id and last_seen_at without rewriting it.
func TestUpsertBotLinkPreservesFirstSeen(t *testing.T) {
	sql := normalize(upsertBotLink)

	if !strings.Contains(sql, "ON CONFLICT (player_id, bot_id) DO UPDATE SET") {
		t.Fatalf("bot link does not upsert on (player_id, bot_id):\n%s", sql)
	}
	for _, want := range []string{
		"telegram_chat_id = EXCLUDED.telegram_chat_id",
		"last_seen_at     = EXCLUDED.last_seen_at",
		"is_reachable     = EXCLUDED.is_reachable",
	} {
		if !strings.Contains(normalize(upsertBotLink), normalize(want)) {
			t.Errorf("bot link upsert does not refresh %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "first_seen_at = EXCLUDED") {
		t.Errorf("bot link upsert rewrites first_seen_at:\n%s", sql)
	}
}

// DO NOTHING plus RowsAffected is the whole mechanism. An upsert here would
// report every replay as fresh and let a command run twice.
func TestReserveKeyUsesDoNothing(t *testing.T) {
	sql := normalize(reserveKey)

	if !strings.Contains(sql, "ON CONFLICT (player_id, idempotency_key) DO NOTHING") {
		t.Errorf("idempotency reserve is not a no-op on conflict:\n%s", sql)
	}
	if strings.Contains(sql, "DO UPDATE") {
		t.Errorf("idempotency reserve updates on conflict, which would mask a replay:\n%s", sql)
	}
}

func TestMarkProcessedUsesTheCompositeKey(t *testing.T) {
	sql := normalize(markProcessed)

	if !strings.Contains(sql, "ON CONFLICT (message_id, consumer) DO NOTHING") {
		t.Errorf("inbox claim is not keyed on (message_id, consumer):\n%s", sql)
	}
	if strings.Contains(sql, "DO UPDATE") {
		t.Errorf("inbox claim updates on conflict, which would mask a redelivery:\n%s", sql)
	}
}

// Both predicates are load-bearing: `enabled` is the operator's switch and
// `status` is the token's condition. Dropping either puts a gateway on a bot
// it must not poll.
func TestSelectEnabledBotsFiltersOnBoth(t *testing.T) {
	sql := normalize(selectEnabledBots)

	if !strings.Contains(sql, "WHERE enabled AND status = 'active'") {
		t.Errorf("bot registry does not filter on both enabled and status:\n%s", sql)
	}
	// Nothing resembling a token may be read from this table.
	if !strings.Contains(sql, "token_secret_ref") {
		t.Errorf("bot registry does not select the secret reference:\n%s", sql)
	}
	for _, forbidden := range []string{"token,", "token ", "bot_token"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("bot registry appears to select a token column (%q):\n%s", forbidden, sql)
		}
	}
}

// The outbox row is written with status pending and no publication timestamp;
// anything else would be an event nobody ever sends.
func TestInsertOutboxIsPendingAndJSONB(t *testing.T) {
	sql := normalize(insertOutbox)

	if !strings.Contains(sql, "$3::jsonb, $4::jsonb") {
		t.Errorf("outbox insert does not cast metadata and payload to jsonb:\n%s", sql)
	}
	if strings.Contains(sql, "published_at") {
		t.Errorf("outbox insert sets published_at at write time:\n%s", sql)
	}
	if !strings.Contains(sql, "INSERT INTO outbox (event_id, subject, metadata, payload, status, attempts, created_at)") {
		t.Errorf("outbox insert column list drifted from the schema:\n%s", sql)
	}
}

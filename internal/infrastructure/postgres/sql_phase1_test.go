package postgres

import (
	"github.com/mrjvadi/torncity/internal/application"
	"os"
	"strings"
	"testing"
)

// SQL-shape assertions for the phase 1 statements, in the spirit of
// sql_test.go: every claim these repositories make about concurrency,
// atomicity and replay safety lives in the text of one statement, and a
// refactor that destroyed the claim would still compile and would still pass
// every test that only checked the returned values.
//
// `normalize` is defined in sql_test.go and collapses a statement to
// single-spaced text.

// ---------------------------------------------------------------------------
// travel
// ---------------------------------------------------------------------------

// Start's whole defence against a double journey is the partial unique index.
// An ON CONFLICT clause of any kind would turn the violation into a silent
// no-op or, worse, an update, and the second tap would be accepted.
func TestInsertTravelLeavesTheConflictToTheIndex(t *testing.T) {
	sql := normalize(insertTravel)

	if !strings.Contains(sql, "INSERT INTO travels (id, player_id, from_city_id, to_city_id, cost, game_action_id, status, departed_at, arrives_at, mode, ledger_transaction_id, content_version, vehicle_id)") {
		t.Fatalf("travel insert column list drifted from the schema:\n%s", sql)
	}
	if strings.Contains(sql, "ON CONFLICT") {
		t.Errorf("travel insert swallows its own conflict; the one-active-per-player index would never surface:\n%s", sql)
	}
	// A pre-flight SELECT folded into the insert would reintroduce exactly the
	// race the index exists to close.
	if strings.Contains(sql, "WHERE NOT EXISTS") || strings.Contains(sql, "SELECT") {
		t.Errorf("travel insert contains a conditional read, which does not serialise two concurrent starts:\n%s", sql)
	}
}

// The name is the conflict target: Start refuses a second journey only for a
// 23505 carrying exactly this index name, so a rename in the migration that
// left this constant alone would turn ErrAlreadyTravelling back into a raw
// driver error in front of a player.
func TestTravelConflictTargetMatchesTheMigration(t *testing.T) {
	migration := readMigration(t)

	for _, name := range []string{
		travelsOneActivePerPlayerIdx,
		friendshipsNoSelfCheck,
	} {
		if !strings.Contains(migration, name) {
			t.Errorf("constraint name %q is not created by migrations/0002_phase1.up.sql", name)
		}
	}

	// The travel index must be UNIQUE and partial on in_transit, or it would
	// not refuse a second journey while permitting a sequence of them.
	if !strings.Contains(normalize(migration),
		"CREATE UNIQUE INDEX "+travelsOneActivePerPlayerIdx+" ON travels (player_id) WHERE status = 'in_transit'") {
		t.Errorf("the one-active-per-player index is no longer a partial unique index on in_transit rows")
	}
}

// Complete has to touch two tables, and the two statements are what say so.
// Either one alone strands the player: an arrived travel with the old city, or
// a moved player whose journey still reads in_transit.
func TestCompleteTravelTouchesTravelAndPlayer(t *testing.T) {
	arrive := normalize(arriveTravel)
	move := normalize(movePlayerToCity)

	if !strings.HasPrefix(arrive, "UPDATE travels SET status = $2") {
		t.Errorf("the arrival statement no longer marks the travel:\n%s", arrive)
	}
	// Without the status predicate a replayed Complete would move a player who
	// has since started another journey.
	if !strings.Contains(arrive, "WHERE id = $1::uuid AND status = 'in_transit'") {
		t.Errorf("the arrival statement is not guarded on the in-transit status:\n%s", arrive)
	}
	// The second statement's parameters come from here; without RETURNING the
	// repository would have to read the row again, outside the update's lock.
	if !strings.Contains(arrive, "RETURNING player_id, to_city_id") {
		t.Errorf("the arrival statement does not return what the move needs:\n%s", arrive)
	}

	if !strings.HasPrefix(move, "UPDATE players SET city_id = $2::uuid") {
		t.Errorf("the second half of arrival no longer moves the player:\n%s", move)
	}
	// An UPDATE players with no WHERE would relocate the entire player base.
	if !strings.Contains(move, "WHERE id = $1::uuid") {
		t.Errorf("the player move is not restricted to one player:\n%s", move)
	}
	if !strings.Contains(move, "updated_at = $3") {
		t.Errorf("the player move does not refresh updated_at:\n%s", move)
	}
}

// Cancel must not move anybody: a cancelled journey never happened, so the
// player is still where players.city_id already says they are.
func TestCancelTravelDoesNotTouchThePlayer(t *testing.T) {
	sql := normalize(cancelTravel)

	if !strings.Contains(sql, "WHERE id = $1::uuid AND status = 'in_transit'") {
		t.Errorf("cancel is not guarded on the in-transit status:\n%s", sql)
	}
	if strings.Contains(sql, "players") || strings.Contains(sql, "city_id") {
		t.Errorf("cancel touches the player's location:\n%s", sql)
	}
}

// Active relies on the partial unique index for there being at most one row.
func TestSelectActiveTravelFiltersInTransit(t *testing.T) {
	sql := normalize(selectActiveTravel)

	if !strings.Contains(sql, "WHERE player_id = $1::uuid AND status = 'in_transit'") {
		t.Errorf("the active-travel lookup is not restricted to journeys in progress:\n%s", sql)
	}
	// A "pick the newest" tie-break would hide a double-start bug rather than
	// let the unique index be the thing that prevents it.
	if strings.Contains(sql, "ORDER BY") || strings.Contains(sql, "LIMIT") {
		t.Errorf("the active-travel lookup tolerates more than one row:\n%s", sql)
	}
}

// Travel status strings are constrained by travels_status_check.
func TestTravelStatusValues(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{TravelInTransit, "in_transit"},
		{TravelArrived, "arrived"},
		{TravelCancelled, "cancelled"},
	} {
		if tc.got != tc.want {
			t.Errorf("travel status constant = %q, want %q", tc.got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// the durable schedule
// ---------------------------------------------------------------------------

// This is the statement the whole scheduler rests on. A standalone SELECT ...
// FOR UPDATE SKIP LOCKED commits on its own, releases every lock it took and
// claims nothing, so two schedulers would each receive the same batch and land
// the same player twice. Only the text distinguishes the two shapes.
func TestClaimDueHoldsItsLocks(t *testing.T) {
	sql := normalize(claimDueSQL)

	for _, want := range []string{
		"FOR UPDATE SKIP LOCKED",
		"WHERE status = 'scheduled'",
		"finish_at <= $1",
		"ORDER BY finish_at",
		"LIMIT $2",
		"UPDATE game_actions a",
		"SET status = 'running'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("the due-claim statement is missing %q:\n%s", want, sql)
		}
	}

	// The locking select must be a CTE of the claiming update. Standalone, its
	// locks are gone the instant it commits.
	if !strings.HasPrefix(sql, "WITH claimed AS ( SELECT id FROM game_actions") {
		t.Errorf("the locking select is not wrapped in the claiming update:\n%s", sql)
	}
	if !strings.Contains(sql, "FROM claimed c WHERE a.id = c.id") {
		t.Errorf("the update is not joined to the claimed set:\n%s", sql)
	}
	// Without RETURNING the caller gets no rows and the claim is lost work.
	if !strings.Contains(sql, "RETURNING a.id") {
		t.Errorf("the due-claim statement returns nothing, so the claimed rows are unreachable:\n%s", sql)
	}
}

// The claim's predicate and order are written to match game_actions_due_idx
// exactly. Drift either way costs the partial index and turns every scheduler
// tick into a sort over the whole table.
func TestClaimDueMatchesTheDueIndex(t *testing.T) {
	sql := normalize(claimDueSQL)
	migration := normalize(readMigration(t))

	if !strings.Contains(migration, "CREATE INDEX game_actions_due_idx ON game_actions (finish_at) WHERE status = 'scheduled'") {
		t.Fatalf("game_actions_due_idx is no longer a partial index on (finish_at) WHERE status = 'scheduled'")
	}
	if !strings.Contains(sql, "WHERE status = 'scheduled' AND finish_at <= $1 ORDER BY finish_at") {
		t.Errorf("the due-claim predicate and order no longer match game_actions_due_idx:\n%s", sql)
	}
	// started_at is the action's own timeline and is what finish_at was
	// computed from; rewriting it at claim time would erase it.
	if strings.Contains(sql, "started_at =") {
		t.Errorf("the claim rewrites started_at:\n%s", sql)
	}
}

// Schedule fixes the status and the retry counter rather than taking them from
// the caller: a row inserted as completed is work nobody will pick up, and one
// that starts with attempts already spent is abandoned early.
func TestInsertGameActionFixesStatusAndRetries(t *testing.T) {
	sql := normalize(insertGameAction)

	if !strings.Contains(sql, "$7::jsonb") {
		t.Errorf("the action insert does not cast the payload to jsonb:\n%s", sql)
	}
	if !strings.Contains(sql, "retry_count, started_at, finish_at)") {
		t.Fatalf("the action insert column list drifted from the schema:\n%s", sql)
	}
	// retry_count is the literal 0, not a parameter.
	if !strings.Contains(sql, "$8, 0, $9, $10)") {
		t.Errorf("the action insert lets the caller choose its retry count:\n%s", sql)
	}
	if strings.Contains(sql, "completed_at") {
		t.Errorf("the action insert sets completed_at at write time:\n%s", sql)
	}
}

// Both closing statements are guarded on the open statuses, which is what
// makes them safe for a late worker to replay: a completed row keeps its first
// completed_at and a cancelled one cannot be resurrected.
func TestActionClosersAreGuardedOnOpenStatuses(t *testing.T) {
	for name, sql := range map[string]string{
		"complete": normalize(completeAction),
		"fail":     normalize(failAction),
	} {
		if !strings.Contains(sql, "WHERE id = $1::uuid AND status IN ('scheduled', 'running')") {
			t.Errorf("the %s statement is not guarded on the open statuses:\n%s", name, sql)
		}
	}
}

// `||` on jsonb concatenates arrays and fails outright on a scalar, so merging
// a failure reason into a payload that is not an object would either append it
// as an element or lose the failure report on top of the original failure.
func TestFailActionMergesOnlyIntoAnObjectPayload(t *testing.T) {
	sql := normalize(failAction)

	if !strings.Contains(sql, "WHEN jsonb_typeof(payload) = 'object'") {
		t.Errorf("the failure reason is merged without checking the payload's type:\n%s", sql)
	}
	if !strings.Contains(sql, "'original_payload', payload") {
		t.Errorf("a non-object payload is discarded rather than preserved:\n%s", sql)
	}
	if !strings.Contains(sql, "retry_count = retry_count + 1") {
		t.Errorf("failing an action does not advance the retry counter:\n%s", sql)
	}
}

// Status strings are constrained by game_actions_status_check.
func TestGameActionStatusValues(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{ActionScheduled, "scheduled"},
		{ActionRunning, "running"},
		{ActionCompleted, "completed"},
		{ActionFailed, "failed"},
		{ActionCancelled, "cancelled"},
	} {
		if tc.got != tc.want {
			t.Errorf("action status constant = %q, want %q", tc.got, tc.want)
		}
	}

	migration := readMigration(t)
	for _, tc := range []string{ActionScheduled, ActionRunning, ActionCompleted, ActionFailed, ActionCancelled} {
		if !strings.Contains(migration, "'"+tc+"'") {
			t.Errorf("status %q is not admitted by the migration's check constraint", tc)
		}
	}
}

// ---------------------------------------------------------------------------
// stats
// ---------------------------------------------------------------------------

// EnsureDefaults is handed DEFAULTS. Writing any EXCLUDED value would reset a
// live player's health, energy and experience to starting values every time
// anything called it again.
func TestInsertStatsDefaultsIsRaceSafeAndNonDestructive(t *testing.T) {
	sql := normalize(insertStatsDefaults)

	if !strings.Contains(sql, "ON CONFLICT (player_id) DO UPDATE SET updated_at = player_stats.updated_at") {
		t.Fatalf("the stats insert does not take a row lock on conflict:\n%s", sql)
	}
	// DO NOTHING does not block on the conflicting row, so the loser of the
	// race reads back nothing while the winner is still uncommitted.
	if strings.Contains(sql, "DO NOTHING") {
		t.Errorf("the stats insert uses DO NOTHING, which cannot return the surviving row:\n%s", sql)
	}
	if strings.Contains(sql, "EXCLUDED") {
		t.Errorf("the stats insert writes proposed defaults over a live row:\n%s", sql)
	}
	// The loser must carry the winning row onward, not the defaults it
	// proposed, so every column the caller gets back is returned.
	if !strings.Contains(sql, "RETURNING player_id, level, xp, health, max_health, energy, max_energy, happiness, stamina, reputation, updated_at") {
		t.Errorf("the stats insert does not return the surviving row in full:\n%s", sql)
	}
}

// Save is an UPDATE, never an upsert: EnsureDefaults is the only thing allowed
// to create the row, so a Save for a player who never had stats is a bug in
// the caller's sequence rather than a row to invent.
func TestSaveStatsDoesNotCreateRows(t *testing.T) {
	sql := normalize(updateStats)

	if !strings.HasPrefix(sql, "UPDATE player_stats SET") {
		t.Fatalf("the stats save is no longer an update:\n%s", sql)
	}
	if strings.Contains(sql, "INSERT") || strings.Contains(sql, "ON CONFLICT") {
		t.Errorf("the stats save can create a row:\n%s", sql)
	}
	if !strings.Contains(sql, "WHERE player_id = $1::uuid") {
		t.Errorf("the stats save is not restricted to one player:\n%s", sql)
	}
}

// ---------------------------------------------------------------------------
// skills
// ---------------------------------------------------------------------------

// The unique key is what turns two concurrent training commands into one row
// instead of two half-progressed ones.
func TestUpsertSkillIsKeyedOnPlayerAndCode(t *testing.T) {
	sql := normalize(upsertSkill)

	if !strings.Contains(sql, "ON CONFLICT (player_id, skill_code) DO UPDATE SET") {
		t.Fatalf("the skill upsert is not keyed on (player_id, skill_code):\n%s", sql)
	}
	for _, want := range []string{
		"level = EXCLUDED.level",
		"xp = EXCLUDED.xp",
		"updated_at = EXCLUDED.updated_at",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("the skill upsert does not apply %q:\n%s", want, sql)
		}
	}
	// A repeated upsert must not hand the same skill a new identity.
	if strings.Contains(sql, "id = EXCLUDED") {
		t.Errorf("the skill upsert rewrites the surrogate id:\n%s", sql)
	}
	if strings.Contains(sql, "DO NOTHING") {
		t.Errorf("the skill upsert drops the caller's progress on conflict:\n%s", sql)
	}
}

// The unique constraint the upsert names must be the one the migration creates.
func TestSkillUniqueKeyMatchesTheMigration(t *testing.T) {
	if !strings.Contains(normalize(readMigration(t)),
		"CONSTRAINT player_skills_player_skill_key UNIQUE (player_id, skill_code)") {
		t.Error("player_skills no longer carries UNIQUE (player_id, skill_code)")
	}
}

// ---------------------------------------------------------------------------
// friendships
// ---------------------------------------------------------------------------

// A repeated request must never downgrade an accepted friendship back to
// pending, which is a real way to lose a friendship by tapping a stale button.
func TestFriendRequestPreservesTheExistingStatus(t *testing.T) {
	sql := normalize(insertFriendRequest)

	if !strings.Contains(sql, "ON CONFLICT (player_id, friend_player_id) DO UPDATE SET status = friendships.status") {
		t.Fatalf("the friend request does not preserve the existing status:\n%s", sql)
	}
	if strings.Contains(sql, "status = EXCLUDED.status") {
		t.Errorf("the friend request overwrites the existing status:\n%s", sql)
	}
	// Whether the row is new is decided by comparing the returned id with the
	// proposed one, so both columns must come back.
	if !strings.Contains(sql, "RETURNING id, status") {
		t.Errorf("the friend request does not return the surviving row:\n%s", sql)
	}
}

// Accepting means turning the other player's pending request into an accepted
// edge. Without the status predicate it would also reanimate an edge the
// requester has since blocked.
func TestAcceptRequiresAPendingRequest(t *testing.T) {
	sql := normalize(acceptIncoming)

	if !strings.Contains(sql, "WHERE player_id = $1::uuid AND friend_player_id = $2::uuid AND status = 'pending'") {
		t.Errorf("the accept statement is not restricted to a pending request:\n%s", sql)
	}
	if !strings.Contains(sql, "RETURNING id") {
		t.Errorf("the accept statement cannot tell a missing request from a changed one:\n%s", sql)
	}
}

// The accepter's own edge is the one place EXCLUDED is right: two people who
// both sent requests must not be left pending forever.
func TestReverseEdgeAppliesTheAcceptedStatus(t *testing.T) {
	sql := normalize(insertReverseEdge)

	if !strings.Contains(sql, "ON CONFLICT (player_id, friend_player_id) DO UPDATE SET status = EXCLUDED.status") {
		t.Errorf("the reverse edge does not apply the accepted status to an existing row:\n%s", sql)
	}
}

// Block and Remove are one-directional by design: the reverse row belongs to
// the other player, and writing it would let one account rewrite another's
// social graph through an ordinary command.
func TestBlockAndRemoveTouchOneEdgeOnly(t *testing.T) {
	block := normalize(blockEdge)
	remove := normalize(deleteFriendship)

	if !strings.Contains(block, "ON CONFLICT (player_id, friend_player_id) DO UPDATE SET status = EXCLUDED.status") {
		t.Errorf("blocking does not override whatever the edge was:\n%s", block)
	}
	if !strings.Contains(remove, "WHERE player_id = $1::uuid AND friend_player_id = $2::uuid") {
		t.Fatalf("the removal is not restricted to the caller's own edge:\n%s", remove)
	}
	// An OR, or a swapped-pair predicate, would delete the other player's row.
	if strings.Contains(remove, " OR ") {
		t.Errorf("the removal reaches the other player's edge:\n%s", remove)
	}
}

// The listing is the caller's own edges in every status, plus the pending
// requests others sent them, and it is totally ordered for paging.
func TestSelectFriendshipsIsOutgoingAndTotallyOrdered(t *testing.T) {
	sql := normalize(selectFriendships)

	if !strings.Contains(sql, "WHERE player_id = $1::uuid") {
		t.Errorf("the friendship listing is not the caller's own edges:\n%s", sql)
	}
	// The other half is only requests sent TO the caller, still pending:
	// somebody else's friendships and blocks are none of their business.
	if !strings.Contains(sql, "WHERE friend_player_id = $1::uuid AND status = 'pending'") {
		t.Errorf("the listing does not add the requests the caller received, and only those:\n%s", sql)
	}
	// created_at is supplied by the application and two rows written in one
	// transaction share it exactly, so the id tie-break is what makes paging
	// neither repeat nor skip a row.
	if !strings.Contains(sql, "ORDER BY created_at, id") {
		t.Errorf("the friendship listing has no total order:\n%s", sql)
	}
	// The caller's own edges are not filtered by status: that would need a
	// separate method per screen.
	own := sql[:strings.Index(sql, "UNION ALL")]
	if strings.Contains(own, "status =") {
		t.Errorf("the listing decides which of the caller's own statuses they may see:\n%s", sql)
	}
}

// mergeFriendships keeps one line per other player.
func TestMergeFriendships(t *testing.T) {
	got := mergeFriendships([]application.Friendship{
		{FriendPlayerID: "a", Status: FriendshipPending},                 // sent to a
		{FriendPlayerID: "a", Status: FriendshipPending, Incoming: true}, // a asked too
		{FriendPlayerID: "b", Status: FriendshipAccepted},                // friends
		{FriendPlayerID: "b", Status: FriendshipPending, Incoming: true}, // stale request from b
		{FriendPlayerID: "c", Status: FriendshipBlocked},                 // blocked
		{FriendPlayerID: "c", Status: FriendshipPending, Incoming: true}, // c's request
		{FriendPlayerID: "d", Status: FriendshipPending, Incoming: true}, // received only
	})
	want := map[string]bool{"a": true, "b": false, "c": false, "d": true} // other -> incoming
	if len(got) != len(want) {
		t.Fatalf("merged %d lines, want %d: %+v", len(got), len(want), got)
	}
	for _, e := range got {
		if w, ok := want[e.FriendPlayerID]; !ok || w != e.Incoming {
			t.Errorf("line %+v", e)
		}
	}
}

// Friendship status strings are constrained by friendships_status_check.
func TestFriendshipStatusValues(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{FriendshipPending, "pending"},
		{FriendshipAccepted, "accepted"},
		{FriendshipBlocked, "blocked"},
	} {
		if tc.got != tc.want {
			t.Errorf("friendship status constant = %q, want %q", tc.got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// search and cities
// ---------------------------------------------------------------------------

// Cities are content. Nothing in this package may create one, and the column
// application.City cannot carry must not be selected into a scan that fails.
func TestCityStatementsAreReadOnlyAndOrderedByCode(t *testing.T) {
	for name, sql := range map[string]string{
		"list":    normalize(selectCities),
		"by id":   normalize(selectCityByID),
		"by code": normalize(selectCityByCode),
	} {
		if !strings.HasPrefix(sql, "SELECT id, code, name, COALESCE(jurisdiction_id::text, ''), cost_of_living, population FROM cities") {
			t.Errorf("the city %s statement drifted from the schema:\n%s", name, sql)
		}
		if strings.Contains(sql, "treasury_account_id") {
			t.Errorf("the city %s statement selects a column nothing can carry:\n%s", name, sql)
		}
		// The tax rate is a policy (ADR 0015): read with PolicyReader, never
		// off the city row.
		if strings.Contains(sql, "tax_rate_bps") {
			t.Errorf("the city %s statement reads the tax rate directly:\n%s", name, sql)
		}
	}

	// Ordering on name would reshuffle every menu the moment a translation
	// lands; code is the stable machine identifier.
	if !strings.HasSuffix(normalize(selectCities), "ORDER BY code") {
		t.Errorf("the city listing is not ordered by code:\n%s", normalize(selectCities))
	}
	// The lookup by code is exact: folding case would let two spellings
	// resolve to one row and collide the day two codes differ only in case.
	if strings.Contains(normalize(selectCityByCode), "ILIKE") || strings.Contains(normalize(selectCityByCode), "lower(") {
		t.Errorf("the city lookup by code folds case:\n%s", normalize(selectCityByCode))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// readMigration returns the phase 1 migration's text.
//
// The constraint names this package matches on are only meaningful if the
// migration still creates them, and nothing else in the build connects the
// two. Reading the file is what turns a rename into a failing test here
// instead of a raw driver error reaching a player.
func readMigration(t *testing.T) string {
	t.Helper()

	const path = "../../../migrations/0002_phase1.up.sql"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// Demand pricing counts every departure on the route by the mode, whatever
// became of it, from the demand index; a status filter would let arrived
// journeys stop counting as demand the moment they land.
func TestCountRecentDeparturesCountsEveryStatus(t *testing.T) {
	sql := normalize(countRecentDepartures)
	for _, want := range []string{"from_city_id = $1::uuid", "to_city_id = $2::uuid", "mode = $3", "departed_at >= $4"} {
		if !strings.Contains(sql, want) {
			t.Errorf("the departure count lacks %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "status") {
		t.Errorf("the departure count filters on status:\n%s", sql)
	}
}

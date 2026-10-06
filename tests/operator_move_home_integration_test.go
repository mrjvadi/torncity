//go:build integration

package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// `admin player move-home`: a resident inside the 72 h cool-down is moved to another settlement
// exactly as a join moves them, skipping only the cool-down; the audit row has the operator, the
// reason, before and after; what a join leaves alone (their office in the old city) stays; a
// second run changes nothing; and the other gates (here: travelling) still refuse.
func TestOperatorMovesAPlayersHomeInsideTheCooldown(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	metaA, founderA := e.group(t)
	foundVillage(t, pool, e.h, metaA)
	metaB, _ := e.group(t)
	foundVillage(t, pool, e.h, metaB)
	var cityA, cityB string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaA.TelegramChatID).Scan(&cityA); err != nil {
		t.Fatal(err)
	}
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaB.TelegramChatID).Scan(&cityB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM audit_logs WHERE action = 'player.move_home' AND new_value->>'player' = $1`, founderA.ID)
	})
	residence := func() (string, *time.Time) {
		t.Helper()
		var city string
		var since *time.Time
		if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(residence_city_id::text, ''), residence_since FROM players WHERE id = $1::uuid`, founderA.ID).Scan(&city, &since); err != nil {
			t.Fatal(err)
		}
		return city, since
	}
	home, sinceBefore := residence()
	if home != cityA {
		t.Fatalf("the founder lives in the village they founded: %s", home)
	}
	officesOf := func() int {
		t.Helper()
		var n int
		if err := postgres.NewUnitOfWork(pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
			s, err := tx.Settlements().ByPlayer(ctx, founderA.ID)
			n = len(s.Offices)
			if err != nil && !strings.Contains(err.Error(), "not found") {
				return err
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	officesBefore := officesOf()
	ops := operator.Ops{Pool: pool}
	actor := operator.Actor{Name: "tester", Reason: "owner asked for a home in the new city", At: time.Now().UTC()}

	res, err := ops.MoveHome(ctx, founderA.PublicCode, cityB, actor)
	if err != nil {
		t.Fatalf("MoveHome: %v", err)
	}
	if res.NoOp || res.To != cityB || res.From != cityA || !res.Placed {
		t.Errorf("the move should go from A to B and place them there: %+v", res)
	}
	home, sinceAfter := residence()
	if home != cityB {
		t.Errorf("residence is %s, want the new settlement", home)
	}
	if sinceAfter == nil || sinceBefore == nil || !sinceAfter.After(*sinceBefore) {
		t.Errorf("residence_since should be reset to now: %v (was %v)", sinceAfter, sinceBefore)
	}
	var city string
	if err := pool.Raw().QueryRow(ctx, `SELECT COALESCE(city_id::text, '') FROM players WHERE id = $1::uuid`, founderA.ID).Scan(&city); err != nil || city != cityB {
		t.Errorf("the player stands in the new settlement: %q %v", city, err)
	}
	if got := officesOf(); got != officesBefore {
		t.Errorf("a join leaves the old city's offices alone, and so does this: %d before, %d after", officesBefore, got)
	}
	var reason, action string
	var newValue string
	if err := pool.Raw().QueryRow(ctx, `SELECT actor || '|' || action, reason, new_value::text FROM audit_logs
		WHERE action = 'player.move_home' AND new_value->>'player' = $1 ORDER BY id DESC LIMIT 1`, founderA.ID).Scan(&action, &reason, &newValue); err != nil {
		t.Fatalf("no audit row: %v", err)
	}
	if action != "tester|player.move_home" || !strings.Contains(reason, "owner asked") || !strings.Contains(newValue, `"before"`) || !strings.Contains(newValue, `"after"`) ||
		!strings.Contains(newValue, cityA) || !strings.Contains(newValue, cityB) {
		t.Errorf("the audit row lacks the operator, the reason or before and after: %s | %s | %s", action, reason, newValue)
	}
	// a second run is a no-op and writes no second audit row
	again, err := ops.MoveHome(ctx, founderA.PublicCode, cityB, actor)
	if err != nil || !again.NoOp {
		t.Errorf("a second run changes nothing: %+v %v", again, err)
	}
	var rows int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'player.move_home' AND new_value->>'player' = $1`, founderA.ID).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("one audit row for one move: %d %v", rows, err)
	}
	// the other gates stay: a player who is travelling is refused, and it says so
	actionID, travelID := newUUID(t), newUUID(t)
	if err := postgres.NewUnitOfWork(pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.GameActions().Schedule(ctx, application.GameAction{ID: actionID, ActionType: "travel", ActorType: "player", ActorID: founderA.ID,
			ReferenceType: "travels", ReferenceID: travelID, StartedAt: time.Now(), FinishAt: time.Now().Add(6 * time.Hour)})
	}); err != nil {
		t.Fatal(err)
	}
	var supportID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE code = 'support'`).Scan(&supportID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO travels (id, player_id, from_city_id, to_city_id, cost, game_action_id, status, departed_at, arrives_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 0, $5::uuid, 'in_transit', now(), now() + interval '6 hours')`, travelID, founderA.ID, cityB, supportID, actionID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM travels WHERE id = $1::uuid`, travelID)
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM game_actions WHERE id = $1::uuid`, actionID)
	})
	if _, err := ops.MoveHome(ctx, founderA.PublicCode, cityA, actor); err == nil || !strings.Contains(err.Error(), "travelling") {
		t.Errorf("a travelling player is refused with the reason: %v", err)
	}
}

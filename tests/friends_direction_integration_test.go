//go:build integration

package tests

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// TestFriendListTellsSentFromReceived: a request shows on both lists, as
// sent on the requester's and as received — the one that can be accepted —
// on the other's; when both asked, the one to answer wins; once friends, one
// line each.
func TestFriendListTellsSentFromReceived(t *testing.T) {
	pool := requirePostgres(t)
	ctx := testCtx(t)
	a := insertPlayer(t, pool)
	b := insertPlayer(t, pool)
	cleanupPlayerRows(t, pool, a.ID, b.ID)
	repo := postgres.NewFriendshipRepository(pool)

	lineFor := func(owner, other string) (application.Friendship, int) {
		t.Helper()
		edges, err := repo.List(testCtx(t), owner)
		if err != nil {
			t.Fatal(err)
		}
		var found application.Friendship
		n := 0
		for _, e := range edges {
			if e.FriendPlayerID == other {
				found = e
				n++
			}
			if e.PlayerID != owner {
				t.Errorf("an edge on %s's list is %s's", owner, e.PlayerID)
			}
		}
		return found, n
	}

	if err := repo.Request(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if e, n := lineFor(a.ID, b.ID); n != 1 || e.Incoming || e.Status != postgres.FriendshipPending {
		t.Errorf("the requester sees %+v (%d lines), want one sent request", e, n)
	}
	if e, n := lineFor(b.ID, a.ID); n != 1 || !e.Incoming || e.Status != postgres.FriendshipPending {
		t.Errorf("the other sees %+v (%d lines), want one received request", e, n)
	}

	// B asks A too: A now has a request to answer, and one line for B.
	if err := repo.Request(ctx, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if e, n := lineFor(a.ID, b.ID); n != 1 || !e.Incoming {
		t.Errorf("after both asked, A sees %+v (%d lines), want B's request to answer", e, n)
	}

	if err := repo.Accept(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{a.ID, b.ID}, {b.ID, a.ID}} {
		if e, n := lineFor(pair[0], pair[1]); n != 1 || e.Incoming || e.Status != postgres.FriendshipAccepted {
			t.Errorf("after accepting, %s sees %+v (%d lines), want one friend", pair[0], e, n)
		}
	}
}

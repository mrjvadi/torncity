package notification

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A player making the village their home is village news, batched like the
// rest; the founder's own move at founding, a leave, and a move into a city
// nobody founded are not.
func TestResidentJoinedIsBatchedVillageNews(t *testing.T) {
	r, pub, _, _ := villageRig(t)
	r.w.cfg.Deps.Founded = onlyFounded{villageID: true}
	route := villageRoute(t, "residence", "changed", "news")
	const support = "99999999-0000-4000-8000-000000000099"
	move := func(id, from, to, via string) {
		t.Helper()
		if err := r.w.Handle(context.Background(), route, villageEvent2(t, id, r.now, map[string]any{
			"player_id": playerID, "from_city_id": from, "to_city_id": to, "via": via})); err != nil {
			t.Fatal(err)
		}
	}
	move("r0", support, villageID, "founding")                          // no news: the founding says it
	move("r1", villageID, support, "leave")                             // no news
	move("r2", support, "88888888-0000-4000-8000-000000000088", "join") // not a founded village
	move("r3", support, villageID, "join")                              // news
	move("r3", support, villageID, "join")                              // redelivery
	r.now = r.now.Add(3 * time.Second)
	move("r4", support, villageID, "join") // a second join in the same burst

	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 0 {
		t.Fatalf("posted before the merge window: %d", len(r.sender.sent))
	}
	r.now = r.now.Add(30 * time.Second)
	r.w.FlushVillageNews(context.Background())
	if len(r.sender.sent) != 1 {
		t.Fatalf("posted %d times, want one batched post", len(r.sender.sent))
	}
	text := r.sender.sent[0].notice.Response.Text
	if !strings.Contains(text, "کورندال") || strings.Count(text, "Ada") != 2 || strings.Count(text, "ساکن") != 2 {
		t.Errorf("the batched join post is %q", text)
	}
	if kb := r.sender.sent[0].notice.Response.Keyboard; kb == nil || kb.Rows[0][0].CallbackData != "settlement:overview" {
		t.Errorf("a join opens the village overview: %+v", kb)
	}

	// The same events, on the realtime route, are the settlement channel's
	// member_joined / member_left (the roster changes for the client).
	rt := villageRoute(t, "residence", "changed", "realtime")
	if err := r.w.Handle(context.Background(), rt, villageEvent2(t, "rt", r.now, map[string]any{
		"player_id": playerID, "from_city_id": support, "to_city_id": villageID, "via": "join"})); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != 1 || pub.pubs[0].data["type"] != "member_joined" || pub.pubs[0].channel != "settlement:"+villageID {
		t.Errorf("publications = %+v", pub.pubs)
	}
}

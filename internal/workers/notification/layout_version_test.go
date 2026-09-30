package notification

import (
	"context"
	"testing"
)

// A publication that changes the picture carries the layout's version after
// the change, for each kind of viewer, exactly as the event's writer put it;
// one that does not, carries none.
func TestBuildingPublicationsCarryTheLayoutVersion(t *testing.T) {
	r, pub, _, _ := villageRig(t)
	lv := map[string]any{"head": "aa11", "member": "bb22", "public": "cc33"}
	for i, c := range []struct{ event, want string }{
		{"build_started", "build_started"}, {"built", "build_finished"},
		{"building_demolished", "build_salvaged"}, {"build_cancelled", "build_cancelled"},
	} {
		route := villageRoute(t, "settlement", c.event, "realtime")
		payload := map[string]any{"settlement_id": villageID, "building_id": "b", "type_code": "watch_hut", "layout_version": lv}
		if err := r.w.Handle(context.Background(), route, villageEvent2(t, "lv"+string(rune('a'+i)), r.now, payload)); err != nil {
			t.Fatal(err)
		}
		got := pub.pubs[len(pub.pubs)-1].data
		versions, _ := got["layout_version"].(map[string]any)
		if got["type"] != c.want || versions["head"] != "aa11" || versions["member"] != "bb22" || versions["public"] != "cc33" {
			t.Errorf("%s: %+v", c.event, got)
		}
		if _, has := got["version"]; has {
			t.Errorf("%s: the layout's word for its version must not be reused for the channel's sequence: %+v", c.event, got)
		}
	}

	// An event that predates the field, and one that is not about buildings,
	// carry no layout version.
	old := villageRoute(t, "settlement", "built", "realtime")
	if err := r.w.Handle(context.Background(), old, villageEvent2(t, "old", r.now, map[string]any{"settlement_id": villageID, "building_id": "b"})); err != nil {
		t.Fatal(err)
	}
	if _, has := pub.pubs[len(pub.pubs)-1].data["layout_version"]; has {
		t.Error("an invented layout version")
	}
	know := villageRoute(t, "settlement", "knowledge_bought", "realtime")
	if err := r.w.Handle(context.Background(), know, villageEvent2(t, "kb", r.now, map[string]any{"settlement_id": villageID, "code": "x"})); err != nil {
		t.Fatal(err)
	}
	if _, has := pub.pubs[len(pub.pubs)-1].data["layout_version"]; has {
		t.Error("knowledge does not change the picture")
	}

	// A new head is the one whose layout is stale (who may place changed).
	head := villageRoute(t, "governance", "appointed", "realtime")
	if err := r.w.Handle(context.Background(), head, villageEvent2(t, "hd", r.now, map[string]any{
		"player_id": playerID, "office": "village_head", "city_ids": []string{villageID}})); err != nil {
		t.Fatal(err)
	}
	if got := pub.pubs[len(pub.pubs)-1].data; got["type"] != "head_changed" || got["layout_stale"] != true {
		t.Errorf("head_changed = %+v", got)
	}
}

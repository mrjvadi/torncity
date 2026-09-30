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

type onlyFounded map[string]bool

func (o onlyFounded) FoundedAmong(_ context.Context, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		if o[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// A neutral city such as Support gets no settlement publication of any kind;
// a journey between it and a founded village publishes only on the village.
func TestContentCitiesPublishNothingOnSettlementChannels(t *testing.T) {
	r, pub, _, _ := villageRig(t)
	const support = "99999999-0000-4000-8000-000000000099"
	r.w.cfg.Deps.Founded = onlyFounded{villageID: true}

	trip := villageRoute(t, "travel", "completed", "realtime")
	if err := r.w.Handle(context.Background(), trip, villageEvent2(t, "s1", r.now, map[string]any{
		"player_id": playerID, "from_city_id": villageID, "to_city_id": support})); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != 1 || pub.pubs[0].channel != "settlement:"+villageID || pub.pubs[0].data["type"] != "member_left" {
		t.Fatalf("leaving a village for Support: %+v", pub.pubs)
	}
	before := len(pub.pubs)
	if err := r.w.Handle(context.Background(), trip, villageEvent2(t, "s2", r.now, map[string]any{
		"player_id": playerID, "from_city_id": support, "to_city_id": "88888888-0000-4000-8000-000000000088"})); err != nil {
		t.Fatal(err)
	}
	move := villageRoute(t, "residence", "changed", "realtime")
	if err := r.w.Handle(context.Background(), move, villageEvent2(t, "s3", r.now, map[string]any{
		"player_id": playerID, "from_city_id": support, "to_city_id": villageID})); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != before+1 || pub.pubs[before].data["type"] != "member_joined" {
		t.Fatalf("only the founded village hears of it: %+v", pub.pubs[before:])
	}
	// Every other kind of village event for a content city is dropped too.
	built := villageRoute(t, "settlement", "built", "realtime")
	if err := r.w.Handle(context.Background(), built, villageEvent2(t, "s4", r.now, map[string]any{"settlement_id": support, "building_id": "b"})); err != nil {
		t.Fatal(err)
	}
	if len(pub.pubs) != before+1 {
		t.Errorf("a content city got a publication: %+v", pub.pubs[before+1:])
	}
}

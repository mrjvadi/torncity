package statesync

import (
	"encoding/json"
	"testing"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

func ent(kind, id, data string) Entity { return Entity{Kind: kind, ID: id, Data: json.RawMessage(data)} }

func TestPlanNewChangedGoneAndBack(t *testing.T) {
	held := map[Key]Held{
		{KindWallet, "SUP"}:      {V: 3, Hash: Hash(json.RawMessage(`{"cash":1}`))},
		{KindWallet, "NIL"}:      {V: 2, Hash: Hash(json.RawMessage(`{"cash":5}`))},
		{KindInventory, "knife"}: {V: 4, Hash: Hash(json.RawMessage(`{"qty":1}`))},
		{KindSkill, "driving"}:   {V: 7, Deleted: true},
	}
	cur := []Entity{
		ent(KindWallet, "SUP", `{"cash":1}`), // unchanged
		ent(KindWallet, "NIL", `{"cash":6}`), // changed
		ent(KindSkill, "driving", `{"level":1}`),
		ent(KindVitals, "p", `{"energy":1}`), // new
	}
	got := Plan(cur, held, nil)
	want := []Change{
		{Kind: KindVitals, ID: "p", Op: OpSet, V: 1},
		{Kind: KindWallet, ID: "NIL", Op: OpSet, V: 3},
		{Kind: KindInventory, ID: "knife", Op: OpDel, V: 5},
		{Kind: KindSkill, ID: "driving", Op: OpSet, V: 8},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d changes, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.Kind || g.ID != w.ID || g.Op != w.Op || g.V != w.V {
			t.Errorf("change %d = %s/%s %s v%d, want %s/%s %s v%d", i, g.Kind, g.ID, g.Op, g.V, w.Kind, w.ID, w.Op, w.V)
		}
	}
}

func TestPlanScopeLeavesOtherKindsAlone(t *testing.T) {
	held := map[Key]Held{{KindWallet, "SUP"}: {V: 1, Hash: "x"}, {KindNotice, "n1"}: {V: 1, Hash: "y"}}
	got := Plan(nil, held, Kinds(KindNotice))
	if len(got) != 1 || got[0].Kind != KindNotice || got[0].Op != OpDel {
		t.Fatalf("a scoped projection deleted outside its scope: %+v", got)
	}
}

func TestPlanIsIdempotent(t *testing.T) {
	cur := []Entity{ent(KindWallet, "SUP", `{"cash":1}`)}
	first := Plan(cur, map[Key]Held{}, nil)
	held := map[Key]Held{}
	for _, c := range first {
		held[Key{c.Kind, c.ID}] = Held{V: c.V, Hash: c.Hash}
	}
	if again := Plan(cur, held, nil); len(again) != 0 {
		t.Fatalf("the same state planned changes twice: %+v", again)
	}
}

func rec(pts int64, kind, id string, v int64, op, data string) Record {
	r := Record{PTS: pts, Entity: kind, ID: id, V: v, Op: op}
	if data != "" {
		r.Data = json.RawMessage(data)
	}
	return r
}

func TestApplyDuplicatesGapsAndVersions(t *testing.T) {
	c := NewClientState(Snapshot{PTS: 10, Entities: map[string]map[string]SnapshotEntity{
		KindWallet: {"SUP": {V: 5, D: json.RawMessage(`{"cash":5}`)}},
	}})
	// a duplicate, an older version under a newer pts (snapshot raced it), the next one
	res := c.Apply([]Record{rec(9, KindWallet, "SUP", 4, OpSet, `{"cash":4}`), rec(11, KindWallet, "SUP", 5, OpSet, `{"cash":5}`),
		rec(12, KindWallet, "SUP", 6, OpSet, `{"cash":6}`)})
	if res.Duplicates != 1 || res.Applied != 2 || res.Gap {
		t.Fatalf("apply = %+v", res)
	}
	if got := string(c.Entities[Key{KindWallet, "SUP"}].Data); got != `{"cash":6}` || c.PTS != 12 {
		t.Fatalf("state = %s at %d", got, c.PTS)
	}
	// out of order: 14 before 13 is a gap; nothing past it applies
	res = c.Apply([]Record{rec(14, KindWallet, "SUP", 8, OpSet, `{"cash":8}`)})
	if !res.Gap || c.PTS != 12 {
		t.Fatalf("a gap was applied: %+v pts %d", res, c.PTS)
	}
	res = c.Apply([]Record{rec(14, KindWallet, "SUP", 8, OpSet, `{"cash":8}`), rec(13, KindWallet, "SUP", 7, OpDel, "")})
	if res.Gap || c.PTS != 14 {
		t.Fatalf("in-order apply failed: %+v pts %d", res, c.PTS)
	}
	if got := string(c.Entities[Key{KindWallet, "SUP"}].Data); got != `{"cash":8}` {
		t.Fatalf("after del and set = %s", got)
	}
}

func TestAffectedReadsActorAndPayload(t *testing.T) {
	actor := "11111111-1111-4111-8111-111111111111"
	payee := "22222222-2222-4222-8222-222222222222"
	crew := "33333333-3333-4333-8333-333333333333"
	payload := json.RawMessage(`{"payee_id":"` + payee + `","amount":5,"crew":{"player_ids":["` + crew + `","` + actor + `"]},
		"company_id":"44444444-4444-4444-8444-444444444444","note":"player-` + actor + `"}`)
	got := Affected(envelope.Metadata{PlayerID: actor}, payload, []string{"payee_id"})
	want := []string{actor, crew, payee}
	if len(got) != len(want) {
		t.Fatalf("Affected = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Affected = %v, want %v", got, want)
		}
	}
}

func TestSettlementsFromPayload(t *testing.T) {
	id := "55555555-5555-4555-8555-555555555555"
	got := Settlements(json.RawMessage(`{"settlement_id":"` + id + `","building_id":"x"}`))
	if len(got) != 1 || got[0] != id {
		t.Fatalf("Settlements = %v", got)
	}
	if got := Settlements(json.RawMessage(`{"settlement_id":"not-an-id"}`)); len(got) != 0 {
		t.Fatalf("a non-id was taken: %v", got)
	}
}

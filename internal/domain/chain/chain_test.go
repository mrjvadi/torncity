package chain

import "testing"

// The shipped iron chain at the rates of today's rows: shifts of 30 minutes (1 hour for the clamp), one worker slot each for the
// smith and the collier, two for the pit.
func ironNodes() []Node {
	return []Node{
		{Code: "bloomery", ShiftMinutes: 30, Slots: 1, In: map[string]int64{"iron_ore": 3, "charcoal": 4}, Out: map[string]int64{"bloom": 1}},
		{Code: "charcoal_clamp", ShiftMinutes: 60, Slots: 1, In: map[string]int64{"firewood": 6}, Out: map[string]int64{"charcoal": 2}},
		{Code: "iron_pit", ShiftMinutes: 30, Slots: 2, Out: map[string]int64{"iron_ore": 6}},
		{Code: "woodcutter_camp", ShiftMinutes: 30, Slots: 2, Out: map[string]int64{"timber": 4, "firewood": 2}},
	}
}

func TestComputeUnrollsTheIronChain(t *testing.T) {
	p, err := Compute("bloom", 2*Milli, ironNodes()) // two blooms an hour
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Line{}
	for _, l := range p.Lines {
		got[l.Item] = l
	}
	// a bloomery makes 2 blooms an hour (1 per 30 minutes): one building
	if got["bloom"].Count != 1 || got["bloom"].Node != "bloomery" {
		t.Fatalf("bloom line %+v", got["bloom"])
	}
	// 2 blooms an hour need 12 ore (3 each per 30 minutes, 6 an hour...) wait: 3 ore per bloom, so 6 ore and 8 charcoal an hour
	if got["iron_ore"].NeedMilli != 6*Milli || got["charcoal"].NeedMilli != 8*Milli {
		t.Fatalf("ore %d charcoal %d (milli)", got["iron_ore"].NeedMilli, got["charcoal"].NeedMilli)
	}
	// a clamp makes 2 charcoal an hour: four clamps per 8 an hour; the pit (2 slots x 6 a shift) makes 24 ore an hour: one
	if got["charcoal"].Count != 4 || got["iron_ore"].Count != 1 {
		t.Fatalf("clamps %d pits %d", got["charcoal"].Count, got["iron_ore"].Count)
	}
	// firewood: 6 per charcoal pair, 12 an hour for 8 charcoal... a camp makes 2 x 2 x 2 = 8 firewood an hour: two camps
	if got["firewood"].NeedMilli != 24*Milli || got["firewood"].Count != 3 {
		t.Fatalf("firewood %+v", got["firewood"])
	}
	if len(p.Raw) != 0 {
		t.Fatalf("everything is made here, raw = %v", p.Raw)
	}
	if _, err := Compute("steel", Milli, ironNodes()); err != ErrNoMaker {
		t.Fatalf("a target nothing makes: %v", err)
	}
}

// The view names the link nearest the target that cannot keep up, with the fix.
func TestTheViewNamesTheFirstStarvedLink(t *testing.T) {
	nodes := ironNodes()
	p, _ := Compute("bloom", 2*Milli, nodes)
	// the bloomery and the pit stand, but only two clamps and one camp
	run := map[string]int64{"bloomery": 1, "iron_pit": 1, "charcoal_clamp": 2, "woodcutter_camp": 1}
	s := Check(p, nodes, run)
	if len(s) < 2 {
		t.Fatalf("starved links: %+v", s)
	}
	first, ok := First(s)
	if !ok || first.Item != "charcoal" || first.Node != "charcoal_clamp" {
		t.Fatalf("first starved link = %+v, want the charcoal clamps", first)
	}
	if first.HaveMilli != 4*Milli || first.NeedMilli != 8*Milli || first.Running != 2 || first.Add != 2 {
		t.Fatalf("charcoal %d of %d an hour, running %d, add %d", first.HaveMilli, first.NeedMilli, first.Running, first.Add)
	}
	// the camps are starved further up the chain, farther from the target
	last := s[len(s)-1]
	if last.Item != "firewood" || last.Add != 2 {
		t.Fatalf("the farthest link %+v, want firewood with 2 camps to add", last)
	}
	// fully built: nothing is starved
	if got := Check(p, nodes, map[string]int64{"bloomery": 1, "iron_pit": 1, "charcoal_clamp": 4, "woodcutter_camp": 3}); len(got) != 0 {
		t.Fatalf("a full chain is starved at %+v", got)
	}
	// nothing running at all: the bloom itself is the first link
	none := Check(p, nodes, map[string]int64{})
	if f, _ := First(none); f.Item != "bloom" || f.Add != 1 {
		t.Fatalf("with nothing running the first link is %+v", f)
	}
}

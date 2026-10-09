//go:build integration

package tests

import (
	"sort"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// own gives the settlement everything a knowledge item stands on (not the item itself).
func (e *researchEnv) own(code string) {
	e.t.Helper()
	tree := loadTestContent(e.t).SettlementKnowledgeTree()
	seen := map[string]bool{}
	providersOf := func(capability, except string) []string {
		var out []string
		for c, t := range tree {
			provides := t.Provides
			if len(provides) == 0 {
				provides = []string{t.Code}
			}
			for _, p := range provides {
				if p == capability && c != except {
					out = append(out, c)
				}
			}
		}
		sort.Strings(out)
		return out
	}
	var add func(c string)
	add = func(c string) {
		if seen[c] {
			return
		}
		seen[c] = true
		tech, ok := tree[c]
		if !ok {
			e.t.Fatalf("no knowledge %q", c)
		}
		for _, r := range tech.Requires {
			add(r)
		}
		for _, capability := range tech.RequiresCapability {
			if ps := providersOf(capability, c); len(ps) > 0 {
				add(ps[0])
			}
		}
		if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at)
			VALUES (gen_random_uuid(), $1::uuid, $2, 'researched', now()) ON CONFLICT DO NOTHING`, e.cityID, c); err != nil {
			e.t.Fatal(err)
		}
	}
	for _, r := range tree[code].Requires {
		add(r)
	}
	for _, capability := range tree[code].RequiresCapability {
		if ps := providersOf(capability, code); len(ps) > 0 {
			add(ps[0])
		}
	}
}

// What is ahead of the world costs more and takes longer, by a factor of the config; once enough settlements hold it
// the world has caught up and the price falls, and the settlement behind learns faster than the pioneer did.
func TestResearchAheadOfTheEraCostsMore(t *testing.T) {
	e := newResearchEnv(t)
	seedTreasury(t, e.pool, e.cityID, 200_000)
	e.own("road_code")
	if _, err := e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_knowledge_holder_counts WHERE code = 'road_code'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_knowledge_holder_counts WHERE code = 'road_code'`)
	})
	// road_code is five steps deep; the world's frontier is 4 until 16 percent of the settlements hold a deeper item
	if r := refusalOf(e.research("road_code", "")); r != "" {
		t.Fatalf("road_code was refused: %s", r)
	}
	p := e.project("road_code")
	if p.Ahead != 13_000 || p.Cost != 6_500 {
		t.Errorf("one step ahead of the world costs 30 percent more: %+v", p)
	}
	if got := p.Finish.Sub(p.Started); got != 52*time.Hour {
		t.Errorf("40h and 30 percent more is 52h, got %v", got)
	}
	if p.Speed != 10_000 {
		t.Errorf("nobody holds it, so there is nothing to catch up on: %d", p.Speed)
	}

	// the world catches up: 2 of 10 settlements hold the item
	if _, err := e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_research WHERE settlement_id = $1::uuid AND code = 'road_code'`, e.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_knowledge_holder_counts (code, holders, total_settlements, refreshed_at)
		VALUES ('road_code', 2, 10, now()) ON CONFLICT (code) DO UPDATE SET holders = 2, total_settlements = 10`); err != nil {
		t.Fatal(err)
	}
	if r := refusalOf(e.research("road_code", "")); r != "" {
		t.Fatalf("road_code was refused: %s", r)
	}
	p = e.project("road_code")
	if p.Ahead != 10_000 || p.Cost != 5_000 {
		t.Errorf("once the world holds it the price is the plain one: %+v", p)
	}
	// 20 percent of the world holds it: the catch-up is 20 percent of 4000 bps
	if p.Speed != 10_800 {
		t.Errorf("a settlement behind learns faster: speed %d, want 10800", p.Speed)
	}
	e.verify()
}

// A laboratory needs two scholars to open and gives two slots; a library beside it gives one more.
func TestResearchTheLaboratoryNeedsTwoScholars(t *testing.T) {
	e := newResearchEnv(t)
	e.building("library")
	lab := e.building("laboratory")
	e.stock("wool", 10)
	e.stock("timber", 10)
	scholar := e.scholar()

	// one player is not enough to open it; a town scholar fills the second post
	e.desk(scholar, village.ResearchActionPost, lab)
	d, _ := e.desk(e.head, "", "")
	var labLine village.ResearchBuildingLine
	for _, b := range d.Buildings {
		if b.ID == lab {
			labLine = b
		}
	}
	if !labLine.Open || labLine.Players != 1 || labLine.NPCs != 1 || labLine.Needed != 2 {
		t.Fatalf("the laboratory with a player and a town scholar: %+v (%+v)", labLine, d.Buildings)
	}
	if d.Capacity != 1+1+2 {
		t.Errorf("capacity = %d, want the free slot, the library's one and the laboratory's two", d.Capacity)
	}
	if got := e.held("timber"); got != 8 {
		t.Errorf("the laboratory burns 2 timber a working day: %d left of 10", got)
	}
	if got := e.held("wool"); got != 8 {
		t.Errorf("the library takes 1 wool and the laboratory 1: %d left of 10", got)
	}
	e.verify()
}

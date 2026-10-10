//go:build integration

package tests

import (
	"testing"
	"time"
)

// The knowledge a settlement holds about a craft lifts every shift of its workplace (ADR 0058): once the settlement knows
// milling and milling_ii, a mill yields 7 percent more than it did before.
func TestKnowledgeOfTheCraftRaisesTheShiftOutput(t *testing.T) {
	w := newWorkplaceEnv(t, "mill")
	w.village.WithRealItems(realItemRules(w.cfg, w.clock.Now(), time.Hour))
	w.shift()
	base := w.lastOutputBPS()

	for _, k := range []string{"milling", "milling_ii"} {
		if _, err := w.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at) VALUES (gen_random_uuid(), $1::uuid, $2, 'researched', now())`, w.cityID, k); err != nil {
			t.Fatal(err)
		}
	}
	w.shift()
	got := w.lastOutputBPS()
	if base <= 0 || got <= base {
		t.Fatalf("knowing the craft should raise the output: %d against %d", got, base)
	}
	// milling_ii no longer adds a percentage: it unlocks the water mill (docs/adr/0067)
	if want := base * 10_400 / 10_000; got < want-1 || got > want+1 {
		t.Errorf("milling (400) adds 400 basis points and milling_ii none: %d, want about %d", got, want)
	}
}

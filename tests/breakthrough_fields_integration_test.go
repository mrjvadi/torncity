//go:build integration

package tests

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
)

// Every research field can accrue experience on a fresh settlement (ADR 0054, plan A6): craft, food, water works,
// infrastructure, security, health, market and education are each fed by a real kind of work, and the proof does the work.

func (e *researchEnv) experience(field string) int64 {
	e.t.Helper()
	return e.scalar(`SELECT COALESCE((SELECT points FROM settlement_experience WHERE settlement_id = $1::uuid AND field = $2), 0)`, e.cityID, field)
}

func TestEveryBreakthroughFieldAccruesOnAFreshSettlement(t *testing.T) {
	e := newResearchEnv(t)
	cfg := config.Defaults()
	clock, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	e.village.WithService(handlers.ServiceRules{Clock: clock})
	e.village.WithTrade(handlers.TradeRules{Rules: settlementcfg.Trade(cfg.Settlement), Clock: clock, KeepPresets: cfg.Settlement.ExportKeepPresets})
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM service_days WHERE settlement_id = $1::uuid`, e.cityID)
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM experience_days WHERE settlement_id = $1::uuid`, e.cityID)
	})
	for field := range content.ResearchFields {
		if e.experience(field) != 0 {
			t.Fatalf("a fresh settlement already has %s experience", field)
		}
	}
	w := &workplaceEnv{researchEnv: e, worker: e.scholar()}
	for i := 0; i < 4; i++ { // homes: the people who staff the posts of the day come from the empty homes
		e.building("cottage")
	}
	e.stock("wheat", 40) // the crews' food
	e.stock("timber", 12)
	e.stock("firewood", 10)
	e.stock("cloth", 5)
	e.stock("spring_water", 10)
	e.stock("paper", 5)

	// craft: a finished shift at the carpenter's
	w.shiftIn(e.building("carpentry_workshop"))
	// food: a finished shift on a farm
	w.shiftIn(e.building("farm_canal"))
	// water works: the well
	w.shiftIn(e.building("well"))
	// infrastructure: a crew lays a road
	road := e.place("road")
	w.crewShift(road)
	// security: a watch post held for the day (the overview settles it), health: a health house open
	e.building("watch_hut")
	e.building("health_house")
	e.overview()
	// market: the trader buys the head's surplus
	e.putOnSale("timber")
	// education: scholars work a day in the library
	e.building("library")
	e.desk(e.head, "", "")

	for field := range content.ResearchFields {
		if e.experience(field) <= 0 {
			t.Errorf("the field %q gave no experience after its own kind of work", field)
		}
	}
	// the day sources are fenced: settling the same day again adds nothing
	before := e.experience("security") + e.experience("health") + e.experience("market") + e.experience("education")
	e.overview()
	e.desk(e.head, "", "")
	if after := e.experience("security") + e.experience("health") + e.experience("market") + e.experience("education"); after != before {
		t.Errorf("a day source paid twice: %d to %d", before, after)
	}
	e.verify()
}

// putOnSale puts an item on sale to the travelling trader, keeping nothing back.
func (e *researchEnv) putOnSale(item string) {
	e.t.Helper()
	if _, err := e.village.TradeDesk(testCtx(e.t), e.as(e.head, "settlement.trade", "trade"), handlers.VillageTradeRequest{Action: "keep", Code: item, N: "0"}); err != nil {
		e.t.Fatal(err)
	}
}

// overview reads the village overview, which settles the day's services.
func (e *researchEnv) overview() {
	e.t.Helper()
	if _, err := e.village.Overview(testCtx(e.t), e.as(e.head, "settlement.overview", "overview")); err != nil {
		e.t.Fatal(err)
	}
}

// crewShift has the worker take the construction job of a site, work the shift and end it.
func (w *workplaceEnv) crewShift(site string) {
	w.t.Helper()
	s, _ := w.site(w.head, site)
	if s.Job == nil {
		w.t.Fatalf("the site has no job on the board: %+v", s)
	}
	if _, err := rrc(w.village.LaborTake(testCtx(w.t), w.as(w.worker, "settlement.labor.take", "labor.take"), handlers.VillageLaborRequest{ID: s.Job.ID})); err != nil {
		w.t.Fatal(err)
	}
	w.clock.Advance(2 * time.Hour)
	for _, sh := range w.workingShifts(site) {
		w.end(sh)
	}
}

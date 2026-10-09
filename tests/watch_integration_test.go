//go:build integration

package tests

import (
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The night watch (ADR 0052): a watch post counts its security on a day it was held (watchmen from the pool, paid by the
// treasury, and a fire of firewood); an idle post secures nothing; the day is judged once.

type watchEnv struct {
	*researchEnv
}

func newWatchEnv(t *testing.T) *watchEnv {
	t.Helper()
	r := newResearchEnv(t)
	cfg := config.Defaults()
	clock, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	r.village.WithWatch(handlers.WatchRules{Clock: clock})
	t.Cleanup(func() {
		_, _ = r.pool.Raw().Exec(testCtx(t), `DELETE FROM watch_days WHERE settlement_id = $1::uuid`, r.cityID)
	})
	return &watchEnv{r}
}

func (e *watchEnv) overview() village.VillageOverviewView {
	e.t.Helper()
	resp, err := e.village.Overview(testCtx(e.t), e.as(e.head, "settlement.overview", "overview"))
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.VillageOverviewView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		e.t.Fatalf("the overview: %v", err)
	}
	return v
}

func (e *watchEnv) days() int64 {
	return e.scalar(`SELECT count(*) FROM watch_days WHERE settlement_id = $1::uuid`, e.cityID)
}

func (e *watchEnv) verifyWatch() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Watch || s.WatchLedger != s.WatchRows || s.WatchItems != s.WatchFuelRows || s.WatchDayBroken != 0 {
		e.t.Errorf("the watch invariants do not hold: %+v", s)
	}
}

// A hut with watchmen, wages and a fire secures the village; the day is paid once however often and by however many it
// is looked at; the next day it burns again.
func TestWatchPostIsHeldWithGuardsWageAndFire(t *testing.T) {
	e := newWatchEnv(t)
	hut := e.building("watch_hut")
	e.stock("firewood", 5)
	treasury0 := e.treasury()

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.village.Overview(testCtx(t), e.as(e.head, "settlement.overview", "overview")); err != nil {
				t.Errorf("overview: %v", err)
			}
		}()
	}
	wg.Wait()
	v := e.overview()
	if e.days() != 1 {
		t.Fatalf("the watch was judged %d times", e.days())
	}
	if len(v.Watch) != 1 || !v.Watch[0].Held || v.SecurityPercent <= 0 {
		t.Fatalf("a held post counts its security: %+v, %d percent", v.Watch, v.SecurityPercent)
	}
	if e.held("firewood") != 4 {
		t.Errorf("the fire burns one firewood a night: %d left of 5", e.held("firewood"))
	}
	wage := e.scalar(`SELECT wage FROM watch_days WHERE settlement_id = $1::uuid`, e.cityID)
	if wage <= 0 || treasury0-e.treasury() != wage {
		t.Errorf("the treasury paid %d, the day says %d", treasury0-e.treasury(), wage)
	}
	if e.scalar(`SELECT guards FROM watch_days WHERE settlement_id = $1::uuid`, e.cityID) != 2 {
		t.Errorf("a post has two watchmen")
	}
	_ = hut
	e.verifyWatch()

	e.clock.Advance(26 * time.Hour)
	e.overview()
	if e.days() != 2 || e.held("firewood") != 3 {
		t.Errorf("the next night: %d days, firewood %d (want 3)", e.days(), e.held("firewood"))
	}
	e.verifyWatch()
}

// No firewood, or no money for the wage: the post is idle and secures nothing; the reason is named.
func TestWatchPostIdleWithoutFuelOrWage(t *testing.T) {
	e := newWatchEnv(t)
	e.building("watch_hut")
	v := e.overview() // no firewood in the store
	if len(v.Watch) != 1 || v.Watch[0].Held || v.Watch[0].Idle != "no_fuel" || v.SecurityPercent != 0 {
		t.Fatalf("a fire with no wood: %+v, %d percent", v.Watch, v.SecurityPercent)
	}
	e.clock.Advance(26 * time.Hour)
	e.stock("firewood", 3)
	e.drainTreasury()
	v = e.overview()
	if len(v.Watch) != 1 || v.Watch[0].Held || v.Watch[0].Idle != "no_wage" || e.held("firewood") != 3 {
		t.Fatalf("watchmen nobody can pay: %+v, firewood %d", v.Watch, e.held("firewood"))
	}
	e.verifyWatch()
}

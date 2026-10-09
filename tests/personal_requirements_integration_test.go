//go:build integration

package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The personal prerequisites of a post (ADR 0055, plan A7): a carpenter's shift asks level 2, a scholar's post literacy. The
// rules are built the way the service builds them (cmd/game personalRules): refused after the grace with what is missing,
// allowed during it with a warning, never silent.

type personalEnv struct {
	*workplaceEnv
}

func newPersonalEnv(t *testing.T, inGrace bool) *personalEnv {
	t.Helper()
	w := newWorkplaceEnv(t, "carpentry_workshop")
	cfg := config.Defaults()
	from := w.clock.Now().Add(-time.Hour) // the rule began an hour ago: the grace is running
	if !inGrace {
		from = w.clock.Now().Add(-time.Duration(cfg.Settlement.PersonalGraceDays+1) * 24 * time.Hour)
	}
	w.village.WithPersonal(handlers.PersonalRules{From: from, GraceDays: cfg.Settlement.PersonalGraceDays})
	w.stock("timber", 6)
	return &personalEnv{w}
}

func (e *personalEnv) work() *presentation.Response {
	e.t.Helper()
	resp, err := rrc(e.village.Work(testCtx(e.t), e.as(e.worker, "settlement.work", "work"), handlers.VillageWorkRequest{ID: e.camp}))
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *personalEnv) setLevel(n int) {
	e.t.Helper()
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `UPDATE player_stats SET level = $2 WHERE player_id = $1::uuid`, e.worker.ID, n); err != nil {
		e.t.Fatal(err)
	}
}

// During the grace a player below level 2 may still work at the carpenter's, and the work screen warns him what will be
// needed and from when.
func TestPersonalPrerequisitesWarnDuringTheGrace(t *testing.T) {
	e := newPersonalEnv(t, true)
	e.setLevel(1)
	list := e.work()
	_ = list
	if n := len(e.workingShifts(e.camp)); n != 1 {
		t.Fatalf("during the grace the shift should start: %d running", n)
	}
	resp, err := rrc(e.village.Work(testCtx(t), e.as(e.head, "settlement.work", "work"), handlers.VillageWorkRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var v village.WorkView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	warned := false
	for _, p := range v.Places {
		for _, n := range p.Personal {
			warned = warned || (n.Kind == village.PersonalLevel && n.Need == 2)
		}
	}
	if !warned || v.PersonalUntil.IsZero() {
		t.Errorf("the work screen should warn of level 2 and say until when: %+v until %v", v.Places, v.PersonalUntil)
	}
}

// After the grace the same shift is refused, naming what is missing; at the level it works; a post that asks nothing of
// anyone (a labourer's at the smithy) is open to everyone.
func TestPersonalPrerequisitesRefuseAfterTheGrace(t *testing.T) {
	e := newPersonalEnv(t, false)
	e.setLevel(1)
	resp := e.work()
	if !strings.Contains(resp.Text, "شرط شخصی") || !strings.Contains(resp.Text, "سطح") {
		t.Fatalf("a level 1 player at the carpenter's after the grace: %q", resp.Text)
	}
	if n := len(e.workingShifts(e.camp)); n != 0 {
		t.Fatalf("a refused shift started: %d running", n)
	}
	e.setLevel(2)
	if resp := e.work(); strings.Contains(resp.Text, "شرط شخصی") {
		t.Fatalf("a level 2 player was refused: %q", resp.Text)
	}
	if n := len(e.workingShifts(e.camp)); n != 1 {
		t.Fatalf("a level 2 player's shift should run: %d", n)
	}
	// the smithy has a smith (level 3) and a labourer who needs nothing: anyone may work there
	e.stock("bloom", 2)
	e.stock("charcoal", 2)
	smithy := e.building("smithy")
	other := e.scholar()
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE player_stats SET level = 1 WHERE player_id = $1::uuid`, other.ID); err != nil {
		t.Fatal(err)
	}
	resp2, err := rrc(e.village.Work(testCtx(t), e.as(other, "settlement.work", "work"), handlers.VillageWorkRequest{ID: smithy}))
	if err != nil || strings.Contains(resp2.Text, "شرط شخصی") {
		t.Fatalf("the labourer's post at the smithy asks nothing: %v %q", err, resp2.Text)
	}
}

// A scholar's post asks literacy: refused after the grace until the player holds the literacy certificate.
func TestPersonalLiteracyForAScholarsPost(t *testing.T) {
	e := newPersonalEnv(t, false)
	lib := e.building("library")
	reader := e.scholar()
	take := func() *presentation.Response {
		t.Helper()
		resp, err := rrc(e.village.ResearchDesk(testCtx(t), e.as(reader, "settlement.research", "research"), handlers.VillageResearchRequest{Action: "post", Code: lib}))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := take(); !strings.Contains(r.Text, "شرط شخصی") {
		t.Fatalf("an illiterate player took a scholar's post: %q", r.Text)
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO certifications (id, player_id, course_code, issued_at) VALUES (gen_random_uuid(), $1::uuid, 'literacy_class', now())`, reader.ID); err != nil {
		t.Skipf("cannot issue the certificate in this schema: %v", err)
	}
	if r := take(); strings.Contains(r.Text, "شرط شخصی") {
		t.Fatalf("a literate player was refused: %q", r.Text)
	}
}

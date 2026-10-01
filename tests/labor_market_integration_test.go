//go:build integration

// Integration test of the labour market (ADR 0037, migration 0059): a building
// is finished only by the work of shifts, a player worker and NPC labourers are
// paid by the employer, the levy of a citizen employer goes to the village, more
// homes lower the wage and scarcity raises it, and `admin economy verify` stays
// green.
package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

type laborEnv struct {
	*foundingEnv
	t       *testing.T
	village *handlers.VillageHandler
	rules   labor.Rules
	cityID  string
	head    *application.Player
	meta    envelope.Metadata
}

func newLaborEnv(t *testing.T) *laborEnv {
	t.Helper()
	e := newFoundingEnv(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatalf("loading the locale catalogue: %v", err)
	}
	rules := labor.Default()
	uow := postgres.NewUnitOfWork(e.pool, testDefaultLanguage)
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(e.pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).WithLabor(rules, []int64{1, 2, 4}, []int64{100, 125, 150, 200})

	meta, founder := e.group(t)
	foundVillage(t, e.pool, e.h, meta)
	var cityID string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}
	pool := e.pool
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		// Everyone whose money the shifts moved: the workers and the citizen employers.
		var people []string
		rows, err := pool.Raw().Query(c, `
			SELECT player_id::text FROM settlement_shifts WHERE settlement_id = $1::uuid AND player_id IS NOT NULL
			UNION SELECT payer_id::text FROM settlement_shifts WHERE settlement_id = $1::uuid AND payer_kind = 'player' AND payer_id IS NOT NULL
			UNION SELECT employer_id::text FROM labor_jobs WHERE settlement_id = $1::uuid AND employer_kind = 'player'`, cityID)
		if err == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					people = append(people, id)
				}
			}
			rows.Close()
		}
		for _, stmt := range []string{
			`DELETE FROM game_actions WHERE reference_type = 'settlement_shift' AND reference_id IN (SELECT id FROM settlement_shifts WHERE settlement_id = $1::uuid)`,
			`DELETE FROM game_actions WHERE reference_type = 'settlement_building' AND reference_id IN (SELECT id FROM settlement_buildings WHERE settlement_id = $1::uuid)`,
			// The shifts and jobs have no foreign key to the village or the players,
			// and the ledger side of their wages is purged with the players and the
			// treasury below: leaving the rows would make every later test's verify
			// see wages nobody paid.
			`DELETE FROM labor_workers WHERE player_id IN (SELECT player_id FROM settlement_shifts WHERE settlement_id = $1::uuid AND player_id IS NOT NULL)`,
			`DELETE FROM settlement_shifts WHERE settlement_id = $1::uuid`,
			`DELETE FROM labor_jobs WHERE settlement_id = $1::uuid`,
		} {
			if _, err := pool.Raw().Exec(c, stmt, cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		for _, id := range append(people, cityID) {
			purgeLedgerFor(t, pool, id)
		}
	})
	seedTreasury(t, pool, cityID, 60_000)
	return &laborEnv{foundingEnv: e, t: t, village: village, rules: rules, cityID: cityID, head: founder, meta: meta}
}

// as is the player asking through the game client, with fresh request ids.
func (l *laborEnv) as(p *application.Player, command, action string) envelope.Metadata {
	m := clientMeta(asPlayer(l.meta, p), command, action)
	m.IdempotencyKey = "it-" + randomToken(l.t, 16)
	m.RequestID = "req_" + randomToken(l.t, 16)
	return m
}

func (l *laborEnv) resident() *application.Player {
	p := insertPlayer(l.t, l.pool)
	if _, err := l.pool.Raw().Exec(testCtx(l.t), `UPDATE players SET residence_city_id = $1::uuid WHERE id = $2::uuid`, l.cityID, p.ID); err != nil {
		l.t.Fatal(err)
	}
	return p
}

// place puts a building on the first free lot and returns its id.
func (l *laborEnv) place(code string) string {
	l.t.Helper()
	ctx := testCtx(l.t)
	lots, err := rrc(l.village.Lots(ctx, l.as(l.head, "settlement.build.lots", "build.lots"), handlers.VillageLotsRequest{Code: code}))
	if err != nil {
		l.t.Fatal(err)
	}
	x, y, ok := firstFittingFreeLot(parseLotGrid(l.t, lots.View))
	if !ok {
		l.t.Fatalf("no lot fits %s:\n%s", code, lots.Text)
	}
	resp, err := rrc(l.village.Place(ctx, l.as(l.head, "settlement.build.place", "build.place"),
		handlers.VillageBuildRequest{Code: code, Lot: screens.LotToken(x, y, false), Confirm: screens.VillageBuildConfirm}))
	if err != nil {
		l.t.Fatal(err)
	}
	var id string
	if err := l.pool.Raw().QueryRow(ctx, `SELECT id::text FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = $2 AND status = 'building'`,
		l.cityID, code).Scan(&id); err != nil {
		l.t.Fatalf("%s was not placed: %v\n%s", code, err, resp.Text)
	}
	return id
}

type siteJSON struct {
	Status          string `json:"status"`
	ProgressBPS     int64  `json:"progress_bps"`
	DoneMinutes     int64  `json:"done_minutes"`
	RequiredMinutes int64  `json:"required_minutes"`
	CanWork         bool   `json:"can_work"`
	WorkWage        int64  `json:"work_wage"`
	Workers         []struct {
		WorkerNPC bool `json:"worker_npc"`
	} `json:"workers"`
	Job *struct {
		ID   string `json:"id"`
		Wage int64  `json:"wage"`
		Left int    `json:"left"`
	} `json:"job"`
	Market struct {
		Pool    int64 `json:"pool"`
		NPCWage int64 `json:"npc_wage"`
	} `json:"market"`
}

func (l *laborEnv) site(p *application.Player, id string) (siteJSON, *presenter.Response) {
	l.t.Helper()
	resp, err := rrc(l.village.LaborSite(testCtx(l.t), l.as(p, "settlement.labor.site", "labor.site"), handlers.VillageLaborRequest{ID: id}))
	if err != nil {
		l.t.Fatal(err)
	}
	var v siteJSON
	if len(resp.View) == 0 {
		l.t.Fatalf("the site screen carries no view:\n%s", resp.Text)
	}
	if err := json.Unmarshal(resp.View, &v); err != nil {
		l.t.Fatal(err)
	}
	return v, resp
}

type shiftRow struct{ id, action string }

func (l *laborEnv) workingShifts(buildingID string) []shiftRow {
	l.t.Helper()
	rows, err := l.pool.Raw().Query(testCtx(l.t),
		`SELECT id::text, game_action_id::text FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'working' ORDER BY started_at, id`, buildingID)
	if err != nil {
		l.t.Fatal(err)
	}
	defer rows.Close()
	var out []shiftRow
	for rows.Next() {
		var s shiftRow
		if err := rows.Scan(&s.id, &s.action); err != nil {
			l.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (l *laborEnv) end(s shiftRow) {
	l.t.Helper()
	m := l.as(l.head, "settlement.worked", "worked")
	for i := 0; i < 2; i++ { // twice: a redelivery changes nothing
		if _, err := rrc(l.village.Worked(testCtx(l.t), m, handlers.CrimeScheduledRequest{ReferenceID: s.id, ActionID: s.action})); err != nil {
			l.t.Fatalf("Worked #%d: %v", i+1, err)
		}
	}
}

func (l *laborEnv) sink() int64 {
	return l.scalar(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'system_sink'`)
}

func (l *laborEnv) scalar(query string, args ...any) int64 {
	l.t.Helper()
	var n int64
	if err := l.pool.Raw().QueryRow(testCtx(l.t), query, args...).Scan(&n); err != nil {
		l.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestLaborConstructionOnlyThroughShifts(t *testing.T) {
	l := newLaborEnv(t)
	ctx := testCtx(t)
	v0, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v0.Village {
		t.Skip("migration 0052 is not applied")
	}
	worker := l.resident()
	stranger := insertPlayer(t, l.pool)

	// 1. Placing a woodcutter's camp needs work, not time: 30 game minutes with
	// a reference crew of 4 is 120 worker-minutes, and nothing is scheduled to
	// finish it.
	camp := l.place("woodcutter_camp")
	if got := l.scalar(`SELECT work_required FROM settlement_buildings WHERE id = $1::uuid`, camp); got != 120 {
		t.Fatalf("work required = %d, want 120", got)
	}
	if got := l.scalar(`SELECT count(*) FROM game_actions WHERE reference_type = 'settlement_building' AND reference_id = $1::uuid`, camp); got != 0 {
		t.Fatalf("a timer was scheduled to finish the building: %d actions", got)
	}
	l.clock.Advance(1000 * time.Hour)
	if _, err := rrc(l.village.Built(ctx, l.as(l.head, "settlement.built", "built"), handlers.CrimeScheduledRequest{ReferenceID: camp})); err != nil {
		t.Fatal(err)
	}
	site, _ := l.site(l.head, camp)
	if site.Status != "building" || site.DoneMinutes != 0 || site.ProgressBPS != 0 {
		t.Fatalf("time alone advanced the building: %+v", site)
	}
	if site.Job == nil || site.Job.Wage < l.rules.MinWage["village"] || site.Job.Left != 3 {
		t.Fatalf("the site's job on the board: %+v", site.Job)
	}
	jobWage := site.Job.Wage
	board, err := rrc(l.village.LaborBoard(ctx, l.as(worker, "settlement.labor.board", "labor.board")))
	if err != nil || !strings.Contains(board.Text, "تابلوی استخدام") || !strings.Contains(econButtonData(board), "settlement:labor.site:"+camp) {
		t.Fatalf("the hiring board: %v\n%s\n%s", err, board.Text, econButtonData(board))
	}

	// 2. A player takes the job: paid what the employer offered, when the shift
	// ends. A stranger who is not in the village cannot; nobody works two shifts.
	if _, err := rrc(l.village.LaborTake(ctx, l.as(stranger, "settlement.labor.take", "labor.take"), handlers.VillageLaborRequest{ID: site.Job.ID})); err != nil {
		t.Errorf("a stranger's take: %v", err)
	}
	if got := l.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid`, camp); got != 0 {
		t.Fatalf("a stranger who does not live here was given a shift")
	}
	cash0 := cashBalance(t, l.pool, application.AccountPlayerCash, worker.ID)
	treasury0 := treasuryOf(t, l.pool, l.cityID)
	take := l.as(worker, "settlement.labor.take", "labor.take")
	resp, err := rrc(l.village.LaborTake(ctx, take, handlers.VillageLaborRequest{ID: site.Job.ID}))
	if err != nil || !strings.Contains(resp.Text, "شیفت شما شروع شد") {
		t.Fatalf("taking the job: %v\n%s", err, resp.Text)
	}
	if r, err := rrc(l.village.LaborTake(ctx, l.as(worker, "settlement.labor.take", "labor.take"), handlers.VillageLaborRequest{ID: site.Job.ID})); err != nil ||
		!strings.Contains(r.Text, "همین حالا") {
		t.Errorf("a second shift of the same worker was not refused: %+v %v", r, err)
	}
	shifts := l.workingShifts(camp)
	if len(shifts) != 1 {
		t.Fatalf("%d shifts on the site, want 1", len(shifts))
	}
	if _, err := rrc(l.village.Worked(ctx, l.as(l.head, "settlement.worked", "worked"), handlers.CrimeScheduledRequest{ReferenceID: shifts[0].id, ActionID: shifts[0].action})); err == nil {
		t.Error("a shift ended before its time")
	}
	l.clock.Advance(time.Hour + time.Minute)
	l.end(shifts[0])
	// An apprentice adds 70 % of 60 minutes.
	site, _ = l.site(l.head, camp)
	if site.DoneMinutes != 42 || site.Status != "building" {
		t.Fatalf("after one apprentice shift the work done is %d (%s), want 42", site.DoneMinutes, site.Status)
	}
	if got := cashBalance(t, l.pool, application.AccountPlayerCash, worker.ID) - cash0; got != jobWage {
		t.Fatalf("the worker was paid %d, want the job's %d", got, jobWage)
	}
	if got := treasury0 - treasuryOf(t, l.pool, l.cityID); got != jobWage {
		t.Fatalf("the treasury paid %d, want %d", got, jobWage)
	}
	if got := l.scalar(`SELECT shifts FROM labor_workers WHERE player_id = $1::uuid`, worker.ID); got != 1 {
		t.Fatalf("the worker's experience is %d shifts, want 1", got)
	}
	mine, err := rrc(l.village.LaborMine(ctx, l.as(worker, "settlement.labor.mine", "labor.mine")))
	if err != nil || !strings.Contains(mine.Text, "شاگرد") {
		t.Errorf("my work: %v\n%s", err, mine.Text)
	}

	// 3. Only the employer hires labourers; the head's two NPC labourers work
	// the rest, paid the market wage to the sink, and the last shift completes the
	// building.
	if r, err := rrc(l.village.LaborHire(ctx, l.as(worker, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: site.Job.ID, N: "2"})); err != nil ||
		!strings.Contains(r.Text, "کارفرمای همین کار") {
		t.Errorf("a worker hired labourers: %+v %v", r, err)
	}
	sink0 := l.sink()
	treasury1 := treasuryOf(t, l.pool, l.cityID)
	hired, err := rrc(l.village.LaborHire(ctx, l.as(l.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: site.Job.ID, N: "2"}))
	if err != nil || !strings.Contains(hired.Text, "کارگرها استخدام شدند") {
		t.Fatalf("hiring: %v\n%s", err, hired.Text)
	}
	shifts = l.workingShifts(camp)
	if len(shifts) != 2 {
		t.Fatalf("%d NPC shifts on the site, want 2", len(shifts))
	}
	if got := l.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND worker_kind = 'npc' AND player_id IS NULL`, camp); got != 2 {
		t.Fatalf("%d NPC shifts recorded, want 2", got)
	}
	l.clock.Advance(time.Hour + time.Minute)
	l.end(shifts[0])
	site, _ = l.site(l.head, camp)
	if site.DoneMinutes != 42+51 || site.Status != "building" {
		t.Fatalf("after an NPC shift the work done is %d (%s), want 93", site.DoneMinutes, site.Status)
	}
	l.end(shifts[1])
	site, _ = l.site(l.head, camp)
	if site.Status != "complete" || site.ProgressBPS != 10_000 {
		t.Fatalf("the building is not complete after all the work: %+v", site)
	}
	var built int
	if err := l.pool.Raw().QueryRow(ctx, `SELECT count(*) FROM settlement_buildings WHERE id = $1::uuid AND status = 'complete' AND completed_at IS NOT NULL`, camp).Scan(&built); err != nil || built != 1 {
		t.Fatalf("the row is not complete: %d %v", built, err)
	}
	if got := l.scalar(`SELECT count(*) FROM labor_jobs WHERE building_id = $1::uuid AND status = 'open'`, camp); got != 0 {
		t.Errorf("the finished building still has an open job")
	}
	npcPaid := l.scalar(`SELECT COALESCE(SUM(wage_paid), 0)::bigint FROM settlement_shifts WHERE building_id = $1::uuid AND worker_kind = 'npc'`, camp)
	if npcPaid < 2*l.rules.MinWage["village"] {
		t.Errorf("the NPC labourers were paid %d", npcPaid)
	}
	if got := l.sink() - sink0; got != npcPaid {
		t.Errorf("the sink took %d, the NPC labourers were paid %d", got, npcPaid)
	}
	if got := treasury1 - treasuryOf(t, l.pool, l.cityID); got != npcPaid {
		t.Errorf("the treasury paid %d for the NPC labourers, want %d", got, npcPaid)
	}

	// 4. A finished workplace is worked through the same board: the head posts
	// a production job and the worker takes it.
	post, err := rrc(l.village.LaborPost(ctx, l.as(l.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: camp}))
	if err != nil || !strings.Contains(post.Text, "آگهی روی تابلو رفت") {
		t.Fatalf("posting a production job: %v\n%s", err, post.Text)
	}
	prodSite, _ := l.site(worker, camp)
	if prodSite.Job == nil {
		t.Fatal("no production job on the board")
	}
	if r, err := rrc(l.village.LaborTake(ctx, l.as(worker, "settlement.labor.take", "labor.take"), handlers.VillageLaborRequest{ID: prodSite.Job.ID})); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(r.Text, "شیفت شما شروع شد") {
		t.Fatalf("taking the production job:\n%s", r.Text)
	}
	if got := l.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND kind = 'production' AND job_id IS NOT NULL AND status = 'working'`, camp); got != 1 {
		t.Fatalf("the production shift was not started through the job: %d", got)
	}
	// (It is left running: its goods would enter the village stock and the item
	// journal, which the shared test database keeps; the workplace shift's own
	// end is covered by TestVillageEconomyLoop.)
	if got := l.scalar(`SELECT wage FROM settlement_shifts WHERE building_id = $1::uuid AND kind = 'production' AND job_id IS NOT NULL AND status = 'working'`, camp); got != prodSite.Job.Wage {
		t.Fatalf("the production shift's wage is %d, want the job's %d", got, prodSite.Job.Wage)
	}

	// 5. The ledger, the item journal and the rows agree.
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	i, i0 := v.VillageInvariants, v0.VillageInvariants
	if i.LaborWageLedger-i.LaborWageRows != i0.LaborWageLedger-i0.LaborWageRows || i.LaborMismatched != i0.LaborMismatched ||
		i.LaborEscrowLedger-i.LaborEscrowRows != i0.LaborEscrowLedger-i0.LaborEscrowRows ||
		i.LaborBuiltWithoutWork != 0 || i.LaborWorkUnbacked != 0 || i.WageMismatched != i0.WageMismatched {
		t.Errorf("the labour ledger drifted: before %+v, after %+v", i0, i)
	}
	if i.LaborWageRows-i0.LaborWageRows != jobWage+npcPaid {
		t.Errorf("labour wage rows grew by %d, want %d", i.LaborWageRows-i0.LaborWageRows, jobWage+npcPaid)
	}
}

// A citizen who owns a building is its employer: the wage is set aside from
// their cash when a shift starts, paid to the worker at its end, and the
// village keeps its levy.
func TestLaborCitizenEmployerPaysAndTheVillageTakesItsLevy(t *testing.T) {
	l := newLaborEnv(t)
	ctx := testCtx(t)
	v0, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v0.Village {
		t.Skip("migration 0052 is not applied")
	}
	owner, worker := l.resident(), l.resident()
	road := l.place("road")
	// The citizen-loop hands the building to its owner and posts the owner's job.
	if _, err := l.pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET employer_player_id = $2::uuid WHERE id = $1::uuid`, road, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.pool.Raw().Exec(ctx, `UPDATE labor_jobs SET employer_kind = 'player', employer_id = $2::uuid, wage = 100 WHERE building_id = $1::uuid AND status = 'open'`, road, owner.ID); err != nil {
		t.Fatal(err)
	}
	const ownerCash = 5_000
	grantCash(t, l.pool, owner.ID, ownerCash)

	site, _ := l.site(worker, road)
	if site.Job == nil || site.Job.Wage != 100 || !site.CanWork {
		t.Fatalf("the citizen's job is not on offer: %+v", site)
	}
	if r, err := rrc(l.village.LaborClose(ctx, l.as(l.head, "settlement.labor.close", "labor.close"), handlers.VillageLaborRequest{ID: site.Job.ID})); err != nil ||
		!strings.Contains(r.Text, "کارفرمای همین کار") {
		t.Errorf("the village head closed a citizen's job: %+v %v", r, err)
	}
	if _, err := rrc(l.village.LaborTake(ctx, l.as(worker, "settlement.labor.take", "labor.take"), handlers.VillageLaborRequest{ID: site.Job.ID})); err != nil {
		t.Fatal(err)
	}
	if got := cashBalance(t, l.pool, application.AccountPlayerCash, owner.ID); got != ownerCash-100 {
		t.Fatalf("the employer's cash is %d after the shift began, want %d (the wage is set aside)", got, ownerCash-100)
	}
	if got := cashBalance(t, l.pool, application.AccountPlayerEscrow, owner.ID); got != 100 {
		t.Fatalf("the employer's escrow holds %d, want 100", got)
	}
	treasury0 := treasuryOf(t, l.pool, l.cityID)
	cash0 := cashBalance(t, l.pool, application.AccountPlayerCash, worker.ID)
	l.clock.Advance(time.Hour + time.Minute)
	l.end(l.workingShifts(road)[0])
	fee := l.rules.Fee(100)
	if got := cashBalance(t, l.pool, application.AccountPlayerCash, worker.ID) - cash0; got != 100-fee {
		t.Fatalf("the worker was paid %d, want %d (100 less the village's levy %d)", got, 100-fee, fee)
	}
	if got := treasuryOf(t, l.pool, l.cityID) - treasury0; got != fee {
		t.Fatalf("the village took %d, want %d", got, fee)
	}
	if got := cashBalance(t, l.pool, application.AccountPlayerEscrow, owner.ID); got != 0 {
		t.Fatalf("the escrow still holds %d", got)
	}
	// One apprentice shift is 42 of the road's 40 worker-minutes.
	site, _ = l.site(owner, road)
	if site.Status != "complete" {
		t.Fatalf("the road is not complete: %+v", site)
	}
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	i, i0 := v.VillageInvariants, v0.VillageInvariants
	if i.LaborWageLedger-i.LaborWageRows != i0.LaborWageLedger-i0.LaborWageRows || i.LaborMismatched != i0.LaborMismatched ||
		i.LaborEscrowLedger-i.LaborEscrowRows != i0.LaborEscrowLedger-i0.LaborEscrowRows || i.LaborBuiltWithoutWork != 0 || i.LaborWorkUnbacked != 0 {
		t.Errorf("the labour ledger drifted: before %+v, after %+v", i0, i)
	}
}

// The owner's example: more houses, more workers; fewer workers, scarcity - the
// wage an NPC labourer asks rises when demand outgrows the labour force and
// falls when homes are built.
func TestLaborScarcityAndHousing(t *testing.T) {
	l := newLaborEnv(t)
	ctx := testCtx(t)
	road := l.place("road")
	wage := func() (int64, int64) {
		s, _ := l.site(l.head, road)
		return s.Market.NPCWage, s.Market.Pool
	}
	baseWage, basePool := wage()
	if basePool != l.rules.PoolSize(0, 1) {
		t.Fatalf("the labour pool is %d, want %d (base housing, one resident)", basePool, l.rules.PoolSize(0, 1))
	}
	// Demand: many sites posting jobs at once.
	for i := 0; i < 4; i++ {
		id := insertSite(t, l, 30+i)
		_ = id
	}
	tightWage, tightPool := wage()
	if tightPool != basePool {
		t.Fatalf("demand changed the pool: %d -> %d", basePool, tightPool)
	}
	if tightWage <= baseWage {
		t.Fatalf("scarcity did not raise the wage: %d -> %d", baseWage, tightWage)
	}
	// Homes: a standing housing block adds 20 to the housing capacity.
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES (gen_random_uuid(), $1::uuid, 'housing_block', 40, 40, 'complete', now(), now())`, l.cityID); err != nil {
		t.Fatal(err)
	}
	roomyWage, roomyPool := wage()
	if roomyPool <= tightPool {
		t.Fatalf("a housing block did not grow the labour pool: %d -> %d", tightPool, roomyPool)
	}
	if roomyWage >= tightWage {
		t.Fatalf("more houses did not lower the wage under the same demand: %d -> %d", tightWage, roomyWage)
	}
	// And the wage is never under the village's minimum.
	if roomyWage < l.rules.MinWage["village"] {
		t.Errorf("the wage %d is under the minimum", roomyWage)
	}
}

// insertSite adds a building under construction with an open job, as demand.
func insertSite(t *testing.T, l *laborEnv, lot int) string {
	t.Helper()
	ctx := testCtx(t)
	var id string
	if err := l.pool.Raw().QueryRow(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, work_required)
		VALUES (gen_random_uuid(), $1::uuid, 'road', $2, 60, 'building', now(), 180) RETURNING id::text`, l.cityID, lot).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO labor_jobs (id, settlement_id, building_id, kind, employer_kind, employer_id, wage, shifts_total, shifts_started, npc_crew, status, created_by, created_at)
		VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'construction', 'settlement', $1::uuid, 30, 50, 0, 0, 'open', $3::uuid, now())`, l.cityID, id, l.head.ID); err != nil {
		t.Fatal(err)
	}
	return id
}

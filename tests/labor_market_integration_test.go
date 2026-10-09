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
	"github.com/mrjvadi/torncity/internal/presentation"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
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

// A standing workplace works with an NPC crew (roadmap 2.2 phase 2): the head posts the job
// and hires labourers; they work production shifts, the goods enter the stock, their wage
// leaves the treasury for the sink, a finished shift is replaced by the crew's next one,
// and a post never works more than its day's shifts.
// alignToLocalMorning moves the test clock to 06:00 of the settlement's local day, so a test that counts a post's day
// does not straddle the village's midnight (its zone follows where the world put it, so it is a different hour on every
// run: the test was flaky for the two hours before local midnight).
func (l *laborEnv) alignToLocalMorning() {
	l.t.Helper()
	var zone time.Duration
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(testCtx(l.t), func(ctx context.Context, tx application.Tx) error {
		s, err := tx.Settlements().ByID(ctx, l.cityID)
		zone = s.Zone()
		return err
	}); err != nil {
		l.t.Fatal(err)
	}
	local := l.clock.Now().Add(zone)
	morning := time.Date(local.Year(), local.Month(), local.Day(), 6, 0, 0, 0, time.UTC).Add(-zone)
	if morning.Before(l.clock.Now()) {
		morning = morning.Add(24 * time.Hour)
	}
	l.clock.Advance(morning.Sub(l.clock.Now()))
}

func TestNPCCrewWorksAProductionJob(t *testing.T) {
	l := newLaborEnv(t)
	l.alignToLocalMorning()
	ctx := testCtx(t)
	// the goods the shifts make enter the item journal, which is append-only: the shared
	// test database must not keep movements whose shift rows the env's cleanup removes
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, l.cityID)
			}
			if _, err := l.pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	v0, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v0.Village {
		t.Skip("migration 0052 is not applied")
	}
	camp := newUUID(t)
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'woodcutter_camp', 81, 3, 'complete', now(), now())`, camp, l.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(l.village.LaborPost(ctx, l.as(l.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: camp})); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := l.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND status = 'open'`, camp).Scan(&jobID); err != nil {
		t.Fatalf("no production job: %v", err)
	}
	sink0, treasury0 := l.sink(), treasuryOf(t, l.pool, l.cityID)
	hire := func() {
		t.Helper()
		_, _ = rrc(l.village.LaborHire(ctx, l.as(l.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "2"}))
	}
	running := func() int64 {
		return l.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND kind = 'production' AND worker_kind = 'npc' AND player_id IS NULL AND status = 'working'`, camp)
	}
	// no food in the village: the labourers do not start, the crew is paused with the reason
	hire()
	if got := running(); got != 0 {
		t.Fatalf("an NPC must not start a shift with no food: %d running", got)
	}
	var paused string
	if err := l.pool.Raw().QueryRow(ctx, `SELECT COALESCE(paused, '') FROM labor_jobs WHERE id = $1::uuid`, jobID).Scan(&paused); err != nil || paused != "no_food" {
		t.Fatalf("the crew should be paused for food: %q %v", paused, err)
	}
	// wheat arrives (2 points a unit): the crew starts, one wheat feeds two one-hour shifts
	uow := postgres.NewUnitOfWork(l.pool, testDefaultLanguage)
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{
			ID: newUUID(t), Item: "wheat", Qty: 10, ToOrg: application.SettlementOrg(l.cityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	hire()
	if got := running(); got != 2 {
		t.Fatalf("two NPC labourers should be working the camp, %d are", got)
	}
	// the work block says so
	meta := l.as(l.head, "settlement.building.view", "building.view")
	resp, err := rrc(l.village.BuildingView(ctx, meta, handlers.VillageBuildingViewRequest{BuildingID: camp}))
	if err != nil {
		t.Fatal(err)
	}
	var panel vpres.BuildingView
	if err := presentation.DecodeView(resp.View, &panel); err != nil {
		t.Fatal(err)
	}
	if panel.Work == nil || panel.Work.Filled != 2 || panel.Work.Status != vpres.NodeWorking {
		t.Errorf("the camp's work block should show two filled posts: %+v", panel.Work)
	}
	// they finish: the goods enter the stock, the wage goes to the sink, the crew starts again
	l.clock.Advance(time.Hour)
	shifts := l.workingShifts(camp)
	for _, s := range shifts {
		l.end(s)
	}
	var timber int64
	if err := l.pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = 'timber'`, l.cityID).Scan(&timber); err != nil {
		t.Fatal(err)
	}
	// an NPC works at 85 percent: two shifts of 6 timber deliver 5 + 5 and carry 0.2
	if timber != 10 {
		t.Errorf("two NPC shifts of 6 timber at 85 percent should deliver 10, %d are in the stock", timber)
	}
	if carry := l.scalar(`SELECT COALESCE((carry->>'timber')::bigint, 0) FROM settlement_buildings WHERE id = $1::uuid`, camp); carry != 2000 {
		t.Errorf("the camp should carry 2000 ten-thousandths of a timber, it carries %d", carry)
	}
	var wheat int64
	if err := l.pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = 'wheat'`, l.cityID).Scan(&wheat); err != nil {
		t.Fatal(err)
	}
	if wheat > 9 || wheat < 7 {
		t.Errorf("the meals should have taken one or two wheat of ten, %d are left", wheat)
	}
	paid := l.scalar(`SELECT COALESCE(SUM(wage_paid), 0)::bigint FROM settlement_shifts WHERE building_id = $1::uuid AND status = 'done'`, camp)
	if paid == 0 || l.sink()-sink0 != paid || treasury0-treasuryOf(t, l.pool, l.cityID) != paid {
		t.Errorf("the treasury should pay %d to the sink: sink +%d, treasury -%d", paid, l.sink()-sink0, treasury0-treasuryOf(t, l.pool, l.cityID))
	}
	if got := running(); got != 2 {
		t.Errorf("a finished shift is replaced by the crew's next: %d running", got)
	}
	// a post works only so many shifts a day: with the day used up the crew stops, with the reason. The yard is emptied
	// first, so that a full yard does not stop the crew before its day is used up.
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		for _, it := range []string{"timber", "firewood", "bark"} {
			var n int64
			if err := l.pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = $2`, l.cityID, it).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				if err := tx.Items().Move(ctx, application.ItemMove{ID: newUUID(t), Item: it, Qty: n, FromOrg: application.SettlementOrg(l.cityID), FromHolding: application.HoldWarehouse,
					Reason: application.ItemResearchUpkeep, ReferenceType: application.ResearchDayReference, ReferenceID: l.cityID, At: time.Now().UTC()}); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	l.clock.Advance(time.Hour)
	for _, s := range l.workingShifts(camp) {
		l.end(s)
	}
	// the day's shifts are stamped with the game clock (the cap counts per local day of the game clock)
	for i := 0; i < 40; i++ {
		if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_shifts (id, settlement_id, building_id, player_id, status, wage, wage_paid, produced, consumed,
			game_action_id, started_at, finish_at, finished_at, kind, worker_kind) VALUES ($1::uuid, $2::uuid, $3::uuid, NULL, 'done', 0, 0, '{}', '{}', $4::uuid, $5, $5, $5, 'production', 'npc')`,
			newUUID(t), l.cityID, camp, newUUID(t), l.clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	l.clock.Advance(time.Hour)
	for _, s := range l.workingShifts(camp) {
		l.end(s)
	}
	if got := running(); got != 0 {
		t.Errorf("a post past its day's shifts starts no more: %d running", got)
	}
	if err := l.pool.Raw().QueryRow(ctx, `SELECT COALESCE(paused, '') FROM labor_jobs WHERE id = $1::uuid`, jobID).Scan(&paused); err != nil || paused != "budget_spent" {
		t.Errorf("the crew should be paused with the reason: %q %v", paused, err)
	}
	// the ledger still balances
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if v.VillageInvariants.WageMismatched != v0.VillageInvariants.WageMismatched ||
		v.VillageInvariants.WageLedger-v.VillageInvariants.WageRows != v0.VillageInvariants.WageLedger-v0.VillageInvariants.WageRows {
		t.Errorf("the production wage ledger drifted: before %+v, after %+v", v0.VillageInvariants, v.VillageInvariants)
	}
}

// A player may work with the village out of food, hungry, at half the output of their rung;
// fed from the kitchen they work at the full rung (roadmap 2.2 phase 3).
func TestAHungryPlayerWorksHalfAndAFedOneFull(t *testing.T) {
	l := newLaborEnv(t)
	ctx := testCtx(t)
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, l.cityID)
			}
			if _, err := l.pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	camp := newUUID(t)
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'woodcutter_camp', 81, 3, 'complete', now(), now())`, camp, l.cityID); err != nil {
		t.Fatal(err)
	}
	worker := l.resident()
	work := func() {
		t.Helper()
		m := l.as(worker, "settlement.work", "work")
		if _, err := rrc(l.village.Work(ctx, m, handlers.VillageWorkRequest{ID: camp})); err != nil {
			t.Fatal(err)
		}
	}
	finish := func() {
		t.Helper()
		l.clock.Advance(time.Hour)
		for _, s := range l.workingShifts(camp) {
			l.end(s)
		}
	}
	timber := func() int64 {
		var n int64
		if err := l.pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = 'timber'`, l.cityID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	last := func() (fed bool, meal, bps int64) {
		t.Helper()
		if err := l.pool.Raw().QueryRow(ctx, `SELECT fed, meal_points, output_bps FROM settlement_shifts WHERE building_id = $1::uuid ORDER BY started_at DESC, id LIMIT 1`, camp).Scan(&fed, &meal, &bps); err != nil {
			t.Fatal(err)
		}
		return
	}
	// 1. no food: hungry, half the rung's output (apprentice 7000 x 5000 = 3500)
	work()
	if fed, meal, bps := last(); fed || meal != 0 || bps != 3500 {
		t.Errorf("a hungry apprentice: fed=%v meal=%d bps=%d, want false 0 3500", fed, meal, bps)
	}
	finish()
	if got := timber(); got != 2 {
		t.Errorf("6 timber at 35 percent deliver 2, %d did", got)
	}
	// 2. food in the village: fed, the full rung (7000), one point of meal
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{
			ID: newUUID(t), Item: "wheat", Qty: 4, ToOrg: application.SettlementOrg(l.cityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	work()
	if fed, meal, bps := last(); !fed || meal != 1 || bps != 7000 {
		t.Errorf("a fed apprentice: fed=%v meal=%d bps=%d, want true 1 7000", fed, meal, bps)
	}
	finish()
	// the kitchen: one wheat opened (2 points), one eaten, one left in the pot
	var pot, opened, eaten int64
	if err := l.pool.Raw().QueryRow(ctx, `SELECT pot, opened_points, eaten_points FROM settlement_kitchen WHERE settlement_id = $1::uuid`, l.cityID).Scan(&pot, &opened, &eaten); err != nil {
		t.Fatal(err)
	}
	if opened != 2 || eaten != 1 || pot != 1 {
		t.Errorf("the kitchen: opened %d, eaten %d, pot %d, want 2 1 1", opened, eaten, pot)
	}
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Goods || len(v.DriftedStacks) != 0 {
		t.Errorf("the item journal drifted from the stacks: %+v", v.DriftedStacks)
	}
}

// The working-node invariants of `admin economy verify` catch a meal that opened food no
// kitchen counted.
func TestWorkNodeInvariantsCatchAnUncountedMeal(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	verify := func() postgres.VillageInvariants {
		t.Helper()
		v, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		return v.VillageInvariants
	}
	if base := verify(); !base.WorkNodes || !base.WorkNodesOK() {
		t.Fatalf("a clean database should verify: %+v", base)
	}
	mealID := newUUID(t)
	t.Cleanup(func() {
		_, _ = pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_meals WHERE id = $1::uuid`, mealID)
	})
	if _, err := pool.Raw().Exec(ctx, `INSERT INTO settlement_meals (id, settlement_id, shift_id, item_code, units, points_each, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'wheat', 1, 2, now())`, mealID, newUUID(t), newUUID(t)); err != nil {
		t.Fatal(err)
	}
	if v := verify(); v.WorkNodesOK() || v.MealOpenedRows == v.MealOpenedKitchen {
		t.Errorf("an uncounted meal opening should break the kitchen check: %+v", v)
	}
}

// A production workplace wears with the days, works at a share when worn, closes when ruined,
// and is restored by a repair job the labourers work (roadmap 2.2 phase 5, ADR 0041 6.10).
func TestAWornWorkplaceWorksLessClosesAndIsRepaired(t *testing.T) {
	l := newLaborEnv(t)
	ctx := testCtx(t)
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, l.cityID)
			}
			if _, err := l.pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	// food in the village so meals never get in the way
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{
			ID: newUUID(t), Item: "wheat", Qty: 40, ToOrg: application.SettlementOrg(l.cityID), ToHolding: application.HoldWarehouse,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	camp := newUUID(t)
	age := func(days int) {
		t.Helper()
		if _, err := l.pool.Raw().Exec(ctx, `UPDATE settlement_buildings SET completed_at = now() - make_interval(days => $2), damage_bps = 0, damage_at = NULL WHERE id = $1::uuid`, camp, days); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'woodcutter_camp', 81, 3, 'complete', now(), now())`, camp, l.cityID); err != nil {
		t.Fatal(err)
	}
	worker := l.resident()
	work := func() (refused bool) {
		t.Helper()
		resp, err := rrc(l.village.Work(ctx, l.as(worker, "settlement.work", "work"), handlers.VillageWorkRequest{ID: camp}))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(resp.Text, "تعمیر")
	}
	lastBPS := func() int64 {
		var bps int64
		if err := l.pool.Raw().QueryRow(ctx, `SELECT output_bps FROM settlement_shifts WHERE building_id = $1::uuid AND kind = 'production' ORDER BY started_at DESC, id LIMIT 1`, camp).Scan(&bps); err != nil {
			t.Fatal(err)
		}
		return bps
	}
	finish := func() {
		t.Helper()
		l.clock.Advance(time.Hour)
		for _, s := range l.workingShifts(camp) {
			l.end(s)
		}
	}
	panel := func() *vpres.WorkNode {
		t.Helper()
		resp, err := rrc(l.village.BuildingView(ctx, l.as(l.head, "settlement.building.view", "building.view"), handlers.VillageBuildingViewRequest{BuildingID: camp}))
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.BuildingView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		return v.Work
	}
	// 1. a young workplace: the rung's full output (apprentice 7000)
	age(10)
	work()
	if got := lastBPS(); got != 7000 {
		t.Errorf("a young camp gives the rung's output, got %d", got)
	}
	finish()
	// 2. 120 days of wear (50 a day): condition 40 percent, worn, 75 percent of the rung's output
	age(120)
	work()
	if got := lastBPS(); got != 5250 {
		t.Errorf("a worn camp gives 75 percent of 7000 = 5250, got %d", got)
	}
	finish()
	// 3. 170 days: condition 15 percent, closed
	age(170)
	if w := panel(); w.Condition == nil || !w.Condition.Closed || w.Status != vpres.NodeIdle {
		t.Fatalf("a ruined camp is closed: %+v", w)
	}
	if !work() {
		t.Error("a closed camp must refuse a shift and say it needs repair")
	}
	// 4. the head posts a repair job; two NPC labourers work it (8500 damage: 9 shifts)
	if _, err := rrc(l.village.LaborPost(ctx, l.as(l.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: camp, N: "repair"})); err != nil {
		t.Fatal(err)
	}
	w := panel()
	if w.Condition == nil || w.Condition.RepairJob == nil || w.Condition.RepairJob.ShiftsLeft != 10 {
		t.Fatalf("the repair job should be open with 10 shifts: %+v", w.Condition)
	}
	if _, err := rrc(l.village.LaborHire(ctx, l.as(l.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: w.Condition.RepairJob.ID, N: "2"})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if len(l.workingShifts(camp)) == 0 {
			break
		}
		finish()
	}
	var damage int64
	if err := l.pool.Raw().QueryRow(ctx, `SELECT damage_bps FROM settlement_buildings WHERE id = $1::uuid`, camp).Scan(&damage); err != nil {
		t.Fatal(err)
	}
	if damage != 0 {
		t.Errorf("nine repair shifts restore the camp, damage is %d", damage)
	}
	if open := l.scalar(`SELECT count(*) FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'repair' AND status = 'open'`, camp); open != 0 {
		t.Errorf("a whole camp has no open repair job: %d", open)
	}
	if work() {
		t.Error("a repaired camp works")
	}
	v, err := postgres.NewEconomyAdmin(l.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v.VillageInvariants.WorkNodesOK() || v.VillageInvariants.LaborMismatched != 0 {
		t.Errorf("the working-node invariants broke: %+v", v.VillageInvariants)
	}
}

// The founding bootstrap (roadmap 2.2 phase 6, rule 2: no dead ends): from a freshly founded
// city with nothing but its grant, the head can buy food from the neutral city, a workplace
// can be worked by a hired NPC crew that eats it, and the goods land in the stock.
func TestAFoundedCityBootstrapsFoodAndAHiredCrew(t *testing.T) {
	l := newLaborEnv(t)
	ctx := testCtx(t)
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_material_purchases WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, l.cityID)
			}
			if _, err := l.pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	camp := newUUID(t)
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'woodcutter_camp', 81, 3, 'complete', now(), now())`, camp, l.cityID); err != nil {
		t.Fatal(err)
	}
	// 1. the head buys wheat from the neutral city
	buy := l.as(l.head, "settlement.materials.buy", "materials.buy")
	bought, err := rrc(l.village.MaterialsBuy(ctx, buy, handlers.VillageMaterialRequest{Item: "wheat", Qty: "10", Confirm: vpres.MaterialsConfirm}))
	if err != nil {
		t.Fatalf("buying food: %v", err)
	}
	if l.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = 'wheat'`, l.cityID) != 10 {
		t.Fatalf("the head's purchase of wheat did not land:\n%s", bought.Text)
	}
	// 2. the head posts the camp's job and hires two labourers: they eat and cut
	if _, err := rrc(l.village.LaborPost(ctx, l.as(l.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: camp})); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := l.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'production' AND status = 'open'`, camp).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(l.village.LaborHire(ctx, l.as(l.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "2"})); err != nil {
		t.Fatal(err)
	}
	if got := l.scalar(`SELECT count(*) FROM settlement_shifts WHERE building_id = $1::uuid AND worker_kind = 'npc' AND status = 'working'`, camp); got != 2 {
		t.Fatalf("two hired labourers should be working with the bought food, %d are", got)
	}
	if got := l.scalar(`SELECT COALESCE(SUM(eaten_points), 0)::bigint FROM settlement_kitchen WHERE settlement_id = $1::uuid`, l.cityID); got != 2 {
		t.Errorf("two one-hour shifts eat two points: %d", got)
	}
	l.clock.Advance(time.Hour)
	for _, s := range l.workingShifts(camp) {
		l.end(s)
	}
	var timber int64
	if err := l.pool.Raw().QueryRow(ctx, `SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid AND item_code = 'timber'`, l.cityID).Scan(&timber); err != nil {
		t.Fatal(err)
	}
	if timber == 0 {
		t.Error("the crew's timber is in the stock")
	}
}

// Crews, not a label, are the capacity (ADR 0044 4.1): a settlement may raise as many buildings
// at once as its homes give, and one more for each workshop of a builder trade that has its
// post filled; a workshop nobody works in adds nothing.
func TestAStaffedBuilderWorkshopRaisesTheBuildCap(t *testing.T) {
	l := newLaborEnv(t)
	ctx := testCtx(t)
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`DELETE FROM settlement_meals WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_kitchen WHERE settlement_id = $1::uuid`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, l.cityID)
			}
			if _, err := l.pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	menuCap := func() int {
		t.Helper()
		resp, err := rrc(l.village.BuildMenu(ctx, l.as(l.head, "settlement.build.menu", "build.menu")))
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.BuildMenuView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatal(err)
		}
		return v.ConcurrentCap
	}
	floor := menuCap()
	shop := newUUID(t)
	if _, err := l.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
	    VALUES ($1::uuid, $2::uuid, 'carpentry_workshop', 82, 3, 'complete', now(), now())`, shop, l.cityID); err != nil {
		t.Fatal(err)
	}
	if got := menuCap(); got != floor {
		t.Errorf("an unstaffed workshop adds no crew: cap %d, floor %d", got, floor)
	}
	// stock and a hired crew: the workshop is staffed
	if err := postgres.NewUnitOfWork(l.pool, testDefaultLanguage).Do(ctx, func(ctx context.Context, tx application.Tx) error {
		for item, qty := range map[string]int64{"wheat": 10, "timber": 9} {
			if err := tx.Items().Move(ctx, application.ItemMove{
				ID: newUUID(t), Item: item, Qty: qty, ToOrg: application.SettlementOrg(l.cityID), ToHolding: application.HoldWarehouse,
				Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(t), At: time.Now().UTC(),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(l.village.LaborPost(ctx, l.as(l.head, "settlement.labor.post", "labor.post"), handlers.VillageLaborRequest{ID: shop})); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := l.pool.Raw().QueryRow(ctx, `SELECT id::text FROM labor_jobs WHERE building_id = $1::uuid AND kind = 'production' AND status = 'open'`, shop).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := rrc(l.village.LaborHire(ctx, l.as(l.head, "settlement.labor.hire", "labor.hire"), handlers.VillageLaborRequest{ID: jobID, N: "1"})); err != nil {
		t.Fatal(err)
	}
	if got := menuCap(); got != floor+1 {
		t.Errorf("a staffed carpentry workshop is one more crew: cap %d, want %d", got, floor+1)
	}
}

//go:build integration

// Integration tests of war (migration 0022,
// docs/adr/0022-military-and-diplomacy.md part two), through the real
// handlers, unit of work, ledger, item journal and policy resolver:
//
//	the two countries sign a pact of non-aggression; the Commonwealth's
//	president declares war on the Federation — once, on a double press — and
//	the declaration breaks the pact; no operation flies before the notice;
//	stealth fighters strike Calderis, defended by a long-wave early-warning
//	radar and a long-range battery: the operation resolves once on a double
//	delivery, from the seed its id gives, and the battery saw the fighters
//	exactly as far as the radar equation says; a ballistic salvo saturates
//	the battery and knocks the defences out; the surviving fighters strike
//	the city, which is damaged — the border and the city close — and heals on
//	the game clock; a war levy is paid into the defence fund; tanks take the
//	city, once: its jurisdiction moves to the Commonwealth, its mayor's seat
//	is emptied, the commander who took it governs it, its residents stay;
//	a missile salvo is launched, a ceasefire is agreed once, and the salvo is
//	called off when it arrives, nothing spent.
//
// The ledger's, the journal's and war's invariants hold at the end, and
// everything the test made is removed — the city goes back to its country.
package tests

import (
	"context"
	"hash/fnv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// requireWar skips when migration 0022 is missing.
func requireWar(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t), `SELECT to_regclass('public.war_operations') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("war_operations does not exist; apply migration 0022 first")
	}
}

// purgeWar removes everything war holds for the countries — occupations
// undone first, so every city is back under its content's country — and
// every piece the states ever held, lost ones included, with the designs
// and the company the test made.
func purgeWar(t *testing.T, pool *postgres.Pool, countries []string, companyID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, step := range []struct {
		sql string
		arg any
	}{
		{`UPDATE jurisdictions j SET parent_id = cc.de_jure_country_id
		    FROM city_control cc JOIN cities c ON c.id = cc.city_id WHERE j.id = c.jurisdiction_id`, nil},
		{`DELETE FROM city_control`, nil},
		{`ALTER TABLE war_events DISABLE TRIGGER war_events_append_only`, nil},
		{`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`, nil},
		{`CREATE TEMP TABLE purge_war ON COMMIT DROP AS SELECT id FROM wars
		   WHERE attacker_id = ANY($1::uuid[]) OR defender_id = ANY($1::uuid[])`, countries},
		{`CREATE TEMP TABLE purge_wpiece ON COMMIT DROP AS SELECT piece_id AS id FROM military_assets
		   WHERE country_id = ANY($1::uuid[])`, countries},
		{`DELETE FROM war_events WHERE war_id IN (SELECT id FROM purge_war)`, nil},
		{`DELETE FROM military_assets WHERE piece_id IN (SELECT id FROM purge_wpiece)`, nil},
		{`DELETE FROM item_movements WHERE piece_id IN (SELECT id FROM purge_wpiece)`, nil},
		{`DELETE FROM item_pieces WHERE id IN (SELECT id FROM purge_wpiece)`, nil},
		{`CREATE TEMP TABLE purge_waction ON COMMIT DROP AS SELECT game_action_id AS id FROM war_operations
		   WHERE war_id IN (SELECT id FROM purge_war)`, nil},
		{`DELETE FROM war_operations WHERE war_id IN (SELECT id FROM purge_war)`, nil},
		{`DELETE FROM game_actions WHERE id IN (SELECT id FROM purge_waction)`, nil},
		{`DELETE FROM war_proposals WHERE war_id IN (SELECT id FROM purge_war)`, nil},
		{`DELETE FROM war_parties WHERE war_id IN (SELECT id FROM purge_war)`, nil},
		{`DELETE FROM wars WHERE id IN (SELECT id FROM purge_war)`, nil},
		{`DELETE FROM city_war_damage`, nil},
		{`DELETE FROM product_designs WHERE company_id::text = $1`, companyID},
		{`DELETE FROM companies WHERE id::text = $1`, companyID},
		{`DELETE FROM outbox WHERE subject LIKE 'game.event.war.%'`, nil},
		{`ALTER TABLE war_events ENABLE TRIGGER war_events_append_only`, nil},
		{`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`, nil},
	} {
		var args []any
		if step.arg != nil {
			args = []any{step.arg}
		}
		if _, err := tx.Exec(ctx, step.sql, args...); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(step.sql), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}

// warDesign records a final design of a good, as a company's studio would.
func warDesign(t *testing.T, pool *postgres.Pool, companyID, item, archetype, fills string) string {
	t.Helper()
	var id string
	if err := pool.Raw().QueryRow(testCtx(t), `INSERT INTO product_designs (id, company_id, item_code, archetype, name, name_key,
	        origin, status, fills, created_at, updated_at, finalized_at)
	  VALUES (gen_random_uuid(), $1::uuid, $2, $3, $2, $2, 'authored', 'final', $4::jsonb, now(), now(), now())
	  RETURNING id::text`, companyID, item, archetype, fills).Scan(&id); err != nil {
		t.Fatalf("design %s: %v", item, err)
	}
	return id
}

// grantState gives a state n pieces of a design, stationed in a city: an
// operator's grant, recorded in the item journal like any origin.
func grantState(t *testing.T, pool *postgres.Pool, country, item, archetype, designID, class, branch, cityID string, n int) {
	t.Helper()
	ctx := testCtx(t)
	for range n {
		var id string
		if err := pool.Raw().QueryRow(ctx, `INSERT INTO item_pieces (id, serial, item_code, archetype, quality, uses_left,
		        holding, origin, origin_ref, created_at, org_kind, org_id, design_id)
		  VALUES (gen_random_uuid(), 'WAR-' || $5, $1, $2, 60, 0, 'warehouse', 'grant', gen_random_uuid(), now(), 'state',
		          $3::uuid, $4::uuid) RETURNING id::text`, item, archetype, country, designID, randomToken(t, 12)).Scan(&id); err != nil {
			t.Fatalf("granting %s: %v", item, err)
		}
		if _, err := pool.Raw().Exec(ctx, `INSERT INTO item_movements (id, item_code, piece_id, quantity, to_org_kind, to_org,
		        to_holding, reason, created_at)
		  VALUES (gen_random_uuid(), $1, $2::uuid, 1, 'state', $3::uuid, 'warehouse', 'grant', now())`, item, id, country); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Raw().Exec(ctx, `INSERT INTO military_assets (piece_id, country_id, branch, class_code, status,
		        garrison_city_id, acquired_at, updated_at)
		  VALUES ($1::uuid, $2::uuid, $3, $4, 'stationed', $5::uuid, now(), now())`, id, country, branch, class, cityID); err != nil {
			t.Fatal(err)
		}
	}
}

// unpost removes one ledger transaction the test posted, reversing it on
// every balance.
func unpost(t *testing.T, pool *postgres.Pool, txID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	tx, err := pool.Raw().Begin(ctx)
	if err != nil {
		t.Errorf("cleanup: begin: %v", err)
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, sql := range []string{
		`ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_append_only`,
		`UPDATE accounts a SET balance = a.balance - d.delta
		   FROM (SELECT account_id, SUM(amount)::bigint AS delta FROM ledger_entries WHERE transaction_id = $1::uuid
		          GROUP BY account_id) d
		  WHERE a.id = d.account_id`,
		`DELETE FROM ledger_entries WHERE transaction_id = $1::uuid`,
		`ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_append_only`,
	} {
		var args []any
		if strings.Contains(sql, "$1") {
			args = []any{txID}
		}
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Errorf("cleanup %q: %v", firstLine(sql), err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("cleanup: commit: %v", err)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestWarDeclaredFoughtTakenAndSuspended(t *testing.T) {
	pool := requirePostgres(t)
	requireLedger(t, pool)
	home, far := requireMilitary(t, pool)
	requireWar(t, pool)
	registry := companyRegistry(t, pool)
	snap := registry.Current()
	if _, ok := snap.War(); !ok {
		t.Skip("the active content has no war section; run `admin content load`")
	}
	ctx := testCtx(t)
	cities := postgres.NewCityRepository(pool)
	kessmoor, err := cities.ByCode(ctx, "kessmoor")
	if err != nil {
		t.Skip(err)
	}
	calderis, err := cities.ByCode(ctx, "calderis")
	if err != nil {
		t.Skip(err)
	}
	vantorReach, err := cities.ByCode(ctx, "vantor_reach")
	if err != nil {
		t.Skip(err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM wars`); n != 0 {
		t.Skipf("the database holds %d wars; run this test on one without", n)
	}

	homePresident, airCmdr, groundCmdr, owner := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	farPresident, mayor, resident, stranger := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	for p, city := range map[*application.Player]string{homePresident: kessmoor.ID, airCmdr: kessmoor.ID,
		groundCmdr: kessmoor.ID, owner: kessmoor.ID, stranger: kessmoor.ID, farPresident: calderis.ID, mayor: calderis.ID,
		resident: calderis.ID} {
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $2::uuid, residence_city_id = $2::uuid WHERE id = $1::uuid`,
			p.ID, city); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{homePresident.ID, airCmdr.ID, groundCmdr.ID, owner.ID, farPresident.ID, mayor.ID, resident.ID, stranger.ID}

	// A defence company's designs: a stealth fighter, a bomb, a ballistic
	// missile and a tank for the Commonwealth; a long-range battery and a
	// long-wave early-warning radar for the Federation.
	var companyID string
	if err := pool.Raw().QueryRow(ctx, `INSERT INTO companies (id, code, name, name_key, type_code, city_id, owner_player_id,
	        status, price_bps, total_shares, registration_fee, content_version, founded_at, updated_at, closed_at, close_reason)
	  VALUES (gen_random_uuid(), upper($2), 'War Works ' || $2, 'war works ' || $2, 'aerospace', $1::uuid, $3::uuid, 'dissolved',
	          10000, 1000, 0, 1, now(), now(), now(), 'closed') RETURNING id::text`,
		kessmoor.ID, randomToken(t, 7), owner.ID).Scan(&companyID); err != nil {
		t.Fatal(err)
	}
	purgeMilitary(t, pool, []string{home, far}, nil)
	purgeWar(t, pool, []string{home, far}, "")
	// revenueTx is the city revenue the war levy is paid from: removed
	// after the levies on it are.
	var revenueTx string
	t.Cleanup(func() {
		purgeWar(t, pool, []string{home, far}, companyID)
		purgeMilitary(t, pool, []string{home, far}, ids)
		if revenueTx != "" {
			unpost(t, pool, revenueTx)
		}
	})
	fill := func(slots ...string) string {
		out := "{"
		for i := 0; i < len(slots); i += 2 {
			if i > 0 {
				out += ","
			}
			out += `"` + slots[i] + `":{"component":"` + slots[i+1] + `","quantity":1}`
		}
		return out + "}"
	}
	stealthDesign := warDesign(t, pool, companyID, "stealth_fighter_jet", "stealth_fighter",
		fill("airframe", "stealth_airframe", "coating", "ram_coating", "engine", "turbofan", "radar", "fire_control_radar"))
	bombDesign := warDesign(t, pool, companyID, "guided_bomb", "guided_bomb",
		fill("warhead", "blast_warhead", "guidance", "precision_guidance"))
	missileDesign := warDesign(t, pool, companyID, "ballistic_missile", "ballistic_missile",
		fill("motor", "two_stage_motor", "warhead", "blast_warhead", "guidance", "precision_guidance"))
	tankDesign := warDesign(t, pool, companyID, "main_battle_tank", "battle_tank",
		fill("hull", "tank_hull", "gun", "tank_gun", "drive", "powerpack", "fcs", "fire_control_computer"))
	samDesign := warDesign(t, pool, companyID, "long_range_sam", "air_defence_long",
		fill("radar", "fire_control_radar", "interceptor", "long_interceptor", "carrier", "light_hull"))
	radarDesign := warDesign(t, pool, companyID, "early_warning_radar", "radar_station",
		fill("radar", "vhf_array", "carrier", "light_hull", "drive", "powerpack"))
	grantState(t, pool, home, "stealth_fighter_jet", "stealth_fighter", stealthDesign, "stealth_fighter", "air", kessmoor.ID, 3)
	grantState(t, pool, home, "guided_bomb", "guided_bomb", bombDesign, "bomb", "air", kessmoor.ID, 12)
	grantState(t, pool, home, "ballistic_missile", "ballistic_missile", missileDesign, "ballistic", "ground", kessmoor.ID, 16)
	grantState(t, pool, home, "main_battle_tank", "battle_tank", tankDesign, "tank", "ground", kessmoor.ID, 8)
	grantState(t, pool, far, "long_range_sam", "air_defence_long", samDesign, "sam_long", "air_defence", calderis.ID, 1)
	grantState(t, pool, far, "early_warning_radar", "radar_station", radarDesign, "radar", "air_defence", calderis.ID, 1)

	clock := &testClock{now: time.Now().UTC()}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	policy := postgres.NewPolicyReader(pool, clock.Now)
	scale := gametime.Scale(gameScale)
	wars := handlers.NewWarHandler(uow, workIDs{t}, nil, registry, cities, policy, scale, handlers.WarRules{
		DeclarationNotice: time.Hour, ProposalTTL: 48 * time.Hour, EndedShownFor: 168 * time.Hour, BoardOperations: 8,
		NoticeCap: 50}, time.Hour, clock.Now)
	dip := handlers.NewDiplomacyHandler(uow, workIDs{t}, nil, registry, handlers.DiplomacyRules{
		SanctionNotice: time.Hour, SanctionMinDuration: 24 * time.Hour, TreatyOfferTTL: 72 * time.Hour,
		EndedShownFor: 168 * time.Hour, HistoryPageSize: 8}, time.Hour, clock.Now)
	forces := handlers.NewMilitaryHandler(uow, workIDs{t}, nil, registry, cities, policy, scale, handlers.MilitaryRules{
		Period: 24 * time.Hour, ReadinessLossBPS: 1000, ReadinessRecoveryBPS: 500, ReferenceRadarKM: 150}, time.Hour, clock.Now)
	travel := handlers.NewTravelHandler(uow, workIDs{t}, nil, cities, snapshotNetwork{snap}, policy, gameScale, 25, time.Hour,
		clock.Now)
	metaAs := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.PlayerID, m.Command, m.Language = p.TelegramUserID, p.ID, command, "en"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	scheduler := func(command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID, m.Command = 0, command
		return m
	}
	seatAs(t, uow, "president", home, homePresident, clock.Now())
	seatAs(t, uow, "president", far, farPresident, clock.Now())
	seatAs(t, uow, "air_force_commander", home, airCmdr, clock.Now())
	seatAs(t, uow, "ground_forces_commander", home, groundCmdr, clock.Now())
	seatAs(t, uow, "mayor", calderis.JurisdictionID, mayor, clock.Now())

	// --- A pact of non-aggression, then a declaration that breaks it. ------
	resp, err := dip.Propose(ctx, metaAs(homePresident, "diplomacy.propose"), handlers.DiplomacyRequest{Target: farCountryCode,
		Kind: "non_aggression", Confirm: "yes"})
	said(t, "propose a pact", resp, err)
	var pactNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM treaties WHERE kind = 'non_aggression' AND proposer_id = $1::uuid`,
		home).Scan(&pactNo); err != nil {
		t.Fatal(err)
	}
	resp, err = dip.Answer(ctx, metaAs(farPresident, "diplomacy.answer"), handlers.DiplomacyRequest{No: itoa(pactNo), Verdict: "accept"})
	said(t, "sign the pact", resp, err)

	resp, err = wars.Declare(ctx, metaAs(stranger, "war.declare"), handlers.WarRequest{Target: farCountryCode,
		Ground: "territorial_claim", Confirm: "yes"})
	said(t, "a citizen declaring war", resp, err, "war.refused.not_holder")
	declare := metaAs(homePresident, "war.declare")
	for range 2 {
		resp, err = wars.Declare(ctx, declare, handlers.WarRequest{Target: farCountryCode, Ground: "territorial_claim", Confirm: "yes"})
		said(t, "declare war", resp, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM wars WHERE attacker_id = $1::uuid`, home); n != 1 {
		t.Fatalf("wars declared = %d, want 1 on a double press", n)
	}
	var warNo int64
	var broke int
	if err := pool.Raw().QueryRow(ctx, `SELECT no, cardinality(broke_treaties) FROM wars WHERE attacker_id = $1::uuid`, home).Scan(
		&warNo, &broke); err != nil {
		t.Fatal(err)
	}
	var pactStatus string
	if err := pool.Raw().QueryRow(ctx, `SELECT status FROM treaties WHERE no = $1`, pactNo).Scan(&pactStatus); err != nil ||
		pactStatus != "terminated" || broke != 1 {
		t.Fatalf("the pact is %q (%v) and the war broke %d treaties; want it terminated by the declaration", pactStatus, err, broke)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.war.declared.v1'`); n != 1 {
		t.Errorf("war.declared announcements = %d, want 1", n)
	}
	launch := func(p *application.Player, req handlers.WarRequest) string {
		t.Helper()
		m := metaAs(p, "war.launch")
		var text string
		for range 2 {
			resp, err := wars.Launch(ctx, m, req)
			said(t, "launch "+req.Kind, resp, err)
			if text == "" {
				text = resp.Text
			}
		}
		return text
	}
	if text := launch(airCmdr, handlers.WarRequest{City: "calderis", Kind: "air", Class: "stealth_fighter", Objective: "city",
		Qty: "3", Confirm: "yes"}); !contains(text, "war.refused.not_yet") {
		t.Fatalf("a strike before the notice ran: %s", text)
	}
	clock.Advance(time.Hour + time.Second)

	// resolve delivers an operation's action twice, concurrently.
	type opRow struct {
		id, action, status string
		seed               int64
		strikes            time.Time
		lost, used, hits   int
		damage             int
		captured           bool
		seen               int64
	}
	lastOp := func() opRow {
		t.Helper()
		var o opRow
		if err := pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text, status, seed, strikes_at, attacker_lost,
		        munitions_used, hits, damage_bps, captured, COALESCE((report->>'seen_at_km')::bigint, 0)
		   FROM war_operations ORDER BY no DESC LIMIT 1`).Scan(&o.id, &o.action, &o.status, &o.seed, &o.strikes, &o.lost, &o.used,
			&o.hits, &o.damage, &o.captured, &o.seen); err != nil {
			t.Fatal(err)
		}
		return o
	}
	resolve := func() opRow {
		t.Helper()
		o := lastOp()
		if o.strikes.After(clock.Now()) {
			clock.Advance(o.strikes.Sub(clock.Now()) + time.Second)
		}
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := wars.Resolve(context.Background(), scheduler("war.resolve"), handlers.CrimeScheduledRequest{
					ActionID: o.action, ReferenceID: o.id}); err != nil {
					t.Errorf("resolve: %v", err)
				}
			}()
		}
		wg.Wait()
		return lastOp()
	}
	ready := func(class, cityID string) int {
		return countRows(t, pool, `SELECT count(*) FROM military_assets WHERE class_code = $1 AND garrison_city_id = $2::uuid
		  AND status = 'stationed' AND condition = 'ready'`, class, cityID)
	}

	// --- Stealth against radar, resolved once. -----------------------------
	launch(airCmdr, handlers.WarRequest{City: "calderis", Kind: "air", Class: "stealth_fighter", Objective: "city", Qty: "3",
		Confirm: "yes"})
	if n := countRows(t, pool, `SELECT count(*) FROM war_operations`); n != 1 {
		t.Fatalf("operations launched = %d, want 1 on a double press", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM military_assets WHERE status = 'committed'`); n != 3+6 {
		t.Fatalf("committed pieces = %d, want three fighters and the six bombs they carry", n)
	}
	strike := resolve()
	if strike.status != "resolved" {
		t.Fatalf("the strike is %s, want resolved", strike.status)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(strike.id))
	if strike.seed != int64(h.Sum64()>>1) {
		t.Errorf("the strike's seed %d is not the one its id gives", strike.seed)
	}
	// The stealth fighter: 0.02 m² airframe × 0.25 coating = 0.005 m²; the
	// long-wave array sees 1 m² at 350 km and enlarges the target twenty
	// times: 350 × (0.1)^¼ km.
	if want := military.DetectionRangeKM(350, 5, 200_000); strike.seen != want {
		t.Errorf("the defence first saw the stealth fighters at %d km, want %d (the radar equation)", strike.seen, want)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM item_movements WHERE reason = 'expended' AND reference_id = $1::uuid`,
		strike.id); n != 6 {
		t.Errorf("bombs spent = %d, want the six loaded, once", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM item_movements WHERE reason = 'destroyed' AND reference_id = $1::uuid`,
		strike.id); n != strike.lost {
		t.Errorf("fighters written off in the journal = %d, the operation lost %d", n, strike.lost)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.war.report.v1'
	  AND payload->>'player_id' = $1`, airCmdr.ID); n != 1 {
		t.Errorf("reports to the commander = %d, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.war.struck.v1'`); n != 1 {
		t.Errorf("strike announcements = %d, want 1", n)
	}

	// --- A ballistic salvo saturates the battery. ----------------------------
	launch(groundCmdr, handlers.WarRequest{City: "calderis", Kind: "missile", Class: "ballistic", Objective: "defences",
		Qty: "12", Confirm: "yes"})
	if salvo := resolve(); salvo.status != "resolved" {
		t.Fatalf("the salvo is %s", salvo.status)
	}
	if n := ready("sam_long", calderis.ID) + ready("radar", calderis.ID); n != 0 {
		t.Fatalf("%d air defence assets still stand after twelve ballistic missiles on two", n)
	}

	// --- The city struck, closed, and healed. --------------------------------
	survivors := ready("stealth_fighter", kessmoor.ID)
	if survivors == 0 {
		t.Fatal("no stealth fighter came home; the third is never engaged by a four-round battery")
	}
	launch(airCmdr, handlers.WarRequest{City: "calderis", Kind: "air", Class: "stealth_fighter", Objective: "city",
		Qty: itoa(int64(survivors)), Confirm: "yes"})
	if hit := resolve(); hit.damage <= 0 || hit.hits == 0 {
		t.Fatalf("an undefended city took %d bps from %d hits", hit.damage, hit.hits)
	}
	board := func(p *application.Player, country string) string {
		resp, err := wars.Board(ctx, metaAs(p, "war.board"), handlers.WarRequest{Country: country})
		said(t, "the war board", resp, err)
		return resp.Text
	}
	if text := board(farPresident, farCountryCode); !contains(text, "war.board.damaged_line") {
		t.Fatalf("the board shows no damaged city: %s", text)
	}
	resp, err = travel.Options(ctx, metaAs(stranger, "travel.options"), handlers.TravelOptionsRequest{City: "calderis"})
	said(t, "a journey across the front", resp, err, "war.blocked.border")
	// Two hundred game hours heal any damage the content allows.
	clock.Advance(200 * time.Minute)
	if text := board(farPresident, farCountryCode); contains(text, "war.board.damaged_line") {
		t.Fatalf("the city has not healed: %s", text)
	}

	// --- The war levy: a defence period at war pays the defence fund. ------
	resp, err = forces.Ministry(ctx, metaAs(homePresident, "military.ministry"), handlers.MilitaryRequest{})
	said(t, "the ministry", resp, err)
	ledger := postgres.NewLedgerRepository(pool)
	treasury, err := ledger.AccountFor(ctx, application.AccountCityTreasury, kessmoor.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Revenue inside the period, on the test's clock.
	revenue := transfer(application.SystemSourceAccountID, treasury.ID, 10_000, application.ReasonAdminGrant)
	revenue.CreatedAt = clock.Now()
	if revenueTx, err = ledger.Post(ctx, revenue); err != nil {
		t.Fatal(err)
	}
	var periodAction string
	var next time.Time
	if err := pool.Raw().QueryRow(ctx, `SELECT action_id::text, next_at FROM military_clocks WHERE country_id = $1::uuid`,
		home).Scan(&periodAction, &next); err != nil {
		t.Fatal(err)
	}
	if next.After(clock.Now()) {
		clock.Advance(next.Sub(clock.Now()) + time.Second)
	}
	if _, err := forces.Settle(ctx, scheduler("military.settle"), handlers.CrimeScheduledRequest{ActionID: periodAction,
		ReferenceID: home, Payload: []byte(`{"country_id":"` + home + `","period_no":1}`)}); err != nil {
		t.Fatal(err)
	}
	var warLevy int64
	if err := pool.Raw().QueryRow(ctx, `SELECT war_levy FROM military_periods WHERE country_id = $1::uuid AND period_no = 1`,
		home).Scan(&warLevy); err != nil || warLevy <= 0 {
		t.Fatalf("the war levy of a country at war = %d (%v), want some of Kessmoor's revenue", warLevy, err)
	}

	// --- Tanks take the city, once. -----------------------------------------
	launch(groundCmdr, handlers.WarRequest{City: "calderis", Kind: "ground", Class: "all", Confirm: "yes"})
	assault := resolve()
	if !assault.captured {
		t.Fatalf("eight tanks did not take an undefended city: %+v", assault)
	}
	var parent string
	if err := pool.Raw().QueryRow(ctx, `SELECT j.parent_id::text FROM cities c JOIN jurisdictions j ON j.id = c.jurisdiction_id
	  WHERE c.id = $1::uuid`, calderis.ID).Scan(&parent); err != nil || parent != home {
		t.Fatalf("Calderis stands under %s (%v), want the Commonwealth", parent, err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM war_events WHERE kind = 'captured' AND city_id = $1::uuid`, calderis.ID); n != 1 {
		t.Errorf("captures recorded = %d, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM city_control WHERE city_id = $1::uuid AND de_jure_country_id = $2::uuid`,
		calderis.ID, far); n != 1 {
		t.Errorf("occupations recorded = %d, want Calderis held from the Federation", n)
	}
	if holder(t, pool, "mayor", calderis.JurisdictionID) != "" {
		t.Error("the mayor of a taken city keeps the seat")
	}
	if holder(t, pool, "military_governor", calderis.JurisdictionID) != groundCmdr.ID {
		t.Error("the commander who took the city does not govern it")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM players WHERE id = $1::uuid AND residence_city_id = $2::uuid`,
		resident.ID, calderis.ID); n != 1 {
		t.Error("a resident of the taken city was moved")
	}
	if ready("tank", calderis.ID) == 0 {
		t.Error("no tank holds the city it took")
	}
	if n := countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = 'game.event.war.taken.v1'`); n != 1 {
		t.Errorf("announcements of the city taken = %d, want 1", n)
	}

	// --- A ceasefire calls off what is in the air. -------------------------
	launch(groundCmdr, handlers.WarRequest{City: "vantor_reach", Kind: "missile", Class: "ballistic", Objective: "city",
		Qty: "2", Confirm: "yes"})
	pending := lastOp()
	resp, err = wars.Propose(ctx, metaAs(farPresident, "war.propose"), handlers.WarRequest{No: itoa(warNo), Kind: "ceasefire",
		Confirm: "yes"})
	said(t, "propose a ceasefire", resp, err)
	var proposalNo int64
	if err := pool.Raw().QueryRow(ctx, `SELECT no FROM war_proposals WHERE kind = 'ceasefire'`).Scan(&proposalNo); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := wars.Answer(context.Background(), metaAs(homePresident, "war.answer"),
				handlers.WarRequest{No: itoa(proposalNo), Verdict: "accept"}); err != nil {
				t.Errorf("accept: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := countRows(t, pool, `SELECT count(*) FROM war_events WHERE kind = 'ceasefire'`); n != 1 {
		t.Fatalf("ceasefires recorded = %d, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM wars WHERE no = $1 AND status = 'ceasefire'`, warNo); n != 1 {
		t.Fatal("the war is not under a ceasefire")
	}
	if off := resolve(); off.id != pending.id || off.status != "called_off" {
		t.Fatalf("the salvo in the air is %s, want called off", off.status)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM item_movements WHERE reference_id = $1::uuid`, pending.id); n != 0 {
		t.Errorf("a called-off salvo spent %d missiles", n)
	}
	if text := launch(groundCmdr, handlers.WarRequest{City: vantorReach.Code, Kind: "missile", Class: "ballistic",
		Objective: "city", Qty: "1", Confirm: "yes"}); !contains(text, "war.refused") {
		t.Fatalf("a strike under a ceasefire: %s", text)
	}
	verifyLedger(t, pool)
}

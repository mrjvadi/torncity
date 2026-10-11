//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/raid"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
)

// Raids, falls and the settlement's insurance fund (docs/adr/0074, plan B10), through the real handlers, the unit of work, the ledger and
// the item journal. Two settlements are founded: the defender is the one the service-day env builds, the attacker a second group.

type raidEnv struct {
	*watchEnv
	rules   application.RaidRules
	scale   gametime.Scale
	attMeta envelope.Metadata
	att     *application.Player
	attCity string
	people  []string
}

func (e *raidEnv) cityName(id string) string {
	var n string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT name FROM cities WHERE id = $1::uuid`, id).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// newRaidEnv founds the attacker beside the defender and wires the raid rules: raids and falls started a day ago, a short fight that
// the side with more hands wins, the defaults of the config for everything else.
func newRaidEnv(t *testing.T, tune func(*application.RaidRules)) *raidEnv {
	t.Helper()
	w := newWatchEnv(t)
	cfg := config.Defaults()
	rules := settlementcfg.Raid(cfg.Raid)
	rules.RaidsFrom = w.clock.Now().Add(-24 * time.Hour)
	rules.FallsFrom = rules.RaidsFrom
	rules.Rounds, rules.BaseHitBPS, rules.HitFloorBPS, rules.HitCeilingBPS = 5, 9500, 9000, 9500
	if tune != nil {
		tune(&rules)
	}
	scale := gametime.Scale(cfg.Game.TimeScale)
	w.village.WithRaids(rules, scale)
	e := &raidEnv{watchEnv: w, rules: rules, scale: scale}

	meta, founder := w.group(t)
	foundVillage(t, w.pool, w.h, meta)
	if err := w.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&e.attCity); err != nil {
		t.Fatal(err)
	}
	e.attMeta, e.att = meta, founder
	seedTreasury(t, w.pool, e.attCity, 60_000)
	attCity, defCity := e.attCity, w.cityID
	t.Cleanup(func() { e.cleanup(attCity, defCity) })
	return e
}

// cleanup takes the raids, falls, insurers, sights and the money of both settlements away before the settlements go.
func (e *raidEnv) cleanup(cities ...string) {
	ctx := testCtx(e.t)
	for _, c := range cities {
		for _, stmt := range []string{
			`ALTER TABLE settlement_raid_loot DISABLE TRIGGER settlement_raid_loot_append_only`,
			`ALTER TABLE insurance_claims DISABLE TRIGGER insurance_claims_append_only`,
			`ALTER TABLE insurance_premiums DISABLE TRIGGER insurance_premiums_append_only`,
			`DELETE FROM game_actions WHERE reference_type = 'settlement_raids' AND reference_id IN (SELECT id FROM settlement_raids WHERE attacker_id = $1::uuid OR defender_id = $1::uuid)`,
			`DELETE FROM settlement_raid_loot WHERE raid_id IN (SELECT id FROM settlement_raids WHERE attacker_id = $1::uuid OR defender_id = $1::uuid)`,
			`DELETE FROM settlement_raid_detections WHERE raid_id IN (SELECT id FROM settlement_raids WHERE attacker_id = $1::uuid OR defender_id = $1::uuid)`,
			`DELETE FROM falls WHERE cause_ref IN (SELECT id FROM settlement_raids WHERE attacker_id = $1::uuid OR defender_id = $1::uuid)`,
			`DELETE FROM settlement_raid_participants WHERE raid_id IN (SELECT id FROM settlement_raids WHERE attacker_id = $1::uuid OR defender_id = $1::uuid)`,
			`DELETE FROM settlement_raids WHERE attacker_id = $1::uuid OR defender_id = $1::uuid`,
			`DELETE FROM building_sight WHERE building_id IN (SELECT id FROM settlement_buildings WHERE settlement_id = $1::uuid)`,
			`DELETE FROM insurance_claims WHERE policy_id IN (SELECT id FROM insurance_policies WHERE country_id = $1::uuid)`,
			`DELETE FROM insurance_premiums WHERE policy_id IN (SELECT id FROM insurance_policies WHERE country_id = $1::uuid)`,
			`DELETE FROM insurance_policies WHERE country_id = $1::uuid`,
			`DELETE FROM settlement_insurers WHERE settlement_id = $1::uuid`,
			`DELETE FROM service_days WHERE settlement_id = $1::uuid`,
			`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
			`DELETE FROM item_movements WHERE to_org = $1::uuid OR from_org = $1::uuid`,
			`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
			`DELETE FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`,
			`ALTER TABLE settlement_raid_loot ENABLE TRIGGER settlement_raid_loot_append_only`,
			`ALTER TABLE insurance_claims ENABLE TRIGGER insurance_claims_append_only`,
			`ALTER TABLE insurance_premiums ENABLE TRIGGER insurance_premiums_append_only`,
		} {
			var args []any
			if strings.Contains(stmt, "$1") {
				args = append(args, c)
			}
			if _, err := e.pool.Raw().Exec(ctx, stmt, args...); err != nil {
				e.t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		purgeLedgerFor(e.t, e.pool, c)
	}
	e.cleanPlayer(e.att.ID)
	e.cleanPlayer(e.head.ID)
	for _, id := range e.people {
		purgeLedgerFor(e.t, e.pool, id)
	}
}

// attacker is a resident of the attacker's settlement with cash in hand.
func (e *raidEnv) attacker(cash int64) *application.Player {
	e.t.Helper()
	return e.person(e.attCity, cash)
}

// defender is a resident of the defender's settlement with cash in hand.
func (e *raidEnv) defender(cash int64) *application.Player {
	e.t.Helper()
	return e.person(e.cityID, cash)
}

func (e *raidEnv) person(city string, cash int64) *application.Player {
	e.t.Helper()
	p := insertPlayer(e.t, e.pool)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `UPDATE players SET residence_city_id = $1::uuid WHERE id = $2::uuid`, city, p.ID); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { e.cleanPlayer(p.ID) })
	e.people = append(e.people, p.ID)
	if cash > 0 {
		grant(e.t, e.pool, application.AccountPlayerCash, p.ID, cash)
	}
	return p
}

// cleanPlayer takes the rows a raid and a fall leave on a player away (the ledger is purged last, by the env).
func (e *raidEnv) cleanPlayer(id string) {
	c := testCtx(e.t)
	for _, stmt := range []string{
		`ALTER TABLE insurance_claims DISABLE TRIGGER insurance_claims_append_only`,
		`ALTER TABLE insurance_premiums DISABLE TRIGGER insurance_premiums_append_only`,
		`DELETE FROM falls WHERE player_id = $1::uuid`,
		`DELETE FROM settlement_raid_participants WHERE player_id = $1::uuid`,
		`DELETE FROM insurance_claims WHERE player_id = $1::uuid`,
		`DELETE FROM insurance_premiums WHERE policy_id IN (SELECT id FROM insurance_policies WHERE player_id = $1::uuid)`,
		`DELETE FROM insurance_policies WHERE player_id = $1::uuid`,
		`DELETE FROM hospital_treatments WHERE player_id = $1::uuid`,
		`DELETE FROM game_actions WHERE actor_id = $1::uuid`,
		`DELETE FROM hospital_stays WHERE player_id = $1::uuid`,
		`ALTER TABLE item_movements DISABLE TRIGGER item_movements_append_only`,
		`DELETE FROM item_movements WHERE to_player = $1::uuid OR from_player = $1::uuid`,
		`ALTER TABLE item_movements ENABLE TRIGGER item_movements_append_only`,
		`DELETE FROM item_stacks WHERE player_id = $1::uuid`,
		`DELETE FROM player_stats WHERE player_id = $1::uuid`,
		`ALTER TABLE insurance_claims ENABLE TRIGGER insurance_claims_append_only`,
		`ALTER TABLE insurance_premiums ENABLE TRIGGER insurance_premiums_append_only`,
	} {
		var args []any
		if strings.Contains(stmt, "$1") {
			args = append(args, id)
		}
		_, _ = e.pool.Raw().Exec(c, stmt, args...)
	}
}

// asAttacker is the attacker's side asking, as a game client.
func (e *raidEnv) asAttacker(p *application.Player, command, action string) envelope.Metadata {
	m := clientMeta(asPlayer(e.attMeta, p), command, action)
	m.IdempotencyKey = "it-" + randomToken(e.t, 16)
	m.RequestID = "req_" + randomToken(e.t, 16)
	return m
}

// raidAs sends settlement.raid as a player of either side.
func (e *raidEnv) raidAs(p *application.Player, attacker bool, req handlers.VillageRaidRequest) (village.RaidView, *presentation.Response) {
	e.t.Helper()
	m := e.as(p, "settlement.raid", "raid")
	if attacker {
		m = e.asAttacker(p, "settlement.raid", "raid")
	}
	resp, err := rrc(e.village.Raid(testCtx(e.t), m, req))
	if err != nil {
		e.t.Fatal(err)
	}
	var v village.RaidView
	if len(resp.View) > 0 && resp.Screen == village.ScreenRaid {
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatal(err)
		}
	}
	return v, resp
}

// refusedWith checks the answer is the village refusal of a kind.
func raidRefused(t *testing.T, resp *presentation.Response, kind string) {
	t.Helper()
	if resp.Screen == village.ScreenRaid {
		t.Fatalf("want the refusal %s, got the raid screen", kind)
	}
	var v village.VillageRefusalView
	if err := presentation.DecodeView(resp.View, &v); err != nil || v.Kind != kind {
		t.Fatalf("want the refusal %s, got %q %v (%s)", kind, v.Kind, err, resp.Screen)
	}
}

// file files a raid from the attacker's head against the defender and returns its id.
func (e *raidEnv) file(lead string, raiders string) string {
	e.t.Helper()
	_, resp := e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: e.defenderCode(), N: lead, Raiders: raiders})
	if resp.Screen != village.ScreenRaid {
		e.t.Fatalf("the raid was refused: %s %s", resp.Screen, string(resp.View))
	}
	return e.lastRaid()
}

func (e *raidEnv) defenderCode() string {
	var code string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT code FROM cities WHERE id = $1::uuid`, e.cityID).Scan(&code); err != nil {
		e.t.Fatal(err)
	}
	return code
}

func (e *raidEnv) lastRaid() string {
	e.t.Helper()
	var id string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT id::text FROM settlement_raids WHERE attacker_id = $1::uuid ORDER BY filed_at DESC LIMIT 1`, e.attCity).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// action is the scheduled action of a raid of a type, for the scheduler's request.
func (e *raidEnv) actions(raidID, kind string) []handlers.RaidScheduledRequest {
	e.t.Helper()
	rows, err := e.pool.Raw().Query(testCtx(e.t), `SELECT id::text, payload FROM game_actions WHERE reference_type = 'settlement_raids' AND reference_id = $1::uuid AND action_type = $2 ORDER BY finish_at`, raidID, kind)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []handlers.RaidScheduledRequest
	for rows.Next() {
		var id string
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, handlers.RaidScheduledRequest{ActionID: id, ActorID: e.att.ID, ReferenceType: "settlement_raids", ReferenceID: raidID, Payload: payload})
	}
	return out
}

func (e *raidEnv) sched(command string) envelope.Metadata {
	m := validMeta(e.t)
	m.TelegramUserID, m.Command = 0, command
	return m
}

func (e *raidEnv) verify() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	if !v.Raids {
		e.t.Fatal("migration 0152 is not applied")
	}
	t := v.VillageInvariants
	if t.RaidFeeLedger != t.RaidFeeRows || t.RaidLootCashLedger != t.RaidLootCashRows || t.RaidLootItemJournal != t.RaidLootItemRows ||
		t.RaidTributeLedger != t.RaidTributeRows || t.FallCashLedger != t.FallCashRows || t.FallGoodsJournal != t.FallGoodsRows ||
		t.FallStrayLegs != 0 || t.RaidWithoutOutcome != 0 || t.RaidOrphans != 0 || t.FallMaxShareBPS > e.rules.Rules.LossCapBPS {
		e.t.Errorf("the raid invariants do not hold: %+v", t)
	}
	f := v.FinanceInvariants
	if f.FundBalances != f.FundFlows || f.Overpaid != 0 {
		e.t.Errorf("the insurance funds do not balance: %d = %d, overpaid %d", f.FundBalances, f.FundFlows, f.Overpaid)
	}
}

func (e *raidEnv) raidRow(id string) (status string, looted bool, outcome map[string]any) {
	e.t.Helper()
	var raw []byte
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT status, looted, outcome FROM settlement_raids WHERE id = $1::uuid`, id).Scan(&status, &looted, &raw); err != nil {
		e.t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &outcome)
	return
}

func (e *raidEnv) cash(p *application.Player) int64 {
	return e.scalar(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'player_cash' AND owner_id = $1::uuid`, p.ID)
}

func (e *raidEnv) cityCash(id string) int64 { return treasuryOf(e.t, e.pool, id) }

// carry puts goods in a player's hands.
func (e *raidEnv) carry(p *application.Player, item string, qty int64) {
	e.t.Helper()
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		return tx.Items().Move(ctx, application.ItemMove{ID: newUUID(e.t), Item: item, Qty: qty, To: p.ID, ToHolding: application.HoldCarried,
			Reason: application.ItemGrant, ReferenceType: "test", ReferenceID: newUUID(e.t), At: e.clock.Now()})
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *raidEnv) carried(p *application.Player, item string) int64 {
	return e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM item_stacks WHERE player_id = $1::uuid AND item_code = $2 AND holding = 'carried'`, p.ID, item)
}

// resolve plays the scheduler: the window opens and ends.
func (e *raidEnv) resolve(raidID string) {
	e.t.Helper()
	ctx := testCtx(e.t)
	e.clock.Advance(3 * time.Hour)
	for _, a := range e.actions(raidID, application.RaidOpenActionType) {
		if _, err := e.village.RaidOpen(ctx, e.sched("raid.open"), a); err != nil {
			e.t.Fatal(err)
		}
	}
	e.clock.Advance(time.Hour)
	for _, a := range e.actions(raidID, application.RaidResolveActionType) {
		if _, err := e.village.RaidResolve(ctx, e.sched("raid.resolve"), a); err != nil {
			e.t.Fatal(err)
		}
	}
}

// A raid is a charter act with a fee, a start and limits; a player who does not hold raid.declare cannot file it.
func TestRaidFilingNeedsThePermissionAndKeepsItsLimits(t *testing.T) {
	e := newRaidEnv(t, nil)
	stranger := e.attacker(0)
	_, resp := e.raidAs(stranger, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: e.defenderCode()})
	raidRefused(t, resp, village.VillageNotOfficeHolder)

	// the lead has bounds
	_, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: e.defenderCode(), N: "0"})
	raidRefused(t, resp, village.RaidLeadShort)
	_, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: e.defenderCode(), N: "49"})
	raidRefused(t, resp, village.RaidLeadLong)
	_, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: "nowhere"})
	raidRefused(t, resp, village.RaidNoTarget)

	fee0, treasury0 := e.sink(), e.cityCash(e.attCity)
	id := e.file("3", "2")
	if got := treasury0 - e.cityCash(e.attCity); got != e.rules.Rules.Fee+2*e.rules.NPCRaiderWage {
		t.Errorf("the attacker paid %d, want the fee %d and two raiders' wages", got, e.rules.Rules.Fee+2*e.rules.NPCRaiderWage)
	}
	if got := e.sink() - fee0; got != e.rules.Rules.Fee+2*e.rules.NPCRaiderWage {
		t.Errorf("the sink took %d", got)
	}
	if status, _, _ := e.raidRow(id); status != "filed" {
		t.Errorf("status %s", status)
	}
	if len(e.actions(id, application.RaidOpenActionType)) != 1 || len(e.actions(id, application.RaidResolveActionType)) != 1 {
		t.Errorf("a raid is two scheduled actions")
	}

	// cooldown: 72 hours between the same two
	_, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: e.defenderCode(), N: "3"})
	raidRefused(t, resp, village.RaidCooldown)
	e.verify()
}

// At most three raids a week on one defender, from anyone, and the cooldown is for the pair.
func TestRaidWeeklyLimit(t *testing.T) {
	e := newRaidEnv(t, nil)
	// three raids of the last week (older than the cooldown) already stand against the defender
	for i := 1; i <= 3; i++ {
		filed := e.clock.Now().Add(-time.Duration(80+i*10) * time.Hour)
		if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_raids (id, attacker_id, defender_id, filed_by, filed_at, start_at, end_at, status, seed, fee, resolved_at, outcome)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, 'resolved', 1, 0, $7, '{}')`,
			newUUID(t), e.attCity, e.cityID, e.att.ID, filed, filed.Add(2*time.Hour), filed.Add(3*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	_, resp := e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: e.defenderCode(), N: "3"})
	raidRefused(t, resp, village.RaidWeekly)
	// a week later the oldest has left the week
	e.clock.Advance(3 * 24 * time.Hour)
	if _, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: e.defenderCode(), N: "3"}); resp.Screen != village.ScreenRaid {
		t.Fatalf("after the week: %s", string(resp.View))
	}
}

// A raid that prevails takes a capped share of the treasury and each stack, once, however many replicas run its end; the loot is in the
// ledger, the item journal and the rows alike.
func TestRaidLootIsCappedAndTakenOnceByAnyNumberOfReplicas(t *testing.T) {
	e := newRaidEnv(t, nil)
	e.stock("firewood", 400)
	e.stock("timber", 100)
	treasury0, att0 := e.cityCash(e.cityID), e.cityCash(e.attCity)
	id := e.file("3", "20")
	paid := e.rules.Rules.Fee + 20*e.rules.NPCRaiderWage
	att0 -= paid

	// open and end at once from six replicas, twice over
	e.clock.Advance(3 * time.Hour)
	ctx := testCtx(t)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, a := range e.actions(id, application.RaidOpenActionType) {
				if _, err := e.village.RaidOpen(ctx, e.sched("raid.open"), a); err != nil {
					t.Errorf("open: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	e.clock.Advance(time.Hour)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, a := range e.actions(id, application.RaidResolveActionType) {
				if _, err := e.village.RaidResolve(ctx, e.sched("raid.resolve"), a); err != nil {
					t.Errorf("resolve: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	// a redelivery of the same action and a late second action change nothing
	for _, a := range e.actions(id, application.RaidResolveActionType) {
		if _, err := e.village.RaidResolve(ctx, e.sched("raid.resolve"), a); err != nil {
			t.Fatal(err)
		}
	}

	status, looted, out := e.raidRow(id)
	if status != "resolved" || !looted || out["taken"] != true {
		t.Fatalf("the raid should have prevailed against an undefended town: %s %v %v", status, looted, out)
	}
	wantCash := treasury0 * e.rules.Rules.LootTreasuryBPS / 10_000
	if got := e.cityCash(e.attCity) - att0; got != wantCash {
		t.Errorf("the attacker took %d of the treasury, want exactly %d (3 percent), once", got, wantCash)
	}
	if got := treasury0 - e.cityCash(e.cityID); got != wantCash {
		t.Errorf("the defender lost %d, want %d", got, wantCash)
	}
	if got := e.scalar(`SELECT count(*) FROM settlement_raid_loot WHERE raid_id = $1::uuid AND kind = 'cash'`, id); got != 1 {
		t.Errorf("%d cash lines", got)
	}
	// each stack gave at most a tenth, and what the raiders could haul (20 hands, 40 each)
	if gone := 400 - e.held("firewood"); gone > 40 || gone < 1 {
		t.Errorf("firewood taken: %d of 400 (at most 40)", gone)
	}
	if gone := 100 - e.held("timber"); gone > 10 || gone < 1 {
		t.Errorf("timber taken: %d of 100 (at most 10)", gone)
	}
	if e.scalar(`SELECT COALESCE(SUM(quantity), 0)::bigint FROM org_stacks WHERE org_kind = 'settlement' AND org_id = $1::uuid`, e.attCity) != 400-e.held("firewood")+100-e.held("timber") {
		t.Errorf("the attacker's store holds what the defender lost")
	}
	e.verify()
}

// A defender may buy a raid off before it opens: the offer is the defender's, the acceptance the attacker's, the money moves once and the
// raid ends with nothing taken; the fee stays spent.
func TestRaidTributeEndsTheRaid(t *testing.T) {
	e := newRaidEnv(t, nil)
	id := e.file("3", "5")
	def0, att0 := e.cityCash(e.cityID), e.cityCash(e.attCity)

	_, resp := e.raidAs(e.head, false, handlers.VillageRaidRequest{Action: village.RaidActionOffer, Code: id, N: "10"})
	raidRefused(t, resp, village.RaidTributeLow)
	_, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionAccept, Code: id})
	raidRefused(t, resp, village.RaidNoOffer)
	if _, resp = e.raidAs(e.head, false, handlers.VillageRaidRequest{Action: village.RaidActionOffer, Code: id, N: "400"}); resp.Screen != village.ScreenRaid {
		t.Fatalf("the offer: %s", string(resp.View))
	}
	if e.cityCash(e.cityID) != def0 {
		t.Error("an offer moves no money")
	}
	// the defender cannot accept his own offer
	_, resp = e.raidAs(e.head, false, handlers.VillageRaidRequest{Action: village.RaidActionAccept, Code: id})
	raidRefused(t, resp, village.RaidSide)
	if _, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionAccept, Code: id}); resp.Screen != village.ScreenRaid {
		t.Fatalf("the acceptance: %s", string(resp.View))
	}
	if def0-e.cityCash(e.cityID) != 400 || e.cityCash(e.attCity)-att0 != 400 {
		t.Errorf("tribute moved %d and %d", def0-e.cityCash(e.cityID), e.cityCash(e.attCity)-att0)
	}
	if status, looted, _ := e.raidRow(id); status != "settled" || looted {
		t.Errorf("status %s looted %v", status, looted)
	}
	// the raid's own actions find it over and take nothing
	e.resolve(id)
	if e.cityCash(e.cityID) != def0-400 {
		t.Error("a settled raid was resolved anyway")
	}
	e.verify()
}

// Only players who chose to take part can fall; one who fell loses a capped share of what he carries, is taken to hospital, and the loss
// goes across the line; the one beside him who stood aside is untouched.
func TestOnlyThoseWhoTookPartFallAndTheLossIsCapped(t *testing.T) {
	e := newRaidEnv(t, nil)
	stands := e.defender(10_000) // carries 10 000, all his assets: the cap is 15 percent, 1 500
	aside := e.defender(10_000)  // stays out
	e.carry(stands, "firewood", 40)
	id := e.file("3", "20")
	e.raidAs(stands, false, handlers.VillageRaidRequest{Action: village.RaidActionStand, Code: id})
	e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionJoin, Code: id})
	att0 := e.cityCash(e.attCity)

	e.resolve(id)

	var fell int
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT count(*) FROM falls WHERE player_id = $1::uuid`, stands.ID).Scan(&fell); err != nil || fell != 1 {
		t.Fatalf("the player who stood should have fallen once: %d %v", fell, err)
	}
	if n := e.scalar(`SELECT count(*) FROM falls WHERE player_id = $1::uuid`, aside.ID); n != 0 {
		t.Errorf("a player who stood aside fell")
	}
	if e.cash(aside) != 10_000 {
		t.Errorf("the one who stood aside lost money: %d", e.cash(aside))
	}
	if n := e.scalar(`SELECT count(*) FROM hospital_stays WHERE player_id = $1::uuid AND cause = 'raid'`, stands.ID); n != 1 {
		t.Errorf("the fallen should be in hospital: %d stays", n)
	}
	if n := e.scalar(`SELECT count(*) FROM hospital_stays WHERE player_id = $1::uuid`, aside.ID); n != 0 {
		t.Errorf("the one who stood aside was hurt")
	}
	lost := 10_000 - e.cash(stands)
	var netAssets, worth int64
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT net_assets, lost_worth FROM falls WHERE player_id = $1::uuid`, stands.ID).Scan(&netAssets, &worth); err != nil {
		t.Fatal(err)
	}
	if worth*10_000 > netAssets*e.rules.Rules.LossCapBPS {
		t.Errorf("the fall took %d of %d, over the cap of %d bps", worth, netAssets, e.rules.Rules.LossCapBPS)
	}
	if lost <= 0 || lost > 2_000 {
		t.Errorf("a fall costs at most a fifth of the carried cash: lost %d", lost)
	}
	if e.cityCash(e.attCity)-att0 < lost {
		t.Errorf("the loss did not go to the settlement across the line")
	}
	if e.carried(stands, "firewood") >= 40 {
		t.Errorf("the carried goods lost nothing")
	}
	// a second fall inside the cooldown costs the stay and nothing more: a second raid, open now, with the same man standing in it
	cash1 := e.cash(stands)
	e.clock.Advance(time.Hour)
	second := newUUID(t)
	now := e.clock.Now()
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_raids (id, attacker_id, defender_id, filed_by, filed_at, start_at, end_at, status, seed, fee, outcome)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, 'open', 7, 0, '{"raiders":20,"transport":"foot"}')`,
		second, e.attCity, e.cityID, e.att.ID, now.Add(-3*time.Hour), now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_raid_participants (raid_id, player_id, side, joined_at) VALUES ($1::uuid, $2::uuid, 'defender', now())`, second, stands.ID); err != nil {
		t.Fatal(err)
	}
	// he has left hospital by then
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE hospital_stays SET status = 'discharged', discharged_at = now() WHERE player_id = $1::uuid`, stands.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.village.RaidResolve(testCtx(t), e.sched("raid.resolve"), handlers.RaidScheduledRequest{ActionID: newUUID(t), ActorID: e.att.ID, ReferenceID: second}); err != nil {
		t.Fatal(err)
	}
	if n := e.scalar(`SELECT count(*) FROM falls WHERE player_id = $1::uuid AND cooling AND lost_cash = 0`, stands.ID); n != 1 {
		t.Errorf("the second fall inside the cooldown should cost no assets: %d such falls", n)
	}
	if e.cash(stands) != cash1 {
		t.Errorf("a fall inside the cooldown took cash: %d to %d", cash1, e.cash(stands))
	}
	e.verify()
}

// The settlement's own fund: a resident buys cover, a fall is paid out of the fund once and, when the fund is short, every claim gets the
// same share of what is due.
func TestMutualInsurancePaysAFallOnceAndProRataWhenShort(t *testing.T) {
	e := newRaidEnv(t, nil)
	a := e.defender(10_000)
	b := e.defender(2_000)
	ins := func(p *application.Player, req handlers.VillageInsuranceRequest) *presentation.Response {
		t.Helper()
		resp, err := rrc(e.village.Insurance(testCtx(t), e.as(p, "settlement.insurance", "insurance"), req))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// no fund yet: nothing to buy, and a resident cannot open one
	raidRefused(t, ins(a, handlers.VillageInsuranceRequest{Action: village.InsuranceActionBuy, Code: "kit_mutual"}), village.InsuranceNotOpen)
	raidRefused(t, ins(a, handlers.VillageInsuranceRequest{Action: village.InsuranceActionOpen}), village.VillageNotOfficeHolder)
	if resp := ins(e.head, handlers.VillageInsuranceRequest{Action: village.InsuranceActionOpen}); resp.Screen != village.ScreenInsurance {
		t.Fatalf("opening: %s", string(resp.View))
	}
	for _, p := range []*application.Player{a, b} {
		if resp := ins(p, handlers.VillageInsuranceRequest{Action: village.InsuranceActionBuy, Code: "kit_mutual"}); resp.Screen != village.ScreenInsurance {
			t.Fatalf("buying: %s", string(resp.View))
		}
	}
	// the same policy twice is refused
	raidRefused(t, ins(a, handlers.VillageInsuranceRequest{Action: village.InsuranceActionBuy, Code: "kit_mutual"}), village.InsuranceHeld)
	fund0 := e.scalar(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'insurance_fund' AND owner_id = $1::uuid`, e.cityID)
	if fund0 != 70 {
		t.Fatalf("the fund holds %d, want two premiums of 35", fund0)
	}
	// past the waiting time
	e.clock.Advance(e.scale.RealWait(48*time.Hour) + time.Hour)
	e.carry(a, "firewood", 40)

	id := e.file("3", "20")
	e.raidAs(a, false, handlers.VillageRaidRequest{Action: village.RaidActionStand, Code: id})
	e.raidAs(b, false, handlers.VillageRaidRequest{Action: village.RaidActionStand, Code: id})
	e.resolve(id)
	// a redelivery of the end claims nothing twice
	for _, act := range e.actions(id, application.RaidResolveActionType) {
		if _, err := e.village.RaidResolve(testCtx(t), e.sched("raid.resolve"), act); err != nil {
			t.Fatal(err)
		}
	}

	type claim struct{ due, paid int64 }
	got := map[string]claim{}
	rows, err := e.pool.Raw().Query(testCtx(t), `SELECT player_id::text, due, paid FROM insurance_claims WHERE source = 'fall:' || $1`, id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var pid string
		var c claim
		if err := rows.Scan(&pid, &c.due, &c.paid); err != nil {
			t.Fatal(err)
		}
		got[pid] = c
	}
	rows.Close()
	ca, cb := got[a.ID], got[b.ID]
	if len(got) != 2 || ca.due <= 0 || cb.due <= 0 {
		t.Fatalf("each fallen policy holder should have exactly one claim: %v", got)
	}
	if ca.paid+cb.paid > fund0 {
		t.Errorf("the claims paid %d out of a fund of %d", ca.paid+cb.paid, fund0)
	}
	if ca.due+cb.due <= fund0 {
		t.Fatalf("the test needs a short fund: dues %d against %d", ca.due+cb.due, fund0)
	}
	// the same share of what is due for everyone (rounding down apart)
	shareA, shareB := ca.paid*10_000/ca.due, cb.paid*10_000/cb.due
	if diff := shareA - shareB; diff > 300 || diff < -300 {
		t.Errorf("the shares differ: %d and %d of 10000", shareA, shareB)
	}
	if left := e.scalar(`SELECT COALESCE(SUM(balance), 0)::bigint FROM accounts WHERE kind = 'insurance_fund' AND owner_id = $1::uuid`, e.cityID); left < 0 || left != fund0-ca.paid-cb.paid {
		t.Errorf("the fund holds %d, want %d", left, fund0-ca.paid-cb.paid)
	}
	e.verify()
}

// A watch that is open sees the raiders; the news reaches the town by a runner or, from the same post, by a chain of open beacons at
// once. A beacon with no wood breaks the chain.
func TestADetectorSeesAndABeaconChainRelaysTheNewsInTime(t *testing.T) {
	// a fire is seen 4 km away: a chain across six kilometres needs a beacon in the middle
	e := newRaidEnv(t, func(r *application.RaidRules) { r.RelayM = 4000 })
	hall := e.scalar(`SELECT COALESCE(MAX(lot_x), 0) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'civic_hall'`, e.cityID)
	hallY := e.scalar(`SELECT COALESCE(MAX(lot_y), 0) FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'civic_hall'`, e.cityID)
	put := func(code string, dx int64) string {
		id := newUUID(t)
		if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'complete', now(), now())`, id, e.cityID, code, hall+dx, hallY+40); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// a lookout 6 km out (200 lots of 30 m); with no beacon the news walks home at 5 km/h and takes 72 minutes
	lookout := put("lookout_post", 200)
	far := put("beacon_tower", 201)
	mid := put("beacon_tower", 100)
	near := put("beacon_tower", 1)
	day := func(held ...string) {
		t.Helper()
		e.pool.Raw().Exec(testCtx(t), `DELETE FROM service_days WHERE settlement_id = $1::uuid`, e.cityID)
		if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
			// the judged day is the settlement's LOCAL day, and the clock moves a few hours between the steps: the same row stands for
			// the days around today, so the test does not depend on the hour it runs at
			def, err := tx.Settlements().ByID(ctx, e.cityID)
			if err != nil {
				return err
			}
			today := gametime.Clock(clockOf(t)).DayAtIn(e.clock.Now(), def.Zone())
			var posts []application.ServicePost
			for _, id := range []string{lookout, far, mid, near} {
				on := false
				for _, h := range held {
					on = on || h == id
				}
				p := application.ServicePost{BuildingID: id, Service: "claim_and_sight", Held: on}
				if on {
					p.Staff = 1
				} else {
					p.Idle = application.ServiceIdleNoSupplies
				}
				posts = append(posts, p)
			}
			for off := int64(-2); off <= 2; off++ {
				if _, err := tx.ServiceDays().RecordDay(ctx, application.ServiceDay{SettlementID: e.cityID, Day: today + off, At: e.clock.Now(), Posts: posts}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	id := e.file("3", "5")
	// a seed for which the lookout's chance comes up by day and by night (six in ten, a tenth less at night)
	for s := int64(1); ; s++ {
		if raid.Detected(4000, raid.Roll(s, "detect:"+lookout)) {
			e.pool.Raw().Exec(testCtx(t), `UPDATE settlement_raids SET seed = $2 WHERE id = $1::uuid`, id, s)
			break
		}
	}
	sight := func() {
		t.Helper()
		e.clock.Advance(2 * time.Hour) // an hour before the start
		if _, err := e.village.RaidSighting(testCtx(t), e.sched("raid.sighting"), handlers.RaidScheduledRequest{ActionID: newUUID(t), ActorID: e.att.ID, ReferenceID: id,
			Payload: []byte(`{"raid_id":"` + id + `","building_id":"` + lookout + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	events := func() int64 {
		return e.scalar(`SELECT count(*) FROM outbox WHERE subject LIKE '%raid_detected%' AND payload->>'raid_id' = $1`, id)
	}

	// 1. the lookout is open, the beacons are dark: it sees, but the runner is too late (72 minutes against the hour that is left)
	day(lookout)
	sight()
	var detected bool
	var hops, lead int64
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT detected, hops, lead_secs FROM settlement_raid_detections WHERE raid_id = $1::uuid AND building_id = $2::uuid`, id, lookout).Scan(&detected, &hops, &lead); err != nil {
		t.Fatal(err)
	}
	if !detected || hops != 0 || lead != 0 {
		t.Fatalf("a runner is too late: detected=%v hops=%d lead=%d", detected, hops, lead)
	}
	if events() != 0 {
		t.Errorf("the town was told too late to matter")
	}
	e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_raid_detections WHERE raid_id = $1::uuid`, id)
	e.pool.Raw().Exec(testCtx(t), `UPDATE service_days SET at = at WHERE settlement_id = $1::uuid`, e.cityID)

	// 2. the lookout and both beacons are open: the news crosses by fire in minutes and the town is told, once
	e.clock.Advance(-2 * time.Hour)
	day(lookout, far, mid, near)
	sight()
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT detected, hops, lead_secs FROM settlement_raid_detections WHERE raid_id = $1::uuid AND building_id = $2::uuid`, id, lookout).Scan(&detected, &hops, &lead); err != nil {
		t.Fatal(err)
	}
	if !detected || hops < 2 || lead <= 0 {
		t.Fatalf("the chain should carry the news in time: detected=%v hops=%d lead=%d", detected, hops, lead)
	}
	if events() != 1 {
		t.Errorf("the town was told %d times", events())
	}
	// a replay of the same sighting changes nothing
	sight()
	if events() != 1 {
		t.Errorf("a second sighting told the town again")
	}

	// 3. the beacon in the middle has no wood: the chain is broken and the news walks again
	e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_raid_detections WHERE raid_id = $1::uuid`, id)
	e.pool.Raw().Exec(testCtx(t), `DELETE FROM outbox WHERE subject LIKE '%raid_detected%' AND payload->>'raid_id' = $1`, id)
	e.clock.Advance(-2 * time.Hour)
	day(lookout, far, near)
	sight()
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT detected, hops, lead_secs FROM settlement_raid_detections WHERE raid_id = $1::uuid AND building_id = $2::uuid`, id, lookout).Scan(&detected, &hops, &lead); err != nil {
		t.Fatal(err)
	}
	if hops != 0 || lead != 0 {
		t.Errorf("a dark tower must break the chain: hops=%d lead=%d", hops, lead)
	}
	// an unstaffed lookout sees nothing at all
	e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_raid_detections WHERE raid_id = $1::uuid`, id)
	e.clock.Advance(-2 * time.Hour)
	day(far, mid, near)
	sight()
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT detected FROM settlement_raid_detections WHERE raid_id = $1::uuid AND building_id = $2::uuid`, id, lookout).Scan(&detected); err != nil {
		t.Fatal(err)
	}
	if detected {
		t.Errorf("a lookout with nobody on duty saw the raiders")
	}
	_ = strings.TrimSpace
}

func clockOf(t *testing.T) gametime.Clock {
	cfg := config.Defaults()
	c, err := cfg.GameClock()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Step 0 of ADR 0074 asked whether a player of a founded settlement has a country, so that the neutral insurer can sell him cover.
func TestAFoundedSettlementPlayerHasACountryForTheFallbackInsurer(t *testing.T) {
	e := newRaidEnv(t, nil)
	var country string
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		var err error
		country, err = tx.Diplomacy().CountryOfCity(ctx, e.cityID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("the country of a founded settlement: %q", country)
	if country == "" {
		t.Skip("a founded settlement belongs to no country in this database: the fallback insurer needs the neutral city's country to be found another way")
	}
}

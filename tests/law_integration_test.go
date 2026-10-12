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
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
)

// Police, courts, informants, the hidden board, laundering and customs (docs/adr/0078, plan B14), through the real handlers, the unit of work and the
// ledger. Three settlements are founded: A (the one the service-day env builds), B and C. Chances are pinned where a test needs a roll to go one way.

type lawEnv struct {
	*specialEnv
	rules  application.LawRules
	mature bool
}

func newLawEnv(t *testing.T) *lawEnv {
	t.Helper()
	s := newSpecialEnv(t, nil)
	e := &lawEnv{specialEnv: s}
	rules := settlementcfg.Law(config.Defaults().Law)
	from := s.clock.Now().Add(-48 * time.Hour)
	rules.PoliceAt, rules.CourtsAt, rules.BlackAt, rules.CustomsAt, rules.LaunderAt = from, from, from, from, from
	rules.Grace = 0
	rules.Mature = func(context.Context) bool { return e.mature }
	e.rules = rules
	s.village.WithLaw(rules)
	t.Cleanup(e.cleanLaw)
	return e
}

// resident is a person of one of the settlements; the law tables are emptied before the person goes, as the polity item's are.
func (e *lawEnv) resident(place string) *application.Player {
	e.t.Helper()
	p := e.specialEnv.resident(place)
	e.t.Cleanup(e.cleanLaw)
	return p
}

// rich is a resident with cash in hand.
func (e *lawEnv) rich(place string, cash int64) *application.Player {
	e.t.Helper()
	p := e.resident(place)
	if cash > 0 {
		grant(e.t, e.pool, application.AccountPlayerCash, p.ID, cash)
	}
	return p
}

// retuneLaw changes the rules of the item for the rest of the test.
func (e *lawEnv) retuneLaw(f func(*application.LawRules)) {
	f(&e.rules)
	e.village.WithLaw(e.rules)
}

// cleanLaw takes everything the item wrote away before the settlements and the players go.
func (e *lawEnv) cleanLaw() {
	ctx := testCtx(e.t)
	var owners []string
	for _, q := range []string{`SELECT id::text FROM law_escrows`, `SELECT id::text FROM law_launder_batches`} {
		rows, err := e.pool.Raw().Query(ctx, q)
		if err != nil {
			e.t.Errorf("cleanup %q: %v", q, err)
			continue
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				owners = append(owners, id)
			}
		}
		rows.Close()
	}
	if _, err := e.pool.Raw().Exec(ctx, `TRUNCATE law_gangs, law_loads, law_launder_batches, law_fronts, law_escrows, law_listings, law_brokers, law_underworld, law_audits, law_bribes,
		law_informant_contacts, law_informants, law_sentences, law_appeals, law_verdicts, law_warrants, law_evidence, law_case_stages, law_cases, law_capacity, law_offences,
		law_settings CASCADE`); err != nil {
		e.t.Errorf("cleanup of the law tables: %v", err)
	}
	_, _ = e.pool.Raw().Exec(ctx, `DELETE FROM game_actions WHERE action_type = 'polity_tick'`)
	purgeLedgerForAll(e.t, e.pool, owners)
}

func (e *lawEnv) law(p *application.Player, req handlers.VillageLawRequest) (village.LawView, string) {
	e.t.Helper()
	resp, err := e.village.Law(testCtx(e.t), e.metaOf(p, "settlement.law", "law"), req)
	if err != nil {
		e.t.Fatalf("law %s: %v", req.Action, err)
	}
	return decodeView[village.LawView](e.specialEnv, resp, village.ScreenLaw)
}

func (e *lawEnv) mustLaw(p *application.Player, req handlers.VillageLawRequest) village.LawView {
	e.t.Helper()
	v, kind := e.law(p, req)
	if kind != "" {
		e.t.Fatalf("law %s was refused: %s", req.Action, kind)
	}
	return v
}

// refused expects an act to be refused, and returns the refusal kind.
func (e *lawEnv) refused(p *application.Player, req handlers.VillageLawRequest) string {
	e.t.Helper()
	_, kind := e.law(p, req)
	if kind == "" {
		e.t.Fatalf("law %s was not refused", req.Action)
	}
	return kind
}

// station stands the buildings of the law in a settlement and records a judged day (and the days round it) with every post open. Which are open is
// the test's to choose by name: constabulary, court, custody, customs.
func (e *lawEnv) station(city string, open ...string) map[string]string {
	e.t.Helper()
	e.know(city, "formal_constabulary", "record_keeping", "written_law", "penal_custody", "customs_duty", "periodic_market", "field_craft", "communal_watch")
	ids := map[string]string{}
	for svc, code := range map[string]string{"constabulary": "police_post", "court": "courthouse", "custody": "jail", "customs": "customs_house"} {
		if id := e.text(`SELECT COALESCE((SELECT id::text FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = $2 LIMIT 1), '')`, city, code); id != "" {
			ids[svc] = id
		} else {
			ids[svc] = e.put(city, code)
		}
	}
	var posts []application.ServicePost
	for _, svc := range open {
		posts = append(posts, application.ServicePost{BuildingID: ids[svc], Service: svc, Held: true, Staff: 2})
	}
	e.dayWith(city, posts...)
	return ids
}

// officer makes a resident of A who holds the given permissions through an office of the charter.
func (e *lawEnv) officer(place string, perms ...string) *application.Player {
	e.t.Helper()
	p := e.resident(place)
	if place != "A" {
		e.t.Fatal("only the head of A creates offices in these tests")
	}
	e.appointWith(perms, p)
	return p
}

// list outlaws an offence in A as its head does.
func (e *lawEnv) list(offence string) {
	e.t.Helper()
	e.mustLaw(e.head, handlers.VillageLawRequest{Action: village.LawActionOffence, Code: offence})
}

// newCase opens a case in A against a person with evidence given by hand, so a test does not depend on a roll.
func (e *lawEnv) newCase(offence string, accused *application.Player, chargedBy *application.Player, source string) string {
	e.t.Helper()
	id := newUUID(e.t)
	var charged any
	if chargedBy != nil {
		charged = chargedBy.ID
	}
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO law_cases (id, settlement_id, offence, accused_id, source, state, model, charged_by, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, 'open', 'adversarial', $6::uuid, now(), now())`, id, e.cityID, offence, accused.ID, source, charged); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO law_case_stages (case_id, stage, at) VALUES ($1::uuid, 'opened', now())`, id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *lawEnv) evidence(caseID, kind string, strength int64, proper bool) string {
	e.t.Helper()
	id := newUUID(e.t)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO law_evidence (id, case_id, kind, strength, proper, ref, at) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, now())`,
		id, caseID, kind, strength, proper, id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// informantOf makes an informant of A with a source and a handler, and the record of the first contact.
func (e *lawEnv) informantOf(handler, source *application.Player, reliability int) string {
	e.t.Helper()
	id := newUUID(e.t)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO law_informants (id, settlement_id, handler_id, source_id, reliability, pay, state, handler_since, recruited_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, 100, 'active', now(), now())`, id, e.cityID, handler.ID, source.ID, reliability); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO law_informant_contacts (id, informant_id, handler_id, kind, at) VALUES ($1::uuid, $2::uuid, $3::uuid, 'recruit', now())`,
		newUUID(e.t), id, handler.ID); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *lawEnv) tipEvidence(caseID, informant string, strength int64, reliability int) {
	e.t.Helper()
	id := newUUID(e.t)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO law_evidence (id, case_id, kind, strength, proper, reliability, informant_id, ref, at)
		VALUES ($1::uuid, $2::uuid, 'informant_report', $3, true, $4, $5::uuid, $1::text, now())`, id, caseID, strength, reliability, informant); err != nil {
		e.t.Fatal(err)
	}
}

func (e *lawEnv) verifyLaw() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Law {
		e.t.Fatal("migration 0157 is not applied")
	}
	if s.LawDirtyStray != 0 || s.LawPotBroken != 0 || s.LawTradeLedger != s.LawTradeRows || s.LawFineLedger != s.LawFineRows || s.LawPenaltyLedger != s.LawPenaltyRows ||
		s.LawLaunderFeeLedger != s.LawLaunderFeeRows || s.LawJailNoCell != 0 || s.LawCellShared != 0 || s.LawSentenceNoVerdict != 0 || s.LawVerdictNoHearing != 0 ||
		s.LawAppealNoAppeal != 0 || s.LawWarrantTipsOnly != 0 || s.LawBribeNoMoney != 0 || s.LawMatureOff != 0 || s.LawInformantNoRecord != 0 || s.LawStagesBroken != 0 {
		e.t.Errorf("the law invariants do not hold: %+v", s)
	}
}

// tickQuiet sends a deadline and gives the error back, for tests that race it.
func (e *lawEnv) tickQuiet(kind, id, aux string) error {
	payload, _ := json.Marshal(handlers.PolityTickPayload{Kind: kind, ID: id, Aux: aux})
	_, err := e.village.PolityTick(testCtx(e.t), e.sched("polity.tick"), handlers.CrimeScheduledRequest{Payload: payload})
	return err
}

func (e *lawEnv) caseState(id string) string {
	return e.text(`SELECT state FROM law_cases WHERE id = $1::uuid`, id)
}

func (e *lawEnv) dirty(p *application.Player) int64 {
	return e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'player_dirty' AND owner_id = $1::uuid`, p.ID)
}

var _ = strings.TrimSpace
var _ sync.Mutex

// ---------------------------------------------------------------------------
// the charter keeps the prosecutor and the judge apart
// ---------------------------------------------------------------------------

// In the adversarial model, the default, no office may hold both the power to charge and the power to judge; the inquisitorial model lifts that, and
// an office that audits integrity never also arrests. The founder's office holds every power: the case itself keeps the roles apart.
func TestNoOfficeHoldsBothTheChargeAndTheJudgment(t *testing.T) {
	e := newLawEnv(t)
	save := func(title string, perms ...string) string {
		var gs []handlers.VillageCharterGrant
		for _, p := range perms {
			gs = append(gs, handlers.VillageCharterGrant{Permission: p})
		}
		resp, err := e.village.CharterOfficeSave(testCtx(t), e.metaOf(e.head, "settlement.charter.office.save", "save"), handlers.VillageCharterRequest{Title: title, Seats: 1, Grants: gs, Acquisition: "appointment"})
		if err != nil {
			t.Fatal(err)
		}
		if resp != nil && resp.Screen == village.ScreenVillageRefusal {
			return refusalKind(t, resp.View)
		}
		return ""
	}
	if kind := save("Magistrate "+randomToken(t, 3), "prosecute.charge", "court.judge"); kind != village.LawSeparation {
		t.Fatalf("an office that charges and judges is refused in the adversarial model, got %q", kind)
	}
	if kind := save("Inspector "+randomToken(t, 3), "audit.integrity", "police.arrest"); kind != village.LawSeparation {
		t.Fatalf("an office that audits the police and arrests is refused, got %q", kind)
	}
	if kind := save("Prosecutor "+randomToken(t, 3), "prosecute.charge", "prosecute.drop"); kind != "" {
		t.Fatalf("a prosecutor's office is fine: %q", kind)
	}
	// the inquisitorial model lets a magistrate hold both
	e.mustLaw(e.head, handlers.VillageLawRequest{Action: village.LawActionModel, Kind: "inquisitorial"})
	if kind := save("Magistrate "+randomToken(t, 3), "prosecute.charge", "court.judge"); kind != "" {
		t.Fatalf("the inquisitorial model lets a magistrate charge and judge, got %q", kind)
	}
	// and the adversarial model, put back, refuses to stand over an office that does
	if kind := e.refused(e.head, handlers.VillageLawRequest{Action: village.LawActionModel, Kind: "adversarial"}); kind != village.LawSeparation {
		t.Fatalf("going back to the adversarial model with such an office standing is refused, got %q", kind)
	}
	// the case itself keeps the roles apart: whoever charged it never judges it, the founder included
	e.station(e.cityID, "constabulary", "court")
	e.list("bribery")
	accused := e.resident("A")
	id := e.newCase("bribery", accused, e.head, "audit")
	e.evidence(id, "record", 25, true)
	e.evidence(id, "ledger_trace", 30, true)
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE law_cases SET state = 'hearing' WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	if kind := e.refused(e.head, handlers.VillageLawRequest{Action: village.LawActionVerdict, ID: id, Code: "acquit"}); kind != village.LawConflict {
		t.Fatalf("the person who charged a case never judges it, got %q", kind)
	}
	e.verifyLaw()
}

// ---------------------------------------------------------------------------
// warrants
// ---------------------------------------------------------------------------

// A warrant needs a judge, and the proper evidence in the file must reach the threshold and must not be informants' tips alone. A tip from a source under the
// reliability floor counts for nothing at all.
func TestAWarrantNeedsAJudgeAndNeverRestsOnTipsAlone(t *testing.T) {
	e := newLawEnv(t)
	e.station(e.cityID, "constabulary", "court")
	e.list("bribery")
	officer := e.officer("A", "police.warrant_ask", "police.investigate")
	judge := e.officer("A", "court.warrant")
	nobody := e.resident("A")
	accused := e.resident("A")
	source := e.resident("A")
	inf := e.informantOf(officer, source, 90)
	id := e.newCase("bribery", accused, nil, "tip")

	// two reliable tips and nothing else
	e.tipEvidence(id, inf, 50, 90)
	e.tipEvidence(id, inf, 50, 90)
	e.mustLaw(officer, handlers.VillageLawRequest{Action: village.LawActionWarrantAsk, ID: id})
	w := e.text(`SELECT id::text FROM law_warrants WHERE case_id = $1::uuid AND status = 'asked'`, id)
	// nobody signs who is not a judge
	if kind := e.refused(nobody, handlers.VillageLawRequest{Action: village.LawActionWarrantSign, ID: w}); kind == "" {
		t.Fatal("a resident with no power signed a warrant")
	}
	if kind := e.refused(officer, handlers.VillageLawRequest{Action: village.LawActionWarrantSign, ID: w}); kind == "" {
		t.Fatal("an officer signed his own warrant")
	}
	if kind := e.refused(judge, handlers.VillageLawRequest{Action: village.LawActionWarrantSign, ID: w}); kind != village.LawTipsAlone {
		t.Fatalf("a warrant on tips alone is refused, got %q", kind)
	}
	// tips from a source under the floor add nothing, even beside a modest witness
	low := e.informantOf(officer, e.resident("A"), 10)
	id2 := e.newCase("bribery", accused, nil, "tip")
	e.tipEvidence(id2, low, 50, 10)
	e.tipEvidence(id2, low, 50, 10)
	e.evidence(id2, "witness", 20, true)
	e.mustLaw(officer, handlers.VillageLawRequest{Action: village.LawActionWarrantAsk, ID: id2})
	w2 := e.text(`SELECT id::text FROM law_warrants WHERE case_id = $1::uuid AND status = 'asked'`, id2)
	if kind := e.refused(judge, handlers.VillageLawRequest{Action: village.LawActionWarrantSign, ID: w2}); kind != village.LawWeakEvidence {
		t.Fatalf("tips from an unreliable source count for nothing, got %q", kind)
	}
	// a tip beside real evidence is enough
	e.evidence(id, "record", 25, true)
	e.evidence(id, "confession", 40, true)
	e.mustLaw(judge, handlers.VillageLawRequest{Action: village.LawActionWarrantSign, ID: w})
	if e.text(`SELECT status FROM law_warrants WHERE id = $1::uuid`, w) != "signed" {
		t.Fatal("the warrant is signed")
	}
	// improper evidence adds nothing to a warrant
	id3 := e.newCase("bribery", accused, nil, "tip")
	e.evidence(id3, "seized_item", 80, false)
	e.mustLaw(officer, handlers.VillageLawRequest{Action: village.LawActionWarrantAsk, ID: id3})
	w3 := e.text(`SELECT id::text FROM law_warrants WHERE case_id = $1::uuid AND status = 'asked'`, id3)
	if kind := e.refused(judge, handlers.VillageLawRequest{Action: village.LawActionWarrantSign, ID: w3}); kind != village.LawWeakEvidence {
		t.Fatalf("improper evidence is not enough for a warrant, got %q", kind)
	}
	e.verifyLaw()
}

// setPost opens or closes a post on every judged day of the test, as a day with no wage or no staff would.
func (e *lawEnv) setPost(building string, held bool) {
	e.t.Helper()
	q := `UPDATE service_day_posts SET held = false, idle = 'no_staff', staff = 0, wage = 0, used = '{}'::jsonb WHERE building_id = $1::uuid`
	if held {
		q = `UPDATE service_day_posts SET held = true, idle = '', staff = 2 WHERE building_id = $1::uuid`
	}
	if _, err := e.pool.Raw().Exec(testCtx(e.t), q, building); err != nil {
		e.t.Fatal(err)
	}
}

// toHearing puts a case in a hearing as a charge and a hearing day would, charged by a person.
func (e *lawEnv) toHearing(id string, by *application.Player) {
	e.t.Helper()
	for _, q := range []string{
		`UPDATE law_cases SET state = 'hearing', charged_by = $2::uuid, charged_at = now(), hearing_at = now() WHERE id = $1::uuid`,
		`INSERT INTO law_case_stages (case_id, stage, at) VALUES ($1::uuid, 'charged', now())`,
		`INSERT INTO law_case_stages (case_id, stage, at) VALUES ($1::uuid, 'hearing', now())`,
	} {
		var args []any
		args = append(args, id)
		if strings.Contains(q, "$2") {
			args = append(args, by.ID)
		}
		if _, err := e.pool.Raw().Exec(testCtx(e.t), q, args...); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *lawEnv) verdicts(id string) int64 {
	return e.scalarInt(`SELECT count(*) FROM law_verdicts WHERE case_id = $1::uuid`, id)
}

// A trial sits only in a courthouse that is open: without one the hearing is put off a day and nothing is decided. With a judge among the holders of the
// power the case waits for him, and when the wait is over the NPC judge decides by a threshold on the evidence that stands - so a settlement is never
// without a court. Each party appeals once; an appeal nobody hears is decided by the NPC panel, which asks for a little more.
func TestATrialSitsOnlyInAStaffedCourthouseTheNPCJudgeFillsInAndEachPartyAppealsOnce(t *testing.T) {
	e := newLawEnv(t)
	ids := e.station(e.cityID, "constabulary", "court")
	e.list("bribery")
	prosecutor := e.officer("A", "prosecute.charge")
	accused := e.rich("A", 10_000)
	id := e.newCase("bribery", accused, nil, "audit")
	e.evidence(id, "ledger_trace", 30, true)
	e.evidence(id, "record", 25, true)
	e.evidence(id, "witness", 20, true)

	// a charge needs a prosecutor; a resident cannot charge
	if kind := e.refused(e.resident("A"), handlers.VillageLawRequest{Action: village.LawActionCharge, ID: id}); kind == "" {
		t.Fatal("a resident charged a case")
	}
	e.mustLaw(prosecutor, handlers.VillageLawRequest{Action: village.LawActionCharge, ID: id})
	if e.caseState(id) != "charged" {
		t.Fatalf("the case is charged, it is %s", e.caseState(id))
	}
	// the courthouse is shut that day: the hearing is put off, nothing is decided
	e.setPost(ids["court"], false)
	e.tick(application.TickLawHearing, id, "")
	if e.caseState(id) != "charged" || e.verdicts(id) != 0 {
		t.Fatalf("a hearing in a shut courthouse decides nothing: %s, %d verdicts", e.caseState(id), e.verdicts(id))
	}
	if e.scalarInt(`SELECT count(*) FROM law_case_stages WHERE case_id = $1::uuid AND stage LIKE 'postponed:%%'`, id) != 1 {
		t.Fatal("the put-off is written once")
	}
	e.tick(application.TickLawHearing, id, "") // the same day again: no second put-off
	if e.scalarInt(`SELECT count(*) FROM law_case_stages WHERE case_id = $1::uuid AND stage LIKE 'postponed:%%'`, id) != 1 {
		t.Fatal("the put-off of a day is written once")
	}
	// open: the head holds the judge's power and did not charge, so the case waits for a verdict
	e.setPost(ids["court"], true)
	e.tick(application.TickLawHearing, id, "")
	if e.caseState(id) != "hearing" || e.verdicts(id) != 0 {
		t.Fatalf("with a judge to decide the case waits: %s, %d", e.caseState(id), e.verdicts(id))
	}
	// the wait is over: the NPC judge decides. 62 of evidence clears the threshold of 55.
	e.tick(application.TickLawHearing, id, "wait")
	if e.caseState(id) != "decided" || e.verdicts(id) != 1 {
		t.Fatalf("the NPC judge decides when the wait is over: %s, %d", e.caseState(id), e.verdicts(id))
	}
	if e.scalarInt(`SELECT count(*) FROM law_verdicts WHERE case_id = $1::uuid AND npc AND guilty AND instance = 1`, id) != 1 {
		t.Fatal("a verdict of the NPC judge, guilty")
	}
	// no jail stands, so the sentence is a fine, a part of the offence's ceiling, taken at once
	if e.scalarInt(`SELECT count(*) FROM law_sentences WHERE case_id = $1::uuid AND kind = 'jail'`, id) != 0 {
		t.Fatal("no jail sentence where no jail stands")
	}
	fine := e.scalarInt(`SELECT fine_paid FROM law_sentences WHERE case_id = $1::uuid AND kind = 'fine'`, id)
	if fine != 6000 || e.cash(accused) != 4000 {
		t.Fatalf("a fine of half the ceiling, taken: %d, cash %d", fine, e.cash(accused))
	}
	// the same tick again decides nothing twice
	e.tick(application.TickLawHearing, id, "wait")
	if e.verdicts(id) != 1 || e.scalarInt(`SELECT count(*) FROM law_sentences WHERE case_id = $1::uuid`, id) != 1 {
		t.Fatal("a second delivery found the step done")
	}

	// the accused appeals once
	e.mustLaw(accused, handlers.VillageLawRequest{Action: village.LawActionAppealAccused, ID: id})
	if kind := e.refused(accused, handlers.VillageLawRequest{Action: village.LawActionAppealAccused, ID: id}); kind != village.LawAppealUsed {
		t.Fatalf("a party appeals once, got %q", kind)
	}
	// so does the prosecution, and nobody else
	if kind := e.refused(e.resident("A"), handlers.VillageLawRequest{Action: village.LawActionAppealProsecution, ID: id}); kind == "" {
		t.Fatal("a resident appealed for the prosecution")
	}
	e.mustLaw(prosecutor, handlers.VillageLawRequest{Action: village.LawActionAppealProsecution, ID: id})
	if e.scalarInt(`SELECT count(*) FROM law_appeals WHERE case_id = $1::uuid`, id) != 2 {
		t.Fatal("one appeal for each party")
	}
	// nobody on the bench hears it: the NPC panel asks for more than the trial did (62 against 65) and quashes
	e.tick(application.TickLawAppeal, id, "accused")
	if e.caseState(id) != "final" || e.scalarInt(`SELECT count(*) FROM law_verdicts WHERE case_id = $1::uuid AND instance = 2 AND NOT guilty`, id) != 1 {
		t.Fatalf("the NPC panel quashes: %s", e.caseState(id))
	}
	if e.scalarInt(`SELECT count(*) FROM law_sentences WHERE case_id = $1::uuid AND status = 'quashed'`, id) != 1 {
		t.Fatal("the sentence is quashed")
	}
	if e.cash(accused) != 10_000-200+200 {
		t.Fatalf("the fine and the appeal fee come back: %d", e.cash(accused))
	}
	e.tick(application.TickLawAppeal, id, "prosecution") // the second party's tick finds it heard
	if e.scalarInt(`SELECT count(*) FROM law_verdicts WHERE case_id = $1::uuid AND instance = 2`, id) != 1 {
		t.Fatal("an appeal is heard once")
	}
	e.verifyLaw()

	// when the only holders of the judge's power are the one who charged and the accused, the NPC judge decides at the hearing
	id2 := e.newCase("bribery", e.rich("A", 100), nil, "audit")
	e.evidence(id2, "ledger_trace", 30, true)
	e.evidence(id2, "record", 25, true)
	e.evidence(id2, "witness", 20, true)
	e.mustLaw(e.head, handlers.VillageLawRequest{Action: village.LawActionCharge, ID: id2})
	if _, err := e.pool.Raw().Exec(testCtx(t), `DELETE FROM charter_seats WHERE office_id IN (SELECT id FROM charter_offices WHERE settlement_id = $1::uuid AND acquisition <> 'head')`, e.cityID); err != nil {
		t.Fatal(err)
	}
	e.tick(application.TickLawHearing, id2, "")
	if e.caseState(id2) != "decided" || e.verdicts(id2) != 1 {
		t.Fatalf("with no one to judge but the charger the NPC judge decides at once: %s", e.caseState(id2))
	}
	e.verifyLaw()
}

// A jail sentence is served in a real jail, in a cell, and only where a jail stands, is open and has a cell free. A court that tries to sentence to jail
// where none stands is refused; the jail's prisoners are let out when it is closed, fed from the treasury for the days served, and bail buys the rest.
func TestNoJailSentenceWithoutAJailBuildingAndTheJailKeepsItsPrisoners(t *testing.T) {
	e := newLawEnv(t)
	ids := e.station(e.cityID, "constabulary", "court", "custody")
	e.list("forgery")
	judge := e.officer("A", "court.judge")
	accused := e.rich("A", 50_000)
	id := e.newCase("forgery", accused, e.head, "audit")
	e.toHearing(id, e.head)
	e.evidence(id, "record", 25, true)

	// the jail is shut (it stands but nobody keeps it): no sentence to it
	e.setPost(ids["custody"], false)
	if kind := e.refused(judge, handlers.VillageLawRequest{Action: village.LawActionVerdict, ID: id, Code: "guilty", Items: "jail:10"}); kind != village.LawNoJail {
		t.Fatalf("a jail sentence where no open jail has a cell is refused, got %q", kind)
	}
	// a sentence over the ceiling of the offence, or of a kind it does not carry
	e.setPost(ids["custody"], true)
	if kind := e.refused(judge, handlers.VillageLawRequest{Action: village.LawActionVerdict, ID: id, Code: "guilty", Items: "jail:9999"}); kind != village.LawOverBound {
		t.Fatalf("over the ceiling, got %q", kind)
	}
	if kind := e.refused(judge, handlers.VillageLawRequest{Action: village.LawActionVerdict, ID: id, Code: "guilty", Items: "caution"}); kind != village.LawKindNotAllowed {
		t.Fatalf("forgery carries no caution, got %q", kind)
	}
	// a fine and a term inside the ceilings, with a jail open: the verdict stands
	e.mustLaw(judge, handlers.VillageLawRequest{Action: village.LawActionVerdict, ID: id, Code: "guilty", Items: "fine:7000,jail:10"})
	if e.verdicts(id) != 1 {
		t.Fatalf("the verdict stands, %d", e.verdicts(id))
	}
	if e.scalarInt(`SELECT count(*) FROM law_sentences WHERE case_id = $1::uuid AND kind = 'jail' AND jail_building = $2::uuid AND cell BETWEEN 1 AND 10`, id, ids["custody"]) != 1 {
		t.Fatal("a jail sentence names the jail and a cell")
	}
	// the prisoner is held: a second person cannot take the same cell, a closed jail lets the prisoner out
	sent := e.text(`SELECT id::text FROM law_sentences WHERE case_id = $1::uuid AND kind = 'jail'`, id)
	treasury := e.cityCash(e.cityID)
	e.setPost(ids["custody"], false)
	e.mustLaw(judge, handlers.VillageLawRequest{Action: village.LawActionOpen})
	if e.text(`SELECT status FROM law_sentences WHERE id = $1::uuid`, sent) != "released" {
		t.Fatal("a jail that is shut holds nobody")
	}
	if e.cityCash(e.cityID) >= treasury {
		t.Fatal("the food of the days served is paid from the treasury")
	}
	// bail: a new prisoner buys the rest of the term
	e.setPost(ids["custody"], true)
	id2 := e.newCase("forgery", accused, e.head, "audit")
	e.toHearing(id2, e.head)
	e.mustLaw(judge, handlers.VillageLawRequest{Action: village.LawActionVerdict, ID: id2, Code: "guilty", Items: "jail:10"})
	sent2 := e.text(`SELECT id::text FROM law_sentences WHERE case_id = $1::uuid AND kind = 'jail'`, id2)
	cash := e.cash(accused)
	e.mustLaw(accused, handlers.VillageLawRequest{Action: village.LawActionBail, ID: sent2})
	if e.text(`SELECT status FROM law_sentences WHERE id = $1::uuid`, sent2) != "bailed" || e.cash(accused) >= cash {
		t.Fatal("bail pays for the rest of the term")
	}
	// a served term ends by itself
	id3 := e.newCase("forgery", accused, e.head, "audit")
	e.toHearing(id3, e.head)
	e.mustLaw(judge, handlers.VillageLawRequest{Action: village.LawActionVerdict, ID: id3, Code: "guilty", Items: "jail:2"})
	sent3 := e.text(`SELECT id::text FROM law_sentences WHERE case_id = $1::uuid AND kind = 'jail'`, id3)
	e.clock.Advance(3 * time.Hour)
	e.tick(application.TickLawJail, sent3, "")
	if e.text(`SELECT status FROM law_sentences WHERE id = $1::uuid`, sent3) != "served" {
		t.Fatal("a term that is over is served")
	}
	e.verifyLaw()
}

// Two replicas working one case at once decide it once: the hearing, the verdict and the end of the case are each fenced by a stage row.
func TestTheStagesOfACaseAreTakenByOneReplica(t *testing.T) {
	e := newLawEnv(t)
	e.station(e.cityID, "constabulary", "court")
	e.list("bribery")
	if _, err := e.pool.Raw().Exec(testCtx(t), `DELETE FROM charter_seats WHERE office_id IN (SELECT id FROM charter_offices WHERE settlement_id = $1::uuid AND acquisition <> 'head')`, e.cityID); err != nil {
		t.Fatal(err)
	}
	accused := e.rich("A", 20_000)
	id := e.newCase("bribery", accused, nil, "audit")
	e.evidence(id, "ledger_trace", 30, true)
	e.evidence(id, "record", 25, true)
	e.evidence(id, "witness", 20, true)
	e.mustLaw(e.head, handlers.VillageLawRequest{Action: village.LawActionCharge, ID: id})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// a transaction that loses the race may be refused for a deadlock; the broker would redeliver it
			for try := 0; try < 3; try++ {
				if err := e.tickQuiet(application.TickLawHearing, id, ""); err == nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	if e.verdicts(id) != 1 {
		t.Fatalf("eight replicas, one verdict, got %d", e.verdicts(id))
	}
	if e.scalarInt(`SELECT count(*) FROM law_sentences WHERE case_id = $1::uuid`, id) != 1 {
		t.Fatal("one sentence")
	}
	if e.scalarInt(`SELECT count(*) FROM law_case_stages WHERE case_id = $1::uuid AND stage IN ('charged', 'hearing', 'decided')`, id) != 3 {
		t.Fatal("each stage is written once")
	}
	if e.cash(accused) != 20_000-6000 {
		t.Fatalf("the fine is taken once: %d", e.cash(accused))
	}
	// the final tick of the appeal window: two replicas, one end
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for try := 0; try < 3; try++ {
				if err := e.tickQuiet(application.TickLawFinal, id, ""); err == nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	if e.caseState(id) != "final" || e.scalarInt(`SELECT count(*) FROM law_case_stages WHERE case_id = $1::uuid AND stage = 'final'`, id) != 1 {
		t.Fatalf("the case ends once: %s", e.caseState(id))
	}
	e.verifyLaw()
}

// ---------------------------------------------------------------------------
// informants
// ---------------------------------------------------------------------------

// act records a bribe offered by a person: an act the game recorded, which a tip can be about.
func (e *lawEnv) act(briber, holder *application.Player) string {
	e.t.Helper()
	id := newUUID(e.t)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO law_bribes (id, settlement_id, briber_id, holder_id, rung, effect_bps, amount, dirty, status, offered_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'verdict', 3000, 600, false, 'offered', now())`, id, e.cityID, briber.ID, holder.ID); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// An informant is a role with a handler, a reliability and a record of every contact. A report with something behind it raises the source's reliability and
// goes into the file as evidence scaled by it; one with nothing behind it lowers it. A handler keeps a source only so long: after the rotation period the
// source must pass to another handler. A source whose tip helped a conviction may be burned: a cash penalty and a ban from the market for some days - never
// harm to the person.
func TestAnInformantIsRotatedAndOnlyBurnedByCashAndABan(t *testing.T) {
	e := newLawEnv(t)
	e.station(e.cityID, "constabulary", "court")
	e.list("bribery")
	handler := e.officer("A", "police.informant_run")
	other := e.officer("A", "police.informant_run")
	source := e.rich("A", 5000)
	target := e.resident("A")
	holder := e.resident("A")

	// recruiting needs the power, and a resident of this settlement
	if kind := e.refused(e.resident("A"), handlers.VillageLawRequest{Action: village.LawActionRecruit, Player: source.PublicCode}); kind == "" {
		t.Fatal("a resident recruited an informant")
	}
	stranger := e.resident("B")
	if kind := e.refused(handler, handlers.VillageLawRequest{Action: village.LawActionRecruit, Player: stranger.PublicCode}); kind != village.LawNotResident {
		t.Fatalf("a resident of another settlement is not recruited here, got %q", kind)
	}
	e.mustLaw(handler, handlers.VillageLawRequest{Action: village.LawActionRecruit, Player: source.PublicCode})
	inf := e.text(`SELECT id::text FROM law_informants WHERE source_id = $1::uuid`, source.ID)
	if e.scalarInt(`SELECT count(*) FROM law_informant_contacts WHERE informant_id = $1::uuid AND kind = 'recruit'`, inf) != 1 {
		t.Fatal("the first contact is on the record")
	}
	if kind := e.refused(handler, handlers.VillageLawRequest{Action: village.LawActionRecruit, Player: source.PublicCode}); kind != village.LawAlreadyInformant {
		t.Fatalf("a source is recruited once, got %q", kind)
	}
	// a report with nothing behind it lowers the reliability; the source is paid all the same, from the treasury (the purse is empty)
	cash0 := e.cash(source)
	v := e.mustLaw(handler, handlers.VillageLawRequest{Action: village.LawActionTip, ID: inf, Player: target.PublicCode})
	if v.Notice != village.LawNoticeTipEmpty {
		t.Fatalf("a tip with nothing behind it, got %q", v.Notice)
	}
	if rel := e.scalarInt(`SELECT reliability FROM law_informants WHERE id = $1::uuid`, inf); rel != e.rules.T.ReliabilityStart-e.rules.T.ReliabilityLoss {
		t.Fatalf("the reliability falls, %d", rel)
	}
	if e.cash(source)-cash0 != e.rules.T.InformantPay {
		t.Fatalf("a report is paid for, %d", e.cash(source)-cash0)
	}
	// a report with an act behind it is evidence and raises the reliability
	e.act(target, holder)
	v = e.mustLaw(handler, handlers.VillageLawRequest{Action: village.LawActionTip, ID: inf, Player: target.PublicCode})
	if v.Notice != village.LawNoticeTip {
		t.Fatalf("a tip with an act behind it, got %q", v.Notice)
	}
	caseID := e.text(`SELECT id::text FROM law_cases WHERE accused_id = $1::uuid`, target.ID)
	if e.scalarInt(`SELECT count(*) FROM law_evidence WHERE case_id = $1::uuid AND kind = 'informant_report' AND informant_id = $2::uuid AND proper`, caseID, inf) != 1 {
		t.Fatal("the tip is in the file")
	}
	// pressing a source for more than he knows files improper evidence and leaves a record
	e.mustLaw(handler, handlers.VillageLawRequest{Action: village.LawActionTip, ID: inf, Player: target.PublicCode, Kind: "press"})
	if e.scalarInt(`SELECT count(*) FROM law_informant_contacts WHERE informant_id = $1::uuid AND kind = 'press'`, inf) != 1 ||
		e.scalarInt(`SELECT count(*) FROM law_evidence WHERE case_id = $1::uuid AND kind = 'informant_report' AND NOT proper`, caseID) != 1 {
		t.Fatal("a pressed tip is improper evidence and is on the record")
	}

	// rotation: the other handler cannot take him before it is due, the handler cannot keep him after
	if kind := e.refused(other, handlers.VillageLawRequest{Action: village.LawActionTake, ID: inf}); kind != village.LawRotateNotDue {
		t.Fatalf("an informant is not taken before the rotation is due, got %q", kind)
	}
	e.clock.Advance(time.Duration(e.rules.T.RotateDays+1) * 24 * time.Hour)
	if kind := e.refused(handler, handlers.VillageLawRequest{Action: village.LawActionTip, ID: inf, Player: target.PublicCode}); kind != village.LawRotateDue {
		t.Fatalf("a handler keeps a source only so long, got %q", kind)
	}
	e.mustLaw(other, handlers.VillageLawRequest{Action: village.LawActionTake, ID: inf})
	if e.text(`SELECT handler_id::text FROM law_informants WHERE id = $1::uuid`, inf) != other.ID {
		t.Fatal("the informant passed to the other handler")
	}
	if kind := e.refused(handler, handlers.VillageLawRequest{Action: village.LawActionTip, ID: inf, Player: target.PublicCode}); kind != village.LawNotYourInformant {
		t.Fatalf("the old handler has no say, got %q", kind)
	}
	e.mustLaw(other, handlers.VillageLawRequest{Action: village.LawActionTip, ID: inf, Player: target.PublicCode})

	// a conviction his tip stood in may burn him: a cash penalty and a ban from the market, nothing else
	e.retuneLaw(func(r *application.LawRules) { r.T.BurnBPS = 10_000 })
	e.evidence(caseID, "confession", 40, true)
	e.evidence(caseID, "ledger_trace", 30, true)
	e.evidence(caseID, "record", 25, true)
	e.evidence(caseID, "witness", 20, true)
	e.toHearing(caseID, e.head)
	cashBefore := e.cash(source)
	e.tick(application.TickLawHearing, caseID, "wait")
	if e.text(`SELECT state FROM law_informants WHERE id = $1::uuid`, inf) != "burned" {
		t.Fatal("the source is burned")
	}
	lost := cashBefore - e.cash(source)
	if lost != e.rules.T.RetaliationFine {
		t.Fatalf("the penalty is the fine and nothing more: %d", lost)
	}
	if e.scalarInt(`SELECT count(*) FROM law_sentences WHERE player_id = $1::uuid AND kind = 'ban' AND ban = 'market' AND ban_days = $2`, source.ID, e.rules.T.RetaliationBanDays) != 1 {
		t.Fatal("a ban from the market for the days of the tuning")
	}
	if e.scalarInt(`SELECT count(*) FROM law_sentences WHERE player_id = $1::uuid AND kind IN ('jail', 'forfeiture')`, source.ID) != 0 {
		t.Fatal("no jail and no forfeiture for the source")
	}
	if e.scalarInt(`SELECT count(*) FROM hospital_stays WHERE player_id = $1::uuid`, source.ID) != 0 {
		t.Fatal("the source is not harmed")
	}
	if e.scalarInt(`SELECT count(*) FROM law_informant_contacts WHERE informant_id = $1::uuid AND kind = 'burn'`, inf) != 1 {
		t.Fatal("the burning is on the record")
	}
	// the record of contacts is never changed
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE law_informant_contacts SET kind = 'retire' WHERE informant_id = $1::uuid`, inf); err == nil {
		t.Fatal("the record of contacts was changed")
	}
	e.verifyLaw()
}

// ---------------------------------------------------------------------------
// the hidden board
// ---------------------------------------------------------------------------

func (e *lawEnv) standing(p *application.Player, n int64) {
	e.t.Helper()
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO law_underworld (player_id, standing, updated_at) VALUES ($1::uuid, $2, now()) ON CONFLICT (player_id) DO UPDATE SET standing = $2`, p.ID, n); err != nil {
		e.t.Fatal(err)
	}
}

// The board is a market with a premium and a chance of losing. A trade between players goes through a broker's escrow: the pot holds the price and the fee until
// the buyer confirms or the time runs out; a broker may leave with the pot (the exit scam), which costs his name and may be traced. What the seller is paid is
// dirty cash, which pays nothing legal.
func TestAnEscrowMayBeConfirmedRefundedOrScammedAndTheTakeIsDirty(t *testing.T) {
	e := newLawEnv(t)
	e.station(e.cityID, "constabulary")
	seller := e.rich("A", 0)
	buyer := e.rich("A", 10_000)
	broker := e.rich("A", 0)
	for _, p := range []*application.Player{seller, buyer, broker} {
		e.standing(p, 10)
	}
	e.mustLaw(broker, handlers.VillageLawRequest{Action: village.LawActionBroker})
	e.carry(seller, "bread", 10)
	grant(t, e.pool, application.AccountPlayerCash, seller.ID, 100)
	// a listing needs standing, goods in the bags and a fee
	if kind := e.refused(e.resident("A"), handlers.VillageLawRequest{Action: village.LawActionList, Code: "bread", Qty: "4", Amount: "50"}); kind != village.LawNoStanding {
		t.Fatalf("a stranger to the board lists nothing, got %q", kind)
	}
	if kind := e.refused(seller, handlers.VillageLawRequest{Action: village.LawActionList, Code: "bread", Qty: "40", Amount: "50"}); kind != village.LawNoGoods {
		t.Fatalf("goods the seller does not carry, got %q", kind)
	}
	e.mustLaw(seller, handlers.VillageLawRequest{Action: village.LawActionList, Code: "bread", Qty: "4", Amount: "50"})
	listing := e.text(`SELECT id::text FROM law_listings WHERE seller_id = $1::uuid`, seller.ID)
	if e.carried(seller, "bread") != 6 {
		t.Fatalf("the listed goods are set aside, carried %d", e.carried(seller, "bread"))
	}
	// the broker's cap: a new broker holds only so much
	big := e.mustLaw(seller, handlers.VillageLawRequest{Action: village.LawActionOpen})
	_ = big
	// buying: the pot holds the price and the fee
	e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionBuy, ID: listing, Code: broker.PublicCode})
	esc := e.text(`SELECT id::text FROM law_escrows WHERE listing_id = $1::uuid`, listing)
	price, fee := int64(200), e.rules.T.EscrowFee(200)
	if e.cash(buyer) != 10_000-price-fee || e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'escrow_black' AND owner_id = $1::uuid`, esc) != price+fee {
		t.Fatalf("the pot holds the price and the fee: buyer %d", e.cash(buyer))
	}
	if kind := e.refused(seller, handlers.VillageLawRequest{Action: village.LawActionConfirm, ID: esc}); kind != village.LawNoEscrow {
		t.Fatalf("only the buyer confirms, got %q", kind)
	}
	// the exit scam: the broker leaves with the pot, in dirty cash
	repBefore := e.scalarInt(`SELECT reputation FROM law_brokers WHERE player_id = $1::uuid`, broker.ID)
	e.retuneLaw(func(r *application.LawRules) { r.T.TraceBPS = 0 })
	v := e.mustLaw(broker, handlers.VillageLawRequest{Action: village.LawActionScam, ID: esc})
	if v.Notice != village.LawNoticeScam {
		t.Fatalf("an untraced scam, got %q", v.Notice)
	}
	if e.dirty(broker) != price+fee {
		t.Fatalf("the broker holds the pot as dirty cash: %d", e.dirty(broker))
	}
	if e.scalarInt(`SELECT reputation FROM law_brokers WHERE player_id = $1::uuid`, broker.ID) != repBefore-e.rules.T.ScamRepLoss {
		t.Fatal("the scam costs the broker his name")
	}
	if e.text(`SELECT status FROM law_escrows WHERE id = $1::uuid`, esc) != "scammed" || e.text(`SELECT status FROM law_listings WHERE id = $1::uuid`, listing) != "open" {
		t.Fatal("the escrow is scammed and the goods are on offer again")
	}
	if e.cash(buyer) != 10_000-price-fee {
		t.Fatal("the buyer lost his money")
	}
	// dirty cash pays nothing legal: the broker can pay no complaint fee with it
	e.list("bribery")
	if kind := e.refused(broker, handlers.VillageLawRequest{Action: village.LawActionComplain, Code: "bribery", Player: buyer.PublicCode}); kind != village.LawNoMoney {
		t.Fatalf("dirty cash pays no fee, got %q", kind)
	}
	// a second deal, confirmed: the seller is paid dirty, the broker his fee in clean cash, the buyer his goods
	e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionBuy, ID: listing, Code: broker.PublicCode})
	esc2 := e.text(`SELECT id::text FROM law_escrows WHERE listing_id = $1::uuid AND status = 'held'`, listing)
	e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionConfirm, ID: esc2})
	if e.dirty(seller) != price || e.carried(buyer, "bread") != 4 || e.cash(broker) != fee {
		t.Fatalf("the trade is settled: seller dirty %d, buyer bread %d, broker cash %d", e.dirty(seller), e.carried(buyer, "bread"), e.cash(broker))
	}
	if e.scalarInt(`SELECT standing FROM law_underworld WHERE player_id = $1::uuid`, buyer.ID) != 10+e.rules.T.StandingGain {
		t.Fatal("a completed trade earns standing")
	}
	e.verifyLaw()

	// a pot nobody answers goes back to the buyer when the time is out
	e.carry(seller, "bread", 4)
	grant(t, e.pool, application.AccountPlayerCash, seller.ID, 100)
	e.mustLaw(seller, handlers.VillageLawRequest{Action: village.LawActionList, Code: "bread", Qty: "2", Amount: "50"})
	l3 := e.text(`SELECT id::text FROM law_listings WHERE seller_id = $1::uuid AND status = 'open'`, seller.ID)
	cash := e.cash(buyer)
	e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionBuy, ID: l3, Code: broker.PublicCode})
	esc3 := e.text(`SELECT id::text FROM law_escrows WHERE listing_id = $1::uuid`, l3)
	e.clock.Advance(time.Duration(e.rules.T.EscrowTimeoutHours+1) * time.Hour)
	e.tick(application.TickLawEscrow, esc3, "")
	if e.text(`SELECT status FROM law_escrows WHERE id = $1::uuid`, esc3) != "refunded" || e.cash(buyer) != cash {
		t.Fatal("an unanswered pot goes back to the buyer")
	}
	// a pot over a broker's cap is refused
	e.carry(seller, "coffee", 200)
	grant(t, e.pool, application.AccountPlayerCash, seller.ID, 100)
	e.mustLaw(seller, handlers.VillageLawRequest{Action: village.LawActionList, Code: "coffee", Qty: "100", Amount: "100"})
	l4 := e.text(`SELECT id::text FROM law_listings WHERE seller_id = $1::uuid AND item = 'coffee'`, seller.ID)
	grant(t, e.pool, application.AccountPlayerCash, buyer.ID, 20_000)
	if kind := e.refused(buyer, handlers.VillageLawRequest{Action: village.LawActionBuy, ID: l4, Code: broker.PublicCode}); kind != village.LawOverBrokerCap {
		t.Fatalf("a broker with no history holds only so much, got %q", kind)
	}
	e.verifyLaw()
}

// A scam may be traced to the broker, and then a case opens against him; the fences of the board put up the day's goods once, the price carries the premium,
// and a trade noticed by the patrol is stopped and opens a case. Nothing mature is listed while the operator switch is off.
func TestTheFencesTheBustAndTheMatureSwitch(t *testing.T) {
	e := newLawEnv(t)
	e.station(e.cityID, "constabulary")
	e.put(e.cityID, "barter_post")
	e.list("forgery")
	buyer := e.rich("A", 100_000)
	e.standing(buyer, 50)
	// the day's supply is put up once, at the premium
	e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionOpen})
	e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionOpen})
	if e.scalarInt(`SELECT count(*) FROM law_listings WHERE seller_id IS NULL AND item = 'fake_documents'`) != 1 {
		t.Fatalf("the fences put a good up once a day, got %d", e.scalarInt(`SELECT count(*) FROM law_listings WHERE seller_id IS NULL AND item = 'fake_documents'`))
	}
	if p := e.scalarInt(`SELECT price FROM law_listings WHERE seller_id IS NULL AND item = 'fake_documents'`); p != 2500+2500*3000/10_000 {
		t.Fatalf("the price carries the premium, %d", p)
	}
	// the supply and the mature rows: a mature listing is neither listed nor bought while the switch is off
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO law_listings (id, settlement_id, item, qty, price, status, mature, supply_day, created_at)
		VALUES ($1::uuid, $2::uuid, 'card_skimmer', 1, 9000, 'open', true, 999999, now())`, newUUID(t), e.cityID); err != nil {
		t.Fatal(err)
	}
	mature := e.text(`SELECT id::text FROM law_listings WHERE mature`)
	v := e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionOpen})
	for _, l := range v.Board.Listings {
		if l.ID == mature {
			t.Fatal("a mature listing was shown while the switch is off")
		}
	}
	if kind := e.refused(buyer, handlers.VillageLawRequest{Action: village.LawActionBuy, ID: mature}); kind != village.LawNoListing {
		t.Fatalf("a mature listing is not bought while the switch is off, got %q", kind)
	}
	e.mature = true
	v = e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionOpen})
	seen := false
	for _, l := range v.Board.Listings {
		seen = seen || l.ID == mature
	}
	if !seen {
		t.Fatal("with the switch on a mature listing is shown (there is still nothing explicit)")
	}
	e.mature = false
	if _, err := e.pool.Raw().Exec(testCtx(t), `DELETE FROM law_listings WHERE mature`); err != nil {
		t.Fatal(err)
	}
	// buying from a fence: the goods come, the money leaves the economy
	fence := e.text(`SELECT id::text FROM law_listings WHERE seller_id IS NULL AND item = 'fake_documents'`)
	e.retuneLaw(func(r *application.LawRules) { r.T.BustBaseBPS = 0 })
	cash := e.cash(buyer)
	e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionBuy, ID: fence, Qty: "1"})
	if e.cash(buyer) != cash-3250 {
		t.Fatalf("the buyer paid the premium price: %d", cash-e.cash(buyer))
	}
	if e.scalarInt(`SELECT count(*) FROM item_pieces WHERE owner_id = $1::uuid AND item_code = 'fake_documents'`, buyer.ID) != 1 {
		t.Fatal("the buyer holds the forged documents")
	}
	// a patrol that notices the trade opens a case: the roll is pinned to always notice
	e.retuneLaw(func(r *application.LawRules) { r.T.BustBaseBPS = 10_000 })
	e.mustLaw(buyer, handlers.VillageLawRequest{Action: village.LawActionBuy, ID: fence, Qty: "1"})
	if e.scalarInt(`SELECT count(*) FROM law_cases WHERE accused_id = $1::uuid AND offence = 'forgery' AND source = 'patrol'`, buyer.ID) != 1 {
		t.Fatal("a bust opens a case against the buyer")
	}
	if e.cash(buyer) != cash-3250 {
		t.Fatal("a bust stops the sale: no second payment")
	}
	e.verifyLaw()
}

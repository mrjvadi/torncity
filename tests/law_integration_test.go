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
	grant(e.t, e.pool, application.AccountPlayerCash, p.ID, cash)
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

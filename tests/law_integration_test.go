//go:build integration

package tests

import (
	"context"
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

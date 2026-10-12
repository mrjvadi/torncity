//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/roads"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Unions, countries, treaties of settlements, tenders and loans between polities (docs/adr/0076, plan B12), through the real handlers, the unit
// of work and the ledger. Three settlements are founded: A (the one the service-day env builds), B (the raid env's attacker) and C.

type polityEnv struct {
	*raidEnv
	rules   application.PolityRules
	cityC   string
	metaC   envelope.Metadata
	headC   *application.Player
	places  map[string]string // player id -> "A", "B" or "C"
	pnumber int
}

func newPolityEnv(t *testing.T, tune func(*application.PolityRules)) *polityEnv {
	t.Helper()
	w := newRaidEnv(t, nil)
	cfg := config.Defaults()
	rules := settlementcfg.Polity(cfg.Polity)
	rules.RuleAt = w.clock.Now().Add(-24 * time.Hour)
	rules.CountryMinResidents = 2
	rules.AssemblyDuesBoundBPS = 5000
	if tune != nil {
		tune(&rules)
	}
	w.village.WithPolity(rules)
	e := &polityEnv{raidEnv: w, rules: rules, places: map[string]string{}}

	meta, founder := w.group(t)
	foundVillage(t, w.pool, w.h, meta)
	if err := w.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&e.cityC); err != nil {
		t.Fatal(err)
	}
	e.metaC, e.headC = meta, founder
	seedTreasury(t, w.pool, e.cityC, 60_000)
	e.places[w.head.ID], e.places[w.att.ID], e.places[founder.ID] = "A", "B", "C"
	cityC := e.cityC
	t.Cleanup(func() { e.cleanupPolity(cityC) })
	return e
}

// cleanupPolity takes everything the unions, the tenders and the loans wrote away, before the settlements go.
func (e *polityEnv) cleanupPolity(cityC string) {
	e.truncatePolity()
	purgeLedgerFor(e.t, e.pool, cityC)
	purgeLedgerFor(e.t, e.pool, e.headC.ID)
	e.cleanup(cityC)
}

// truncatePolity empties the tables of the item. It is idempotent and runs before every player the test made is deleted (the votes and the
// seats name players).
func (e *polityEnv) truncatePolity() {
	ctx := testCtx(e.t)
	cityC := e.cityC
	var owners []string
	for _, q := range []string{
		`SELECT jurisdiction_id::text FROM unions`,
		`SELECT id::text FROM tenders`,
		`SELECT id::text FROM companies WHERE type_code = 'works_contractor'`,
	} {
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
	for _, stmt := range []string{
		`UPDATE jurisdictions SET parent_id = (SELECT id FROM jurisdictions WHERE kind = 'country' AND code = 'default_country') WHERE parent_id IN (SELECT jurisdiction_id FROM unions)`,
		`ALTER TABLE diplomacy_events DISABLE TRIGGER diplomacy_events_append_only`,
		`DELETE FROM diplomacy_events WHERE treaty_id IN (SELECT id FROM treaties WHERE proposer_id IN (SELECT jurisdiction_id FROM cities WHERE id IN ($1::uuid, $2::uuid, $3::uuid) UNION SELECT jurisdiction_id FROM unions) OR partner_id IN (SELECT jurisdiction_id FROM cities WHERE id IN ($1::uuid, $2::uuid, $3::uuid) UNION SELECT jurisdiction_id FROM unions))`,
		`ALTER TABLE diplomacy_events ENABLE TRIGGER diplomacy_events_append_only`,
		`DELETE FROM treaties WHERE proposer_id IN (SELECT jurisdiction_id FROM cities WHERE id IN ($1::uuid, $2::uuid, $3::uuid) UNION SELECT jurisdiction_id FROM unions) OR partner_id IN (SELECT jurisdiction_id FROM cities WHERE id IN ($1::uuid, $2::uuid, $3::uuid) UNION SELECT jurisdiction_id FROM unions)`,
		`ALTER TABLE union_audit DISABLE TRIGGER union_audit_no_change`,
		`ALTER TABLE sovereign_loan_events DISABLE TRIGGER sovereign_loan_events_no_change`,
		`DELETE FROM game_actions WHERE action_type = 'polity_tick'`,
		`TRUNCATE sovereign_loan_events, sovereign_loan_periods, sovereign_loans, tender_stages, tender_bids, tender_debarments, tenders, union_settlements, union_dues_periods,
			union_motion_votes, union_motions, union_ballot_signatures, union_ballot_votes, union_ballots, union_invites, union_proposals, union_audit, union_seats, union_offices,
			union_members, unions CASCADE`,
		`ALTER TABLE union_audit ENABLE TRIGGER union_audit_no_change`,
		`ALTER TABLE sovereign_loan_events ENABLE TRIGGER sovereign_loan_events_no_change`,
		`DELETE FROM jurisdictions WHERE kind IN ('league', 'country') AND content_version_id IS NULL`,
		`DELETE FROM settlement_claims WHERE settlement_id IN ($1::uuid, $2::uuid, $3::uuid)`,
		`DELETE FROM company_shareholders WHERE company_id IN (SELECT id FROM companies WHERE type_code = 'works_contractor')`,
		`DELETE FROM companies WHERE type_code = 'works_contractor'`,
	} {
		var err error
		if strings.Contains(stmt, "$1") {
			_, err = e.pool.Raw().Exec(ctx, stmt, e.cityID, e.attCity, cityC)
		} else {
			_, err = e.pool.Raw().Exec(ctx, stmt)
		}
		if err != nil {
			e.t.Errorf("cleanup %q: %v", stmtHead(stmt), err)
		}
	}
	for _, id := range owners {
		purgeLedgerFor(e.t, e.pool, id)
	}
}

func stmtHead(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

// metaOf is the player asking through the game client, from their own settlement's group.
func (e *polityEnv) metaOf(p *application.Player, command, action string) envelope.Metadata {
	base := e.meta
	switch e.places[p.ID] {
	case "B":
		base = e.attMeta
	case "C":
		base = e.metaC
	}
	m := clientMeta(asPlayer(base, p), command, action)
	m.IdempotencyKey = "it-" + randomToken(e.t, 16)
	m.RequestID = "req_" + randomToken(e.t, 16)
	return m
}

// resident is a person of one of the settlements, with the settlement they live in remembered.
func (e *polityEnv) resident(place string) *application.Player {
	e.t.Helper()
	city := map[string]string{"A": e.cityID, "B": e.attCity, "C": e.cityC}[place]
	p := e.person(city, 0)
	e.places[p.ID] = place
	e.t.Cleanup(e.truncatePolity)
	return p
}

func (e *polityEnv) union(p *application.Player, req handlers.VillageUnionRequest) (village.UnionView, string) {
	e.t.Helper()
	resp, err := e.village.Union(testCtx(e.t), e.metaOf(p, "settlement.union", "union"), req)
	if err != nil {
		e.t.Fatalf("union %s: %v", req.Action, err)
	}
	if resp.Screen == village.ScreenUnion {
		var v village.UnionView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatalf("decoding the union screen: %v", err)
		}
		return v, ""
	}
	kind := refusalKind(e.t, resp.View)
	if kind == "" {
		e.t.Fatalf("union %s answered %s", req.Action, resp.Screen)
	}
	return village.UnionView{}, kind
}

// mustUnion does an act of the union screen and fails the test if it was refused.
func (e *polityEnv) mustUnion(p *application.Player, req handlers.VillageUnionRequest) village.UnionView {
	e.t.Helper()
	v, kind := e.union(p, req)
	if kind != "" {
		e.t.Fatalf("union %s was refused: %s", req.Action, kind)
	}
	return v
}

// tick sends one deadline of the item as the scheduler would.
func (e *polityEnv) tick(kind, id, aux string) {
	e.t.Helper()
	payload, _ := json.Marshal(handlers.PolityTickPayload{Kind: kind, ID: id, Aux: aux})
	if _, err := e.village.PolityTick(testCtx(e.t), e.sched("polity.tick"), handlers.CrimeScheduledRequest{Payload: payload}); err != nil {
		e.t.Fatalf("tick %s: %v", kind, err)
	}
}

func (e *polityEnv) scalarInt(q string, args ...any) int64 {
	e.t.Helper()
	var n int64
	if err := e.pool.Raw().QueryRow(testCtx(e.t), q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *polityEnv) text(q string, args ...any) string {
	e.t.Helper()
	var s string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), q, args...).Scan(&s); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return s
}

// earn gives a settlement's treasury income at the test clock's instant, so a dues period sees it.
func (e *polityEnv) earn(city string, amount int64) {
	e.t.Helper()
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonAdminGrant, CreatedAt: e.clock.Now(), Entries: []application.LedgerEntry{
			{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-amount)}, {AccountID: acct.ID, Amount: money.FromMinor(amount)}}})
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *polityEnv) verifyPolity() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Polity {
		e.t.Fatal("migration 0154 is not applied")
	}
	if s.UnionDuesLedger != s.UnionDuesRows || s.UnionPayoutLedger != s.UnionPayoutRows || s.TenderEscrowBroken != 0 || s.DebtDisbursedLedger != s.DebtDisbursedRows ||
		s.DebtRepaidLedger != s.DebtRepaidRows || s.DebtPenaltyLedger != s.DebtFeesRows || s.CountryParentBroken != 0 || s.UnionNoManager != 0 || s.UnionCeilingBroken != 0 ||
		s.UnionNoFounded != 0 || s.SettlementTwoCountries != 0 {
		e.t.Errorf("the polity invariants do not hold: %+v", s)
	}
}

// managerOffice is a founding charter with one office that can run the union and does what the tests need, held by the given players.
func managerOffice(grants []string, holders ...*application.Player) json.RawMessage {
	codes := make([]string, 0, len(holders))
	for _, h := range holders {
		codes = append(codes, h.PublicCode)
	}
	all := append([]string{"office.edit", "office.create", "office.appoint", "office.dismiss", "charter.amend", "union.admit", "union.expel", "treasury.spend", "notice.post", "works.tender", "debt.lend", "debt.borrow", "debt.restructure"}, grants...)
	raw, _ := json.Marshal([]map[string]any{{"title": "Council", "seats": 3, "grants": all, "holders": codes}})
	return raw
}

func (e *polityEnv) proposeLeague(by *application.Player, name, delegations string, invite string, holders ...*application.Player) string {
	e.t.Helper()
	e.mustUnion(by, handlers.VillageUnionRequest{Action: village.UnionActionPropose, Name: name, Kind: "league", Mode: "double_majority", Exit: "free", Delegations: delegations,
		DuesFixed: "100", DuesShare: "1000", Invite: invite, Offices: managerOffice(grantsFor(delegations), holders...)})
	return e.text(`SELECT id::text FROM union_proposals WHERE name = $1 AND status = 'pending'`, name)
}

func (e *polityEnv) codeOf(city string) string {
	return e.text(`SELECT code FROM cities WHERE id = $1::uuid`, city)
}

func (e *polityEnv) unionIDByName(name string) string {
	return e.text(`SELECT id::text FROM unions WHERE name = $1`, name)
}

func (e *polityEnv) unionStatus(proposal string) string {
	return e.text(`SELECT status FROM union_proposals WHERE id = $1::uuid`, proposal)
}

var _ = fmt.Sprint

// A union binds the settlements that signed, each by its own charter's rule: the holder of union.ratify in a settlement signs for it, a
// resident without the power cannot, a league enters into force at two, and the third (who never signed) is not a member (ADR 0076 4.2).
func TestAUnionIsRatifiedByEachMembersOwnRuleAndBindsOnlyTheSignatories(t *testing.T) {
	e := newPolityEnv(t, nil)
	before := e.cityCash(e.cityID)
	prop := e.proposeLeague(e.head, "Northern League", "roads,treaty", e.codeOf(e.attCity)+","+e.codeOf(e.cityC), e.head)
	if got := e.cityCash(e.cityID); got != before-e.rules.LeagueFee {
		t.Fatalf("the proposer pays the filing fee into the sink: %d -> %d, fee %d", before, got, e.rules.LeagueFee)
	}
	if e.unionStatus(prop) != "pending" {
		t.Fatal("a proposal waits for the answers")
	}
	// a resident of B holds no office: B's own charter gives him no say
	bystander := e.resident("B")
	if _, kind := e.union(bystander, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop}); kind != village.VillageNotOfficeHolder {
		t.Fatalf("a resident without union.ratify must be refused, got %q", kind)
	}
	// A's head signs: one of three, a league needs two
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop})
	if e.unionStatus(prop) != "pending" {
		t.Fatal("one signature is not enough for a league")
	}
	// B's head signs: in force
	e.mustUnion(e.att, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop})
	if e.unionStatus(prop) != "in_force" {
		t.Fatalf("two signatures put a league into force, the proposal is %s", e.unionStatus(prop))
	}
	id := e.unionIDByName("Northern League")
	members := e.scalarInt(`SELECT count(*) FROM union_members WHERE union_id = $1::uuid AND status = 'active'`, id)
	if members != 2 {
		t.Fatalf("the signatories are the members, got %d", members)
	}
	if e.scalarInt(`SELECT count(*) FROM union_members WHERE union_id = $1::uuid AND settlement_id = $2::uuid`, id, e.cityC) != 0 {
		t.Fatal("C never signed and is not a member")
	}
	// C's late signature finds the proposal decided
	if _, kind := e.union(e.headC, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop}); kind != village.UnionDecided {
		t.Fatalf("a signature after the agreement is refused, got %q", kind)
	}
	// a league leaves the tree alone: A still sits under the commons
	if e.text(`SELECT j.parent_id::text FROM jurisdictions j JOIN cities c ON c.jurisdiction_id = j.id WHERE c.id = $1::uuid`, e.cityID) == e.text(`SELECT jurisdiction_id::text FROM unions WHERE id = $1::uuid`, id) {
		t.Fatal("a league must not reparent its members")
	}
	e.verifyPolity()
}

// countryOffice is the founding charter of a country: war, treaties and courts handed over, a council that holds the powers of state.
func countryOffice(holders ...*application.Player) json.RawMessage {
	codes := make([]string, 0, len(holders))
	for _, h := range holders {
		codes = append(codes, h.PublicCode)
	}
	raw, _ := json.Marshal([]map[string]any{{"title": "Government", "seats": 3, "holders": codes, "grants": []string{"office.edit", "office.create", "office.appoint", "office.dismiss",
		"charter.amend", "treasury.spend", "war.declare", "war.peace", "treaty.sign", "treaty.propose", "treaty.end", "military.command", "military.procure", "court.judge", "union.admit", "union.expel"}}})
	return raw
}

func (e *polityEnv) countryOf(p *application.Player) string {
	e.t.Helper()
	var got map[string]string
	err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(e.t), func(ctx context.Context, tx application.Tx) error {
		var err error
		got, err = tx.Diplomacy().CountriesOfPlayers(ctx, []string{p.ID})
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return got[p.ID]
}

// A country needs the delegations of a country; the deep ones (war, courts, money, customs, defence) also go to each member's own assembly;
// when it enters into force its members sit under it and the existing diplomacy and war code finds their country (ADR 0076 4.2, 5.1).
func TestACountryNeedsEachAssemblyAndSitsItsMembersUnderIt(t *testing.T) {
	e := newPolityEnv(t, nil)
	e.clock.Advance(4 * 24 * time.Hour) // residents vote after they have lived in the settlement for the days the charter asks
	// a country without the delegations of a country is refused
	if _, kind := e.union(e.head, handlers.VillageUnionRequest{Action: village.UnionActionPropose, Name: "Weak State", Kind: "country", Delegations: "roads", Invite: e.codeOf(e.attCity),
		Offices: countryOffice(e.head)}); kind != village.UnionNotCountry {
		t.Fatalf("a country must take war, treaties and courts: %q", kind)
	}
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionPropose, Name: "Free State", Kind: "country", Mode: "double_majority", Exit: "free",
		Delegations: "war,treaty,courts,defence,roads", Invite: e.codeOf(e.attCity), Offices: countryOffice(e.head)})
	prop := e.text(`SELECT id::text FROM union_proposals WHERE name = 'Free State'`)
	// A's head asks his assembly: the signature alone is not the answer
	view := e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop})
	if view.Notice != village.UnionNoticeAssembly {
		t.Fatalf("a deep delegation goes to the assembly, the notice was %q", view.Notice)
	}
	if e.scalarInt(`SELECT count(*) FROM union_invites WHERE proposal_id = $1::uuid AND decision = 'assembly'`, prop) != 1 {
		t.Fatal("A's invitation waits for its people")
	}
	ballot := e.text(`SELECT id::text FROM union_ballots WHERE proposal_id = $1::uuid AND settlement_id = $2::uuid`, prop, e.cityID)
	// a resident of B has no vote in A's assembly
	outsider := e.resident("B")
	if _, kind := e.union(outsider, handlers.VillageUnionRequest{Action: village.UnionActionVote, ID: ballot, Choice: "yes"}); kind != village.UnionNotEligible {
		t.Fatalf("only A's residents vote in A's assembly: %q", kind)
	}
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionVote, ID: ballot, Choice: "yes"})
	// the only resident has voted: the count is not waited for
	if got := e.text(`SELECT decision FROM union_invites WHERE proposal_id = $1::uuid AND settlement_id = $2::uuid`, prop, e.cityID); got != "yes" {
		t.Fatalf("the assembly of A carried: %q", got)
	}
	if e.unionStatus(prop) != "pending" {
		t.Fatal("a country waits for every invited settlement")
	}
	e.mustUnion(e.att, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop})
	ballotB := e.text(`SELECT id::text FROM union_ballots WHERE proposal_id = $1::uuid AND settlement_id = $2::uuid`, prop, e.attCity)
	// B's people refuse
	e.mustUnion(e.att, handlers.VillageUnionRequest{Action: village.UnionActionVote, ID: ballotB, Choice: "no"})
	// the other resident of B has not voted: the count waits for the closing of the ballot
	e.clock.Advance(e.rules.VoteWindow + time.Hour)
	e.tick(application.TickBallot, ballotB, "")
	if e.unionStatus(prop) != "refused" {
		t.Fatalf("one refusal ends a country, the proposal is %s", e.unionStatus(prop))
	}
	if e.scalarInt(`SELECT count(*) FROM unions WHERE name = 'Free State'`) != 0 {
		t.Fatal("a refused country is not founded")
	}
	// a second try that both assemblies carry
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionPropose, Name: "Free State Two", Kind: "country", Mode: "double_majority", Exit: "free",
		Delegations: "war,treaty,courts,defence", Invite: e.codeOf(e.attCity), Offices: countryOffice(e.head)})
	prop2 := e.text(`SELECT id::text FROM union_proposals WHERE name = 'Free State Two'`)
	for _, who := range []*application.Player{e.head, e.att} {
		e.mustUnion(who, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop2})
	}
	for _, c := range []struct {
		who  *application.Player
		city string
	}{{e.head, e.cityID}, {e.att, e.attCity}, {outsider, e.attCity}} {
		b := e.text(`SELECT id::text FROM union_ballots WHERE proposal_id = $1::uuid AND settlement_id = $2::uuid`, prop2, c.city)
		e.mustUnion(c.who, handlers.VillageUnionRequest{Action: village.UnionActionVote, ID: b, Choice: "yes"})
	}
	if e.unionStatus(prop2) != "in_force" {
		t.Fatalf("both assemblies carried: %s", e.unionStatus(prop2))
	}
	id := e.unionIDByName("Free State Two")
	jid := e.text(`SELECT jurisdiction_id::text FROM unions WHERE id = $1::uuid`, id)
	for _, p := range []*application.Player{e.head, e.att} {
		if got := e.countryOf(p); got != jid {
			t.Fatalf("a member's people belong to the new country: %s, want %s", got, jid)
		}
	}
	if e.countryOf(e.headC) == jid {
		t.Fatal("C did not sign and stays under the commons")
	}
	// the country is a country: it is a party the war and treaty code can find, with no content office behind it
	if e.scalarInt(`SELECT count(*) FROM jurisdictions WHERE id = $1::uuid AND kind = 'country' AND content_version_id IS NULL`, jid) != 1 {
		t.Fatal("the union's jurisdiction is a country the players founded")
	}
	// a second country for the same settlement is refused while the first stands
	if _, kind := e.union(e.head, handlers.VillageUnionRequest{Action: village.UnionActionPropose, Name: "Third State", Kind: "country", Delegations: "war,treaty,courts,defence",
		Invite: e.codeOf(e.cityC), Offices: countryOffice(e.head)}); kind != village.UnionOneCountry {
		t.Fatalf("a settlement belongs to one country at a time: %q", kind)
	}
	e.verifyPolity()
}

func (e *polityEnv) office(p *application.Player, unionID, officeID, title string, grants []string) (village.UnionView, string) {
	gs := make([]handlers.VillageCharterGrant, 0, len(grants))
	for _, g := range grants {
		gs = append(gs, handlers.VillageCharterGrant{Permission: g})
	}
	return e.union(p, handlers.VillageUnionRequest{Action: village.UnionActionOffice, ID: unionID, Office: officeID, Title: title, Seats: 2, Grants: gs})
}

// formLeague founds a league of A and B (the proposer holds the council) and returns its id.
func (e *polityEnv) formLeague(name, delegations string, extraHolders ...*application.Player) string {
	e.t.Helper()
	holders := append([]*application.Player{e.head}, extraHolders...)
	prop := e.proposeLeague(e.head, name, delegations, e.codeOf(e.attCity), holders...)
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop})
	e.mustUnion(e.att, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop})
	if e.unionStatus(prop) != "in_force" {
		e.t.Fatalf("the league %q did not enter into force: %s", name, e.unionStatus(prop))
	}
	return e.unionIDByName(name)
}

// The ceiling of a union's offices is what the members delegated; nobody grants a power they do not hold; the union always keeps a held
// office manager; a holder is recalled by the residents of the members (ADR 0076 4.3, 4.6).
func TestTheCeilingTheRailsAndRecallOfAUnionCharter(t *testing.T) {
	e := newPolityEnv(t, nil)
	e.clock.Advance(4 * 24 * time.Hour)
	id := e.formLeague("Rails League", "roads,treaty")

	// D1: war was never delegated, so no office of the union may hold it
	if _, kind := e.office(e.head, id, "", "Warlords", []string{"war.declare"}); kind != village.UnionCeiling {
		t.Fatalf("an office with a power nobody delegated must break the ceiling, got %q", kind)
	}
	// a power in a class that was delegated is fine
	if _, kind := e.office(e.head, id, "", "Surveyors", []string{"road.draw"}); kind != "" {
		t.Fatalf("roads were delegated: %q", kind)
	}
	// R1: an office that can make offices but cannot spend cannot make an office that spends
	_, kind := e.office(e.head, id, "", "Clerks", []string{"office.create", "office.edit", "notice.post"})
	if kind != "" {
		t.Fatalf("clerks: %q", kind)
	}
	clerk := e.resident("B")
	clerksOffice := e.text(`SELECT id::text FROM union_offices WHERE union_id = $1::uuid AND title = 'Clerks'`, id)
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionAppoint, ID: id, Office: clerksOffice, Player: clerk.PublicCode})
	if _, kind := e.office(clerk, id, "", "Treasurers", []string{"treasury.spend"}); kind == "" {
		t.Fatal("nobody grants what they do not hold: the clerk holds no treasury.spend")
	}
	if e.scalarInt(`SELECT count(*) FROM union_offices WHERE union_id = $1::uuid AND title = 'Treasurers'`, id) != 0 {
		t.Fatal("the refused office was written")
	}
	// R2: the only manager cannot be closed, and cannot be dismissed
	council := e.text(`SELECT id::text FROM union_offices WHERE union_id = $1::uuid AND title = 'Council'`, id)
	if _, kind := e.union(e.head, handlers.VillageUnionRequest{Action: village.UnionActionCloseOffice, ID: id, Office: council}); kind == "" {
		t.Fatal("the union must keep an office manager: closing the council is refused")
	}
	if _, kind := e.union(e.head, handlers.VillageUnionRequest{Action: village.UnionActionDismiss, ID: id, Office: council, Player: e.head.PublicCode}); kind == "" {
		t.Fatal("the union must keep a held manager: dismissing the only one is refused")
	}
	// a second manager (B's head) makes room: then the first may be dismissed
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionAppoint, ID: id, Office: council, Player: e.att.PublicCode})
	e.clock.Advance(6 * 24 * time.Hour) // the holder has served long enough to be recalled
	// recall of B's head: the residents of both settlements sign and vote
	voters := []*application.Player{e.head, e.att, e.resident("A"), e.resident("B")}
	for i, v := range voters[:3] {
		view := e.mustUnion(v, handlers.VillageUnionRequest{Action: village.UnionActionRecall, ID: id, Office: council, Player: e.att.PublicCode})
		wantNotice := village.UnionNoticeSigned
		if i == 2 {
			wantNotice = village.UnionNoticeRecall
		}
		if view.Notice != wantNotice {
			t.Fatalf("signature %d: notice %q, want %q", i+1, view.Notice, wantNotice)
		}
	}
	ballot := e.text(`SELECT id::text FROM union_ballots WHERE union_id = $1::uuid AND kind = 'recall' AND status = 'open'`, id)
	for i, v := range voters {
		choice := "yes"
		if i == 3 {
			choice = "no"
		}
		e.mustUnion(v, handlers.VillageUnionRequest{Action: village.UnionActionVote, ID: ballot, Choice: choice})
	}
	// the clerk has not voted: the count waits for the closing of the ballot
	e.clock.Advance(time.Duration(72)*time.Hour + time.Hour)
	e.tick(application.TickBallot, ballot, "")
	if e.scalarInt(`SELECT count(*) FROM union_seats s JOIN union_offices o ON o.id = s.office_id WHERE o.union_id = $1::uuid AND s.holder_id = $2::uuid AND s.until IS NULL`, id, e.att.ID) != 0 {
		t.Fatal("the recalled holder has left the council")
	}
	if e.scalarInt(`SELECT count(*) FROM union_audit WHERE union_id = $1::uuid AND action = 'recalled'`, id) != 1 {
		t.Fatal("the recall is in the log")
	}
	// every act of the charter is in the log
	for _, action := range []string{"founded", "office_created", "seat_filled", "recall_vote_opened"} {
		if e.scalarInt(`SELECT count(*) FROM union_audit WHERE union_id = $1::uuid AND action = $2`, id, action) == 0 {
			t.Errorf("the log has no %q line", action)
		}
	}
	// the last manager still holds: the recall of the only one left would be void
	e.verifyPolity()
}

// The members decide between themselves by the union's rule (here the double majority), admit a settlement that then joins by its own act,
// pay dues out of their income, expel one who is voted out, and a member leaves after its notice with its share of the purse, nothing else
// moving (ADR 0076 4.4, 4.5).
func TestMembersVoteAdmitPayDuesAndLeaveAfterTheNotice(t *testing.T) {
	e := newPolityEnv(t, nil)
	e.clock.Advance(4 * 24 * time.Hour)
	id := e.formLeague("Dues League", "dues,roads")

	// admit C: the members vote, then C joins by its own act; C cannot join before the vote passes
	if _, kind := e.union(e.headC, handlers.VillageUnionRequest{Action: village.UnionActionAccede, ID: id}); kind != village.UnionNotAdmitted {
		t.Fatalf("a settlement the members have not admitted cannot join: %q", kind)
	}
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionMotion, ID: id, Kind: "admit", Code: e.codeOf(e.cityC)})
	motion := e.text(`SELECT id::text FROM union_motions WHERE union_id = $1::uuid AND kind = 'admit' AND status = 'open'`, id)
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionMotionVote, ID: motion, Choice: "yes"})
	if _, kind := e.union(e.att, handlers.VillageUnionRequest{Action: village.UnionActionMotionVote, ID: motion, Choice: "yes"}); kind != "" {
		t.Fatalf("B's vote: %q", kind)
	}
	if _, kind := e.union(e.att, handlers.VillageUnionRequest{Action: village.UnionActionMotionVote, ID: motion, Choice: "no"}); kind != village.UnionDecided {
		t.Fatalf("the motion was decided when every member had voted: %q", kind)
	}
	e.mustUnion(e.headC, handlers.VillageUnionRequest{Action: village.UnionActionAccede, ID: id})
	if e.scalarInt(`SELECT count(*) FROM union_members WHERE union_id = $1::uuid AND status = 'active'`, id) != 3 {
		t.Fatal("C joined")
	}

	// dues: a fixed sum and a share of the income, never more than the cap, once a period for each member
	e.earn(e.cityID, 5000)
	e.earn(e.attCity, 3000)
	purse := e.text(`SELECT jurisdiction_id::text FROM unions WHERE id = $1::uuid`, id)
	e.clock.Advance(e.rules.DuesPeriod + time.Hour)
	aBefore, bBefore := e.cityCash(e.cityID), e.cityCash(e.attCity)
	e.tick(application.TickDues, id, "")
	e.tick(application.TickDues, id, "") // a replayed deadline charges nobody twice
	if got := aBefore - e.cityCash(e.cityID); got != 500 {
		t.Fatalf("A pays 100 + 10%% of 5000 = 600, capped at 10%% of its income = 500, paid %d", got)
	}
	if got := bBefore - e.cityCash(e.attCity); got != 300 {
		t.Fatalf("B pays 100 + 300 = 400, capped at 300, paid %d", got)
	}
	if n := e.scalarInt(`SELECT count(*) FROM union_dues_periods WHERE union_id = $1::uuid`, id); n != 3 {
		t.Fatalf("one dues row per member and period, got %d", n)
	}
	if got := e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'state_treasury' AND owner_id = $1::uuid`, purse); got != 800 {
		t.Fatalf("the purse holds what was paid, got %d", got)
	}
	e.verifyPolity()

	// the members vote C out: the first motion fails (half the members is not 55 percent), the second carries
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionMotion, ID: id, Kind: "expel", Code: e.codeOf(e.cityC)})
	m1 := e.text(`SELECT id::text FROM union_motions WHERE union_id = $1::uuid AND kind = 'expel' AND status = 'open'`, id)
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionMotionVote, ID: m1, Choice: "yes"})
	e.mustUnion(e.att, handlers.VillageUnionRequest{Action: village.UnionActionMotionVote, ID: m1, Choice: "no"})
	if e.text(`SELECT status FROM union_members WHERE union_id = $1::uuid AND settlement_id = $2::uuid`, id, e.cityC) != "active" {
		t.Fatal("half the members is not a double majority: C stays")
	}
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionMotion, ID: id, Kind: "expel", Code: e.codeOf(e.cityC)})
	m2 := e.text(`SELECT id::text FROM union_motions WHERE union_id = $1::uuid AND kind = 'expel' AND status = 'open'`, id)
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionMotionVote, ID: m2, Choice: "yes"})
	e.mustUnion(e.att, handlers.VillageUnionRequest{Action: village.UnionActionMotionVote, ID: m2, Choice: "yes"})
	if e.text(`SELECT status FROM union_members WHERE union_id = $1::uuid AND settlement_id = $2::uuid`, id, e.cityC) != "expelled" {
		t.Fatal("two of two voted C out")
	}

	// B leaves: its notice runs 14 days; before that it is still a member, after that it takes its share of the purse (its dues over all dues)
	e.mustUnion(e.att, handlers.VillageUnionRequest{Action: village.UnionActionLeave, ID: id})
	e.tick(application.TickExit, id, e.attCity)
	if e.text(`SELECT status FROM union_members WHERE union_id = $1::uuid AND settlement_id = $2::uuid`, id, e.attCity) != "notice" {
		t.Fatal("a member is not out before its notice has run")
	}
	e.clock.Advance(time.Duration(e.rules.ExitNoticeDays)*24*time.Hour - time.Hour)
	e.tick(application.TickExit, id, e.attCity)
	if e.text(`SELECT status FROM union_members WHERE union_id = $1::uuid AND settlement_id = $2::uuid`, id, e.attCity) != "notice" {
		t.Fatal("an hour short of the notice the member is still in")
	}
	bMid := e.cityCash(e.attCity)
	aMid := e.cityCash(e.cityID)
	e.clock.Advance(2 * time.Hour)
	e.tick(application.TickExit, id, e.attCity)
	if got := e.text(`SELECT status FROM union_members WHERE union_id = $1::uuid AND settlement_id = $2::uuid`, id, e.attCity); got != "left" {
		t.Fatalf("after the notice the member is out: %s", got)
	}
	if got := e.cityCash(e.attCity) - bMid; got != 300 {
		t.Fatalf("B takes 800 x 300 / 800 = 300 of the purse, got %d", got)
	}
	// only A is left: a league of one is dissolved and the rest of the purse goes to the last member; nothing is deleted
	if e.text(`SELECT status FROM unions WHERE id = $1::uuid`, id) != "dissolved" {
		t.Fatal("a league that falls below two members ends")
	}
	if got := e.cityCash(e.cityID) - aMid; got != 500 {
		t.Fatalf("A gets the rest of the purse, got %d", got)
	}
	if got := e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'state_treasury' AND owner_id = $1::uuid`, purse); got != 0 {
		t.Fatalf("a dissolved union's purse is empty: %d", got)
	}
	e.verifyPolity()
}

// grantsFor are the powers of the delegated classes a council may hold.
func grantsFor(delegations string) []string {
	var out []string
	for _, d := range strings.Split(delegations, ",") {
		switch strings.TrimSpace(d) {
		case "treaty":
			out = append(out, "treaty.sign", "treaty.propose", "treaty.end")
		case "roads":
			out = append(out, "road.draw")
		}
	}
	return out
}

func (e *polityEnv) treaty(p *application.Player, req handlers.VillageTreatyRequest) (village.SettlementTreatyView, string) {
	e.t.Helper()
	resp, err := e.village.Treaty(testCtx(e.t), e.metaOf(p, "settlement.treaty", "treaty"), req)
	if err != nil {
		e.t.Fatalf("treaty %s: %v", req.Action, err)
	}
	if resp.Screen == village.ScreenTreaty {
		var v village.SettlementTreatyView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatal(err)
		}
		return v, ""
	}
	return village.SettlementTreatyView{}, refusalKind(e.t, resp.View)
}

func (e *polityEnv) mustTreaty(p *application.Player, req handlers.VillageTreatyRequest) village.SettlementTreatyView {
	e.t.Helper()
	v, kind := e.treaty(p, req)
	if kind != "" {
		e.t.Fatalf("treaty %s was refused: %s", req.Action, kind)
	}
	return v
}

// signTreaty has one settlement propose a kind of treaty to another and the other accept it; it returns the treaty's number.
func (e *polityEnv) signTreaty(from *application.Player, to *application.Player, toCode, kind string) string {
	e.t.Helper()
	e.mustTreaty(from, handlers.VillageTreatyRequest{Action: village.TreatyActionPropose, Kind: kind, Code: toCode})
	no := e.text(`SELECT no::text FROM treaties WHERE kind = $1 AND status = 'proposed' ORDER BY no DESC LIMIT 1`, kind)
	e.mustTreaty(to, handlers.VillageTreatyRequest{Action: village.TreatyActionAccept, No: no})
	return no
}

// A treaty a party broke cools that party: for the cooling days it may not raid the one it left, while the one it left may. A treaty of
// defence between two settlements keeps them from raiding each other (ADR 0076 7.2).
func TestATreatyBreakCoolsTheBreakerAndRefusesWarAndRaids(t *testing.T) {
	e := newPolityEnv(t, nil)
	codeA, codeB := e.codeOf(e.cityID), e.codeOf(e.attCity)
	no := e.signTreaty(e.head, e.att, codeB, "alliance")
	if e.text(`SELECT status FROM treaties WHERE no = $1::bigint`, no) != "active" {
		t.Fatal("the alliance is in force")
	}
	// allies do not raid each other, either way
	_, resp := e.raidAs(e.head, false, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: codeB})
	raidRefused(t, resp, village.RaidTreaty)
	_, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: codeA})
	raidRefused(t, resp, village.RaidTreaty)
	// A ends the alliance: it is on the public record, with the party that ended it
	e.mustTreaty(e.head, handlers.VillageTreatyRequest{Action: village.TreatyActionEnd, No: no})
	if e.text(`SELECT status FROM treaties WHERE no = $1::bigint`, no) != "terminated" || e.text(`SELECT COALESCE(ended_party::text, '') FROM treaties WHERE no = $1::bigint`, no) !=
		e.text(`SELECT jurisdiction_id::text FROM cities WHERE id = $1::uuid`, e.cityID) {
		t.Fatal("the treaty ended, by A")
	}
	if e.scalarInt(`SELECT count(*) FROM diplomacy_events WHERE kind = 'treaty_terminated' AND treaty_id = (SELECT id FROM treaties WHERE no = $1::bigint)`, no) != 1 {
		t.Fatal("the break is on the public record")
	}
	// the 7-day cooling-off: A may not raid B; B, who was left, may
	_, resp = e.raidAs(e.head, false, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: codeB})
	raidRefused(t, resp, village.RaidCooling)
	_, resp = e.raidAs(e.att, true, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: codeA})
	if resp.Screen != village.ScreenRaid {
		t.Fatalf("the one who was left may raid: %s", resp.Screen)
	}
	e.clock.Advance(7*24*time.Hour + time.Hour)
	_, resp = e.raidAs(e.head, false, handlers.VillageRaidRequest{Action: village.RaidActionFile, Code: codeB})
	if k := refusalKind(t, resp.View); k == village.RaidCooling || k == village.RaidTreaty {
		t.Fatalf("after the cooling A is free to act, got %q", k)
	}
	// ending a treaty twice, or ending another's: refused
	if _, kind := e.treaty(e.head, handlers.VillageTreatyRequest{Action: village.TreatyActionEnd, No: no}); kind != village.TreatyState {
		t.Fatalf("an ended treaty cannot end again: %q", kind)
	}
	if _, kind := e.treaty(e.headC, handlers.VillageTreatyRequest{Action: village.TreatyActionEnd, No: no}); kind != village.TreatyNotParty {
		t.Fatalf("a third party cannot end it: %q", kind)
	}
}

// A road over another settlement's claim needs a passage treaty with it (ADR 0042 4.10, 0076 7.3).
func TestAWayOverAnotherSettlementsClaimNeedsAPassageTreaty(t *testing.T) {
	e := newPolityEnv(t, nil)
	cfg := config.Defaults()
	mining := settlementcfg.Mining(cfg.Mining)
	mining.RuleAt = e.clock.Now().Add(-48 * time.Hour)
	mining.From = mining.RuleAt
	e.village.WithMining(mining)
	w, _ := e.foundingEnv.world(t)
	var tiles []worldgen.TileID
	e.village.WithRouter(func(ctx context.Context, w *worldgen.World, from, to int32, class roads.Class) (roads.Route, error) {
		pt := w.Cells[from].Point
		t0 := w.TileOfLatLon(pt.LatDeg, pt.LonDeg)
		out := []worldgen.TileID{t0}
		for i := 0; i < 8; i++ {
			t0 = w.Offset(t0, 1, 0)
			out = append(out, t0)
		}
		tiles = out
		return roads.Route{Tiles: out, LengthM: 8 * w.TileLengthM()}, nil
	})
	_ = w
	link := func() string {
		resp, err := e.village.Haul(testCtx(t), e.as(e.head, "settlement.haul", "haul"), handlers.VillageHaulRequest{Action: village.HaulActionLink, Code: e.codeOf(e.attCity), N: "path"})
		if err != nil {
			t.Fatal(err)
		}
		return refusalKind(t, resp.View)
	}
	if k := link(); k == village.HaulNoPassage {
		t.Fatal("nobody holds those tiles: no passage is needed")
	}
	if len(tiles) == 0 {
		t.Fatal("the router was not asked")
	}
	face, gx, gy := tiles[3].Parts()
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_claims (id, settlement_id, tile_face, tile_gx, tile_gy, claimed_by, claimed_at, paid_through, ledger_transaction_id)
		VALUES (gen_random_uuid(), $1::uuid, $2, $3, $4, $5::uuid, now(), now() + interval '30 days', gen_random_uuid())`, e.cityC, face, gx, gy, e.headC.ID); err != nil {
		t.Fatal(err)
	}
	if k := link(); k != village.HaulNoPassage {
		t.Fatalf("C holds a tile of the way and A has no passage with C: %q", k)
	}
	e.signTreaty(e.head, e.headC, e.codeOf(e.cityC), "passage")
	if k := link(); k == village.HaulNoPassage {
		t.Fatal("with a passage treaty the way is open")
	}
	// a passage treaty with B does not open C's land
	e.mustTreaty(e.head, handlers.VillageTreatyRequest{Action: village.TreatyActionEnd, No: e.text(`SELECT no::text FROM treaties WHERE kind = 'passage' AND status = 'active' ORDER BY no DESC LIMIT 1`)})
	if k := link(); k != village.HaulNoPassage {
		t.Fatalf("an ended passage treaty closes the way again: %q", k)
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_claims WHERE settlement_id = $1::uuid`, e.cityC); err != nil {
		t.Fatal(err)
	}
}

// foundCountryOfOne founds a country of one settlement by the shortcut of ADR 0044: the settlement proposes alone, its assembly (every
// resident in the list) votes yes, and it returns the union's id.
func (e *polityEnv) foundCountryOfOne(head *application.Player, name string, voters ...*application.Player) string {
	e.t.Helper()
	e.mustUnion(head, handlers.VillageUnionRequest{Action: village.UnionActionPropose, Name: name, Kind: "country", Mode: "double_majority", Exit: "free",
		Delegations: "war,treaty,courts,defence", Offices: countryOffice(head)})
	prop := e.text(`SELECT id::text FROM union_proposals WHERE name = $1 AND status = 'pending'`, name)
	e.mustUnion(head, handlers.VillageUnionRequest{Action: village.UnionActionRatify, ID: prop})
	ballot := e.text(`SELECT id::text FROM union_ballots WHERE proposal_id = $1::uuid AND status = 'open'`, prop)
	for _, v := range voters {
		e.mustUnion(v, handlers.VillageUnionRequest{Action: village.UnionActionVote, ID: ballot, Choice: "yes"})
	}
	if e.unionStatus(prop) != "in_force" {
		e.t.Fatalf("the country %q is not in force: %s", name, e.unionStatus(prop))
	}
	return e.unionIDByName(name)
}

// A player country's war is declared by its charter's holder of war.declare; a country that ended an alliance lately cannot declare war on
// the one it left until the cooling-off has run (ADR 0076 7.2, 8).
func TestAWarOfAPlayerCountryNeedsItsCharterAndTheCoolingOffAfterABrokenAlliance(t *testing.T) {
	e := newPolityEnv(t, nil)
	e.clock.Advance(4 * 24 * time.Hour)
	ra, rb := e.resident("A"), e.resident("B")
	alpha := e.foundCountryOfOne(e.head, "Alpha", e.head, ra)
	beta := e.foundCountryOfOne(e.att, "Beta", e.att, rb)
	alphaJ := e.text(`SELECT jurisdiction_id::text FROM unions WHERE id = $1::uuid`, alpha)
	betaJ := e.text(`SELECT jurisdiction_id::text FROM unions WHERE id = $1::uuid`, beta)
	t.Cleanup(func() { purgeWar(t, e.pool, []string{alphaJ, betaJ}, "") })

	cat, _ := e.foundingEnv.world(t)
	_ = cat
	snap := loadTestContent(t)
	uow := postgres.NewUnitOfWork(e.pool, testDefaultLanguage)
	cities := postgres.NewCityRepository(e.pool)
	policy := postgres.NewPolicyReader(e.pool, e.clock.Now)
	wars := handlers.NewWarHandler(uow, workIDs{t}, nil, registryOf(snap), cities, policy, gametime.Scale(60), handlers.WarRules{
		DeclarationNotice: time.Hour, ProposalTTL: 48 * time.Hour, EndedShownFor: 168 * time.Hour, BoardOperations: 8, NoticeCap: 50}, time.Hour, e.clock.Now).
		WithCooling(e.rules.TreatyCooling)
	declare := func(by *application.Player, target string) *presentation.Response {
		resp, err := wars.Declare(testCtx(t), e.metaOf(by, "war.declare", "declare"), handlers.WarRequest{Target: target, Ground: "aggression", Confirm: "yes"})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	betaCode := e.text(`SELECT code FROM jurisdictions WHERE id = $1::uuid`, betaJ)
	alphaCode := e.text(`SELECT code FROM jurisdictions WHERE id = $1::uuid`, alphaJ)

	// the alliance of two countries is signed through their charters (each side's holder of the treaty powers)
	e.mustTreaty(e.head, handlers.VillageTreatyRequest{Action: village.TreatyActionPropose, Union: alpha, ToUnion: beta, Kind: "alliance"})
	no := e.text(`SELECT no::text FROM treaties WHERE kind = 'alliance' AND status = 'proposed'`)
	e.mustTreaty(e.att, handlers.VillageTreatyRequest{Action: village.TreatyActionAccept, Union: beta, No: no})
	// a resident without the power cannot sign for the country
	if _, kind := e.treaty(ra, handlers.VillageTreatyRequest{Action: village.TreatyActionEnd, Union: alpha, No: no}); kind == "" {
		t.Fatal("a resident of Alpha who holds no office of the charter cannot end its treaty")
	}
	e.mustTreaty(e.head, handlers.VillageTreatyRequest{Action: village.TreatyActionEnd, Union: alpha, No: no})
	if e.text(`SELECT ended_party::text FROM treaties WHERE no = $1::bigint`, no) != alphaJ {
		t.Fatal("Alpha is the party that ended the alliance")
	}
	// the war: refused for Alpha, in its cooling-off
	resp := declare(e.head, betaCode)
	if k := refusalKind(t, resp.View); k != "cooling" {
		t.Fatalf("Alpha ended an alliance lately and may not declare war on Beta: %q (%s)", k, resp.Screen)
	}
	if e.scalarInt(`SELECT count(*) FROM wars WHERE attacker_id = $1::uuid`, alphaJ) != 0 {
		t.Fatal("no war was declared")
	}
	// the one who was left may declare: Beta's charter holder
	resp = declare(e.att, alphaCode)
	if k := refusalKind(t, resp.View); k != "" {
		t.Fatalf("Beta, which was left, may declare war: %q", k)
	}
	if e.scalarInt(`SELECT count(*) FROM wars WHERE attacker_id = $1::uuid AND defender_id = $2::uuid`, betaJ, alphaJ) != 1 {
		t.Fatal("Beta's war is declared, by its charter")
	}
	if got := e.text(`SELECT declared_office FROM wars WHERE attacker_id = $1::uuid`, betaJ); got != "Government" {
		t.Fatalf("the office the war was declared from is the charter's title: %q", got)
	}
	// a resident of Beta who holds no office cannot declare anything for it
	resp = declare(rb, alphaCode)
	if k := refusalKind(t, resp.View); k == "" {
		t.Fatal("a resident without the power cannot declare war")
	}
}

func (e *polityEnv) tender(p *application.Player, req handlers.VillageTenderRequest) (village.TenderView, string) {
	e.t.Helper()
	resp, err := e.village.Tender(testCtx(e.t), e.metaOf(p, "settlement.tender", "tender"), req)
	if err != nil {
		e.t.Fatalf("tender %s: %v", req.Action, err)
	}
	if resp.Screen == village.ScreenTender {
		var v village.TenderView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatal(err)
		}
		return v, ""
	}
	return village.TenderView{}, refusalKind(e.t, resp.View)
}

func (e *polityEnv) mustTender(p *application.Player, req handlers.VillageTenderRequest) village.TenderView {
	e.t.Helper()
	v, kind := e.tender(p, req)
	if kind != "" {
		e.t.Fatalf("tender %s was refused: %s", req.Action, kind)
	}
	return v
}

// contractor is a building contractor company of a settlement, owned by a resident, with money in its treasury.
func (e *polityEnv) contractor(place string, funds int64) (*application.Player, string) {
	e.t.Helper()
	owner := e.resident(place)
	city := map[string]string{"A": e.cityID, "B": e.attCity, "C": e.cityC}[place]
	return owner, e.companyFor(owner, city, funds)
}

func (e *polityEnv) companyFor(owner *application.Player, city string, funds int64) string {
	e.t.Helper()
	id, code := newUUID(e.t), "T"+randomTokenUpper(e.t, 6)
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO companies (id, code, name, name_key, type_code, city_id, owner_player_id, status, price_bps, total_shares, registration_fee, content_version, founded_at, updated_at)
		VALUES ($1::uuid, $2, $3, $3, 'works_contractor', $4::uuid, $5::uuid, 'active', 10000, 100, 0, 1, now(), now())`, id, code, "Builders "+code, city, owner.ID); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `INSERT INTO company_shareholders (company_id, player_id, shares, acquired_at) VALUES ($1::uuid, $2::uuid, 100, now())`, id, owner.ID); err != nil {
		e.t.Fatal(err)
	}
	if funds > 0 {
		grant(e.t, e.pool, application.AccountCompanyTreasury, id, funds)
	}
	return id
}

func (e *polityEnv) companyCode(id string) string {
	return e.text(`SELECT code FROM companies WHERE id = $1::uuid`, id)
}

func (e *polityEnv) companyCash(id string) int64 {
	return e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'company_treasury' AND owner_id = $1::uuid`, id)
}

func (e *polityEnv) escrowCash(tender string) int64 {
	return e.scalarInt(`SELECT COALESCE(sum(balance), 0)::bigint FROM accounts WHERE kind = 'works_escrow' AND owner_id = $1::uuid`, tender)
}

func (e *polityEnv) bid(owner *application.Player, tender, company string, price, days int64) string {
	_, kind := e.tender(owner, handlers.VillageTenderRequest{Action: village.TenderActionBid, ID: tender, Company: e.companyCode(company), Price: fmt.Sprint(price), Days: fmt.Sprint(days)})
	return kind
}

// A tender has one winner even when every replica tries to award it at once; an official cannot award to a company he owns, manages or holds
// shares of; the contractor's crew is paid by its company, the price is paid in stages from the escrow with a retention, and the bond and the
// retention come back when the defects period ends; a missed deadline forfeits the bond and debars the company (ADR 0076 10).
func TestATenderHasOneWinnerRefusesAConflictAndPaysByStages(t *testing.T) {
	e := newPolityEnv(t, nil)
	e.clock.Advance(4 * 24 * time.Hour)
	site := e.place("road")
	owner1, c1 := e.contractor("A", 20_000)
	owner2, c2 := e.contractor("B", 20_000)
	// an official's own company: the head of A owns a contractor too
	own := e.companyFor(e.head, e.cityID, 20_000)

	treasury0 := e.cityCash(e.cityID)
	e.mustTender(e.head, handlers.VillageTenderRequest{Action: village.TenderActionPost, Building: site, Cap: "5000", Due: "2", Window: "24", Rule: "lowest_price"})
	if got := treasury0 - e.cityCash(e.cityID); got != e.rules.TenderFee {
		t.Fatalf("the filing fee is a sink: %d, fee %d", got, e.rules.TenderFee)
	}
	tid := e.text(`SELECT id::text FROM tenders WHERE building_id = $1::uuid`, site)
	// a second tender for the same building is refused while the first stands
	if _, kind := e.tender(e.head, handlers.VillageTenderRequest{Action: village.TenderActionPost, Building: site, Cap: "5000", Due: "2"}); kind != village.TenderHave {
		t.Fatalf("one tender per building: %q", kind)
	}
	// the conflict of interest: the official who awards owns this company, so its bid is refused, in the open
	if kind := e.bid(e.head, tid, own, 3000, 2); kind != village.TenderConflict {
		t.Fatalf("an official's own company cannot bid: %q", kind)
	}
	// a bid over the cap or with no days is refused; a good one posts its bond
	if kind := e.bid(owner1, tid, c1, 6000, 2); kind != village.TenderBadBid {
		t.Fatalf("over the cap: %q", kind)
	}
	if kind := e.bid(owner1, tid, c1, 4000, 2); kind != "" {
		t.Fatalf("bid 1: %q", kind)
	}
	if kind := e.bid(owner2, tid, c2, 3500, 2); kind != "" {
		t.Fatalf("bid 2: %q", kind)
	}
	if kind := e.bid(owner2, tid, c2, 3000, 2); kind != village.TenderHaveBid {
		t.Fatalf("one bid per company: %q", kind)
	}
	if e.escrowCash(tid) != 400+350 || e.companyCash(c1) != 20_000-400 {
		t.Fatalf("the bonds are set aside: escrow %d, company %d", e.escrowCash(tid), e.companyCash(c1))
	}
	// before the close nobody can award
	if _, kind := e.tender(e.head, handlers.VillageTenderRequest{Action: village.TenderActionAward, ID: tid}); kind != village.TenderNotYetDue {
		t.Fatalf("the bidding is not over: %q", kind)
	}
	e.clock.Advance(25 * time.Hour)
	// every replica awards at once: one wins
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, _ := json.Marshal(handlers.PolityTickPayload{Kind: application.TickTender, ID: tid})
			if i%2 == 0 {
				if _, err := e.village.PolityTick(testCtx(t), e.sched("polity.tick"), handlers.CrimeScheduledRequest{Payload: payload}); err != nil {
					t.Errorf("tick: %v", err)
				}
				return
			}
			if _, err := e.village.Tender(testCtx(t), e.metaOf(e.head, "settlement.tender", "tender"), handlers.VillageTenderRequest{Action: village.TenderActionAward, ID: tid}); err != nil {
				t.Errorf("award: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if e.text(`SELECT status FROM tenders WHERE id = $1::uuid`, tid) != "awarded" || e.text(`SELECT company_id::text FROM tenders WHERE id = $1::uuid`, tid) != c2 {
		t.Fatalf("the lower price wins, once: %s", e.text(`SELECT status || ' ' || COALESCE(company_id::text, '') FROM tenders WHERE id = $1::uuid`, tid))
	}
	if e.scalarInt(`SELECT count(*) FROM tender_bids WHERE tender_id = $1::uuid AND status = 'won'`, tid) != 1 || e.companyCash(c1) != 20_000 {
		t.Fatal("one winning bid; the loser's bond is back")
	}
	// the price is funded into the escrow at the award, with the winner's bond: the government's treasury paid it
	if e.escrowCash(tid) != 3500+350 {
		t.Fatalf("the escrow holds the price and the winner's bond: %d", e.escrowCash(tid))
	}
	if e.text(`SELECT employer_kind FROM labor_jobs WHERE building_id = $1::uuid AND status = 'open'`, site) != "company" {
		t.Fatal("the site's crew is now the contractor's")
	}
	e.verifyPolity()

	// the contractor's crew is paid by the company: a resident takes the job and works a shift
	worker := e.resident("A")
	siteView, _ := e.site(worker, site)
	if siteView.Job == nil {
		t.Fatal("the site has a job")
	}
	// the road is small: make the work big enough that one shift is a sliver of it
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE settlement_buildings SET work_required = work_required * 1000 WHERE id = $1::uuid`, site); err != nil {
		t.Fatal(err)
	}
	cash0, wage := e.companyCash(c2), siteView.Job.Wage
	if _, err := rrc(e.village.LaborTake(testCtx(t), e.as(worker, "settlement.labor.take", "labor.take"), handlers.VillageLaborRequest{ID: siteView.Job.ID})); err != nil {
		t.Fatal(err)
	}
	shifts := e.workingShifts(site)
	if len(shifts) != 1 {
		t.Fatalf("a shift is working: %d", len(shifts))
	}
	e.clock.Advance(time.Hour + time.Minute)
	e.end(shifts[0])
	if got := cash0 - e.companyCash(c2); got != wage {
		t.Fatalf("the company paid the worker's wage %d, got %d", wage, got)
	}

	// stages: a third each of the work done pays a third of the price less the retention, once
	c2Before := e.companyCash(c2)
	for i, pct := range []int{34, 67} {
		if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE settlement_buildings SET work_done = work_required * $2 / 100 WHERE id = $1::uuid`, site, pct); err != nil {
			t.Fatal(err)
		}
		e.mustTender(e.head, handlers.VillageTenderRequest{Action: village.TenderActionSettle, ID: tid})
		e.mustTender(e.head, handlers.VillageTenderRequest{Action: village.TenderActionSettle, ID: tid}) // paid once
		if got := e.scalarInt(`SELECT count(*) FROM tender_stages WHERE tender_id = $1::uuid`, tid); got != int64(i+1) {
			t.Fatalf("after %d%% there are %d stages paid", pct, got)
		}
	}
	if got := e.companyCash(c2) - c2Before; got != 2*(1166-59) {
		t.Fatalf("two stages of 1166 less a retention of 59 each: %d", got)
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE settlement_buildings SET work_done = work_required, status = 'complete', completed_at = now() WHERE id = $1::uuid`, site); err != nil {
		t.Fatal(err)
	}
	e.mustTender(e.head, handlers.VillageTenderRequest{Action: village.TenderActionSettle, ID: tid})
	if e.scalarInt(`SELECT stages_paid FROM tenders WHERE id = $1::uuid`, tid) != 3 || e.scalarInt(`SELECT retention_held FROM tenders WHERE id = $1::uuid`, tid) != 59+59+59 {
		t.Fatalf("three stages paid, the retention held: %d", e.scalarInt(`SELECT retention_held FROM tenders WHERE id = $1::uuid`, tid))
	}
	if e.escrowCash(tid) != 177+350 {
		t.Fatalf("the escrow holds the retention and the bond until the defects period ends: %d", e.escrowCash(tid))
	}
	e.verifyPolity()
	// before the defects period ends nothing is released; after it the retention and the bond go back to the contractor
	e.tick(application.TickRetention, tid, "")
	if e.text(`SELECT status FROM tenders WHERE id = $1::uuid`, tid) != "awarded" {
		t.Fatal("the retention is held for the defects period")
	}
	e.clock.Advance(time.Duration(e.rules.DefectsDays)*24*time.Hour + time.Hour)
	before := e.companyCash(c2)
	e.tick(application.TickRetention, tid, "")
	e.tick(application.TickRetention, tid, "")
	if e.text(`SELECT status FROM tenders WHERE id = $1::uuid`, tid) != "done" || e.companyCash(c2)-before != 177+350 || e.escrowCash(tid) != 0 {
		t.Fatalf("done, once: retention and bond back, the escrow empty (%d)", e.escrowCash(tid))
	}
	e.verifyPolity()
}

// A contractor that does not finish forfeits its bond to the government, the unpaid price goes back, the site goes back to the
// government's own crew and the company is kept from public tenders for the debarment days.
func TestAContractorThatMissesItsDeadlineForfeitsItsBondAndIsDebarred(t *testing.T) {
	e := newPolityEnv(t, nil)
	e.clock.Advance(4 * 24 * time.Hour)
	site := e.place("road")
	owner, c := e.contractor("B", 20_000)
	e.mustTender(e.head, handlers.VillageTenderRequest{Action: village.TenderActionPost, Building: site, Cap: "5000", Due: "2", Window: "24"})
	tid := e.text(`SELECT id::text FROM tenders WHERE building_id = $1::uuid`, site)
	if kind := e.bid(owner, tid, c, 4000, 2); kind != "" {
		t.Fatal(kind)
	}
	e.clock.Advance(25 * time.Hour)
	e.tick(application.TickTender, tid, "")
	if e.text(`SELECT status FROM tenders WHERE id = $1::uuid`, tid) != "awarded" {
		t.Fatal("the lone bid wins")
	}
	treasury := e.cityCash(e.cityID)
	// the deadline and the grace pass with no work done
	e.clock.Advance(2*24*time.Hour + e.rules.TenderGrace + time.Hour)
	e.tick(application.TickTenderDue, tid, "")
	e.tick(application.TickTenderDue, tid, "")
	if e.text(`SELECT status FROM tenders WHERE id = $1::uuid`, tid) != "failed" {
		t.Fatal("the work failed")
	}
	if got := e.cityCash(e.cityID) - treasury; got != 4000+400 {
		t.Fatalf("the government got back the whole price and the bond %d", got)
	}
	if e.escrowCash(tid) != 0 || e.companyCash(c) != 20_000-400 {
		t.Fatalf("the escrow is empty and the company lost its bond: %d, %d", e.escrowCash(tid), e.companyCash(c))
	}
	if e.text(`SELECT employer_kind FROM labor_jobs WHERE building_id = $1::uuid AND status = 'open'`, site) != "settlement" {
		t.Fatal("the site went back to the government's own crew")
	}
	// debarred: a bid on the next tender is refused
	site2 := e.place("road") // the same unfinished road: its first tender failed, the government puts it out again
	e.mustTender(e.head, handlers.VillageTenderRequest{Action: village.TenderActionPost, Building: site2, Cap: "5000", Due: "2", Window: "24"})
	tid2 := e.text(`SELECT id::text FROM tenders WHERE building_id = $1::uuid AND status = 'open'`, site2)
	if kind := e.bid(owner, tid2, c, 3000, 2); kind != village.TenderDebarred {
		t.Fatalf("a company that forfeited its bond is debarred: %q (tender %s)", kind, e.text(`SELECT status || ' closes ' || closes_at::text || ' now ' || now()::text FROM tenders WHERE id = $1::uuid`, tid2))
	}
	if got := e.scalarInt(`SELECT count(*) FROM tender_debarments WHERE company_id = $1::uuid AND until > now() + interval '13 days'`, c); got != 1 {
		t.Fatal("the debarment runs the configured days")
	}
	e.verifyPolity()
}

func (e *polityEnv) debt(p *application.Player, req handlers.VillageDebtRequest) (village.DebtView, string) {
	e.t.Helper()
	resp, err := e.village.Debt(testCtx(e.t), e.metaOf(p, "settlement.debt", "debt"), req)
	if err != nil {
		e.t.Fatalf("debt %s: %v", req.Action, err)
	}
	if resp.Screen == village.ScreenDebt {
		var v village.DebtView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			e.t.Fatal(err)
		}
		return v, ""
	}
	return village.DebtView{}, refusalKind(e.t, resp.View)
}

func (e *polityEnv) mustDebt(p *application.Player, req handlers.VillageDebtRequest) village.DebtView {
	e.t.Helper()
	v, kind := e.debt(p, req)
	if kind != "" {
		e.t.Fatalf("debt %s was refused: %s", req.Action, kind)
	}
	return v
}

func (e *polityEnv) loanStatus(id string) string {
	return e.text(`SELECT status FROM sovereign_loans WHERE id = $1::uuid`, id)
}

// pairCash is the money in the two settlements' treasuries together.
func (e *polityEnv) pairCash() int64 { return e.cityCash(e.cityID) + e.cityCash(e.attCity) }

// A loan between polities creates no money: the principal moves from one treasury to the other and the instalments move back; a borrower
// that cannot pay is late, then in default, and the lender restructures by agreement or writes the debt off as a book entry; no loan is
// offered that the lender cannot spare or the borrower could not carry (ADR 0076 11).
func TestASovereignLoanCreatesNoMoneyAndADefaultIsHandled(t *testing.T) {
	e := newPolityEnv(t, nil)
	e.clock.Advance(4 * 24 * time.Hour)
	codeA, codeB := e.codeOf(e.cityID), e.codeOf(e.attCity)
	e.earn(e.attCity, 20_000)
	e.clock.Advance(time.Hour)
	// the rails of the offer
	if _, kind := e.debt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionOffer, Code: codeA, Principal: "1000", Rate: "1000", Periods: "4"}); kind != village.DebtSelf {
		t.Fatalf("a polity cannot lend to itself: %q", kind)
	}
	if _, kind := e.debt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionOffer, Code: codeB, Principal: "9000000", Rate: "1000", Periods: "4"}); kind != village.DebtLender {
		t.Fatalf("a lender that does not hold it cannot lend: %q", kind)
	}
	if _, kind := e.debt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionOffer, Code: codeB, Principal: "1000", Rate: "9999", Periods: "4"}); kind != village.DebtTerms {
		t.Fatalf("a rate over the bound: %q", kind)
	}
	if _, kind := e.debt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionOffer, Code: e.codeOf(e.cityC), Principal: "50000", Rate: "1000", Periods: "1"}); kind != village.DebtService {
		t.Fatalf("C earns 60000 a period and could not carry a single instalment of 50000: %q", kind)
	}
	// a resident with no power cannot lend for the town
	if _, kind := e.debt(e.resident("A"), handlers.VillageDebtRequest{Action: village.DebtActionOffer, Code: codeB, Principal: "1000", Rate: "1000", Periods: "4"}); kind != village.VillageNotOfficeHolder {
		t.Fatalf("lending is a power of the charter: %q", kind)
	}

	money0 := e.pairCash()
	e.mustDebt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionOffer, Code: codeB, Principal: "4000", Rate: "1000", Periods: "4"})
	loan := e.text(`SELECT id::text FROM sovereign_loans WHERE status = 'offered' ORDER BY offered_at DESC LIMIT 1`)
	if e.pairCash() != money0 {
		t.Fatal("an offer moves nothing")
	}
	// only the borrower accepts
	if _, kind := e.debt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionAccept, ID: loan}); kind != village.DebtNotParty {
		t.Fatalf("the lender cannot accept its own offer: %q", kind)
	}
	aBefore := e.cityCash(e.cityID)
	e.mustDebt(e.att, handlers.VillageDebtRequest{Action: village.DebtActionAccept, ID: loan})
	if e.cityCash(e.cityID) != aBefore-4000 || e.pairCash() != money0 {
		t.Fatalf("the principal moved from the lender to the borrower, creating nothing: %d, %d -> %d", e.cityCash(e.cityID), money0, e.pairCash())
	}
	// an instalment falls due each period; it is paid from the borrower's treasury, once
	e.clock.Advance(e.rules.DebtPeriod + time.Hour)
	aMid := e.cityCash(e.cityID)
	e.tick(application.TickLoanDue, loan, "")
	e.tick(application.TickLoanDue, loan, "")
	if got := e.cityCash(e.cityID) - aMid; got != 1007 {
		t.Fatalf("the first instalment of 1000 + 7 reached the lender once: %d", got)
	}
	if e.pairCash() != money0 {
		t.Fatalf("repaying moves money between the two, never creates it: %d != %d", e.pairCash(), money0)
	}
	if e.scalarInt(`SELECT paid FROM sovereign_loans WHERE id = $1::uuid`, loan) != 1 {
		t.Fatal("one instalment paid")
	}
	e.verifyPolity()

	// the borrower is drained and misses: late, late, in default
	if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, e.attCity)
		if err != nil {
			return err
		}
		bal, err := tx.Ledger().Balance(ctx, acct.ID)
		if err != nil {
			return err
		}
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{Reason: application.ReasonAdminGrant, CreatedAt: e.clock.Now(), Entries: []application.LedgerEntry{
			{AccountID: acct.ID, Amount: money.FromMinor(-bal.Minor())}, {AccountID: application.SystemSinkAccountID, Amount: bal}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		e.clock.Advance(e.rules.DebtPeriod + time.Hour)
		e.tick(application.TickLoanDue, loan, "")
	}
	if e.loanStatus(loan) != "defaulted" {
		t.Fatalf("three missed instalments default the loan: %s", e.loanStatus(loan))
	}
	if e.scalarInt(`SELECT count(*) FROM sovereign_loan_events WHERE loan_id = $1::uuid AND kind = 'missed'`, loan) < 2 || e.scalarInt(`SELECT count(*) FROM sovereign_loan_events WHERE loan_id = $1::uuid AND kind = 'defaulted'`, loan) != 1 {
		t.Fatal("the misses and the default are in the loan's log")
	}
	// the borrower's record is public and shows it
	v := e.mustDebt(e.head, handlers.VillageDebtRequest{})
	if len(v.Others) == 0 || v.Others[0].Defaulted != 1 {
		t.Fatalf("the lender sees the borrower's record: %+v", v.Others)
	}
	// nothing is taken beyond what the borrower held, and nothing created
	money1 := e.pairCash()
	// a lender restructures by agreement: less principal over more periods; the borrower accepts; no money moves
	e.mustDebt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionRestructure, ID: loan, Principal: "2000", Rate: "500", Periods: "8"})
	if _, kind := e.debt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionRestructure, ID: loan, Principal: "99999", Rate: "500", Periods: "8"}); kind != village.DebtRaise {
		t.Fatalf("a restructuring may not raise the debt: %q", kind)
	}
	newLoan := e.text(`SELECT id::text FROM sovereign_loans WHERE replaces = $1::uuid`, loan)
	e.mustDebt(e.att, handlers.VillageDebtRequest{Action: village.DebtActionAccept, ID: newLoan})
	if e.loanStatus(loan) != "restructured" || e.loanStatus(newLoan) != "active" || e.pairCash() != money1 {
		t.Fatalf("the old loan is closed, the new one runs, no money moved: %s %s", e.loanStatus(loan), e.loanStatus(newLoan))
	}
	// the new loan defaults too, and the lender writes it off: a book entry, no money
	for i := 0; i < 3; i++ {
		e.clock.Advance(e.rules.DebtPeriod + time.Hour)
		e.tick(application.TickLoanDue, newLoan, "")
	}
	if e.loanStatus(newLoan) != "defaulted" {
		t.Fatalf("the restructured loan defaulted: %s", e.loanStatus(newLoan))
	}
	if _, kind := e.debt(e.att, handlers.VillageDebtRequest{Action: village.DebtActionWriteOff, ID: newLoan}); kind == "" {
		t.Fatal("only the lender writes off")
	}
	money2 := e.pairCash()
	e.mustDebt(e.head, handlers.VillageDebtRequest{Action: village.DebtActionWriteOff, ID: newLoan})
	if e.loanStatus(newLoan) != "written_off" || e.pairCash() != money2 {
		t.Fatal("a write-off moves no money")
	}
	e.verifyPolity()
}

// societyEntries lists the entries the society hub offers a player standing in their settlement.
func (e *polityEnv) societyEntries(p *application.Player) []string {
	e.t.Helper()
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `UPDATE players SET city_id = residence_city_id WHERE id = $1::uuid`, p.ID); err != nil {
		e.t.Fatal(err)
	}
	resp, err := e.village.SocietyHub(testCtx(e.t), e.metaOf(p, "society.hub", "hub"))
	if err != nil {
		e.t.Fatal(err)
	}
	var v struct {
		Entries []struct {
			Code string `json:"code"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(resp.View, &v); err != nil {
		e.t.Fatalf("the society hub: %v (%s)", err, resp.Screen)
	}
	out := make([]string, 0, len(v.Entries))
	for _, x := range v.Entries {
		out = append(out, x.Code)
	}
	return out
}

func has(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}

// The army and the war board are the army of a country the players founded: a barracks standing in a settlement under the commons opens
// nothing; in a settlement of a player country it opens for the holder of a power of war or of the forces in that country's charter, and for
// nobody else (ADR 0076 8, ADR 0063).
func TestTheBarracksShowOnlyToAPlayerCountrysOfficeHolder(t *testing.T) {
	e := newPolityEnv(t, nil)
	e.clock.Advance(4 * 24 * time.Hour)
	ra := e.resident("A")
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES (gen_random_uuid(), $1::uuid, 'barracks', 4, 4, 'complete', now(), now())`, e.cityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'barracks'`, e.cityID)
	})
	// under the commons: the barracks stands and opens nothing, for the head or for anybody
	if has(e.societyEntries(e.head), "war") || has(e.societyEntries(ra), "war") {
		t.Fatal("a settlement under the commons has no army to show")
	}
	if err := e.village.MilitaryOpen(testCtx(t), e.metaOf(e.head, "military.ministry", "ministry")); err == nil {
		t.Fatal("the military commands are closed to a settlement without a country of its own")
	}
	// the settlement founds a country of one; the head holds the powers of war and of the forces
	e.foundCountryOfOne(e.head, "Armed State", e.head, ra)
	if !has(e.societyEntries(e.head), "war") {
		t.Fatal("the holder of the country's powers sees the army and the war board")
	}
	if err := e.village.MilitaryOpen(testCtx(t), e.metaOf(e.head, "military.ministry", "ministry")); err != nil {
		t.Fatalf("the holder may use the military commands: %v", err)
	}
	if has(e.societyEntries(ra), "war") {
		t.Fatal("a resident who holds no office of the charter is shown no army")
	}
	if err := e.village.MilitaryOpen(testCtx(t), e.metaOf(ra, "military.ministry", "ministry")); err == nil {
		t.Fatal("the military commands refuse a resident without a power")
	}
	// the head gives the resident a post with the power over the forces: now the resident sees it
	id := e.unionIDByName("Armed State")
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionOffice, ID: id, Title: "Marshal", Seats: 1,
		Grants: []handlers.VillageCharterGrant{{Permission: "military.command"}}})
	marshal := e.text(`SELECT id::text FROM union_offices WHERE union_id = $1::uuid AND title = 'Marshal'`, id)
	e.mustUnion(e.head, handlers.VillageUnionRequest{Action: village.UnionActionAppoint, ID: id, Office: marshal, Player: ra.PublicCode})
	if !has(e.societyEntries(ra), "war") {
		t.Fatal("the marshal sees the army")
	}
	e.verifyPolity()
}

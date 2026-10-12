//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
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
	ctx := testCtx(e.t)
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
		`DELETE FROM treaties WHERE proposer_id IN (SELECT jurisdiction_id FROM cities WHERE id IN ($1::uuid, $2::uuid, $3::uuid)) OR partner_id IN (SELECT jurisdiction_id FROM cities WHERE id IN ($1::uuid, $2::uuid, $3::uuid))`,
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
	purgeLedgerFor(e.t, e.pool, cityC)
	purgeLedgerFor(e.t, e.pool, e.headC.ID)
	e.cleanup(cityC)
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
	all := append([]string{"office.edit", "office.create", "office.appoint", "office.dismiss", "charter.amend", "union.admit", "union.expel", "treasury.spend"}, grants...)
	raw, _ := json.Marshal([]map[string]any{{"title": "Council", "seats": 3, "grants": all, "holders": codes}})
	return raw
}

func (e *polityEnv) proposeLeague(by *application.Player, name, delegations string, invite string, holders ...*application.Player) string {
	e.t.Helper()
	e.mustUnion(by, handlers.VillageUnionRequest{Action: village.UnionActionPropose, Name: name, Kind: "league", Mode: "double_majority", Exit: "free", Delegations: delegations,
		DuesFixed: "100", DuesShare: "1000", Invite: invite, Offices: managerOffice([]string{"treaty.sign", "treaty.propose", "treaty.end", "road.draw"}, holders...)})
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

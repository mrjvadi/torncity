//go:build integration

// Integration test of charter phase 2 (docs/adr/0044 6.5, 6.6; owner decision
// 2026-10-05): an elected office, its term, recall by petition and vote, an amendment
// by the residents, and the acting head while the head seat is vacant.
package tests

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

func TestCharterPhase2(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	set := charter.Defaults()
	set.AmendVoteMinResidents = 4
	set.MinResidencyDays = 0
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000, TimezoneCooldown: 24 * time.Hour, CharterSettings: set,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 3000})
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID, jurisdictionID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text, jurisdiction_id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID, &jurisdictionID); err != nil {
		t.Fatal(err)
	}
	var founderCode string
	var firstResidents int
	_ = pool.Raw().QueryRow(ctx, `SELECT count(*) FROM players WHERE residence_city_id = $1::uuid`, cityID).Scan(&firstResidents)
	_ = firstResidents
	_ = founderCode

	resident := func() *application.Player {
		p := insertPlayer(t, pool)
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $1::uuid, residence_city_id = $1::uuid WHERE id = $2::uuid`, cityID, p.ID); err != nil {
			t.Fatal(err)
		}
		cid := cityID
		p.CityID = &cid
		return p
	}
	r1, r2, r3, r4, r5 := resident(), resident(), resident(), resident(), resident()
	stranger := insertPlayer(t, pool)
	t.Cleanup(func() {
		c := testCtx(t)
		for _, q := range []string{
			`ALTER TABLE charter_audit DISABLE TRIGGER charter_audit_no_change`,
			`DELETE FROM charter_audit WHERE settlement_id = $1::uuid`,
			`ALTER TABLE charter_audit ENABLE TRIGGER charter_audit_no_change`,
			`DELETE FROM charter_petition_signatures WHERE petition_id IN (SELECT id FROM charter_petitions WHERE settlement_id = $1::uuid)`,
			`DELETE FROM charter_petitions WHERE settlement_id = $1::uuid`,
			`DELETE FROM charter_ballot_votes WHERE ballot_id IN (SELECT id FROM charter_ballots WHERE settlement_id = $1::uuid)`,
			`DELETE FROM charter_ballot_candidates WHERE ballot_id IN (SELECT id FROM charter_ballots WHERE settlement_id = $1::uuid)`,
			`DELETE FROM charter_ballots WHERE settlement_id = $1::uuid`,
			`DELETE FROM charter_seats WHERE office_id IN (SELECT id FROM charter_offices WHERE settlement_id = $1::uuid)`,
			`DELETE FROM charter_offices WHERE settlement_id = $1::uuid`,
		} {
			if strings.Contains(q, "$1") {
				_, _ = pool.Raw().Exec(c, q, cityID)
			} else {
				_, _ = pool.Raw().Exec(c, q)
			}
		}
	})
	code := func(p *application.Player) string {
		var c string
		if err := pool.Raw().QueryRow(ctx, `SELECT public_code FROM players WHERE id = $1::uuid`, p.ID).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	as := func(p *application.Player, command string) envelope.Metadata {
		m := asPlayer(meta, p)
		m.Command = command
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	refusal := func(resp *presentation.Response, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.VillageRefusalView
		if resp != nil && resp.Screen == vpres.ScreenVillageRefusal && presentation.DecodeView(resp.View, &v) == nil {
			return v.Kind
		}
		return ""
	}
	view := func(p *application.Player) vpres.CharterView {
		t.Helper()
		resp, err := village.CharterView(ctx, as(p, "settlement.charter.view"))
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.CharterView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatalf("%v: %s", err, resp.Text)
		}
		return v
	}
	grants := func(ps ...string) []handlers.VillageCharterGrant {
		var out []handlers.VillageCharterGrant
		for _, p := range ps {
			out = append(out, handlers.VillageCharterGrant{Permission: p})
		}
		return out
	}
	save := func(p *application.Player, req handlers.VillageCharterRequest) (string, vpres.CharterChangedView) {
		t.Helper()
		resp, err := village.CharterOfficeSave(ctx, as(p, "settlement.charter.office.save"), req)
		k := refusal(resp, err)
		var ch vpres.CharterChangedView
		if k == "" && resp.Screen == vpres.ScreenVillageCharterChanged {
			_ = presentation.DecodeView(resp.View, &ch)
		}
		return k, ch
	}
	stand := func(p *application.Player, ballot string) string {
		return refusal(village.CharterStand(ctx, as(p, "settlement.charter.stand"), handlers.VillageBallotRequest{Ballot: ballot}))
	}
	vote := func(p *application.Player, ballot, choice string) string {
		return refusal(village.CharterVote(ctx, as(p, "settlement.charter.vote"), handlers.VillageBallotRequest{Ballot: ballot, Choice: choice}))
	}
	take := func(p *application.Player) string {
		return refusal(village.StockTake(ctx, as(p, "settlement.stock.take"), handlers.VillageStockMoveRequest{Item: "timber", Qty: "1"}))
	}
	office := func(v vpres.CharterView, title string) vpres.CharterOfficeView {
		for _, o := range v.Offices {
			if o.Title == title {
				return o
			}
		}
		t.Fatalf("no office %q in %+v", title, v.Offices)
		return vpres.CharterOfficeView{}
	}
	ballotOf := func(v vpres.CharterView, kind, status string) *vpres.CharterBallotView {
		for i := range v.Ballots {
			if v.Ballots[i].Kind == kind && v.Ballots[i].Status == status {
				return &v.Ballots[i]
			}
		}
		return nil
	}
	hours := func(n int) { e.clock.Advance(time.Duration(n) * time.Hour) }

	// 1. An elected office: the founder creates it; it has no holder, so an election opens by itself.
	if k, _ := save(founder, handlers.VillageCharterRequest{Title: "کدخدا", Seats: 1, Grants: grants("storage.take"), Acquisition: "election"}); k != "" {
		t.Fatalf("creating an elected office: %q", k)
	}
	v := view(founder)
	kad := office(v, "کدخدا")
	if kad.Acquisition != "election" || kad.TermDays != 14 {
		t.Errorf("the elected office: %+v", kad)
	}
	el := ballotOf(v, "election", "open")
	if el == nil || el.OfficeID != kad.ID || el.Phase != "candidacy" {
		t.Fatalf("no election opened for a vacant elected office: %+v", v.Ballots)
	}
	if k := take(r1); k != vpres.VillageNotOfficeHolder {
		t.Errorf("a resident took from the stock before any election: %q", k)
	}

	// 2. Candidacy: residents stand; a stranger and a double candidacy are refused; no voting yet.
	if k := stand(r1, el.ID); k != "" {
		t.Fatalf("r1 could not stand: %q", k)
	}
	if k := stand(r2, el.ID); k != "" {
		t.Fatalf("r2 could not stand: %q", k)
	}
	if k := stand(r1, el.ID); k != vpres.CharterAlreadyStanding {
		t.Errorf("standing twice: %q", k)
	}
	if k := stand(stranger, el.ID); k != vpres.CharterNotEligible {
		t.Errorf("a non-resident stood: %q", k)
	}
	if k := vote(r3, el.ID, code(r1)); k != vpres.CharterNotVoting {
		t.Errorf("voting during candidacy: %q", k)
	}
	hours(49)
	if k := stand(r3, el.ID); k != vpres.CharterNotCandidacy {
		t.Errorf("standing after candidacy closed: %q", k)
	}

	// 3. Voting: secret and final; plurality wins; the term is 14 days.
	if k := vote(r3, el.ID, code(r4)); k != vpres.CharterBadChoice {
		t.Errorf("voting for someone who does not stand: %q", k)
	}
	if k := vote(stranger, el.ID, code(r1)); k != vpres.CharterNotEligible {
		t.Errorf("a non-resident voted: %q", k)
	}
	for _, p := range []*application.Player{r3, r4, r5, founder} {
		if k := vote(p, el.ID, code(r1)); k != "" {
			t.Fatalf("vote: %q", k)
		}
	}
	if k := vote(r2, el.ID, code(r2)); k != "" {
		t.Fatalf("a candidate voting for themselves: %q", k)
	}
	if k := vote(r3, el.ID, code(r2)); k != vpres.CharterAlreadyVoted {
		t.Errorf("a second vote: %q", k)
	}
	if mid := view(r3); ballotOf(mid, "election", "open") == nil || ballotOf(mid, "election", "open").Candidates[0].Votes != nil {
		t.Error("the count must stay secret while the election runs")
	}
	hours(73)
	v = view(r3)
	done := ballotOf(v, "election", "passed")
	if done == nil || len(done.Winners) != 1 || done.Winners[0].Code != code(r1) {
		t.Fatalf("the election result: %+v", v.Ballots)
	}
	if k := take(r1); k == vpres.VillageNotOfficeHolder {
		t.Errorf("the elected holder cannot use the office: %q", k)
	}
	if k := take(r2); k != vpres.VillageNotOfficeHolder {
		t.Errorf("the runner-up holds the office: %q", k)
	}
	kad = office(v, "کدخدا")
	if kad.TermEnds == nil || kad.TermEnds.Sub(e.clock.Now()) < 13*24*time.Hour || kad.TermEnds.Sub(e.clock.Now()) > 14*24*time.Hour {
		t.Errorf("the term ends %v, want 14 days out", kad.TermEnds)
	}

	// 4. Recall: an appointed sheriff, a petition needs five days of service and 3 signatures here.
	if k, _ := save(founder, handlers.VillageCharterRequest{Title: "کلانتر", Seats: 1, Grants: grants("road.draw"), Acquisition: "appointment"}); k != "" {
		t.Fatalf("creating the sheriff: %q", k)
	}
	sheriff := office(view(founder), "کلانتر")
	if resp, err := village.CharterAppoint(ctx, as(founder, "settlement.charter.appoint"), handlers.VillageCharterRequest{Office: sheriff.ID, Player: code(r2)}); refusal(resp, err) != "" {
		t.Fatalf("appointing the sheriff: %q", refusal(resp, err))
	}
	recallStart := func(p *application.Player, o, target string) string {
		return refusal(village.CharterRecallStart(ctx, as(p, "settlement.charter.recall.start"), handlers.VillageBallotRequest{Office: o, Player: target}))
	}
	if k := recallStart(r3, sheriff.ID, code(r2)); k != vpres.CharterRecallTooEarly {
		t.Errorf("a petition on the first day: %q", k)
	}
	if k := recallStart(r3, sheriff.ID, code(r3)); k != vpres.CharterRecallSelf && k != vpres.CharterNotHolder {
		t.Errorf("a recall of a non-holder: %q", k)
	}
	hours(24 * 5)
	// the elected kadkhoda's term (14 days) is not over: still 9 days to go
	if k := recallStart(stranger, sheriff.ID, code(r2)); k != vpres.CharterNotEligible {
		t.Errorf("a non-resident started a petition: %q", k)
	}
	if k := recallStart(r3, sheriff.ID, code(r2)); k != "" {
		t.Fatalf("a petition after the tenure: %q", k)
	}
	if k := recallStart(r4, sheriff.ID, code(r2)); k != vpres.CharterRecallOpen {
		t.Errorf("a second petition on the same holder: %q", k)
	}
	v = view(r4)
	if len(v.Petitions) != 1 || v.Petitions[0].Signatures != 1 || v.Petitions[0].Needed < 3 {
		t.Fatalf("petition: %+v", v.Petitions)
	}
	pid := v.Petitions[0].ID
	sign := func(p *application.Player) string {
		return refusal(village.CharterRecallSign(ctx, as(p, "settlement.charter.recall.sign"), handlers.VillageBallotRequest{Petition: pid}))
	}
	if k := sign(r2); k != vpres.CharterRecallSelf {
		t.Errorf("the target signed their own recall: %q", k)
	}
	if k := sign(r4); k != "" {
		t.Fatalf("sign: %q", k)
	}
	if view(r5).Ballots != nil && ballotOf(view(r5), "recall", "open") != nil {
		t.Error("two signatures opened a recall vote")
	}
	if k := sign(r5); k != "" {
		t.Fatalf("sign: %q", k)
	}
	v = view(r5)
	rb := ballotOf(v, "recall", "open")
	if rb == nil || rb.Target == nil || rb.Target.Code != code(r2) {
		t.Fatalf("the third signature did not open the recall vote: %+v", v.Ballots)
	}
	if k := vote(r2, rb.ID, "no"); k != vpres.CharterTargetVoting {
		t.Errorf("the target voted on their own recall: %q", k)
	}
	for _, p := range []*application.Player{r3, r4, r5} {
		if k := vote(p, rb.ID, "yes"); k != "" {
			t.Fatalf("recall vote: %q", k)
		}
	}
	if k := vote(founder, rb.ID, "no"); k != "" {
		t.Fatalf("recall vote: %q", k)
	}
	hours(73)
	v = view(founder)
	if b := ballotOf(v, "recall", "passed"); b == nil || b.Yes == nil || *b.Yes != 3 || *b.No != 1 {
		t.Fatalf("the recall result: %+v", v.Ballots)
	}
	if h := office(v, "کلانتر").Holders; len(h) != 0 {
		t.Errorf("the recalled sheriff still holds the office: %+v", h)
	}
	if resp, err := village.CharterAppoint(ctx, as(founder, "settlement.charter.appoint"), handlers.VillageCharterRequest{Office: sheriff.ID, Player: code(r2)}); refusal(resp, err) != vpres.CharterRecalledRecent {
		t.Errorf("re-appointing a recalled holder: %q", refusal(resp, err))
	}

	// 5. Amendments: a key permission for an existing office goes to the residents' vote.
	if k, ch := save(founder, handlers.VillageCharterRequest{Office: sheriff.ID, Title: "کلانتر", Seats: 1, Grants: grants("road.draw", "office.appoint")}); k != "" || ch.Action != "amendment_proposed" {
		t.Fatalf("a key permission was not put to the vote: %q %+v", k, ch)
	}
	if office(view(founder), "کلانتر").Grants[0].Permission == "office.appoint" || len(office(view(founder), "کلانتر").Grants) != 1 {
		t.Error("the change was applied before the vote")
	}
	if k, _ := save(founder, handlers.VillageCharterRequest{Office: sheriff.ID, Title: "کلانتر", Seats: 2, Grants: grants("road.draw", "office.appoint")}); k != vpres.CharterVotePending {
		t.Errorf("a second amendment while one is open: %q", k)
	}
	am := ballotOf(view(founder), "amendment", "open")
	if am == nil || am.Proposal == nil || am.Proposal.Title != "کلانتر" {
		t.Fatalf("the amendment ballot: %+v", am)
	}
	for _, p := range []*application.Player{r3, r4, r5} {
		if k := vote(p, am.ID, "yes"); k != "" {
			t.Fatalf("amendment vote: %q", k)
		}
	}
	hours(73)
	v = view(founder)
	if ballotOf(v, "amendment", "passed") == nil {
		t.Fatalf("the amendment did not carry: %+v", v.Ballots)
	}
	if len(office(v, "کلانتر").Grants) != 2 {
		t.Errorf("the amendment was not applied: %+v", office(v, "کلانتر").Grants)
	}
	// a change that is not structural needs no vote
	if k, ch := save(founder, handlers.VillageCharterRequest{Office: sheriff.ID, Title: "کلانتر ارشد", Seats: 2, Grants: grants("road.draw", "office.appoint")}); k != "" || ch.Action != "office_changed" {
		t.Errorf("a rename: %q %+v", k, ch)
	}
	// an amendment that nobody votes for fails and changes nothing
	if _, ch := save(founder, handlers.VillageCharterRequest{Office: sheriff.ID, Title: "کلانتر ارشد", Seats: 2, Grants: grants("road.draw", "office.appoint", "treasury.spend")}); ch.Action != "amendment_proposed" {
		t.Fatalf("treasury.spend must be voted: %+v", ch)
	}
	hours(73)
	v = view(founder)
	if ballotOf(v, "amendment", "failed") == nil || len(office(v, "کلانتر ارشد").Grants) != 2 {
		t.Errorf("an unsupported amendment: %+v", v.Ballots)
	}
	// closing an elected office is voted too
	if k := refusal(village.CharterOfficeClose(ctx, as(founder, "settlement.charter.office.close"), handlers.VillageCharterRequest{Office: kad.ID})); k != "" {
		t.Fatalf("closing the elected office: %q", k)
	}
	if o := office(view(founder), "کدخدا"); o.ID == "" {
		t.Error("an elected office was closed without the residents")
	}

	// 6. The acting head: a deputy office, the head seat vacant.
	if _, err := pool.Raw().Exec(ctx, `UPDATE charter_offices SET deputy = true WHERE id = $1::uuid`, sheriff.ID); err != nil {
		t.Fatal(err)
	}
	if resp, err := village.CharterAppoint(ctx, as(founder, "settlement.charter.appoint"), handlers.VillageCharterRequest{Office: sheriff.ID, Player: code(r4)}); refusal(resp, err) != "" {
		t.Fatalf("appointing the deputy: %q", refusal(resp, err))
	}
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, _, err := application.VacateOffice(ctx, tx, "village_head", jurisdictionID, 1, e.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	v = view(r3)
	if !v.HeadVacant || v.Acting == nil || v.Acting.Player.Code != code(r4) || v.Acting.SpendCap != 2000 {
		t.Fatalf("the acting head: %+v vacant=%v", v.Acting, v.HeadVacant)
	}
	if k := take(r4); k == vpres.VillageNotOfficeHolder {
		t.Errorf("the acting head cannot run the day-to-day: %q", k)
	}
	if resp, err := village.TimezoneSet(ctx, as(r4, "settlement.timezone.set"), handlers.VillageZoneRequest{OffsetMinutes: 60}); refusal(resp, err) != vpres.VillageNotOfficeHolder {
		t.Errorf("the acting head changed the zone: %q", refusal(resp, err))
	}
	if k, _ := save(r4, handlers.VillageCharterRequest{Title: "تازه", Grants: grants("storage.take")}); k != vpres.VillageNotOfficeHolder {
		t.Errorf("the acting head created an office: %q", k)
	}
	if k := take(founder); k != vpres.VillageNotOfficeHolder {
		t.Errorf("a vacant head seat still gives its holder's powers: %q", k)
	}
	var he *vpres.CharterBallotView
	for i := range v.Ballots {
		if v.Ballots[i].Kind == "election" && v.Ballots[i].Status == "open" && v.Ballots[i].OfficeID != kad.ID {
			he = &v.Ballots[i]
		}
	}
	if he == nil {
		t.Fatalf("no election for the head seat: %+v", v.Ballots)
	}
	if k := stand(r5, he.ID); k != "" {
		t.Fatalf("standing for the head: %q", k)
	}
	hours(49)
	for _, p := range []*application.Player{r1, r3, r4} {
		if k := vote(p, he.ID, code(r5)); k != "" {
			t.Fatalf("head vote: %q", k)
		}
	}
	hours(73)
	v = view(r3)
	if v.HeadVacant {
		t.Fatalf("the head election did not seat a head: %+v", v.Ballots)
	}
	if resp, err := village.TimezoneSet(ctx, as(r5, "settlement.timezone.set"), handlers.VillageZoneRequest{OffsetMinutes: 60}); refusal(resp, err) != "" {
		t.Errorf("the elected head cannot use the founder's powers: %q", refusal(resp, err))
	}
	// the acting authority is over once a head sits; and it lapses by itself after 7 days
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, _, err := application.VacateOffice(ctx, tx, "village_head", jurisdictionID, 1, e.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	hours(24 * 8)
	if k := take(r4); k != vpres.VillageNotOfficeHolder {
		t.Errorf("an acting head acted after the acting days: %q", k)
	}
	// every change is in the log, and the log says that people voted, never how
	seen := map[string]bool{}
	rows, err := pool.Raw().Query(ctx, `SELECT DISTINCT action FROM charter_audit WHERE settlement_id = $1::uuid`, cityID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		seen[a] = true
	}
	rows.Close()
	for _, want := range []string{"election_opened", "seat_elected", "recall_petition_started", "recall_vote_opened", "recall_settled", "amendment_proposed", "amendment_passed", "amendment_failed", "vote_cast", "seat_term_ended", "election_no_result"} {
		if !seen[want] {
			t.Errorf("the charter log lacks %q: %v", want, seen)
		}
	}
	var votes int
	if err := pool.Raw().QueryRow(ctx, `SELECT count(*) FROM charter_audit WHERE settlement_id = $1::uuid AND action = 'vote_cast' AND detail::text LIKE '%choice%'`, cityID).Scan(&votes); err != nil || votes != 0 {
		t.Errorf("the log reveals how people voted: %d %v", votes, err)
	}
}

func init() { _ = context.Background }

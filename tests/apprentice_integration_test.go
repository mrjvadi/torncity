//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// The teaching network (ADR 0071): the teacher's own skill in a class, apprenticeship with a master, the pool's craftsman, the exam
// and the certificate with a name on it.

type apprEnv struct {
	*craftEnv
	ap *handlers.ApprenticeHandler
}

func newApprEnv(t *testing.T) *apprEnv {
	t.Helper()
	e := &apprEnv{craftEnv: newCraftEnv(t)}
	e.ap = handlers.NewApprenticeHandler(postgres.NewUnitOfWork(e.pool, testDefaultLanguage), workIDs{t}, staticContentSource{snap: loadTestContent(t)},
		postgres.NewCityRepository(e.pool), postgres.NewPlayerSearchRepository(e.pool), e.cache, e.clock.Now).WithRules(25, nil, "support")
	e.village.WithApprentices(e.ap)
	t.Cleanup(func() {
		c := testCtx(t)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM apprentice_exams WHERE apprenticeship_id IN (SELECT id FROM apprenticeships WHERE settlement_id = $1::uuid)`, e.cityID)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM apprenticeships WHERE settlement_id = $1::uuid`, e.cityID)
	})
	return e
}

// skillAt gives a player a skill with this much xp (and the level it makes).
func skillAt(t *testing.T, pool *postgres.Pool, playerID, skill string, xp int64) {
	t.Helper()
	level := 0
	for level < player.MaxSkillLevel && xp >= player.SkillXPForLevel(level+1) {
		level++
	}
	if _, err := pool.Raw().Exec(testCtx(t), `INSERT INTO player_skills (id, player_id, skill_code, level, xp, updated_at) VALUES (gen_random_uuid(), $1::uuid, $2, $3, $4, now())
		ON CONFLICT (player_id, skill_code) DO UPDATE SET level = EXCLUDED.level, xp = EXCLUDED.xp`, playerID, skill, level, xp); err != nil {
		t.Fatal(err)
	}
}

func (e *apprEnv) lines(resp *presentation.Response, err error) plife.ApprenticesView {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.Refusal != nil {
		e.t.Fatalf("refused: %s", resp.Refusal.Code)
	}
	var v plife.ApprenticesView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		e.t.Fatalf("the apprenticeships view (%s): %v", resp.Screen, err)
	}
	return v
}

func refusedWith(resp *presentation.Response) string {
	if resp == nil || resp.Refusal == nil {
		return ""
	}
	return resp.Refusal.Code
}

func (e *apprEnv) verifyAppr() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Apprentice || s.ApprenticeFeeLedger != s.ApprenticeFeeRows || s.ApprenticeRefundLedger != s.ApprenticeRefundRows || s.ApprenticeCertBroken != 0 || s.ApprenticeOverSlots != 0 {
		e.t.Errorf("the apprenticeship invariants do not hold: %+v", s)
	}
	e.verify()
}

// The pool's craftsman at a public workshop is a master of level 2: the apprentice joins at once, learns faster from his shifts there
// (never past level 2), keeps half the wage, and when the term is over and the piece is made (three shifts) the craftsman passes him if he
// has reached level 2; the certificate carries no person's name.
func TestAnApprenticeLearnsAtThePublicWorkshopAndSitsTheExam(t *testing.T) {
	e := newApprEnv(t)
	hand := e.resident()
	e.grant("carpentry")
	shop := e.placeAt("carpentry_workshop", 40, 0)
	e.placeAt("storehouse", 70, 25)
	e.stock("timber", 40)
	e.stock("tools", 4)
	skillAt(t, e.pool, hand.ID, "carpentry", 290)
	certify(t, e.researchEnv, hand.ID, "literacy_class")
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, hand.ID, e.cityID); err != nil {
		t.Fatal(err)
	}
	cid := e.cityID
	hand.CityID = &cid

	resp, err := e.ap.Join(testCtx(t), e.as(hand, "settlement.apprentice.join", "join"), handlers.ApprenticeRequest{Building: shop})
	v := e.lines(resp, err)
	if len(v.Lines) != 1 || v.Lines[0].Status != "active" || !v.Lines[0].NPC {
		t.Fatalf("joined the pool's craftsman: %+v", v.Lines)
	}
	if resp, _ := e.ap.Join(testCtx(t), e.as(hand, "settlement.apprentice.join", "join"), handlers.ApprenticeRequest{Building: shop}); refusedWith(resp) != "apprentice_busy" {
		t.Errorf("one apprenticeship at a time: %q", refusedWith(resp))
	}
	for i := 0; i < 3; i++ {
		if got := e.shiftWith(hand, shop, ""); got != "" {
			t.Fatalf("shift %d refused as %q", i, got)
		}
	}
	if e.scalar(`SELECT shifts FROM apprenticeships WHERE apprentice_id = $1::uuid`, hand.ID) != 3 || e.scalar(`SELECT xp_extra FROM apprenticeships WHERE apprentice_id = $1::uuid`, hand.ID) <= 0 {
		t.Fatalf("the contract counted the shifts and the extra learning")
	}
	// half the wage: the shift rows
	d, _ := loadTestContent(t).SettlementBuildingDef("carpentry_workshop")
	if w := e.scalar(`SELECT wage FROM settlement_shifts WHERE player_id = $1::uuid ORDER BY started_at DESC LIMIT 1`, hand.ID); w != d.Wage/2 {
		t.Errorf("the apprentice keeps half the wage: %d of %d", w, d.Wage)
	}
	if e.scalar(`SELECT xp FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'carpentry'`, hand.ID) < 300 {
		t.Error("290 xp and three shifts beside a master reach level 2")
	}
	// the term runs: no exam yet
	if resp, _ := e.ap.Exam(testCtx(t), e.as(hand, "settlement.apprentice.exam", "exam"), handlers.ApprenticeRequest{ID: e.apprenticeshipOf(hand)}); refusedWith(resp) != "apprentice_exam_not_ready" {
		t.Errorf("the term is not over: %q", refusedWith(resp))
	}
	e.clock.Advance(29 * 24 * time.Hour)
	resp, err = e.ap.Exam(testCtx(t), e.as(hand, "settlement.apprentice.exam", "exam"), handlers.ApprenticeRequest{ID: e.apprenticeshipOf(hand)})
	v = e.lines(resp, err)
	if len(v.Lines) != 1 || !v.Lines[0].Certified {
		t.Fatalf("the exam passed: %+v", v.Lines)
	}
	if e.scalar(`SELECT count(*) FROM certifications WHERE player_id = $1::uuid AND course_code = 'carpentry_craft' AND route = 'apprenticeship' AND issued_by IS NULL`, hand.ID) != 1 {
		t.Error("the certificate of the apprenticeship route")
	}
	e.verifyAppr()
}

func (e *apprEnv) apprenticeshipOf(p *application.Player) string {
	e.t.Helper()
	var id string
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT id::text FROM apprenticeships WHERE apprentice_id = $1::uuid ORDER BY offered_at DESC LIMIT 1`, p.ID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// A player master teaches in his own workshop: the offer, the refusals (not his workshop, too many, himself, a busy apprentice, terms out of
// range), the fee that moves on acceptance, the refund of a dismissal, the hook that trains the apprentice and pays the master teaching xp
// once a day, and the exam the master judges: a failure first, then a pass, with his name on the certificate.
func TestAPlayerMasterTeachesInHisOwnWorkshop(t *testing.T) {
	e := newApprEnv(t)
	master := e.resident()
	pupil, second := e.resident(), e.resident()
	e.grant("carpentry")
	skillAt(t, e.pool, master.ID, "carpentry", 600) // level 3
	certify(t, e.researchEnv, pupil.ID, "literacy_class")
	for _, p := range []*application.Player{master, pupil, second} {
		if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, p.ID, e.cityID); err != nil {
			t.Fatal(err)
		}
		cid := e.cityID
		p.CityID = &cid
	}
	grantCash(t, e.pool, pupil.ID, 2_000)
	grantCash(t, e.pool, master.ID, 2_000)
	bench := newUUID(t)
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES ($1::uuid, $2::uuid, 'carpentry_workshop_own', 41, 40, 'complete', now(), now())`, bench, e.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(testCtx(t), `INSERT INTO settlement_private_buildings (building_id, settlement_id, owner_id, permit_fee, construction_paid, materials_paid, assessed_value, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 0, 0, 0, 2000, now())`, bench, e.cityID, master.ID); err != nil {
		t.Fatal(err)
	}
	offer := func(who *application.Player, to *application.Player, days, fee string) string {
		resp, err := e.ap.Offer(testCtx(t), e.as(who, "settlement.apprentice.offer", "offer"),
			handlers.ApprenticeRequest{Building: bench, Player: to.PublicCode, Days: days, Fee: fee})
		if err != nil {
			t.Fatal(err)
		}
		return refusedWith(resp)
	}
	if got := offer(pupil, pupil, "14", "0"); got != "apprentice_not_master" {
		t.Errorf("only the owner is the master: %q", got)
	}
	if got := offer(master, master, "14", "0"); got != "apprentice_self" {
		t.Errorf("nobody apprentices himself: %q", got)
	}
	if got := offer(master, pupil, "3", "0"); got != "apprentice_range" {
		t.Errorf("a term under the least: %q", got)
	}
	if got := offer(master, pupil, "14", "400"); got != "" {
		t.Fatalf("a good offer was refused: %q", got)
	}
	id := e.apprenticeshipOf(pupil)

	// acceptance moves the fee
	cash := func(p *application.Player) int64 {
		return e.scalar(`SELECT balance FROM accounts WHERE kind = 'player_cash' AND owner_id = $1::uuid`, p.ID)
	}
	m0, p0 := cash(master), cash(pupil)
	if resp, _ := e.ap.Accept(testCtx(t), e.as(second, "settlement.apprentice.accept", "accept"), handlers.ApprenticeRequest{ID: id}); refusedWith(resp) != "apprentice_not_yours" {
		t.Errorf("only the apprentice accepts: %q", refusedWith(resp))
	}
	resp, err := e.ap.Accept(testCtx(t), e.as(pupil, "settlement.apprentice.accept", "accept"), handlers.ApprenticeRequest{ID: id})
	if v := e.lines(resp, err); len(v.Lines) != 1 || v.Lines[0].Status != "active" {
		t.Fatalf("accepted: %+v", v.Lines)
	}
	if cash(master)-m0 != 400 || p0-cash(pupil) != 400 {
		t.Errorf("the fee moved: master %+d, pupil %+d", cash(master)-m0, cash(pupil)-p0)
	}
	if got := offer(master, pupil, "14", "0"); got != "apprentice_busy" {
		t.Errorf("an apprentice has one master at a time: %q", got)
	}

	// the hook of a finished shift: extra learning, the master's teaching xp once a day
	shift := func() {
		t.Helper()
		if err := postgres.NewUnitOfWork(e.pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
			return e.ap.OnShift(ctx, tx, loadTestContent(t), pupil.ID, bench, "carpentry", 20, e.clock.Now())
		}); err != nil {
			t.Fatal(err)
		}
	}
	shift()
	if e.scalar(`SELECT xp_extra FROM apprenticeships WHERE id = $1::uuid`, id) != 16 { // master level 3: 150% + 30% = 180%, 20 xp makes 16 more
		t.Errorf("a level 3 master's shift teaches 80 percent more: %d", e.scalar(`SELECT xp_extra FROM apprenticeships WHERE id = $1::uuid`, id))
	}
	if e.scalar(`SELECT COALESCE(xp, 0) FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'teaching'`, master.ID) != 25 {
		t.Error("the master got a day's teaching xp")
	}
	shift()
	shift()
	if e.scalar(`SELECT xp FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'teaching'`, master.ID) != 25 {
		t.Error("the master's teaching xp is once a day")
	}
	if e.scalar(`SELECT shifts FROM apprenticeships WHERE id = $1::uuid`, id) != 3 {
		t.Error("three shifts counted")
	}

	// the exam: the master judges after the term; a failure may be retaken
	e.clock.Advance(15 * 24 * time.Hour)
	exam := func(pass string) string {
		resp, err := e.ap.Exam(testCtx(t), e.as(master, "settlement.apprentice.exam", "exam"), handlers.ApprenticeRequest{ID: id, Pass: pass})
		if err != nil {
			t.Fatal(err)
		}
		return refusedWith(resp)
	}
	if got := exam("fail"); got != "" {
		t.Fatalf("a failing exam: %q", got)
	}
	if e.scalar(`SELECT count(*) FROM certifications WHERE player_id = $1::uuid AND course_code = 'carpentry_craft'`, pupil.ID) != 0 {
		t.Fatal("a failed exam gives no certificate")
	}
	if got := exam("pass"); got != "" {
		t.Fatalf("the retake: %q", got)
	}
	if e.scalar(`SELECT count(*) FROM certifications WHERE player_id = $1::uuid AND course_code = 'carpentry_craft' AND route = 'apprenticeship' AND issued_by = $2::uuid`, pupil.ID, master.ID) != 1 {
		t.Error("the certificate carries the master's name")
	}
	if got := exam("pass"); got != "apprentice_exam_done" {
		t.Errorf("one certificate: %q", got)
	}

	// a dismissal gives back the unearned part of the fee
	third := e.resident()
	certify(t, e.researchEnv, third.ID, "literacy_class")
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, third.ID, e.cityID); err != nil {
		t.Fatal(err)
	}
	grantCash(t, e.pool, third.ID, 1_000)
	cid := e.cityID
	third.CityID = &cid
	if got := offer(master, third, "20", "200"); got != "" {
		t.Fatalf("a second offer: %q", got)
	}
	id2 := e.apprenticeshipOf(third)
	if _, err := e.ap.Accept(testCtx(t), e.as(third, "settlement.apprentice.accept", "accept"), handlers.ApprenticeRequest{ID: id2}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.ap.Dismiss(testCtx(t), e.as(master, "settlement.apprentice.dismiss", "dismiss"), handlers.ApprenticeRequest{ID: id2, Reason: "because"}); refusedWith(resp) != "apprentice_reason" {
		t.Errorf("the reason must be on the list: %q", refusedWith(resp))
	}
	t0 := cash(third)
	if resp, err := e.ap.Dismiss(testCtx(t), e.as(master, "settlement.apprentice.dismiss", "dismiss"), handlers.ApprenticeRequest{ID: id2, Reason: "idle"}); err != nil || resp.Refusal != nil {
		t.Fatalf("the dismissal: %v %v", err, refusedWith(resp))
	}
	if got := cash(third) - t0; got != 200 {
		t.Errorf("the whole fee is back when no day was served: %d", got)
	}
	e.verifyAppr()
}

// The directory lists the craft's teachers within reach, the pool's workshop and the city's school.
func TestTheDirectoryOfTeachers(t *testing.T) {
	e := newApprEnv(t)
	seeker := e.resident()
	e.grant("carpentry")
	e.placeAt("carpentry_workshop", 40, 0)
	if _, err := e.pool.Raw().Exec(testCtx(t), `UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, seeker.ID, e.cityID); err != nil {
		t.Fatal(err)
	}
	resp, err := e.ap.Teachers(testCtx(t), e.as(seeker, "education.teachers", "teachers"), handlers.ApprenticeRequest{Craft: "carpentry"})
	if err != nil {
		t.Fatal(err)
	}
	var v plife.TeachersView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, en := range v.Entries {
		kinds[en.Kind] = true
	}
	if v.Craft != "carpentry" || !kinds[plife.DirKindNPC] || !kinds[plife.DirKindCity] || len(v.Crafts) < 12 {
		t.Fatalf("the directory: %+v", v)
	}
}

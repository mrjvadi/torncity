//go:build integration

// Integration tests of the Activities follow-up (docs/research/2026-10-03-activities-audit.md
// section 7): the fee of a school goes to the settlement treasury and its teacher is
// paid per class, a home teacher is paid by the student less the settlement's tax,
// nobody teaching means nothing is taught, training builds stamina and strength, a
// workplace shift trains its trade, and `admin economy verify` stays green.
package tests

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

type teachEnv struct {
	*laborEnv
	edu   *handlers.EducationHandler
	train *handlers.TrainingHandler
}

func newTeachEnv(t *testing.T) *teachEnv {
	t.Helper()
	l := newLaborEnv(t)
	ctx := testCtx(t)
	uow := postgres.NewUnitOfWork(l.pool, testDefaultLanguage)
	src := staticContentSource{snap: loadTestContent(t)}
	cities := postgres.NewCityRepository(l.pool)
	edu := handlers.NewEducationHandler(uow, workIDs{t}, nil, src, cities, gametime.Scale(1), 5, time.Hour, l.clock.Now).
		WithHomeCity("support").
		WithTeaching(handlers.TeachRules{WageBPS: 6000, MinWage: 40, MaxStudents: 2}, postgres.NewPolicyReader(l.pool, nil), l.village.LaborAvailable)
	rules := handlers.TrainingRules{
		Session: player.TrainingRules{EnergyCost: 10, StaminaGain: 6, StrengthXP: 30, DiminishStamina: 400, StaminaPerMaxEnergy: 50, MaxEnergyBonusCap: 30},
		YardBPS: 4000, GroundBPS: 6000, GymBPS: 10000, GroundFee: 20, GymFee: 60,
	}
	train := handlers.NewTrainingHandler(uow, workIDs{t}, nil, src, cities, rules, "support", time.Hour, l.clock.Now)

	// A teaching circle stands and the settlement knows how to read and write.
	for _, stmt := range []string{
		`INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		 VALUES (gen_random_uuid(), $1::uuid, 'teaching_circle', 70, 70, 'complete', now(), now())`,
		`INSERT INTO settlement_knowledge_owned (id, settlement_id, code, acquired_via, acquired_at)
		 VALUES (gen_random_uuid(), $1::uuid, 'basic_literacy', 'researched', now()) ON CONFLICT DO NOTHING`,
	} {
		if _, err := l.pool.Raw().Exec(ctx, stmt, l.cityID); err != nil {
			t.Fatalf("setting the school up: %v", err)
		}
	}
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`DELETE FROM course_teachers WHERE settlement_id = $1::uuid`,
			`DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'teaching_circle'`,
			`DELETE FROM settlement_knowledge_owned WHERE settlement_id = $1::uuid AND code = 'basic_literacy'`,
		} {
			if _, err := l.pool.Raw().Exec(c, stmt, l.cityID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	return &teachEnv{laborEnv: l, edu: edu, train: train}
}

// pupil is a resident with cash who stands in the village.
func (e *teachEnv) pupil(cash int64) *application.Player {
	p := e.resident()
	if _, err := e.pool.Raw().Exec(testCtx(e.t), `UPDATE players SET city_id = $1::uuid WHERE id = $2::uuid`, e.cityID, p.ID); err != nil {
		e.t.Fatal(err)
	}
	cid := e.cityID
	p.CityID = &cid
	if cash > 0 {
		grantCash(e.t, e.pool, p.ID, cash)
	}
	e.t.Cleanup(func() {
		c := testCtx(e.t)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM class_seats WHERE enrollment_id IN (SELECT id FROM enrollments WHERE player_id = $1::uuid)`, p.ID)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM course_teachers WHERE player_id = $1::uuid`, p.ID)
		purgeStudiesFor(e.t, e.pool, p.ID)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM game_actions WHERE actor_id = $1::uuid`, p.ID)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM player_skills WHERE player_id = $1::uuid`, p.ID)
		purgeLedgerFor(e.t, e.pool, p.ID)
	})
	return p
}

func (e *teachEnv) reasonSum(reason string, positiveOnly bool) int64 {
	q := `SELECT COALESCE(SUM(amount), 0)::bigint FROM ledger_entries WHERE reason = $1`
	if positiveOnly {
		q += ` AND amount > 0`
	}
	return e.scalar(q, reason)
}

func (e *teachEnv) view(p *application.Player, course string) map[string]any {
	resp, err := e.edu.View(testCtx(e.t), e.as(p, "education.view", "view"), handlers.CourseRequest{Course: course})
	if err != nil {
		e.t.Fatalf("view: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(resp.View, &out); err != nil {
		e.t.Fatalf("view json: %v (%s)", err, resp.Text)
	}
	return out
}

func (e *teachEnv) enrolRows(p *application.Player) int64 {
	return e.scalar(`SELECT count(*) FROM enrollments WHERE player_id = $1::uuid`, p.ID)
}

// A course nobody teaches is not taught; an NPC teacher the head hires opens it; the
// fee goes to the treasury and the class pays the teacher into the sink, once.
func TestSchoolFeeAndTheNPCTeacher(t *testing.T) {
	e := newTeachEnv(t)
	ctx := testCtx(t)
	v0, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v0.Village {
		t.Skip("village tables are not applied")
	}
	student := e.pupil(1000)

	// No teacher: the school and the research stand, yet the course is not taught.
	resp, err := e.edu.Enroll(ctx, e.as(student, "education.enroll", "enroll"), handlers.CourseRequest{Course: "literacy_class", Method: "cash"})
	if err != nil {
		t.Fatal(err)
	}
	if e.enrolRows(student) != 0 {
		t.Fatalf("enrolled in a course nobody teaches (%s)", resp.Text)
	}

	// A resident who is not the head cannot hire.
	if _, err := e.edu.TeacherHire(ctx, e.as(student, "education.hire", "hire"), handlers.TeachRequest{Course: "literacy_class"}); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT count(*) FROM course_teachers WHERE settlement_id = $1::uuid`, e.cityID); got != 0 {
		t.Fatalf("a resident hired a teacher: %d posts", got)
	}

	// The head hires the school's teacher.
	if _, err := e.edu.TeacherHire(ctx, e.as(e.head, "education.hire", "hire"), handlers.TeachRequest{Course: "literacy_class"}); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT count(*) FROM course_teachers WHERE settlement_id = $1::uuid AND kind = 'npc' AND ended_at IS NULL`, e.cityID); got != 1 {
		t.Fatalf("%d NPC teachers after hiring, want 1", got)
	}
	// Hiring twice is refused, not doubled.
	if _, err := e.edu.TeacherHire(ctx, e.as(e.head, "education.hire", "hire"), handlers.TeachRequest{Course: "literacy_class"}); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT count(*) FROM course_teachers WHERE settlement_id = $1::uuid AND ended_at IS NULL`, e.cityID); got != 1 {
		t.Fatalf("%d teachers after a second hire, want still 1", got)
	}

	treasury0 := e.treasury()
	fee0 := e.reasonSum("course_fee", true)
	meta := e.as(student, "education.enroll", "enroll")
	if resp, err = e.edu.Enroll(ctx, meta, handlers.CourseRequest{Course: "literacy_class", Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	if e.enrolRows(student) != 1 {
		t.Fatalf("not enrolled although a teacher is hired (%s)", resp.Text)
	}
	if got := e.treasury() - treasury0; got != 200 {
		t.Fatalf("the treasury gained %d from the fee, want 200", got)
	}
	if got := e.reasonSum("course_fee", true) - fee0; got != 200 {
		t.Fatalf("course_fee credits %d, want 200", got)
	}
	var wage int64
	if err := e.pool.Raw().QueryRow(ctx, `SELECT wage FROM class_seats WHERE enrollment_id IN (SELECT id FROM enrollments WHERE player_id = $1::uuid)`, student.ID).Scan(&wage); err != nil {
		t.Fatalf("no seat: %v", err)
	}
	if wage != 120 {
		t.Fatalf("the class owes %d, want 120 (60%% of 200)", wage)
	}

	// The class ends: the treasury pays the NPC teacher into the sink, once.
	var enrollmentID, actionID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text FROM enrollments WHERE player_id = $1::uuid`, student.ID).Scan(&enrollmentID, &actionID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(48 * time.Hour)
	treasury1 := e.treasury()
	for range 2 {
		if _, err := e.edu.Complete(ctx, e.as(student, "education.complete", "complete"), handlers.CompleteCourseRequest{
			ActionID: actionID, ActorID: student.ID, ReferenceType: "enrollments", ReferenceID: enrollmentID}); err != nil {
			t.Fatal(err)
		}
	}
	if got := treasury1 - e.treasury(); got != 120 {
		t.Fatalf("the treasury paid %d for the class, want 120 once", got)
	}
	if got := e.reasonSum("teacher_wage_npc", true); got != 120 {
		t.Fatalf("teacher_wage_npc credits %d, want 120", got)
	}
	if got := e.scalar(`SELECT count(*) FROM certifications WHERE player_id = $1::uuid AND course_code = 'literacy_class'`, student.ID); got != 1 {
		t.Fatalf("%d certificates, want 1", got)
	}

	// The head dismisses the teacher: the course is no longer taught.
	var postID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text FROM course_teachers WHERE settlement_id = $1::uuid AND ended_at IS NULL`, e.cityID).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.edu.TeacherEnd(ctx, e.as(e.head, "education.unteach", "unteach"), handlers.TeachRequest{Course: "literacy_class", ID: postID}); err != nil {
		t.Fatal(err)
	}
	other := e.pupil(1000)
	if _, err := e.edu.Enroll(ctx, e.as(other, "education.enroll", "enroll"), handlers.CourseRequest{Course: "literacy_class", Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	if e.enrolRows(other) != 0 {
		t.Fatal("enrolled after the only teacher was dismissed")
	}

	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if v.LedgerSum != "0" || len(v.Unbalanced) != 0 || len(v.Drifted) != 0 || !v.VillageInvariants.TeachingOK() {
		t.Fatalf("economy verify failed: sum %s, %d unbalanced, %d drifted, teaching %+v", v.LedgerSum, len(v.Unbalanced), len(v.Drifted), v.VillageInvariants)
	}
}

func (e *teachEnv) treasury() int64 {
	return e.scalar(`SELECT balance FROM accounts WHERE kind = 'city_treasury' AND owner_id = $1::uuid`, e.cityID)
}

// A certificate holder teaches at home: no building or research is needed, the student
// pays them directly and the settlement's income tax comes off; no wage is owed by the
// treasury. A teacher does not teach themselves.
func TestHomeTeacherIsPaidByTheStudent(t *testing.T) {
	e := newTeachEnv(t)
	ctx := testCtx(t)
	teacher := e.pupil(1000)
	student := e.pupil(1000)
	// The teacher earns the certificate the real way: the school's NPC teaches the class.
	if _, err := e.edu.TeacherHire(ctx, e.as(e.head, "education.hire", "hire"), handlers.TeachRequest{Course: "literacy_class"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.edu.Enroll(ctx, e.as(teacher, "education.enroll", "enroll"), handlers.CourseRequest{Course: "literacy_class", Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	var enrollmentID, actionID string
	if err := e.pool.Raw().QueryRow(ctx, `SELECT id::text, game_action_id::text FROM enrollments WHERE player_id = $1::uuid`, teacher.ID).Scan(&enrollmentID, &actionID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(48 * time.Hour)
	if _, err := e.edu.Complete(ctx, e.as(teacher, "education.complete", "complete"), handlers.CompleteCourseRequest{
		ActionID: actionID, ActorID: teacher.ID, ReferenceType: "enrollments", ReferenceID: enrollmentID}); err != nil {
		t.Fatal(err)
	}
	// Then the school's teacher leaves and the school is gone: only a home teacher can teach.
	if _, err := e.pool.Raw().Exec(ctx, `UPDATE course_teachers SET ended_at = now() WHERE settlement_id = $1::uuid AND kind = 'npc'`, e.cityID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Raw().Exec(ctx, `DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'teaching_circle'`, e.cityID); err != nil {
		t.Fatal(err)
	}
	// without the certificate nobody may teach it
	if _, err := e.edu.TeacherStart(ctx, e.as(student, "education.teach", "teach"), handlers.TeachRequest{Course: "literacy_class", Mode: "home"}); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT count(*) FROM course_teachers WHERE settlement_id = $1::uuid AND ended_at IS NULL`, e.cityID); got != 0 {
		t.Fatalf("a player without the certificate teaches: %d posts", got)
	}
	if _, err := e.edu.TeacherStart(ctx, e.as(teacher, "education.teach", "teach"), handlers.TeachRequest{Course: "literacy_class", Mode: "home"}); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT count(*) FROM course_teachers WHERE settlement_id = $1::uuid AND employer = 'self' AND ended_at IS NULL`, e.cityID); got != 1 {
		t.Fatalf("%d home posts, want 1", got)
	}
	treasury0 := e.treasury()
	cash0 := e.scalar(`SELECT COALESCE(balance, 0) FROM accounts WHERE kind = 'player_cash' AND owner_id = $1::uuid`, teacher.ID)
	if _, err := e.edu.Enroll(ctx, e.as(student, "education.enroll", "enroll"), handlers.CourseRequest{Course: "literacy_class", Method: "cash"}); err != nil {
		t.Fatal(err)
	}
	if e.enrolRows(student) != 1 {
		t.Fatal("the student could not enrol with a home teacher")
	}
	teacherGot := e.scalar(`SELECT balance FROM accounts WHERE kind = 'player_cash' AND owner_id = $1::uuid`, teacher.ID) - cash0
	taxGot := e.treasury() - treasury0
	if teacherGot+taxGot != 200 || teacherGot <= 0 {
		t.Fatalf("tuition split: teacher %d + tax %d, want 200 in all", teacherGot, taxGot)
	}
	if e.scalar(`SELECT wage FROM class_seats WHERE enrollment_id IN (SELECT id FROM enrollments WHERE player_id = $1::uuid)`, student.ID) != 0 {
		t.Fatal("the treasury owes a home teacher a wage")
	}
	if e.reasonSum("tuition", true) != 200 {
		t.Fatalf("tuition credits %d, want 200", e.reasonSum("tuition", true))
	}
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if v.LedgerSum != "0" || len(v.Unbalanced) != 0 || len(v.Drifted) != 0 || !v.VillageInvariants.TeachingOK() {
		t.Fatalf("economy verify failed: sum %s, %d unbalanced, %d drifted, teaching %+v", v.LedgerSum, len(v.Unbalanced), len(v.Drifted), v.VillageInvariants)
	}
}

// Training: a session on open ground spends energy and gives stamina and strength; a
// training ground charges the treasury's fee; the same session is refused without energy.
func TestTrainingSessions(t *testing.T) {
	e := newTeachEnv(t)
	ctx := testCtx(t)
	p := e.pupil(500)

	resp, err := e.train.Start(ctx, e.as(p, "training.start", "start"), handlers.TrainRequest{Venue: "yard"})
	if err != nil {
		t.Fatal(err)
	}
	var st struct{ Energy, Stamina int }
	if err := e.pool.Raw().QueryRow(ctx, `SELECT energy, stamina FROM player_stats WHERE player_id = $1::uuid`, p.ID).Scan(&st.Energy, &st.Stamina); err != nil {
		t.Fatalf("%v (%s)", err, resp.Text)
	}
	if st.Energy != player.DefaultMaxEnergy-10 || st.Stamina != player.DefaultStamina+2 {
		t.Fatalf("after a yard session: energy %d stamina %d", st.Energy, st.Stamina)
	}
	if got := e.scalar(`SELECT COALESCE(SUM(xp), 0) FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'strength'`, p.ID); got != 12 {
		t.Fatalf("strength xp %d, want 12 (30 at 40%%)", got)
	}
	// the replayed request changes nothing
	meta := e.as(p, "training.start", "start")
	for range 2 {
		if _, err := e.train.Start(ctx, meta, handlers.TrainRequest{Venue: "yard"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.scalar(`SELECT energy FROM player_stats WHERE player_id = $1::uuid`, p.ID); got != int64(player.DefaultMaxEnergy-20) {
		t.Fatalf("a replayed session spent twice: energy %d", got)
	}

	// no ground stands: the venue is refused
	before := e.scalar(`SELECT energy FROM player_stats WHERE player_id = $1::uuid`, p.ID)
	if _, err := e.train.Start(ctx, e.as(p, "training.start", "start"), handlers.TrainRequest{Venue: "ground"}); err != nil {
		t.Logf("ground refused as expected: %v", err)
	}
	if e.scalar(`SELECT energy FROM player_stats WHERE player_id = $1::uuid`, p.ID) != before {
		t.Fatal("trained at a ground that does not stand")
	}

	// the ground stands: the fee goes to the treasury
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES (gen_random_uuid(), $1::uuid, 'training_ground', 72, 72, 'complete', now(), now())`, e.cityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'training_ground'`, e.cityID)
	})
	treasury0 := e.treasury()
	if _, err := e.train.Start(ctx, e.as(p, "training.start", "start"), handlers.TrainRequest{Venue: "ground"}); err != nil {
		t.Fatal(err)
	}
	if got := e.treasury() - treasury0; got != 20 {
		t.Fatalf("the treasury gained %d from the session, want 20", got)
	}
	if e.reasonSum("training_fee", true) != 20 {
		t.Fatalf("training_fee credits %d, want 20", e.reasonSum("training_fee", true))
	}
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if v.LedgerSum != "0" || len(v.Unbalanced) != 0 || len(v.Drifted) != 0 || !v.VillageInvariants.TeachingOK() {
		t.Fatalf("economy verify failed: sum %s, %d unbalanced, %d drifted, teaching %+v", v.LedgerSum, len(v.Unbalanced), len(v.Drifted), v.VillageInvariants)
	}
}

// The training ground is a working building: with a trainer (a free labourer and a
// treasury that can pay a session's wage) it trains at its own rate, the fee goes to
// the treasury and the wage to the sink; with none it trains like open ground, free.
func TestTrainerSeat(t *testing.T) {
	e := newTeachEnv(t)
	ctx := testCtx(t)
	e.train.WithTrainer(e.village.TrainerSeat)
	p := e.pupil(500)
	if _, err := e.pool.Raw().Exec(ctx, `INSERT INTO settlement_buildings (id, settlement_id, type_code, lot_x, lot_y, status, queued_at, completed_at)
		VALUES (gen_random_uuid(), $1::uuid, 'training_ground', 72, 72, 'complete', now(), now())`, e.cityID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Raw().Exec(testCtx(t), `DELETE FROM settlement_buildings WHERE settlement_id = $1::uuid AND type_code = 'training_ground'`, e.cityID)
	})
	xp := func() int64 {
		return e.scalar(`SELECT COALESCE(SUM(xp), 0) FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'strength'`, p.ID)
	}
	treasury0 := e.treasury()
	if _, err := e.train.Start(ctx, e.as(p, "training.start", "start"), handlers.TrainRequest{Venue: "ground"}); err != nil {
		t.Fatal(err)
	}
	wages := e.reasonSum("trainer_wage", true)
	if wages <= 0 {
		t.Fatalf("the trainer was not paid (%d)", wages)
	}
	if got := e.treasury() - treasury0; got != 20-wages {
		t.Errorf("treasury changed by %d, want fee 20 less the wage %d", got, wages)
	}
	if got := xp(); got != 18 { // 30 at 60 percent
		t.Errorf("strength xp at a kept ground = %d, want 18", got)
	}
	// the treasury can no longer pay the coach: the ground trains like open ground, for nothing
	var bal int64
	uow := postgres.NewUnitOfWork(e.pool, testDefaultLanguage)
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, e.cityID)
		if err != nil {
			return err
		}
		bal = e.treasury()
		_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{ID: newUUID(t), Reason: application.ReasonSettlementConstruction,
			CreatedAt: e.clock.Now(), ReferenceType: "test", ReferenceID: newUUID(t),
			Entries: []application.LedgerEntry{{AccountID: acct.ID, Amount: money.FromMinor(-bal)}, {AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(bal)}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := xp()
	if _, err := e.train.Start(ctx, e.as(p, "training.start", "start"), handlers.TrainRequest{Venue: "ground"}); err != nil {
		t.Fatal(err)
	}
	if got := xp() - before; got != 12 { // 30 at the yard's 40 percent
		t.Errorf("strength xp at an unkept ground = %d, want 12", got)
	}
	if e.treasury() != 0 || e.reasonSum("trainer_wage", true) != wages {
		t.Errorf("an unkept ground took a fee or paid a wage")
	}
	seedTreasury(t, e.pool, e.cityID, bal) // leave the books as the cleanup expects them
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if v.LedgerSum != "0" || len(v.Unbalanced) != 0 || v.VillageInvariants.ServiceMisrouted != 0 {
		t.Fatalf("economy verify failed: %+v", v.VillageInvariants)
	}
}

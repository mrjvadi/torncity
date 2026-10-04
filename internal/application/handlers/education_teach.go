package handlers

import (
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Teachers of courses (docs/research/2026-10-03-activities-audit.md section 7).
//
// A course in a founded settlement is taught only while somebody teaches it:
//
//   - the school's NPC teacher, hired by the head from the settlement's labour pool
//     (ADR 0037); each student's class costs the treasury a wage, paid at the end of
//     the class into the sink (an NPC is not a player);
//   - a player holding the course's certificate who takes the school's post; the same
//     wage, paid to them;
//   - a player holding the certificate who teaches at home (ADR 0038 owner decision 6
//     point 3): no building or research is needed, the student pays them directly and
//     the settlement's income tax comes off.
//
// The neutral city keeps the state's schools: no teacher is asked for, the fee goes to
// the sink as before.

// TeachRules is the tuning of teaching (config education.*).
type TeachRules struct {
	// WageBPS is the share of the course's listed fee a class pays its teacher; MinWage
	// the least a class pays; MaxStudents how many students one teacher teaches at once.
	WageBPS, MinWage, MaxStudents int64
}

func (r TeachRules) max() int {
	if r.MaxStudents <= 0 {
		return 12
	}
	return int(r.MaxStudents)
}

// wage is what one student's class pays the teacher for a course of this listed fee.
func (r TeachRules) wage(listFee int64) int64 {
	w := listFee * r.WageBPS / 10_000
	if w < r.MinWage {
		w = r.MinWage
	}
	return w
}

// WithTeaching sets the teaching rules, the policy reader (the settlement's income
// tax) and how many free labourers a settlement still has (the NPC teacher is one).
func (h *EducationHandler) WithTeaching(r TeachRules, policy application.PolicyReader,
	pool func(ctx context.Context, tx application.Tx, settlementID string) (int64, error),
) *EducationHandler {
	h.teach, h.policy, h.pool = r, policy, pool
	return h
}

// TeachRequest names a course and what to do about teaching it: Mode is "school" or
// "home" for education.teach.start; ID the post for education.teach.end.
type TeachRequest struct {
	Course string `json:"course"`
	Mode   string `json:"mode,omitempty"`
	ID     string `json:"id,omitempty"`
}

// teacherGate weighs who teaches a course in the founded settlement of c for a
// student: the school's teachers count only while the school can teach the course
// (judged), a home teacher always counts; a teacher never teaches themselves; one who
// has a full class is skipped. Nil means nobody can take the student. A content city
// has no teacher post (nil, nil) and teaches everything.
func (h *EducationHandler) teacherGate(ctx context.Context, tx application.Tx, c courseHere, code string, judged bool,
	studentID string,
) (*application.CourseTeacher, error) {
	if c.all {
		return nil, nil
	}
	list, err := tx.Education().Teachers(ctx, c.settlement.CityID, code)
	if err != nil {
		return nil, err
	}
	var best *application.CourseTeacher
	bestLoad := 0
	for i := range list {
		t := list[i]
		if t.PlayerID != "" && t.PlayerID == studentID {
			continue
		}
		if t.Employer == application.EmployerSettlement && !judged {
			continue
		}
		n, err := tx.Education().Students(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		if n >= h.teach.max() {
			continue
		}
		school := t.Employer == application.EmployerSettlement
		better := best == nil ||
			(school && best.Employer != application.EmployerSettlement) ||
			(school == (best.Employer == application.EmployerSettlement) && n < bestLoad)
		if better {
			best, bestLoad = &t, n
		}
	}
	return best, nil
}

// withTeacher applies the teacher to a settlement's judgement of a course: a course the
// school could teach but nobody teaches is «not here» with the teacher as what it lacks;
// a course the school cannot teach is taught anyway while somebody teaches it at home.
func (h *EducationHandler) withTeacher(ctx context.Context, tx application.Tx, c courseHere, code, studentID string,
	taught, reachable bool, needs []presentation.CourseNeed,
) (bool, bool, []presentation.CourseNeed, *application.CourseTeacher, error) {
	if c.all {
		return taught, reachable, needs, nil, nil
	}
	t, err := h.teacherGate(ctx, tx, c, code, taught, studentID)
	if err != nil {
		return false, false, nil, nil, err
	}
	switch {
	case t != nil:
		return true, true, nil, t, nil
	case taught:
		return false, true, append(needs, presentation.CourseNeed{Kind: presentation.CourseNeedTeacher}), nil, nil
	}
	return taught, reachable, needs, nil, nil
}

// settlementOfPlayer is the founded settlement the player stands in, and its course
// standing; ok is false when they are in a content city or nowhere.
func (h *EducationHandler) settlementOfPlayer(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	p *application.Player,
) (courseHere, bool, error) {
	if p.CityID == nil || *p.CityID == "" {
		return courseHere{}, false, nil
	}
	c, err := h.courseHereOf(ctx, tx, snap, *p.CityID)
	if err != nil {
		return c, false, err
	}
	return c, !c.all, nil
}

func (h *EducationHandler) judgeCourse(snap *content.Snapshot, c courseHere, code string) bool {
	tag, tagged := snap.AvailabilityTag("course", code)
	taught, _, _ := c.judge(snap, tag, tagged)
	return taught
}

// TeacherHire handles education.teacher.hire: the head hires the school's NPC teacher
// for a course the school can teach. The teacher is one labourer of the settlement's
// pool (ADR 0037): a settlement with nobody free cannot hire.
func (h *EducationHandler) TeacherHire(ctx context.Context, meta envelope.Metadata, req TeachRequest) (*presentation.Response, error) {
	return h.teachCommand(ctx, meta, req, func(ctx context.Context, tx application.Tx, snap *content.Snapshot,
		p *application.Player, c courseHere, now time.Time,
	) (string, error) {
		if _, err := requirePermission(ctx, tx, c.settlement, p.ID, charter.StaffHire); err != nil {
			if stderrors.Is(err, application.ErrNotOfficeHolder) {
				return "", refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqNotHead}})
			}
			return "", err
		}
		if !h.judgeCourse(snap, c, req.Course) {
			return "", refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqCourseCity}})
		}
		if h.pool != nil {
			free, err := h.pool(ctx, tx, c.settlement.CityID)
			if err != nil {
				return "", err
			}
			if free < 1 {
				return "", refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqTeacherNoPool}})
			}
		}
		id := h.ids.NewID()
		if err := tx.Education().AddTeacher(ctx, application.CourseTeacher{
			ID: id, SettlementID: c.settlement.CityID, CourseCode: req.Course, Kind: application.TeacherNPC,
			Employer: application.EmployerSettlement, StartedAt: now,
		}); err != nil {
			return "", teachAddRefusal(err)
		}
		return id, nil
	}, "teacher_hired")
}

// TeacherStart handles education.teach.start: a player holding the course's
// certificate takes the school's post (Mode school) or teaches at home (Mode home).
func (h *EducationHandler) TeacherStart(ctx context.Context, meta envelope.Metadata, req TeachRequest) (*presentation.Response, error) {
	return h.teachCommand(ctx, meta, req, func(ctx context.Context, tx application.Tx, snap *content.Snapshot,
		p *application.Player, c courseHere, now time.Time,
	) (string, error) {
		def, ok := snap.CourseDef(req.Course)
		if !ok || !def.Certifies {
			return "", refuse(plife.RefusalCourseNotFound, nil)
		}
		certs, err := tx.Education().Certifications(ctx, p.ID)
		if err != nil {
			return "", err
		}
		held := false
		for _, cert := range certs {
			held = held || cert.CourseCode == req.Course
		}
		if !held {
			return "", refuse(plife.RefusalCourseRequirements, []plife.Requirement{{
				Kind: presentation.ReqCertificate, CourseCode: def.Code, CourseName: def.Name,
			}})
		}
		employer := application.EmployerSelf
		if req.Mode == plife.TeachModeSchool {
			employer = application.EmployerSettlement
			if !h.judgeCourse(snap, c, req.Course) {
				return "", refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqCourseCity}})
			}
		}
		id := h.ids.NewID()
		if err := tx.Education().AddTeacher(ctx, application.CourseTeacher{
			ID: id, SettlementID: c.settlement.CityID, CourseCode: req.Course, Kind: application.TeacherPlayer,
			PlayerID: p.ID, Employer: employer, StartedAt: now,
		}); err != nil {
			return "", teachAddRefusal(err)
		}
		return id, nil
	}, "teacher_started")
}

// TeacherEnd handles education.teach.end: the holder leaves the post, or the head
// dismisses a school teacher. Classes already under way finish and are still paid.
func (h *EducationHandler) TeacherEnd(ctx context.Context, meta envelope.Metadata, req TeachRequest) (*presentation.Response, error) {
	return h.teachCommand(ctx, meta, req, func(ctx context.Context, tx application.Tx, snap *content.Snapshot,
		p *application.Player, c courseHere, now time.Time,
	) (string, error) {
		t, err := tx.Education().Teacher(ctx, req.ID)
		if err != nil || t.SettlementID != c.settlement.CityID || t.CourseCode != req.Course {
			if err == nil || stderrors.Is(err, application.ErrNoTeacher) {
				return "", refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqNotTeaching}})
			}
			return "", err
		}
		if t.PlayerID != p.ID {
			// not their own post: only the head ends a school's teacher
			if t.Employer != application.EmployerSettlement {
				return "", refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqNotTeaching}})
			}
			if _, err := requirePermission(ctx, tx, c.settlement, p.ID, charter.StaffFire); err != nil {
				if stderrors.Is(err, application.ErrNotOfficeHolder) {
					return "", refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqNotHead}})
				}
				return "", err
			}
		}
		if _, err := tx.Education().EndTeacher(ctx, t.ID, now); err != nil {
			return "", err
		}
		return t.ID, nil
	}, "teacher_ended")
}

func teachAddRefusal(err error) error {
	if stderrors.Is(err, application.ErrAlreadyTeaching) {
		return refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqAlreadyTeaching}})
	}
	return err
}

// teachCommand is the shape the three teaching commands share: one idempotent
// transaction in the settlement the player stands in, then the course's screen.
func (h *EducationHandler) teachCommand(ctx context.Context, meta envelope.Metadata, req TeachRequest,
	run func(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, c courseHere, now time.Time) (string, error),
	event string,
) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	if req.Course == "" {
		return nil, errors.InvalidInput("a course is required")
	}
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		if _, ok := snap.CourseDef(req.Course); !ok {
			return refuse(plife.RefusalCourseNotFound, nil)
		}
		c, ok, err := h.settlementOfPlayer(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		if !ok {
			// only a founded settlement has teaching posts
			return refuse(plife.RefusalCourseRequirements, []plife.Requirement{{Kind: presentation.ReqResidence}})
		}
		now := h.now()
		id, err := run(ctx, tx, snap, p, c, now)
		if err != nil {
			return err
		}
		return appendEducationEvent(ctx, tx, meta, event, id, map[string]any{
			"teacher_id": id, "player_id": p.ID, "settlement_id": c.settlement.CityID, "course": req.Course,
			"content_version": snap.Version(),
		})
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return h.View(ctx, meta, CourseRequest{Course: req.Course})
}

// staffLines lists the teachers of a course in the viewer's settlement and what the
// viewer may do about teaching it. Nil in a content city.
func (h *EducationHandler) staffLines(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	c courseHere, def content.CourseDef, held bool, listFee int64,
) ([]plife.TeacherLine, *plife.TeachingView, error) {
	if c.all {
		return nil, nil, nil
	}
	judged := h.judgeCourse(snap, c, def.Code)
	list, err := tx.Education().Teachers(ctx, c.settlement.CityID, def.Code)
	if err != nil {
		return nil, nil, err
	}
	head := hasPermission(ctx, tx, c.settlement, p.ID, charter.StaffHire)
	tv := &plife.TeachingView{SchoolWage: h.teach.wage(listFee), HireWage: h.teach.wage(listFee)}
	if h.policy != nil {
		if city, err := h.cities.ByID(ctx, c.settlement.CityID); err == nil {
			if pol, err := readLabourPolicy(ctx, h.policy, *city); err == nil {
				tv.TaxBPS = pol.IncomeTaxBPS
			}
		}
	}
	npc, mine := false, false
	var lines []plife.TeacherLine
	for _, t := range list {
		n, err := tx.Education().Students(ctx, t.ID)
		if err != nil {
			return nil, nil, err
		}
		line := plife.TeacherLine{ID: t.ID, Students: n, Max: h.teach.max(), Mine: t.PlayerID == p.ID}
		switch {
		case t.Kind == application.TeacherNPC:
			line.Kind, npc = plife.TeacherKindNPC, true
		case t.Employer == application.EmployerSettlement:
			line.Kind = plife.TeacherKindSchool
		default:
			line.Kind = plife.TeacherKindHome
		}
		if t.PlayerID != "" {
			if tp, err := tx.Players().GetByID(ctx, t.PlayerID); err == nil {
				line.Name = tp.DisplayName
			}
		}
		line.CanEnd = line.Mine || (head && t.Employer == application.EmployerSettlement)
		mine = mine || line.Mine
		lines = append(lines, line)
	}
	if head && judged && !npc {
		tv.CanHire = true
		if h.pool != nil {
			free, err := h.pool(ctx, tx, c.settlement.CityID)
			if err != nil {
				return nil, nil, err
			}
			tv.NoPool = free < 1
		}
	}
	if held && !mine {
		tv.CanSchool, tv.CanHome = judged, true
	}
	return lines, tv, nil
}

// teachMoney settles the fee of a class at enrolment and returns the wage the class
// will owe its teacher at the end. fee is what the student pays after the subsidy
// (school) or the listed fee (home teacher); listFee the course's listed fee.
func (h *EducationHandler) classLedger(ctx context.Context, tx application.Tx, t *application.CourseTeacher,
	fee money.Amount,
) (reason application.Reason, to []application.LedgerEntry, err error) {
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, t.SettlementID)
	if err != nil {
		return "", nil, err
	}
	if t.Employer == application.EmployerSettlement {
		return application.ReasonCourseFee, []application.LedgerEntry{{AccountID: treasury.ID, Amount: fee}}, nil
	}
	taxBPS := 0
	if h.policy != nil {
		if city, cerr := h.cities.ByID(ctx, t.SettlementID); cerr == nil {
			if pol, perr := readLabourPolicy(ctx, h.policy, *city); perr == nil {
				taxBPS = pol.IncomeTaxBPS
			}
		}
	}
	tax := fee.Minor() * int64(taxBPS) / 10_000
	cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, t.PlayerID)
	if err != nil {
		return "", nil, err
	}
	to = []application.LedgerEntry{{AccountID: cash.ID, Amount: money.FromMinor(fee.Minor() - tax)}}
	if tax > 0 {
		to = append(to, application.LedgerEntry{AccountID: treasury.ID, Amount: money.FromMinor(tax)})
	}
	return application.ReasonTuition, to, nil
}

// payTeacher pays the wage a finished class owes its teacher, exactly once: the seat's
// conditional update decides, the treasury pays what it holds up to the wage.
func (h *EducationHandler) payTeacher(ctx context.Context, tx application.Tx, enrollmentID string, now time.Time) error {
	seat, err := tx.Education().SeatOf(ctx, enrollmentID)
	if err != nil || seat == nil || seat.WagePaid {
		return err
	}
	if seat.Wage <= 0 {
		_, err := tx.Education().PaySeat(ctx, enrollmentID, now, 0)
		return err
	}
	t, err := tx.Education().TeacherAny(ctx, seat.TeacherID)
	if err != nil {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, seat.SettlementID)
	if err != nil {
		return err
	}
	pay := min(seat.Wage, max(treasury.Balance.Minor(), 0))
	fresh, err := tx.Education().PaySeat(ctx, enrollmentID, now, pay)
	if err != nil || !fresh || pay <= 0 {
		return err
	}
	var payee application.Account
	reason := application.ReasonTeacherWage
	if t.Kind == application.TeacherNPC {
		reason = application.ReasonTeacherWageNPC
		payee = application.Account{ID: application.SystemSinkAccountID}
	} else if payee, err = tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, t.PlayerID); err != nil {
		return err
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason: reason, ReferenceType: "class_seats", ReferenceID: enrollmentID, CreatedAt: now,
		Entries: []application.LedgerEntry{
			{AccountID: treasury.ID, Amount: money.FromMinor(-pay)},
			{AccountID: payee.ID, Amount: money.FromMinor(pay)},
		},
	})
	return err
}

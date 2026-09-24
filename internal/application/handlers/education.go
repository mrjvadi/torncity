package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/education"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// CourseRequest names a course by its code from education.yml. Method is how
// the tuition is paid — cash or card — on education.enroll; without one, the
// course's price screen is shown with a button per way to pay.
type CourseRequest struct {
	Course string `json:"course"`
	Method string `json:"method,omitempty"`
}

// EducationActionPayload is the jsonb an enrolment writes onto its
// game_actions row, and the shape read back when the course comes due. It
// repeats the row's columns for the reason TravelActionPayload gives.
type EducationActionPayload struct {
	EnrollmentID string `json:"enrollment_id"`
	PlayerID     string `json:"player_id"`
	CourseCode   string `json:"course_code"`
}

// CompleteCourseRequest is education.complete, published by the scheduler
// when a course comes due. It mirrors ArriveTravelRequest.
type CompleteCourseRequest struct {
	ActionID      string          `json:"action_id"`
	ActorID       string          `json:"actor_id"`
	ReferenceType string          `json:"reference_type"`
	ReferenceID   string          `json:"reference_id"`
	Payload       json.RawMessage `json:"payload"`
}

// EducationHandler serves study: the courses on offer, enrolling, and the
// completion the schedule produces.
//
// # Who is paid
//
// Every institution today belongs to nobody, so a fee leaves the economy
// into system_sink under ReasonServiceFee — the drain ADR 0009 lists for
// education. A course run by a player company will pay that company's
// treasury instead (education.InstitutionCompany), in chargeFee.
type EducationHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	// scale is the game clock: a course's duration is game time, and the
	// student waits it through this (config game.time_scale).
	scale gametime.Scale

	pageSize       int
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewEducationHandler wires the handler.
func NewEducationHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	source ContentSource,
	cities application.CityRepository,
	scale gametime.Scale,
	pageSize int,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *EducationHandler {
	if source == nil || cities == nil {
		panic("handlers: NewEducationHandler requires content and cities")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewEducationHandler requires a positive idempotency ttl")
	}
	if scale.Validate() != nil {
		panic("handlers: NewEducationHandler requires a game clock within 1..gametime.MaxScale")
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &EducationHandler{
		uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, scale: scale,
		pageSize: pageSize, idempotencyTTL: idempotencyTTL, now: now,
	}
}

func (h *EducationHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

func (h *EducationHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if r, ok := asRefusal(err); ok {
		return screens.Refusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	return nil, err
}

// hereCode is the code of the city the player stands in, "" while nowhere.
func (h *EducationHandler) hereCode(ctx context.Context, s standing) (string, error) {
	if s.here() == "" {
		return "", nil
	}
	city, err := h.cities.ByID(ctx, s.here())
	if err != nil {
		return "", err
	}
	return city.Code, nil
}

// visible reports whether a course is on offer to this player here: taught
// here or anywhere, its prerequisites held, and — for a certifying course —
// not already certified. A course the player cannot reach yet is not listed:
// it appears once they earn what opens it or arrive where it is taught.
func visible(def content.CourseDef, s standing, here string) bool {
	if def.City != "" && (def.City != here || s.travelling) {
		return false
	}
	if def.Certifies && s.holds(def.Code) {
		return false
	}
	for _, pre := range def.Prerequisites {
		if !s.holds(pre) {
			return false
		}
	}
	return true
}

// applicant is the standing as the study rules take it.
func (s standing) applicant(here string, current *application.Enrollment) education.Applicant {
	a := education.Applicant{
		Stats:          domainStats(s.stats),
		CityCode:       here,
		Certifications: certificateCodes(s.certs),
	}
	if current != nil {
		a.Current = domainEnrollment(*current)
	}
	return a
}

func domainEnrollment(e application.Enrollment) education.Enrollment {
	d := education.Enrollment{
		CourseCode:  e.CourseCode,
		Status:      education.Status(e.Status),
		StartedAt:   e.StartedAt,
		CompletesAt: e.CompletesAt,
	}
	if e.PausedAt != nil {
		d.Paused = *e.PausedAt
	}
	return d
}

// Time in jail (docs/adr/0019-crime-engine.md): a student in jail cannot
// attend, so the course stands still from the jailing until the release. The
// crime engine calls pauseStudies when it jails a player and resumeStudies
// when the sentence ends — bail, a served term, or a lapsed sentence closed
// by a new one — in the same transaction as the sentence's own change, so the
// two cannot disagree.

// pauseStudies stops the player's course, if one is running, at at.
func pauseStudies(ctx context.Context, tx application.Tx, playerID string, at time.Time) error {
	_, err := tx.Education().Pause(ctx, playerID, at)
	return err
}

// resumeStudies restarts the player's paused course as of at, the moment the
// jail ended: the course moves on by the time it stood still and a new
// completion is scheduled for its new end. The completion scheduled before
// the jail stays on the schedule and does nothing when it fires: the row no
// longer names it (EducationHandler.Complete). Exactly once: the course row
// is locked (Active), and Resume changes only a course still paused.
func resumeStudies(ctx context.Context, tx application.Tx, ids IDGenerator, playerID string, at time.Time) error {
	e, err := activeEnrollment(ctx, tx, playerID)
	if err != nil || e == nil || e.PausedAt == nil {
		return err
	}
	moved := domainEnrollment(*e).Resumed(at)
	actionID := ids.NewID()
	payload, err := json.Marshal(EducationActionPayload{
		EnrollmentID: e.ID, PlayerID: playerID, CourseCode: e.CourseCode,
	})
	if err != nil {
		return err
	}
	// The schedule row first: the enrolment points at it.
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID:            actionID,
		ActionType:    application.EducationActionType,
		ActorType:     "player",
		ActorID:       playerID,
		ReferenceType: "enrollments",
		ReferenceID:   e.ID,
		Payload:       payload,
		StartedAt:     at,
		FinishAt:      moved.CompletesAt,
	}); err != nil {
		return err
	}
	resumed, err := tx.Education().Resume(ctx, e.ID, moved.StartedAt, moved.CompletesAt, actionID)
	if err != nil {
		return err
	}
	if !resumed {
		// The row was locked and paused a moment ago in this transaction.
		return errors.Internal(stderrors.New("handlers: a paused course could not be resumed"))
	}
	return nil
}

// activeEnrollment returns the course in progress, or nil.
func activeEnrollment(ctx context.Context, tx application.Tx, playerID string) (*application.Enrollment, error) {
	e, err := tx.Education().Active(ctx, playerID)
	if isSentinel(err, application.ErrNoActiveEnrollment) {
		return nil, nil
	}
	return e, err
}

// List handles education.list: the course in progress, the certificates
// held, and the courses on offer here. It writes nothing.
func (h *EducationHandler) List(ctx context.Context, meta envelope.Metadata, req PageRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	page := parsePage(req.Page)
	var view screens.EducationView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		now := h.now()
		s, err := loadStanding(ctx, tx, p, now)
		if err != nil {
			return err
		}
		here, err := h.hereCode(ctx, s)
		if err != nil {
			return err
		}
		current, err := activeEnrollment(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if current != nil {
			d := domainEnrollment(*current)
			view.Current = &screens.CurrentCourseView{
				Course:    courseRef(snap, current.CourseCode),
				Percent:   d.Progress(now) / 100,
				Remaining: d.Remaining(now),
				EndsAt:    current.CompletesAt,
				Paused:    d.IsPaused(),
			}
		}
		for _, c := range s.certs {
			view.Certificates = append(view.Certificates, courseRef(snap, c.CourseCode))
		}

		var lines []screens.CourseLine
		for _, def := range snap.Courses() {
			if !visible(def, s, here) {
				continue
			}
			course, ok := snap.Course(def.Code)
			if !ok {
				continue
			}
			lines = append(lines, screens.CourseLine{
				Course:   screens.CourseRef{Code: def.Code, Name: def.Name},
				Fee:      course.Cost.Minor(),
				Duration: h.scale.RealWait(course.Duration),
				MinLevel: course.MinLevel,
				Eligible: s.stats.Level >= course.MinLevel,
			})
		}
		start, end, pages := pageWindow(len(lines), page, h.pageSize)
		view.Courses = lines[start:end]
		view.Page, view.Pages = min(page, pages), pages
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.Education(h.screen(meta, lang), view), nil
}

// course finds a course on offer to this player here, or refuses.
func (h *EducationHandler) course(snap *content.Snapshot, code string, s standing, here string) (content.CourseDef, education.Course, error) {
	def, ok := snap.CourseDef(code)
	if !ok || !visible(def, s, here) {
		return content.CourseDef{}, education.Course{}, refuse(screens.RefusalCourseNotFound, nil)
	}
	course, ok := snap.Course(code)
	if !ok {
		return content.CourseDef{}, education.Course{}, refuse(screens.RefusalCourseNotFound, nil)
	}
	return def, course, nil
}

// viewable finds a course to show, wherever it is taught.
func (h *EducationHandler) viewable(snap *content.Snapshot, code string) (content.CourseDef, education.Course, error) {
	def, ok := snap.CourseDef(code)
	course, ok2 := snap.Course(code)
	if !ok || !ok2 {
		return content.CourseDef{}, education.Course{}, refuse(screens.RefusalCourseNotFound, nil)
	}
	return def, course, nil
}

// View handles education.view: one course, and the enrol button when the
// enrolment would be accepted. It writes nothing.
func (h *EducationHandler) View(ctx context.Context, meta envelope.Metadata, req CourseRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CourseDetailView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		s, err := loadStanding(ctx, tx, p, h.now())
		if err != nil {
			return err
		}
		here, err := h.hereCode(ctx, s)
		if err != nil {
			return err
		}
		// A course taught elsewhere, or one whose certificate the player
		// already holds, is still shown — with where it is taught and why
		// it cannot be joined here — rather than as "not offered".
		def, course, err := h.viewable(snap, req.Course)
		if err != nil {
			return err
		}
		current, err := activeEnrollment(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		seats, err := tx.Education().SeatsTaken(ctx, course.Code)
		if err != nil {
			return err
		}

		view = screens.CourseDetailView{
			Course:      screens.CourseRef{Code: def.Code, Name: def.Name},
			Institution: string(course.Institution),
			Fee:         course.Cost.Minor(),
			Duration:    h.scale.RealWait(course.Duration),
			Limited:     course.Capacity > 0,
			SeatsLeft:   max(course.Capacity-seats, 0),
			Certifies:   course.Certifies,
		}
		if def.City != "" {
			view.CityCode = def.City
			if city, err := h.cities.ByCode(ctx, def.City); err == nil {
				view.City = city.Name
			} else if !isSentinel(err, application.ErrCityNotFound) {
				return err
			}
		}
		for _, r := range course.SkillRewards {
			view.Skills = append(view.Skills, screens.SkillGain{Skill: string(r.Skill), XP: r.XP})
		}
		elsewhere := def.City != "" && (def.City != here || s.travelling)
		if elsewhere {
			req := screens.Requirement{Kind: screens.ReqCourseCity, CityCode: def.City}
			if city, err := h.cities.ByCode(ctx, def.City); err == nil {
				req.City = city.Name
			} else if !isSentinel(err, application.ErrCityNotFound) {
				return err
			}
			view.Requirements = append(view.Requirements, req)
		}
		if def.Certifies && s.holds(def.Code) {
			view.Requirements = append(view.Requirements, screens.Requirement{Kind: screens.ReqAlreadyCertified})
		}
		if course.MinLevel > 1 {
			view.Requirements = append(view.Requirements, screens.Requirement{
				Kind: screens.ReqLevel, Met: s.stats.Level >= course.MinLevel,
				Need: int64(course.MinLevel), Have: int64(s.stats.Level),
			})
		}
		for _, pre := range course.Prerequisites {
			ref := courseRef(snap, pre)
			view.Requirements = append(view.Requirements, screens.Requirement{
				Kind: screens.ReqCertificate, Met: s.holds(pre), CourseCode: ref.Code, CourseName: ref.Name,
			})
		}
		if current != nil {
			ref := courseRef(snap, current.CourseCode)
			view.Requirements = append(view.Requirements, screens.Requirement{
				Kind: screens.ReqAlreadyEnrolled, CourseCode: ref.Code, CourseName: ref.Name,
			})
		}
		if course.Capacity > 0 && seats >= course.Capacity {
			view.Requirements = append(view.Requirements, screens.Requirement{Kind: screens.ReqCourseFull})
		}
		view.CanEnrol = visible(def, s, here) && education.CanEnroll(course, s.applicant(here, current), seats) == nil
		if view.CanEnrol && course.Cost.Minor() > 0 {
			w, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
			if err != nil {
				return err
			}
			choice := paymentChoice(w.Plan(course.Cost, snap.CourseAccepts(course.Code)), w)
			view.Payment = &choice
		}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.CourseDetail(h.screen(meta, lang), view), nil
}

// Enroll handles education.enroll: the fee, the enrolment and its scheduled
// completion, in one transaction.
func (h *EducationHandler) Enroll(ctx context.Context, meta envelope.Metadata, req CourseRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	if def, ok := snap.CourseDef(req.Course); ok && def.Cost > 0 && !chosen {
		// A price and no way to pay it chosen: the course's price screen,
		// with a button for each way the player can pay.
		return h.View(ctx, meta, CourseRequest{Course: req.Course})
	}
	lang := meta.Language
	replayed := false
	var view screens.EnrolledView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}

		now := h.now()
		s, err := loadStanding(ctx, tx, p, now)
		if err != nil {
			return err
		}
		here, err := h.hereCode(ctx, s)
		if err != nil {
			return err
		}
		def, course, err := h.course(snap, req.Course, s, here)
		if err != nil {
			return err
		}
		current, err := activeEnrollment(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		seats, err := tx.Education().SeatsTaken(ctx, course.Code)
		if err != nil {
			return err
		}
		// A prisoner cannot start a course: it could not advance anyway.
		if err := RefuseJailed(ctx, tx, p.ID, now); err != nil {
			return err
		}
		// A course is joined where it is taught: at the city place of its
		// institution (the university quarter). Nowhere to go — a city
		// without the place, content without places — joins from anywhere.
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if err := needService(w, snap, place.Service(course.Institution), h.scale, now); err != nil {
			return err
		}
		enrolment, fee, err := education.Enroll(course, s.applicant(here, current), seats, now, h.scale)
		if err != nil {
			if missing, ok := shortfalls(snap, err, application.City{}); ok {
				for i := range missing {
					if missing[i].Kind == screens.ReqAlreadyEnrolled && current != nil {
						ref := courseRef(snap, current.CourseCode)
						missing[i].CourseCode, missing[i].CourseName = ref.Code, ref.Name
					}
				}
				return refuse(screens.RefusalCourseRequirements, missing)
			}
			return errors.Internal(err)
		}

		enrollmentID := h.ids.NewID()
		actionID := h.ids.NewID()
		if err := h.chargeFee(ctx, tx, snap, p.ID, course.Code, enrollmentID, fee, method, now); err != nil {
			return err
		}
		payload, err := json.Marshal(EducationActionPayload{
			EnrollmentID: enrollmentID, PlayerID: p.ID, CourseCode: course.Code,
		})
		if err != nil {
			return err
		}
		// The schedule row first: the enrolment points at it, and it is what
		// makes the course finish even if every process restarts meanwhile.
		if err := tx.GameActions().Schedule(ctx, application.GameAction{
			ID:            actionID,
			ActionType:    application.EducationActionType,
			ActorType:     "player",
			ActorID:       p.ID,
			ReferenceType: "enrollments",
			ReferenceID:   enrollmentID,
			Payload:       payload,
			StartedAt:     enrolment.StartedAt,
			FinishAt:      enrolment.CompletesAt,
		}); err != nil {
			return err
		}
		if err := tx.Education().Enroll(ctx, application.Enrollment{
			ID:           enrollmentID,
			PlayerID:     p.ID,
			CourseCode:   course.Code,
			GameActionID: actionID,
			Status:       application.EnrollmentInProgress,
			Fee:          fee.Minor(),
			StartedAt:    enrolment.StartedAt,
			CompletesAt:  enrolment.CompletesAt,
		}); err != nil {
			if isSentinel(err, application.ErrAlreadyEnrolled) {
				return refuse(screens.RefusalCourseRequirements, []screens.Requirement{{Kind: screens.ReqAlreadyEnrolled}})
			}
			return err
		}
		if err := appendEducationEvent(ctx, tx, meta, "enrolled", enrollmentID, map[string]any{
			"enrollment_id":   enrollmentID,
			"player_id":       p.ID,
			"course":          course.Code,
			"fee":             fee.Minor(),
			"completes_at":    enrolment.CompletesAt,
			"content_version": snap.Version(),
		}); err != nil {
			return err
		}
		view = screens.EnrolledView{
			Course:   screens.CourseRef{Code: def.Code, Name: def.Name},
			Duration: enrolment.CompletesAt.Sub(enrolment.StartedAt),
			EndsAt:   enrolment.CompletesAt,
			Fee:      fee.Minor(),
			Method:   string(method),
		}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	if replayed {
		return h.List(ctx, meta, PageRequest{})
	}
	return screens.Enrolled(h.screen(meta, lang), view), nil
}

// chargeFee takes a course fee from the purse the player chose — their cash
// or their bank card, whichever the course accepts (payments.yml, service
// tuition, narrowed by the course's own list). The institution is the game's
// (nobody owns it), so the fee leaves the economy into system_sink as a
// service fee, the same reason whichever method paid. A company-run course
// will pay the company's treasury here instead. A method that does not cover
// the fee is refused with both balances (screens.PaymentDeclined).
func (h *EducationHandler) chargeFee(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	playerID, courseCode, enrollmentID string, fee money.Amount, method payment.Method, now time.Time,
) error {
	if fee.IsZero() {
		return nil
	}
	w, err := application.OpenWallet(ctx, tx.Ledger(), playerID)
	if err != nil {
		return err
	}
	plan := w.Plan(fee, snap.CourseAccepts(courseCode))
	back := []string{screens.AddrCourseView, courseCode}
	if err := checkMethod(plan, method, w, "education.button.back_to_course", back...); err != nil {
		return err
	}
	_, err = w.Pay(ctx, tx.Ledger(), application.Charge{
		Method:        method,
		Accepted:      plan.Accepted,
		Reason:        application.ReasonServiceFee,
		ReferenceType: "enrollments",
		ReferenceID:   enrollmentID,
		To:            []application.LedgerEntry{{AccountID: application.SystemSinkAccountID, Amount: fee}},
		CreatedAt:     now,
	})
	if stderrors.Is(err, application.ErrPaymentDeclined) {
		// The balance moved between the read and the post; the ledger's
		// refusal is the one that counts.
		return declined(plan, w, "education.button.back_to_course", back...)
	}
	return err
}

// Complete finishes a course. It arrives from the SCHEDULER, like
// travel.arrive, and is idempotent the same three ways: the key is derived
// from the enrolment, a second delivery finds no course in progress, and the
// domain refuses to complete a completed enrolment.
func (h *EducationHandler) Complete(ctx context.Context, meta envelope.Metadata, req CompleteCourseRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	playerID, enrollmentID := req.ActorID, req.ReferenceID
	if playerID == "" || enrollmentID == "" {
		var inner EducationActionPayload
		if len(req.Payload) > 0 {
			if err := json.Unmarshal(req.Payload, &inner); err != nil {
				return nil, errors.InvalidInput("course completion payload is unreadable").WithCause(err)
			}
		}
		if playerID == "" {
			playerID = inner.PlayerID
		}
		if enrollmentID == "" {
			enrollmentID = inner.EnrollmentID
		}
	}
	if playerID == "" || enrollmentID == "" {
		return nil, errors.InvalidInput("course completion names no enrolment")
	}

	snap := h.content.Current()
	var (
		view     screens.CourseCompletedView
		done     bool
		language = meta.Language
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		// Keyed on the action as well as the enrolment: a course paused by
		// jail gets a new completion on release, and that one must not be
		// taken for a replay of the first.
		key := idempotency.Derive(playerID, meta.Command, enrollmentID+":"+req.ActionID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		e, err := activeEnrollment(ctx, tx, playerID)
		if err != nil || e == nil || e.ID != enrollmentID {
			// Already completed, or the row that came due is not the course
			// this player is on: nothing to do.
			return err
		}
		if req.ActionID != "" && e.GameActionID != req.ActionID {
			// A completion the course no longer waits for: jail moved its
			// end, and a later completion is on the schedule.
			return nil
		}
		def, ok := snap.CourseDef(e.CourseCode)
		course, ok2 := snap.Course(e.CourseCode)
		if !ok || !ok2 {
			return errors.Internal(stderrors.New("handlers: enrolment names a course the content does not have: " + e.CourseCode))
		}
		now := h.now()
		finished, rewards, err := education.Complete(course, domainEnrollment(*e), now)
		if err != nil {
			var early education.NotFinished
			if stderrors.As(err, &early) {
				// Due early by a clock step: a fault worth a retry, which the
				// broker's backoff turns into "later".
				return errors.Internal(err)
			}
			if stderrors.Is(err, education.ErrNotInProgress) || stderrors.Is(err, education.ErrPaused) {
				// Finished already, or standing still in jail: its release
				// schedules the completion that will finish it.
				return nil
			}
			return errors.Internal(err)
		}
		if err := tx.Education().Complete(ctx, e.ID, now); err != nil {
			return err
		}
		skills, err := tx.Skills().List(ctx, playerID)
		if err != nil {
			return err
		}
		awards := make([]skillAward, 0, len(rewards.SkillXP))
		for _, r := range rewards.SkillXP {
			awards = append(awards, skillAward{Skill: r.Skill, XP: r.XP})
		}
		gains, err := awardSkillXP(ctx, tx, playerID, skills, awards, now)
		if err != nil {
			return err
		}
		certified := false
		if rewards.Certification != "" {
			if certified, err = tx.Education().Certify(ctx, playerID, rewards.Certification, e.ID, now); err != nil {
				return err
			}
		}
		skillXP := make([]CourseSkillGain, 0, len(gains))
		for _, g := range gains {
			skillXP = append(skillXP, CourseSkillGain{Skill: g.Skill, XP: g.XP, Level: g.Level})
		}
		if err := appendEducationEvent(ctx, tx, meta, "completed", e.ID, map[string]any{
			"enrollment_id":   e.ID,
			"player_id":       playerID,
			"course":          e.CourseCode,
			"course_name":     def.Name,
			"certified":       certified,
			"skills":          skillXP,
			"status":          string(finished.Status),
			"content_version": snap.Version(),
		}); err != nil {
			return err
		}
		if p, err := tx.Players().GetByID(ctx, playerID); err == nil {
			language = RenderLanguage(meta, p)
		} else if !isSentinel(err, application.ErrPlayerNotFound) {
			return err
		}
		done = true
		view = screens.CourseCompletedView{
			Course:    screens.CourseRef{Code: def.Code, Name: def.Name},
			Certified: certified,
			Skills:    gains,
		}
		return nil
	})
	if err != nil || !done {
		return nil, err
	}
	return screens.CourseCompleted(screens.Context{Msgs: h.msgs, Lang: language}, view), nil
}

// CourseSkillGain is one skill a finished course trained, as the
// education.completed event carries it: the XP added and, when the skill
// levelled up, the level reached (else 0).
type CourseSkillGain struct {
	Skill string `json:"skill"`
	XP    int64  `json:"xp"`
	Level int    `json:"level,omitempty"`
}

// appendEducationEvent writes a study event to the outbox in the command's
// transaction.
func appendEducationEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("education."+name, "enrollment", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID:  ev.ID,
		Subject:  subjects.Event("education", name),
		Metadata: meta,
		Payload:  ev.Payload,
	})
}

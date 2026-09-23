package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/job"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// JobRequest names a career, for job.view and job.apply. The payload key is
// "role", the name internal/gateway/routing has always given job.apply's
// argument; its value is a career CODE from jobs.yml.
type JobRequest struct {
	Role string `json:"role"`
}

// QuitRequest is job.quit's payload: empty asks for confirmation, and
// screens.QuitConfirmation confirms.
type QuitRequest struct {
	Confirm string `json:"confirm,omitempty"`
}

// JobsHandler serves work: the player's job, the openings in their city,
// applying, starting a shift and settling it when its time is up, promotion
// and leaving.
//
// # Who pays
//
// Every employer today is a city's base (NPC) employer. It pays each shift
// when it ends, from system_source under ReasonBaseEmployerSalary — the
// faucet ADR 0009 lists for exactly this. When player companies arrive, a
// job at a company is paid from that company's treasury instead, as a
// transfer, in payWage; nothing else here changes.
//
// # What the city decides
//
// The minimum wage, the income tax and the fatigue limits are the city's
// policy, read only through application.PolicyReader (ADR 0015). Tax is
// withheld under ReasonIncomeTax into the treasury of the city the player
// lives in.
type JobsHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	// scale is the game clock: a tier's shift duration, the time-in-tier bar
	// and the fatigue window are game time, and the player waits them
	// through it (config game.time_scale).
	scale gametime.Scale

	pageSize       int
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewJobsHandler wires the handler. idempotencyTTL is rejected at zero for
// the reason NewTravelHandler gives, and a game clock outside
// 1..gametime.MaxScale is refused for the same reason: it is a wiring
// mistake that looks like a design decision once it is live.
func NewJobsHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	source ContentSource,
	cities application.CityRepository,
	policy application.PolicyReader,
	scale gametime.Scale,
	pageSize int,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *JobsHandler {
	if source == nil || cities == nil || policy == nil {
		panic("handlers: NewJobsHandler requires content, cities and a policy reader")
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewJobsHandler requires a positive idempotency ttl")
	}
	if scale.Validate() != nil {
		panic("handlers: NewJobsHandler requires a game clock within 1..gametime.MaxScale")
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
	return &JobsHandler{
		uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy, scale: scale,
		pageSize: pageSize, idempotencyTTL: idempotencyTTL, now: now,
	}
}

func (h *JobsHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta)}
}

// finish turns the outcome of a command into what the player sees: a refusal
// becomes its screen, anything else is returned as it is.
func (h *JobsHandler) finish(meta envelope.Metadata, lang string, resp *presenter.Response, err error) (*presenter.Response, error) {
	if err != nil {
		if r, ok := asRefusal(err); ok {
			return screens.Refusal(h.screen(meta, lang), r.view), nil
		}
		return nil, err
	}
	return resp, nil
}

func validatePlayerMeta(meta envelope.Metadata) error {
	if err := meta.Validate(); err != nil {
		return errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return errors.InvalidInput("request carries no telegram user")
	}
	return nil
}

// Status handles job.status: the player's job, or where to find one. It
// writes nothing.
func (h *JobsHandler) Status(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.JobStatusView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		view, err = h.statusView(ctx, tx, snap, p)
		return err
	})
	resp, err := h.finish(meta, lang, nil, err)
	if err != nil || resp != nil {
		return resp, err
	}
	return screens.JobStatus(h.screen(meta, lang), view), nil
}

// statusView builds the job screen inside a transaction.
func (h *JobsHandler) statusView(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player) (screens.JobStatusView, error) {
	emp, err := tx.Employment().Current(ctx, p.ID)
	if isSentinel(err, application.ErrNotEmployed) {
		return screens.JobStatusView{}, nil
	}
	if err != nil {
		return screens.JobStatusView{}, err
	}
	def, career, err := careerOf(snap, emp.CareerCode)
	if err != nil {
		return screens.JobStatusView{}, err
	}
	city, err := h.cities.ByID(ctx, emp.CityID)
	if err != nil {
		return screens.JobStatusView{}, err
	}
	pol, err := readLabourPolicy(ctx, h.policy, *city)
	if err != nil {
		return screens.JobStatusView{}, err
	}
	now := h.now()
	s, err := loadStanding(ctx, tx, p, now)
	if err != nil {
		return screens.JobStatusView{}, err
	}
	shift, err := activeShift(ctx, tx, p.ID)
	if err != nil {
		return screens.JobStatusView{}, err
	}
	tier := career.Tiers[emp.Tier]
	view := screens.JobStatusView{
		Employed:     true,
		Job:          jobRef(def, emp.Tier),
		CityCode:     city.Code,
		City:         city.Name,
		Pay:          max(emp.Rate, pol.MinimumWage.Minor()),
		EnergyCost:   tier.EnergyCost,
		Energy:       s.stats.Energy,
		MaxEnergy:    s.stats.MaxEnergy,
		Performance:  emp.Performance,
		ShiftsInTier: emp.ShiftsInTier,
		TotalEarned:  emp.TotalEarned,
		AtWorkplace:  !s.travelling && s.here() == emp.CityID,
		TopTier:      emp.Tier == len(career.Tiers)-1,
		ShiftLength:  h.scale.RealWait(tier.ShiftDuration),
	}
	if shift != nil {
		view.Shift = &screens.ShiftProgress{
			Remaining: domainActivity(*shift).Remaining(now),
			EndsAt:    shift.EndsAt,
		}
	}
	if !view.TopTier {
		view.Next = jobRef(def, emp.Tier+1)
		ok, reason := job.Promotion(career, domainEmployment(*emp), s.candidate(emp.CityID), now, h.scale)
		switch {
		case ok:
			view.PromotionReady = true
		default:
			missing, known := shortfalls(snap, reason, *city)
			if !known {
				return screens.JobStatusView{}, errors.Internal(reason)
			}
			view.Missing = missing
		}
	}
	return view, nil
}

// List handles job.list: the openings in the player's city. It writes
// nothing.
func (h *JobsHandler) List(ctx context.Context, meta envelope.Metadata, req PageRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	page := parsePage(req.Page)
	var view screens.JobOpeningsView
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
		if s.travelling || s.here() == "" {
			view = screens.JobOpeningsView{Travelling: true}
			return nil
		}
		city, err := h.cities.ByID(ctx, s.here())
		if err != nil {
			return err
		}
		pol, err := readLabourPolicy(ctx, h.policy, *city)
		if err != nil {
			return err
		}
		view = screens.JobOpeningsView{CityCode: city.Code, City: city.Name}
		if emp, err := tx.Employment().Current(ctx, p.ID); err == nil {
			view.Employed = true
			if def, ok := snap.CareerDef(emp.CareerCode); ok {
				view.Current = jobRef(def, emp.Tier)
			}
		} else if !isSentinel(err, application.ErrNotEmployed) {
			return err
		}

		var all []screens.JobOpening
		for _, def := range snap.Careers() {
			if !def.OfferedIn(city.Code) {
				continue
			}
			career, ok := snap.Career(def.Code)
			if !ok {
				continue
			}
			all = append(all, screens.JobOpening{
				Job:      jobRef(def, 0),
				Pay:      entryRate(career, pol.Policy).Minor(),
				Eligible: job.Eligibility(career, 0, s.candidate(city.ID)) == nil,
			})
		}
		start, end, pages := pageWindow(len(all), page, h.pageSize)
		view.Openings = all[start:end]
		view.Page, view.Pages = min(page, pages), pages
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.JobOpenings(h.screen(meta, lang), view), nil
}

// View handles job.view: one opening, its requirements, and the apply
// button when every one is met. It writes nothing.
func (h *JobsHandler) View(ctx context.Context, meta envelope.Metadata, req JobRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.JobDetailView
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
		city, def, career, err := h.opening(ctx, snap, s, req.Role)
		if err != nil {
			return err
		}
		pol, err := readLabourPolicy(ctx, h.policy, *city)
		if err != nil {
			return err
		}
		_, empErr := tx.Employment().Current(ctx, p.ID)
		employed := empErr == nil
		if empErr != nil && !isSentinel(empErr, application.ErrNotEmployed) {
			return empErr
		}
		view = screens.JobDetailView{
			Job:          jobRef(def, 0),
			CityCode:     city.Code,
			City:         city.Name,
			Pay:          entryRate(career, pol.Policy).Minor(),
			EnergyCost:   career.Tiers[0].EnergyCost,
			Requirements: tierRequirements(snap, career.Tiers[0], s, *city),
			Employed:     employed,
			CanApply:     !employed && job.Eligibility(career, 0, s.candidate(city.ID)) == nil,
		}
		return nil
	})
	resp, err := h.finish(meta, lang, nil, err)
	if err != nil || resp != nil {
		return resp, err
	}
	return screens.JobDetail(h.screen(meta, lang), view), nil
}

// opening finds a career the base employer of the player's city hires into.
func (h *JobsHandler) opening(ctx context.Context, snap *content.Snapshot, s standing, code string) (*application.City, content.CareerDef, job.Career, error) {
	if s.travelling || s.here() == "" {
		return nil, content.CareerDef{}, job.Career{}, refuse(screens.RefusalJobNotOffered, nil)
	}
	city, err := h.cities.ByID(ctx, s.here())
	if err != nil {
		return nil, content.CareerDef{}, job.Career{}, err
	}
	def, ok := snap.CareerDef(code)
	if !ok || !def.OfferedIn(city.Code) {
		return nil, content.CareerDef{}, job.Career{}, refuse(screens.RefusalJobNotOffered, nil)
	}
	career, ok := snap.Career(code)
	if !ok {
		return nil, content.CareerDef{}, job.Career{}, refuse(screens.RefusalJobNotOffered, nil)
	}
	return city, def, career, nil
}

// entryRate is what the base employer offers for the first tier: its
// authored base salary, raised to the minimum wage when policy is above it.
// A player company will set its own offer instead.
func entryRate(career job.Career, policy job.Policy) money.Amount {
	base := career.Tiers[0].BaseSalary
	if base.Minor() < policy.MinimumWage.Minor() {
		return policy.MinimumWage
	}
	return base
}

// Apply handles job.apply: taking the first position of a career in the
// player's city.
func (h *JobsHandler) Apply(ctx context.Context, meta envelope.Metadata, req JobRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	replayed := false
	var view screens.JobHiredView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
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

		if _, err := tx.Employment().Current(ctx, p.ID); err == nil {
			return refuse(screens.RefusalAlreadyEmployed, nil)
		} else if !isSentinel(err, application.ErrNotEmployed) {
			return err
		}
		s, err := loadStanding(ctx, tx, p, h.now())
		if err != nil {
			return err
		}
		city, def, career, err := h.opening(ctx, snap, s, req.Role)
		if err != nil {
			return err
		}
		pol, err := readLabourPolicy(ctx, h.policy, *city)
		if err != nil {
			return err
		}
		rate := entryRate(career, pol.Policy)
		hired, err := job.Hire(career, 0, s.candidate(city.ID), rate, pol.Policy, h.now())
		if err != nil {
			if missing, ok := shortfalls(snap, err, *city); ok {
				return refuse(screens.RefusalJobRequirements, missing)
			}
			return errors.Internal(err)
		}

		empID := h.ids.NewID()
		now := h.now()
		if err := tx.Employment().Hire(ctx, application.Employment{
			ID:          empID,
			PlayerID:    p.ID,
			CareerCode:  hired.CareerCode,
			CityID:      city.ID,
			Tier:        hired.Tier,
			Rate:        hired.Rate.Minor(),
			Performance: hired.Performance,
			TierSince:   hired.TierSince,
			HiredAt:     now,
			UpdatedAt:   now,
		}); err != nil {
			if isSentinel(err, application.ErrAlreadyEmployed) {
				return refuse(screens.RefusalAlreadyEmployed, nil)
			}
			return err
		}
		if err := appendJobEvent(ctx, tx, meta, "hired", empID, map[string]any{
			"employment_id":   empID,
			"player_id":       p.ID,
			"career":          hired.CareerCode,
			"tier":            hired.Tier,
			"city_id":         city.ID,
			"rate":            hired.Rate.Minor(),
			"content_version": snap.Version(),
		}); err != nil {
			return err
		}
		view = screens.JobHiredView{Job: jobRef(def, 0), CityCode: city.Code, City: city.Name, Pay: hired.Rate.Minor()}
		return nil
	})
	resp, err := h.finish(meta, lang, nil, err)
	if err != nil || resp != nil {
		return resp, err
	}
	if replayed {
		return h.Status(ctx, meta)
	}
	return screens.JobHired(h.screen(meta, lang), view), nil
}

// Work handles job.work: STARTING one shift.
//
// A shift takes time (docs/adr/0018-game-clock.md). Starting it commits, in
// one unit of work: the energy it costs, the shift_sessions row that makes
// the player busy at work, its end on the schedule, and the event. Nothing is
// paid here; FinishShift settles the shift when the scheduler says its time
// is up. A shift the player cannot afford, one started while another runs,
// while travelling or away from the job is refused before anything is
// written.
//
// A double press cannot start two shifts. The job row is locked first, so a
// second start waits and then finds the first one working; and should two
// starts ever reach the insert together, the partial unique index on
// shift_sessions refuses the second.
func (h *JobsHandler) Work(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	replayed := false
	var view screens.ShiftStartedView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
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

		// Locks the job: a second start of this player waits here and then
		// sees the shift the first one began.
		emp, err := tx.Employment().Current(ctx, p.ID)
		if isSentinel(err, application.ErrNotEmployed) {
			return refuse(screens.RefusalNotEmployed, nil)
		}
		if err != nil {
			return err
		}
		now := h.now()
		current, err := activeShift(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if current != nil {
			return shiftRefusal(*current, now)
		}
		// No shift from a cell or in the middle of a crime (docs/adr/0019).
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		def, career, err := careerOf(snap, emp.CareerCode)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, emp.CityID)
		if err != nil {
			return err
		}
		// Locks the player's stats row, the one every departure also
		// locks: from here a journey and a shift cannot both begin.
		s, err := loadStanding(ctx, tx, p, now)
		if err != nil {
			return err
		}
		if s.travelling || s.here() != emp.CityID {
			r := refuse(screens.RefusalNotAtWorkplace, nil).(*refusal)
			r.view.CityCode, r.view.City = city.Code, city.Name
			return r
		}
		pol, err := readLabourPolicy(ctx, h.policy, *city)
		if err != nil {
			return err
		}

		tier := career.Tiers[emp.Tier]
		started, err := job.StartShift(job.Shift{
			Career:     career,
			Employment: domainEmployment(*emp),
			Stats:      domainStats(s.stats),
			Skills:     domainSkills(s.skills),
			Policy:     pol.Policy,
			Now:        now,
			Clock:      h.scale,
		})
		if err != nil {
			if stderrors.Is(err, player.ErrNotEnoughEnergy) {
				return energyRefusal(err, tier.EnergyCost, s.stats.Energy)
			}
			return errors.Internal(err)
		}

		stats := storedStats(s.stats, started.Stats)
		stats.UpdatedAt = s.stats.UpdatedAt
		if err := tx.Stats().Save(ctx, stats); err != nil {
			return err
		}

		sessionID := h.ids.NewID()
		actionID := h.ids.NewID()
		payload, err := json.Marshal(ShiftActionPayload{SessionID: sessionID, PlayerID: p.ID, EmploymentID: emp.ID})
		if err != nil {
			return err
		}
		a := started.Activity
		// The schedule row first: the session points at it, and it is what
		// ends the shift even if every process restarts meanwhile.
		if err := tx.GameActions().Schedule(ctx, application.GameAction{
			ID:            actionID,
			ActionType:    application.ShiftActionType,
			ActorType:     "player",
			ActorID:       p.ID,
			ReferenceType: "shift_sessions",
			ReferenceID:   sessionID,
			Payload:       payload,
			StartedAt:     a.StartedAt,
			FinishAt:      a.EndsAt,
		}); err != nil {
			return err
		}
		if err := tx.Employment().StartShift(ctx, application.ShiftSession{
			ID:           sessionID,
			EmploymentID: emp.ID,
			PlayerID:     p.ID,
			Tier:         a.Tier,
			GameActionID: actionID,
			Status:       application.ShiftWorking,
			FatigueBPS:   a.FatigueBPS,
			EnergyCost:   tier.EnergyCost,
			StartedAt:    a.StartedAt,
			EndsAt:       a.EndsAt,
		}); err != nil {
			if isSentinel(err, application.ErrShiftInProgress) {
				return refuse(screens.RefusalShiftInProgress, nil)
			}
			return err
		}
		if err := appendJobEvent(ctx, tx, meta, "shift_started", emp.ID, map[string]any{
			"employment_id":   emp.ID,
			"session_id":      sessionID,
			"player_id":       p.ID,
			"career":          emp.CareerCode,
			"tier":            a.Tier,
			"energy":          tier.EnergyCost,
			"fatigue_bps":     a.FatigueBPS,
			"ends_at":         a.EndsAt,
			"content_version": snap.Version(),
		}); err != nil {
			return err
		}
		view = screens.ShiftStartedView{
			Job:        jobRef(def, emp.Tier),
			Duration:   a.EndsAt.Sub(a.StartedAt),
			EndsAt:     a.EndsAt,
			FatigueBPS: a.FatigueBPS,
			Energy:     stats.Energy,
			MaxEnergy:  stats.MaxEnergy,
		}
		return nil
	})
	resp, err := h.finish(meta, lang, nil, err)
	if err != nil || resp != nil {
		return resp, err
	}
	if replayed {
		return h.Status(ctx, meta)
	}
	return screens.ShiftStarted(h.screen(meta, lang), view), nil
}

// ShiftActionPayload is the jsonb a started shift writes onto its
// game_actions row, and the shape read back when it comes due. It repeats
// the row's columns for the reason TravelActionPayload gives.
type ShiftActionPayload struct {
	SessionID    string `json:"session_id"`
	PlayerID     string `json:"player_id"`
	EmploymentID string `json:"employment_id"`
}

// FinishShiftRequest is job.finish_shift, published by the scheduler when a
// shift's time is up. It mirrors CompleteCourseRequest.
type FinishShiftRequest struct {
	ActionID      string          `json:"action_id"`
	ActorID       string          `json:"actor_id"`
	ReferenceType string          `json:"reference_type"`
	ReferenceID   string          `json:"reference_id"`
	Payload       json.RawMessage `json:"payload"`
}

// FinishShift settles a shift whose time is up. It arrives from the
// SCHEDULER, like travel.arrive and education.complete, and pays exactly once
// the same three ways: the idempotency key is derived from the session, a
// second delivery finds the session no longer working (the job row is locked
// first, so two deliveries run in turn), and the payroll row takes the
// session's id as its primary key.
//
// Everything a shift earns commits together or not at all: the XP, the skill
// XP, the job's performance, counters and fatigue history, the payroll row,
// the wage, the income tax and the job.shift_worked event the notifier turns
// into the player's notice.
func (h *JobsHandler) FinishShift(ctx context.Context, meta envelope.Metadata, req FinishShiftRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	playerID, sessionID := req.ActorID, req.ReferenceID
	if playerID == "" || sessionID == "" {
		var inner ShiftActionPayload
		if len(req.Payload) > 0 {
			if err := json.Unmarshal(req.Payload, &inner); err != nil {
				return nil, errors.InvalidInput("shift end payload is unreadable").WithCause(err)
			}
		}
		if playerID == "" {
			playerID = inner.PlayerID
		}
		if sessionID == "" {
			sessionID = inner.SessionID
		}
	}
	if playerID == "" || sessionID == "" {
		return nil, errors.InvalidInput("shift end names no shift")
	}

	snap := h.content.Current()
	var (
		view     screens.ShiftWorkedView
		done     bool
		language = meta.Language
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(playerID, meta.Command, sessionID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		// The job row first, as every other shift command takes it.
		emp, err := tx.Employment().Current(ctx, playerID)
		if err != nil && !isSentinel(err, application.ErrNotEmployed) {
			return err
		}
		session, err := activeShift(ctx, tx, playerID)
		if err != nil || session == nil || session.ID != sessionID {
			// Already settled, or the row that came due is not the shift
			// this player is working: nothing to do.
			return err
		}
		now := h.now()
		if emp == nil || emp.ID != session.EmploymentID {
			// The job ended under a running shift. Quitting refuses that, so
			// this is a job ended some other way; the shift ends unpaid.
			err := tx.Employment().EndShift(ctx, session.ID, application.ShiftAbandoned, now)
			if isSentinel(err, application.ErrNoShiftInProgress) {
				return nil // a concurrent delivery ended it first
			}
			return err
		}
		def, career, err := careerOf(snap, emp.CareerCode)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, emp.CityID)
		if err != nil {
			return err
		}
		p, err := tx.Players().GetByID(ctx, playerID)
		if err != nil {
			return err
		}
		language = RenderLanguage(meta, p)
		s, err := loadStanding(ctx, tx, p, now)
		if err != nil {
			return err
		}
		pol, err := readLabourPolicy(ctx, h.policy, *city)
		if err != nil {
			return err
		}

		res, err := job.FinishShift(job.Finish{
			Career:     career,
			Employment: domainEmployment(*emp),
			Stats:      domainStats(s.stats),
			Skills:     domainSkills(s.skills),
			Policy:     pol.Policy,
			Activity:   domainActivity(*session),
			Now:        now,
			Clock:      h.scale,
		})
		if err != nil {
			// Early by a clock step, or anything else: a fault worth a retry,
			// which the broker's backoff turns into "later".
			return errors.Internal(err)
		}
		if err := tx.Employment().EndShift(ctx, session.ID, application.ShiftCompleted, now); err != nil {
			return err
		}

		stats := storedStats(s.stats, res.Stats)
		stats.UpdatedAt = s.stats.UpdatedAt
		if err := tx.Stats().Save(ctx, stats); err != nil {
			return err
		}
		gains, err := awardSkillXP(ctx, tx, playerID, s.skills, skillXPFromJob(res.SkillXP), now)
		if err != nil {
			return err
		}
		// The payroll row and both ledger transactions carry the session's
		// id: one shift, one payment, whatever is delivered twice.
		pay, err := h.payWage(ctx, tx, playerID, s.residence, emp.CityID, session.ID, res.Pay, pol)
		if err != nil {
			return err
		}

		next := *emp
		next.Performance = res.Employment.Performance
		next.ShiftsInTier = res.Employment.ShiftsInTier
		next.RecentShifts = res.Employment.RecentShifts
		next.TotalShifts++
		next.TotalEarned += pay.Gross.Minor()
		next.UpdatedAt = now
		if err := tx.Employment().Save(ctx, next); err != nil {
			return err
		}
		if err := tx.Employment().RecordShift(ctx, application.WorkShift{
			ID:                  session.ID,
			EmploymentID:        emp.ID,
			PlayerID:            playerID,
			Tier:                session.Tier,
			WorkedAt:            now,
			Gross:               pay.Gross.Minor(),
			Tax:                 pay.Tax.Minor(),
			Net:                 pay.Net.Minor(),
			XP:                  res.XP,
			PerformanceDelta:    res.PerformanceDelta,
			FatigueBPS:          res.FatigueBPS,
			LedgerTransactionID: pay.transactionID,
		}); err != nil {
			return err
		}

		top := 0
		for _, up := range res.LevelUps {
			top = max(top, up.Level)
		}
		skills := make([]CourseSkillGain, 0, len(gains))
		for _, g := range gains {
			skills = append(skills, CourseSkillGain{Skill: g.Skill, XP: g.XP, Level: g.Level})
		}
		ref := jobRef(def, session.Tier)
		if err := appendJobEvent(ctx, tx, meta, "shift_worked", emp.ID, map[string]any{
			"employment_id":     emp.ID,
			"shift_id":          session.ID,
			"player_id":         playerID,
			"career":            emp.CareerCode,
			"career_name":       ref.CareerName,
			"rank":              ref.Rank,
			"title":             ref.Title,
			"tier":              session.Tier,
			"gross":             pay.Gross.Minor(),
			"tax":               pay.Tax.Minor(),
			"net":               pay.Net.Minor(),
			"xp":                res.XP,
			"skills":            skills,
			"performance":       res.Employment.Performance,
			"performance_delta": res.Employment.Performance - clampPercent(emp.Performance),
			"fatigue_bps":       res.FatigueBPS,
			"level":             top,
			"levels":            levelNumbers(res.LevelUps),
			"energy":            stats.Energy,
			"max_energy":        stats.MaxEnergy,
			"content_version":   snap.Version(),
		}); err != nil {
			return err
		}

		done = true
		view = screens.ShiftWorkedView{
			Gross:            pay.Gross.Minor(),
			Tax:              pay.Tax.Minor(),
			Net:              pay.Net.Minor(),
			XP:               res.XP,
			Skills:           gains,
			Performance:      res.Employment.Performance,
			PerformanceDelta: res.Employment.Performance - clampPercent(emp.Performance),
			FatigueBPS:       res.FatigueBPS,
			Level:            top,
			Energy:           stats.Energy,
			MaxEnergy:        stats.MaxEnergy,
		}
		return nil
	})
	if err != nil || !done {
		return nil, err
	}
	return screens.ShiftWorked(screens.Context{Msgs: h.msgs, Lang: language}, view), nil
}

// activeShift returns the shift the player is working, or nil.
func activeShift(ctx context.Context, tx application.Tx, playerID string) (*application.ShiftSession, error) {
	s, err := tx.Employment().ActiveShift(ctx, playerID)
	if isSentinel(err, application.ErrNoShiftInProgress) {
		return nil, nil
	}
	return s, err
}

// domainActivity lifts a stored shift into the domain's value.
func domainActivity(s application.ShiftSession) job.Activity {
	return job.Activity{Tier: s.Tier, StartedAt: s.StartedAt, EndsAt: s.EndsAt, FatigueBPS: s.FatigueBPS}
}

// shiftRefusal is "you are at work", with the time the shift has left.
func shiftRefusal(s application.ShiftSession, now time.Time) error {
	r := refuse(screens.RefusalShiftInProgress, nil).(*refusal)
	r.view.Wait = domainActivity(s).Remaining(now)
	r.view.EndsAt = s.EndsAt
	return r
}

// wage is one shift's pay after withholding.
type wage struct {
	Gross, Tax, Net money.Amount
	transactionID   string
}

// payWage pays one shift through the ledger and withholds income tax.
//
// The arithmetic is job.Payroll's, over a single line: the base employer
// settles each shift as it is worked, so the "period" is the shift itself and
// the whole of the pay is shift pay. Gross always equals net plus tax.
//
// Two transactions, each with its own reason, so the flows stay measurable:
//
//  1. the wage: system_source -> the player's cash, ReasonBaseEmployerSalary.
//     This is the NPC employer. A player company will pay from its
//     company_treasury account here instead, as a transfer; that is the one
//     line that changes when companies exist.
//  2. the tax: the player's cash -> the treasury of the city they live in,
//     ReasonIncomeTax, at that city's city.income_tax.
//
// A player with no residence pays no income tax: no city's policy applies to
// them. A zero wage (a policy of zero minimum wage and a tired shift can
// floor to nothing) moves no money.
func (h *JobsHandler) payWage(ctx context.Context, tx application.Tx, playerID, residence, jobCityID, shiftID string, pay money.Amount, jobPolicy labourPolicy) (wage, error) {
	taxBPS := 0
	if residence != "" {
		pol := jobPolicy
		if residence != jobCityID {
			city, err := h.cities.ByID(ctx, residence)
			if err != nil {
				return wage{}, err
			}
			if pol, err = readLabourPolicy(ctx, h.policy, *city); err != nil {
				return wage{}, err
			}
		}
		taxBPS = pol.IncomeTaxBPS
	}
	now := h.now()
	payments, err := job.Payroll([]job.Employee{{ID: playerID, ShiftPay: pay}},
		job.Period{Start: now, End: now.Add(time.Nanosecond)}, taxBPS)
	if err != nil {
		return wage{}, errors.Internal(err)
	}
	if len(payments) == 0 {
		return wage{}, nil
	}
	w := wage{Gross: payments[0].Gross, Tax: payments[0].Tax, Net: payments[0].Net}

	ledger := tx.Ledger()
	cash, err := ledger.AccountFor(ctx, application.AccountPlayerCash, playerID)
	if err != nil {
		return wage{}, err
	}
	negGross, err := w.Gross.Neg()
	if err != nil {
		return wage{}, errors.Internal(err)
	}
	if w.transactionID, err = ledger.Post(ctx, application.LedgerTransaction{
		Reason:        application.ReasonBaseEmployerSalary,
		ReferenceType: "work_shifts",
		ReferenceID:   shiftID,
		Entries: []application.LedgerEntry{
			{AccountID: application.SystemSourceAccountID, Amount: negGross},
			{AccountID: cash.ID, Amount: w.Gross},
		},
		CreatedAt: now,
	}); err != nil {
		return wage{}, err
	}
	if w.Tax.IsZero() {
		return w, nil
	}
	treasury, err := ledger.AccountFor(ctx, application.AccountCityTreasury, residence)
	if err != nil {
		return wage{}, err
	}
	negTax, err := w.Tax.Neg()
	if err != nil {
		return wage{}, errors.Internal(err)
	}
	if _, err := ledger.Post(ctx, application.LedgerTransaction{
		Reason:        application.ReasonIncomeTax,
		ReferenceType: "work_shifts",
		ReferenceID:   shiftID,
		Entries: []application.LedgerEntry{
			{AccountID: cash.ID, Amount: negTax},
			{AccountID: treasury.ID, Amount: w.Tax},
		},
		CreatedAt: now,
	}); err != nil {
		return wage{}, err
	}
	return w, nil
}

// Promote handles job.promote: stepping up one position once it is earned.
func (h *JobsHandler) Promote(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	replayed := false
	var view screens.JobPromotedView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
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
		emp, err := tx.Employment().Current(ctx, p.ID)
		if isSentinel(err, application.ErrNotEmployed) {
			return refuse(screens.RefusalNotEmployed, nil)
		}
		if err != nil {
			return err
		}
		def, career, err := careerOf(snap, emp.CareerCode)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, emp.CityID)
		if err != nil {
			return err
		}
		now := h.now()
		if shift, err := activeShift(ctx, tx, p.ID); err != nil {
			return err
		} else if shift != nil {
			// The shift running is paid at the tier it started in; a
			// promotion waits until it is over.
			return shiftRefusal(*shift, now)
		}
		s, err := loadStanding(ctx, tx, p, now)
		if err != nil {
			return err
		}
		promoted, err := job.Promote(career, domainEmployment(*emp), s.candidate(emp.CityID), now, h.scale)
		if err != nil {
			if missing, ok := shortfalls(snap, err, *city); ok {
				return refuse(screens.RefusalPromotion, missing)
			}
			return errors.Internal(err)
		}
		next := *emp
		next.Tier = promoted.Tier
		next.Rate = promoted.Rate.Minor()
		next.TierSince = promoted.TierSince
		next.ShiftsInTier = promoted.ShiftsInTier
		next.UpdatedAt = now
		if err := tx.Employment().Save(ctx, next); err != nil {
			return err
		}
		pol, err := readLabourPolicy(ctx, h.policy, *city)
		if err != nil {
			return err
		}
		if err := appendJobEvent(ctx, tx, meta, "promoted", emp.ID, map[string]any{
			"employment_id":   emp.ID,
			"player_id":       p.ID,
			"career":          emp.CareerCode,
			"from_tier":       emp.Tier,
			"tier":            promoted.Tier,
			"rate":            promoted.Rate.Minor(),
			"content_version": snap.Version(),
		}); err != nil {
			return err
		}
		view = screens.JobPromotedView{
			Job: jobRef(def, promoted.Tier),
			Pay: max(promoted.Rate.Minor(), pol.MinimumWage.Minor()),
		}
		return nil
	})
	resp, err := h.finish(meta, lang, nil, err)
	if err != nil || resp != nil {
		return resp, err
	}
	if replayed {
		return h.Status(ctx, meta)
	}
	return screens.JobPromoted(h.screen(meta, lang), view), nil
}

// Quit handles job.quit. Without confirmation it only asks; with it, the
// job ends.
func (h *JobsHandler) Quit(ctx context.Context, meta envelope.Metadata, req QuitRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	confirmed := req.Confirm == screens.QuitConfirmation
	replayed := false
	var ref screens.JobRef
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if confirmed {
			key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
			fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		emp, err := tx.Employment().Current(ctx, p.ID)
		if isSentinel(err, application.ErrNotEmployed) {
			return refuse(screens.RefusalNotEmployed, nil)
		}
		if err != nil {
			return err
		}
		if def, ok := snap.CareerDef(emp.CareerCode); ok {
			ref = jobRef(def, emp.Tier)
		}
		now := h.now()
		if shift, err := activeShift(ctx, tx, p.ID); err != nil {
			return err
		} else if shift != nil {
			// Walking out mid-shift would leave a shift nobody pays; the
			// player finishes it first.
			return shiftRefusal(*shift, now)
		}
		if !confirmed {
			return nil
		}
		if err := tx.Employment().End(ctx, emp.ID, application.EndResigned, now); err != nil {
			return err
		}
		return appendJobEvent(ctx, tx, meta, "quit", emp.ID, map[string]any{
			"employment_id":   emp.ID,
			"player_id":       p.ID,
			"career":          emp.CareerCode,
			"tier":            emp.Tier,
			"performance":     emp.Performance,
			"reason":          application.EndResigned,
			"content_version": snap.Version(),
		})
	})
	resp, err := h.finish(meta, lang, nil, err)
	if err != nil || resp != nil {
		return resp, err
	}
	switch {
	case replayed:
		return h.Status(ctx, meta)
	case !confirmed:
		return screens.JobQuitConfirm(h.screen(meta, lang), ref), nil
	}
	return screens.JobQuit(h.screen(meta, lang), ref), nil
}

// careerOf returns a career's definition and domain value. A stored job
// whose career is not in the content is a content load that should have been
// refused (ADR 0004 rule 7), so it is a fault, not a player's mistake.
func careerOf(snap *content.Snapshot, code string) (content.CareerDef, job.Career, error) {
	def, ok := snap.CareerDef(code)
	career, ok2 := snap.Career(code)
	if !ok || !ok2 {
		return content.CareerDef{}, job.Career{}, errors.Internal(stderrors.New("handlers: stored job names a career the content does not have: " + code))
	}
	return def, career, nil
}

// domainEmployment lifts a stored job into the domain's value.
func domainEmployment(e application.Employment) job.Employment {
	return job.Employment{
		CareerCode:   e.CareerCode,
		Tier:         e.Tier,
		Rate:         money.FromMinor(e.Rate),
		Performance:  e.Performance,
		TierSince:    e.TierSince,
		ShiftsInTier: e.ShiftsInTier,
		RecentShifts: append([]time.Time(nil), e.RecentShifts...),
	}
}

// clampPercent keeps a stored performance on the 0..100 scale, as the domain
// reads it, so the change shown is the change the domain made.
func clampPercent(p int) int { return min(max(p, 0), job.MaxPerformance) }

// skillXPFromJob converts a shift's skill awards to the common shape.
func skillXPFromJob(in []job.SkillXP) []skillAward {
	out := make([]skillAward, 0, len(in))
	for _, s := range in {
		out = append(out, skillAward{Skill: s.Skill, XP: s.XP})
	}
	return out
}

// skillAward is XP to add to one skill.
type skillAward struct {
	Skill player.SkillCode
	XP    int64
}

// awardSkillXP adds XP to skills through the domain's curve and stores the
// result, returning what each gained for the screen.
func awardSkillXP(ctx context.Context, tx application.Tx, playerID string, current []application.Skill, awards []skillAward, now time.Time) ([]screens.SkillGain, error) {
	var gains []screens.SkillGain
	for _, a := range awards {
		if a.XP <= 0 {
			continue
		}
		skill, err := player.NewSkill(a.Skill)
		if err != nil {
			return nil, errors.Internal(err)
		}
		for _, row := range current {
			if row.Code == string(a.Skill) {
				skill.Level, skill.XP = row.Level, row.XP
			}
		}
		next, ups := skill.AddSkillXP(a.XP)
		if err := tx.Skills().Upsert(ctx, application.Skill{
			PlayerID: playerID, Code: string(next.Code), Level: next.Level, XP: next.XP, UpdatedAt: now,
		}); err != nil {
			return nil, err
		}
		gain := screens.SkillGain{Skill: string(a.Skill), XP: a.XP}
		for _, up := range ups {
			gain.Level = max(gain.Level, up.Level)
		}
		gains = append(gains, gain)
	}
	return gains, nil
}

// appendJobEvent writes a job event to the outbox in the command's
// transaction.
func appendJobEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("job."+name, "employment", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID:  ev.ID,
		Subject:  subjects.Event("job", name),
		Metadata: meta,
		Payload:  ev.Payload,
	})
}

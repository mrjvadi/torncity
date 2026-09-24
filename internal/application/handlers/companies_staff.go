package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/job"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Hiring at a company: its openings, the applications to them, its staff —
// and, on the other side, a player looking at an opening and applying.
//
// An opening offers one career's entry position at a wage per shift, never
// under the city's minimum wage when posted (the shift pays at least the
// minimum wage in force anyway: job.FinishShift raises it). A company may
// not offer more positions than its kind's staff allows, counting its
// employees and the free positions of its other openings. An applicant must
// meet the position's requirements as they would at the base employer, and
// is hired either by the owner or the manager, or at once when the company
// hires automatically.

// Openings handles company.openings: a company's openings, and the careers
// it may advertise.
func (h *CompaniesHandler) Openings(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyOpeningsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		c, _, err := h.managed(ctx, tx, snap, p, req.code(), company.RightManageStaff)
		if err != nil {
			return err
		}
		view, err = h.openingsView(ctx, tx, snap, *c)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyOpenings(h.screen(meta, lang), view), nil
}

// taken is how many staff places a company has used: its employees and the
// free positions of its open openings, except the one named skip.
func taken(ctx context.Context, tx application.Tx, companyID, skip string) (int, []application.CompanyOpening, error) {
	staff, err := tx.Companies().Staff(ctx, companyID)
	if err != nil {
		return 0, nil, err
	}
	openings, err := tx.Companies().Openings(ctx, companyID)
	if err != nil {
		return 0, nil, err
	}
	n := len(staff)
	for _, o := range openings {
		if o.ID != skip {
			n += o.Free()
		}
	}
	return n, openings, nil
}

func (h *CompaniesHandler) openingsView(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company) (screens.CompanyOpeningsView, error) {
	def, ty, err := companyType(snap, c)
	if err != nil {
		return screens.CompanyOpeningsView{}, err
	}
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return screens.CompanyOpeningsView{}, err
	}
	minWage, err := h.lever(ctx, *city, leverMinimumWage)
	if err != nil {
		return screens.CompanyOpeningsView{}, err
	}
	used, openings, err := taken(ctx, tx, c.ID, "")
	if err != nil {
		return screens.CompanyOpeningsView{}, err
	}
	v := screens.CompanyOpeningsView{Ref: companyRef(snap, c), MinimumWage: minWage, Room: ty.MaxStaff - used,
		AtMax: len(openings) >= h.rules.MaxOpenings}
	for _, o := range openings {
		v.Openings = append(v.Openings, openingLine(snap, o))
	}
	for _, career := range def.Careers {
		if cd, ok := snap.CareerDef(career); ok {
			v.Careers = append(v.Careers, jobRef(cd, 0))
		}
	}
	return v, nil
}

// Post handles company.post: a new opening of one position for a career,
// at the wage typed.
func (h *CompaniesHandler) Post(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view     screens.CompanyOpeningsView
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		c, _, err := h.managed(ctx, tx, snap, p, req.code(), company.RightManageStaff)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			view, err = h.openingsView(ctx, tx, snap, *c)
			return err
		}
		raw := req.Wage
		if raw == "" {
			raw = req.Amount
		}
		wage, err := bank.ParseAmount(raw)
		if err != nil || wage.Minor() > h.rules.Limits.Max.Minor() {
			return refuseCompany(screens.CompanyRefusedInvalidAmount, c, snap)
		}
		_, ty, err := companyType(snap, *c)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, c.CityID)
		if err != nil {
			return err
		}
		minWage, err := h.lever(ctx, *city, leverMinimumWage)
		if err != nil {
			return err
		}
		used, openings, err := taken(ctx, tx, c.ID, "")
		if err != nil {
			return err
		}
		if len(openings) >= h.rules.MaxOpenings {
			r := refuseCompany(screens.CompanyRefusedOpeningsMax, c, snap)
			r.view.Max = int64(h.rules.MaxOpenings)
			return r
		}
		career := strings.ToLower(strings.TrimSpace(req.Career))
		if err := company.CheckOpening(ty, company.Opening{Career: career, Wage: wage, Positions: 1},
			money.FromMinor(minWage), used); err != nil {
			return openingRefusal(err, c, snap, minWage)
		}
		if _, ok := snap.CareerDef(career); !ok {
			return refuseCompany(screens.CompanyRefusedCareer, c, snap)
		}
		now := h.now()
		o, err := tx.Companies().PostOpening(ctx, application.CompanyOpening{
			ID: h.ids.NewID(), CompanyID: c.ID, CareerCode: career, Wage: wage.Minor(), Positions: 1, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := appendCompanyEvent(ctx, tx, meta, "opening_posted", c.ID, map[string]any{
			"company_id": c.ID, "opening_id": o.ID, "no": o.No, "career": career, "wage": wage.Minor(), "by": p.ID,
		}); err != nil {
			return err
		}
		view, err = h.openingsView(ctx, tx, snap, *c)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	_ = replayed
	return screens.CompanyOpenings(h.screen(meta, lang), view), nil
}

// openingRefusal is the refusal of an opening the rules turned down.
func openingRefusal(err error, c *application.Company, snap *content.Snapshot, minWage int64) error {
	switch {
	case stderrors.Is(err, company.ErrBelowMinimumWage):
		r := refuseCompany(screens.CompanyRefusedBelowMinimum, c, snap)
		r.view.Min = minWage
		return r
	case stderrors.Is(err, company.ErrCareerNotHired):
		return refuseCompany(screens.CompanyRefusedCareer, c, snap)
	case stderrors.Is(err, company.ErrStaffFull):
		return refuseCompany(screens.CompanyRefusedStaffFull, c, snap)
	case stderrors.Is(err, company.ErrInvalidAmount):
		return refuseCompany(screens.CompanyRefusedInvalidAmount, c, snap)
	}
	return errors.Internal(err)
}

// Slots handles company.slots: an opening's positions set to a number the
// button carries — one more, one fewer, or none, which closes it. The
// button names the number, not the step, so a second press of it changes
// nothing more. Positions already filled stay filled.
func (h *CompaniesHandler) Slots(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := strconv.ParseInt(strings.TrimSpace(req.No), 10, 64)
	if err != nil || no <= 0 {
		return nil, errors.InvalidInput("an opening is named by its number")
	}
	target, err := strconv.Atoi(strings.TrimSpace(req.Positions))
	if err != nil || target < 0 {
		return nil, errors.InvalidInput("an opening's positions are a whole number")
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyOpeningsView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		o, err := tx.Companies().Opening(ctx, no, false)
		if isSentinel(err, application.ErrOpeningNotFound) {
			return refuseCompany(screens.CompanyRefusedOpeningClosed, nil, snap)
		}
		if err != nil {
			return err
		}
		c, err := tx.Companies().ByID(ctx, o.CompanyID)
		if err != nil {
			return err
		}
		// The company first, then the opening: the order every company
		// command takes them in.
		if c, _, err = h.managed(ctx, tx, snap, p, c.Code, company.RightManageStaff); err != nil {
			return err
		}
		if o, err = tx.Companies().Opening(ctx, no, true); err != nil {
			return err
		}
		if o.Status != application.OpeningOpen {
			view, err = h.openingsView(ctx, tx, snap, *c)
			return err
		}
		now := h.now()
		switch {
		case target == o.Positions:
			// A press of a button already obeyed.
		case target == 0 || target < o.Filled:
			// None left to offer: the opening closes; whoever it hired
			// keeps their job.
			o.Positions = max(o.Filled, 1)
			o.Status = application.OpeningClosed
		case target > o.Positions:
			_, ty, err := companyType(snap, *c)
			if err != nil {
				return err
			}
			used, _, err := taken(ctx, tx, c.ID, o.ID)
			if err != nil {
				return err
			}
			// The opening's own filled places are the company's staff,
			// already in used.
			if used+target-o.Filled > ty.MaxStaff {
				return refuseCompany(screens.CompanyRefusedStaffFull, c, snap)
			}
			o.Positions = target
		default:
			o.Positions = target
		}
		o.UpdatedAt = now
		if err := tx.Companies().SaveOpening(ctx, *o); err != nil {
			return err
		}
		view, err = h.openingsView(ctx, tx, snap, *c)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyOpenings(h.screen(meta, lang), view), nil
}

// Staff handles company.staff: the company's employees and the
// applications waiting.
func (h *CompaniesHandler) Staff(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyStaffView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		c, _, err := h.managed(ctx, tx, snap, p, req.code(), company.RightManageStaff)
		if err != nil {
			return err
		}
		view, err = h.staffView(ctx, tx, snap, *c)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyStaff(h.screen(meta, lang), view), nil
}

func (h *CompaniesHandler) staffView(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company) (screens.CompanyStaffView, error) {
	v := screens.CompanyStaffView{Ref: companyRef(snap, c)}
	staff, err := tx.Companies().Staff(ctx, c.ID)
	if err != nil {
		return v, err
	}
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return v, err
	}
	if v.Citizens, err = h.citizensNow(ctx, tx, snap, c, city.Code); err != nil {
		return v, err
	}
	for _, e := range staff {
		who, err := playerNamed(ctx, tx, e.PlayerID)
		if err != nil {
			return v, err
		}
		v.Employees = append(v.Employees, screens.CompanyEmployeeLine{Player: who, Job: careerRef(snap, e.CareerCode, e.Tier),
			Wage: e.Rate, Shifts: e.TotalShifts, Working: e.Working})
	}
	apps, err := tx.Companies().Pending(ctx, c.ID)
	if err != nil {
		return v, err
	}
	for _, a := range apps {
		line, err := h.applicationLine(ctx, tx, snap, a)
		if err != nil {
			return v, err
		}
		v.Applications = append(v.Applications, line)
	}
	return v, nil
}

// careerRef names tier of a career for a screen, the code when the content
// no longer has it.
func careerRef(snap *content.Snapshot, career string, tier int) screens.JobRef {
	if def, ok := snap.CareerDef(career); ok {
		return jobRef(def, tier)
	}
	return screens.JobRef{CareerCode: career, CareerName: career}
}

func (h *CompaniesHandler) applicationLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, a application.CompanyApplication) (screens.CompanyApplicationLine, error) {
	line := screens.CompanyApplicationLine{No: a.No}
	who, err := tx.Players().GetByID(ctx, a.PlayerID)
	if err != nil && !isSentinel(err, application.ErrPlayerNotFound) {
		return line, err
	}
	line.Player = govPlayerOf(who)
	if who != nil {
		if stats, err := tx.Stats().Get(ctx, who.ID); err == nil {
			line.Level = stats.Level
		}
	}
	o, err := tx.Companies().OpeningByID(ctx, a.OpeningID)
	if err != nil {
		return line, err
	}
	line.Job = careerRef(snap, o.CareerCode, 0)
	return line, nil
}

// Decide handles company.decide: accepting or rejecting one application.
// Accepting hires the applicant, if they still qualify, have no job and a
// position is still free; they are told either way.
func (h *CompaniesHandler) Decide(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := strconv.ParseInt(strings.TrimSpace(req.No), 10, 64)
	if err != nil || no <= 0 {
		return nil, errors.InvalidInput("an application is named by its number")
	}
	accept := req.Verdict == screens.CompanyAccept
	if !accept && req.Verdict != screens.CompanyReject {
		return nil, errors.InvalidInput("an application is accepted or rejected")
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyStaffView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		a, err := tx.Companies().Application(ctx, no)
		if isSentinel(err, application.ErrApplicationNotFound) {
			return refuseCompany(screens.CompanyRefusedApplicationOld, nil, snap)
		}
		if err != nil {
			return err
		}
		c, err := tx.Companies().ByID(ctx, a.CompanyID)
		if err != nil {
			return err
		}
		if c, _, err = h.managed(ctx, tx, snap, p, c.Code, company.RightManageStaff); err != nil {
			return err
		}
		if a.Status != application.ApplicationPending {
			// Decided already: a second press of the same button.
			view, err = h.staffView(ctx, tx, snap, *c)
			return err
		}
		line, err := h.applicationLine(ctx, tx, snap, *a)
		if err != nil {
			return err
		}
		now := h.now()
		hired := false
		if accept {
			applicant, err := tx.Players().GetByID(ctx, a.PlayerID)
			if err != nil {
				return err
			}
			o, err := tx.Companies().Opening(ctx, mustNo(ctx, tx, a.OpeningID), true)
			if err != nil {
				return err
			}
			switch err := h.hire(ctx, tx, meta, snap, c, o, applicant, now); {
			case err == nil:
				hired = true
			case applicantUnfit(err):
				// They took another job or no longer qualify: the
				// application is turned down, and they are told.
			default:
				return err
			}
		}
		status := application.ApplicationRejected
		if hired {
			status = application.ApplicationAccepted
		}
		if _, err := tx.Companies().Decide(ctx, a.ID, status, p.ID, now); err != nil {
			return err
		}
		if !hired {
			o, err := tx.Companies().OpeningByID(ctx, a.OpeningID)
			if err != nil {
				return err
			}
			if err := h.employeeEvent(ctx, tx, meta, snap, *c, a.PlayerID, o.CareerCode, 0, o.Wage,
				screens.CompanyEmployeeRejected); err != nil {
				return err
			}
		}
		if view, err = h.staffView(ctx, tx, snap, *c); err != nil {
			return err
		}
		view.Decided, view.Hired = &line, hired
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyStaff(h.screen(meta, lang), view), nil
}

// applicantUnfit reports whether hiring failed on the applicant — a job
// already, or a requirement no longer met — rather than on the opening.
func applicantUnfit(err error) bool {
	if v, ok := asCompanyRefusal(err); ok {
		return v.Kind == screens.CompanyRefusedEmployed
	}
	_, ok := asRefusal(err)
	return ok
}

// mustNo is an opening's number, for the locking read by number; an opening
// that cannot be read reads as zero, which finds nothing.
func mustNo(ctx context.Context, tx application.Tx, openingID string) int64 {
	o, err := tx.Companies().OpeningByID(ctx, openingID)
	if err != nil {
		return 0
	}
	return o.No
}

// hire takes applicant on at company c through opening o (locked), if they
// qualify, have no job and a position is free. Their other applications
// are withdrawn and they are told.
func (h *CompaniesHandler) hire(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	c *application.Company, o *application.CompanyOpening, applicant *application.Player, now time.Time,
) error {
	if o.Status != application.OpeningOpen {
		return refuseCompany(screens.CompanyRefusedOpeningClosed, c, snap)
	}
	if o.Free() == 0 {
		return refuseCompany(screens.CompanyRefusedOpeningFull, c, snap)
	}
	if _, err := tx.Employment().Current(ctx, applicant.ID); err == nil {
		return refuseCompany(screens.CompanyRefusedEmployed, c, snap)
	} else if !isSentinel(err, application.ErrNotEmployed) {
		return err
	}
	career, ok := snap.Career(o.CareerCode)
	if !ok {
		return refuseCompany(screens.CompanyRefusedCareer, c, snap)
	}
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return err
	}
	pol, err := readLabourPolicy(ctx, h.policy, *city)
	if err != nil {
		return err
	}
	s, err := loadStanding(ctx, tx, applicant, now)
	if err != nil {
		return err
	}
	hired, err := job.Hire(career, 0, s.candidate(city.ID), money.FromMinor(o.Wage), pol.Policy, now)
	if err != nil {
		if missing, ok := shortfalls(snap, err, *city); ok {
			return refuse(screens.RefusalJobRequirements, missing)
		}
		if stderrors.Is(err, job.ErrBelowMinimumWage) {
			// The minimum wage rose above the opening's wage: it is hired at
			// the minimum instead, as every shift pays at least that.
			hired, err = job.Hire(career, 0, s.candidate(city.ID), pol.MinimumWage, pol.Policy, now)
		}
		if err != nil {
			return errors.Internal(err)
		}
	}
	empID := h.ids.NewID()
	if err := tx.Employment().Hire(ctx, application.Employment{
		ID: empID, PlayerID: applicant.ID, CareerCode: hired.CareerCode, CityID: city.ID, Tier: hired.Tier,
		Rate: hired.Rate.Minor(), Performance: hired.Performance, TierSince: hired.TierSince, HiredAt: now, UpdatedAt: now,
		CompanyID: c.ID, OpeningID: o.ID,
	}); err != nil {
		if isSentinel(err, application.ErrAlreadyEmployed) {
			return refuseCompany(screens.CompanyRefusedEmployed, c, snap)
		}
		return err
	}
	if _, err := tx.Companies().WithdrawPlayerApplications(ctx, applicant.ID, now); err != nil {
		return err
	}
	if err := appendJobEvent(ctx, tx, meta, "hired", empID, map[string]any{
		"employment_id": empID, "player_id": applicant.ID, "career": hired.CareerCode, "tier": hired.Tier,
		"city_id": city.ID, "rate": hired.Rate.Minor(), "company_id": c.ID, "content_version": snap.Version(),
	}); err != nil {
		return err
	}
	return h.employeeEvent(ctx, tx, meta, snap, *c, applicant.ID, hired.CareerCode, 0, hired.Rate.Minor(),
		screens.CompanyEmployeeHired)
}

// Fire handles company.fire: without confirmation it asks; with it, the
// employee's job ends and they are told. An employee on a shift finishes it
// first: its wage is promised.
func (h *CompaniesHandler) Fire(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	confirmed := req.Confirm == screens.CompanyConfirm
	var view screens.CompanyStaffView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if confirmed {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				c, _, err := h.managed(ctx, tx, snap, p, req.code(), company.RightManageStaff)
				if err != nil {
					return err
				}
				view, err = h.staffView(ctx, tx, snap, *c)
				return err
			}
		}
		// The employee's job first, then the company: the order a shift
		// takes them in, so a shift starting now and this firing wait for
		// each other instead of deadlocking.
		if found, err := h.byCode(ctx, tx, snap, req.code(), false); err == nil {
			if staff, err := tx.Companies().Staff(ctx, found.ID); err == nil {
				for _, e := range staff {
					if g, err := playerNamed(ctx, tx, e.PlayerID); err == nil && g.Code == playercode.Normalize(req.Player) {
						if _, err := tx.Employment().Current(ctx, e.PlayerID); err != nil && !isSentinel(err, application.ErrNotEmployed) {
							return err
						}
					}
				}
			}
		}
		c, _, err := h.managed(ctx, tx, snap, p, req.code(), company.RightManageStaff)
		if err != nil {
			return err
		}
		staff, err := tx.Companies().Staff(ctx, c.ID)
		if err != nil {
			return err
		}
		code := playercode.Normalize(req.Player)
		var target *application.CompanyEmployee
		var who screens.GovPlayer
		for i := range staff {
			g, err := playerNamed(ctx, tx, staff[i].PlayerID)
			if err != nil {
				return err
			}
			if g.Code != "" && g.Code == code {
				target, who = &staff[i], g
			}
		}
		if target == nil {
			if confirmed {
				// Fired already: a second press shows the staff as they are.
				view, err = h.staffView(ctx, tx, snap, *c)
				return err
			}
			return refuseCompany(screens.CompanyRefusedNotEmployee, c, snap)
		}
		if target.Working {
			return refuseCompany(screens.CompanyRefusedWorking, c, snap)
		}
		line := screens.CompanyEmployeeLine{Player: who, Job: careerRef(snap, target.CareerCode, target.Tier), Wage: target.Rate}
		if !confirmed {
			view = screens.CompanyStaffView{Ref: companyRef(snap, *c), Firing: &line}
			return nil
		}
		// The job row first, as every shift command takes it.
		emp, err := tx.Employment().Current(ctx, target.PlayerID)
		if err != nil {
			return err
		}
		if shift, err := activeShift(ctx, tx, target.PlayerID); err != nil {
			return err
		} else if shift != nil {
			return refuseCompany(screens.CompanyRefusedWorking, c, snap)
		}
		if err := tx.Employment().End(ctx, emp.ID, application.EndFired, h.now()); err != nil {
			return err
		}
		if err := h.employeeEvent(ctx, tx, meta, snap, *c, target.PlayerID, target.CareerCode, target.Tier, target.Rate,
			screens.CompanyEmployeeFired); err != nil {
			return err
		}
		view, err = h.staffView(ctx, tx, snap, *c)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyStaff(h.screen(meta, lang), view), nil
}

// Opening handles company.opening: one opening as a player looking for work
// sees it, with the apply button when they may.
func (h *CompaniesHandler) Opening(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := strconv.ParseInt(strings.TrimSpace(req.No), 10, 64)
	if err != nil || no <= 0 {
		return nil, errors.InvalidInput("an opening is named by its number")
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyOpeningView
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		view, err = h.openingView(ctx, tx, snap, p, no)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyOpening(h.screen(meta, lang), view), nil
}

func (h *CompaniesHandler) openingView(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, no int64) (screens.CompanyOpeningView, error) {
	o, err := tx.Companies().Opening(ctx, no, false)
	if isSentinel(err, application.ErrOpeningNotFound) {
		return screens.CompanyOpeningView{}, refuseCompany(screens.CompanyRefusedOpeningClosed, nil, snap)
	}
	if err != nil {
		return screens.CompanyOpeningView{}, err
	}
	c, err := tx.Companies().ByID(ctx, o.CompanyID)
	if err != nil {
		return screens.CompanyOpeningView{}, err
	}
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return screens.CompanyOpeningView{}, err
	}
	v := screens.CompanyOpeningView{No: o.No, Company: companyRef(snap, *c), Job: careerRef(snap, o.CareerCode, 0),
		CityCode: city.Code, City: city.Name, Wage: o.Wage, Free: o.Free(), AutoAccept: c.AutoAccept,
		Closed: o.Status != application.OpeningOpen || !c.Active()}
	if def, _, ok := snap.CompanyType(c.TypeCode); ok {
		v.Place = placeNamed(snap, def.Place)
	}
	career, ok := snap.Career(o.CareerCode)
	if !ok {
		v.Closed = true
		return v, nil
	}
	pol, err := readLabourPolicy(ctx, h.policy, *city)
	if err != nil {
		return v, err
	}
	v.Wage = max(o.Wage, pol.MinimumWage.Minor())
	tier := career.Tiers[0]
	v.EnergyCost, v.ShiftLength = tier.EnergyCost, h.scale.RealWait(tier.ShiftDuration)
	s, err := loadStanding(ctx, tx, p, h.now())
	if err != nil {
		return v, err
	}
	v.Requirements = tierRequirements(snap, tier, s, *city)
	if _, err := tx.Employment().Current(ctx, p.ID); err == nil {
		v.Employed = true
	} else if !isSentinel(err, application.ErrNotEmployed) {
		return v, err
	}
	pending, err := tx.Companies().Pending(ctx, c.ID)
	if err != nil {
		return v, err
	}
	for _, a := range pending {
		if a.PlayerID == p.ID && a.OpeningID == o.ID {
			v.Applied = true
		}
	}
	v.CanApply = !v.Closed && !v.Employed && !v.Applied && v.Free > 0 &&
		job.Eligibility(career, 0, s.candidate(city.ID)) == nil
	return v, nil
}

// Apply handles company.apply: an application to an opening, or — at a
// company that hires automatically — the job itself.
func (h *CompaniesHandler) Apply(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, err := strconv.ParseInt(strings.TrimSpace(req.No), 10, 64)
	if err != nil || no <= 0 {
		return nil, errors.InvalidInput("an opening is named by its number")
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		applied  *screens.CompanyAppliedView
		hired    *screens.JobHiredView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		// The job row first, as every job command takes it.
		if _, err := tx.Employment().Current(ctx, p.ID); err == nil {
			return refuseCompany(screens.CompanyRefusedEmployed, nil, snap)
		} else if !isSentinel(err, application.ErrNotEmployed) {
			return err
		}
		o, err := tx.Companies().Opening(ctx, no, false)
		if isSentinel(err, application.ErrOpeningNotFound) {
			return refuseCompany(screens.CompanyRefusedOpeningClosed, nil, snap)
		}
		if err != nil {
			return err
		}
		// The company, then the opening.
		c, err := tx.Companies().Lock(ctx, o.CompanyID)
		if err != nil {
			return err
		}
		if o, err = tx.Companies().Opening(ctx, no, true); err != nil {
			return err
		}
		if !c.Active() || o.Status != application.OpeningOpen {
			return refuseCompany(screens.CompanyRefusedOpeningClosed, c, snap)
		}
		if o.Free() == 0 {
			return refuseCompany(screens.CompanyRefusedOpeningFull, c, snap)
		}
		city, err := h.cities.ByID(ctx, c.CityID)
		if err != nil {
			return err
		}
		now := h.now()
		s, err := loadStanding(ctx, tx, p, now)
		if err != nil {
			return err
		}
		if s.travelling || s.here() != c.CityID {
			r := refuseCompany(screens.CompanyRefusedAway, c, snap)
			r.view.CityCode, r.view.City = city.Code, city.Name
			return r
		}
		career, ok := snap.Career(o.CareerCode)
		if !ok {
			return refuseCompany(screens.CompanyRefusedOpeningClosed, c, snap)
		}
		if err := job.Eligibility(career, 0, s.candidate(city.ID)); err != nil {
			if missing, ok := shortfalls(snap, err, *city); ok {
				return refuse(screens.RefusalJobRequirements, missing)
			}
			return errors.Internal(err)
		}
		ref := careerRef(snap, o.CareerCode, 0)
		if c.AutoAccept {
			if err := h.hire(ctx, tx, meta, snap, c, o, p, now); err != nil {
				return err
			}
			pol, err := readLabourPolicy(ctx, h.policy, *city)
			if err != nil {
				return err
			}
			hired = &screens.JobHiredView{Job: ref, Employer: c.Name, CityCode: city.Code, City: city.Name,
				Pay: max(o.Wage, pol.MinimumWage.Minor())}
			return nil
		}
		a, err := tx.Companies().Apply(ctx, application.CompanyApplication{
			ID: h.ids.NewID(), OpeningID: o.ID, CompanyID: c.ID, PlayerID: p.ID, AppliedAt: now,
		})
		if isSentinel(err, application.ErrAlreadyApplied) {
			return refuseCompany(screens.CompanyRefusedApplied, c, snap)
		}
		if err != nil {
			return err
		}
		applied = &screens.CompanyAppliedView{Company: companyRef(snap, *c), Job: ref}
		for _, to := range []string{c.OwnerID, c.ManagerID} {
			if to == "" {
				continue
			}
			if err := appendCompanyEvent(ctx, tx, meta, "applied", c.ID, map[string]any{
				"company_id": c.ID, "code": c.Code, "name": c.Name, "no": a.No, "recipient_id": to,
				"player_id": p.ID, "player_name": shownName(p), "player_code": p.PublicCode, "level": s.stats.Level,
				"career": ref.CareerCode, "career_name": ref.CareerName, "rank": ref.Rank, "title": ref.Title,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	switch {
	case replayed:
		return h.Opening(ctx, meta, req)
	case hired != nil:
		return screens.JobHired(h.screen(meta, lang), *hired), nil
	}
	return screens.CompanyApplied(h.screen(meta, lang), *applied), nil
}

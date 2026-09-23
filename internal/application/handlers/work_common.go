package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/education"
	"github.com/mrjvadi/torncity/internal/domain/job"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds what the work and study handlers share: where the content
// comes from, how a player's standing becomes a domain Candidate or
// Applicant, how a city's labour policy is read, and how a domain refusal
// becomes the list of requirements a screen shows.

// ContentSource hands out the content snapshot in force. *content.Registry
// satisfies it. A handler takes the snapshot ONCE per request, so a reload
// landing mid-request never mixes two versions of a career.
type ContentSource interface {
	Current() *content.Snapshot
}

// The labour levers (configs/content/governance.yml). They are read only
// through application.PolicyReader: a mayor may have moved any of them.
const (
	leverMinimumWage         = "city.minimum_wage"
	leverIncomeTax           = "city.income_tax"
	leverShiftWindowHours    = "city.shift_window_hours"
	leverShiftsBeforeFatigue = "city.shifts_before_fatigue"
)

// labourPolicy is a city's labour law as the job rules take it, plus the
// income tax its treasury withholds.
type labourPolicy struct {
	job.Policy
	IncomeTaxBPS int
}

// readLabourPolicy reads a city's labour levers through the resolver.
func readLabourPolicy(ctx context.Context, policy application.PolicyReader, city application.City) (labourPolicy, error) {
	get := func(lever string) (int64, error) {
		v, err := policy.Get(ctx, city.JurisdictionID, lever)
		if err != nil {
			return 0, err
		}
		return v.Value, nil
	}
	wage, err := get(leverMinimumWage)
	if err != nil {
		return labourPolicy{}, err
	}
	tax, err := get(leverIncomeTax)
	if err != nil {
		return labourPolicy{}, err
	}
	window, err := get(leverShiftWindowHours)
	if err != nil {
		return labourPolicy{}, err
	}
	free, err := get(leverShiftsBeforeFatigue)
	if err != nil {
		return labourPolicy{}, err
	}
	return labourPolicy{
		Policy: job.Policy{
			MinimumWage: money.FromMinor(wage),
			// GAME hours: the domain maps the window through the game clock,
			// like the shifts it counts (docs/adr/0018-game-clock.md).
			FatigueWindow:     time.Duration(window) * time.Hour,
			FatigueFreeShifts: int(free),
		},
		IncomeTaxBPS: int(tax),
	}, nil
}

// domainSkills lifts stored skill rows into the domain's values.
func domainSkills(rows []application.Skill) []player.Skill {
	out := make([]player.Skill, 0, len(rows))
	for _, r := range rows {
		out = append(out, player.Skill{Code: player.SkillCode(r.Code), Level: r.Level, XP: r.XP})
	}
	return out
}

// certificateCodes lists the course codes of held certificates.
func certificateCodes(certs []application.Certification) []string {
	out := make([]string, 0, len(certs))
	for _, c := range certs {
		out = append(out, c.CourseCode)
	}
	return out
}

// standing is everything about a player the job and study rules read,
// gathered once inside the command's transaction.
type standing struct {
	player    *application.Player
	stats     application.Stats
	skills    []application.Skill
	certs     []application.Certification
	residence string
	// travelling is whether a journey is in progress.
	travelling bool
}

// loadStanding reads a player's standing, with energy regenerated to now.
func loadStanding(ctx context.Context, tx application.Tx, p *application.Player, now time.Time) (standing, error) {
	s := standing{player: p}
	row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
	if err != nil {
		return s, err
	}
	s.stats, _ = regenerateEnergy(*row, now)
	if s.skills, err = tx.Skills().List(ctx, p.ID); err != nil {
		return s, err
	}
	if s.certs, err = tx.Education().Certifications(ctx, p.ID); err != nil {
		return s, err
	}
	if s.residence, err = tx.Employment().ResidenceCityID(ctx, p.ID); err != nil {
		return s, err
	}
	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		s.travelling = true
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return s, err
	}
	return s, nil
}

// candidate is the standing as the job rules take it, for a job in cityID.
// A work permit does not exist yet (ADR 0014), so only a resident qualifies.
func (s standing) candidate(cityID string) job.Candidate {
	return job.Candidate{
		Stats:          domainStats(s.stats),
		Skills:         domainSkills(s.skills),
		Certifications: certificateCodes(s.certs),
		Residency:      job.Residency{Resident: s.residence != "" && s.residence == cityID},
	}
}

// here is the city the player stands in, or "" while nowhere.
func (s standing) here() string {
	if s.player.CityID == nil {
		return ""
	}
	return *s.player.CityID
}

// skillLevel returns the player's level in one skill.
func (s standing) skillLevel(code player.SkillCode) int {
	for _, r := range s.skills {
		if r.Code == string(code) {
			return r.Level
		}
	}
	return 0
}

// holds reports whether the player holds the certificate course issues.
func (s standing) holds(course string) bool {
	for _, c := range s.certs {
		if c.CourseCode == course {
			return true
		}
	}
	return false
}

// jobRef names tier i of a career for a screen.
func jobRef(def content.CareerDef, i int) screens.JobRef {
	ref := screens.JobRef{CareerCode: def.Code, CareerName: def.Name}
	if i >= 0 && i < len(def.Tiers) {
		ref.Rank = def.Tiers[i].Rank
		ref.Title = def.Tiers[i].Title
	}
	return ref
}

// courseRef names a course for a screen, by its code and authored name.
func courseRef(snap *content.Snapshot, code string) screens.CourseRef {
	ref := screens.CourseRef{Code: code, Name: code}
	if def, ok := snap.CourseDef(code); ok {
		ref.Name = def.Name
	}
	return ref
}

// tierRequirements lists every requirement of a tier, met or not.
func tierRequirements(snap *content.Snapshot, t job.Tier, s standing, city application.City) []screens.Requirement {
	var out []screens.Requirement
	resident := s.residence != "" && s.residence == city.ID
	out = append(out, screens.Requirement{
		Kind: screens.ReqResidence, Met: resident, CityCode: city.Code, City: city.Name,
	})
	if t.MinLevel > 1 {
		out = append(out, screens.Requirement{
			Kind: screens.ReqLevel, Met: s.stats.Level >= t.MinLevel,
			Need: int64(t.MinLevel), Have: int64(s.stats.Level),
		})
	}
	for _, r := range t.RequiredSkills {
		have := s.skillLevel(r.Skill)
		out = append(out, screens.Requirement{
			Kind: screens.ReqSkill, Met: have >= r.Level, Skill: string(r.Skill),
			Need: int64(r.Level), Have: int64(have),
		})
	}
	for _, code := range t.RequiredCertifications {
		ref := courseRef(snap, code)
		out = append(out, screens.Requirement{
			Kind: screens.ReqCertificate, Met: s.holds(code), CourseCode: ref.Code, CourseName: ref.Name,
		})
	}
	return out
}

// shortfalls turns a domain refusal — every unmet requirement joined — into
// the lines a screen shows. It reports false when err holds no requirement,
// so the caller can pass it on as the fault it is.
func shortfalls(snap *content.Snapshot, err error, jobCity application.City) ([]screens.Requirement, bool) {
	var errs []error
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	} else {
		errs = []error{err}
	}
	var out []screens.Requirement
	for _, e := range errs {
		var (
			level    job.LevelShortfall
			skill    job.SkillShortfall
			cert     job.CertificationShortfall
			perf     job.PerformanceShortfall
			wait     job.TimeShortfall
			shifts   job.ShiftShortfall
			eduLevel education.LevelShortfall
			prereq   education.PrerequisiteShortfall
			city     education.CityShortfall
		)
		switch {
		case stderrors.As(e, &level):
			out = append(out, screens.Requirement{Kind: screens.ReqLevel, Need: int64(level.Need), Have: int64(level.Have)})
		case stderrors.As(e, &eduLevel):
			out = append(out, screens.Requirement{Kind: screens.ReqLevel, Need: int64(eduLevel.Need), Have: int64(eduLevel.Have)})
		case stderrors.As(e, &skill):
			out = append(out, screens.Requirement{Kind: screens.ReqSkill, Skill: string(skill.Skill),
				Need: int64(skill.Need), Have: int64(skill.Have)})
		case stderrors.As(e, &cert):
			ref := courseRef(snap, cert.Code)
			out = append(out, screens.Requirement{Kind: screens.ReqCertificate, CourseCode: ref.Code, CourseName: ref.Name})
		case stderrors.As(e, &prereq):
			ref := courseRef(snap, prereq.Code)
			out = append(out, screens.Requirement{Kind: screens.ReqCertificate, CourseCode: ref.Code, CourseName: ref.Name})
		case stderrors.Is(e, job.ErrWorkPermitRequired):
			out = append(out, screens.Requirement{Kind: screens.ReqResidence, CityCode: jobCity.Code, City: jobCity.Name})
		case stderrors.As(e, &perf):
			out = append(out, screens.Requirement{Kind: screens.ReqPerformance, Need: int64(perf.Need), Have: int64(perf.Have)})
		case stderrors.As(e, &wait):
			out = append(out, screens.Requirement{Kind: screens.ReqTime, Wait: wait.Remaining})
		case stderrors.As(e, &shifts):
			out = append(out, screens.Requirement{Kind: screens.ReqShifts, Need: int64(shifts.Need), Have: int64(shifts.Have)})
		case stderrors.Is(e, job.ErrTopTier):
			out = append(out, screens.Requirement{Kind: screens.ReqTopTier})
		case stderrors.As(e, &city):
			out = append(out, screens.Requirement{Kind: screens.ReqCourseCity, CityCode: city.CityCode, City: city.CityCode})
		case stderrors.Is(e, education.ErrCourseFull):
			out = append(out, screens.Requirement{Kind: screens.ReqCourseFull})
		case stderrors.Is(e, education.ErrAlreadyCertified):
			out = append(out, screens.Requirement{Kind: screens.ReqAlreadyCertified})
		case stderrors.Is(e, education.ErrAlreadyEnrolled):
			out = append(out, screens.Requirement{Kind: screens.ReqAlreadyEnrolled})
		default:
			return nil, false
		}
	}
	return out, len(out) > 0
}

// refusal carries a refused request out of a unit of work, so the
// transaction rolls back — the idempotency key with it, and a retry after the
// player meets the requirement is not taken for a replay — and the handler
// then answers with the refusal screen instead of an error.
type refusal struct {
	view screens.RefusalView
}

func (r *refusal) Error() string { return "handlers: refused: " + r.view.Kind }

// asRefusal extracts a refusal from err.
func asRefusal(err error) (*refusal, bool) {
	var r *refusal
	if stderrors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// refuse builds a refusal.
func refuse(kind string, missing []screens.Requirement) error {
	return &refusal{view: screens.RefusalView{Kind: kind, Missing: missing}}
}

// energyRefusal classifies a shift the player has no energy for, carrying
// the two numbers the generic energy sentence quotes.
func energyRefusal(err error, needed, current int) error {
	return errors.InvalidInput("not enough energy to work").
		WithCause(err).
		WithDetail("needed", needed).
		WithDetail("current", current)
}

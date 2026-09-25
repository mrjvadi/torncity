package handlers

import (
	"context"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/recruit"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// A company's specialists: the list, and what the owner or the manager does
// to one — renew a completed contract, match the market, part ways.

// Specialists handles company.npcs.
func (h *RecruitHandler) Specialists(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.SpecialistsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		def, err := recruitment(snap)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, "", req.code())
		if err != nil {
			return err
		}
		view, err = h.specialistsView(ctx, tx, snap, def, *c)
		return err
	})
	if err != nil {
		return h.finish(meta, lang, err)
	}
	return screens.Specialists(h.screen(meta, lang), view), nil
}

func (h *RecruitHandler) specialistsView(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	def content.RecruitmentDef, c application.Company,
) (screens.SpecialistsView, error) {
	v := screens.SpecialistsView{Ref: companyRef(snap, c), Max: h.rules.MaxStaff}
	staff, err := tx.Recruitment().Staff(ctx, c.ID)
	if err != nil {
		return v, err
	}
	m := h.market(snap, def)
	for _, s := range staff {
		l, err := specialistLine(ctx, tx, m, c, s)
		if err != nil {
			return v, err
		}
		v.Lines = append(v.Lines, l)
	}
	return v, nil
}

// specialistLine is a specialist as the list shows them: where their pay
// stands against today's market.
func specialistLine(ctx context.Context, tx application.Tx, m jobMarket, c application.Company, s application.NPCStaff,
) (screens.SpecialistLine, error) {
	home, _ := m.snap.CityByID(s.HomeCityID)
	l := screens.SpecialistLine{No: s.No, NameSeed: s.NameSeed, Skill: s.Skill, Level: s.Level,
		Home: named(home.Code, home.Name), Salary: s.Salary, Housing: s.Housing, Served: s.Served, Term: s.TermPeriods,
		Expiring: s.Expiring, Shares: s.Shares}
	expected, err := expectedNow(ctx, tx, m, c, s)
	if err != nil {
		return l, err
	}
	rules := m.def.Staff.Rules()
	l.MarketDue = recruit.MarketDue(s.AcceptedBPS, expected)
	if recruit.RatioBPS(s.Due(), expected) < recruit.Scale(s.AcceptedBPS, rules.UnderpaidBPS) {
		l.Underpaid, l.UnderpaidLeft = true, max(rules.UnderpaidPeriods-s.UnderpaidRun, 1)
	}
	if s.UnpaidRun > 0 {
		l.UnpaidLeft = max(rules.UnpaidPeriods-s.UnpaidRun, 1)
	}
	return l, nil
}

// expectedNow is what the market expects today for a specialist working
// for c: their level, c's city, their home pool's scarcity.
func expectedNow(ctx context.Context, tx application.Tx, m jobMarket, c application.Company, s application.NPCStaff) (int64, error) {
	dest, ok := cityOfCompany(m.snap, c)
	home, okHome := m.snap.CityByID(s.HomeCityID)
	if !ok || !okHome {
		return max(s.Due(), 1), nil
	}
	return m.expectedFrom(ctx, tx, s.Skill, s.Level, home, dest)
}

// Specialist handles company.npc: renew, match the market, or — confirmed —
// part ways with one specialist.
func (h *RecruitHandler) Specialist(ctx context.Context, meta envelope.Metadata, req RecruitRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := req.number()
	if !ok {
		return nil, errNoNumber
	}
	act := strings.TrimSpace(req.Act)
	switch act {
	case screens.SpecialistRenew, screens.SpecialistRaise, screens.SpecialistDismiss:
	default:
		return nil, invalidRecruit("no such act on a specialist")
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.SpecialistsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		def, err := recruitment(snap)
		if err != nil {
			return err
		}
		s, err := tx.Recruitment().Specialist(ctx, no)
		if isSentinel(err, application.ErrSpecialistNotFound) {
			return refuseRecruit(screens.RecruitRefusedNotFound, nil, snap)
		}
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, s.CompanyID, "")
		if err != nil {
			return err
		}
		// Re-read under the company's lock: a settlement may have moved it.
		if s, err = tx.Recruitment().Specialist(ctx, no); err != nil {
			return err
		}
		back := []string{screens.AddrSpecialists, c.Code}
		if s.Status != application.StaffActive {
			r := refuseRecruit(screens.RecruitRefusedGone, c, snap)
			r.view.Back, r.view.NameSeed = back, s.NameSeed
			return r
		}
		m := h.market(snap, def)
		if act == screens.SpecialistDismiss && !req.confirmed() {
			if view, err = h.specialistsView(ctx, tx, snap, def, *c); err != nil {
				return err
			}
			line, err := specialistLine(ctx, tx, m, *c, *s)
			view.Confirm, view.ConfirmAct = &line, act
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		notice := ""
		if fresh {
			if notice, err = h.act(ctx, tx, m, *c, s, act); err != nil {
				if kind, ok := err.(recruitNotNow); ok {
					r := refuseRecruit(string(kind), c, snap)
					r.view.Back = back
					return r
				}
				return err
			}
		}
		if view, err = h.specialistsView(ctx, tx, snap, def, *c); err != nil {
			return err
		}
		view.Notice, view.NoticeSeed = notice, s.NameSeed
		return nil
	})
	if err != nil {
		return h.finish(meta, lang, err)
	}
	return screens.Specialists(h.screen(meta, lang), view), nil
}

// recruitNotNow is an act the specialist's standing does not allow.
type recruitNotNow string

func (e recruitNotNow) Error() string { return "handlers: recruitment act refused: " + string(e) }

// act does one act to a specialist and returns its notice.
func (h *RecruitHandler) act(ctx context.Context, tx application.Tx, m jobMarket, c application.Company,
	s *application.NPCStaff, act string,
) (string, error) {
	now := m.now
	switch act {
	case screens.SpecialistDismiss:
		s.Status, s.LeaveReason, s.LeftAt, s.UpdatedAt = application.StaffLeft, recruit.LeaveDismissed, &now, now
		return "dismissed", tx.Recruitment().SaveStaff(ctx, *s)
	case screens.SpecialistRenew:
		if !s.Expiring {
			return "", recruitNotNow(screens.RecruitRefusedNotNow)
		}
		expected, err := expectedNow(ctx, tx, m, c, *s)
		if err != nil {
			return "", err
		}
		st := recruit.Renew(standingOf(*s), s.TermPeriods, expected)
		s.Salary = max(st.Due-s.Housing, s.Salary)
		s.Served, s.Expiring, s.UnderpaidRun, s.UpdatedAt = 0, false, 0, now
		return "renewed", tx.Recruitment().SaveStaff(ctx, *s)
	}
	expected, err := expectedNow(ctx, tx, m, c, *s)
	if err != nil {
		return "", err
	}
	due := recruit.MarketDue(s.AcceptedBPS, expected)
	if due <= s.Due() {
		return "", recruitNotNow(screens.RecruitRefusedNotNow)
	}
	s.Salary, s.UnderpaidRun, s.UpdatedAt = due-s.Housing, 0, now
	return "raised", tx.Recruitment().SaveStaff(ctx, *s)
}

// standingOf is a specialist as the rules take them.
func standingOf(s application.NPCStaff) recruit.Standing {
	return recruit.Standing{Due: s.Due(), AcceptedBPS: s.AcceptedBPS, Served: s.Served, Term: s.TermPeriods,
		Expiring: s.Expiring, UnpaidRun: s.UnpaidRun, UnderpaidRun: s.UnderpaidRun}
}

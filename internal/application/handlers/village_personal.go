package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// The personal prerequisites of a post (docs/adr/0055, plan A7). A staff role of the content may ask its holder for a
// level, a skill of a level, a certificate or literacy (availability.yml staff_roles.personal). Those lists were only
// read by the content lint; this file makes the runtime read them where a player holds a post of a settlement:
//
//   - a shift at a workplace: the staff roles of the workplace's function. The player qualifies when one role asks
//     nothing of him that he lacks (a smithy has a smith who needs level 3 and a labourer who needs nothing);
//   - a scholar's post at a research building: the scholar role asks for literacy.
//
// GRACE. Nobody is locked out silently. From settlement.personal_rule_at the post refuses a player who lacks what it
// asks, and says what, how to get it; for settlement.personal_grace_days real days after it the player may still work,
// and the work screen and the research desk warn him what will be needed and from when.

// PersonalRules are the date the personal prerequisites began to be asked and the grace after it.
type PersonalRules struct {
	From      time.Time
	GraceDays int64
}

// GraceUntil is when the grace ends; the zero time when there is none.
func (r PersonalRules) GraceUntil() time.Time {
	if r.GraceDays <= 0 || r.From.IsZero() {
		return time.Time{}
	}
	return r.From.AddDate(0, 0, int(r.GraceDays))
}

// InGrace reports whether a player who lacks a prerequisite may still go on at now: before the rule date and during the
// grace after it. Without a rule (zero rules, the older wirings) nothing is asked at all.
func (r PersonalRules) InGrace(now time.Time) bool {
	u := r.GraceUntil()
	return !u.IsZero() && now.Before(u)
}

func (r PersonalRules) enabled() bool { return !r.From.IsZero() }

// WithPersonal gives the village handler the rules of the personal prerequisites.
func (h *VillageHandler) WithPersonal(r PersonalRules) *VillageHandler {
	h.personal = r
	return h
}

// personalStanding is what a player personally has.
type personalStanding struct {
	level  int
	skills map[string]int
	certs  map[string]bool
}

func (h *VillageHandler) standingOf(ctx context.Context, tx application.Tx, playerID string) (personalStanding, error) {
	st := personalStanding{skills: map[string]int{}, certs: map[string]bool{}}
	stats, err := tx.Stats().Get(ctx, playerID)
	switch {
	case err == nil:
		st.level = stats.Level
	case apperrors.CodeOf(err) == apperrors.CodeNotFound:
		st.level = 1 // a player who has no stats row yet is at the first level
	default:
		return st, err
	}
	skills, err := tx.Skills().List(ctx, playerID)
	if err != nil {
		return st, err
	}
	for _, sk := range skills {
		st.skills[sk.Code] = sk.Level
	}
	certs, err := tx.Education().Certifications(ctx, playerID)
	if err != nil {
		return st, err
	}
	for _, c := range certs {
		st.certs[c.CourseCode] = true
	}
	return st, nil
}

// literacyCourse is the course whose certificate is a person's literacy (availability.yml personal_sources).
const literacyCourse = "literacy_class"

// missing lists what a standing lacks of a staff role's personal list.
func (st personalStanding) missing(snap *content.Snapshot, needs []content.AvailabilityPersonal) []village.PersonalNeed {
	var out []village.PersonalNeed
	for _, n := range needs {
		switch n.Kind {
		case content.PersonalLevel:
			if st.level < n.Min {
				out = append(out, village.PersonalNeed{Kind: village.PersonalLevel, Have: int64(st.level), Need: int64(n.Min), How: village.HowTrain})
			}
		case content.PersonalSkill:
			if have := st.skills[n.Code]; have < max(n.Min, 1) {
				out = append(out, village.PersonalNeed{Kind: village.PersonalSkill, Item: named(n.Code, n.Code), Have: int64(have), Need: int64(max(n.Min, 1)), How: village.HowTrain})
			}
		case content.PersonalCertificate:
			if !st.certs[n.Code] {
				out = append(out, village.PersonalNeed{Kind: village.PersonalCertificate, Item: courseNamed(snap, n.Code), Need: 1, How: village.HowTrain})
			}
		case content.PersonalLiteracy:
			if !st.certs[literacyCourse] {
				out = append(out, village.PersonalNeed{Kind: village.PersonalLiteracy, Item: courseNamed(snap, literacyCourse), Need: 1, How: village.HowTrain})
			}
		}
		// rank: no content asks it today; a rank is not a personal prerequisite anything can fail yet
	}
	return out
}

// postMissing is what the player lacks to hold a post of the function: nil when some staff role asks nothing he lacks (or
// the function has no staff role with a personal list); otherwise the shortest list of the roles that ask.
func (st personalStanding) postMissing(snap *content.Snapshot, def content.BuildingFunctionDef, roles ...string) []village.PersonalNeed {
	var best []village.PersonalNeed
	asked := false
	for _, s := range def.Staff {
		if len(roles) > 0 && !containsString(roles, s.Role) {
			continue
		}
		r, ok := snap.StaffRole(s.Role)
		if !ok || len(r.Personal) == 0 {
			return nil // a post that asks nothing of anyone is open to everyone
		}
		miss := st.missing(snap, r.Personal)
		if len(miss) == 0 {
			return nil
		}
		if !asked || len(miss) < len(best) {
			best = miss
		}
		asked = true
	}
	if !asked {
		return nil
	}
	return best
}


// workMissing is what the player lacks for a shift at a building type (nil for none), from its function's staff roles.
func (h *VillageHandler) workMissing(snap *content.Snapshot, st personalStanding, buildingCode string) []village.PersonalNeed {
	if !h.personal.enabled() {
		return nil
	}
	fn, ok := snap.FunctionReplacing(buildingCode)
	if !ok {
		return nil
	}
	def, ok := snap.BuildingFunction(fn)
	if !ok {
		return nil
	}
	return st.postMissing(snap, def)
}

// personalRefusal is the refusal of a post the player is not qualified for, once the grace is over.
func personalRefusal(missing []village.PersonalNeed, back string) *villageRefusal {
	r := refuseVillage(village.VillagePersonal, back)
	r.personal = missing
	return r
}

func courseNamed(snap *content.Snapshot, code string) presentation.Named {
	ref := courseRef(snap, code)
	return named(ref.Code, ref.Name)
}

// WithPersonal gives the education handler the rules of the personal prerequisites (the student's literacy).
func (h *EducationHandler) WithPersonal(r PersonalRules) *EducationHandler {
	h.personal = r
	return h
}

// literacyGate is the student's literacy at a village class (owner, 2026-10-10): every class given in a founded
// settlement except the literacy class itself asks the student to read. It returns the requirement the student fails
// (nil when he reads, or nothing is asked), and whether it blocks now: from the rule date plus the grace on; before it
// the requirement only warns, with the date, and where to learn to read.
func (h *EducationHandler) literacyGate(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, code, cityID string, now time.Time,
) (*presentation.Requirement, bool, error) {
	if !h.personal.enabled() || code == literacyCourse || cityID == "" {
		return nil, false, nil
	}
	if _, err := tx.Settlements().ByID(ctx, cityID); err != nil {
		if isSentinel(err, application.ErrCityNotFound) {
			return nil, false, nil // a class of the neutral city
		}
		return nil, false, err
	}
	certs, err := tx.Education().Certifications(ctx, p.ID)
	if err != nil {
		return nil, false, err
	}
	for _, c := range certs {
		if c.CourseCode == literacyCourse {
			return nil, false, nil
		}
	}
	ref := courseRef(snap, literacyCourse)
	req := &presentation.Requirement{Kind: presentation.ReqLiteracy, CourseCode: ref.Code, CourseName: ref.Name}
	tag, tagged := snap.AvailabilityTag("course", literacyCourse)
	if near, err := h.nearest(ctx, tag, tagged); err != nil {
		return nil, false, err
	} else if near != nil {
		req.CityCode, req.City = near.Code, near.Name
		req.Trip = tripTo(ctx, tx, h.trips, p, near)
	}
	if h.personal.InGrace(now) {
		req.Until = h.personal.GraceUntil()
		return req, false, nil
	}
	return req, true, nil
}

// knowledgeOutputBPS is the sum of the effects with the target that the settlement's owned knowledge adds (basis points):
// the more it knows of a craft, the more a shift of that craft yields.
func (h *VillageHandler) knowledgeOutputBPS(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID, target string) (int64, error) {
	owned, err := tx.SettlementKnowledge().Owned(ctx, settlementID)
	if err != nil {
		return 0, err
	}
	var sum int64
	for _, o := range owned {
		d, ok := snap.SettlementKnowledgeDef(o.Code)
		if !ok {
			continue
		}
		for _, e := range d.Effects {
			if e.Target == target && e.Op == "add" {
				sum += e.Value
			}
		}
	}
	return sum, nil
}

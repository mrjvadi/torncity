package handlers

import (
	"context"
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/presentation"
)

// What a place teaches comes from what it has (CLAUDE.md section 2, ADR 0038 owner
// decision 6): a settlement teaches a course only when it has reached the course's stage,
// researched the knowledge it names and has the class building standing, with its teacher.
// The tags are availability.yml's `course` entries; nothing here is a rule of its own. A
// content city, the neutral one included, teaches every course: Support is where the
// foundational ones can always be had.

// WithHomeCity sets the code of the neutral city, the nearest place a course missing
// in a settlement can be had (settlement.home_city_code).
func (h *EducationHandler) WithHomeCity(code string) *EducationHandler {
	h.home = code
	return h
}

// courseHere is how the settlement the player stands in stands for teaching.
type courseHere struct {
	// all says every course is taught: a content city (or no settlement to judge by).
	all      bool
	tier     string
	stage    int
	owned    map[string]bool
	stands   func(content.AvailabilityBuilding) bool
	literacy int
	// settlement is the founded settlement (zero when all).
	settlement application.FoundedSettlement
	// hasClass says a building of the education role stands.
	hasClass bool
	// growth, caps and capsFound are the dual read of ADR 0044 phase G1: nil
	// growth (growth.capabilities off) leaves the tier the only answer.
	growth    *GrowthGate
	caps      wsettle.Capabilities
	capsFound bool
}

// courseHereOf reads the standing of the settlement of cityID.
func (h *EducationHandler) courseHereOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, cityID string) (courseHere, error) {
	c := courseHere{all: true}
	if cityID == "" {
		return c, nil
	}
	f, err := tx.Settlements().ByID(ctx, cityID)
	if stderrors.Is(err, application.ErrCityNotFound) {
		return c, nil // a content city
	}
	if err != nil {
		return c, err
	}
	owned, err := tx.SettlementKnowledge().Owned(ctx, cityID)
	if err != nil {
		return c, err
	}
	rows, err := tx.SettlementBuildings().List(ctx, cityID)
	if err != nil {
		return c, err
	}
	share, _, err := tx.SettlementKnowledge().Literacy(ctx, cityID)
	if err != nil {
		return c, err
	}
	c = courseHere{settlement: f, tier: tierStage(f.Tier), stage: content.StageRank(tierStage(f.Tier)),
		owned: map[string]bool{}, stands: standsIn(snap, rows), literacy: share}
	for _, o := range owned {
		c.owned[o.Code] = true
	}
	c.hasClass = c.stands(content.AvailabilityBuilding{Role: "education", Tier: 1})
	if gg := currentGrowth(); gg != nil {
		c.growth = gg
		c.caps = gg.FromRows(snap, application.SettlementStanding{Buildings: rows, Knowledge: owned})
		c.capsFound = true
	}
	return c, nil
}

// judge weighs a course's tag against the settlement: whether it is taught here, whether
// it is one the settlement could come to teach itself (its stage is reached), and what is
// still missing. An untagged course is taught everywhere.
func (c courseHere) judge(snap *content.Snapshot, tag content.AvailabilityDef, tagged bool) (taught, reachable bool, needs []presentation.CourseNeed) {
	if c.all || !tagged || tag.Stage == content.StageUndecided {
		return true, true, nil
	}
	taught, reachable, needs = c.judgeTier(snap, tag)
	if c.growth != nil {
		// ADR 0044 phase G1: the capability answer beside the tier's
		if d := c.growth.Decide("courses", c.settlement.CityID, snap, c.caps, c.capsFound, tag, taught); d != taught {
			taught = d
			if d {
				reachable, needs = true, nil
			}
		}
	}
	return taught, reachable, needs
}

// judgeTier is judge by the tier alone: the stage reached, the knowledge, the buildings and the
// class's teacher. A tag with no stage (ADR 0044 phase G0) has no stage to reach.
func (c courseHere) judgeTier(snap *content.Snapshot, tag content.AvailabilityDef) (taught, reachable bool, needs []presentation.CourseNeed) {
	if tag.Stage != "" {
		need := content.StageRank(tag.Stage)
		if need == 0 {
			// support: only the neutral city teaches it
			return false, false, []presentation.CourseNeed{{Kind: presentation.CourseNeedStage, Code: tag.Stage}}
		}
		if c.stage < need {
			return false, false, []presentation.CourseNeed{{Kind: presentation.CourseNeedStage, Code: tag.Stage}}
		}
	}
	if tag.Requires == nil {
		return true, true, nil
	}
	for _, k := range tag.Requires.Knowledge {
		if !c.owned[k] {
			needs = append(needs, presentation.CourseNeed{Kind: presentation.CourseNeedKnowledge, Code: k})
		}
	}
	listed := map[content.AvailabilityBuilding]bool{}
	for _, b := range tag.Requires.Buildings {
		if !c.stands(b) {
			needs = append(needs, presentation.CourseNeed{Kind: presentation.CourseNeedBuilding, Code: b.Code, Role: b.Role, Tier: b.Tier})
			listed[b] = true
		}
	}
	for _, code := range tag.Requires.Staff {
		// the teacher works in the class building: without it nobody teaches
		if r, ok := snap.StaffRole(code); ok && r.Building != nil {
			b := content.AvailabilityBuilding{Code: r.Building.Code, Role: r.Building.Role, Tier: r.Building.Tier}
			if !c.stands(b) && !listed[b] {
				needs = append(needs, presentation.CourseNeed{Kind: presentation.CourseNeedTeacher, Role: b.Role, Tier: b.Tier})
				listed[b] = true
			}
		}
	}
	return len(needs) == 0, true, needs
}

// nearest is the neutral city as the place a course missing here can be had, when its tag
// names it; nil when it does not or the city is not known.
func (h *EducationHandler) nearest(ctx context.Context, tag content.AvailabilityDef, tagged bool) (*presentation.Named, error) {
	if h.home == "" || !tagged {
		return nil, nil
	}
	has := len(tag.Elsewhere) == 0 && tag.Stage == content.StageSupport
	for _, e := range tag.Elsewhere {
		has = has || e.Where == content.StageSupport
	}
	if !has {
		return nil, nil
	}
	city, err := h.cities.ByCode(ctx, h.home)
	if stderrors.Is(err, application.ErrCityNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &presentation.Named{Code: city.Code, Name: city.Name}, nil
}

// taughtHere is the requirement a course shows when this place does not teach it, nil when it does.
func (h *EducationHandler) taughtHere(ctx context.Context, tx application.Tx, snap *content.Snapshot, code, cityID string,
) (*presentation.Requirement, error) {
	here, err := h.courseHereOf(ctx, tx, snap, cityID)
	if err != nil {
		return nil, err
	}
	tag, tagged := snap.AvailabilityTag("course", code)
	taught, _, _ := here.judge(snap, tag, tagged)
	if taught {
		return nil, nil
	}
	req := &presentation.Requirement{Kind: presentation.ReqCourseCity}
	if near, err := h.nearest(ctx, tag, tagged); err != nil {
		return nil, err
	} else if near != nil {
		req.CityCode, req.City = near.Code, near.Name
	}
	return req, nil
}

// educationEmpty names why nothing is on offer in a settlement and the class building that
// would change it. It reads the content only.
func educationEmpty(snap *content.Snapshot, here courseHere, offered int) (string, *presentation.Named) {
	if here.all || offered > 0 {
		return "", nil
	}
	if !here.hasClass {
		for _, code := range sortedBuildingCodes(snap) {
			d, _ := snap.SettlementBuildingDef(code)
			if !d.Private() && d.Def().Role == "education" && d.Def().Tier == 1 {
				return presentation.EducationNoClass, &presentation.Named{Code: d.Code, Name: d.Name}
			}
		}
		return presentation.EducationNoClass, nil
	}
	return presentation.EducationNothingTaught, nil
}

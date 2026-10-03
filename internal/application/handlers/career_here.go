package handlers

import (
	"context"
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/presentation"
)

// What a place employs follows from what it has (CLAUDE.md section 2): a
// career is hired into only where the settlement has the research and the
// buildings its availability tag names. A career the place does not offer is
// shown as «not here» with the nearest place that does and what this place
// lacks. The neutral city (a content city) employs every career, as before.

// WithHomeCity sets the code of the neutral city, the nearest place a career
// missing in a settlement can be had (settlement.home_city_code).
func (h *JobsHandler) WithHomeCity(code string) *JobsHandler {
	h.home = code
	return h
}

// needs lists what the settlement lacks of a tag's research and buildings.
func (s hubSettlement) needs(snap *content.Snapshot, tag content.AvailabilityDef) []presentation.CourseNeed {
	var out []presentation.CourseNeed
	if tag.Stage != "" && content.StageRank(tag.Stage) > s.stageRank && !s.content {
		// the label is legacy; it is named only when nothing more specific is
		if tag.Requires == nil {
			return []presentation.CourseNeed{{Kind: presentation.CourseNeedStage, Code: tag.Stage}}
		}
	}
	if tag.Requires == nil || s.content {
		return out
	}
	for _, k := range tag.Requires.Knowledge {
		if !s.owned[k] {
			out = append(out, presentation.CourseNeed{Kind: presentation.CourseNeedKnowledge, Code: k, Name: knowledgeNameOf(snap, k)})
		}
	}
	for _, b := range tag.Requires.Buildings {
		if !s.stands(b) {
			out = append(out, presentation.CourseNeed{Kind: presentation.CourseNeedBuilding, Code: b.Code, Role: b.Role, Tier: b.Tier, Name: buildingNameOf(snap, b.Code)})
		}
	}
	return out
}

// careerHere says whether the settlement the player stands in employs the
// career, and when it does not, what it lacks.
func (h *JobsHandler) careerHere(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City, code string,
) (bool, []presentation.CourseNeed, error) {
	tag, ok := snap.AvailabilityTag("career", code)
	if !ok || tag.Stage == content.StageUndecided {
		return true, nil, nil
	}
	here, err := judgeSettlementOf(ctx, tx, snap, city, h.home)
	if err != nil {
		return false, nil, err
	}
	if here.offered(snap, "career", code) {
		return true, nil, nil
	}
	return false, here.needs(snap, tag), nil
}

// careerNearest is the neutral city as the place a career missing here is
// had, when its tag names it.
func (h *JobsHandler) careerNearest(ctx context.Context, snap *content.Snapshot, code string) (*presentation.Named, error) {
	tag, ok := snap.AvailabilityTag("career", code)
	if !ok || h.home == "" {
		return nil, nil
	}
	has := false
	for _, e := range tag.Elsewhere {
		has = has || e.Where == content.StageSupport
	}
	if !has {
		return nil, nil
	}
	c, err := h.cities.ByCode(ctx, h.home)
	if stderrors.Is(err, application.ErrCityNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &presentation.Named{Code: c.Code, Name: c.Name}, nil
}

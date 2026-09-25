package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Staging (docs/adr/0021-production-economy.md section 14). The owner asked
// that production open step by step: a new company sees what it can do now
// and the very next thing it could unlock — never a phone on the first day.
// So the studio offers the goods the company may design now, and shows as
// locked only those one research (or one license) away, with the way in;
// the lab shows the technologies it may research now and those one step
// beyond; the floor shows what it can make and the components one step
// away. Everything further is hidden until the company comes closer. The
// rules are technology.Assess and item.DesignGaps; this file reads the
// company's standing for them.

// licenceRule is a stored licence as the rules read it.
func licenceRule(l application.DefenceLicence) military.Licence {
	out := military.Licence{Status: military.LicenceStatus(l.Status)}
	if l.EffectiveAt != nil {
		out.EffectiveAt = *l.EffectiveAt
	}
	return out
}

// companyLicence reads a company's latest licence, nil for none.
func companyLicence(ctx context.Context, tx application.Tx, companyID string, lock bool) (*application.DefenceLicence, error) {
	l, err := tx.Military().CompanyLicence(ctx, companyID, lock)
	if isSentinel(err, application.ErrDefenceLicenceNotFound) {
		return nil, nil
	}
	return l, err
}

// companySector is a company's sector as export control reads it
// (docs/adr/0022 section 2.14): a company of the licensed sector whose
// licence is not in force counts as civilian, and a civilian company whose
// contractor licence is in force counts as of the licensed sector.
func companySector(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company,
	def content.CompanyTypeDef, now time.Time,
) (string, error) {
	d, ok := snap.DefenceLicence()
	if !ok {
		return def.SectorCode(), nil
	}
	l, err := companyLicence(ctx, tx, c.ID, false)
	if err != nil {
		return "", err
	}
	inForce := l != nil && licenceRule(*l).InForce(now)
	switch {
	case def.SectorCode() == d.Sector && !inForce:
		return content.DefaultSector, nil
	case def.SectorCode() != d.Sector && inForce && l.Kind == application.LicenceContractor:
		return d.Sector, nil
	}
	return def.SectorCode(), nil
}

// buyer is the company as export control sees it, read once per floor.
func (f *floor) buyer(ctx context.Context, tx application.Tx, snap *content.Snapshot, now time.Time) (technology.Buyer, error) {
	if !f.sectorRead {
		s, err := companySector(ctx, tx, snap, *f.c, f.def, now)
		if err != nil {
			return technology.Buyer{}, err
		}
		f.sector, f.sectorRead = s, true
	}
	return technology.Buyer{Kind: application.OrgCompany, Sector: f.sector}, nil
}

// licensable answers whether a license for a technology is on offer to the
// company now: another active company sells one, and export control clears
// the company for it. Answers are cached for the unit of work.
func (h *ProductionHandler) licensable(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor,
	buyer technology.Buyer,
) (func(string) bool, *error) {
	cache := map[string]bool{}
	var firstErr error
	return func(code string) bool {
		if v, ok := cache[code]; ok {
			return v
		}
		def, ok := snap.Technology(code)
		if !ok || technology.Cleared(def.Tech().Control, buyer) != nil {
			cache[code] = false
			return false
		}
		offers, err := tx.Production().Offers(ctx, code)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		found := false
		for _, o := range offers {
			if o.Company.ID != f.c.ID && o.Tech.Mode == technology.Licensed.String() {
				found = true
			}
		}
		cache[code] = found
		return found
	}, &firstErr
}

// stage is where a company stands for staging: its research standing, the
// tree, and whether a license is on offer.
type stage struct {
	standing   technology.Standing
	tree       technology.Tree
	tiers      map[string]int
	licensable func(string) bool
	err        *error
}

// stageOf reads a company's stage.
func (h *ProductionHandler) stageOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor) (*stage, error) {
	running, err := tx.Production().RunningResearch(ctx, f.c.ID)
	if err != nil {
		return nil, err
	}
	st, err := h.researchStanding(ctx, tx, snap, f, running)
	if err != nil {
		return nil, err
	}
	lic, lerr := h.licensable(ctx, tx, snap, f, st.Buyer)
	return &stage{standing: st, tree: snap.TechTree(), tiers: snap.TechTiers(), licensable: lic, err: lerr}, nil
}

// assess sorts what a set of missing technologies is for the company.
func (s *stage) assess(missing []string) (technology.Reach, []technology.Step, error) {
	r, steps := technology.Assess(missing, s.tree, s.standing, s.licensable)
	return r, steps, *s.err
}

// techSteps names steps for a screen.
func techSteps(snap *content.Snapshot, steps []technology.Step) []screens.TechStep {
	out := make([]screens.TechStep, 0, len(steps))
	for _, st := range steps {
		td, _ := snap.Technology(st.Tech)
		out = append(out, screens.TechStep{Tech: named(td.Code, td.Name), Research: st.Research})
	}
	return out
}

// studioKinds sorts the goods a company's kind designs into those it may
// design now and those one step away; the rest are hidden. hidden reports
// whether any are.
func (h *ProductionHandler) studioKinds(snap *content.Snapshot, f *floor, s *stage,
) (ready []screens.Named, next []screens.StudioKind, hidden bool, err error) {
	comps := snap.Components()
	for _, d := range snap.DesignableItems(f.c.TypeCode) {
		a, ok := snap.Archetype(d.Archetype)
		if !ok {
			continue
		}
		missing, possible := item.DesignGaps(a, d.RequiresTechnology, comps, f.access)
		if !possible {
			continue
		}
		r, steps, err := s.assess(missing)
		if err != nil {
			return nil, nil, false, err
		}
		switch r {
		case technology.Ready:
			ready = append(ready, named(d.Code, d.Name))
		case technology.Next:
			next = append(next, screens.StudioKind{Item: named(d.Code, d.Name), Steps: techSteps(snap, steps)})
		default:
			hidden = true
		}
	}
	return ready, next, hidden, nil
}

// designGate refuses a new design of a good the company may not design
// yet, naming what it lacks.
func (h *ProductionHandler) designGate(snap *content.Snapshot, f *floor, def content.ItemDef, a item.Archetype) error {
	missing, possible := item.DesignGaps(a, def.RequiresTechnology, snap.Components(), f.access)
	if !possible {
		return refuseProduction(screens.ProductionRefusedWrongType, f.c, snap).back(screens.AddrStudio, f.c.Code)
	}
	if len(missing) == 0 {
		return nil
	}
	r := refuseProduction(screens.ProductionRefusedTechLocked, f.c, snap).back(screens.AddrStudio, f.c.Code)
	for _, t := range sortedTechs(snap, missing) {
		td, _ := snap.Technology(t)
		r.view.Techs = append(r.view.Techs, named(td.Code, td.Name))
	}
	return r
}

// labShown decides whether the lab shows a technology to the company: what
// it owns or may use, what it may research now, and what is one step away.
// A technology export control keeps from it is never shown.
func (s *stage) labShown(t technology.Tech, usable bool) bool {
	if usable || s.standing.Owned.Has(t.Code) {
		return true
	}
	if t.Control.Restricted && technology.Cleared(t.Control, s.standing.Buyer) != nil {
		return false
	}
	if s.standing.OneStep(t) {
		return true
	}
	if s.licensable(t.Code) {
		return true
	}
	missing := s.standing.Missing(t)
	if len(missing) == 0 {
		return false
	}
	r, _ := technology.Assess(missing, s.tree, s.standing, s.licensable)
	return r == technology.Next
}

// lockedComponents lists the components a company's kind makes that are one
// step away.
func (h *ProductionHandler) lockedComponents(snap *content.Snapshot, f *floor, s *stage) ([]screens.LockedTarget, error) {
	var out []screens.LockedTarget
	for _, comp := range snap.MadeBy(f.c.TypeCode) {
		var missing []string
		for _, t := range comp.RequiresTechnology {
			if !f.access.Allows(t) {
				missing = append(missing, t)
			}
		}
		if len(missing) == 0 {
			continue
		}
		r, steps, err := s.assess(missing)
		if err != nil {
			return nil, err
		}
		if r == technology.Next {
			out = append(out, screens.LockedTarget{Good: screens.Good{Component: true, Item: named(comp.Code, comp.Name)},
				Steps: techSteps(snap, steps)})
		}
	}
	return out, nil
}

// byTier orders technology codes basic first.
func byTier(snap *content.Snapshot, codes []string) []string {
	tiers := snap.TechTiers()
	out := sortedTechs(snap, codes)
	sort.SliceStable(out, func(i, j int) bool { return tiers[out[i]] < tiers[out[j]] })
	return out
}

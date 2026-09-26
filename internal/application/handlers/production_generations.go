package handlers

import (
	"context"
	"fmt"
	"maps"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Product generations (the owner's 2026 request, on top of ADR
// 0021-production-economy.md): a design's lineage of versions, an
// improvement project's small bounded gain, and retrofitting an existing
// unit with an upgrade kit — for ANY company's ANY product, civilian or
// (under the military stage's own export control and office gates)
// military. Nothing here is specific to one archetype: a phone gets a v2
// with a new chip exactly as a radar gets one with a new set.

// lineageOf mirrors item's private helper for a stored design: a version 1
// design is its own lineage's head.
func lineageOf(d application.Design) string {
	if d.LineageID != "" {
		return d.LineageID
	}
	return d.ID
}

// designStanding is the technology standing EffectsFor reads: what the
// company owns and what is published, nothing about a research decision.
func designStanding(f *floor) technology.Standing {
	return technology.Standing{Owned: f.ownSet, Published: f.pubSet}
}

// currentAttributes computes a design's attributes as they read TODAY: the
// archetype's aggregation over its components, the company's current
// technology-generation effects (technology.EffectsFor), and the design's
// own improvement projects (item.ImprovementEffects) — in that order, so a
// company that keeps researching and keeps improving sees both compound.
func currentAttributes(a item.Archetype, d item.Design, components item.Components, tree technology.Tree, st technology.Standing) (map[string]int64, error) {
	base, err := item.ComputeAttributes(a, d, components)
	if err != nil {
		return nil, err
	}
	effects := technology.EffectsFor(d, components, tree, st)
	effects = append(effects, item.ImprovementEffects(d)...)
	if len(effects) == 0 {
		return base, nil
	}
	return item.ApplyEffects(base, effects)
}

// designViewWithHistory is designView plus a revision's version, and the
// previous version's current attributes for the screen's ▲▼ deltas.
func (h *ProductionHandler) designViewWithHistory(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	c *application.Company, d application.Design,
) (screens.DesignView, error) {
	v, err := h.designView(ctx, tx, snap, c, d, "")
	if err != nil {
		return v, err
	}
	v.Version = d.Version
	if v.Version < 1 {
		v.Version = 1
	}
	if d.ParentID == "" {
		return v, nil
	}
	parent, err := tx.Production().DesignByID(ctx, d.ParentID)
	if err != nil {
		return v, nil //nolint:nilerr // a missing parent just skips the delta, never blocks the page
	}
	a, ok := snap.Archetype(parent.Archetype)
	if !ok {
		return v, nil
	}
	f, err := readFloor(ctx, tx, snap, c)
	if err != nil {
		return v, nil
	}
	prev, err := currentAttributes(a, domainDesign(*parent), snap.Components(), snap.TechTree(), designStanding(f))
	if err == nil {
		v.PrevAttributes = prev
	}
	return v, nil
}

// DesignRevise handles company.drevise: create the next version of a
// design's lineage — same archetype, same fills to start from (the studio's
// existing DesignFill/DesignQty/DesignName edit it like any draft), named
// with a version suffix so the product line keeps its identity. Manually
// revising always starts a fresh baseline: any improvement projects the
// version being revised gained are not carried into the new one (that is
// what an improvement project itself is for — see ImprovementStart).
func (h *ProductionHandler) DesignRevise(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.DesignView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}
		d, c, err := h.designOf(ctx, tx, snap, p, req.No, false)
		if err != nil {
			return err
		}
		if d.Status != application.DesignFinal {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrStudio, c.Code)
		}
		designs, err := tx.Production().Designs(ctx, c.ID)
		if err != nil {
			return err
		}
		if len(designs) >= h.rules.MaxDesigns {
			r := refuseProduction(screens.ProductionRefusedMaxDesigns, c, snap).back(screens.AddrStudio, c.Code)
			r.view.Max = h.rules.MaxDesigns
			return r
		}
		version := d.Version
		if version < 1 {
			version = 1
		}
		version++
		name := fmt.Sprintf("%s - نسخهٔ %d", d.Name, version)
		next := application.Design{ID: h.ids.NewID(), CompanyID: c.ID, Item: d.Item, Archetype: d.Archetype,
			Name: name, NameKey: company.NameKey(name), Origin: d.Origin, Status: application.DesignDraft,
			Fills: maps.Clone(d.Fills), QualityLossBPS: d.QualityLossBPS, OverheadBPS: d.OverheadBPS,
			LineageID: lineageOf(*d), Version: version, ParentID: d.ID, CreatedBy: p.ID, CreatedAt: h.now()}
		next, err = tx.Production().CreateDesign(ctx, next)
		if err != nil {
			return err
		}
		view, err = h.designViewWithHistory(ctx, tx, snap, c, next)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Design(h.screen(meta, lang), view), nil
}

// DesignRetire handles company.dretire: a company marks a version obsolete.
// Existing production orders already running and existing instances are
// unaffected; production.PlanOrder refuses only a NEW order against it. A
// retired version may still be revised further and reverse engineered.
func (h *ProductionHandler) DesignRetire(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.DesignView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		place := req.confirmed()
		if place {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				place = false
			}
		}
		d, c, err := h.designOf(ctx, tx, snap, p, req.No, place)
		if err != nil {
			return err
		}
		if d.Status != application.DesignFinal {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrStudio, c.Code)
		}
		if place {
			d.Status = application.DesignRetired
			d.UpdatedAt = h.now()
			if err := tx.Production().SaveDesign(ctx, *d); err != nil {
				return err
			}
		}
		view, err = h.designViewWithHistory(ctx, tx, snap, c, *d)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Design(h.screen(meta, lang), view), nil
}

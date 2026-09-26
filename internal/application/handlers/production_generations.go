package handlers

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// improvementReference is the journal and ledger reference_type of an
// improvement project.
const improvementReference = "design_improvement_projects"

// baseDesignName strips a previous " - نسخهٔ N" suffix, so revising a
// revision or improving an improved design never compounds "- نسخهٔ ۲ -
// نسخهٔ ۳" into its name: every version's name reads the same lineage.
func baseDesignName(name string) string {
	if i := strings.Index(name, " - نسخهٔ "); i >= 0 {
		return name[:i]
	}
	return name
}

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
		name := fmt.Sprintf("%s - نسخهٔ %d", baseDesignName(d.Name), version)
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

// ImprovementStart handles company.improve: the plan of an improvement
// project on one attribute of one final design of the company's own — its
// estimated gain (item.NextImprovementBPS), its flat cost and its time — and,
// confirmed, starting it. One project runs at a time per company, like
// research; it completes from the scheduler (Improved) producing the next
// version of the design's lineage.
func (h *ProductionHandler) ImprovementStart(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ImprovementView
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
		d, c, err := h.designOf(ctx, tx, snap, p, req.No, false)
		if err != nil {
			return err
		}
		if d.Status != application.DesignFinal {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrStudio, c.Code)
		}
		attribute := strings.TrimSpace(req.Slot)
		a, ok := snap.Archetype(d.Archetype)
		found := false
		for _, at := range a.Attributes {
			if at.Name == attribute {
				found = true
				break
			}
		}
		if !ok || attribute == "" || !found {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrDesign, req.No)
		}
		gained, err := item.NextImprovementBPS(domainDesign(*d).Improvements[attribute])
		if err != nil {
			return refuseProduction(screens.ProductionRefusedImprovementCapped, c, snap).back(screens.AddrDesign, req.No)
		}
		view = screens.ImprovementView{Ref: companyRef(snap, *c), No: d.No, Design: designGood(snap, *d),
			Attribute: named(attribute, attribute), GainBPS: gained, Cost: h.rules.ImprovementCost,
			Duration: h.scale.RealWait(h.rules.ImprovementTime)}
		if !place {
			return nil
		}
		running, err := tx.Production().RunningImprovement(ctx, c.ID)
		if err != nil {
			return err
		}
		if running != nil {
			return refuseProduction(screens.ProductionRefusedImprovementBusy, c, snap).back(screens.AddrDesign, req.No)
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		now := h.now()
		txID, err := h.spend(ctx, tx, snap, f, application.ReasonDesignImprovement, application.SystemSinkAccountID,
			money.FromMinor(h.rules.ImprovementCost), now)
		if err != nil {
			return err
		}
		finish := now.Add(h.scale.RealWait(h.rules.ImprovementTime))
		id := h.ids.NewID()
		actionID, err := h.schedule(ctx, tx, application.ImprovementActionType, improvementReference, id, c.ID, now, finish)
		if err != nil {
			return err
		}
		if err := tx.Production().StartImprovement(ctx, application.DesignImprovement{ID: id, CompanyID: c.ID,
			DesignID: d.ID, Attribute: attribute, GainedBPS: gained, Cost: h.rules.ImprovementCost,
			LedgerTransactionID: txID, GameActionID: actionID, StartedBy: p.ID, StartedAt: now, FinishAt: finish}); err != nil {
			return err
		}
		view.Started, view.FinishAt = true, finish
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Improvement(h.screen(meta, lang), view), nil
}

// Improved handles company.improved from the SCHEDULER: an improvement
// project finishing exactly once. Its result is the next version of the
// design's lineage, same fills, its Improvements raised by the project's
// already-known gain (item.ApplyImprovement) — deterministic, so a replayed
// completion (blocked by the row's own status check below) would compute the
// identical result anyway.
func (h *ProductionHandler) Improved(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	in, err := productionPayload(meta, req)
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		row, err := tx.Production().Improvement(ctx, in.ID)
		if isSentinel(err, application.ErrImprovementNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.Status != application.ImprovementRunning || (req.ActionID != "" && row.GameActionID != req.ActionID) {
			return nil
		}
		now := h.now()
		if now.Before(row.FinishAt) {
			return internalf("an improvement project finished before its time")
		}
		c, err := tx.Companies().Lock(ctx, row.CompanyID)
		if err != nil {
			return err
		}
		parent, err := tx.Production().DesignByID(ctx, row.DesignID)
		if err != nil {
			return err
		}
		if !c.Active() {
			return tx.Production().FinishImprovement(ctx, row.ID, "", 0, now)
		}
		next, gained, err := item.ApplyImprovement(domainDesign(*parent), row.Attribute)
		if err != nil {
			return errors.Internal(err)
		}
		name := fmt.Sprintf("%s - نسخهٔ %d", baseDesignName(parent.Name), next.Version)
		result := application.Design{ID: h.ids.NewID(), CompanyID: c.ID, Item: parent.Item, Archetype: parent.Archetype,
			Name: name, NameKey: company.NameKey(name), Origin: string(item.OriginAuthored), Status: application.DesignFinal,
			Fills: storedFills(next.Fills), QualityLossBPS: next.QualityLossBPS, OverheadBPS: next.OverheadBPS,
			LineageID: next.LineageID, Version: int64(next.Version), ParentID: next.ParentID, Improvements: next.Improvements,
			CreatedBy: row.StartedBy, CreatedAt: now, FinalizedAt: &now}
		result, err = tx.Production().CreateDesign(ctx, result)
		if err != nil {
			return err
		}
		if err := tx.Production().FinishImprovement(ctx, row.ID, result.ID, gained, now); err != nil {
			return err
		}
		return appendCompanyEvent(ctx, tx, meta, "improved", c.ID, map[string]any{"company_id": c.ID, "code": c.Code,
			"name": c.Name, "owner_id": c.OwnerID, "item": parent.Item, "attribute": row.Attribute,
			"design_no": result.No, "design": result.Name})
	})
}

// retrofitReference is the journal and ledger reference_type of a retrofit
// job.
const retrofitReference = "retrofit_jobs"

// domainInstance is a held piece as the item rules take it — just enough for
// item.Retrofit, which only reads DesignID.
func domainInstance(p application.Piece) item.Instance {
	return item.Instance{Serial: p.Serial, Archetype: p.Archetype, DesignID: p.DesignID, Quality: p.Quality,
		Provenance: item.Provenance{ProductionOrderID: p.OriginRef}}
}

// retrofitPlan is what a retrofit job would do, resolved and checked but not
// yet started.
type retrofitPlan struct {
	kit, target       *application.Piece
	current, toDesign *application.Design
	good              screens.Good
}

// planRetrofit resolves and checks a kit and a target piece an org holds: the
// kit must be an upgrade kit's own output, still in the warehouse; the target
// must be in the warehouse at an earlier version of the same lineage
// (item.Retrofit). Both pieces must belong to org.
func planRetrofit(ctx context.Context, tx application.Tx, snap *content.Snapshot, org application.Org, kitSerial, targetSerial string,
) (retrofitPlan, error) {
	var plan retrofitPlan
	kit, err := tx.Items().PieceBySerial(ctx, kitSerial)
	if isSentinel(err, application.ErrPieceNotFound) {
		return plan, refuseProduction(screens.ProductionRefusedNotFound, nil, snap)
	}
	if err != nil {
		return plan, err
	}
	if kit.Org != org || kit.Holding != application.HoldWarehouse {
		return plan, refuseProduction(screens.ProductionRefusedNotFound, nil, snap)
	}
	order, err := tx.Production().Order(ctx, kit.OriginRef)
	if isSentinel(err, application.ErrProductionOrderNotFound) || (err == nil && order.Kind != application.OrderKindUpgradeKit) {
		return plan, refuseProduction(screens.ProductionRefusedNotSameLineage, nil, snap)
	}
	if err != nil {
		return plan, err
	}
	toDesign, err := tx.Production().DesignByID(ctx, order.TargetDesignID)
	if err != nil {
		return plan, err
	}
	target, err := tx.Items().PieceBySerial(ctx, targetSerial)
	if isSentinel(err, application.ErrPieceNotFound) {
		return plan, refuseProduction(screens.ProductionRefusedNotFound, nil, snap)
	}
	if err != nil {
		return plan, err
	}
	if target.Org != org || target.Holding != application.HoldWarehouse {
		return plan, refuseProduction(screens.ProductionRefusedNotFound, nil, snap)
	}
	current, err := tx.Production().DesignByID(ctx, target.DesignID)
	if err != nil {
		return plan, err
	}
	if _, err := item.Retrofit(domainInstance(*target), domainDesign(*current), domainDesign(*toDesign)); err != nil {
		return plan, refuseProduction(screens.ProductionRefusedNotSameLineage, nil, snap)
	}
	plan.kit, plan.target, plan.current, plan.toDesign = kit, target, current, toDesign
	plan.good = designGood(snap, *toDesign)
	return plan, nil
}

// RetrofitStart handles company.retrofit: the plan of retrofitting one of the
// company's own units with one of its own upgrade kits, and, confirmed,
// starting it. Exactly one retrofit runs on a unit at a time; the kit is
// consumed the moment the job starts (like a reverse-engineering sample),
// whether or not anything else about the unit changes yet — the retrofit
// itself finishes from the scheduler (Retrofitted).
func (h *ProductionHandler) RetrofitStart(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.RetrofitView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		place := req.confirmed() && req.Item != "" && req.Serial != ""
		if place {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				place = false
			}
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		org := application.CompanyOrg(c.ID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		plan, err := planRetrofit(ctx, tx, snap, org, req.Item, req.Serial)
		if err != nil {
			return err
		}
		view = screens.RetrofitView{Ref: companyRef(snap, *c), Good: plan.good, FromVer: max(plan.current.Version, 1),
			ToVer: plan.toDesign.Version, Duration: h.scale.RealWait(h.rules.RetrofitTime)}
		if !place {
			return nil
		}
		now := h.now()
		id := h.ids.NewID()
		finish := now.Add(h.scale.RealWait(h.rules.RetrofitTime))
		actionID, err := h.schedule(ctx, tx, application.RetrofitActionType, retrofitReference, id, c.ID, now, finish)
		if err != nil {
			return err
		}
		if err := tx.Production().StartRetrofit(ctx, application.RetrofitJob{ID: id, OrgKind: org.Kind, OrgID: org.ID,
			PieceID: plan.target.ID, KitPieceID: plan.kit.ID, FromDesignID: plan.current.ID, ToDesignID: plan.toDesign.ID,
			GameActionID: actionID, StartedBy: p.ID, StartedAt: now, FinishAt: finish}); err != nil {
			if isSentinel(err, application.ErrRetrofitBusy) {
				return refuseProduction(screens.ProductionRefusedRetrofitBusy, c, snap)
			}
			return err
		}
		// The kit is consumed now, whether or not the retrofit has finished.
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: plan.kit.Item, PieceID: plan.kit.ID,
			Qty: 1, FromOrg: org, FromHolding: application.HoldWarehouse, Reason: application.ItemRetrofitKit,
			ReferenceType: retrofitReference, ReferenceID: id, At: now}); err != nil {
			return err
		}
		view.Started, view.FinishAt = true, finish
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Retrofit(h.screen(meta, lang), view), nil
}

// Retrofitted handles company.retrofitted from the SCHEDULER: a retrofit job
// finishing exactly once. Its one write is item_pieces.design_id, so the
// unit's attributes, market value and — for a military asset — a war
// strike's inputs are all read fresh off it from this moment on, with
// nothing else to update anywhere.
func (h *ProductionHandler) Retrofitted(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	in, err := productionPayload(meta, req)
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		row, err := tx.Production().Retrofit(ctx, in.ID)
		if isSentinel(err, application.ErrRetrofitNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.Status != application.RetrofitRunning || (req.ActionID != "" && row.GameActionID != req.ActionID) {
			return nil
		}
		now := h.now()
		if now.Before(row.FinishAt) {
			return internalf("a retrofit job finished before its time")
		}
		if err := tx.Items().SetDesignID(ctx, row.PieceID, row.ToDesignID); err != nil {
			return err
		}
		if err := tx.Production().FinishRetrofit(ctx, row.ID, now); err != nil {
			return err
		}
		if row.OrgKind != application.OrgCompany {
			return nil
		}
		c, err := tx.Companies().Lock(ctx, row.OrgID)
		if err != nil || !c.Active() {
			return err
		}
		to, err := tx.Production().DesignByID(ctx, row.ToDesignID)
		if err != nil {
			return err
		}
		return appendCompanyEvent(ctx, tx, meta, "retrofitted", c.ID, map[string]any{"company_id": c.ID, "code": c.Code,
			"name": c.Name, "owner_id": c.OwnerID, "item": to.Item, "design_no": to.No, "design": to.Name})
	})
}

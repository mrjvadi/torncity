package handlers

import (
	"context"
	stderrors "errors"
	"maps"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The design studio: a draft for a kind of good, its slots filled one by one
// with components the company may design with (item.ValidateDesign: every
// technology they need owned, published or licensed), a quantity typed where
// a slot takes a range, a name, and the final design — private to the
// company, what it makes tradeable.

// domainDesign is a stored design as the item rules take it.
func domainDesign(d application.Design) item.Design {
	out := item.Design{ID: d.ID, Archetype: d.Archetype, Fills: map[string]item.Fill{}, Origin: item.Origin(d.Origin),
		QualityLossBPS: d.QualityLossBPS, OverheadBPS: d.OverheadBPS}
	for slot, f := range d.Fills {
		out.Fills[slot] = item.Fill{Component: f.Component, Quantity: f.Quantity}
	}
	return out
}

// storedFills is a domain design's fills as stored.
func storedFills(fills map[string]item.Fill) map[string]application.DesignFill {
	out := make(map[string]application.DesignFill, len(fills))
	for slot, f := range fills {
		out[slot] = application.DesignFill{Component: f.Component, Quantity: f.Quantity}
	}
	return out
}

// partial is the archetype with every slot optional, so a draft's filled
// slots can be computed before the rest are.
func partial(a item.Archetype) item.Archetype {
	out := a
	out.Slots = make([]item.Slot, len(a.Slots))
	for i, s := range a.Slots {
		s.Optional = true
		out.Slots[i] = s
	}
	return out
}

// prices are the components' reference prices as money.
func prices(snap *content.Snapshot) map[string]money.Amount {
	out := map[string]money.Amount{}
	for code, p := range snap.ComponentPrices() {
		out[code] = money.FromMinor(p)
	}
	return out
}

// designView builds a design's screen.
func (h *ProductionHandler) designView(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
	d application.Design, choosing string,
) (screens.DesignView, error) {
	a, ok := snap.Archetype(d.Archetype)
	if !ok {
		return screens.DesignView{}, internalf("a design of an archetype the content does not have: " + d.Archetype)
	}
	v := screens.DesignView{Ref: companyRef(snap, *c), No: d.No, Name: d.Name, Item: itemNamed(snap, d.Item),
		Status: d.Status, Origin: d.Origin, QualityLossBPS: d.QualityLossBPS, OverheadBPS: d.OverheadBPS}
	if d.SourceDesignID != "" {
		if src, err := tx.Production().DesignByID(ctx, d.SourceDesignID); err == nil {
			v.Source = src.Name
		}
	}
	f, err := readFloor(ctx, tx, snap, c)
	if err != nil {
		return v, err
	}
	components := snap.Components()
	complete := true
	locked := map[string]bool{}
	for _, s := range a.Slots {
		line := screens.SlotLine{Slot: s.Name, Optional: s.Optional, Min: s.Quantity.Min, Max: s.Quantity.Max, Unit: s.Quantity.Unit}
		if fill, ok := d.Fills[s.Name]; ok {
			comp, _ := snap.ComponentDef(fill.Component)
			line.Component, line.Qty = named(comp.Code, comp.Name), fill.Quantity
			if d.Status == application.DesignDraft {
				for _, t := range comp.RequiresTechnology {
					if !f.access.Allows(t) {
						locked[t] = true
					}
				}
			}
		} else if !s.Optional {
			complete = false
		}
		v.Slots = append(v.Slots, line)
	}
	v.Complete = complete
	for _, t := range sortedTechs(snap, keysOf(locked)) {
		td, _ := snap.Technology(t)
		v.Locked = append(v.Locked, named(td.Code, td.Name))
	}
	dd := domainDesign(d)
	if attrs, err := item.ComputeAttributes(partial(a), dd, components); err == nil {
		for _, at := range a.Attributes {
			v.Attributes = append(v.Attributes, screens.AttributeLine{Name: at.Name, Value: attrs[at.Name], Observable: at.Observable})
		}
	}
	if recipe, err := item.DeriveRecipe(dd); err == nil {
		if cost, err := item.ItemizedCost(recipe, prices(snap)); err == nil {
			v.CostFloor = cost.Minor()
		}
	}
	if choosing != "" && d.Status == application.DesignDraft {
		if _, ok := a.Slot(choosing); ok {
			v.Choosing = choosing
			for _, cd := range snap.SlotCandidates(a, choosing) {
				v.Candidates = append(v.Candidates, screens.Candidate{Component: named(cd.Code, cd.Name), Price: cd.BasePrice,
					Quality: cd.Quality(), Locked: item.CanManufacture(cd.Component(), f.access) != nil})
			}
		}
	}
	return v, nil
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// designOf reads a design by number and the company it belongs to, which
// the player must run: the company locked first, then the design.
func (h *ProductionHandler) designOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	raw string, lock bool,
) (*application.Design, *application.Company, error) {
	no, ok := number(raw)
	if !ok {
		return nil, nil, refuseProduction(screens.ProductionRefusedNotFound, nil, snap)
	}
	d, err := tx.Production().Design(ctx, no, false)
	if isSentinel(err, application.ErrDesignNotFound) {
		return nil, nil, refuseProduction(screens.ProductionRefusedNotFound, nil, snap)
	}
	if err != nil {
		return nil, nil, err
	}
	owner, err := tx.Companies().ByID(ctx, d.CompanyID)
	if err != nil {
		return nil, nil, err
	}
	c, err := h.managed(ctx, tx, snap, p, owner.Code, company.RightProduce)
	if err != nil {
		return nil, nil, err
	}
	if lock {
		if d, err = tx.Production().Design(ctx, no, true); err != nil {
			return nil, nil, err
		}
	}
	return d, c, nil
}

// Studio handles company.studio: the company's designs, and the goods it
// may start a design of.
func (h *ProductionHandler) Studio(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.StudioView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		designs, err := tx.Production().Designs(ctx, c.ID)
		if err != nil {
			return err
		}
		view = screens.StudioView{Ref: companyRef(snap, *c), CanDesign: len(designs) < h.rules.MaxDesigns,
			Max: h.rules.MaxDesigns, Need: h.rules.DesignMinSkill}
		for _, d := range designs {
			view.Designs = append(view.Designs, screens.DesignLine{No: d.No, Name: d.Name, Item: itemNamed(snap, d.Item),
				Status: d.Status, Origin: d.Origin})
		}
		// Staged: what the company may design now, and what is one step
		// away with the way in; the rest waits (docs/adr/0021, section 14).
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		st, err := h.stageOf(ctx, tx, snap, f)
		if err != nil {
			return err
		}
		view.Kinds, view.Next, view.Hidden, err = h.studioKinds(snap, f, st)
		view.CanResearch = company.RoleOf(p.ID, c.OwnerID, c.ManagerID).Can(company.RightResearch)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Studio(h.screen(meta, lang), view), nil
}

// checkEngineer refuses a design of archetype a when nobody in the company
// has its craft at the level authoring needs.
func (h *ProductionHandler) checkEngineer(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor, a item.Archetype) error {
	level, _, err := f.best(ctx, tx, a.ReverseSkill)
	if err != nil {
		return err
	}
	if level < h.rules.DesignMinSkill {
		r := refuseProduction(screens.ProductionRefusedSkill, f.c, snap).back(screens.AddrStudio, f.c.Code)
		r.view.Skill, r.view.Level, r.view.Have = a.ReverseSkill, h.rules.DesignMinSkill, level
		r.view.Gap = skillGapOf(snap, f.c.Code, a.ReverseSkill, h.rules.DesignMinSkill)
		return r
	}
	return nil
}

// DesignNew handles company.dnew: a new draft design of a good.
func (h *ProductionHandler) DesignNew(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
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
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		var def content.ItemDef
		for _, d := range snap.DesignableItems(c.TypeCode) {
			if d.Code == strings.TrimSpace(req.Item) {
				def = d
			}
		}
		if def.Code == "" {
			return refuseProduction(screens.ProductionRefusedWrongType, c, snap).back(screens.AddrStudio, c.Code)
		}
		a, _ := snap.Archetype(def.Archetype)
		designs, err := tx.Production().Designs(ctx, c.ID)
		if err != nil {
			return err
		}
		if !fresh {
			// A replay shows the draft the first press made.
			for _, d := range designs {
				if d.Status == application.DesignDraft && d.Item == def.Code && d.CreatedBy == p.ID {
					view, err = h.designView(ctx, tx, snap, c, d, "")
					return err
				}
			}
		}
		if len(designs) >= h.rules.MaxDesigns {
			r := refuseProduction(screens.ProductionRefusedMaxDesigns, c, snap).back(screens.AddrStudio, c.Code)
			r.view.Max = h.rules.MaxDesigns
			return r
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		// A good the company may not design yet — its own technology, or
		// no part it may design with in a slot the design must fill — is
		// refused, naming what it lacks.
		if err := h.designGate(snap, f, def, a); err != nil {
			return err
		}
		if err := h.checkEngineer(ctx, tx, snap, f, a); err != nil {
			return err
		}
		now := h.now()
		fills := map[string]application.DesignFill{}
		d, err := tx.Production().CreateDesign(ctx, application.Design{ID: h.ids.NewID(), CompanyID: c.ID, Item: def.Code,
			Archetype: a.Code, Origin: string(item.OriginAuthored), Status: application.DesignDraft, Fills: fills,
			CreatedBy: p.ID, CreatedAt: now})
		if err != nil {
			return err
		}
		view, err = h.designView(ctx, tx, snap, c, d, "")
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Design(h.screen(meta, lang), view), nil
}

// Design handles company.design: one design, with a slot's candidates when
// the request names a slot.
func (h *ProductionHandler) Design(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	return h.designWith(ctx, meta, req, strings.TrimSpace(req.Slot), "")
}

func (h *ProductionHandler) designWith(ctx context.Context, meta envelope.Metadata, req ProductionRequest, choosing, notice string) (*presenter.Response, error) {
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
		d, c, err := h.designOf(ctx, tx, snap, p, req.No, false)
		if err != nil {
			return err
		}
		view, err = h.designView(ctx, tx, snap, c, *d, choosing)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	view.Notice = notice
	return screens.Design(h.screen(meta, lang), view), nil
}

// editDraft runs change on a draft design under its lock and saves it.
func (h *ProductionHandler) editDraft(ctx context.Context, meta envelope.Metadata, req ProductionRequest, choosing string,
	change func(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company, a item.Archetype, d *application.Design) error,
) (*presenter.Response, error) {
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
		d, c, err := h.designOf(ctx, tx, snap, p, req.No, true)
		if err != nil {
			return err
		}
		if d.Status != application.DesignDraft {
			return refuseProduction(screens.ProductionRefusedFinal, c, snap).back(screens.AddrDesign, req.No)
		}
		a, ok := snap.Archetype(d.Archetype)
		if !ok {
			return internalf("a design of an archetype the content does not have: " + d.Archetype)
		}
		if err := change(ctx, tx, snap, c, a, d); err != nil {
			return err
		}
		d.UpdatedAt = h.now()
		if err := tx.Production().SaveDesign(ctx, *d); err != nil {
			if isSentinel(err, application.ErrDesignNameTaken) {
				return refuseProduction(screens.ProductionRefusedNameTaken, c, snap).back(screens.AddrDesign, req.No)
			}
			return err
		}
		view, err = h.designView(ctx, tx, snap, c, *d, choosing)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Design(h.screen(meta, lang), view), nil
}

// DesignFill handles company.dfill: a component in a slot of a draft. A slot
// with a fixed quantity takes it; one with a range starts at its least, for
// the designer to change (company.dqty). An optional slot may be emptied.
// Only a component the company may design with fits.
func (h *ProductionHandler) DesignFill(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	slot := strings.TrimSpace(req.Slot)
	var reopen string
	resp, err := h.editDraft(ctx, meta, req, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
		a item.Archetype, d *application.Design,
	) error {
		s, ok := a.Slot(slot)
		if !ok {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrDesign, req.No)
		}
		code := strings.TrimSpace(req.Component)
		fills := maps.Clone(d.Fills)
		if fills == nil {
			fills = map[string]application.DesignFill{}
		}
		if code == screens.SlotEmpty {
			if !s.Optional {
				return refuseProduction(screens.ProductionRefusedIncomplete, c, snap).back(screens.AddrDesign, req.No)
			}
			delete(fills, slot)
			d.Fills = fills
			return nil
		}
		comp, ok := snap.ComponentDef(code)
		if !ok || comp.Category != s.Accepts {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrDesign, req.No, slot)
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		if err := item.CanManufacture(comp.Component(), f.access); err != nil {
			r := refuseProduction(screens.ProductionRefusedTechLocked, c, snap).back(screens.AddrDesign, req.No, slot)
			for _, t := range comp.RequiresTechnology {
				if !f.access.Allows(t) {
					td, _ := snap.Technology(t)
					r.view.Techs = append(r.view.Techs, named(td.Code, td.Name))
				}
			}
			return r
		}
		qty := s.Quantity.Min
		if old, ok := fills[slot]; ok && s.Quantity.Contains(old.Quantity) {
			qty = old.Quantity
		}
		fills[slot] = application.DesignFill{Component: code, Quantity: qty}
		d.Fills = fills
		if !s.Quantity.IsFixed() {
			reopen = slot
		}
		return nil
	})
	if err != nil || resp == nil || reopen == "" {
		return resp, err
	}
	return h.designWith(ctx, meta, req, reopen, "")
}

// DesignQty handles company.dqty: the typed quantity of a slot that takes a
// range.
func (h *ProductionHandler) DesignQty(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	slot := strings.TrimSpace(req.Slot)
	return h.editDraft(ctx, meta, req, slot, func(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
		a item.Archetype, d *application.Design,
	) error {
		s, ok := a.Slot(slot)
		fill, filled := d.Fills[slot]
		if !ok || !filled {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrDesign, req.No)
		}
		qty, ok := quantityArg(req.Qty)
		if !ok || !s.Quantity.Contains(qty) {
			return refuseProduction(screens.ProductionRefusedAmount, c, snap).back(screens.AddrDesign, req.No, slot)
		}
		fills := maps.Clone(d.Fills)
		fills[slot] = application.DesignFill{Component: fill.Component, Quantity: qty}
		d.Fills = fills
		return nil
	})
}

// designNameRules are what a design's name may be: a company name's rules.
func (h *ProductionHandler) designNameRules(snap *content.Snapshot) company.NameRules {
	return company.NameRules{MinRunes: h.rules.NameMin, MaxRunes: h.rules.NameMax, Reserved: snap.CompanyReservedNames()}
}

// DesignName handles company.dname: the typed name of a draft.
func (h *ProductionHandler) DesignName(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	return h.editDraft(ctx, meta, req, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
		a item.Archetype, d *application.Design,
	) error {
		name, err := company.CheckName(req.Name, h.designNameRules(snap))
		if err != nil {
			kind := screens.ProductionRefusedNameCharset
			var length company.NameLength
			if stderrors.As(err, &length) {
				kind = screens.ProductionRefusedNameLength
			}
			r := refuseProduction(kind, c, snap).back(screens.AddrDesign, req.No)
			r.view.Level, r.view.Max = h.rules.NameMin, h.rules.NameMax
			return r
		}
		d.Name, d.NameKey = name, company.NameKey(name)
		return nil
	})
}

// DesignFinal handles company.dfinal: a named, complete draft whose parts
// the company may all design with becomes a final design. From now on it is
// produced, never edited.
func (h *ProductionHandler) DesignFinal(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	resp, err := h.editDraft(ctx, meta, req, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
		a item.Archetype, d *application.Design,
	) error {
		back := []string{screens.AddrDesign, req.No}
		if d.Name == "" {
			return refuseProduction(screens.ProductionRefusedNoName, c, snap).back(back...)
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		if err := h.checkEngineer(ctx, tx, snap, f, a); err != nil {
			return err
		}
		if def, ok := snap.ItemDef(d.Item); ok {
			if err := h.designGate(snap, f, def, a); err != nil {
				var r *productionRefusal
				if stderrors.As(err, &r) {
					r.back(back...)
				}
				return err
			}
		}
		err = item.ValidateDesign(a, domainDesign(*d), snap.Components(), f.access)
		switch {
		case stderrors.Is(err, item.ErrTechnologyLocked):
			r := refuseProduction(screens.ProductionRefusedTechLocked, c, snap).back(back...)
			locked := map[string]bool{}
			for _, fill := range d.Fills {
				comp, _ := snap.ComponentDef(fill.Component)
				for _, t := range comp.RequiresTechnology {
					if !f.access.Allows(t) {
						locked[t] = true
					}
				}
			}
			for _, t := range sortedTechs(snap, keysOf(locked)) {
				td, _ := snap.Technology(t)
				r.view.Techs = append(r.view.Techs, named(td.Code, td.Name))
			}
			return r
		case err != nil:
			return refuseProduction(screens.ProductionRefusedIncomplete, c, snap).back(back...)
		}
		now := h.now()
		d.Status, d.FinalizedAt = application.DesignFinal, &now
		return nil
	})
	return resp, err
}

// finalAt is a design's finalisation, or zero.
func finalAt(d application.Design) time.Time {
	if d.FinalizedAt == nil {
		return time.Time{}
	}
	return *d.FinalizedAt
}

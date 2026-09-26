package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/inventory"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/production"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Production orders: N units of one of the company's final designs, or N
// batches of a component it makes. The order is planned by
// production.PlanOrder against the warehouse; its inputs are taken out when
// it is placed, every one of them in the same transaction or none (a
// ShortageError lists everything short); it runs on the game clock for the
// time the plan says for the company's crew; and when its scheduled action
// runs, once, its output enters the warehouse at a quality rolled from the
// inputs' quality and the crew's craft (production.RollQuality), each piece
// with the order as its provenance.

// productionReference is the journal and ledger reference_type of an order.
const productionReference = "production_orders"

// orderTarget is what an order makes, as the rules take it.
type orderTarget struct {
	good      screens.Good
	kind      string
	design    *application.Design
	archetype item.Archetype
	plan      item.Design
	output    string
	// batch is units per batch of a component; zero for a design.
	batch int64
	skill string
}

// target resolves a production target of a company: «d12», one of its final
// designs, or the code of a component its kind makes and it may make.
func (h *ProductionHandler) target(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor, raw string) (orderTarget, error) {
	return h.targetOf(ctx, tx, snap, f, raw, false)
}

// kitTarget resolves an UPGRADE KIT order's target: «d12», one of the
// company's own final designs, and only that — a kit is built for a specific
// version of the company's own product line, never for a component. Its
// archetype, recipe and cost are the design's own (retrofit is refitting
// with a fresh build's worth of parts, docs/adr/0021 generations addendum);
// what makes the order a kit is only its kind and TargetDesignID, both set
// by the caller.
func (h *ProductionHandler) kitTarget(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor, raw string) (orderTarget, error) {
	return h.targetOf(ctx, tx, snap, f, raw, true)
}

func (h *ProductionHandler) targetOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor, raw string, forKit bool) (orderTarget, error) {
	raw = strings.TrimSpace(raw)
	c := f.c
	back := []string{screens.AddrOrders, c.Code}
	if no, ok := strings.CutPrefix(raw, screens.DesignTargetPrefix); ok {
		n, ok := number(no)
		if !ok {
			return orderTarget{}, refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(back...)
		}
		d, err := tx.Production().Design(ctx, n, false)
		if isSentinel(err, application.ErrDesignNotFound) || (err == nil && (d.CompanyID != c.ID || d.Status != application.DesignFinal)) {
			return orderTarget{}, refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(back...)
		}
		if err != nil {
			return orderTarget{}, err
		}
		a, ok := snap.Archetype(d.Archetype)
		if !ok {
			return orderTarget{}, internalf("a design of an archetype the content does not have: " + d.Archetype)
		}
		if !f.def.Makes(a.Code) {
			return orderTarget{}, refuseProduction(screens.ProductionRefusedWrongType, c, snap).back(back...)
		}
		// Arms are made under a defence licence in force
		// (docs/adr/0022, section 2.14): a company whose licence lapsed
		// keeps its designs but orders no more of an export-controlled
		// good.
		if def, ok := snap.ItemDef(d.Item); ok && def.ExportControl.Control().Restricted {
			if lic, ok := snap.DefenceLicence(); ok {
				buyer, err := f.buyer(ctx, tx, snap, h.now())
				if err != nil {
					return orderTarget{}, err
				}
				if buyer.Sector != lic.Sector {
					return orderTarget{}, refuseProduction(screens.ProductionRefusedNotCleared, c, snap).back(back...)
				}
			}
		}
		kind := application.OrderKindDesign
		if forKit {
			kind = application.OrderKindUpgradeKit
		}
		return orderTarget{good: designGood(snap, *d), kind: kind, design: d, archetype: a,
			plan: domainDesign(*d), output: d.Item, skill: a.ReverseSkill}, nil
	}
	if forKit {
		// A kit is built for a specific version of the company's own
		// product line; a component has no version to retrofit anything to.
		return orderTarget{}, refuseProduction(screens.ProductionRefusedWrongType, c, snap).back(back...)
	}
	a, plan, def, ok := snap.ComponentRecipe(raw)
	if !ok || !hasCode(def.By, c.TypeCode) {
		return orderTarget{}, refuseProduction(screens.ProductionRefusedWrongType, c, snap).back(back...)
	}
	comp, _ := snap.ComponentDef(raw)
	if err := item.CanManufacture(comp.Component(), f.access); err != nil {
		r := refuseProduction(screens.ProductionRefusedTechLocked, c, snap).back(back...)
		for _, t := range comp.RequiresTechnology {
			if !f.access.Allows(t) {
				td, _ := snap.Technology(t)
				r.view.Techs = append(r.view.Techs, named(td.Code, td.Name))
			}
		}
		return orderTarget{}, r
	}
	return orderTarget{good: screens.Good{Component: true, Item: named(comp.Code, comp.Name)}, kind: application.OrderKindComponent,
		archetype: a, plan: plan, output: raw, batch: def.Batch, skill: def.Skill}, nil
}

// targets lists what a company can order: its final designs, then the
// components its kind makes and it may make.
func (h *ProductionHandler) targets(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor) ([]screens.ProduceTarget, error) {
	var out []screens.ProduceTarget
	designs, err := tx.Production().Designs(ctx, f.c.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range designs {
		if d.Status == application.DesignFinal && f.def.Makes(d.Archetype) {
			out = append(out, screens.ProduceTarget{Good: designGood(snap, d)})
		}
	}
	for _, comp := range snap.MadeBy(f.c.TypeCode) {
		if item.CanManufacture(comp.Component(), f.access) != nil {
			continue
		}
		out = append(out, screens.ProduceTarget{Good: screens.Good{Component: true, Item: named(comp.Code, comp.Name)},
			Batch: comp.Production.Batch})
	}
	return out, nil
}

// warehouseStock is a company's counted units, as production.PlanOrder takes
// stock.
func warehouseStock(ctx context.Context, tx application.Tx, companyID string) (production.Stock, error) {
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.CompanyOrg(companyID), application.HoldWarehouse)
	if err != nil {
		return nil, err
	}
	out := production.Stock{}
	for _, s := range stacks {
		out[s.Item] = s.Qty
	}
	return out, nil
}

// Orders handles company.orders: the production floor — what it can make,
// its orders running and done.
func (h *ProductionHandler) Orders(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.OrdersView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightProduce)
		if err != nil {
			return err
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		if err := h.withCitizens(ctx, tx, f); err != nil {
			return err
		}
		view = screens.OrdersView{Ref: companyRef(snap, *c), Max: h.rules.MaxRunningOrders, Crew: f.crew()}
		if view.Targets, err = h.targets(ctx, tx, snap, f); err != nil {
			return err
		}
		st, err := h.stageOf(ctx, tx, snap, f)
		if err != nil {
			return err
		}
		if view.Locked, err = h.lockedComponents(snap, f, st); err != nil {
			return err
		}
		orders, err := tx.Production().Orders(ctx, c.ID, 10)
		if err != nil {
			return err
		}
		now := h.now()
		for _, o := range orders {
			line, err := h.orderLine(ctx, tx, snap, o, now)
			if err != nil {
				return err
			}
			if !line.Done {
				view.Running++
			}
			view.Orders = append(view.Orders, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Orders(h.screen(meta, lang), view), nil
}

// orderLine is an order for a screen.
func (h *ProductionHandler) orderLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, o application.ProductionOrder,
	now time.Time,
) (screens.ProductionLine, error) {
	good := goodOf(snap, o.Output)
	if o.DesignID != "" {
		d, err := tx.Production().DesignByID(ctx, o.DesignID)
		if err != nil {
			return screens.ProductionLine{}, err
		}
		good = designGood(snap, *d)
	}
	return screens.ProductionLine{No: o.No, Good: good, Output: o.OutputQty, Done: o.Status == application.OrderDone,
		Quality: o.Quality, FinishAt: o.FinishAt, Left: countdownTo(o.FinishAt, now)}, nil
}

// plan is an order's arithmetic for the company as it stands.
type plan struct {
	target orderTarget
	stock  production.Stock
	plan   production.Plan
	short  *production.ShortageError
	per    item.Recipe
	crew   int
}

// planOrder plans qty of a target against the warehouse; a shortage is
// returned in the plan, not as an error.
func (h *ProductionHandler) planOrder(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor, t orderTarget,
	qty int64,
) (plan, error) {
	if err := h.withCitizens(ctx, tx, f); err != nil {
		return plan{}, err
	}
	out := plan{target: t, crew: f.crew()}
	var err error
	switch {
	case f.readOnly && f.stockKept != nil:
		out.stock = f.stockKept
	default:
		if out.stock, err = warehouseStock(ctx, tx, f.c.ID); err != nil {
			return out, err
		}
		if f.readOnly {
			f.stockKept = out.stock
		}
	}
	if out.per, err = item.DeriveRecipe(t.plan); err != nil {
		return out, errors.Internal(err)
	}
	profile, ok := snap.Profile(t.archetype.Method)
	if !ok {
		return out, internalf("no timing for the method " + string(t.archetype.Method))
	}
	if qty < 1 {
		return out, nil
	}
	out.plan, err = production.PlanOrder(production.Request{Archetype: t.archetype, Design: t.plan, Components: snap.Components(),
		Quantity: qty, Workers: out.crew}, profile, out.stock)
	var short *production.ShortageError
	switch {
	case stderrors.As(err, &short):
		out.short = short
		return out, nil
	case stderrors.Is(err, production.ErrInvalidQuantity):
		return out, refuseProduction(screens.ProductionRefusedAmount, f.c, snap).back(screens.AddrProduce, f.c.Code, t.good.TargetArg())
	case stderrors.Is(err, production.ErrTooLong):
		return out, refuseProduction(screens.ProductionRefusedTooLong, f.c, snap).back(screens.AddrProduce, f.c.Code, t.good.TargetArg())
	case err != nil:
		return out, errors.Internal(err)
	}
	return out, nil
}

// maxOrder is the most units (or batches) the warehouse can make now.
func (p plan) maxOrder() int64 {
	if len(p.per) == 0 {
		return 0
	}
	most := int64(production.MaxOrderQuantity)
	for _, in := range p.per {
		most = min(most, p.stock[in.Component]/max(in.Quantity, 1))
	}
	return max(most, 0)
}

// produceView renders a plan.
func (h *ProductionHandler) produceView(snap *content.Snapshot, c *application.Company, p plan, qty int64, now time.Time) screens.ProduceView {
	v := screens.ProduceView{Ref: companyRef(snap, *c), Target: screens.ProduceTarget{Good: p.target.good, Batch: p.target.batch},
		Qty: qty, Crew: p.crew, MaxQty: p.maxOrder()}
	for _, in := range p.per {
		comp, _ := snap.ComponentDef(in.Component)
		need := in.Quantity * max(qty, 0)
		v.Recipe = append(v.Recipe, screens.RecipeLine{Component: named(comp.Code, comp.Name), Per: in.Quantity, Need: need,
			Have: p.stock[in.Component]})
	}
	if p.short != nil {
		for _, s := range p.short.Shortages {
			comp, _ := snap.ComponentDef(s.Component)
			v.Short = append(v.Short, screens.Shortage{Component: named(comp.Code, comp.Name), Need: s.Need, Have: s.Have})
		}
	}
	if qty > 0 && p.short == nil {
		v.Output = p.plan.Output * max(p.target.batch, 1)
		v.Duration = h.scale.RealWait(p.plan.Duration)
		v.FinishAt = now.Add(v.Duration)
	}
	return v
}

// Produce handles company.produce: the plan of an order — its inputs, what
// the warehouse holds, its time for the crew — and, confirmed, placing it.
func (h *ProductionHandler) Produce(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	qty := int64(0)
	if strings.TrimSpace(req.Qty) != "" {
		var ok bool
		if qty, ok = quantityArg(req.Qty); !ok {
			qty = -1
		}
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ProduceView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		place := req.confirmed() && qty > 0
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
		if qty < 0 {
			return refuseProduction(screens.ProductionRefusedAmount, c, snap).back(screens.AddrProduce, c.Code, req.Target)
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		t, err := h.target(ctx, tx, snap, f, req.Target)
		if err != nil {
			return err
		}
		if qty == 0 {
			// No size asked: the screen opens at a quick order — what the
			// warehouse can cover, up to the quick size — so one more tap
			// places it (docs/adr/0021, section 14).
			probe, err := h.planOrder(ctx, tx, snap, f, t, 0)
			if err != nil {
				return err
			}
			qty = h.quickQty(probe.maxOrder())
		}
		org := application.CompanyOrg(c.ID)
		if place {
			if err := tx.Items().LockOrg(ctx, org); err != nil {
				return err
			}
		}
		pl, err := h.planOrder(ctx, tx, snap, f, t, qty)
		if err != nil {
			return err
		}
		now := h.now()
		if !place || pl.short != nil {
			view = h.produceView(snap, c, pl, qty, now)
			if err := h.sourceShortages(ctx, tx, snap, f, &view, now); err != nil {
				return err
			}
			if place && pl.short != nil {
				r := refuseProduction(screens.ProductionRefusedShortage, c, snap).back(screens.AddrProduce, c.Code, t.good.TargetArg())
				r.view.Shortages = view.Short
				return r
			}
			return nil
		}
		running, err := tx.Production().RunningOrders(ctx, c.ID)
		if err != nil {
			return err
		}
		if running >= h.rules.MaxRunningOrders {
			r := refuseProduction(screens.ProductionRefusedMaxOrders, c, snap).back(screens.AddrOrders, c.Code)
			r.view.Max = h.rules.MaxRunningOrders
			return r
		}
		quality := map[string]int{}
		for _, in := range pl.plan.Consumed {
			comp, _ := snap.ComponentDef(in.Component)
			quality[in.Component] = comp.Quality()
		}
		inputQuality, err := production.InputQuality(pl.plan.Consumed, quality)
		if err != nil {
			return errors.Internal(err)
		}
		skill, _, err := f.best(ctx, tx, t.skill)
		if err != nil {
			return err
		}
		id := h.ids.NewID()
		finish := now.Add(h.scale.RealWait(pl.plan.Duration))
		actionID, err := h.schedule(ctx, tx, application.ProductionActionType, productionReference, id, c.ID, now, finish)
		if err != nil {
			return err
		}
		consumed := map[string]int64{}
		for _, in := range pl.plan.Consumed {
			consumed[in.Component] = in.Quantity
		}
		o := application.ProductionOrder{ID: id, CompanyID: c.ID, Kind: t.kind, Output: t.output, Quantity: qty,
			OutputQty: pl.plan.Output * max(t.batch, 1), Workers: pl.crew, InputQuality: inputQuality, SkillLevel: skill,
			Consumed: consumed, GameActionID: actionID, PlacedBy: p.ID, StartedAt: now, FinishAt: finish}
		if t.design != nil {
			o.DesignID = t.design.ID
		}
		if o, err = tx.Production().PlaceOrder(ctx, o); err != nil {
			return err
		}
		// All or nothing: every input leaves the warehouse in this
		// transaction, or the order is not placed.
		for _, in := range pl.plan.Consumed {
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: in.Component, Qty: in.Quantity,
				FromOrg: org, FromHolding: application.HoldWarehouse, Reason: application.ItemProductionInput,
				ReferenceType: productionReference, ReferenceID: id, At: now}); err != nil {
				return err
			}
		}
		view = h.produceView(snap, c, pl, qty, now)
		view.Placed = &screens.ProductionLine{No: o.No, Good: t.good, Output: o.OutputQty, FinishAt: finish,
			Left: countdownTo(finish, now)}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Produce(h.screen(meta, lang), view), nil
}

// Produced handles company.produced from the SCHEDULER: an order finishing.
// Exactly once: the order row, locked, must still be running under the
// action that finishes it. Its output enters the company's warehouse at the
// quality rolled for it, each piece with the order as its provenance.
func (h *ProductionHandler) Produced(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	in, err := productionPayload(meta, req)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		o, err := tx.Production().Order(ctx, in.ID)
		if isSentinel(err, application.ErrProductionOrderNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if o.Status != application.OrderRunning || (req.ActionID != "" && o.GameActionID != req.ActionID) {
			return nil
		}
		now := h.now()
		if now.Before(o.FinishAt) {
			return internalf("a production order finished before its time")
		}
		c, err := tx.Companies().Lock(ctx, o.CompanyID)
		if err != nil {
			return err
		}
		org := application.CompanyOrg(c.ID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		var (
			design *application.Design
			loss   int64
		)
		if o.DesignID != "" {
			if design, err = tx.Production().DesignByID(ctx, o.DesignID); err != nil {
				return err
			}
			loss = design.QualityLossBPS
		}
		roll := func(i int) (int, error) {
			return production.RollQuality(production.QualityInputs{InputQuality: o.InputQuality, WorkerSkill: o.SkillLevel,
				DesignQualityLossBPS: loss}, rollFrom(o.ID, i))
		}
		quality, err := roll(0)
		if err != nil {
			return errors.Internal(err)
		}
		move := application.ItemMove{Item: o.Output, Qty: o.OutputQty, ToOrg: org, ToHolding: application.HoldWarehouse,
			Reason: application.ItemProduced, ReferenceType: productionReference, ReferenceID: o.ID, At: now}
		def, isItem := snap.ItemDef(o.Output)
		if isItem && def.Form == string(inventory.Unique) {
			arch, ok := snap.Archetype(def.Archetype)
			if !ok {
				return internalf("a good of an archetype the content does not have: " + def.Archetype)
			}
			sum := 0
			for i := int64(0); i < o.OutputQty; i++ {
				q, err := roll(int(i))
				if err != nil {
					return errors.Internal(err)
				}
				sum += q
				inst, err := item.NewInstance(arch, newSerial(h.ids), o.DesignID, q, item.Provenance{ProductionOrderID: o.ID})
				if err != nil {
					return errors.Internal(err)
				}
				m := move
				m.ID, m.Qty = h.ids.NewID(), 1
				if err := tx.Items().CreatePiece(ctx, application.Piece{ID: h.ids.NewID(), Serial: inst.Serial, Item: o.Output,
					Archetype: inst.Archetype, Quality: inst.Quality, UsesLeft: def.Durability, Org: org,
					Holding: application.HoldWarehouse, DesignID: o.DesignID, Origin: application.OriginProduction,
					OriginRef: o.ID, CreatedAt: now}, m); err != nil {
					return err
				}
			}
			quality = sum / int(max(o.OutputQty, 1))
		} else {
			move.ID = h.ids.NewID()
			if err := tx.Items().Move(ctx, move); err != nil {
				return err
			}
		}
		if err := tx.Production().FinishOrder(ctx, o.ID, quality, now); err != nil {
			return err
		}
		payload := map[string]any{"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode,
			"owner_id": c.OwnerID, "city_id": c.CityID, "item": o.Output, "qty": o.OutputQty, "quality": quality,
			"component": !isItem}
		if design != nil {
			payload["design"], payload["design_no"] = design.Name, design.No
		}
		if err := appendCompanyEvent(ctx, tx, meta, "produced", c.ID, payload); err != nil {
			return err
		}
		if design == nil || !c.Active() {
			return nil
		}
		done, err := tx.Production().DoneOrders(ctx, design.ID)
		if err != nil || done != 1 {
			return err
		}
		return appendCompanyEvent(ctx, tx, meta, "product_launched", c.ID, payload)
	})
}

// quickQty is a quick order's size: the quick size, or what the warehouse
// can cover when that is less but something.
func (h *ProductionHandler) quickQty(most int64) int64 {
	q := int64(max(h.rules.QuickUnits, 1))
	if most >= 1 && most < q {
		return most
	}
	return q
}

// sourceShortages says where each input an order is short of comes from —
// a supplier of the city, the company's own floor, or other companies — and,
// when the suppliers sell every one of them now, what buying them all costs:
// the one-tap purchase the screen offers.
func (h *ProductionHandler) sourceShortages(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor,
	v *screens.ProduceView, now time.Time,
) error {
	if len(v.Short) == 0 {
		return nil
	}
	city, err := h.cities.ByID(ctx, f.c.CityID)
	if err != nil {
		return err
	}
	var total int64
	all := true
	for i, s := range v.Short {
		lack := s.Need - s.Have
		if sp, sh, ok := supplierOf(snap, city.Code, s.Component.Code); ok {
			stock, _, err := h.shelf(ctx, tx, city.ID, sp, sh, now)
			if err != nil {
				return err
			}
			if stock >= lack {
				v.Short[i].Source = screens.ShortFromSupplier
				total += lack * sh.Price
				continue
			}
		}
		all = false
		if comp, ok := snap.ComponentDef(s.Component.Code); ok && comp.Production != nil && hasCode(comp.Production.By, f.c.TypeCode) &&
			item.CanManufacture(comp.Component(), f.access) == nil {
			v.Short[i].Source = screens.ShortMadeHere
			continue
		}
		v.Short[i].Source = screens.ShortFromCompanies
	}
	if all {
		v.StockUp = total
	}
	return nil
}

// StockUp handles company.stockup: in one tap, every input an order of qty
// is short of, bought from the city's suppliers — all of them in one
// transaction, or none — and the order's plan shown ready to place.
func (h *ProductionHandler) StockUp(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	qty, ok := quantityArg(req.Qty)
	if !ok {
		req.Qty = ""
		return h.Produce(ctx, meta, req)
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ProduceView
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
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		t, err := h.target(ctx, tx, snap, f, req.Target)
		if err != nil {
			return err
		}
		if err := tx.Items().LockOrg(ctx, application.CompanyOrg(c.ID)); err != nil {
			return err
		}
		now := h.now()
		pl, err := h.planOrder(ctx, tx, snap, f, t, qty)
		if err != nil {
			return err
		}
		var bought money.Amount
		if fresh && pl.short != nil {
			city, err := h.cities.ByID(ctx, c.CityID)
			if err != nil {
				return err
			}
			back := []string{screens.AddrProduce, c.Code, t.good.TargetArg()}
			for _, s := range pl.short.Shortages {
				sp, sh, ok := supplierOf(snap, city.Code, s.Component)
				if !ok {
					return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(back...)
				}
				paid, err := h.supplyOnce(ctx, tx, snap, f, p, city, sp, sh, s.Need-s.Have, now, back...)
				if err != nil {
					return err
				}
				if bought, err = bought.Add(paid); err != nil {
					return errors.Internal(err)
				}
			}
			if pl, err = h.planOrder(ctx, tx, snap, f, t, qty); err != nil {
				return err
			}
		}
		view = h.produceView(snap, c, pl, qty, now)
		view.Bought = bought.Minor()
		return h.sourceShortages(ctx, tx, snap, f, &view, now)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Produce(h.screen(meta, lang), view), nil
}

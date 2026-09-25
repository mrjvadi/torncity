package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/production"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The next step (docs/adr/0021-production-economy.md section 14): the one
// thing a company's floor should do next, worked out from where it stands —
// never stored, never a quest to fall out of. In order:
//
//  1. no product yet: design a first, basic one (or finish the draft);
//  2. goods in the warehouse nobody is offered: sell them;
//  3. something it can make now: make a quick order of it — or, short of
//     inputs, buy them from the city's suppliers in one tap (or make the
//     component it is short of first, or buy it from other companies);
//  4. an order running: wait for it;
//  5. nothing more to make: research the next technology;
//  6. and design what that opened.
//
// Each step is shown with one button that does it.

// nextStep works out a company's next step, nil for a company that neither
// designs nor makes anything, or that has nothing left to do.
func (h *ProductionHandler) nextStep(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
	canResearch bool,
) (*screens.NextStep, error) {
	f, err := readFloor(ctx, tx, snap, c)
	if err != nil {
		return nil, err
	}
	f.readOnly = true
	made := snap.MadeBy(c.TypeCode)
	if len(f.def.Produces) == 0 && len(made) == 0 {
		return nil, nil
	}
	now := h.now()
	designs, err := tx.Production().Designs(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	var finals []application.Design
	var draft *application.Design
	designed := map[string]bool{}
	for i, d := range designs {
		designed[d.Item] = true
		switch {
		case d.Status == application.DesignFinal && f.def.Makes(d.Archetype):
			finals = append(finals, d)
		case d.Status == application.DesignDraft && draft == nil:
			draft = &designs[i]
		}
	}
	var st *stage
	stageNow := func() (*stage, error) {
		if st == nil {
			var err error
			st, err = h.stageOf(ctx, tx, snap, f)
			return st, err
		}
		return st, nil
	}

	// 1. A first product.
	if len(finals) == 0 && len(f.def.Produces) > 0 {
		if draft != nil {
			return &screens.NextStep{Kind: screens.StepDesignDraft, DesignNo: draft.No, DesignName: draft.Name}, nil
		}
		s, err := stageNow()
		if err != nil {
			return nil, err
		}
		ready, _, _, err := h.studioKinds(snap, f, s)
		if err != nil {
			return nil, err
		}
		if len(ready) > 0 {
			return &screens.NextStep{Kind: screens.StepDesignFirst}, nil
		}
	}

	// 2. Goods to sell.
	if step, err := h.sellStep(ctx, tx, snap, c); step != nil || err != nil {
		return step, err
	}

	// 3. Something to make, or its inputs to get.
	running, err := tx.Production().RunningOrders(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	var fallback *screens.NextStep
	if running < h.rules.MaxRunningOrders {
		var targets []orderTarget
		for _, d := range finals {
			if t, err := h.target(ctx, tx, snap, f, screens.DesignTarget(d.No)); err == nil {
				targets = append(targets, t)
			}
		}
		for _, comp := range made {
			if item.CanManufacture(comp.Component(), f.access) != nil {
				continue
			}
			if t, err := h.target(ctx, tx, snap, f, comp.Code); err == nil {
				targets = append(targets, t)
			}
		}
		for _, t := range targets {
			step, err := h.chainStep(ctx, tx, snap, f, t, 0, true, now)
			if err != nil {
				return nil, err
			}
			switch {
			case step == nil:
			case step.Kind == screens.StepBuyGoods:
				if fallback == nil {
					fallback = step
				}
			default:
				return step, nil
			}
		}
	}

	// 4. An order running.
	if running > 0 {
		orders, err := tx.Production().Orders(ctx, c.ID, 10)
		if err != nil {
			return nil, err
		}
		for _, o := range orders {
			if o.Status == application.OrderRunning {
				line, err := h.orderLine(ctx, tx, snap, o, now)
				if err != nil {
					return nil, err
				}
				return &screens.NextStep{Kind: screens.StepProducing, Good: line.Good, Qty: line.Output,
					FinishAt: line.FinishAt, Left: line.Left}, nil
			}
		}
	}
	if fallback != nil {
		return fallback, nil
	}

	// 5 and 6. Grow: research what opens the next good, then design it.
	s, err := stageNow()
	if err != nil {
		return nil, err
	}
	ready, next, _, err := h.studioKinds(snap, f, s)
	if err != nil {
		return nil, err
	}
	for _, k := range next {
		for _, stp := range k.Steps {
			if stp.Research {
				return &screens.NextStep{Kind: screens.StepResearch, Item: k.Item, Tech: stp.Tech,
					CanResearch: canResearch}, nil
			}
		}
	}
	for _, k := range ready {
		if !designed[k.Code] {
			return &screens.NextStep{Kind: screens.StepDesignNext, Item: k}, nil
		}
	}
	return nil, nil
}

// sellStep is the first good in the warehouse not yet offered for sale, if
// the company does not sell it to the city on its own (stocked).
func (h *ProductionHandler) sellStep(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
) (*screens.NextStep, error) {
	stocked := snap.StockedCodes(c.TypeCode)
	stacks, pieces, err := tx.Items().OrgHoldings(ctx, application.CompanyOrg(c.ID), application.HoldWarehouse)
	if err != nil {
		return nil, err
	}
	for _, p := range pieces {
		if stocked.Has(p.Item) || !h.sellable(snap, p.Item) {
			continue
		}
		good := screens.Good{Item: itemNamed(snap, p.Item)}
		if p.DesignID != "" {
			d, err := tx.Production().DesignByID(ctx, p.DesignID)
			if err != nil {
				return nil, err
			}
			good = designGood(snap, *d)
		}
		return &screens.NextStep{Kind: screens.StepSell, Good: good}, nil
	}
	for _, s := range stacks {
		if _, component := snap.ComponentDef(s.Item); component || stocked.Has(s.Item) || !h.sellable(snap, s.Item) {
			continue
		}
		return &screens.NextStep{Kind: screens.StepSell, Good: screens.Good{Item: itemNamed(snap, s.Item)}, Qty: s.Qty}, nil
	}
	return nil, nil
}

// chainStep is the step that gets a target made: make it now, buy its inputs
// from the suppliers, make the component it is short of first (one level
// down), or buy that from other companies. qty zero plans a quick order;
// quick false keeps the size asked. nil when the target cannot be planned.
func (h *ProductionHandler) chainStep(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor, t orderTarget,
	qty int64, top bool, now time.Time,
) (*screens.NextStep, error) {
	plan := func(q int64) (plan, bool, error) {
		p, err := h.planOrder(ctx, tx, snap, f, t, q)
		var r *productionRefusal
		if stderrors.As(err, &r) {
			return p, false, nil
		}
		return p, err == nil, err
	}
	if qty == 0 {
		probe, ok, err := plan(0)
		if !ok || err != nil {
			return nil, err
		}
		qty = h.quickQty(probe.maxOrder())
	}
	pl, ok, err := plan(qty)
	if !ok || err != nil {
		return nil, err
	}
	if pl.short == nil {
		return &screens.NextStep{Kind: screens.StepProduce, Good: t.good, Qty: qty, Batch: t.batch}, nil
	}
	view := screens.ProduceView{Short: shortagesOf(snap, pl.short)}
	if err := h.sourceShortages(ctx, tx, snap, f, &view, now); err != nil {
		return nil, err
	}
	if view.StockUp > 0 {
		return &screens.NextStep{Kind: screens.StepSupply, Good: t.good, Qty: qty, Total: view.StockUp}, nil
	}
	var buy *screens.Shortage
	for i, s := range view.Short {
		switch s.Source {
		case screens.ShortMadeHere:
			if !top {
				continue
			}
			sub, err := h.target(ctx, tx, snap, f, s.Component.Code)
			if err != nil {
				continue
			}
			batch := max(sub.batch, 1)
			need := (s.Need - s.Have + batch - 1) / batch
			step, err := h.chainStep(ctx, tx, snap, f, sub, max(need, 1), false, now)
			if err != nil || (step != nil && step.Kind != screens.StepBuyGoods) {
				return step, err
			}
		case screens.ShortFromCompanies:
			if buy == nil {
				buy = &view.Short[i]
			}
		}
	}
	if buy == nil {
		return nil, nil
	}
	return &screens.NextStep{Kind: screens.StepBuyGoods, Good: t.good, Component: buy.Component}, nil
}

// shortagesOf names a plan's shortages for a screen.
func shortagesOf(snap *content.Snapshot, short *production.ShortageError) []screens.Shortage {
	out := make([]screens.Shortage, 0, len(short.Shortages))
	for _, s := range short.Shortages {
		comp, _ := snap.ComponentDef(s.Component)
		out = append(out, screens.Shortage{Component: named(comp.Code, comp.Name), Need: s.Need, Have: s.Have})
	}
	return out
}

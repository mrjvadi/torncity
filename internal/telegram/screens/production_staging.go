package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Staging (docs/adr/0021-production-economy.md section 14): what a company
// can do now, the very next thing it could unlock and how, and the one step
// its floor should take next.

// TechStep is a technology one step away, and how the company takes it:
// research it, or buy a license for it.
type TechStep struct {
	Tech     Named
	Research bool
}

// StudioKind is a good one step away from the studio.
type StudioKind struct {
	Item  Named
	Steps []TechStep
}

// LockedTarget is a component one step away from the floor.
type LockedTarget struct {
	Good  Good
	Steps []TechStep
}

// andList joins names, the last with the language's "and".
func (c Context) andList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return c.list(names[:len(names)-1]) + c.T("production.list_and", nil) + names[len(names)-1]
}

// unlockHint says how a locked thing opens: the technologies to research,
// and those whose license to buy.
func (c Context) unlockHint(steps []TechStep) string {
	var research, license []string
	for _, s := range steps {
		if s.Research {
			research = append(research, c.TechName(s.Tech))
		} else {
			license = append(license, c.TechName(s.Tech))
		}
	}
	switch {
	case len(research) > 0 && len(license) > 0:
		return c.T("production.unlock.both", map[string]any{"research": c.andList(research), "license": c.andList(license)})
	case len(license) > 0:
		return c.T("production.unlock.license", map[string]any{"techs": c.andList(license)})
	}
	return c.T("production.unlock.research", map[string]any{"techs": c.andList(research)})
}

// Next step kinds: the one thing a company's floor should do next.
const (
	StepDesignFirst = "design_first"
	StepDesignDraft = "design_draft"
	StepSell        = "sell"
	StepProduce     = "produce"
	StepProducing   = "producing"
	StepSupply      = "supply"
	StepBuyGoods    = "buy_goods"
	StepResearch    = "research"
	StepDesignNext  = "design_next"
)

// NextStep is the one step a company's floor should take next, worked out
// from where it stands: design a first product, buy its inputs, make it,
// sell it, research further, design something more advanced.
type NextStep struct {
	Kind string
	// Good is what the step is about: the product to make, sell or buy
	// inputs for; Qty how many (batches of a component, which yields Batch
	// units each); Total what the inputs cost.
	Good  Good
	Qty   int64
	Batch int64
	Total int64
	// Component is the input other companies make, for buy_goods.
	Component Named
	// Item is a good to design; Tech a technology to research for it.
	Item Named
	Tech Named
	// DesignNo and DesignName are the draft to finish.
	DesignNo   int64
	DesignName string
	// FinishAt and Left are when the running order is done.
	FinishAt time.Time
	Left     time.Duration
	// CanResearch is the owner, who alone runs the lab.
	CanResearch bool
}

// nextStep renders the next step's paragraph and its one primary button,
// added to kb as its own row.
func (c Context) nextStep(kb *keyboards.Builder, company string, s *NextStep) string {
	if s == nil {
		return ""
	}
	good := c.GoodName(s.Good)
	args := map[string]any{"good": good, "qty": FormatNumber(c, s.Qty*max(s.Batch, 1)), "total": FormatMoney(c, s.Total),
		"component": c.ComponentName(s.Component), "item": c.ItemName(s.Item), "tech": c.TechName(s.Tech),
		"name": s.DesignName, "time": FormatClock(c, s.FinishAt), "duration": FormatDuration(c, s.Left)}
	if s.DesignName == "" {
		args["name"] = c.T("production.design_unnamed", map[string]any{"no": FormatNumber(c, s.DesignNo)})
	}
	var btn presenter.Button
	ok := false
	switch s.Kind {
	case StepDesignFirst:
		btn, ok = keyboards.Button(c.T("production.step_button.design_first", nil), AddrStudio, company)
	case StepDesignDraft:
		btn, ok = keyboards.Button(c.T("production.step_button.design_draft", args), AddrDesign, strconv.FormatInt(s.DesignNo, 10))
	case StepSell:
		btn, ok = keyboards.Button(c.T("production.step_button.sell", args), AddrSell, company, s.Good.target())
	case StepProduce:
		btn, ok = keyboards.Button(c.T("production.step_button.produce", args), AddrProduce, company, s.Good.target(),
			strconv.FormatInt(s.Qty, 10), ProductionConfirm)
	case StepProducing:
		btn, ok = keyboards.Button(c.T("production.button.orders", nil), AddrOrders, company)
	case StepSupply:
		btn, ok = keyboards.Button(c.T("production.step_button.supply", args), AddrStockUp, company, s.Good.target(),
			strconv.FormatInt(s.Qty, 10))
	case StepBuyGoods:
		btn, ok = keyboards.Button(c.T("production.button.goods", nil), AddrCompanyGoods)
	case StepResearch:
		if s.CanResearch {
			btn, ok = keyboards.Button(c.T("production.step_button.research", args), AddrLab, company, s.Tech.Code)
		}
	case StepDesignNext:
		btn, ok = keyboards.Button(c.T("production.step_button.design_next", args), AddrDesignNew, company, s.Item.Code)
	}
	if ok {
		kb.Row(btn)
	}
	return body(c.T("production.step_title", nil), c.T("production.step."+s.Kind, args))
}

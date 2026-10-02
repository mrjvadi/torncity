package screens

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Staging (docs/adr/0021-production-economy.md section 14): what a company
// can do now, the very next thing it could unlock and how, and the one step
// its floor should take next.

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
		btn, ok = keyboards.Button(c.T("production.step_button.sell", args), AddrSell, company, s.Good.TargetArg())
	case StepProduce:
		btn, ok = keyboards.Button(c.T("production.step_button.produce", args), AddrProduce, company, s.Good.TargetArg(),
			strconv.FormatInt(s.Qty, 10), ProductionConfirm)
	case StepProducing:
		btn, ok = keyboards.Button(c.T("production.button.orders", nil), AddrOrders, company)
	case StepSupply:
		btn, ok = keyboards.Button(c.T("production.step_button.supply", args), AddrStockUp, company, s.Good.TargetArg(),
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

package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
)

// companyRules is the companies' tuning from the configuration: config
// company, and the bank's bounds on one amount of money moved.
func companyRules(c config.Company, limits bank.Limits) handlers.CompanyRules {
	return handlers.CompanyRules{
		Period: c.Period, MaxPerPlayer: c.MaxPerPlayer, NameMin: c.NameMinLength, NameMax: c.NameMaxLength,
		FoundingShares: c.FoundingShares, InsolvencyPeriods: c.InsolvencyPeriods, NPCCityPeriodCap: c.NPCCityPeriodCap,
		MaxOpenings: c.MaxOpenings, PriceStepBPS: c.PriceStepBPS, Limits: limits,
		Citizens:              citizenRules(c),
		CitizenLabourShareBPS: c.CitizenLabourShareBPS,
	}
}

// bindCompanies maps the company commands to their handler. bind merges it
// into the one table bindAll checks against the subscriptions.
func (h phaseHandlers) bindCompanies() map[string]commandFunc {
	c := h.companies
	return map[string]commandFunc{
		"company.list":     bare(c.List),
		"company.view":     decoded(c.View),
		"company.register": bare(c.Register),
		"company.type":     decoded(c.Type),
		"company.found":    decoded(c.Found),
		"company.mine":     bare(c.Mine),
		"company.manage":   decoded(c.Manage),
		"company.deposit":  decoded(c.Deposit),
		"company.withdraw": decoded(c.Withdraw),
		"company.price":    decoded(c.Price),
		"company.auto":     decoded(c.Auto),
		"company.manager":  decoded(c.Manager),
		"company.close":    decoded(c.Close),
		"company.openings": decoded(c.Openings),
		"company.post":     decoded(c.Post),
		"company.slots":    decoded(c.Slots),
		"company.staff":    decoded(c.Staff),
		"company.decide":   decoded(c.Decide),
		"company.fire":     decoded(c.Fire),
		"company.opening":  decoded(c.Opening),
		"company.apply":    decoded(c.Apply),
		// The scheduler's: a city's company period ending.
		"company.settle": decoded(c.Settle),
	}
}

// productionRules is the production economy's tuning from the
// configuration: config company, and the bank's bounds on one amount.
func productionRules(c config.Company, limits bank.Limits) handlers.ProductionRules {
	return handlers.ProductionRules{
		MaxRunningOrders: c.MaxRunningOrders, MaxDesigns: c.MaxDesigns, MaxListings: c.MaxListings,
		DesignMinSkill: c.DesignMinSkill, ReverseTime: c.ReverseTime, NameMin: c.NameMinLength, NameMax: c.NameMaxLength,
		Limits: limits, Citizens: citizenRules(c),
	}
}

// citizenRules is the tuning of citizen labour from config company.
func citizenRules(c config.Company) company.CitizenRules {
	return company.CitizenRules{ShiftsPerPeriod: c.CitizenShiftsPerPeriod, ProductivityBPS: c.CitizenProductivityBPS}
}

// bindProduction maps the production economy's commands to their handler.
func (h phaseHandlers) bindProduction() map[string]commandFunc {
	p := h.production
	return map[string]commandFunc{
		"company.warehouse": decoded(p.Warehouse),
		"company.suppliers": decoded(p.Suppliers),
		"company.supply":    decoded(p.Supply),
		"company.lab":       decoded(p.Lab),
		"company.research":  decoded(p.Research),
		"company.techmode":  decoded(p.TechMode),
		"company.license":   decoded(p.License),
		"company.studio":    decoded(p.Studio),
		"company.dnew":      decoded(p.DesignNew),
		"company.design":    decoded(p.Design),
		"company.dfill":     decoded(p.DesignFill),
		"company.dqty":      decoded(p.DesignQty),
		"company.dname":     decoded(p.DesignName),
		"company.dfinal":    decoded(p.DesignFinal),
		"company.produce":   decoded(p.Produce),
		"company.orders":    decoded(p.Orders),
		"company.relab":     decoded(p.ReverseLab),
		"company.reverse":   decoded(p.Reverse),
		"company.sell":      decoded(p.Sell),
		"company.listings":  decoded(p.Listings),
		"company.unlist":    decoded(p.Unlist),
		"company.goods":     bare(p.Goods),
		"company.buy":       decoded(p.Buy),
		// The scheduler's: a research, an order, a reverse engineering done.
		"company.researched": decoded(p.Researched),
		"company.produced":   decoded(p.Produced),
		"company.reversed":   decoded(p.Reversed),
	}
}

package main

import (
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/bank"
)

// companyRules is the companies' tuning from the configuration: config
// company, and the bank's bounds on one amount of money moved.
func companyRules(c config.Company, limits bank.Limits) handlers.CompanyRules {
	return handlers.CompanyRules{
		Period: c.Period, MaxPerPlayer: c.MaxPerPlayer, NameMin: c.NameMinLength, NameMax: c.NameMaxLength,
		FoundingShares: c.FoundingShares, InsolvencyPeriods: c.InsolvencyPeriods, NPCCityPeriodCap: c.NPCCityPeriodCap,
		MaxOpenings: c.MaxOpenings, PriceStepBPS: c.PriceStepBPS, Limits: limits,
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

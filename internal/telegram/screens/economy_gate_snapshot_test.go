package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The "not available here" state of the economy screens
// (testdata/snapshots/<language>/economy_gate.txt): what a player in a village
// reads for a service that starts at a larger stage, and the nearest place
// that has it.
func init() { snapshotAreas["economy_gate"] = economyGateSnapshots }

func economyGateSnapshots(c Context, _ people, add func(string, *presenter.Response)) {
	support := &Named{Code: "brennhaven", Name: "Brennhaven"}
	loans := &economy.Unavailable{Service: "loans", Stage: "city", Here: "village",
		Requires: []economy.NeedBuilding{{Code: "bank"}}, Nearest: support}
	add("Not here, bank counters · loans in a village", FinanceHub(c, FinanceHubView{Unavailable: loans}))
	add("Not here, bank counters · savings in a village", Savings(c, SavingsView{Unavailable: &economy.Unavailable{
		Service: "savings", Stage: "city", Here: "village", Requires: loans.Requires, Nearest: support}}))
	add("Not here, bank counters · insurance in a town", Insurance(c, InsuranceView{Unavailable: &economy.Unavailable{
		Service: "insurance", Stage: "city", Here: "town", Requires: loans.Requires, Nearest: support}}))
	add("Not here, markets of money · the exchange in a village", Exchange(c, ExchangeView{Unavailable: &economy.Unavailable{
		Service: "stocks", Stage: "city", Here: "village", Requires: loans.Requires, Nearest: support}}))
	add("Not here, markets of money · gold in a village", Gold(c, GoldView{Unavailable: &economy.Unavailable{
		Service: "gold", Stage: "city", Here: "village", Requires: loans.Requires, Nearest: support}}))
	add("Not here, the auction house · auctions, only in the central city", Auctions(c, AuctionsView{Unavailable: &economy.Unavailable{
		Service: "auction_house", Stage: "support", Here: "town", Nearest: support}}))
	add("Not here, markets of money · nowhere known to go", Gold(c, GoldView{Unavailable: &economy.Unavailable{
		Service: "gold", Stage: "city", Here: "village"}}))
}

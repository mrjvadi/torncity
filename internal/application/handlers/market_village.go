package handlers

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/market"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The village book (storage and market audit F7, F8, F15, P3; docs/adr/0040
// section 5): a founded settlement's market has stalls, a listing fee and dues,
// the last two to the settlement's treasury (not the sink) at rates its market
// warden sets within bounds, and a market day when the settlement has
// researched the periodic market.
//
//   - WHO WORKS THERE. The market warden, an office the mayor appoints (the real
//     muhtasib: weights, dues, honest dealing); the stall owners themselves.
//     A stall sells for its owner only while the owner is in the settlement; a
//     hired seller who keeps it open while the owner is away needs the stall
//     keeper hire of ADR 0040 Part B, which is not built (documented gap).
//   - WHAT IT CONSUMES. Stalls: a market post gives VillageStallsPost, a market
//     hall VillageStallsHall; every open order takes one while it rests.
//   - WHAT IT PROVIDES. The book where residents trade with their own money; the
//     treasury's income: the listing fee when an order takes a stall, the dues on
//     every trade.
//   - HOW IT LINKS. Dues come out of the buyer's escrow with the seller's
//     proceeds; a market day halves the dues and waives the listing fee.

// VillageMarketRules is the tuning of the village book (config trade.village_*,
// trade.market_day_every_days) and the game clock market days are counted on.
type VillageMarketRules struct {
	StallsPost, StallsHall                 int
	StallsPerPlayerPost, StallsPerPlayerHall int
	DayEveryDays                           int
	Clock                                  gametime.Clock
}

// WithVillageBook gives the market the village book's stalls, fees and market day.
func (h *MarketHandler) WithVillageBook(r VillageMarketRules) *MarketHandler {
	h.village = r
	return h
}

// The levers of the village book are set by the market warden within bounds, one
// pair for each level a settlement can be (village, town, city).
func bookLevers(city *application.City) (listing, dues string) {
	level := "city"
	if t := tierStage(city.Tier); t == content.StageVillage || t == content.StageTown {
		level = t
	}
	return level + ".market_listing_fee_bps", level + ".market_dues_bps"
}

// villageBook is what a settlement's book is at this moment.
type villageBook struct {
	// active: the city is a founded settlement, not the neutral city or a content
	// city; everything below applies only then.
	active bool
	// Stalls is the book's room, PerPlayer one player's share of it.
	Stalls, PerPlayer int
	// ListingBPS and DuesBPS are the warden's rates today, MarketDay whether the
	// settlement is holding its market day (listing fee waived, dues halved).
	ListingBPS, DuesBPS int64
	MarketDay           bool
}

// villageBookOf reads the village book of a city, inactive for any other.
func (h *MarketHandler) villageBookOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City,
	now time.Time,
) (villageBook, error) {
	var vb villageBook
	if city.Tier == "" {
		return vb, nil
	}
	here, err := judgeSettlementOf(ctx, tx, snap, city, h.home)
	if err != nil {
		return vb, err
	}
	if here.content || here.neutral || here.stands == nil {
		return vb, nil
	}
	vb.active = true
	rows, err := tx.SettlementBuildings().List(ctx, city.ID)
	if err != nil {
		return vb, err
	}
	var posts, halls int
	for _, b := range rows {
		if b.Status != "complete" {
			continue
		}
		switch b.TypeCode {
		case "barter_post":
			posts++
		case "market":
			halls++
		}
	}
	vb.Stalls = posts*h.village.StallsPost + halls*h.village.StallsHall
	vb.PerPlayer = h.village.StallsPerPlayerPost
	if halls > 0 {
		vb.PerPlayer = h.village.StallsPerPlayerHall
	}
	listingLever, duesLever := bookLevers(city)
	listing, err := h.policy.Get(ctx, city.JurisdictionID, listingLever)
	if err != nil {
		return vb, err
	}
	dues, err := h.policy.Get(ctx, city.JurisdictionID, duesLever)
	if err != nil {
		return vb, err
	}
	vb.ListingBPS, vb.DuesBPS = listing.Value, dues.Value
	if h.village.DayEveryDays > 0 && h.village.Clock.Validate() == nil {
		owned, err := tx.SettlementKnowledge().Owned(ctx, city.ID)
		if err != nil {
			return vb, err
		}
		for _, o := range owned {
			if o.Code == "periodic_market" && h.village.Clock.DayAt(now)%int64(h.village.DayEveryDays) == 0 {
				vb.MarketDay = true
				vb.ListingBPS, vb.DuesBPS = 0, vb.DuesBPS/2
			}
		}
	}
	return vb, nil
}

// listingFee is the fee an order of this value pays to take a stall: a share of
// its value, at least 1 when the rate is not zero.
func (vb villageBook) listingFee(notional int64) int64 {
	if !vb.active || vb.ListingBPS <= 0 {
		return 0
	}
	return max(notional*vb.ListingBPS/10_000, 1)
}

// view is the village book as the market screens show it.
func (vb villageBook) view(used, mine int) *economy.VillageBookView {
	if !vb.active {
		return nil
	}
	return &economy.VillageBookView{Stalls: vb.Stalls, StallsUsed: used, PerPlayer: vb.PerPlayer, Mine: mine,
		ListingBPS: vb.ListingBPS, DuesBPS: vb.DuesBPS, MarketDay: vb.MarketDay}
}

// payListingFee takes the listing fee from the player's purse to the settlement's
// treasury. A buy pays with the method it chose; a sell with the first purse that
// covers it.
func (h *MarketHandler) payListingFee(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City,
	playerID string, method payment.Method, fee int64, orderID string, now time.Time,
) error {
	if fee <= 0 {
		return nil
	}
	wallet, err := application.OpenWallet(ctx, tx.Ledger(), playerID)
	if err != nil {
		return err
	}
	amount := money.FromMinor(fee)
	plan := wallet.Plan(amount, snap.Accepts(content.ServiceMarket))
	if method == "" || !plan.Accepted.Has(method) || !wallet.Balances().Covers(method, amount) {
		if len(plan.Usable) == 0 {
			return declined(plan, wallet, "market.button.market", economy.AddrMarket)
		}
		method = plan.Usable[0]
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
	if err != nil {
		return err
	}
	_, err = wallet.Pay(ctx, tx.Ledger(), application.Charge{
		Method: method, Accepted: plan.Accepted, Reason: application.ReasonVillageMarketListingFee,
		ReferenceType: "market_orders", ReferenceID: orderID,
		To: []application.LedgerEntry{{AccountID: treasury.ID, Amount: amount}}, CreatedAt: now,
	})
	return err
}

// awayAsks are the resting asks whose owner is not in the settlement: their stall
// is shut, so they do not sell (ADR 0040 Part B; the seller a stall may hire to
// keep it open is not built yet).
func (h *MarketHandler) awayAsks(ctx context.Context, tx application.Tx, cityID string, resting []application.MarketOrder) (map[string]bool, error) {
	away := map[string]bool{}
	var ids []string
	seen := map[string]bool{}
	for _, o := range resting {
		if o.Side == string(market.Sell) && !seen[o.OwnerID] {
			seen[o.OwnerID] = true
			ids = append(ids, o.OwnerID)
		}
	}
	if len(ids) == 0 {
		return away, nil
	}
	facts, err := tx.Presence().Facts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if facts[id].CityID != cityID {
			away[id] = true
		}
	}
	return away, nil
}

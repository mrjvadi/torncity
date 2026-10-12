package handlers

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/law"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The hidden board (docs/adr/0078 3.8). It is a market with a higher margin and a higher chance of losing, not a free shop: it is visible only to
// those with standing, its goods carry a premium, a trade between players goes through a broker's escrow that may fail, and every trade may be noticed.
// Only goods that exist in the game trade on it.
//
//   - WHO WORKS THERE. The NPC fences (the daily supply, items that no shop sells) and the brokers (players who hold others' money against goods).
//   - WHAT IT CONSUMES. A listing fee (to the sink), the broker's fee, standing earned by completed trades.
//   - WHAT IT PRODUCES. A good delivered to the buyer, dirty money to the seller; a bust that freezes the listing and opens a case.
//   - WHAT BREAKS. A broker can leave with the pot (the exit scam): the buyer loses, the broker's name falls, and the scam may be traced.

// lawHasBuildingRole says a standing complete building of the settlement fills a role at a tier or higher.
func lawHasBuildingRole(snap *content.Snapshot, buildings []application.SettlementBuildingInstance, role string, tier int) bool {
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok && d.Role == role && d.Tier >= tier {
			return true
		}
	}
	return false
}

// lawSupplyHeld says the settlement has what a supply row requires: the research and the buildings.
func (h *VillageHandler) lawSupplyHeld(ctx context.Context, tx application.Tx, lc *lawCtx, d content.ShadowSupplyDef, buildings []application.SettlementBuildingInstance) (bool, error) {
	if d.Requires == nil {
		return true, nil
	}
	if missing, err := knowledgeMissing(ctx, tx, lc.s.CityID, d.Requires.Knowledge); err != nil || len(missing) > 0 {
		return false, err
	}
	for _, b := range d.Requires.Buildings {
		switch {
		case b.Role != "":
			if !lawHasBuildingRole(lc.snap, buildings, b.Role, max(b.Tier, 1)) {
				return false, nil
			}
		case b.Code != "":
			found := false
			for _, x := range buildings {
				found = found || (x.TypeCode == b.Code && x.Status == "complete")
			}
			if !found {
				return false, nil
			}
		}
	}
	return true, nil
}

// lawSupply puts the day's goods of the NPC fences on the board. The fence is the unique index of (settlement, day, good): any replica may try, one wins.
func (h *VillageHandler) lawSupply(ctx context.Context, tx application.Tx, lc *lawCtx) error {
	if !lc.has || !h.blackOpen() {
		return nil
	}
	buildings, err := tx.SettlementBuildings().List(ctx, lc.s.CityID)
	if err != nil {
		return err
	}
	day := h.lawDay(lc)
	for _, d := range lc.snap.ShadowSupply() {
		if lawMatureHidden(lc.mature, d.Mature) {
			continue
		}
		held, err := h.lawSupplyHeld(ctx, tx, lc, d, buildings)
		if err != nil {
			return err
		}
		if !held {
			continue
		}
		def, ok := lc.snap.ItemDef(d.Item)
		if !ok || def.BasePrice <= 0 {
			continue
		}
		dd := day
		if _, err := tx.Law().InsertListing(ctx, application.LawListing{ID: h.ids.NewID(), SettlementID: lc.s.CityID, Item: d.Item, Qty: d.Daily,
			Price: law.BlackPrice(def.BasePrice, d.PremiumBPS), Mature: d.Mature, SupplyDay: &dd, CreatedAt: lc.now}); err != nil {
			return err
		}
	}
	return nil
}

// lawStanding is the standing of a player on the board; false when he has none yet.
func lawStanding(ctx context.Context, tx application.Tx, player string) (int64, bool, error) {
	return tx.Law().Standing(ctx, player)
}

// lawAsk is asking around for a first contact. The answer is a seeded roll of the player and the day, so asking again the same day gives the same
// answer; a newcomer who is told gets the introductory standing. Nobody is told anything for sure.
func (h *VillageHandler) lawAsk(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if !lc.has {
		return "", lawRefuse(village.LawNoSettlement)
	}
	if _, ok, err := lawStanding(ctx, tx, lc.p.ID); err != nil {
		return "", err
	} else if ok {
		return village.LawNoticeAlreadyKnown, nil
	}
	if !law.Hit(lc.t.AskBPS, law.Roll(lc.p.ID+"|ask|"+itoa(h.lawDay(lc)))) {
		return "", lawRefuse(village.LawNobodyTalks)
	}
	return village.LawNoticeContact, tx.Law().AddStanding(ctx, lc.p.ID, "", lc.t.StandingIntro, lc.now)
}

// lawIntroduce is a member of the board vouching for a newcomer: he gets the introductory standing. The introducer needs standing himself.
func (h *VillageHandler) lawIntroduce(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if st, ok, err := lawStanding(ctx, tx, lc.p.ID); err != nil {
		return "", err
	} else if !ok || st < lc.t.StandingIntro {
		return "", lawRefuse(village.LawNoStanding)
	}
	other, err := lawPlayerByCode(ctx, tx, lc.req.Player)
	if err != nil {
		return "", err
	}
	if other == lc.p.ID {
		return "", lawRefuse(village.LawSelf)
	}
	if _, ok, err := lawStanding(ctx, tx, other); err != nil {
		return "", err
	} else if ok {
		return "", lawRefuse(village.LawAlreadyInformant)
	}
	return village.LawNoticeContact, tx.Law().AddStanding(ctx, other, lc.p.ID, lc.t.StandingIntro, lc.now)
}

// lawBroker makes a player with standing a broker of escrows.
func (h *VillageHandler) lawBroker(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if _, ok, err := lawStanding(ctx, tx, lc.p.ID); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoStanding)
	}
	return village.LawNoticeBroker, tx.Law().UpsertBroker(ctx, lc.p.ID, lc.now)
}

// goodsOf finds the pieces or the stack count a player holds of an item in a holding.
func lawGoods(ctx context.Context, tx application.Tx, player, holding, item string) (stack int64, pieces []application.Piece, err error) {
	stacks, ps, err := tx.Items().Holdings(ctx, player, holding)
	if err != nil {
		return 0, nil, err
	}
	for _, s := range stacks {
		if s.Item == item {
			stack += s.Qty
		}
	}
	for _, p := range ps {
		if p.Item == item {
			pieces = append(pieces, p)
		}
	}
	sort.Slice(pieces, func(i, j int) bool { return pieces[i].Serial < pieces[j].Serial })
	return stack, pieces, nil
}

// lawHeld is how many units of an item a player holds in a holding (a stack, or the pieces of a unique good).
func lawHeld(ctx context.Context, tx application.Tx, player, holding, item string) (int64, error) {
	s, ps, err := lawGoods(ctx, tx, player, holding, item)
	return s + int64(len(ps)), err
}

// lawMoveGoods moves qty units of an item from a player's holding to another player's holding: a stack as one move, a unique good piece by piece.
func (h *VillageHandler) lawMoveGoods(ctx context.Context, tx application.Tx, lc *lawCtx, from, fromHold, to, toHold, item string, qty int64,
	reason application.ItemReason, refType, refID string,
) error {
	if qty <= 0 {
		return nil
	}
	if err := tx.Items().LockOwner(ctx, from); err != nil {
		return err
	}
	if to != from {
		if err := tx.Items().LockOwner(ctx, to); err != nil {
			return err
		}
	}
	stack, pieces, err := lawGoods(ctx, tx, from, fromHold, item)
	if err != nil {
		return err
	}
	if stack >= qty {
		return tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: item, Qty: qty, From: from, FromHolding: fromHold, To: to, ToHolding: toHold,
			Reason: reason, ReferenceType: refType, ReferenceID: refID, At: lc.now})
	}
	if int64(len(pieces)) < qty {
		return lawRefuse(village.LawNoGoods)
	}
	for _, p := range pieces[:qty] {
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: item, PieceID: p.ID, Qty: 1, From: from, FromHolding: fromHold, To: to,
			ToHolding: toHold, Reason: reason, ReferenceType: refType, ReferenceID: refID, At: lc.now}); err != nil {
			return err
		}
	}
	return nil
}

// lawList puts a player's goods on the board. The goods are set aside from his bags until sold or taken back; the listing costs a fee.
func (h *VillageHandler) lawList(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if !lc.has {
		return "", lawRefuse(village.LawNoSettlement)
	}
	if _, ok, err := lawStanding(ctx, tx, lc.p.ID); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoStanding)
	}
	item := strings.TrimSpace(lc.req.Code)
	def, ok := lc.snap.ItemDef(item)
	if !ok {
		return "", lawRefuse(village.LawNoGoods)
	}
	qty, price := lc.req.num(lc.req.Qty), lc.req.num(lc.req.Amount)
	if qty < 1 || price < 1 {
		return "", lawRefuse(village.LawBadArgs)
	}
	if banned, err := tx.Law().Banned(ctx, lc.p.ID, BanMarket, lc.now); err != nil {
		return "", err
	} else if banned {
		return "", lawRefuse(village.LawBanned)
	}
	_ = def
	sink, err := lawSink(ctx, tx)
	if err != nil {
		return "", err
	}
	if err := lawPay(ctx, tx, lc, lc.p.ID, lc.t.ListingFee, application.ReasonBlackListingFee, application.LawEscrowReference, lc.p.ID, sink.ID); err != nil {
		return "", err
	}
	id := h.ids.NewID()
	if err := h.lawMoveGoods(ctx, tx, lc, lc.p.ID, application.HoldCarried, lc.p.ID, application.HoldEscrow, item, qty, application.ItemMarketEscrow, application.LawEscrowReference, id); err != nil {
		return "", err
	}
	if _, err := tx.Law().InsertListing(ctx, application.LawListing{ID: id, SettlementID: lc.s.CityID, SellerID: lc.p.ID, Item: item, Qty: qty, Price: price, CreatedAt: lc.now}); err != nil {
		return "", err
	}
	return village.LawNoticeListed, nil
}

// lawDelist takes a seller's open listing off the board and gives him his goods back.
func (h *VillageHandler) lawDelist(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	l, err := tx.Law().ListingByID(ctx, strings.TrimSpace(lc.req.ID), true)
	if err != nil {
		return "", err
	}
	if l == nil || l.SellerID != lc.p.ID || !lc.has || l.SettlementID != lc.s.CityID {
		return "", lawRefuse(village.LawNoListing)
	}
	if ok, err := tx.Law().SetListing(ctx, l.ID, []string{"open"}, "cancelled", lc.now); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoListing)
	}
	return village.LawNoticeDelisted, h.lawMoveGoods(ctx, tx, lc, l.SellerID, application.HoldEscrow, l.SellerID, application.HoldCarried, l.Item, l.Qty,
		application.ItemMarketRelease, application.LawEscrowReference, l.ID)
}

// lawBuy buys from the board. A fence's good is paid for at once (the money leaves the economy); a player's goes through a broker's escrow. Either way
// the trade may be noticed: a bust is a seeded roll that grows with the settlement's patrol that day and falls with a bribe of the buyer on the
// detection rung. A bust freezes the listing, seizes the goods and opens a case against the seller (or, for a fence's good, the buyer).
func (h *VillageHandler) lawBuy(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	if !lc.has {
		return "", lawRefuse(village.LawNoSettlement)
	}
	st, ok, err := lawStanding(ctx, tx, lc.p.ID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", lawRefuse(village.LawNoStanding)
	}
	l, err := tx.Law().ListingByID(ctx, strings.TrimSpace(lc.req.ID), true)
	if err != nil {
		return "", err
	}
	if l == nil || l.SettlementID != lc.s.CityID || l.Status != "open" || lawMatureHidden(lc.mature, l.Mature) {
		return "", lawRefuse(village.LawNoListing)
	}
	if l.SellerID == lc.p.ID {
		return "", lawRefuse(village.LawSelf)
	}
	if banned, err := tx.Law().Banned(ctx, lc.p.ID, BanMarket, lc.now); err != nil {
		return "", err
	} else if banned {
		return "", lawRefuse(village.LawBanned)
	}
	if d, ok := lc.snap.ShadowSupplyOf(l.Item); ok && l.SellerID == "" && st < d.MinStanding {
		return "", lawRefuse(village.LawNoStanding)
	}
	qty := l.Qty
	if q := lc.req.num(lc.req.Qty); q > 0 && q < l.Qty && l.SellerID == "" {
		qty = q
	}
	total := l.Price * qty
	patrol, err := h.lawPatrolToday(ctx, tx, lc)
	if err != nil {
		return "", err
	}
	// the chance this trade is noticed; a bribe of the buyer on the detection rung lowers it, never to nothing
	chance := lc.t.BustBaseBPS + patrol*lc.t.BustPatrolBPS
	if b, err := tx.Law().BribeFrom(ctx, lc.s.CityID, lc.p.ID, "", "detection", lc.now); err != nil {
		return "", err
	} else if b != nil {
		chance = lc.t.AfterBribe(chance, int64(b.EffectBPS))
	}
	busted := law.Hit(chance, law.Roll(l.ID+"|"+lc.p.ID+"|bust"))
	dirty := strings.TrimSpace(lc.req.Dirty) == "1"
	if l.SellerID == "" {
		return h.lawBuyFromFence(ctx, tx, lc, l, qty, total, dirty, busted)
	}
	return h.lawBuyFromPlayer(ctx, tx, lc, l, total, dirty, busted)
}

// lawPayFrom takes the price of a purchase from the buyer's dirty balance or his clean money into an account.
func (h *VillageHandler) lawPayFrom(ctx context.Context, tx application.Tx, lc *lawCtx, dirty bool, amount int64, reason application.Reason, refType, refID, to string) error {
	if !dirty {
		return lawPay(ctx, tx, lc, lc.p.ID, amount, reason, refType, refID, to)
	}
	d, bal, err := lawBalance(ctx, tx, application.AccountPlayerDirty, lc.p.ID)
	if err != nil {
		return err
	}
	if bal < amount {
		return lawRefuse(village.LawNoMoney)
	}
	return lawPost(ctx, tx, lc, reason, refType, refID, d.ID, to, amount)
}

func (h *VillageHandler) lawBuyFromFence(ctx context.Context, tx application.Tx, lc *lawCtx, l *application.LawListing, qty, total int64, dirty, busted bool) (string, error) {
	if busted {
		// the buyer is caught in the act before the goods change hands
		if offence := lawItemOffence(lc.snap, l.Item); offence != "" {
			if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, offence, lc.now); err != nil {
				return "", err
			} else if o != nil {
				c, err := h.lawOpenOrFind(ctx, tx, lc, offence, lc.p.ID, "patrol", "", 0)
				if err != nil {
					return "", err
				}
				if _, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvWitness, "bust:"+l.ID+":"+lc.p.ID, true, -1, "", ""); err != nil {
					return "", err
				}
			}
		}
		return village.LawNoticeBusted, nil
	}
	sink, err := lawSink(ctx, tx)
	if err != nil {
		return "", err
	}
	if err := h.lawPayFrom(ctx, tx, lc, dirty, total, application.ReasonEscrowRelease, application.LawEscrowReference, l.ID, sink.ID); err != nil {
		return "", err
	}
	dd := dirty
	_ = dd
	if _, err := bring(ctx, tx, lc.snap, h.ids, nil, lc.p.ID, l.Item, qty, -1, origin{kind: application.OriginSupply, reason: application.ItemSupplied,
		refType: application.LawEscrowReference, refID: l.ID}, lc.now); err != nil {
		return "", err
	}
	// the day's supply is used up in pieces: a listing that is bought out closes, one that is not keeps what is left
	if qty >= l.Qty {
		if _, err := tx.Law().SetListing(ctx, l.ID, []string{"open"}, "sold", lc.now); err != nil {
			return "", err
		}
	} else if err := tx.Law().ShrinkListing(ctx, l.ID, qty); err != nil {
		return "", err
	}
	return village.LawNoticeBought, tx.Law().AddStanding(ctx, lc.p.ID, "", lc.t.StandingGain, lc.now)
}

// lawItemOffence is the offence a trade in the good is.
func lawItemOffence(snap *content.Snapshot, item string) string {
	if d, ok := snap.ShadowSupplyOf(item); ok {
		return d.Offence
	}
	return ""
}

func (h *VillageHandler) lawBuyFromPlayer(ctx context.Context, tx application.Tx, lc *lawCtx, l *application.LawListing, total int64, dirty, busted bool) (string, error) {
	if busted {
		// the listing is frozen, the goods seized into the settlement's stock, and a case opens against the seller if the good is outlawed here
		if ok, err := tx.Law().SetListing(ctx, l.ID, []string{"open"}, "frozen", lc.now); err != nil || !ok {
			return "", err
		}
		if err := h.lawSeizeListed(ctx, tx, lc, l); err != nil {
			return "", err
		}
		if offence := lawItemOffence(lc.snap, l.Item); offence != "" {
			if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, offence, lc.now); err != nil {
				return "", err
			} else if o != nil {
				c, err := h.lawOpenOrFind(ctx, tx, lc, offence, l.SellerID, "patrol", "", 0)
				if err != nil {
					return "", err
				}
				if _, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvSeized, "seize:"+l.ID, true, -1, "", ""); err != nil {
					return "", err
				}
			}
		}
		return village.LawNoticeBusted, nil
	}
	broker, err := lawPlayerByCode(ctx, tx, lc.req.Code)
	if err != nil {
		return "", lawRefuse(village.LawNoBroker)
	}
	b, err := tx.Law().Broker(ctx, broker)
	if err != nil {
		return "", err
	}
	if b == nil || broker == lc.p.ID || broker == l.SellerID {
		return "", lawRefuse(village.LawNoBroker)
	}
	if total > lc.t.BrokerCap(b.Reputation) {
		return "", lawRefuse(village.LawOverBrokerCap)
	}
	fee := lc.t.EscrowFee(total)
	esc := application.LawEscrow{ID: h.ids.NewID(), ListingID: l.ID, BuyerID: lc.p.ID, BrokerID: broker, Amount: total, Fee: fee, Qty: l.Qty, Dirty: dirty,
		DueAt: lc.now.Add(time.Duration(lc.t.EscrowTimeoutHours) * time.Hour), At: lc.now}
	if ok, err := tx.Law().InsertEscrow(ctx, esc); err != nil {
		return "", err
	} else if !ok {
		return "", lawRefuse(village.LawNoListing)
	}
	pot, err := lawAcct(ctx, tx, application.AccountEscrowBlack, esc.ID)
	if err != nil {
		return "", err
	}
	if err := h.lawPayFrom(ctx, tx, lc, dirty, total+fee, application.ReasonEscrowHold, application.LawEscrowReference, esc.ID, pot.ID); err != nil {
		return "", err
	}
	if ok, err := tx.Law().SetListing(ctx, l.ID, []string{"open"}, "sold", lc.now); err != nil || !ok {
		return "", err
	}
	if err := h.schedulePolity(ctx, tx, application.TickLawEscrow, application.LawEscrowReference, esc.ID, "", lc.p.ID, lc.now, esc.DueAt); err != nil {
		return "", err
	}
	return village.LawNoticeEscrowed, nil
}

// lawSeizeListed takes a frozen listing's goods out of the seller's set-aside and into the settlement's stock.
func (h *VillageHandler) lawSeizeListed(ctx context.Context, tx application.Tx, lc *lawCtx, l *application.LawListing) error {
	if err := tx.Items().LockOwner(ctx, l.SellerID); err != nil {
		return err
	}
	org := application.SettlementOrg(lc.s.CityID)
	if err := tx.Items().LockOrg(ctx, org); err != nil {
		return err
	}
	stack, pieces, err := lawGoods(ctx, tx, l.SellerID, application.HoldEscrow, l.Item)
	if err != nil {
		return err
	}
	if stack >= l.Qty {
		return tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: l.Item, Qty: l.Qty, From: l.SellerID, FromHolding: application.HoldEscrow, ToOrg: org,
			ToHolding: application.HoldWarehouse, Reason: application.ItemConfiscated, ReferenceType: application.LawEscrowReference, ReferenceID: l.ID, At: lc.now})
	}
	for i, p := range pieces {
		if int64(i) >= l.Qty {
			break
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: l.Item, PieceID: p.ID, Qty: 1, From: l.SellerID, FromHolding: application.HoldEscrow,
			ToOrg: org, ToHolding: application.HoldWarehouse, Reason: application.ItemConfiscated, ReferenceType: application.LawEscrowReference, ReferenceID: l.ID, At: lc.now}); err != nil {
			return err
		}
	}
	return nil
}

// lawEscrowFor reads a held escrow.
func (h *VillageHandler) lawEscrowFor(ctx context.Context, tx application.Tx, id string) (*application.LawEscrow, *application.LawListing, error) {
	e, err := tx.Law().EscrowByID(ctx, strings.TrimSpace(id), true)
	if err != nil {
		return nil, nil, err
	}
	if e == nil || e.Status != "held" {
		return nil, nil, lawRefuse(village.LawNoEscrow)
	}
	l, err := tx.Law().ListingByID(ctx, e.ListingID, true)
	if err != nil || l == nil {
		return nil, nil, lawRefuse(village.LawNoEscrow)
	}
	return e, l, nil
}

// lawConfirm is the buyer confirming he has what he paid for: the broker releases the pot. The price goes to the seller's dirty balance, the fee to
// the broker, the goods to the buyer; both sides gain standing and the broker a name.
func (h *VillageHandler) lawConfirm(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	e, l, err := h.lawEscrowFor(ctx, tx, lc.req.ID)
	if err != nil {
		return "", err
	}
	if e.BuyerID != lc.p.ID {
		return "", lawRefuse(village.LawNoEscrow)
	}
	if ok, err := tx.Law().CloseEscrow(ctx, e.ID, "released", lc.now); err != nil || !ok {
		return "", err
	}
	pot, err := lawAcct(ctx, tx, application.AccountEscrowBlack, e.ID)
	if err != nil {
		return "", err
	}
	sd, err := lawAcct(ctx, tx, application.AccountPlayerDirty, l.SellerID)
	if err != nil {
		return "", err
	}
	bc, err := lawAcct(ctx, tx, application.AccountPlayerCash, e.BrokerID)
	if err != nil {
		return "", err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonEscrowRelease, application.LawEscrowReference, e.ID, pot.ID, sd.ID, e.Amount); err != nil {
		return "", err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonEscrowFee, application.LawEscrowReference, e.ID, pot.ID, bc.ID, e.Fee); err != nil {
		return "", err
	}
	if err := h.lawMoveGoods(ctx, tx, lc, l.SellerID, application.HoldEscrow, e.BuyerID, application.HoldCarried, l.Item, e.Qty, application.ItemMarketTrade,
		application.LawEscrowReference, e.ID); err != nil {
		return "", err
	}
	for _, p := range []string{e.BuyerID, l.SellerID} {
		if err := tx.Law().AddStanding(ctx, p, "", lc.t.StandingGain, lc.now); err != nil {
			return "", err
		}
	}
	if err := tx.Law().BrokerMoved(ctx, e.BrokerID, 1, 1, 0); err != nil {
		return "", err
	}
	return village.LawNoticeConfirmed, nil
}

// lawRefund sends the pot back to the buyer: the seller or the broker may do it at any time, and the buyer once the time is out. The goods go back to
// the seller and the listing opens again.
func (h *VillageHandler) lawRefund(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	e, l, err := h.lawEscrowFor(ctx, tx, lc.req.ID)
	if err != nil {
		return "", err
	}
	if lc.p.ID != l.SellerID && lc.p.ID != e.BrokerID && !(lc.p.ID == e.BuyerID && !lc.now.Before(e.DueAt)) {
		return "", lawRefuse(village.LawNoEscrow)
	}
	return village.LawNoticeRefunded, h.lawRefundEscrow(ctx, tx, lc, e, l)
}

// lawRefundEscrow closes a held escrow as refunded and puts everything back where it came from.
func (h *VillageHandler) lawRefundEscrow(ctx context.Context, tx application.Tx, lc *lawCtx, e *application.LawEscrow, l *application.LawListing) error {
	if ok, err := tx.Law().CloseEscrow(ctx, e.ID, "refunded", lc.now); err != nil || !ok {
		return err
	}
	pot, err := lawAcct(ctx, tx, application.AccountEscrowBlack, e.ID)
	if err != nil {
		return err
	}
	kind := application.AccountPlayerCash
	if e.Dirty {
		kind = application.AccountPlayerDirty
	}
	back, err := lawAcct(ctx, tx, kind, e.BuyerID)
	if err != nil {
		return err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonEscrowRefund, application.LawEscrowReference, e.ID, pot.ID, back.ID, e.Amount+e.Fee); err != nil {
		return err
	}
	_, err = tx.Law().SetListing(ctx, l.ID, []string{"sold"}, "open", lc.now)
	return err
}

// lawScam is the broker leaving with the pot: the buyer's money and the fee go to the broker's dirty balance, the goods stay with the seller, and the
// broker's name falls. The scam may be traced to the broker by a seeded roll, and then a case opens against him.
func (h *VillageHandler) lawScam(ctx context.Context, tx application.Tx, lc *lawCtx) (string, error) {
	if err := h.lawNeed(h.blackOpen(), village.LawNotYet); err != nil {
		return "", err
	}
	e, l, err := h.lawEscrowFor(ctx, tx, lc.req.ID)
	if err != nil {
		return "", err
	}
	if e.BrokerID != lc.p.ID {
		return "", lawRefuse(village.LawNoEscrow)
	}
	if ok, err := tx.Law().CloseEscrow(ctx, e.ID, "scammed", lc.now); err != nil || !ok {
		return "", err
	}
	pot, err := lawAcct(ctx, tx, application.AccountEscrowBlack, e.ID)
	if err != nil {
		return "", err
	}
	bd, err := lawAcct(ctx, tx, application.AccountPlayerDirty, e.BrokerID)
	if err != nil {
		return "", err
	}
	if err := lawPost(ctx, tx, lc, application.ReasonEscrowScam, application.LawEscrowReference, e.ID, pot.ID, bd.ID, e.Amount+e.Fee); err != nil {
		return "", err
	}
	if _, err := tx.Law().SetListing(ctx, l.ID, []string{"sold"}, "open", lc.now); err != nil {
		return "", err
	}
	if err := tx.Law().BrokerMoved(ctx, e.BrokerID, -lc.t.ScamRepLoss, 0, 1); err != nil {
		return "", err
	}
	if lc.t.ScamTraced(law.Roll(e.ID + "|trace")) {
		if o, err := tx.Law().OffenceIn(ctx, lc.s.CityID, "fraud", lc.now); err != nil {
			return "", err
		} else if o != nil {
			c, err := h.lawOpenOrFind(ctx, tx, lc, "fraud", e.BrokerID, "trace", "", 0)
			if err != nil {
				return "", err
			}
			if _, err := h.lawEvidence(ctx, tx, lc, c.ID, law.EvRecord, "scam:"+e.ID, true, -1, "", ""); err != nil {
				return "", err
			}
		}
		return village.LawNoticeScamTraced, nil
	}
	return village.LawNoticeScam, nil
}

// tickLawEscrow returns the pot to the buyer when the time is out and nobody answered.
func (h *VillageHandler) tickLawEscrow(ctx context.Context, tx application.Tx, meta lawMeta, escrowID string, now time.Time) error {
	e, err := tx.Law().EscrowByID(ctx, escrowID, true)
	if err != nil || e == nil || e.Status != "held" || now.Before(e.DueAt) {
		return err
	}
	l, err := tx.Law().ListingByID(ctx, e.ListingID, true)
	if err != nil || l == nil {
		return err
	}
	lc, err := h.lawCtxFor(ctx, tx, meta, l.SettlementID, now)
	if err != nil || lc == nil {
		return err
	}
	return h.lawRefundEscrow(ctx, tx, lc, e, l)
}

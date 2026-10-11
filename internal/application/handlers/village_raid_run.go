package handlers

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/raid"
	"github.com/mrjvadi/torncity/internal/domain/war"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
)

// The window of a raid and its end (docs/adr/0074 6, 8): raid.open moves a filed raid to open at its start; raid.resolve ends it. Both are
// scheduled game actions, so any replica may run them; the status of the raid moves once by a conditional update under the raid's row
// lock, and a second run finds the status moved and does nothing.

func (h *VillageHandler) raidScheduled(ctx context.Context, meta envelope.Metadata, req RaidScheduledRequest, run func(ctx context.Context, tx application.Tx, row *application.SettlementRaid, now time.Time) error) error {
	if err := meta.Validate(); err != nil {
		return errors.InvalidInput("malformed request context").WithCause(err)
	}
	var pl raidPayload
	if len(req.Payload) > 0 {
		_ = json.Unmarshal(req.Payload, &pl)
	}
	if pl.RaidID == "" {
		pl.RaidID = req.ReferenceID
	}
	if pl.RaidID == "" {
		return errors.InvalidInput(meta.Command + " names no raid")
	}
	return h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		// the filer owns the key (a key names a player); without one the status of the raid is the fence
		if req.ActorID != "" {
			key := idempotency.Derive(req.ActorID, meta.Command, pl.RaidID+":"+req.ActionID)
			if fresh, err := tx.Idempotency().Reserve(ctx, string(key), req.ActorID, meta.RequestID, meta.Command, h.idempotencyTTL); err != nil || !fresh {
				return err
			}
		}
		row, err := tx.Raids().ByID(ctx, pl.RaidID, true)
		if err != nil {
			if isSentinel(err, application.ErrRaidNotFound) {
				return nil
			}
			return err
		}
		return run(ctx, tx, row, h.now())
	})
}

// RaidOpen handles raid.open: the window opens.
func (h *VillageHandler) RaidOpen(ctx context.Context, meta envelope.Metadata, req RaidScheduledRequest) (*presentation.Response, error) {
	return nil, h.raidScheduled(ctx, meta, req, func(ctx context.Context, tx application.Tx, row *application.SettlementRaid, now time.Time) error {
		moved, err := tx.Raids().Move(ctx, row.ID, application.RaidFiled, application.RaidOpen, now)
		if err != nil || !moved {
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "raid_opened", row.DefenderID, map[string]any{"raid_id": row.ID, "attacker_id": row.AttackerID, "defender_id": row.DefenderID})
	})
}

// RaidResolve handles raid.resolve: the window closes and the raid is decided, once.
func (h *VillageHandler) RaidResolve(ctx context.Context, meta envelope.Metadata, req RaidScheduledRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	return nil, h.raidScheduled(ctx, meta, req, func(ctx context.Context, tx application.Tx, row *application.SettlementRaid, now time.Time) error {
		switch row.Status {
		case application.RaidResolved, application.RaidSettled:
			return nil
		case application.RaidFiled:
			// the opening was missed (a stopped worker): it opens now so the status only ever moves forward
			if _, err := tx.Raids().Move(ctx, row.ID, application.RaidFiled, application.RaidOpen, now); err != nil {
				return err
			}
		}
		return h.resolveRaid(ctx, tx, meta, snap, row, now)
	})
}

// unitsOf builds the fighting units of one side: the players who took part, then the hired or garrison seats.
func (h *VillageHandler) unitsOf(players, seats int64) []war.Unit {
	u := war.Unit{Firepower: h.raid.SeatFirepower, Armour: h.raid.SeatArmour, Quality: 50}
	out := make([]war.Unit, 0, players+seats)
	for i := int64(0); i < players+seats; i++ {
		out = append(out, u)
	}
	return out
}

// garrisonSeats counts the seats of the posts that were open on the day: their watchmen, militiamen and retainers, fed, paid and with
// a fire.
func (h *VillageHandler) garrisonSeats(day *application.ServiceDay) int64 {
	if day == nil {
		return 0
	}
	want := map[string]bool{}
	for _, s := range h.raid.GarrisonServices {
		want[s] = true
	}
	var seats int64
	for _, p := range day.Posts {
		if p.Held && want[p.Service] {
			seats += p.Staff
		}
	}
	return seats
}

// raidSide is a player who took part and is still able to: a resident of his side's settlement, not in hospital.
func (h *VillageHandler) raidSide(ctx context.Context, tx application.Tx, parts []application.RaidParticipant, side, settlementID string, now time.Time) ([]string, error) {
	var out []string
	for _, x := range parts {
		if x.Side != side {
			continue
		}
		home, err := tx.Employment().ResidenceCityID(ctx, x.PlayerID)
		if err != nil {
			return nil, err
		}
		if home != settlementID {
			continue
		}
		if st, err := hospitalised(ctx, tx, x.PlayerID, now); err != nil {
			return nil, err
		} else if st != nil {
			continue
		}
		out = append(out, x.PlayerID)
	}
	sort.Strings(out)
	return out, nil
}

func (h *VillageHandler) resolveRaid(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, row *application.SettlementRaid, now time.Time) error {
	def, err := tx.Settlements().ByID(ctx, row.DefenderID)
	if err != nil {
		return err
	}
	att, err := tx.Settlements().ByID(ctx, row.AttackerID)
	if err != nil {
		return err
	}
	out := decodeOutcome(row.Outcome)
	parts, err := tx.Raids().Participants(ctx, row.ID)
	if err != nil {
		return err
	}
	attPlayers, err := h.raidSide(ctx, tx, parts, application.RaidAttacker, att.CityID, now)
	if err != nil {
		return err
	}
	defPlayers, err := h.raidSide(ctx, tx, parts, application.RaidDefender, def.CityID, now)
	if err != nil {
		return err
	}
	buildings, err := tx.SettlementBuildings().List(ctx, def.CityID)
	if err != nil {
		return err
	}
	day, err := h.SettleServiceDay(ctx, tx, snap, def, buildings)
	if err != nil {
		return err
	}
	seats := h.garrisonSeats(day)
	fight, err := war.Fight(war.Assault{Seed: row.Seed, Attackers: h.unitsOf(int64(len(attPlayers)), out.Raiders), Defenders: h.unitsOf(int64(len(defPlayers)), seats),
		AttackerReadiness: war.BPSWhole, DefenderReadiness: war.BPSWhole, Rounds: h.raid.Rounds, DefenderAdvantageBPS: h.raid.DefenderAdvantageBPS,
		BaseHitBPS: h.raid.BaseHitBPS, HitFloorBPS: h.raid.HitFloorBPS, HitCeilingBPS: h.raid.HitCeilingBPS, HoldMin: 1})
	if err != nil {
		return errors.Internal(err)
	}
	out.Resolved, out.Taken = true, fight.Taken
	out.AttUnits, out.DefUnits = int64(len(fight.Attackers)), int64(len(fight.Defenders))

	// what the raiders take, when they prevailed
	var claims []ClaimItem
	if fight.Taken {
		standing := int64(0)
		for _, f := range fight.Attackers {
			if f != war.UnitDestroyed {
				standing++
			}
		}
		loot, err := h.takeLoot(ctx, tx, snap, row, att, def, standing, now)
		if err != nil {
			return err
		}
		out.Cash, out.Goods = loot.Cash, loot.Goods
		dmgClaims, hit, total, err := h.damageBuildings(ctx, tx, snap, row, buildings, now)
		if err != nil {
			return err
		}
		out.Buildings, out.Damage = hit, total
		claims = append(claims, dmgClaims...)
	}

	// the players who fell: only those who took part, on either side
	for i, pid := range attPlayers {
		if fight.Attackers[i] == war.UnitFit {
			continue
		}
		item, err := h.raidFall(ctx, tx, meta, snap, row, pid, def.CityID, att.CityID, now)
		if err != nil {
			return err
		}
		out.Falls = append(out.Falls, pid)
		if item != nil {
			claims = append(claims, *item)
		}
	}
	for i, pid := range defPlayers {
		if fight.Defenders[i] == war.UnitFit {
			continue
		}
		item, err := h.raidFall(ctx, tx, meta, snap, row, pid, att.CityID, def.CityID, now)
		if err != nil {
			return err
		}
		out.Falls = append(out.Falls, pid)
		if item != nil {
			claims = append(claims, *item)
		}
	}
	if _, err := ClaimBatch(ctx, tx, snap, h.ids, meta, claims, now); err != nil {
		return err
	}
	body, _ := json.Marshal(out)
	moved, err := tx.Raids().Finish(ctx, row.ID, application.RaidOpen, application.RaidResolved, out.Cash > 0 || len(out.Goods) > 0, nil, body, now)
	if err != nil || !moved {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "raid_resolved", def.CityID, map[string]any{"raid_id": row.ID, "attacker_id": att.CityID, "defender_id": def.CityID,
		"taken": out.Taken, "cash": out.Cash, "falls": len(out.Falls), "buildings": out.Buildings})
}

// takeLoot moves what the raiders take from the defender's treasury and storehouse to the attacker's, capped by what is exposed, by what
// the raiders can haul, by a shield after a recent loss, and by the proportion of the two settlements' wealth.
func (h *VillageHandler) takeLoot(ctx context.Context, tx application.Tx, snap *content.Snapshot, row *application.SettlementRaid, att, def application.FoundedSettlement,
	standing int64, now time.Time,
) (raid.Loot, error) {
	treasury, stock, defWorth, err := h.exposedOf(ctx, tx, snap, def.CityID)
	if err != nil {
		return raid.Loot{}, err
	}
	_, _, attWorth, err := h.exposedOf(ctx, tx, snap, att.CityID)
	if err != nil {
		return raid.Loot{}, err
	}
	shielded := false
	past, err := tx.Raids().AgainstSince(ctx, def.CityID, now.Add(-(h.raid.Rules.Shield + h.raid.Rules.MaxLead + h.raid.Rules.Window)))
	if err != nil {
		return raid.Loot{}, err
	}
	for _, x := range past {
		if x.ID != row.ID && x.Looted && x.ResolvedAt != nil && now.Sub(*x.ResolvedAt) < h.raid.Rules.Shield {
			shielded = true
		}
	}
	loot := h.raid.Rules.LootOf(raid.Exposed{Treasury: treasury, Stock: stock, Carry: standing * h.raid.CarryPerRaider}, shielded, attWorth, defWorth)
	if loot.Cash > 0 {
		from, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, def.CityID)
		if err != nil {
			return loot, err
		}
		to, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, att.CityID)
		if err != nil {
			return loot, err
		}
		txID, err := move(ctx, tx, from.ID, to.ID, loot.Cash, application.ReasonRaidLootCash, application.RaidReference, row.ID, "", now)
		if err != nil {
			return loot, err
		}
		if err := tx.Raids().AddLoot(ctx, application.RaidLoot{ID: h.ids.NewID(), RaidID: row.ID, Kind: application.LootCash, Qty: loot.Cash, LedgerTx: txID, At: now}); err != nil {
			return loot, err
		}
	}
	if len(loot.Goods) > 0 {
		from, to := application.SettlementOrg(def.CityID), application.SettlementOrg(att.CityID)
		first, second := from, to
		if second.ID < first.ID {
			first, second = second, first
		}
		if err := tx.Items().LockOrg(ctx, first); err != nil {
			return loot, err
		}
		if err := tx.Items().LockOrg(ctx, second); err != nil {
			return loot, err
		}
		items := make([]string, 0, len(loot.Goods))
		for it := range loot.Goods {
			items = append(items, it)
		}
		sort.Strings(items)
		for _, it := range items {
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: it, Qty: loot.Goods[it], FromOrg: from, FromHolding: application.HoldWarehouse,
				ToOrg: to, ToHolding: application.HoldWarehouse, Reason: application.ItemRaidLoot, ReferenceType: application.RaidReference, ReferenceID: row.ID, At: now}); err != nil {
				return loot, err
			}
			if err := tx.Raids().AddLoot(ctx, application.RaidLoot{ID: h.ids.NewID(), RaidID: row.ID, Kind: application.LootItem, Item: it, Qty: loot.Goods[it], At: now}); err != nil {
				return loot, err
			}
		}
	}
	return loot, nil
}

// damageBuildings breaks some of the defender's buildings: each complete one is hit with a chance, by a share of the most a raid may do,
// never past ruin; repairs restore it as they restore wear. It returns the claims of the raid_damage policies on the buildings hit.
func (h *VillageHandler) damageBuildings(ctx context.Context, tx application.Tx, snap *content.Snapshot, row *application.SettlementRaid, buildings []application.SettlementBuildingInstance,
	now time.Time,
) (claims []ClaimItem, hit, total int64, err error) {
	if h.raid.Rules.DamageMaxBPS <= 0 {
		return nil, 0, 0, nil
	}
	damaged := map[string]int64{}
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		if raid.Roll(row.Seed, "hit:"+b.ID) >= 2500 {
			continue
		}
		dmg := h.raid.Rules.DamageMaxBPS * (5000 + raid.Roll(row.Seed, "dmg:"+b.ID)/2) / raid.BPS
		stored := int64(b.DamageBPS)
		dmg = min(dmg, 10000-stored)
		if dmg <= 0 {
			continue
		}
		if err := tx.SettlementBuildings().SetDamage(ctx, b.ID, int(stored+dmg), now); err != nil {
			return nil, 0, 0, err
		}
		if err := tx.Raids().AddLoot(ctx, application.RaidLoot{ID: h.ids.NewID(), RaidID: row.ID, Kind: application.LootDamage, Qty: dmg, BuildingID: b.ID, At: now}); err != nil {
			return nil, 0, 0, err
		}
		damaged[b.ID] = dmg
		hit++
		total += dmg
	}
	if len(damaged) == 0 {
		return nil, 0, 0, nil
	}
	policies, err := tx.Finance().BuildingPolicies(ctx, row.DefenderID)
	if err != nil {
		return nil, 0, 0, err
	}
	for _, p := range policies {
		dmg, ok := damaged[p.BuildingID]
		if !ok {
			continue
		}
		value := buildingValue(ctx, tx, snap, p.BuildingID)
		if loss := value * dmg / raid.BPS; loss > 0 {
			claims = append(claims, ClaimItem{Policy: p, Source: "raid:" + row.ID, Loss: loss})
		}
	}
	return claims, hit, total, nil
}

// raidFall is what happens to a player who fell: he is taken to the hospital of the settlement where it happened (the stay is the cost
// in time) and, from the date the rule starts and outside the cooldown of the last fall, loses a capped share of what he carries to the
// settlement across the line. Nothing else is touched: not the bank, not a lot, not a skill. It returns the claim his insurance owes.
func (h *VillageHandler) raidFall(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, row *application.SettlementRaid,
	playerID, receiverID, placeID string, now time.Time,
) (*ClaimItem, error) {
	inj, err := injure(ctx, tx, snap, h.ids, h.raidScale, meta, hurt{playerID: playerID, cityID: placeID, cause: application.CauseRaid, causeRef: row.ID, damage: 1 << 20}, now)
	if err != nil {
		return nil, err
	}
	fall := application.Fall{ID: h.ids.NewID(), PlayerID: playerID, Cause: application.CauseRaid, CauseRef: row.ID, LostGoods: map[string]int64{}, At: now}
	if inj != nil && inj.Stay != nil {
		fall.StayID = inj.Stay.ID
	}
	var loss raid.Loss
	var values map[string]int64
	if h.raid.FallsOn(now) {
		if err := tx.Items().LockOwner(ctx, playerID); err != nil {
			return nil, err
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), playerID)
		if err != nil {
			return nil, err
		}
		stacks, _, err := tx.Items().Holdings(ctx, playerID, application.HoldCarried)
		if err != nil {
			return nil, err
		}
		carried := raid.Carried{Cash: max(wallet.Cash.Balance.Minor(), 0), Goods: map[string]int64{}, Value: map[string]int64{}}
		carried.NetAssets = carried.Cash + max(wallet.Bank.Balance.Minor(), 0)
		for _, st := range stacks {
			if st.Qty <= 0 {
				continue
			}
			carried.Goods[st.Item] += st.Qty
			if d, ok := snap.ItemDef(st.Item); ok {
				carried.Value[st.Item] = d.BasePrice
				carried.NetAssets += st.Qty * d.BasePrice
			}
		}
		last, err := tx.Raids().LastFall(ctx, playerID)
		if err != nil {
			return nil, err
		}
		fall.Cooling = !last.IsZero() && now.Sub(last) < h.raid.Rules.FallCooldown
		loss = h.raid.Rules.FallLoss(carried, fall.Cooling)
		values = carried.Value
		fall.LostCash, fall.LostGoods, fall.Capped = loss.Cash, loss.Goods, loss.Capped
		fall.NetAssets, fall.LostWorth = carried.NetAssets, loss.Worth(carried.Value)
		if loss.Cash > 0 {
			fall.CashTx = h.ids.NewID()
		}
		if len(fall.LostGoods) == 0 {
			fall.LostGoods = map[string]int64{}
		}
		fresh, err := tx.Raids().RecordFall(ctx, fall)
		if err != nil || !fresh {
			return nil, err
		}
		if loss.Cash > 0 {
			to, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, receiverID)
			if err != nil {
				return nil, err
			}
			if _, err := move(ctx, tx, wallet.Cash.ID, to.ID, loss.Cash, application.ReasonDeathCash, application.FallReference, fall.ID, fall.CashTx, now); err != nil {
				return nil, err
			}
		}
		items := make([]string, 0, len(loss.Goods))
		for it := range loss.Goods {
			items = append(items, it)
		}
		sort.Strings(items)
		for _, it := range items {
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: it, Qty: loss.Goods[it], From: playerID, FromHolding: application.HoldCarried,
				ToOrg: application.SettlementOrg(receiverID), ToHolding: application.HoldWarehouse, Reason: application.ItemDeathDrop, ReferenceType: application.FallReference,
				ReferenceID: fall.ID, At: now}); err != nil {
				return nil, err
			}
		}
	} else {
		fall.Cooling = true // before the rule date a fall costs the stay and nothing else
		if fresh, err := tx.Raids().RecordFall(ctx, fall); err != nil || !fresh {
			return nil, err
		}
	}
	if err := appendVillageEvent(ctx, tx, meta, "player_fell", placeID, map[string]any{"player_id": playerID, "raid_id": row.ID, "cash": fall.LostCash, "capped": fall.Capped}); err != nil {
		return nil, err
	}
	worth := loss.Worth(values)
	if worth <= 0 {
		return nil, nil
	}
	policy, err := tx.Finance().CoveringPolicy(ctx, playerID, content.CoverDeathLoss)
	if err != nil || policy == nil {
		return nil, err
	}
	return &ClaimItem{Policy: *policy, Source: "fall:" + row.ID, Loss: worth}, nil
}

package handlers

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/raid"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// Raids (docs/adr/0074-death-insurance-raids-detection.md, plan B10).
//
// A raid is a charter act of one settlement against another: the holder of raid.declare files it with a fee, a start between the
// minimum and the maximum lead, some hired raiders and a way of travelling. It happens at its start whether anyone is online or not
// (there is no offline shield); the only warning is what the defender's staffed lookouts, towers and beacons see (village_raid_watch.go).
// Players choose to take part: the raiders join, the defenders stand. At the end of its window the raid is resolved once, by a seeded
// fight (war.Fight) of the raiders against the garrison seats that were open that day and the defenders who stood; if the raiders
// prevailed they take a capped share of the exposed stock, and buildings take condition damage. A player who falls loses a capped share of
// what he carries, goes to hospital and is paid by the insurance he held (village_raid_run.go). Nothing here is a tick: three scheduled
// actions per raid, each moving the raid's status once by a conditional update, so any replica may run them and a second run does nothing.

// WithRaids gives the village handler its raid rules and the clock scale the stay of a fall is measured on.
func (h *VillageHandler) WithRaids(r application.RaidRules, scale gametime.Scale) *VillageHandler {
	h.raid, h.raidScale = r, scale
	return h
}

// VillageRaidRequest is the payload of settlement.raid; the names are those internal/gateway/routing gives the arguments.
type VillageRaidRequest struct {
	// Action is "" (the screen), file, join, stand, offer or accept (village.RaidAction*).
	Action string `json:"action,omitempty"`
	// Code is a target settlement's code (file) or a raid's id (the rest).
	Code string `json:"code,omitempty"`
	// N is the lead in hours (file) or the tribute (offer).
	N string `json:"n,omitempty"`
	// Raiders is how many raiders to hire (file); Transport how they travel (foot, cart, horse, rail).
	Raiders   string `json:"raiders,omitempty"`
	Transport string `json:"transport,omitempty"`
}

// raidPayload is the payload of a raid's scheduled actions.
type raidPayload struct {
	RaidID     string `json:"raid_id"`
	BuildingID string `json:"building_id,omitempty"`
}

// RaidScheduledRequest is the scheduler's payload for raid.sighting, raid.open and raid.resolve.
type RaidScheduledRequest = CrimeScheduledRequest

// raidOutcome is what a raid's row keeps beside its status: how it was filed and how it ended.
type raidOutcome struct {
	Raiders   int64            `json:"raiders"`
	Transport string           `json:"transport"`
	Taken     bool             `json:"taken,omitempty"`
	Cash      int64            `json:"cash,omitempty"`
	Goods     map[string]int64 `json:"goods,omitempty"`
	Buildings int64            `json:"buildings,omitempty"`
	Damage    int64            `json:"damage,omitempty"`
	Falls     []string         `json:"falls,omitempty"`
	AttUnits  int64            `json:"attacker_units,omitempty"`
	DefUnits  int64            `json:"defender_units,omitempty"`
	Resolved  bool             `json:"resolved,omitempty"`
}

func decodeOutcome(b []byte) raidOutcome {
	var o raidOutcome
	if len(b) > 0 {
		_ = json.Unmarshal(b, &o)
	}
	return o
}

// raidSeedOf makes a raid's seed from its id.
func raidSeedOf(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return int64(h.Sum64() >> 1)
}

// Raid handles settlement.raid: the raids the settlement is part of, the targets it could raid, and the acts (file, join, stand, offer,
// accept).
func (h *VillageHandler) Raid(ctx context.Context, meta envelope.Metadata, req VillageRaidRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.RaidView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.raidAct(ctx, tx, meta, snap, s, p, act, req); err != nil {
					return err
				}
			}
		}
		view, err = h.raidView(ctx, tx, snap, s, p)
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Raid(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) raidAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, act string, req VillageRaidRequest,
) (string, error) {
	now := h.now()
	home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
	if err != nil {
		return "", err
	}
	if home != s.CityID {
		return "", refuseVillage(village.RaidNotResident, village.AddrRaid)
	}
	code := strings.TrimSpace(req.Code)
	switch act {
	case village.RaidActionFile:
		return village.RaidNoticeFiled, h.raidFile(ctx, tx, meta, snap, s, p, code, req, now)
	case village.RaidActionJoin, village.RaidActionStand:
		raidRow, err := tx.Raids().ByID(ctx, code, true)
		if err != nil {
			return "", refuseVillage(village.RaidNotFound, village.AddrRaid)
		}
		if raidRow.Status != application.RaidFiled && raidRow.Status != application.RaidOpen {
			return "", refuseVillage(village.RaidNotOpen, village.AddrRaid)
		}
		side := application.RaidAttacker
		if act == village.RaidActionStand {
			side = application.RaidDefender
		}
		if (side == application.RaidAttacker && raidRow.AttackerID != s.CityID) || (side == application.RaidDefender && raidRow.DefenderID != s.CityID) {
			return "", refuseVillage(village.RaidSide, village.AddrRaid)
		}
		if _, err := tx.Raids().AddParticipant(ctx, application.RaidParticipant{RaidID: raidRow.ID, PlayerID: p.ID, Side: side, JoinedAt: now}); err != nil {
			return "", err
		}
		if side == application.RaidAttacker {
			return village.RaidNoticeJoined, nil
		}
		return village.RaidNoticeStanding, nil
	case village.RaidActionOffer:
		return village.RaidNoticeOffered, h.raidOffer(ctx, tx, meta, s, p, code, req.N)
	case village.RaidActionAccept:
		return village.RaidNoticeSettled, h.raidAccept(ctx, tx, meta, snap, s, p, code, now)
	}
	return "", refuseVillage(village.RaidBadArgs, village.AddrRaid)
}

// raidWorldPoint is a settlement's place on the world (degrees).
func (h *VillageHandler) raidWorldPoint(ctx context.Context, s application.FoundedSettlement) (lat, lon float64, ok bool) {
	w, err := h.world(ctx)
	if err != nil || w == nil || s.WorldCellID < 0 || int(s.WorldCellID) >= len(w.Cells) {
		return 0, 0, false
	}
	pt := w.Cells[s.WorldCellID].Point
	return pt.LatDeg, pt.LonDeg, true
}

// exposedOf is what a settlement holds within reach of raiders: the treasury and the storehouse stacks, and their worth at the reference
// prices.
func (h *VillageHandler) exposedOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string) (treasury int64, stock map[string]int64, worth int64, err error) {
	if treasury, err = treasuryBalance(ctx, tx, settlementID); err != nil {
		return 0, nil, 0, err
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(settlementID), application.HoldWarehouse)
	if err != nil {
		return 0, nil, 0, err
	}
	stock = map[string]int64{}
	worth = treasury
	for _, st := range stacks {
		if st.Qty <= 0 {
			continue
		}
		stock[st.Item] += st.Qty
		if d, ok := snap.ItemDef(st.Item); ok {
			worth += st.Qty * d.BasePrice
		}
	}
	return treasury, stock, worth, nil
}

func (h *VillageHandler) raidFile(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, targetCode string, req VillageRaidRequest, now time.Time,
) error {
	if !h.raid.RaidsOn(now) {
		r := refuseVillage(village.RaidOff, village.AddrRaid)
		r.remaining = max(h.raid.RaidsFrom.Sub(now), time.Second)
		return r
	}
	if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.RaidDeclare); err != nil {
		return err
	}
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return err
	}
	var target *application.FoundedSettlement
	for i := range founded {
		if strings.EqualFold(founded[i].Code, targetCode) {
			target = &founded[i]
		}
	}
	if target == nil {
		return refuseVillage(village.RaidNoTarget, village.AddrRaid)
	}
	if target.CityID == s.CityID {
		return refuseVillage(village.RaidSame, village.AddrRaid)
	}
	if why, err := h.aggressionBlocked(ctx, tx, snap, s, *target, now); err != nil {
		return err
	} else if why != "" {
		return refuseVillage(why, village.AddrRaid)
	}
	lead := h.raid.Rules.MinLead
	if n := strings.TrimSpace(req.N); n != "" {
		hours, err := strconv.ParseInt(n, 10, 64)
		if err != nil || hours < 0 {
			return refuseVillage(village.RaidBadArgs, village.AddrRaid)
		}
		lead = time.Duration(hours) * time.Hour
	}
	raiders := int64(0)
	if r := strings.TrimSpace(req.Raiders); r != "" {
		n, err := strconv.ParseInt(r, 10, 64)
		if err != nil || n < 0 {
			return refuseVillage(village.RaidBadArgs, village.AddrRaid)
		}
		if n > h.raid.MaxNPCRaiders {
			return refuseVillage(village.RaidTooMany, village.AddrRaid)
		}
		raiders = n
	}
	transport := strings.ToLower(strings.TrimSpace(req.Transport))
	if _, ok := h.raid.SpeedKmh[transport]; !ok {
		transport = "foot"
	}
	attRes, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return err
	}
	defRes, err := tx.Settlements().ResidentCount(ctx, target.CityID)
	if err != nil {
		return err
	}
	_, _, worth, err := h.exposedOf(ctx, tx, snap, target.CityID)
	if err != nil {
		return err
	}
	past, err := tx.Raids().AgainstSince(ctx, target.CityID, now.Add(-7*24*time.Hour))
	if err != nil {
		return err
	}
	f := raid.Filing{Now: now, Start: now.Add(lead), Attacker: s.CityID, Defender: target.CityID, AttackerResidents: attRes, DefenderResidents: defRes,
		DefenderFoundedAt: target.FoundedAt, DefenderExposed: worth}
	for _, x := range past {
		past := raid.Past{Attacker: x.AttackerID, Defender: x.DefenderID, FiledAt: x.FiledAt, Looted: x.Looted}
		if x.ResolvedAt != nil {
			past.ResolvedAt = *x.ResolvedAt
		}
		f.History = append(f.History, past)
	}
	if r := h.raid.Rules.Check(f); r != raid.Allowed {
		kind := map[raid.Refusal]string{raid.SameSettlement: village.RaidSame, raid.LeadTooShort: village.RaidLeadShort, raid.LeadTooLong: village.RaidLeadLong,
			raid.Cooldown: village.RaidCooldown, raid.WeeklyLimit: village.RaidWeekly, raid.Shielded: village.RaidShielded,
			raid.FoundingGrace: village.RaidFoundingGrace, raid.NothingToTake: village.RaidNothing}[r]
		ref := refuseVillage(kind, village.AddrRaid)
		switch r {
		case raid.LeadTooShort:
			ref.min = int64(h.raid.Rules.MinLead / time.Hour)
		case raid.LeadTooLong:
			ref.max = int64(h.raid.Rules.MaxLead / time.Hour)
		}
		return ref
	}
	wages := raiders * h.raid.NPCRaiderWage
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return err
	}
	if treasury < h.raid.Rules.Fee+wages {
		return refuseVillage(village.RaidNoMoney, village.AddrRaid)
	}
	row := application.SettlementRaid{ID: h.ids.NewID(), AttackerID: s.CityID, DefenderID: target.CityID, FiledBy: p.ID, FiledAt: now,
		StartAt: now.Add(lead), EndAt: now.Add(lead + h.raid.Rules.Window), Fee: h.raid.Rules.Fee}
	row.Seed = raidSeedOf(row.ID)
	if h.raid.Rules.Fee > 0 {
		if row.FeeTx, err = spendVillage(ctx, tx, s.CityID, application.ReasonRaidFee, h.raid.Rules.Fee, now); err != nil {
			return err
		}
	}
	if wages > 0 {
		if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonRaidWage, wages, now); err != nil {
			return err
		}
	}
	row.Outcome, _ = json.Marshal(raidOutcome{Raiders: raiders, Transport: transport})
	if err := tx.Raids().Insert(ctx, row); err != nil {
		return err
	}
	if err := h.scheduleRaid(ctx, tx, row, application.RaidOpenActionType, row.StartAt, ""); err != nil {
		return err
	}
	if err := h.scheduleRaid(ctx, tx, row, application.RaidResolveActionType, row.EndAt, ""); err != nil {
		return err
	}
	if err := h.scheduleSightings(ctx, tx, snap, row, *target, s, transport, now); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "raid_filed", s.CityID, map[string]any{"raid_id": row.ID, "attacker_id": s.CityID, "defender_id": target.CityID,
		"start_at": row.StartAt, "raiders": raiders, "filed_by": p.ID})
}

// scheduleRaid puts one of a raid's actions on the game clock.
func (h *VillageHandler) scheduleRaid(ctx context.Context, tx application.Tx, r application.SettlementRaid, kind string, at time.Time, buildingID string) error {
	payload, err := json.Marshal(raidPayload{RaidID: r.ID, BuildingID: buildingID})
	if err != nil {
		return err
	}
	return tx.GameActions().Schedule(ctx, application.GameAction{ID: h.ids.NewID(), ActionType: kind, ActorType: "player", ActorID: r.FiledBy,
		ReferenceType: application.RaidReference, ReferenceID: r.ID, Payload: payload, StartedAt: r.FiledAt, FinishAt: at})
}

func (h *VillageHandler) raidOffer(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement, p *application.Player, raidID, n string) error {
	row, err := tx.Raids().ByID(ctx, raidID, true)
	if err != nil {
		return refuseVillage(village.RaidNotFound, village.AddrRaid)
	}
	if row.DefenderID != s.CityID {
		return refuseVillage(village.RaidSide, village.AddrRaid)
	}
	if row.Status != application.RaidFiled {
		return refuseVillage(village.RaidNotOpen, village.AddrRaid)
	}
	limit, err := h.requireVillage(ctx, tx, s, p.ID, charter.TreasurySpend)
	if err != nil {
		return err
	}
	amount, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
	if err != nil || amount <= 0 {
		return refuseVillage(village.RaidBadArgs, village.AddrRaid)
	}
	if amount < h.raid.TributeMin {
		r := refuseVillage(village.RaidTributeLow, village.AddrRaid)
		r.min = h.raid.TributeMin
		return r
	}
	if limit > 0 && amount > limit {
		return refuseVillage(village.RaidNoMoney, village.AddrRaid)
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return err
	}
	if treasury < amount {
		return refuseVillage(village.RaidNoMoney, village.AddrRaid)
	}
	if err := tx.Raids().OfferTribute(ctx, row.ID, amount); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "raid_offered", s.CityID, map[string]any{"raid_id": row.ID, "attacker_id": row.AttackerID, "defender_id": row.DefenderID, "amount": amount})
}

func (h *VillageHandler) raidAccept(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player,
	raidID string, now time.Time,
) error {
	row, err := tx.Raids().ByID(ctx, raidID, true)
	if err != nil {
		return refuseVillage(village.RaidNotFound, village.AddrRaid)
	}
	if row.AttackerID != s.CityID {
		return refuseVillage(village.RaidSide, village.AddrRaid)
	}
	if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.RaidDeclare); err != nil {
		return err
	}
	if row.Status != application.RaidFiled {
		return refuseVillage(village.RaidNotOpen, village.AddrRaid)
	}
	if row.Tribute == nil || *row.Tribute <= 0 {
		return refuseVillage(village.RaidNoOffer, village.AddrRaid)
	}
	amount := *row.Tribute
	defTreasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, row.DefenderID)
	if err != nil {
		return err
	}
	attTreasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, row.AttackerID)
	if err != nil {
		return err
	}
	bal, err := tx.Ledger().Balance(ctx, defTreasury.ID)
	if err != nil {
		return err
	}
	if bal.Minor() < amount {
		return refuseVillage(village.RaidNoMoney, village.AddrRaid)
	}
	txID, err := move(ctx, tx, defTreasury.ID, attTreasury.ID, amount, application.ReasonRaidTribute, application.RaidReference, row.ID, "", now)
	if err != nil {
		return err
	}
	out := decodeOutcome(row.Outcome)
	out.Resolved = true
	body, _ := json.Marshal(out)
	moved, err := tx.Raids().Finish(ctx, row.ID, application.RaidFiled, application.RaidSettled, false, &amount, body, now)
	if err != nil || !moved {
		return err
	}
	if err := tx.Raids().AddLoot(ctx, application.RaidLoot{ID: h.ids.NewID(), RaidID: row.ID, Kind: application.LootTribute, Qty: amount, LedgerTx: txID, At: now}); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "raid_settled", row.ID, map[string]any{"raid_id": row.ID, "attacker_id": row.AttackerID, "defender_id": row.DefenderID, "amount": amount})
}

// raidView reads the raid screen.
func (h *VillageHandler) raidView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player) (village.RaidView, error) {
	now := h.now()
	v := village.RaidView{Village: s.Name, On: h.raid.RaidsOn(now), Fee: h.raid.Rules.Fee, NPCRaiderWage: h.raid.NPCRaiderWage, MaxNPCRaiders: h.raid.MaxNPCRaiders,
		TributeMin: h.raid.TributeMin, MinLeadHours: int64(h.raid.Rules.MinLead / time.Hour), MaxLeadHours: int64(h.raid.Rules.MaxLead / time.Hour),
		WindowMinutes: int64(h.raid.Rules.Window / time.Minute), Transports: []string{"foot", "cart", "horse", "rail"}}
	if !v.On && !h.raid.RaidsFrom.IsZero() {
		at := h.raid.RaidsFrom
		v.StartsAt = &at
	}
	v.May = hasPermission(ctx, tx, s, p.ID, charter.RaidDeclare)
	v.MayOffer = hasPermission(ctx, tx, s, p.ID, charter.TreasurySpend)
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return v, err
	}
	names := map[string]presentation.Named{}
	for _, f := range founded {
		names[f.CityID] = presentation.Named{Code: f.Code, Name: f.Name}
	}
	rows, err := tx.Raids().Of(ctx, s.CityID, 12)
	if err != nil {
		return v, err
	}
	for _, r := range rows {
		line, err := h.raidLine(ctx, tx, s, p, r, names)
		if err != nil {
			return v, err
		}
		v.Mine = append(v.Mine, line)
	}
	if lat, lon, ok := h.raidWorldPoint(ctx, s); ok {
		w, _ := h.world(ctx)
		radius := 6_371_000.0
		if w != nil && w.Params.PlanetRadiusKm > 0 {
			radius = w.Params.PlanetRadiusKm * 1000
		}
		type cand struct {
			t application.FoundedSettlement
			d float64
		}
		var cs []cand
		for _, f := range founded {
			if f.CityID == s.CityID {
				continue
			}
			if la, lo, ok := h.raidWorldPoint(ctx, f); ok {
				cs = append(cs, cand{f, raid.DistanceM(lat, lon, la, lo, radius)})
			}
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].d < cs[j].d || (cs[i].d == cs[j].d && cs[i].t.Code < cs[j].t.Code) })
		for i, c := range cs {
			if i >= 8 {
				break
			}
			n, err := tx.Settlements().ResidentCount(ctx, c.t.CityID)
			if err != nil {
				return v, err
			}
			v.Targets = append(v.Targets, village.RaidTarget{Settlement: presentation.Named{Code: c.t.Code, Name: c.t.Name}, DistanceKm: int64(math.Round(c.d / 1000)), Residents: n})
		}
	}
	v.Watch, err = h.watchLines(ctx, tx, snap, s)
	return v, err
}

func (h *VillageHandler) raidLine(ctx context.Context, tx application.Tx, s application.FoundedSettlement, p *application.Player, r application.SettlementRaid,
	names map[string]presentation.Named,
) (village.RaidLine, error) {
	side, other := village.RaidSideAttacker, r.DefenderID
	if r.DefenderID == s.CityID {
		side, other = village.RaidSideDefender, r.AttackerID
	}
	out := decodeOutcome(r.Outcome)
	line := village.RaidLine{ID: r.ID, Side: side, Other: names[other], Status: r.Status, StartAt: r.StartAt, EndAt: r.EndAt, Fee: r.Fee, Raiders: out.Raiders}
	if r.Tribute != nil {
		line.Tribute = *r.Tribute
	}
	parts, err := tx.Raids().Participants(ctx, r.ID)
	if err != nil {
		return line, err
	}
	for _, x := range parts {
		if x.Side == application.RaidAttacker {
			line.Joined++
		} else {
			line.Standing++
		}
		if x.PlayerID == p.ID {
			line.Me = true
		}
	}
	live := r.Status == application.RaidFiled || r.Status == application.RaidOpen
	line.CanJoin = live && side == village.RaidSideAttacker && !line.Me
	line.CanStand = live && side == village.RaidSideDefender && !line.Me
	line.CanOffer = r.Status == application.RaidFiled && side == village.RaidSideDefender
	line.CanAccept = r.Status == application.RaidFiled && side == village.RaidSideAttacker && line.Tribute > 0
	if side == village.RaidSideDefender {
		dets, err := tx.Raids().Detections(ctx, r.ID)
		if err != nil {
			return line, err
		}
		for _, d := range dets {
			if d.Detected && d.LeadSecs > 0 {
				line.Warned, line.LeadSecs = true, max(line.LeadSecs, d.LeadSecs)
				line.WarnedBy = d.Code
			}
		}
	}
	if out.Resolved || r.Status == application.RaidResolved {
		oc := &village.RaidOutcomeLine{Taken: out.Taken, Cash: out.Cash, Buildings: out.Buildings, Damage: out.Damage, Falls: int64(len(out.Falls)),
			AttackerUnits: out.AttUnits, DefenderUnits: out.DefUnits}
		for item, q := range out.Goods {
			oc.Goods = append(oc.Goods, village.RaidGoodsLine{Item: presentation.Named{Code: item, Name: item}, Qty: q})
		}
		sort.Slice(oc.Goods, func(i, j int) bool { return oc.Goods[i].Item.Code < oc.Goods[j].Item.Code })
		for _, id := range out.Falls {
			if id == p.ID {
				oc.FellMe = true
			}
		}
		line.Outcome = oc
	}
	return line, nil
}

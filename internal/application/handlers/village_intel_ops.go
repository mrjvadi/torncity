package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/intel"
	"github.com/mrjvadi/torncity/internal/domain/project"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// sabotageTry is an act of sabotage that passed the rules and waits for its roll: what it is about.
type sabotageTry struct {
	kind, refType, refID, unit string
	amount                     int64
	building                   *application.SettlementBuildingInstance
	project                    *application.SpecialProject
	item                       string
}

// sabotageAim checks the act against the rules of sabotage before the dice are thrown: the kind, the week, the target, the shield, the treaty, and
// that the thing aimed at is of the target and is the kind of thing this act may touch. Nothing here reaches a person or a home.
func (h *VillageHandler) sabotageAim(ctx context.Context, tx application.Tx, snap *content.Snapshot, sponsor, target application.FoundedSettlement, a *application.IntelAgent,
	kind, ref string, now time.Time,
) (*sabotageTry, error) {
	if ref == "" || len(ref) > 64 {
		return nil, refuseVillage(village.IntelBadArgs, village.AddrIntel)
	}
	rules := h.sabotageRules()
	acts, err := tx.Intel().ActsSince(ctx, sponsor.CityID, now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	last, err := tx.Intel().LastAct(ctx, target.CityID, kind, ref)
	if err != nil {
		return nil, err
	}
	blocked, err := h.aggressionBlocked(ctx, tx, snap, sponsor, target, now)
	if err != nil {
		return nil, err
	}
	hist := intel.History{ActsThisWeek: acts, LastSameAt: last, ShieldedOrGrace: target.ProtectedUntil.After(now), TreatyForbids: blocked != "", HasCover: true}
	if r := rules.Check(kind, hist, now); r != intel.Allowed {
		return nil, refuseVillage(village.IntelSabRefused+"_"+string(r), village.AddrIntel)
	}
	t := &sabotageTry{kind: kind, refID: ref}
	buildings, err := tx.SettlementBuildings().List(ctx, target.CityID)
	if err != nil {
		return nil, err
	}
	find := func(id string) *application.SettlementBuildingInstance {
		for i := range buildings {
			if buildings[i].ID == id && buildings[i].Status == "complete" {
				return &buildings[i]
			}
		}
		return nil
	}
	switch kind {
	case intel.DelayWork:
		pr, err := tx.Projects().ByID(ctx, ref, true)
		if err != nil {
			return nil, err
		}
		if pr == nil || pr.SettlementID != target.CityID || !pr.Active() {
			return nil, refuseVillage(village.IntelNoTarget, village.AddrIntel)
		}
		t.project, t.refType, t.amount, t.unit = pr, "special_project", h.special.Intel.SabotageDelayHours, "hours"
	case intel.SpoilStock:
		stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(target.CityID), application.HoldWarehouse)
		if err != nil {
			return nil, err
		}
		var have int64
		for _, st := range stacks {
			if st.Item == ref {
				have += st.Qty
			}
		}
		if have < 1 {
			return nil, refuseVillage(village.IntelNoTarget, village.AddrIntel)
		}
		t.item, t.refType, t.amount, t.unit = ref, "item", rules.Spoil(have), "units"
	case intel.BlindPost:
		b := find(ref)
		if b == nil {
			return nil, refuseVillage(village.IntelNoTarget, village.AddrIntel)
		}
		isDetector := false
		for _, d := range detectorPosts(snap, []application.SettlementBuildingInstance{*b}) {
			isDetector = isDetector || d.b.ID == b.ID
		}
		if !isDetector {
			return nil, refuseVillage(village.IntelNoTarget, village.AddrIntel)
		}
		t.building, t.refType, t.amount, t.unit = b, "building", rules.BlindBPS, "bps"
	case intel.SkipServiceDay:
		b := find(ref)
		if b == nil || len(servicePosts(snap, []application.SettlementBuildingInstance{*b})) == 0 {
			return nil, refuseVillage(village.IntelNoTarget, village.AddrIntel)
		}
		t.building, t.refType, t.amount, t.unit = b, "building", 1, "day"
	}
	return t, nil
}

// sabotageDo carries out an act that succeeded: small, capped and written down with how it is mended.
func (h *VillageHandler) sabotageDo(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, sponsor, target application.FoundedSettlement, a *application.IntelAgent,
	op application.IntelOp, t *sabotageTry, now time.Time,
) error {
	rules := h.sabotageRules()
	act := application.SabotageAct{ID: h.ids.NewID(), SponsorID: sponsor.CityID, TargetID: target.CityID, AgentID: a.ID, Kind: t.kind, RefType: t.refType, RefID: t.refID,
		Amount: t.amount, Unit: t.unit, Cost: op.Fee, At: now}
	var eff *application.SabotageEffect
	switch t.kind {
	case intel.DelayWork:
		var standing time.Time
		if t.project.DelayedUntil != nil {
			standing = *t.project.DelayedUntil
		}
		if err := tx.Projects().SetDelay(ctx, t.project.ID, rules.Delay(standing, now)); err != nil {
			return err
		}
		act.Repair = "passes"
	case intel.SpoilStock:
		org := application.SettlementOrg(target.CityID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return err
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: t.item, Qty: t.amount, FromOrg: org, FromHolding: application.HoldWarehouse,
			Reason: application.ItemSabotageSpoil, ReferenceType: application.SabotageReference, ReferenceID: act.ID, At: now}); err != nil {
			if stderrors.Is(err, application.ErrNotEnoughItems) {
				return refuseVillage(village.IntelNoTarget, village.AddrIntel)
			}
			return err
		}
		act.Repair = "replace"
	case intel.BlindPost:
		until := now.Add(24 * time.Hour)
		eff = &application.SabotageEffect{ActID: act.ID, Kind: intel.BlindPost, BuildingID: t.building.ID, Until: &until, BPS: int(rules.BlindBPS)}
		act.Repair = "recovers"
	case intel.SkipServiceDay:
		day := h.service.Clock.DayAtIn(now, target.Zone())
		if d, err := tx.ServiceDays().Day(ctx, target.CityID, day); err != nil {
			return err
		} else if d != nil {
			day++
		}
		eff = &application.SabotageEffect{ActID: act.ID, Kind: intel.SkipServiceDay, BuildingID: t.building.ID, Day: &day}
		act.Repair = "catch_up"
	}
	if err := tx.Intel().InsertAct(ctx, act, eff); err != nil {
		return err
	}
	return specialEvent(ctx, tx, meta, "sabotage_done", target.CityID, map[string]any{"act": act.ID, "kind": t.kind})
}

// surveyReport collects the metrics of a survey, names each by its band and writes the report. A partial report is vaguer by a band.
func (h *VillageHandler) surveyReport(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.IntelOpDef, op application.IntelOp, sponsor, target application.FoundedSettlement,
	partial bool, conf string, now time.Time,
) error {
	bands := map[string]intel.Band{}
	fields := intel.Fields{}
	for _, m := range def.Metrics {
		v, err := h.metricOf(ctx, tx, snap, target, m.Code)
		if err != nil {
			return err
		}
		bands[m.Code] = m.Band()
		fields[m.Code] = m.Band().Of(v)
	}
	if partial {
		fields = intel.Widen(fields, bands)
	}
	if err := fields.Check(bands); err != nil {
		return err
	}
	rules := h.intelRules()
	return tx.Intel().InsertReport(ctx, application.IntelReport{ID: h.ids.NewID(), OpID: op.ID, SponsorID: sponsor.CityID, TargetID: target.CityID, Kind: def.Code, Confidence: conf,
		Fields: map[string]string(fields), TakenAt: now, ExpiresAt: rules.Fresh(now)})
}

var (
	foodItems     = []string{"wheat", "flour", "bread", "rice", "lentils", "dried_fruit", "flour_sack"}
	materialItems = []string{"timber", "plank", "stone", "brick", "clay", "bar_iron"}
)

// metricOf reads one figure of the target for a survey. The figure goes no further than the band it falls in.
func (h *VillageHandler) metricOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, target application.FoundedSettlement, metric string) (int64, error) {
	sum := func(items []string) (int64, error) {
		stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(target.CityID), application.HoldWarehouse)
		if err != nil {
			return 0, err
		}
		want := map[string]bool{}
		for _, i := range items {
			want[i] = true
		}
		var n int64
		for _, st := range stacks {
			if want[st.Item] {
				n += st.Qty
			}
		}
		return n, nil
	}
	switch metric {
	case "treasury":
		return treasuryBalance(ctx, tx, target.CityID)
	case "food":
		return sum(foodItems)
	case "materials":
		return sum(materialItems)
	case "seats", "watch_posts":
		buildings, err := tx.SettlementBuildings().List(ctx, target.CityID)
		if err != nil {
			return 0, err
		}
		day, err := h.SettleServiceDay(ctx, tx, snap, target, buildings)
		if err != nil || day == nil {
			return 0, err
		}
		if metric == "seats" {
			return h.garrisonSeats(day), nil
		}
		var n int64
		for _, p := range day.Posts {
			if p.Held && detectorServices[p.Service] {
				n++
			}
		}
		return n, nil
	case "pending":
		ps, err := tx.Unions().ProposalsOf(ctx, target.CityID)
		if err != nil {
			return 0, err
		}
		var n int64
		for _, p := range ps {
			if p.Status == "pending" {
				n++
			}
		}
		return n, nil
	case "progress", "shortfall":
		projects, err := tx.Projects().OfSettlement(ctx, target.CityID, 10)
		if err != nil {
			return 0, err
		}
		var pct, short int64
		for _, pr := range projects {
			if !pr.Active() {
				continue
			}
			def, ok := snap.SpecialProject(pr.Kind)
			if !ok {
				continue
			}
			if p := int64(pr.Stage-1) * 100 / int64(pr.Stages); p > pct {
				pct = p
			}
			st, ok := stageNow(def, &pr)
			if !ok {
				continue
			}
			stock, err := tx.Projects().Stock(ctx, pr.ID)
			if err != nil {
				return 0, err
			}
			var need, miss int64
			for _, q := range st.Materials {
				need += q
			}
			for _, q := range project.Missing(domainStage(st), stock) {
				miss += q
			}
			if need > 0 && miss*100/need > short {
				short = miss * 100 / need
			}
		}
		if metric == "progress" {
			return pct, nil
		}
		return short, nil
	case "vacant":
		st, err := loadCharterState(ctx, tx, target, "head")
		if err != nil {
			return 0, err
		}
		var n int64
		for _, o := range st.offices {
			if o.Closed || o.Acquisition == charter.AcquireHead {
				continue
			}
			if len(st.seats[o.ID]) < o.Seats {
				n++
			}
		}
		return n, nil
	}
	return 0, nil
}

// intelShare passes a report to another settlement's bureau: an explicit, audited act. The recipient sees the bands, with the report's age.
func (h *VillageHandler) intelShare(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, req VillageIntelRequest, now time.Time) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.IntelShare); err != nil {
		return err
	}
	rep, err := tx.Intel().ReportByID(ctx, strings.TrimSpace(req.ID))
	if err != nil {
		return err
	}
	if rep == nil || rep.SponsorID != s.CityID {
		return refuseVillage(village.IntelNoReport, village.AddrIntel)
	}
	to, err := h.settlementByCode(ctx, tx, strings.TrimSpace(req.Code))
	if err != nil {
		return err
	}
	if to == nil || to.CityID == s.CityID {
		return refuseVillage(village.IntelNoParty, village.AddrIntel)
	}
	_, err = tx.Intel().AddShare(ctx, rep.ID, "settlement", to.CityID, p.ID, now)
	return err
}

func (h *VillageHandler) intelVet(ctx context.Context, tx application.Tx, s application.FoundedSettlement, has bool, p *application.Player, req VillageIntelRequest, now time.Time) error {
	if !has {
		return refuseVillage(village.VillageNoSettlement)
	}
	if _, err := requirePermission(ctx, tx, s, p.ID, charter.CounterVet); err != nil {
		return err
	}
	days, err := strconv.Atoi(strings.TrimSpace(req.N))
	if err != nil || days < 0 || days > 60 {
		return refuseVillage(village.IntelBadArgs, village.AddrIntel)
	}
	return tx.Intel().SetVetting(ctx, s.CityID, days, p.ID, now)
}

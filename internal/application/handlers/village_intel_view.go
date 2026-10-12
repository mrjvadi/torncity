package handlers

import (
	"context"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/intel"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// intelView builds the intelligence screen. The reports show band names; the agents of other bureaus are never shown; the acts done to the
// settlement show the kind and the day, and name the sponsor only to the sponsor.
func (h *VillageHandler) intelView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, has bool, p *application.Player) (village.IntelView, error) {
	v := village.IntelView{Open: h.intelOpen(), SabotageOpen: h.sabotageOpen()}
	if has {
		v.Village = s.Name
	}
	if !v.Open {
		return v, nil
	}
	now := h.now()
	named := func(id string) presentation.Named {
		st, err := tx.Settlements().ByID(ctx, id)
		if err != nil {
			return presentation.Named{}
		}
		return presentation.Named{Code: st.Code, Name: st.Name}
	}
	// the viewer's own engagements and offers, wherever they work
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return v, err
	}
	for _, f := range founded {
		if !has || f.CityID != s.CityID {
			v.Targets = append(v.Targets, village.IntelTargetLine{Target: presentation.Named{Code: f.Code, Name: f.Name}, Kind: "settlement"})
		}
	}
	tune := h.special.Intel
	v.Caps = village.IntelCaps{WeeklyLimit: tune.SabotageWeeklyLimit, DelayHours: tune.SabotageDelayHours, SpoilPercent: tune.SabotageSpoilBPS / 100, BlindPercent: tune.SabotageBlindBPS / 100,
		SkipDays: tune.SabotageSkipDays, PerTargetDays: tune.SabotagePerTargetDays, Cost: tune.SabotageCost}
	if has {
		if v.Caps.UsedThisWeek, err = tx.Intel().ActsSince(ctx, s.CityID, now.Add(-7*24*time.Hour)); err != nil {
			return v, err
		}
	}
	mineAgents, err := tx.Intel().AgentsOfPlayer(ctx, p.ID, 20)
	if err != nil {
		return v, err
	}
	for _, a := range mineAgents {
		line := village.IntelAgentLine{ID: a.ID, Player: presentation.Named{Code: p.PublicCode, Name: h.playerName(ctx, tx, p.ID)}, Sponsor: named(a.SponsorID), Target: named(a.TargetID),
			Status: a.Status, Mine: true, CreatedAt: a.CreatedAt, EndedAt: a.EndedAt}
		switch a.Status {
		case "offered":
			line.MayAccept = true
			v.Offers = append(v.Offers, line)
		case "active":
			line.MayRecall, line.MayOperate = true, true
			if a.CoverSince != nil {
				line.CoverDays = int64(now.Sub(*a.CoverSince) / (24 * time.Hour))
			}
			n, err := tx.Intel().OpsToday(ctx, a.ID, gameDayNumber(now))
			if err != nil {
				return v, err
			}
			line.OpsToday = n
			if h.sabotageOpen() {
				if line.Choices, err = h.sabotageChoices(ctx, tx, snap, a.TargetID); err != nil {
					return v, err
				}
			}
			v.Agents = append(v.Agents, line)
		}
	}
	for _, d := range snap.IntelOps() {
		missing := []string(nil)
		if has {
			if missing, err = knowledgeMissing(ctx, tx, s.CityID, needsKnowledge(d.Requires)); err != nil {
				return v, err
			}
		}
		v.Ops = append(v.Ops, village.IntelOpLine{Code: d.Code, Name: d.Name, Kind: d.Kind, Available: len(missing) == 0, Missing: missing})
	}
	if !has {
		return v, nil
	}
	v.MayRun = hasPermission(ctx, tx, s, p.ID, charter.IntelRun)
	v.MayRead = hasPermission(ctx, tx, s, p.ID, charter.IntelRead)
	v.MayVet = hasPermission(ctx, tx, s, p.ID, charter.CounterVet)
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	if b := bureauOf(buildings); b != nil {
		v.BureauBuilt = true
		if day, err := tx.ServiceDays().Last(ctx, s.CityID); err == nil && day != nil {
			v.BureauOpen = day.HeldPost(b.ID)
		}
	}
	if v.VettingDays, err = tx.Intel().Vetting(ctx, s.CityID); err != nil {
		return v, err
	}
	posts, err := h.watchPosts(ctx, tx, snap, s)
	if err != nil {
		return v, err
	}
	residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	switch c := h.intelRules().Counter(int64(len(posts)), v.VettingDays > 0, residents); {
	case c <= 0:
		v.Counter = "none"
	case c < 3*h.special.Intel.CounterPostBPS:
		v.Counter = "light"
	default:
		v.Counter = "solid"
	}
	if v.MayRun {
		sponsored, err := tx.Intel().AgentsOfSponsor(ctx, s.CityID, 30)
		if err != nil {
			return v, err
		}
		for _, a := range sponsored {
			if a.PlayerID == p.ID {
				continue
			}
			line := village.IntelAgentLine{ID: a.ID, Player: presentation.Named{Code: a.PlayerID, Name: h.playerName(ctx, tx, a.PlayerID)}, Sponsor: named(a.SponsorID), Target: named(a.TargetID),
				Status: a.Status, MayRecall: a.Status == "active" || a.Status == "offered", CreatedAt: a.CreatedAt, EndedAt: a.EndedAt}
			v.Agents = append(v.Agents, line)
		}
	}
	if v.MayRead {
		reps, err := tx.Intel().ReportsOfSponsor(ctx, s.CityID, 30)
		if err != nil {
			return v, err
		}
		shared, err := tx.Intel().SharedTo(ctx, "settlement", s.CityID, 30)
		if err != nil {
			return v, err
		}
		add := func(reps []application.IntelReport, sharedIn bool) {
			for _, r := range reps {
				line := village.IntelReportLine{ID: r.ID, Op: r.Kind, Target: named(r.TargetID), Confidence: r.Confidence, TakenAt: r.TakenAt, Old: h.intelRules().Aged(r.TakenAt, now), Shared: sharedIn,
					MayShare: !sharedIn && hasPermission(ctx, tx, s, p.ID, charter.IntelShare)}
				for _, k := range sortedFieldKeys(r.Fields) {
					line.Fields = append(line.Fields, village.IntelFieldLine{Metric: k, Band: r.Fields[k]})
				}
				v.Reports = append(v.Reports, line)
			}
		}
		add(reps, false)
		add(shared, true)
	}
	acts, err := tx.Intel().ActsOf(ctx, s.CityID, 20)
	if err != nil {
		return v, err
	}
	for _, a := range acts {
		line := village.IntelActLine{Kind: a.Kind, Done: true, ByUs: a.SponsorID == s.CityID, Target: named(a.TargetID), At: a.At, Repair: a.Repair, Repaired: a.RepairedAt != nil}
		if line.ByUs {
			sp := named(a.SponsorID)
			line.Sponsor = &sp
		}
		v.Acts = append(v.Acts, line)
	}
	exp, err := tx.Intel().ExpulsionsOf(ctx, s.CityID, 20)
	if err != nil {
		return v, err
	}
	for _, e := range exp {
		line := village.IntelExpulsionLine{Until: e.Until, At: e.At}
		if e.Attributed {
			sp := named(e.SponsorID)
			line.Sponsor = &sp
		}
		v.Expelled = append(v.Expelled, line)
	}
	return v, nil
}

func sortedFieldKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sabotageChoices lists what each act may be aimed at in the target: its projects in progress, the kinds of goods in its warehouse (no
// quantities) and its detector and service buildings. Only what a sponsor could plausibly see from outside.
func (h *VillageHandler) sabotageChoices(ctx context.Context, tx application.Tx, snap *content.Snapshot, targetID string) ([]village.IntelChoiceLine, error) {
	var out []village.IntelChoiceLine
	projects, err := tx.Projects().OfSettlement(ctx, targetID, 20)
	if err != nil {
		return nil, err
	}
	for _, pr := range projects {
		if !pr.Active() {
			continue
		}
		name := pr.Kind
		if d, ok := snap.SpecialProject(pr.Kind); ok {
			name = d.Name
		}
		out = append(out, village.IntelChoiceLine{Op: intel.DelayWork, Ref: presentation.Named{Code: pr.ID, Name: name}, Kind: "project"})
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(targetID), application.HoldWarehouse)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	sort.Slice(stacks, func(i, j int) bool { return stacks[i].Item < stacks[j].Item })
	for _, st := range stacks {
		if st.Qty < 1 || seen[st.Item] {
			continue
		}
		seen[st.Item] = true
		name := st.Item
		if d, ok := snap.ItemDef(st.Item); ok {
			name = d.Name
		}
		out = append(out, village.IntelChoiceLine{Op: intel.SpoilStock, Ref: presentation.Named{Code: st.Item, Name: name}, Kind: "item"})
	}
	buildings, err := tx.SettlementBuildings().List(ctx, targetID)
	if err != nil {
		return nil, err
	}
	for _, d := range detectorPosts(snap, buildings) {
		out = append(out, village.IntelChoiceLine{Op: intel.BlindPost, Ref: presentation.Named{Code: d.b.ID, Name: h.buildingName(d.b.TypeCode)}, Kind: "building"})
	}
	for _, d := range servicePosts(snap, buildings) {
		out = append(out, village.IntelChoiceLine{Op: intel.SkipServiceDay, Ref: presentation.Named{Code: d.b.ID, Name: h.buildingName(d.b.TypeCode)}, Kind: "building"})
	}
	return out, nil
}

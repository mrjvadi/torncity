package handlers

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	ratio "github.com/mrjvadi/torncity/internal/domain/chain"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The ratio view (docs/adr/0075 8). It is computed, not a mechanic: from the rows of the workplaces and from what worked in the last hour
// it says how fast each kind of workplace makes and uses its goods, how a node's inputs compare with what is made upstream, and, for a
// target, the chain of buildings it needs and the first link that cannot keep up.

// chainWindow is how far back a workplace counts as running.
const chainWindow = time.Hour

// VillageChainRequest is the payload of settlement.chain: the target item and the rate (units an hour) the chain is planned for.
type VillageChainRequest struct {
	Code string `json:"code,omitempty"`
	N    string `json:"n,omitempty"`
}

// chainNodes are the workplaces of the content seen as rates, the cheaper tier of a good first (a pit before a mine).
func chainNodes(snap *content.Snapshot) []ratio.Node {
	defs := snap.SettlementWorkplaces()
	sort.SliceStable(defs, func(i, j int) bool {
		if defs[i].Tier != defs[j].Tier {
			return defs[i].Tier < defs[j].Tier
		}
		return defs[i].Code < defs[j].Code
	})
	var out []ratio.Node
	for _, d := range defs {
		if len(d.Produces) == 0 {
			continue
		}
		out = append(out, ratio.Node{Code: d.Code, ShiftMinutes: shiftMinutes(d), Slots: int64(d.Workers), In: copyQty(d.Consumes), Out: copyQty(d.Produces)})
	}
	return out
}

// Chain handles settlement.chain.
func (h *VillageHandler) Chain(ctx context.Context, meta envelope.Metadata, req VillageChainRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.ChainView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		view, err = h.chainView(ctx, tx, snap, s, strings.TrimSpace(req.Code), strings.TrimSpace(req.N))
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Chain(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) chainView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, target, perHour string) (village.ChainView, error) {
	v := village.ChainView{Village: s.Name, Open: h.mining.Enabled()}
	nodes := chainNodes(snap)
	now := h.now()
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	stats, err := tx.Mines().Stats(ctx, s.CityID, now.Add(-chainWindow).Truncate(time.Hour))
	if err != nil {
		return v, err
	}
	activeBuilding := map[string]bool{}
	lastHour := map[string]map[string]int64{}
	typeOf := map[string]string{}
	for _, b := range buildings {
		typeOf[b.ID] = b.TypeCode
	}
	for _, st := range stats {
		if st.Shifts > 0 {
			activeBuilding[st.BuildingID] = true
		}
		code := typeOf[st.BuildingID]
		if lastHour[code] == nil {
			lastHour[code] = map[string]int64{}
		}
		for it, q := range st.Produced {
			lastHour[code][it] += q
		}
	}
	built := map[string]int{}
	running := map[string]int64{}
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		built[b.TypeCode]++
		if activeBuilding[b.ID] {
			running[b.TypeCode]++
		}
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(s.CityID), application.HoldWarehouse)
	if err != nil {
		return v, err
	}
	stock := map[string]int64{}
	for _, st := range stacks {
		stock[st.Item] += st.Qty
	}
	byCode := map[string]ratio.Node{}
	for _, n := range nodes {
		byCode[n.Code] = n
	}
	// the supply of each item by the running workplaces
	supply := map[string]int64{}
	makers := map[string][]string{}
	for _, n := range nodes {
		for it := range n.Out {
			supply[it] += running[n.Code] * n.OutMilli(it)
			if built[n.Code] > 0 {
				makers[it] = append(makers[it], n.Code)
			}
		}
	}
	nameOf := func(code string) presentation.Named { return named(code, snapItemName(snap, code)) }
	rates := func(n ratio.Node, in bool) []village.ChainRateLine {
		src := n.Out
		if in {
			src = n.In
		}
		var out []village.ChainRateLine
		for _, it := range materialCodes(src) {
			m := n.OutMilli(it)
			if in {
				m = n.InMilli(it)
			}
			out = append(out, village.ChainRateLine{Item: nameOf(it), Milli: m})
		}
		return out
	}
	for _, n := range nodes {
		if built[n.Code] == 0 {
			continue
		}
		d, _ := snap.SettlementBuildingDef(n.Code)
		line := village.ChainNodeLine{Node: named(n.Code, d.Name), Built: built[n.Code], Running: int(running[n.Code]), ShiftMinutes: n.ShiftMinutes, Slots: n.Slots, Out: rates(n, false), In: rates(n, true)}
		for _, it := range materialCodes(n.In) {
			feed := village.ChainFeedLine{Item: nameOf(it), NeedMilli: running[n.Code] * n.InMilli(it), HaveMilli: supply[it], Stock: stock[it]}
			for _, mc := range makers[it] {
				md, _ := snap.SettlementBuildingDef(mc)
				feed.Makers = append(feed.Makers, named(mc, md.Name))
			}
			line.Feeds = append(line.Feeds, feed)
		}
		for _, it := range materialCodes(lastHour[n.Code]) {
			line.LastHour = append(line.LastHour, village.ChainRateLine{Item: nameOf(it), Milli: lastHour[n.Code][it] * 1000})
		}
		v.Nodes = append(v.Nodes, line)
	}
	seen := map[string]bool{}
	for _, n := range nodes {
		for _, it := range materialCodes(n.Out) {
			if !seen[it] {
				seen[it] = true
				v.Targets = append(v.Targets, nameOf(it))
			}
		}
	}
	if target == "" {
		return v, nil
	}
	rate := h.mining.ChainDefaultPerHour * ratio.Milli
	if perHour != "" {
		if n, err := strconv.ParseInt(perHour, 10, 64); err == nil && n > 0 {
			rate = n * ratio.Milli
		} else {
			return v, refuseVillage(village.ChainBadArgs, village.AddrChain)
		}
	}
	plan, err := ratio.Compute(target, rate, nodes)
	if err != nil {
		return v, refuseVillage(village.ChainNoMaker, village.AddrChain)
	}
	v.Target, v.PerHourMilli = nameOf(target), rate
	starved := ratio.Check(plan, nodes, running)
	bad := map[string]ratio.Starved{}
	for _, st := range starved {
		bad[st.Item] = st
	}
	for _, l := range plan.Lines {
		n := byCode[l.Node]
		d, _ := snap.SettlementBuildingDef(l.Node)
		ll := village.ChainLineLine{Node: named(l.Node, d.Name), Item: nameOf(l.Item), NeedMilli: l.NeedMilli, Count: l.Count, Running: running[l.Node], HaveMilli: running[l.Node] * n.OutMilli(l.Item), Depth: l.Depth}
		if st, ok := bad[l.Item]; ok {
			ll.Starved, ll.Add = true, st.Add
		}
		v.Lines = append(v.Lines, ll)
	}
	if first, ok := ratio.First(starved); ok {
		for i := range v.Lines {
			if v.Lines[i].Item.Code == first.Item {
				l := v.Lines[i]
				v.First = &l
			}
		}
	}
	for _, it := range materialCodes(plan.Raw) {
		v.Raw = append(v.Raw, village.ChainRawLine{Item: nameOf(it), NeedMilli: plan.Raw[it], Stock: stock[it]})
	}
	return v, nil
}

package handlers

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// The research desk (ADR 0048): the board of slots, scholars, running projects, pacts and breakthrough progress, and
// the four things a viewer can do there: take or leave a scholar's post, offer, answer or end a research-sharing pact.

// maxPactNeighbours is how many settlements the desk offers a pact to at once (a screen's worth, not a rule).
const maxPactNeighbours = 12

// VillageResearchRequest is settlement.research: an act and its argument (the building id of a post, the code of the
// settlement to offer a pact to, or the id of a pact). No act shows the board.
type VillageResearchRequest struct {
	Action string `json:"action,omitempty"`
	Code   string `json:"code,omitempty"`
}

// ResearchDesk handles settlement.research.
func (h *VillageHandler) ResearchDesk(ctx context.Context, meta envelope.Metadata, req VillageResearchRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.ResearchBoardView
	act := strings.TrimSpace(req.Action)
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
		if act != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if err := tx.Research().LockSettlement(ctx, s.CityID); err != nil {
					return err
				}
				if err := h.researchAct(ctx, tx, meta, snap, p, s, act, strings.TrimSpace(req.Code)); err != nil {
					return err
				}
			}
		}
		view, err = h.researchBoard(ctx, tx, snap, p, s)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.ResearchBoard(h.screen(meta, lang), view), nil
}

// researchAct does one act of the desk.
func (h *VillageHandler) researchAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	p *application.Player, s application.FoundedSettlement, act, code string,
) error {
	now := h.now()
	back := village.AddrResearchDesk
	switch act {
	case village.ResearchActionPost:
		if ok, err := h.resident(ctx, tx, p.ID, s.CityID); err != nil {
			return err
		} else if !ok {
			return refuseVillage(village.VillageNotResident, back)
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		var site *researchSite
		for _, rs := range researchSites(snap, buildings) {
			if rs.b.ID == code {
				rs := rs
				site = &rs
			}
		}
		if site == nil {
			return refuseVillage(village.ResearchNoPost, back)
		}
		posts, err := tx.Research().Posts(ctx, s.CityID)
		if err != nil {
			return err
		}
		held := 0
		for _, po := range posts {
			if po.BuildingID == site.b.ID {
				held++
			}
		}
		if held >= site.posts {
			return refuseVillage(village.ResearchNoPost, back)
		}
		if err := tx.Research().TakePost(ctx, application.ResearchPost{BuildingID: site.b.ID, PlayerID: p.ID, SettlementID: s.CityID, Since: now}); err != nil {
			if isSentinel(err, application.ErrResearchPostHeld) {
				return refuseVillage(village.ResearchPostHeld, back)
			}
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "research_post_taken", s.CityID, map[string]any{
			"settlement_id": s.CityID, "building_id": site.b.ID, "player_id": p.ID})
	case village.ResearchActionLeave:
		left, err := tx.Research().LeavePost(ctx, p.ID)
		if err != nil {
			return err
		}
		if !left {
			return refuseVillage(village.ResearchNoPostHeld, back)
		}
		return appendVillageEvent(ctx, tx, meta, "research_post_left", s.CityID, map[string]any{
			"settlement_id": s.CityID, "player_id": p.ID})
	case village.ResearchActionPropose, village.ResearchActionAccept, village.ResearchActionDecline, village.ResearchActionEnd:
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.ResearchShare); err != nil {
			return err
		}
		return h.researchPactAct(ctx, tx, meta, s, p, act, code, now)
	}
	return refuseVillage(village.VillageNotFound, back)
}

// researchPactAct is the pact half of the desk.
func (h *VillageHandler) researchPactAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, s application.FoundedSettlement,
	p *application.Player, act, code string, now time.Time,
) error {
	back := village.AddrResearchDesk
	repo := tx.Research()
	if act == village.ResearchActionPropose {
		all, err := tx.Settlements().Founded(ctx)
		if err != nil {
			return err
		}
		var target *application.FoundedSettlement
		for i := range all {
			if all[i].Code == code {
				target = &all[i]
			}
		}
		switch {
		case target == nil:
			return refuseVillage(village.VillageNotFound, back)
		case target.CityID == s.CityID:
			return refuseVillage(village.ResearchPactSelf, back)
		}
		id := h.ids.NewID()
		if err := repo.ProposePact(ctx, application.ResearchPact{ID: id, A: s.CityID, B: target.CityID, ProposedBy: p.ID, ProposedAt: now}); err != nil {
			if isSentinel(err, application.ErrResearchPactOpen) {
				return refuseVillage(village.ResearchPactOpen, back)
			}
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "research_pact_proposed", s.CityID, map[string]any{
			"settlement_id": s.CityID, "partner_id": target.CityID, "pact_id": id})
	}
	pact, err := repo.PactByID(ctx, code)
	if isSentinel(err, application.ErrResearchPactNotFound) {
		return refuseVillage(village.ResearchPactNotFound, back)
	}
	if err != nil {
		return err
	}
	if pact.A != s.CityID && pact.B != s.CityID {
		return refuseVillage(village.ResearchPactNotFound, back)
	}
	switch act {
	case village.ResearchActionAccept, village.ResearchActionDecline:
		// only the settlement the offer was made to answers it
		if pact.B != s.CityID || pact.Status != application.PactProposed {
			return refuseVillage(village.ResearchPactNotFound, back)
		}
		changed, err := repo.AnswerPact(ctx, pact.ID, act == village.ResearchActionAccept, now)
		if err != nil || !changed {
			return err
		}
		name := "research_pact_declined"
		if act == village.ResearchActionAccept {
			name = "research_pact_active"
		}
		return appendVillageEvent(ctx, tx, meta, name, s.CityID, map[string]any{
			"settlement_id": s.CityID, "partner_id": pact.A, "pact_id": pact.ID})
	default: // end
		changed, err := repo.EndPact(ctx, pact.ID, now)
		if err != nil || !changed {
			return err
		}
		return appendVillageEvent(ctx, tx, meta, "research_pact_ended", s.CityID, map[string]any{
			"settlement_id": s.CityID, "partner_id": pact.Other(s.CityID), "pact_id": pact.ID})
	}
}

// researchBoard builds the desk.
func (h *VillageHandler) researchBoard(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	s application.FoundedSettlement,
) (village.ResearchBoardView, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return village.ResearchBoardView{}, err
	}
	literacy, _, err := tx.SettlementKnowledge().Literacy(ctx, s.CityID)
	if err != nil {
		return village.ResearchBoardView{}, err
	}
	rc, err := h.researchContext(ctx, tx, snap, s, buildings, int64(literacy))
	if err != nil {
		return village.ResearchBoardView{}, err
	}
	rules := rc.rules
	st := rc.state
	view := village.ResearchBoardView{Name: s.Name, Capacity: st.Capacity, Running: len(st.Running), Frontier: rc.frontier,
		LiteracyPercent: literacy / 100, ShareCapBPS: rules.ShareCapBPS}
	view.MayShare = hasPermission(ctx, tx, s, p.ID, charter.ResearchShare)

	byID := map[string]researchSite{}
	for _, site := range st.Sites {
		byID[site.b.ID] = site
	}
	buildingName := func(id string) presentation.Named {
		if site, ok := byID[id]; ok {
			return named(site.b.TypeCode, site.typeName)
		}
		return presentation.Named{}
	}
	for _, sl := range st.Slots {
		view.Slots = append(view.Slots, village.ResearchSlotLine{Ref: sl.Ref, Building: buildingName(sl.Ref), Capacity: sl.Capacity,
			Used: st.UsedBySlot[sl.Ref], BonusBPS: sl.BonusBPS, StaffBPS: sl.StaffBPS})
	}

	// the buildings, their posts and today's story
	posts, err := tx.Research().Posts(ctx, s.CityID)
	if err != nil {
		return village.ResearchBoardView{}, err
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(s.CityID), application.HoldWarehouse)
	if err != nil {
		return village.ResearchBoardView{}, err
	}
	stock := map[string]int64{}
	for _, sk := range stacks {
		stock[sk.Item] += sk.Qty
	}
	var wagePer int64
	if len(h.labor.Curve) >= 4 {
		if market, err := h.laborMarket(ctx, tx, snap, s, buildings); err == nil {
			wagePer = market.line.NPCWage
		}
	}
	mine, err := tx.Research().PostOf(ctx, p.ID)
	if err != nil {
		return village.ResearchBoardView{}, err
	}
	for _, site := range st.Sites {
		line := village.ResearchBuildingLine{ID: site.b.ID, Building: named(site.b.TypeCode, site.typeName), Slots: site.def.Research.Slots,
			Needed: site.def.Research.MinStaff, Posts: site.posts, BonusBPS: int64(site.def.Research.BonusBPS),
			Wage: wagePer * site.wageBPS / 10_000}
		held := 0
		for _, po := range posts {
			if po.BuildingID == site.b.ID {
				held++
			}
		}
		row, today := st.Rows[site.b.ID]
		line.Open = today && row.Staffed
		if line.Open {
			line.Players = min(held, site.posts)
			line.NPCs = max(len(row.Skills)-line.Players, 0)
		} else {
			line.Players = min(held, site.posts)
			line.Idle = row.Idle
		}
		items := make([]string, 0, len(site.def.Research.Upkeep))
		for it := range site.def.Research.Upkeep {
			items = append(items, it)
		}
		sort.Strings(items)
		for _, it := range items {
			ul := village.ResearchUpkeepLine{Item: itemNamed(snap, it), Qty: int64(site.def.Research.Upkeep[it]), Have: stock[it]}
			if si := h.realItems.standIns(snap, h.now())[it]; si != "" {
				ul.StandIn, ul.StandInHave = itemNamed(snap, si), stock[si]
				view.StandInUntil = h.realItems.GraceUntil()
			}
			line.Upkeep = append(line.Upkeep, ul)
		}
		line.Mine = mine != nil && mine.BuildingID == site.b.ID
		line.CanTake = mine == nil && held < site.posts
		view.Buildings = append(view.Buildings, line)
	}

	// the running projects with the quote each started on
	for _, r := range st.Running {
		d, _ := snap.SettlementKnowledgeDef(r.Code)
		view.Projects = append(view.Projects, village.ResearchProjectLine{Knowledge: named(d.Code, d.Name), Slot: r.SlotRef,
			Building: buildingName(r.SlotRef), SpeedBPS: r.SpeedBPS, AheadBPS: r.AheadBPS, DiscountBPS: r.DiscountBPS, ShareBPS: r.ShareBPS,
			FinishAt: r.FinishAt, Left: countdownTo(r.FinishAt, h.now())})
	}

	// the pacts and the settlements one could be offered to
	pacts, err := tx.Research().Pacts(ctx, s.CityID)
	if err != nil {
		return village.ResearchBoardView{}, err
	}
	all, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return village.ResearchBoardView{}, err
	}
	nameOf := map[string]application.FoundedSettlement{}
	for _, f := range all {
		nameOf[f.CityID] = f
	}
	linked := map[string]bool{s.CityID: true}
	for _, pa := range pacts {
		other := pa.Other(s.CityID)
		linked[other] = true
		state := village.ResearchPactActive
		if pa.Status == application.PactProposed {
			state = village.ResearchPactOutgoing
			if pa.B == s.CityID {
				state = village.ResearchPactIncoming
			}
		}
		f := nameOf[other]
		view.Pacts = append(view.Pacts, village.ResearchPactLine{ID: pa.ID, Partner: named(f.Code, f.Name), State: state})
	}
	cands := make([]application.FoundedSettlement, 0, len(all))
	for _, f := range all {
		if !linked[f.CityID] {
			cands = append(cands, f)
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Name != cands[j].Name {
			return cands[i].Name < cands[j].Name
		}
		return cands[i].CityID < cands[j].CityID
	})
	for _, f := range cands[:min(len(cands), maxPactNeighbours)] {
		view.Neighbours = append(view.Neighbours, named(f.Code, f.Name))
	}

	// breakthrough progress
	fields := make([]string, 0, len(rc.experience))
	for f := range rc.experience {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		if rc.experience[f] > 0 {
			view.Experience = append(view.Experience, village.ResearchExperienceLine{Field: f, Points: rc.experience[f],
				Per: rules.BreakthroughNeedPerDepth, MaxBPS: rules.BreakthroughMaxBPS})
		}
	}
	return view, nil
}

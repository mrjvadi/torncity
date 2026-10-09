package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/research"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Research capacity and speed (ADR 0048, migration 0134). CLAUDE.md rule 1c for the library, the laboratory and the
// higher school (building_functions.yml, `research:` block):
//
//   - WHO WORKS THERE. Scholars. A resident takes a post (research_posts) and is paid a day's wage by the treasury
//     (ledger reason scholar_wage); the building needs `min_staff` scholars to open, and the labour pool's NPC citizens
//     fill what the players do not, up to that number (reason scholar_wage_npc, to the sink). Their seats are claimed
//     in the workforce ledger (seatClaims.Scholars).
//   - WHAT IT CONSUMES. The wages and the day's upkeep (paper, fuel, reagents: `research.upkeep`) out of the stock
//     (item reason research_upkeep). No wage money, no upkeep in stock, or too few scholars: the building STANDS IDLE
//     that day and gives no slot (`if_unstaffed: idle`).
//   - WHAT IT GIVES. Research slots (projects at once) while it works, and a speed bonus on every project in it. The
//     settlement always has one free slot, so nothing a live settlement had is lost.
//   - WHAT BREAKS. A project already running is never slowed or stopped when its building lapses: the quote it
//     started on is its own (speed_bps and the rest are on the row). Only a new project needs a working slot.
//
// # When the day is settled
//
// Once per local day of the settlement, fenced by research_days (like the stores' day): the first reader of the day
// pays the scholars, draws the upkeep and records which buildings worked, whatever replica or command it is.

// ResearchRules are the research tuning (config settlement.research_*) and the game clock that counts the research
// days. Without them every settlement has its one free slot, research costs what it always did, and no building
// opens a slot (the older wirings and tests).
type ResearchRules struct {
	Rules research.Rules
	Clock gametime.Clock
	// ExperiencePerShift is what a finished work shift adds to its field; ScholarXP the scholarship experience a
	// player scholar earns each working day.
	ExperiencePerShift, ScholarXP int64
}

func (r ResearchRules) enabled() bool { return r.Rules.Enabled() && r.Clock.Validate() == nil }

// rules are the rules a quote is worked on: the configured ones, or the plain old single project.
func (r ResearchRules) rules() research.Rules {
	if r.Rules.Enabled() {
		return r.Rules
	}
	return research.Rules{FreeSlots: 1, SpeedFloorBPS: research.BPS}
}

// WithResearch gives the village handler its research capacity and speed rules.
func (h *VillageHandler) WithResearch(r ResearchRules) *VillageHandler {
	h.research = r
	return h
}

// researchSite is a standing research building and what its function asks for.
type researchSite struct {
	b        application.SettlementBuildingInstance
	fn       string
	def      content.BuildingFunctionDef
	posts    int
	wageBPS  int64
	typeName string
}

// researchSites lists the standing research buildings in a fixed order: the type code, then the id.
func researchSites(snap *content.Snapshot, buildings []application.SettlementBuildingInstance) []researchSite {
	var out []researchSite
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		fn, ok := snap.FunctionReplacing(b.TypeCode)
		if !ok {
			continue
		}
		def, ok := snap.BuildingFunction(fn)
		if !ok || def.Research == nil {
			continue
		}
		site := researchSite{b: b, fn: fn, def: def, wageBPS: 10_000, typeName: def.Name}
		for _, st := range def.Staff {
			if st.Role == "scholar" {
				site.posts += st.Slots
				if st.WageBPS > 0 {
					site.wageBPS = int64(st.WageBPS)
				}
			}
		}
		if site.posts == 0 {
			continue
		}
		out = append(out, site)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].b.TypeCode != out[j].b.TypeCode {
			return out[i].b.TypeCode < out[j].b.TypeCode
		}
		return out[i].b.ID < out[j].b.ID
	})
	return out
}

// researchState is what the settlement can run today and what it runs.
type researchState struct {
	// Slots are the free slot and every building that worked today; Capacity their sum.
	Slots    []research.Slot
	Capacity int
	// Sites are the standing research buildings, Rows how today went in each (by building id).
	Sites []researchSite
	Rows  map[string]application.ResearchDayBuilding
	// Running are the running projects, UsedBySlot how many each slot has.
	Running    []application.SettlementResearch
	UsedBySlot map[string]int
}

// slot returns the slot with that ref.
func (st *researchState) slot(ref string) (research.Slot, bool) {
	for _, s := range st.Slots {
		if s.Ref == ref {
			return s, true
		}
	}
	return research.Slot{}, false
}

// free reports whether the slot has room for one more project (and the settlement as a whole too).
func (st *researchState) free(s research.Slot) bool {
	return len(st.Running) < st.Capacity && st.UsedBySlot[s.Ref] < s.Capacity
}

// researchToday settles the research day and reads the settlement's slots and running projects. It writes only the
// day (idempotently); callers that start a project lock the settlement first.
func (h *VillageHandler) researchToday(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance,
) (*researchState, error) {
	rules := h.research.rules()
	sites := researchSites(snap, buildings)
	st := &researchState{Sites: sites, Rows: map[string]application.ResearchDayBuilding{}, UsedBySlot: map[string]int{}}
	running, err := tx.Research().Running(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	st.Running = running
	for _, r := range running {
		st.UsedBySlot[r.SlotRef]++
	}
	var day *application.ResearchDay
	if h.research.enabled() && len(sites) > 0 {
		if day, err = h.settleResearchDay(ctx, tx, snap, s, buildings, sites); err != nil {
			return nil, err
		}
	}
	var open []research.Building
	if day != nil {
		for _, row := range day.Rows {
			st.Rows[row.BuildingID] = row
		}
		for _, site := range sites {
			row, ok := st.Rows[site.b.ID]
			if !ok || !row.Staffed {
				continue
			}
			open = append(open, research.Building{ID: site.b.ID, Slots: site.def.Research.Slots, MinStaff: site.def.Research.MinStaff,
				BonusBPS: int64(site.def.Research.BonusBPS), Skills: row.Skills})
		}
	}
	st.Slots = research.Slots(open, rules)
	st.Capacity = research.Capacity(st.Slots)
	return st, nil
}

// settleResearchDay brings today's research day up to date: if it has not been settled it decides which buildings
// worked, pays the scholars and draws the upkeep. It is idempotent: the row written first is the fence.
func (h *VillageHandler) settleResearchDay(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, sites []researchSite,
) (*application.ResearchDay, error) {
	repo := tx.Research()
	now := h.now()
	today := h.research.Clock.DayAtIn(now, s.Zone())
	if d, err := repo.Day(ctx, s.CityID, today); err != nil || d != nil {
		return d, err
	}
	d := application.ResearchDay{SettlementID: s.CityID, Day: today, Buildings: int64(len(sites)), At: now}

	// the people: players with a post, then the pool's NPCs up to what a building needs to open
	posts, err := repo.Posts(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	byBuilding := map[string][]application.ResearchPost{}
	for _, p := range posts {
		byBuilding[p.BuildingID] = append(byBuilding[p.BuildingID], p)
	}
	var npcFree, wagePer int64
	if len(h.labor.Curve) >= 4 {
		market, err := h.laborMarket(ctx, tx, snap, s, buildings)
		if err != nil {
			return nil, err
		}
		shopSeat := int64(0)
		if h.shop.enabled() {
			if _, ok := snap.VillageShop(); ok {
				shopSeat = 1
			}
		}
		// the seats the last day's scholars held are free to be filled again; the shop and the keepers are not
		npcFree = max(market.claims.StaffFree()+market.claims.Scholars-shopSeat-market.claims.Keepers-market.claims.Clerks-market.claims.Services, 0)
		wagePer = market.line.NPCWage
	}
	treasury, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return nil, err
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(s.CityID), application.HoldWarehouse)
	if err != nil {
		return nil, err
	}
	stock := map[string]int64{}
	for _, sk := range stacks {
		stock[sk.Item] += sk.Qty
	}

	type payee struct {
		id   string
		wage int64
	}
	var payees []payee
	var xp []string
	upkeep := map[string]int64{}
	for _, site := range sites {
		row := application.ResearchDayBuilding{BuildingID: site.b.ID, TypeCode: site.b.TypeCode}
		var skills []int
		var players []string
		for _, p := range byBuilding[site.b.ID] {
			if len(players) >= site.posts {
				break
			}
			// a scholar who has moved away holds the post no longer
			if ok, err := h.resident(ctx, tx, p.PlayerID, s.CityID); err != nil {
				return nil, err
			} else if !ok {
				if _, err := repo.LeavePost(ctx, p.PlayerID); err != nil {
					return nil, err
				}
				continue
			}
			lv := 0
			sk, err := tx.Skills().Get(ctx, p.PlayerID, "scholarship")
			switch {
			case err == nil:
				lv = sk.Level
			case !isSentinel(err, application.ErrSkillNotFound):
				return nil, err
			}
			skills = append(skills, lv)
			players = append(players, p.PlayerID)
		}
		need := site.def.Research.MinStaff
		npcs := max(need-len(players), 0)
		wage := int64(len(players)+npcs) * (wagePer * site.wageBPS / 10_000)
		wantUp := map[string]int64{}
		for it, q := range site.def.Research.Upkeep {
			wantUp[it] = int64(q)
		}
		use, usable := h.realItems.resolveUse(snap, stock, wantUp, now)
		switch {
		case int64(npcs) > npcFree:
			// not enough people for the posts: it stands idle, nobody is paid today
			row.Idle = village.ResearchIdleNoScholars
		case treasury < wage:
			row.Idle = village.ResearchIdleNoWage
		case !usable:
			row.Idle = village.ResearchIdleNoUpkeep
		default:
			row.Staffed = true
			treasury -= wage
			npcFree -= int64(npcs)
			for it, q := range use {
				stock[it] -= q
				upkeep[it] += q
			}
			for i := 0; i < npcs; i++ {
				skills = append(skills, h.research.Rules.NPCScholarLevel)
			}
			one := wagePer * site.wageBPS / 10_000
			for _, pid := range players {
				payees = append(payees, payee{id: pid, wage: one})
				xp = append(xp, pid)
			}
			d.ScholarsPlayer += int64(len(players))
			d.ScholarsNPC += int64(npcs)
			d.WagePlayer += one * int64(len(players))
			d.WageNPC += one * int64(npcs)
			d.Staffed++
		}
		if row.Staffed {
			row.Skills = skills
		}
		d.Rows = append(d.Rows, row)
	}
	for _, q := range upkeep {
		d.UpkeepUnits += q
	}
	staffedDay := d.Staffed
	if d.WagePlayer > 0 {
		d.LedgerPlayerTx = h.ids.NewID()
	}
	if d.WageNPC > 0 {
		d.LedgerNPCTx = h.ids.NewID()
	}

	// The fence first: only the transaction that writes the day's row pays and draws.
	fresh, err := repo.RecordDay(ctx, d)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return repo.Day(ctx, s.CityID, today)
	}
	// a day scholars worked is practice in education
	if err := h.accrueDaily(ctx, tx, s.CityID, "education", "research", today, staffedDay, now); err != nil {
		return nil, err
	}
	if len(upkeep) > 0 {
		org := application.SettlementOrg(s.CityID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return nil, err
		}
		items := make([]string, 0, len(upkeep))
		for it := range upkeep {
			items = append(items, it)
		}
		sort.Strings(items)
		for _, it := range items {
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: it, Qty: upkeep[it],
				FromOrg: org, FromHolding: application.HoldWarehouse, Reason: application.ItemResearchUpkeep,
				ReferenceType: application.ResearchDayReference, ReferenceID: s.CityID, At: now}); err != nil {
				return nil, err
			}
		}
	}
	if d.WagePlayer > 0 || d.WageNPC > 0 {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
		if err != nil {
			return nil, err
		}
		if d.WagePlayer > 0 {
			entries := []application.LedgerEntry{{AccountID: acct.ID, Amount: money.FromMinor(-d.WagePlayer)}}
			for _, p := range payees {
				cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, p.id)
				if err != nil {
					return nil, err
				}
				entries = append(entries, application.LedgerEntry{AccountID: cash.ID, Amount: money.FromMinor(p.wage)})
			}
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: d.LedgerPlayerTx, Reason: application.ReasonScholarWage,
				CreatedAt: now, ReferenceType: application.ResearchDayReference, ReferenceID: s.CityID, Entries: entries}); err != nil {
				return nil, err
			}
		}
		if d.WageNPC > 0 {
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: d.LedgerNPCTx, Reason: application.ReasonScholarWageNPC,
				CreatedAt: now, ReferenceType: application.ResearchDayReference, ReferenceID: s.CityID, Entries: []application.LedgerEntry{
					{AccountID: acct.ID, Amount: money.FromMinor(-d.WageNPC)},
					{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(d.WageNPC)},
				}}); err != nil {
				return nil, err
			}
		}
	}
	// A scholar learns the trade by doing it: a day on duty trains scholarship.
	if h.research.ScholarXP > 0 {
		for _, pid := range xp {
			skills, err := tx.Skills().List(ctx, pid)
			if err != nil {
				return nil, err
			}
			if _, err := awardSkillXP(ctx, tx, snap, pid, skills, []skillAward{{Skill: "scholarship", XP: h.research.ScholarXP}}, now); err != nil {
				return nil, err
			}
		}
	}
	return &d, nil
}

// stockHas reports whether the stock holds every item of a basket.
func stockHas(stock map[string]int64, need map[string]int) bool {
	for it, q := range need {
		if stock[it] < int64(q) {
			return false
		}
	}
	return true
}

// researchContext is everything a quote needs besides the item: the day's slots, the world's frontier, how widely the
// world holds each item, the settlement's experience in each field and its literacy.
type researchContext struct {
	state       *researchState
	depths      map[string]int
	frontier    int
	shares      map[string]int64
	experience  map[string]int64
	literacyBPS int64
	rules       research.Rules
}

func (h *VillageHandler) researchContext(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance, literacyBPS int64,
) (*researchContext, error) {
	st, err := h.researchToday(ctx, tx, snap, s, buildings)
	if err != nil {
		return nil, err
	}
	rules := h.research.rules()
	tree := snap.SettlementKnowledgeTree()
	nodes := make([]research.Node, 0, len(tree))
	for _, t := range tree {
		nodes = append(nodes, research.Node{Code: t.Code, Requires: t.Requires, RequiresCapability: t.RequiresCapability, Provides: t.Provides})
	}
	depths, err := research.Depths(nodes)
	if err != nil {
		return nil, err
	}
	shares, err := tx.Research().HolderShares(ctx)
	if err != nil {
		return nil, err
	}
	exp, err := tx.Research().Experience(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	frontier := research.Frontier(depths, func(code string) int64 { return shares[code] }, rules)
	return &researchContext{state: st, depths: depths, frontier: frontier, shares: shares, experience: exp, literacyBPS: literacyBPS, rules: rules}, nil
}

// researchQuote is a project's price and pace in a slot, and the experience it would spend.
type researchQuote struct {
	research.Quote
	Slot  research.Slot
	Spent int64
}

// quote works out one item in one slot. partners is how many sharing partners hold the item.
func (rc *researchContext) quote(d content.SettlementKnowledgeDef, t settlementknowledge.Tech, slot research.Slot, partners int) researchQuote {
	depth := max(rc.depths[d.Code], 1)
	var exp int64
	if d.Field != "" {
		exp = rc.experience[d.Field]
	}
	q := research.Price(research.Input{
		BaseCost: d.Cost, BaseTime: t.Time, Depth: depth, Frontier: rc.frontier, Slot: slot, Running: rc.state.UsedBySlot[slot.Ref],
		LiteracyBPS: rc.literacyBPS, HoldersShareBPS: rc.shares[d.Code], SharePartners: partners, Experience: exp,
	}, rc.rules)
	var spent int64
	if q.DiscountBPS > 0 {
		spent = min(exp, int64(depth)*rc.rules.BreakthroughNeedPerDepth)
	}
	return researchQuote{Quote: q, Slot: slot, Spent: spent}
}

// best picks the free slot a project would finish soonest in (the earliest in the list on a tie); the zero value and
// false when no slot has room.
func (rc *researchContext) best(d content.SettlementKnowledgeDef, t settlementknowledge.Tech, partners int) (researchQuote, bool) {
	var best researchQuote
	found := false
	for _, s := range rc.state.Slots {
		if !rc.state.free(s) {
			continue
		}
		q := rc.quote(d, t, s, partners)
		if !found || q.Duration < best.Duration {
			best, found = q, true
		}
	}
	return best, found
}

// researchFinish is the time a quote's project finishes, in real time.
func (h *VillageHandler) researchFinish(now time.Time, q researchQuote) time.Time {
	return now.Add(h.scale.RealWait(q.Duration))
}

// accrueExperience adds a finished work shift to the experience of its field (learning by doing, ADR 0048 point 9).
func (h *VillageHandler) accrueExperience(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	buildings []application.SettlementBuildingInstance, sh *application.SettlementShift, now time.Time,
) error {
	if h.research.ExperiencePerShift <= 0 {
		return nil
	}
	for _, b := range buildings {
		if b.ID != sh.BuildingID {
			continue
		}
		d, ok := snap.SettlementBuildingDef(b.TypeCode)
		if !ok {
			return nil
		}
		field := content.FieldOfRole(d.Role)
		if field == "" {
			return nil
		}
		return tx.Research().AddExperience(ctx, sh.SettlementID, field, h.research.ExperiencePerShift, now)
	}
	return nil
}

// accrueDaily adds practice from a day source (a held watch or health day, a market day that sold, a staffed research
// day, a teaching day): once per settlement, field, source and local day, however often the day is settled.
func (h *VillageHandler) accrueDaily(ctx context.Context, tx application.Tx, settlementID, field, source string, day, mult int64, now time.Time) error {
	if h.research.ExperiencePerShift <= 0 || field == "" || mult <= 0 {
		return nil
	}
	_, err := tx.Research().AddDailyExperience(ctx, settlementID, field, source, day, h.research.ExperiencePerShift*mult, now)
	return err
}

// accrueSiteExperience adds a finished construction, repair or fit-out shift to the field of the building it worked on
// (the crew that lays a road learns infrastructure, the one that digs a canal learns water works).
func (h *VillageHandler) accrueSiteExperience(ctx context.Context, tx application.Tx, snap *content.Snapshot, sh *application.SettlementShift, now time.Time) error {
	if h.research.ExperiencePerShift <= 0 {
		return nil
	}
	b, err := tx.SettlementBuildings().Get(ctx, sh.BuildingID)
	if err != nil {
		if stderrors.Is(err, application.ErrBuildingNotFound) {
			return nil
		}
		return err
	}
	d, ok := snap.SettlementBuildingDef(b.TypeCode)
	if !ok {
		return nil
	}
	field := content.FieldOfRole(d.Role)
	if field == "" {
		return nil
	}
	return tx.Research().AddExperience(ctx, sh.SettlementID, field, h.research.ExperiencePerShift, now)
}

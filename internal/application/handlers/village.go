package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// VillageHandler serves K2 (docs/adr/0031-knowledge-and-village-
// progression.md, acquisition) and W5 (construction) for a settlement
// founded by a Telegram group (docs/adr/0028-world-and-settlements.md
// section 3): the village overview, its knowledge list, research and
// buying from Support, its build menu, placing and demolishing a building,
// and construction progress.
//
// GROUP-ONLY, LIKE FOUNDING ITSELF. Every command here answers where it was
// sent, and every one of them refuses outside the group that founded the
// settlement in question (village.VillageRefusal, VillageNoSettlement) —
// the same "a village's growth is the group's own decision" rule
// SettlementsHandler already follows for founding.
//
// OFFICE PERMISSION, DIRECTLY. Four actions change the settlement's own
// state — research, buy, build, demolish — and each is gated by
// application.Authorize(jurisdictionID, villageHeadOffice, playerID)
// called straight from the handler, not through the content-driven
// actions.yml/mayAct indirection military_common.go uses: that mechanism's
// own validator (internal/content, "military content") treats actions.yml
// as a closed, military-specific vocabulary (procure, sanction, treaty,
// war, a defence licence or a branch's command), and a village-level
// action does not belong in it. Authorize itself is the general-purpose
// primitive (used directly by appointments.go too); this is that same
// direct use, one more caller, no content change.
type VillageHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	worlds  *application.WorldCache
	cities  application.CityRepository
	scale   gametimeScale

	// villageGridLots is config.Settlement.VillageGridLots, the same value
	// PlaceFoundingKit was laid out on, copied in rather than imported so
	// this package stays free of internal/config like every other handler.
	villageGridLots int
	// concurrentBuildCap and gridLotsByTier are ADR 0028 section 4's own
	// table (village 1 build / 5x5, town 2/9x9, city 4/15x15). Only
	// "village" is reachable today — promotion is a later phase — but the
	// table costs nothing to carry in full.
	concurrentBuildCap map[string]int
	// homesPerCrew is settlement.build_homes_per_crew.
	homesPerCrew int64
	// charterLimits are the caps of rail R4 (settlement.charter_*).
	charterLimits charter.Limits
	// tzCooldown is settlement.timezone_cooldown.
	tzCooldown time.Duration
	gridLotsByTier     map[string]int

	// teachPeriod is how often the literacy diffusion tick runs, GAME
	// time (config education.teach_period or a village-specific default);
	// teachRateBPS/schoolCapacityFactorBPS feed
	// settlementknowledge.AdvanceLiteracy directly.
	teachPeriod           time.Duration
	teachRateBPS          int64
	baseSchoolCapacityBPS int64
	scarcityKBPS          int64
	scarcityFloorBPS      int64
	scarcityCapBPS        int64
	sellerBandBPS         int64
	demolitionSalvageBPS  int64
	// Roads (config.Settlement): the fee per automatic road lot.
	autoRoadCost       int64
	materialMarkupBPS  int64
	stockBaseCapacity  int64
	// storage is the stores' keepers and spoilage (village_storage.go).
	storage StorageRules
	materialBuyMax     int64
	materialBuyPresets []int64
	residenceCooldown  time.Duration
	homeCityCode       string
	donationMin        int64
	donationMax        int64
	donationPresets    []int64

	// citizen is the citizen loop's tuning (village_citizen.go).
	citizen CitizenRules

	// activity is where the Activities hub lists crime (WithActivities).
	activity ActivityRules

	// shop is the village shop's tuning (village_shop.go); zero has no shop.
	shop ShopRules

	// labor are the labour market's rules (ADR 0037); zero keeps the timer.
	labor     labor.Rules
	laborHire []int64
	laborWage []int64

	idempotencyTTL time.Duration
	now            func() time.Time
}

// gametimeScale is the one method this file needs of gametime.Scale,
// declared narrow so a test can hand in a trivial fake instead of a real
// game clock configuration.
type gametimeScale interface {
	RealWait(game time.Duration) time.Duration
}

// VillageRules bundles every content-independent tuning number K2/W5 needs
// that is not itself content (base_cost/k/floor/cap live in
// settlement_knowledge.yml per item; these are the curve's own shared
// knobs and the diffusion formula's own rates, ADR 0031 sections 4.4 and
// 10 point 3).
type VillageRules struct {
	VillageGridLots       int
	// HomesPerBuildCrew is settlement.build_homes_per_crew.
	HomesPerBuildCrew int64
	// CharterLimits are settlement.charter_* (zero: the defaults).
	CharterLimits charter.Limits
	// TimezoneCooldown is settlement.timezone_cooldown.
	TimezoneCooldown time.Duration
	TeachPeriod           time.Duration
	TeachRateBPS          int64
	BaseSchoolCapacityBPS int64
	ScarcityKBPS          int64
	ScarcityFloorBPS      int64
	ScarcityCapBPS        int64
	SellerBandBPS         int64
	DemolitionSalvageBPS  int64
	// AutoRoadCost is settlement.auto_road_cost.
	AutoRoadCost int64
	// MaterialMarkupBPS, StockBaseCapacity and MaterialBuyMax are
	// settlement.material_markup_bps, .stock_base_capacity and
	// .material_buy_max (village_economy.go).
	MaterialMarkupBPS int64
	StockBaseCapacity int64
	MaterialBuyMax    int64
	// MaterialBuyPresets are the quantities the buy buttons offer.
	MaterialBuyPresets []int64
	// ResidenceCooldown and HomeCityCode are settlement.residence_cooldown
	// and settlement.home_city_code (village_residence.go).
	ResidenceCooldown time.Duration
	HomeCityCode      string
}

// NewVillageHandler wires the handler.
func NewVillageHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	worlds *application.WorldCache, cities application.CityRepository, scale gametimeScale, rules VillageRules,
	idempotencyTTL time.Duration, now func() time.Time,
) *VillageHandler {
	if uow == nil || ids == nil || source == nil || worlds == nil || cities == nil || scale == nil {
		panic("handlers: NewVillageHandler requires a unit of work, ids, content, a world cache, cities and a game clock")
	}
	if rules.VillageGridLots < 2 || rules.TeachPeriod <= 0 || idempotencyTTL <= 0 {
		panic("handlers: NewVillageHandler requires a village grid, a teach period and an idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &VillageHandler{
		uow: uow, ids: ids, msgs: msgs, content: source, worlds: worlds, cities: cities, scale: scale,
		villageGridLots:       rules.VillageGridLots,
		homesPerCrew:          rules.HomesPerBuildCrew,
		charterLimits:         rules.CharterLimits,
		tzCooldown:            rules.TimezoneCooldown,
		concurrentBuildCap:    map[string]int{"village": settlementbuilding.ConcurrentCap("village"), "town": settlementbuilding.ConcurrentCap("town"), "city": settlementbuilding.ConcurrentCap("city")},
		gridLotsByTier:        map[string]int{"village": rules.VillageGridLots, "town": 9, "city": 15},
		teachPeriod:           rules.TeachPeriod,
		teachRateBPS:          rules.TeachRateBPS,
		baseSchoolCapacityBPS: rules.BaseSchoolCapacityBPS,
		scarcityKBPS:          rules.ScarcityKBPS,
		scarcityFloorBPS:      rules.ScarcityFloorBPS,
		scarcityCapBPS:        rules.ScarcityCapBPS,
		sellerBandBPS:         rules.SellerBandBPS,
		demolitionSalvageBPS:  rules.DemolitionSalvageBPS,
		autoRoadCost:          rules.AutoRoadCost,
		materialMarkupBPS:     rules.MaterialMarkupBPS,
		stockBaseCapacity:     rules.StockBaseCapacity,
		materialBuyMax:        rules.MaterialBuyMax,
		materialBuyPresets:    append([]int64(nil), rules.MaterialBuyPresets...),
		residenceCooldown:     rules.ResidenceCooldown,
		homeCityCode:          rules.HomeCityCode,
		idempotencyTTL:        idempotencyTTL,
		now:                   now,
	}
}

// villageHeadOffice is the office ADR 0028 section 4 names for a village's
// own top office; officeFor extends it to town/city for when promotion
// exists.
const villageHeadOffice = "village_head"

func officeFor(tier string) string { return wsettle.HeadOffice(tier) }

func (h *VillageHandler) screen(meta envelope.Metadata, lang string) presentation.Ctx {
	return presentation.Ctx{Lang: lang}
}

// viewer reads the player behind a command and the language to answer in,
// requiring the command to have been sent in a group (every village
// command is group-first, the same rule SettlementsHandler.Found follows).
func (h *VillageHandler) viewer(ctx context.Context, tx application.Tx, meta envelope.Metadata) (*application.Player, string, error) {
	if err := validPlayerRequest(meta); err != nil {
		return nil, meta.Language, err
	}
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, meta.Language, err
	}
	return p, RenderLanguage(meta, p), nil
}

// villageRefusal is a sentinel-carrying error a handler raises for a
// refusal village.VillageRefusal renders, mirroring productionRefusal's own
// shape (production.go) at a fraction of its size: K2/W5 needs no
// per-kind extra fields today.
type villageRefusal struct {
	kind, back string
	// remaining is how long a residence cool-down still runs.
	remaining time.Duration
	// min and max are a donation's bounds, for donate_range.
	min, max int64
	// lots are the lots a refused batch names.
	lots []village.BatchLotFailure
	// missing is the room a refused shift or purchase lacks, in units.
	missing int64
	// action, subject and needs are the attempt view of a refused build,
	// research or shift: exactly what is missing and where it comes from
	// (village_economy.go).
	action  string
	subject presentation.Named
	needs   []village.VillageNeed
	// access is the lot's road access and the ways to put it right, shown
	// instead of a bare refusal when a private building has no road.
	access *village.LotAccessView
}

func (e *villageRefusal) Error() string { return "handlers: village refusal: " + e.kind }

func refuseVillage(kind string, back ...string) *villageRefusal {
	r := &villageRefusal{kind: kind}
	if len(back) > 0 {
		r.back = back[0]
	}
	return r
}

// villageFinish turns a refusal (or any other error) into the response a
// player sees, exactly production.go's own finish pattern.
func (h *VillageHandler) villageFinish(meta envelope.Metadata, lang string, err error) (*presentation.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *villageRefusal
	if stderrors.As(err, &r) {
		if r.access != nil {
			return village.LotAccessScreen(c, *r.access), nil
		}
		return village.VillageRefusal(c, village.VillageRefusalView{Kind: r.kind, Back: presentation.RefOfAddress(r.back), Remaining: r.remaining, Min: r.min, Max: r.max, Lots: r.lots, Missing: r.missing,
			Action: r.action, Subject: r.subject, Needs: r.needs}), nil
	}
	if stderrors.Is(err, application.ErrCityNotFound) {
		return village.VillageRefusal(c, village.VillageRefusalView{Kind: village.VillageNoSettlement}), nil
	}
	if stderrors.Is(err, application.ErrNotOfficeHolder) {
		return village.VillageRefusal(c, village.VillageRefusalView{Kind: village.VillageNotOfficeHolder}), nil
	}
	if stderrors.Is(err, application.ErrInsufficientFunds) {
		return village.VillageRefusal(c, village.VillageRefusalView{Kind: village.VillageInsufficient}), nil
	}
	return nil, err
}

// settlementOf resolves the settlement this group's command is about, or
// application.ErrCityNotFound. It does not lock anything: reads use it
// straight, writes re-resolve under whatever lock they themselves need.
func (h *VillageHandler) settlementOf(ctx context.Context, tx application.Tx, meta envelope.Metadata) (application.FoundedSettlement, error) {
	if meta.FromClient() || !meta.InGroup() {
		// A game client (or a private chat) has no Telegram group to name its settlement by:
		// it is the player's own, the one they head or live in. Every
		// write still goes through authorizeVillage, so a resident who
		// is not the head is refused exactly as in the group.
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return application.FoundedSettlement{}, err
		}
		ps, err := tx.Settlements().ByPlayer(ctx, p.ID)
		if err != nil {
			return application.FoundedSettlement{}, err
		}
		return ps.FoundedSettlement, nil
	}
	return tx.Settlements().ByFoundingGroup(ctx, meta.TelegramChatID)
}

// authorize requires playerID to hold (or act as deputy for) the
// settlement's own top office.
func authorizeVillage(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string) error {
	_, err := application.Authorize(ctx, tx, s.JurisdictionID, officeFor(s.Tier), playerID)
	return err
}

// world returns the active planet, for terrain sampling. K2/W5 reads it
// the same way SettlementsHandler.Found does: there is only ever one live
// world (ADR 0028 section 2).
func (h *VillageHandler) world(ctx context.Context) (*worldgen.World, error) {
	_, w, err := h.worlds.Active(ctx)
	return w, err
}

// grid samples the settlement's own lot grid, terrain and occupancy both:
// SampleGrid's own terrain plus whatever settlement_buildings already
// stands (any status but demolished occupies its lot forever, ADR 0028
// section 6.2's permanence rule).
func (h *VillageHandler) grid(ctx context.Context, tx application.Tx, w *worldgen.World, s application.FoundedSettlement,
) (settlementbuilding.Grid, []application.SettlementBuildingInstance, error) {
	gridLots := h.gridSide(s)
	cell := w.Cells[s.WorldCellID]
	gridLat, gridLon := wsettle.GridCentreGrown(w, cell.Point.LatDeg, cell.Point.LonDeg, s.GridShiftX, s.GridShiftY, s.GridGrowth)
	sampled := wsettle.SampleGrid(w, gridLat, gridLon, gridLots, s.WorldCellID)

	existing, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return nil, nil, err
	}
	g := make(settlementbuilding.Grid, gridLots)
	for y := 0; y < gridLots; y++ {
		g[y] = make([]settlementbuilding.Lot, gridLots)
		for x := 0; x < gridLots; x++ {
			g[y][x] = settlementbuilding.Lot{Buildable: sampled[y][x].Buildable, TerrainTags: sampled[y][x].Tags}
		}
	}
	snap := h.content.Current()
	for _, b := range existing {
		if !b.Holds() {
			continue
		}
		// The whole footprint, turned as it was placed, is taken; a type
		// the content no longer declares still holds its own lot.
		fw, fh := 1, 1
		if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
			def := d.Def()
			if b.Rotated {
				def = def.Rotate()
			}
			fw, fh = def.FootprintW, def.FootprintH
		}
		for dy := 0; dy < fh; dy++ {
			for dx := 0; dx < fw; dx++ {
				x, y := b.LotX+dx, b.LotY+dy
				if y >= 0 && y < gridLots && x >= 0 && x < gridLots {
					g[y][x].Occupied = true
				}
			}
		}
	}
	// Right-of-way (docs/adr/0043): only a road may stand on a reserved lot.
	reserves, err := tx.Citizens().RoadReserves(ctx, s.CityID)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range reserves {
		if r.Y >= 0 && r.Y < gridLots && r.X >= 0 && r.X < gridLots {
			g[r.Y][r.X].Reserved = true
		}
	}
	return g, existing, nil
}

// gridSide is the side, in lots, of a settlement's grid now: the tier's base
// side plus the expansions it has bought.
func (h *VillageHandler) gridSide(s application.FoundedSettlement) int {
	base := h.gridLotsByTier[s.Tier]
	if base < 1 {
		base = h.villageGridLots
	}
	return base + s.GridGrowth
}

// knowledgeStanding builds the settlementknowledge.Standing a settlement
// brings to a research/purchase decision, and settlementbuilding.Standing
// the construction side needs — the two domain packages never share a
// type, so this reads settlement_knowledge_owned once and hands its shape
// to each.
func (h *VillageHandler) knowledgeStanding(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	s application.FoundedSettlement, terrainTags []string, running bool,
) (settlementknowledge.Standing, item.Set, error) {
	owned, err := tx.SettlementKnowledge().Owned(ctx, s.CityID)
	if err != nil {
		return settlementknowledge.Standing{}, nil, err
	}
	ownedSet := item.Set{}
	for _, o := range owned {
		ownedSet[o.Code] = struct{}{}
	}
	literacyBPS, _, err := tx.SettlementKnowledge().Literacy(ctx, s.CityID)
	if err != nil {
		return settlementknowledge.Standing{}, nil, err
	}
	tree := snap.SettlementKnowledgeTree()
	capabilities := item.Set{}
	for code := range ownedSet {
		t, ok := tree[code]
		if !ok {
			continue
		}
		provides := t.Provides
		if len(provides) == 0 {
			provides = []string{t.Code}
		}
		for _, c := range provides {
			capabilities[c] = struct{}{}
		}
	}
	st := settlementknowledge.Standing{Owned: ownedSet, Researching: running, TerrainTags: terrainTags, LiteracyShareBPS: literacyBPS}
	return st, capabilities, nil
}

// ---------------------------------------------------------------------
// Village overview
// ---------------------------------------------------------------------

// Overview handles settlement.overview.
func (h *VillageHandler) Overview(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.overview(ctx, meta, false)
}

func (h *VillageHandler) overview(ctx context.Context, meta envelope.Metadata, home bool) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.VillageOverviewView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		viewer, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, err := h.settlementOf(ctx, tx, meta)
		if err != nil {
			return err
		}
		residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
		if err != nil {
			return err
		}
		home, err := tx.Employment().ResidenceCityID(ctx, viewer.ID)
		if err != nil {
			return err
		}
		treasury, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return err
		}
		buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
		if err != nil {
			return err
		}
		literacyBPS, _, err := tx.SettlementKnowledge().Literacy(ctx, s.CityID)
		if err != nil {
			return err
		}

		var cap int64
		coverage := map[string]int64{}
		byRole := map[string]village.VillageRoleLine{}
		for _, b := range buildings {
			if !b.Complete() || b.Status == "demolished" {
				continue
			}
			def, ok := snap.SettlementBuildingDef(b.TypeCode)
			if !ok {
				continue
			}
			for _, e := range def.BuildingEffects() {
				if e.Target == "housing_capacity" {
					continue
				}
				coverage[e.Target] += e.Value
			}
			if def.Role != "" {
				if cur, ok := byRole[def.Role]; !ok || def.Tier > cur.Tier {
					byRole[def.Role] = village.VillageRoleLine{Role: def.Role, Building: presentation.Named{Code: def.Code, Name: def.Name}, Tier: def.Tier}
				}
			}
		}
		// The homes the village has: the households it starts with plus what its
		// standing buildings add, the same number the labour market reads.
		cap = h.labor.BaseHousing + housingOf(snap, buildings)
		var roleLines []village.VillageRoleLine
		for _, role := range []string{"security", "craft", "forestry", "extraction", "water_infra", "food", "housing", "health", "education", "market", "storage", "recreation"} {
			if l, ok := byRole[role]; ok {
				roleLines = append(roleLines, l)
			}
		}

		isHead, _ := h.holdsAnyOffice(ctx, tx, s, viewer.ID)
		view = village.VillageOverviewView{
			IsHead: isHead,
			Name:   s.Name, ZoneMinutes: int(s.Zone() / time.Minute), Tier: application.TierCity, Population: residents, PopulationCap: cap,
			Resident: home == s.CityID, SettlementID: s.CityID,
			Treasury:         treasury,
			FoodPercent:      int(coverage["food_coverage_bps"] / 100),
			JobPercent:       int(coverage["job_coverage_bps"] / 100),
			ServicePercent:   int(coverage["service_coverage_bps"] / 100),
			HappinessPercent: int(coverage["happiness_bps"] / 100),
			SecurityPercent:  int(coverage["local_security_bps"] / 100),
			LiteracyPercent:  literacyBPS / 100,
			Buildings:        roleLines,
		}
		view.Development = true // the readout is the way forward; the promotion ladder is retired
		if h.homeCityCode != "" {
			if support, err := h.cities.ByCode(ctx, h.homeCityCode); err == nil {
				view.Support = &village.VillageSupport{Code: support.Code, Name: support.Name}
				for _, s := range village.SupportServiceRoles {
					if _, has := byRole[s.Role]; s.Role == "" || !has {
						view.Support.Services = append(view.Support.Services, s.Service)
					}
				}
			}
		}
		return nil
	})
	if home && stderrors.Is(err, application.ErrCityNotFound) {
		// The village is the home: without one, home is the call to found it.
		if meta.InGroup() {
			return village.VillageHomeCall(h.screen(meta, lang)), nil
		}
		return village.VillageHomeNone(h.screen(meta, lang)), nil
	}
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.VillageOverview(h.screen(meta, lang), view), nil
}

// treasuryBalance reads a settlement's own city_treasury balance, opening
// the account on first use exactly Ledger.AccountFor already does for
// every other kind of owner.
func treasuryBalance(ctx context.Context, tx application.Tx, cityID string) (int64, error) {
	acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, cityID)
	if err != nil {
		return 0, err
	}
	bal, err := tx.Ledger().Balance(ctx, acct.ID)
	if err != nil {
		return 0, err
	}
	return bal.Minor(), nil
}

// spendVillage pays amount from a settlement's own treasury into
// system_sink under reason — the drain shape ADR 0031 sections 4.1/4.2 and
// ADR 0028 section 6.3 all describe (research, buying from Support, a
// building's construction cost), reused across all three rather than
// duplicated: only the reason differs.
func spendVillage(ctx context.Context, tx application.Tx, settlementID string, reason application.Reason, amount int64, at time.Time) (string, error) {
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, settlementID)
	if err != nil {
		return "", err
	}
	return tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason: reason, CreatedAt: at,
		Entries: []application.LedgerEntry{
			{AccountID: treasury.ID, Amount: money.FromMinor(-amount)},
			{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(amount)},
		},
	})
}

// scarcityPrice reads the periodically refreshed holder-count aggregate and
// applies the owner's own curve (settlementknowledge.ScarcityPrice, ADR
// 0031 section 10 point 3).
func (h *VillageHandler) scarcityPrice(ctx context.Context, tx application.Tx, baseCost int64, code string) (int64, error) {
	shareBPS, err := tx.SettlementKnowledge().HoldersShareBPS(ctx, code)
	if err != nil {
		return 0, err
	}
	return settlementknowledge.ScarcityPrice(baseCost, shareBPS, h.scarcityKBPS, h.scarcityFloorBPS, h.scarcityCapBPS), nil
}

// VillageActionPayload is the jsonb K2/W5's three game_actions kinds carry:
// the row the action is about (a research or a building id; empty for the
// settlement-wide teach tick) and the settlement itself.
type VillageActionPayload struct {
	ID           string `json:"id,omitempty"`
	SettlementID string `json:"settlement_id"`
}

// schedule puts one K2/W5 action on the game clock.
func (h *VillageHandler) schedule(ctx context.Context, tx application.Tx, actionType, refType, refID, settlementID string,
	now, finish time.Time,
) (string, error) {
	return scheduleVillageAction(ctx, tx, h.ids, actionType, refType, refID, settlementID, now, finish)
}

// scheduleVillageAction puts one K2/W5 action on the game clock. A
// standalone function, not only a VillageHandler method, because
// SettlementsHandler.Found also needs it once, to start a freshly founded
// settlement's own first literacy tick in the same transaction as founding
// itself (ADR 0031 section 4.4).
func scheduleVillageAction(ctx context.Context, tx application.Tx, ids IDGenerator, actionType, refType, refID, settlementID string,
	now, finish time.Time,
) (string, error) {
	payload, err := json.Marshal(VillageActionPayload{ID: refID, SettlementID: settlementID})
	if err != nil {
		return "", err
	}
	id := ids.NewID()
	return id, tx.GameActions().Schedule(ctx, application.GameAction{
		ID: id, ActionType: actionType, ActorType: "system", ReferenceType: refType, ReferenceID: refID,
		Payload: payload, StartedAt: now, FinishAt: finish,
	})
}

// villagePayload reads a scheduled K2/W5 action's payload, the same shape
// productionPayload already reads for company research.
func villagePayload(meta envelope.Metadata, req CrimeScheduledRequest) (VillageActionPayload, error) {
	if err := meta.Validate(); err != nil {
		return VillageActionPayload{}, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in VillageActionPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return in, errors.InvalidInput("village action payload is unreadable").WithCause(err)
		}
	}
	if in.ID == "" {
		in.ID = req.ReferenceID
	}
	if in.SettlementID == "" {
		in.SettlementID = req.ReferenceID
	}
	return in, nil
}

// reserve takes the idempotency key of a K2/W5 command that writes.
func (h *VillageHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// countdownTo (production.go) already gives "how long until at, never below
// a second" for a running research or order; K2/W5 reuses it verbatim.

// appendVillageEvent writes a K2/W5 event to the outbox.
func appendVillageEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("settlement."+name, "settlement", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("settlement", name), Metadata: meta, Payload: ev.Payload,
	})
}

// Home handles settlement.home: the village is the home. In a group with a
// founded village it is that village's overview (build, knowledge, residents,
// treasury and donation, and the services that are a journey away in
// Support); in a group without one, the call to found it; in a private chat,
// the village the player lives in.
func (h *VillageHandler) Home(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.overview(ctx, meta, true)
}

// HomeIfVillage is Home for a group that has a village, and reports false for
// a group that has none, so a caller with its own screen (the city hall) can
// let the village take the place of the city only where there is a village.
func (h *VillageHandler) HomeIfVillage(ctx context.Context, meta envelope.Metadata) (*presentation.Response, bool, error) {
	if !meta.InGroup() {
		return nil, false, nil
	}
	var found bool
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		_, err := tx.Settlements().ByFoundingGroup(ctx, meta.TelegramChatID)
		switch {
		case err == nil:
			found = true
		case stderrors.Is(err, application.ErrCityNotFound):
		default:
			return err
		}
		return nil
	})
	if err != nil || !found {
		return nil, false, err
	}
	resp, err := h.Home(ctx, meta)
	return resp, err == nil, err
}

// buildCap is how many buildings the settlement may raise at once: one crew for
// each build_homes_per_crew homes it has, at least what its old label gave.
func (h *VillageHandler) buildCap(snap *content.Snapshot, s application.FoundedSettlement, buildings []application.SettlementBuildingInstance) int {
	return settlementbuilding.CrewCap(s.Tier, housingOf(snap, buildings), h.homesPerCrew)
}

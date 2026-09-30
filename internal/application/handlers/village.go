package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/settlementknowledge"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
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
// settlement in question (screens.VillageRefusal, VillageNoSettlement) —
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
	residenceCooldown     time.Duration
	homeCityCode          string
	donationMin           int64
	donationMax           int64
	donationPresets       []int64

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
	TeachPeriod           time.Duration
	TeachRateBPS          int64
	BaseSchoolCapacityBPS int64
	ScarcityKBPS          int64
	ScarcityFloorBPS      int64
	ScarcityCapBPS        int64
	SellerBandBPS         int64
	DemolitionSalvageBPS  int64
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
		concurrentBuildCap:    map[string]int{"village": 1, "town": 2, "city": 4},
		gridLotsByTier:        map[string]int{"village": rules.VillageGridLots, "town": 9, "city": 15},
		teachPeriod:           rules.TeachPeriod,
		teachRateBPS:          rules.TeachRateBPS,
		baseSchoolCapacityBPS: rules.BaseSchoolCapacityBPS,
		scarcityKBPS:          rules.ScarcityKBPS,
		scarcityFloorBPS:      rules.ScarcityFloorBPS,
		scarcityCapBPS:        rules.ScarcityCapBPS,
		sellerBandBPS:         rules.SellerBandBPS,
		demolitionSalvageBPS:  rules.DemolitionSalvageBPS,
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

func (h *VillageHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
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
// refusal screens.VillageRefusal renders, mirroring productionRefusal's own
// shape (production.go) at a fraction of its size: K2/W5 needs no
// per-kind extra fields today.
type villageRefusal struct {
	kind, back string
	// remaining is how long a residence cool-down still runs.
	remaining time.Duration
	// min and max are a donation's bounds, for donate_range.
	min, max int64
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
func (h *VillageHandler) villageFinish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *villageRefusal
	if stderrors.As(err, &r) {
		return screens.VillageRefusal(c, screens.VillageRefusalView{Kind: r.kind, Back: r.back, Remaining: r.remaining, Min: r.min, Max: r.max}), nil
	}
	if stderrors.Is(err, application.ErrCityNotFound) {
		return screens.VillageRefusal(c, screens.VillageRefusalView{Kind: screens.VillageNoSettlement}), nil
	}
	if stderrors.Is(err, application.ErrNotOfficeHolder) {
		return screens.VillageRefusal(c, screens.VillageRefusalView{Kind: screens.VillageNotOfficeHolder}), nil
	}
	if stderrors.Is(err, application.ErrInsufficientFunds) {
		return screens.VillageRefusal(c, screens.VillageRefusalView{Kind: screens.VillageInsufficient}), nil
	}
	return nil, err
}

// settlementOf resolves the settlement this group's command is about, or
// application.ErrCityNotFound. It does not lock anything: reads use it
// straight, writes re-resolve under whatever lock they themselves need.
func (h *VillageHandler) settlementOf(ctx context.Context, tx application.Tx, meta envelope.Metadata) (application.FoundedSettlement, error) {
	if meta.FromClient() {
		// A game client has no Telegram group to name its settlement by:
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
	if !meta.InGroup() {
		return application.FoundedSettlement{}, refuseVillage(screens.VillageNoSettlement)
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
	gridLots := h.gridLotsByTier[s.Tier]
	if gridLots < 1 {
		gridLots = h.villageGridLots
	}
	cell := w.Cells[s.WorldCellID]
	gridLat, gridLon := wsettle.GridCentre(w, cell.Point.LatDeg, cell.Point.LonDeg, s.GridShiftX, s.GridShiftY)
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
	return g, existing, nil
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
func (h *VillageHandler) Overview(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view screens.VillageOverviewView
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
		byRole := map[string]screens.VillageRoleLine{}
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
					cap += e.Value
					continue
				}
				coverage[e.Target] += e.Value
			}
			if def.Role != "" {
				if cur, ok := byRole[def.Role]; !ok || def.Tier > cur.Tier {
					byRole[def.Role] = screens.VillageRoleLine{Role: def.Role, Building: screens.Named{Code: def.Code, Name: def.Name}, Tier: def.Tier}
				}
			}
		}
		var roleLines []screens.VillageRoleLine
		for _, role := range []string{"security", "craft", "extraction", "water_infra", "food", "health", "education", "market", "storage"} {
			if l, ok := byRole[role]; ok {
				roleLines = append(roleLines, l)
			}
		}

		view = screens.VillageOverviewView{
			Name: s.Name, Tier: s.Tier, Population: residents, PopulationCap: cap,
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
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.VillageOverview(h.screen(meta, lang), view), nil
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

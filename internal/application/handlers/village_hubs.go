package handlers

import (
	"context"
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// The Economy and Society hubs (docs/ui/web-structure.md sections 6 and 7).
// Like the Activities hub they list only what exists for the player where they
// stand: a tile of something the settlement does not have is not mentioned. What
// exists is the data of configs/content/availability.yml (the stage a thing
// starts at and the buildings it needs standing), the player's own things, and
// one tuned stage (settlement.property_hub_min_stage). They only read.

// hubSettlement is what the hubs judge a place by: its stage, whether it is the
// neutral city, and whether a required building stands.
type hubSettlement struct {
	stageRank int
	neutral   bool
	// content is true for a content city (the neutral one and the old
	// cities): it has no settlement buildings to judge by, every building its
	// tier would have is taken as standing.
	content bool
	stands  func(content.AvailabilityBuilding) bool
	// owned is the research the settlement holds; a tag that names knowledge
	// is not offered without it, whatever the stage says.
	owned map[string]bool
	// growth, cityID, caps and capsFound are the dual read of ADR 0044 phase
	// G1: nil growth (growth.capabilities off) leaves the tier the only answer.
	growth    *GrowthGate
	cityID    string
	caps      wsettle.Capabilities
	capsFound bool
}

// offered says whether the tagged thing exists in this settlement: the stage is
// reached (a city belongs to a country, so it has the national level around
// it), and every building it requires stands. A stage "support" is the neutral
// city only. A thing nothing tags exists everywhere; an undecided one nowhere.
func (s hubSettlement) offered(snap *content.Snapshot, kind, code string) bool {
	tag, ok := snap.AvailabilityTag(kind, code)
	if !ok {
		return true
	}
	byTier := s.offeredByTier(tag)
	if s.growth != nil && !s.content && s.capsFound {
		return s.growth.Decide("hubs", s.cityID, snap, s.caps, true, tag, byTier)
	}
	return byTier
}

// offeredByTier is offered by the stage and the standing buildings alone. A tag
// with no stage (ADR 0044 phase G0) has no stage to reach.
func (s hubSettlement) offeredByTier(tag content.AvailabilityDef) bool {
	if tag.Stage == content.StageSupport {
		return s.neutral
	}
	if tag.Stage != "" {
		need := content.StageRank(tag.Stage)
		if need == 0 {
			return false
		}
		have := s.stageRank
		if have >= content.StageRank(content.StageCity) {
			have = content.StageRank(content.StageCountry)
		}
		if have < need {
			return false
		}
	}
	if s.content || tag.Requires == nil {
		return true
	}
	for _, k := range tag.Requires.Knowledge {
		if !s.owned[k] {
			return false
		}
	}
	for _, b := range tag.Requires.Buildings {
		if !s.stands(b) {
			return false
		}
	}
	return true
}

// judgeSettlement reads where the player stands for the hubs. A player on the
// road has no settlement: a city with nothing standing.
func (h *VillageHandler) judgeSettlement(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City) (hubSettlement, error) {
	return judgeSettlementOf(ctx, tx, snap, city, h.homeCityCode)
}

// judgeSettlementOf is judgeSettlement for any handler: homeCityCode is the
// neutral city's code (settlement.home_city_code).
func judgeSettlementOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, city *application.City, homeCityCode string,
) (hubSettlement, error) {
	if city == nil {
		return hubSettlement{stageRank: content.StageRank(content.StageCity), content: true}, nil
	}
	s := hubSettlement{
		stageRank: content.StageRank(tierStage(city.Tier)),
		neutral:   homeCityCode != "" && city.Code == homeCityCode,
	}
	if _, err := tx.Settlements().ByID(ctx, city.ID); stderrors.Is(err, application.ErrCityNotFound) {
		s.content = true
		return s, nil
	} else if err != nil {
		return s, err
	}
	rows, err := tx.SettlementBuildings().List(ctx, city.ID)
	if err != nil {
		return s, err
	}
	owned, err := tx.SettlementKnowledge().Owned(ctx, city.ID)
	if err != nil {
		return s, err
	}
	s.owned = map[string]bool{}
	for _, o := range owned {
		s.owned[o.Code] = true
	}
	if gg := currentGrowth(); gg != nil {
		s.growth, s.cityID, s.capsFound = gg, city.ID, true
		s.caps = gg.FromRows(snap, application.SettlementStanding{Buildings: rows, Knowledge: owned})
	}
	s.stands = func(b content.AvailabilityBuilding) bool {
		for _, row := range rows {
			if row.Status != "complete" {
				continue
			}
			d, ok := snap.SettlementBuildingDef(row.TypeCode)
			if !ok {
				continue
			}
			if b.Code != "" && d.Code == b.Code {
				return true
			}
			if b.Role != "" && d.Def().Role == b.Role && d.Def().Tier >= b.Tier {
				return true
			}
		}
		return false
	}
	return s, nil
}

// EconomyHub handles economy.hub. The backpack, the market and the wallet are
// every player's. «شرکت‌های من» is listed once the player has a company or a
// kind of business the settlement can run stands here (a smallholding); «ملک»
// from the stage of settlement.property_hub_min_stage; «بورس» where an exchange
// exists (the stocks tag: a city with a bank, or the neutral city).
func (h *VillageHandler) EconomyHub(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	var view plife.HubView
	lang := meta.Language
	snap := h.content.Current()
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		city, err := h.whereIs(ctx, p)
		if err != nil {
			return err
		}
		here, err := h.judgeSettlement(ctx, tx, snap, city)
		if err != nil {
			return err
		}
		mine, err := tx.Companies().Of(ctx, p.ID)
		if err != nil {
			return err
		}
		view.Place = h.placeOf(city)
		// The market is listed only where one stands (audit F4, F6): a market
		// post or a hall in a settlement, or the neutral city's own bazaar.
		// It always opens the market, never the head's procurement screen;
		// the storehouse is its own entry.
		view.Entries = []plife.ActivityEntry{{Code: plife.EconomyInventory, Command: "inventory.show"}}
		if un, err := closedIn(ctx, tx, snap, city, h.homeCityCode, marketNeed); err != nil {
			return err
		} else if un == nil {
			view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.EconomyMarket, Command: "market.list"})
		}
		if !here.content && !here.neutral {
			view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.EconomyStorehouse, Command: "settlement.materials"})
		}
		view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.EconomyBank, Command: "bank.show"})
		if len(mine) > 0 || h.smallholdingHere(snap, here) {
			view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.EconomyCompanies, Command: "company.mine"})
		}
		if here.offersAny(snap, "property_type") {
			view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.EconomyProperty, Command: "property.mine"})
		}
		if here.offered(snap, "finance_service", "stocks") {
			view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.EconomyStocks, Command: "stock.list"})
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return plife.EconomyHub(h.screen(meta, lang), view), nil
}

// smallholdingHere says a kind of business exists in this settlement that
// belongs to its stage or below and whose buildings stand (a farm where a
// farm's food building stands, a grocery where a market does).
func (h *VillageHandler) smallholdingHere(snap *content.Snapshot, here hubSettlement) bool {
	for _, tag := range snap.AvailabilityTags("company_type") {
		if tag.Stage == content.StageCountry {
			continue // the defence sector is the state's, not a smallholding
		}
		if here.offered(snap, "company_type", tag.Code) {
			return true
		}
	}
	return false
}

// SocietyHub handles society.hub. Messages, friends and the government (the
// village head's «دهیاری» in a village) are every player's. «جناح» is listed
// where the faction tag is reached, «ارتش و جنگ» only with a state around the
// settlement. The elections tile is always listed: it says when none is open.
func (h *VillageHandler) SocietyHub(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	var view plife.HubView
	lang := meta.Language
	snap := h.content.Current()
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		city, err := h.whereIs(ctx, p)
		if err != nil {
			return err
		}
		here, err := h.judgeSettlement(ctx, tx, snap, city)
		if err != nil {
			return err
		}
		view.Place = h.placeOf(city)
		view.Entries = []plife.ActivityEntry{{Code: plife.SocietyInbox, Command: "inbox.show"}}
		if here.offered(snap, "faction", "faction") {
			view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.SocietyFaction, Command: "faction.mine"})
		}
		view.Entries = append(view.Entries,
			plife.ActivityEntry{Code: plife.SocietyFriends, Command: "social.friend.list"},
			plife.ActivityEntry{Code: plife.SocietyElections, Command: "election.list"},
			plife.ActivityEntry{Code: plife.SocietyGovernment, Command: "gov.city"})
		// The army and the war board exist only where a barracks stands, and
		// are not mentioned at all otherwise (not even as a locked tile).
		if here.hasBarracks() && here.offered(snap, "government_action", "country.war") {
			view.Entries = append(view.Entries, plife.ActivityEntry{Code: plife.SocietyWar, Command: "military.ministry"})
		}
		return nil
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return plife.SocietyHub(h.screen(meta, lang), view), nil
}

// offersAny reports whether the settlement offers at least one entry of a kind
// (by what it has, through the same gate every entry passes).
func (s hubSettlement) offersAny(snap *content.Snapshot, kind string) bool {
	for _, t := range snap.AvailabilityTags(kind) {
		if s.offered(snap, kind, t.Code) {
			return true
		}
	}
	return false
}

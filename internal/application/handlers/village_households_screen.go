package handlers

import (
	"context"
	"sort"
	"strings"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/household"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// VillageHouseholdsRequest is settlement.households: an act and its arguments. No act shows the screen.
type VillageHouseholdsRequest struct {
	Action string `json:"action,omitempty"`
	Code   string `json:"code,omitempty"`
	N      string `json:"n,omitempty"`
}

// householdListMax is how many households the screen lists.
const householdListMax = 40

// Households handles settlement.households: the households of the settlement (reading it settles today's day), and the levers of
// the head (admission, the tax on a wage) and of a dwelling's owner (opening it to tenants).
func (h *VillageHandler) Households(ctx context.Context, meta envelope.Metadata, req VillageHouseholdsRequest) (*presentation.Response, error) {
	snap := h.content.Current()
	lang := meta.Language
	var view village.HouseholdsView
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
		if act := strings.TrimSpace(req.Action); act != "" {
			if fresh, err := h.reserve(ctx, tx, p.ID, meta); err != nil {
				return err
			} else if fresh {
				if err := h.householdsAct(ctx, tx, meta, snap, s, p, act, strings.TrimSpace(req.Code), strings.TrimSpace(req.N)); err != nil {
					return err
				}
			}
		}
		view, err = h.householdsView(ctx, tx, snap, s, p)
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Households(h.screen(meta, lang), view), nil
}

func (h *VillageHandler) householdsAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player, act, code, n string,
) error {
	repo := tx.Household()
	now := h.now()
	flag, err := h.householdFlag(ctx, tx, s.CityID)
	if err != nil {
		return err
	}
	if flag == nil {
		return refuseVillage(village.VillageNotFound, village.AddrHouseholds)
	}
	audit := func(field string, value any) error {
		return appendVillageEvent(ctx, tx, meta, "households_set", s.CityID, map[string]any{
			"settlement_id": s.CityID, "field": field, "value": value, "set_by": p.ID, "content_version": snap.Version(),
		})
	}
	switch act {
	case village.HouseholdActionAdmit, village.HouseholdActionClose:
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.CitizenAdmit); err != nil {
			return err
		}
		closed := act == village.HouseholdActionClose
		if err := repo.SetAdmission(ctx, s.CityID, closed, p.ID, now); err != nil {
			return err
		}
		return audit("admission_closed", closed)
	case village.HouseholdActionTax:
		if _, err := h.requireVillage(ctx, tx, s, p.ID, charter.FiscalSalesTax); err != nil {
			return err
		}
		if n == "" {
			n = code // the tax is the first argument after the act: settlement.households tax 500
		}
		bps, ok := parseNonNegative(n)
		if !ok || bps > 5_000 {
			return refuseVillage(village.VillageNotFound, village.AddrHouseholds)
		}
		if err := repo.SetIncomeTax(ctx, s.CityID, bps, p.ID, now); err != nil {
			return err
		}
		return audit("income_tax_bps", bps)
	case village.HouseholdActionRentOpen, village.HouseholdActionRentClose:
		b, ok, err := h.ownDwelling(ctx, tx, snap, s, p, code)
		if err != nil {
			return err
		}
		if !ok {
			return refuseVillage(village.VillageNotFound, village.AddrHouseholds)
		}
		if act == village.HouseholdActionRentClose {
			if err := repo.ClearRental(ctx, b.ID); err != nil {
				return err
			}
			return audit("rent_close", b.ID)
		}
		rent, ok := parseNonNegative(n)
		if !ok || rent < 1 || rent > h.house.MaxRent {
			return refuseVillage(village.VillageNotFound, village.AddrHouseholds)
		}
		_, slots := housingOfBuilding(snap, b)
		if err := repo.SetRental(ctx, application.HouseholdRental{BuildingID: b.ID, SettlementID: s.CityID, OwnerID: p.ID, Rent: rent, Slots: slots, ListedAt: now}); err != nil {
			return err
		}
		return audit("rent_open", map[string]any{"building": b.ID, "rent": rent})
	}
	return refuseVillage(village.VillageNotFound, village.AddrHouseholds)
}

// housingOfBuilding is the homes one finished building gives.
func housingOfBuilding(snap *content.Snapshot, b application.SettlementBuildingInstance) (bool, int64) {
	d, ok := snap.SettlementBuildingDef(b.TypeCode)
	if !ok {
		return false, 0
	}
	var n int64
	for _, e := range d.BuildingEffects() {
		if e.Target == "housing_capacity" && e.Op == item.EffectAdd {
			n += e.Value
		}
	}
	return n > 0, n
}

// ownDwelling finds a finished home of the player's own in the settlement: the citizen who built it and employs for it.
func (h *VillageHandler) ownDwelling(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, p *application.Player,
	id string,
) (application.SettlementBuildingInstance, bool, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return application.SettlementBuildingInstance{}, false, err
	}
	for _, b := range buildings {
		if b.ID != id || b.Status != "complete" || b.EmployerPlayerID != p.ID {
			continue
		}
		if ok, _ := housingOfBuilding(snap, b); ok {
			return b, true, nil
		}
	}
	return application.SettlementBuildingInstance{}, false, nil
}

func (h *VillageHandler) householdsView(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	p *application.Player,
) (village.HouseholdsView, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return village.HouseholdsView{}, err
	}
	now := h.now()
	if err := h.SettleHouseholdDay(ctx, tx, snap, s, buildings, now); err != nil {
		return village.HouseholdsView{}, err
	}
	repo := tx.Household()
	v := village.HouseholdsView{Name: s.Name, Phase: village.HouseholdPhaseNone, Open: true, MaxRent: h.house.MaxRent,
		TaxPresets: []int64{0, 500, 1000, 2000}, RentPresets: []int64{20, 50, 100}}
	flag, err := h.householdFlag(ctx, tx, s.CityID)
	if err != nil {
		return v, err
	}
	v.MayAdmit = hasPermission(ctx, tx, s, p.ID, charter.CitizenAdmit)
	v.MayTax = hasPermission(ctx, tx, s, p.ID, charter.FiscalSalesTax)
	if flag == nil {
		return v, nil
	}
	v.Phase, v.Open, v.IncomeTaxBPS, v.TargetPool = flag.State, !flag.Closed, flag.IncomeTaxBPS, flag.TargetPool
	residents, err := tx.Settlements().ResidentCount(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	housing, err := h.housingNow(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return v, err
	}
	if v.Pool, err = h.poolSize(ctx, tx, s.CityID, housing, residents); err != nil {
		return v, err
	}
	v.Homes = max(h.labor.BaseHousing+housing-residents, 0)
	hs, err := repo.Living(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	worst := map[string]int{}
	var sats []int64
	tenants := map[string]int64{}
	for i, x := range hs {
		if x.State == application.HouseholdLeaving {
			v.Leaving++
		} else {
			v.Living++
			v.Earners += x.Earners
			sats = append(sats, x.SatisfactionBPS)
		}
		v.Persons += x.Adults + x.Dependants
		if x.WorstNeed != "" {
			worst[x.WorstNeed]++
		}
		if b := buildingOfRef(x.HomeRef); b != "" {
			tenants[b]++
		}
		if i >= householdListMax {
			continue
		}
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountHouseholdWallet, x.ID)
		if err != nil {
			return v, err
		}
		bal, err := tx.Ledger().Balance(ctx, acct.ID)
		if err != nil {
			return v, err
		}
		v.Wallets += bal.Minor()
		v.List = append(v.List, village.HouseholdLine{Index: i + 1, Adults: x.Adults, Dependants: x.Dependants, Earners: x.Earners,
			SatisfactionBPS: x.SatisfactionBPS, State: x.State, Worst: x.WorstNeed, Wallet: bal.Minor(), ArrivedAt: x.ArrivedAt})
	}
	v.MoodBPS = household.Mood(sats)
	var top string
	var topN int
	needs := make([]string, 0, len(worst))
	for k := range worst {
		needs = append(needs, k)
	}
	sort.Strings(needs)
	for _, k := range needs {
		if worst[k] > topN {
			top, topN = k, worst[k]
		}
	}
	v.Worst = top
	if last, err := repo.LastDone(ctx, s.CityID); err != nil {
		return v, err
	} else if last != nil && last.DoneAt != nil {
		v.LastDay = &village.HouseholdDayLine{Households: last.Households, Arrivals: last.Arrivals, Departures: last.Departures, At: *last.DoneAt}
	}
	events, err := repo.Events(ctx, s.CityID, 12)
	if err != nil {
		return v, err
	}
	for _, e := range events {
		v.News = append(v.News, village.HouseholdNewsLine{Kind: e.Kind, Detail: e.Detail, At: e.At})
	}
	rentals, err := repo.Rentals(ctx, s.CityID)
	if err != nil {
		return v, err
	}
	rented := map[string]application.HouseholdRental{}
	for _, r := range rentals {
		rented[r.BuildingID] = r
	}
	for _, b := range buildings {
		if b.Status != "complete" || b.EmployerPlayerID != p.ID {
			continue
		}
		ok, slots := housingOfBuilding(snap, b)
		if !ok {
			continue
		}
		d, _ := snap.SettlementBuildingDef(b.TypeCode)
		r, open := rented[b.ID]
		v.Dwellings = append(v.Dwellings, village.HouseholdDwellingLine{Building: presentation.Named{Code: b.ID, Name: d.Name}, Slots: slots, Open: open, Rent: r.Rent, Tenants: tenants[b.ID]})
	}
	return v, nil
}

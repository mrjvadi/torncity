package handlers

import (
	"context"
	stderrors "errors"

	"github.com/mrjvadi/torncity/internal/application"
)

// Care in a founded settlement (docs/adr/0069, plan B5): the health house and the clinic are working mechanics, not a coverage
// number.
//
//   - WHO WORKS THERE. A health worker keeps the house, a nurse and a health worker the clinic: seats of the labour pool, paid a
//     day's wage from the treasury by the daily service (village_service.go).
//   - WHAT IT CONSUMES. Cloth and water every open day (the service day), and a medicine from the settlement stock for each
//     treatment: a bandage at the house, a first aid kit or painkillers at the clinic (item reason medicine_used).
//   - WHAT IT PROVIDES. On a day the post was open a patient of the settlement is treated: the house takes a share off the stay
//     for nothing, the clinic a larger one for a fee into the treasury (hospital_fee).
//   - WITHOUT. A closed post, or an empty shelf, treats nobody and says what is missing (the hospital view).
//
// The health handler asks this reader through application.VillageCarer; it prices and records the treatment.

// CareHere reads the care a founded settlement offers today. The settlement's local day is judged first if nobody has yet
// (SettleServiceDay), so the answer is the posts' real state. It returns nil for a city that is not a founded settlement.
func (h *VillageHandler) CareHere(ctx context.Context, tx application.Tx, cityID string) (*application.VillageCare, error) {
	s, err := tx.Settlements().ByID(ctx, cityID)
	switch {
	case stderrors.Is(err, application.ErrCityNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}
	snap := h.content.Current()
	buildings, err := tx.SettlementBuildings().List(ctx, cityID)
	if err != nil {
		return nil, err
	}
	out := &application.VillageCare{SettlementID: cityID, Stock: map[string]int64{}, Refer: h.homeCityCode}
	day, err := h.SettleServiceDay(ctx, tx, snap, s, buildings)
	if err != nil {
		return nil, err
	}
	for _, p := range servicePosts(snap, buildings) {
		var site *application.CareSite
		switch p.service() {
		case application.ServicePrimaryCare:
			site = &out.House
		case application.ServiceClinicalCare:
			site = &out.Clinic
		default:
			continue
		}
		if !site.Present {
			site.Present, site.BuildingID = true, p.b.ID
		}
		if site.Open {
			continue
		}
		if day == nil {
			continue
		}
		for _, row := range day.Posts {
			if row.BuildingID != p.b.ID {
				continue
			}
			if row.Held {
				site.Open, site.Idle, site.BuildingID = true, "", p.b.ID
			} else if site.Idle == "" {
				site.Idle = row.Idle
			}
		}
	}
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.SettlementOrg(cityID), application.HoldWarehouse)
	if err != nil {
		return nil, err
	}
	for _, st := range stacks {
		out.Stock[st.Item] += st.Qty
	}
	return out, nil
}

package handlers

import (
	"context"
	"math"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/life"
)

// Workers eat (roadmap 2.2 phase 3, ADR 0041 6.5, rule 1c). A production shift draws the food
// points of its hours from the village kitchen: the pot of points opened from the stock, best
// food first. The village's food leaves the stock only when the pot runs short, one unit at a
// time, so a unit's leftover points feed the next shift. Farm, pasture and fishing shifts
// (every output is food) are exempt: their worker eats from the produce.
//
// No food: an NPC does not start (the crew pauses with no_food); a player may start hungry,
// at a share of the output (config labor.hungry_output_bps), and their own hunger rises.

// mealPointsOf is the food points one shift of d eats: its hours, at least one; none when
// the building makes only food.
func mealPointsOf(snap *content.Snapshot, d content.SettlementBuildingDef) int64 {
	if len(d.Produces) > 0 {
		allFood := true
		for item := range d.Produces {
			if st, ok := snap.ItemStorage(item); !ok || st.Class != "food" {
				allFood = false
				break
			}
		}
		if allFood {
			return 0
		}
	}
	h := math.Ceil(d.Def().Work.Shift.Hours())
	return max(int64(h), 1)
}

// mealPlan says how a shift of `points` is fed: from the pot, opening units of the stock
// (less what the shift's own inputs take) when the pot is short. ok is false when the village
// cannot feed it; nothing is opened then.
func mealPlan(foods []content.MealFood, stockUnits, inputs map[string]int64, pot, points int64) (openings []application.MealOpening, ok bool) {
	need := points - pot
	if need <= 0 {
		return nil, true
	}
	for _, f := range foods {
		avail := stockUnits[f.Item] - inputs[f.Item]
		if avail <= 0 {
			continue
		}
		units := min(avail, (need+f.Points-1)/f.Points)
		openings = append(openings, application.MealOpening{Item: f.Item, Units: units, PointsEach: f.Points})
		need -= units * f.Points
		if need <= 0 {
			return openings, true
		}
	}
	return nil, false
}

// eatMeal opens the planned units and takes the shift's meal out of the pot, in the shift's
// transaction (the stock lock is held by the caller). Item journal: meal_eaten, one movement
// per opening, referencing its settlement_meals row.
func (h *VillageHandler) eatMeal(ctx context.Context, tx application.Tx, settlementID, shiftID string, points int64,
	openings []application.MealOpening, at time.Time,
) error {
	for i := range openings {
		openings[i].ID = h.ids.NewID()
		if err := tx.Items().Move(ctx, application.ItemMove{
			Item: openings[i].Item, Qty: openings[i].Units, FromOrg: application.SettlementOrg(settlementID), FromHolding: application.HoldWarehouse,
			Reason: application.ItemMealEaten, ReferenceType: application.MealReference, ReferenceID: openings[i].ID, At: at,
		}); err != nil {
			return err
		}
	}
	return tx.SettlementTreasury().Eat(ctx, settlementID, shiftID, points, openings, at)
}

// hungerOfWork raises a hungry player's own hunger need for the shift they worked fasting.
func (h *VillageHandler) hungerOfWork(ctx context.Context, tx application.Tx, playerID string) error {
	l, err := tx.Life().Get(ctx, playerID)
	if err != nil || l == nil {
		return err
	}
	l.Hunger = min(l.Hunger+h.labor.HungryShiftHunger*life.Milli, life.MaxPoints*life.Milli)
	return tx.Life().Save(ctx, *l)
}

package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
)

var testFoods = []content.MealFood{{Item: "bread", Points: 4}, {Item: "wheat", Points: 2}}

func TestMealPlanOpensTheBestFoodAndKeepsTheLeftoverInThePot(t *testing.T) {
	// the pot covers a small shift: nothing is opened
	if op, ok := mealPlan(testFoods, map[string]int64{"bread": 3}, nil, 5, 4); !ok || len(op) != 0 {
		t.Errorf("a pot of 5 feeds 4 points: %v %v", op, ok)
	}
	// short pot: one bread opens (4 points), the rest of it feeds the next shift
	op, ok := mealPlan(testFoods, map[string]int64{"bread": 3, "wheat": 9}, nil, 1, 3)
	if !ok || len(op) != 1 || op[0].Item != "bread" || op[0].Units != 1 {
		t.Errorf("one bread feeds the 2 missing points: %v %v", op, ok)
	}
	// bread is not enough: the wheat follows, the shift's own inputs are not eaten
	op, ok = mealPlan(testFoods, map[string]int64{"bread": 1, "wheat": 4}, map[string]int64{"wheat": 3}, 0, 8)
	if ok {
		t.Errorf("1 bread + 1 free wheat is 6 points, not 8: %v", op)
	}
	op, ok = mealPlan(testFoods, map[string]int64{"bread": 1, "wheat": 6}, map[string]int64{"wheat": 3}, 0, 8)
	if !ok || len(op) != 2 || op[0].Item != "bread" || op[1].Item != "wheat" || op[1].Units != 2 {
		t.Errorf("bread then two wheat: %v %v", op, ok)
	}
	// no food at all
	if _, ok := mealPlan(testFoods, map[string]int64{}, nil, 0, 1); ok {
		t.Error("an empty store feeds nobody")
	}
}

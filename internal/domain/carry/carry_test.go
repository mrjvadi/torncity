package carry

import (
	"errors"
	"testing"
)

func rules() Rules {
	return Rules{Base: 8, BaseComfortG: 7_000, BaseHardG: 20_000, FullShareBPS: 5_000, TornSpaceBPS: 5_000, RepairShareBPS: 2_500, WearPerDay: 1}
}

func sack(wear int) Bag {
	return Bag{Slot: SlotBack, Space: 12, ComfortG: 10_000, HardG: 30_000, Wear: wear, WearMax: 40}
}

func pouch(wear int) Bag {
	return Bag{Slot: SlotBelt, Space: 4, ComfortG: 10_000, HardG: 25_000, Wear: wear, WearMax: 60}
}

func TestCapacityIsHandsPlusBags(t *testing.T) {
	r := rules()
	if got := r.Capacity(nil); got != 8 {
		t.Fatalf("no bag: %d", got)
	}
	// The cheapest sack restores exactly the 20 of ADR 0040 section 6.
	if got := r.Capacity([]Bag{sack(40)}); got != 20 {
		t.Fatalf("sack: %d", got)
	}
	if got := r.Capacity([]Bag{pouch(60), sack(40)}); got != 24 {
		t.Fatalf("pouch and sack: %d", got)
	}
}

func TestATornBagGivesHalfItsSpaceAndNoComfort(t *testing.T) {
	r := rules()
	if !sack(0).Torn() || sack(1).Torn() {
		t.Fatal("torn is wear at zero")
	}
	if got := r.Capacity([]Bag{sack(0)}); got != 8+6 {
		t.Fatalf("torn sack: %d", got)
	}
	comfort, hard := r.Load([]Bag{sack(0)})
	if comfort != 7_000 || hard != 20_000 {
		t.Fatalf("torn load %d/%d", comfort, hard)
	}
	comfort, hard = r.Load([]Bag{sack(40)})
	if comfort != 17_000 || hard != 50_000 {
		t.Fatalf("whole load %d/%d", comfort, hard)
	}
	// A bag that never wears is never torn.
	if (Bag{Space: 5}).Torn() {
		t.Fatal("a bag with no wear points tore")
	}
}

func TestNoRoomNoBuy(t *testing.T) {
	r := rules()
	bags := []Bag{sack(40)}
	room := r.RoomLeft(bags, 18, 1_000)
	if err := room.Fits(2, 500); err != nil {
		t.Fatalf("2 units fit in 2 free: %v", err)
	}
	if err := room.Fits(3, 500); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("3 units in 2 free: %v", err)
	}
	if err := r.RoomLeft(bags, 5, 49_900).Fits(1, 500); !errors.Is(err, ErrTooHeavy) {
		t.Fatalf("the hard load was passed: %v", err)
	}
	// Over capacity already (a player who held more before bags existed) has no room, never negative room.
	if rm := r.RoomLeft(nil, 40, 0); rm.FreeSpace != 0 {
		t.Fatalf("free space %d", rm.FreeSpace)
	}
}

func TestWearOnlyWhenHalfFull(t *testing.T) {
	r := rules()
	// 20 space, half is 10.
	if p, through := r.WearDue(5, 8, 10, 20); p != 3 || through != 8 {
		t.Fatalf("3 days half full: %d through %d", p, through)
	}
	if p, through := r.WearDue(5, 8, 9, 20); p != 0 || through != 8 {
		t.Fatalf("under half full wears nothing but the day is settled: %d through %d", p, through)
	}
	if p, through := r.WearDue(8, 8, 20, 20); p != 0 || through != 8 {
		t.Fatalf("same day: %d through %d", p, through)
	}
	if p, through := r.WearDue(9, 8, 20, 20); p != 0 || through != 9 {
		t.Fatalf("a clock that went back never un-settles a day: %d through %d", p, through)
	}
}

func TestRepairCostIsAQuarterOfThePriceForAFullRepair(t *testing.T) {
	r := rules()
	if got := r.RepairCost(80, 0, 40); got != 20 {
		t.Fatalf("full repair of an 80 sack: %d", got)
	}
	if got := r.RepairCost(80, 20, 40); got != 10 {
		t.Fatalf("half repair: %d", got)
	}
	if got := r.RepairCost(80, 39, 40); got != 1 {
		t.Fatalf("a scratch still costs 1: %d", got)
	}
	if got := r.RepairCost(80, 40, 40); got != 0 {
		t.Fatalf("a whole bag costs nothing: %d", got)
	}
	if got := r.RepairCost(80, 0, 0); got != 0 {
		t.Fatalf("a bag that never wears costs nothing: %d", got)
	}
}

func TestRulesValidate(t *testing.T) {
	if err := rules().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := rules()
	bad.Base = 0
	bad.TornSpaceBPS = 20_000
	if err := bad.Validate(); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("bad rules accepted: %v", err)
	}
}

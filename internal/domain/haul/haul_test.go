package haul

import (
	"testing"
	"time"
)

func TestSplitAddsUpAndFitsTheCarrier(t *testing.T) {
	for _, c := range []struct{ qty, unitG, capKg int64 }{{10, 25_000, 100}, {7, 9_000, 20}, {1, 1000, 1}, {1000, 2000, 150}} {
		trips, err := Split(c.qty, c.unitG, c.capKg)
		if err != nil {
			t.Fatal(err)
		}
		var sum int64
		for _, n := range trips {
			sum += n
			if n <= 0 || n*c.unitG > c.capKg*1000 {
				t.Fatalf("trip of %d units of %d g overloads a %d kg carrier", n, c.unitG, c.capKg)
			}
		}
		if sum != c.qty {
			t.Fatalf("trips add up to %d, want %d", sum, c.qty)
		}
	}
	if _, err := Split(1, 50_000, 10); err == nil {
		t.Fatal("a unit heavier than the carrier must be refused")
	}
	if _, err := Split(0, 1000, 10); err == nil {
		t.Fatal("an empty consignment must be refused")
	}
}

func TestDurationHalvesOnABrokenWay(t *testing.T) {
	if got := Duration(8000, 8, 9000, 3000); got != time.Hour {
		t.Fatalf("8 km at 8 km/h = %s", got)
	}
	if got := Duration(8000, 8, 2000, 3000); got != 2*time.Hour {
		t.Fatalf("a broken way must halve the pace, got %s", got)
	}
}

func TestFreightFeeByTonneKilometre(t *testing.T) {
	// 10 t over 20 km at 50 a tonne-km
	if got := FreightFee(50, 10_000, 20_000); got != 10_000 {
		t.Fatalf("fee = %d, want 10000", got)
	}
	if FreightFee(50, 1, 1) != 1 {
		t.Fatal("a short trip is never free")
	}
	if FreightFee(0, 1000, 1000) != 0 {
		t.Fatal("no rate, no fee")
	}
}

// A consignment conserves goods in transit: at every step stores plus road hold exactly what was there.
func TestBookConservesGoodsInTransit(t *testing.T) {
	b := NewBook()
	b.Put("mine", "iron_ore", 40)
	b.Put("smelter", "iron_ore", 5)
	want := b.Total("iron_ore")
	trips, _ := Split(40, 25_000, 250)
	for _, n := range trips {
		if err := b.Depart("mine", "iron_ore", n); err != nil {
			t.Fatal(err)
		}
		if b.Total("iron_ore") != want {
			t.Fatalf("goods vanished at departure: %d, want %d", b.Total("iron_ore"), want)
		}
	}
	if b.Road["iron_ore"] != 40 || b.Stores["iron_ore"]["mine"] != 0 {
		t.Fatalf("on the road %d, at the mine %d", b.Road["iron_ore"], b.Stores["iron_ore"]["mine"])
	}
	for _, n := range trips {
		if err := b.Arrive("smelter", "iron_ore", n); err != nil {
			t.Fatal(err)
		}
		if b.Total("iron_ore") != want {
			t.Fatalf("goods appeared at arrival: %d, want %d", b.Total("iron_ore"), want)
		}
	}
	if b.Stores["iron_ore"]["smelter"] != 45 || b.Road["iron_ore"] != 0 {
		t.Fatalf("smelter holds %d, road %d", b.Stores["iron_ore"]["smelter"], b.Road["iron_ore"])
	}
	if err := b.Depart("mine", "iron_ore", 1); err == nil {
		t.Fatal("a store cannot send what it does not hold")
	}
	if err := b.Arrive("smelter", "iron_ore", 1); err == nil {
		t.Fatal("nothing is on the road, nothing may arrive")
	}
}

func TestAllocateFillsCarriersInOrderAndAddsUp(t *testing.T) {
	// 30 sacks of 25 kg on carts of 600 kg (24 sacks) and 1000 kg (40 sacks)
	got, ok := Allocate(30, 25_000, []int64{600, 1000})
	if !ok || got[0] != 24 || got[1] != 6 {
		t.Fatalf("allocation %v %v", got, ok)
	}
	if _, ok := Allocate(100, 25_000, []int64{600, 1000}); ok {
		t.Fatal("the carriers cannot take 100 sacks")
	}
	if got, ok := Allocate(10, 25_000, []int64{600, 1000}); !ok || got[0] != 10 || got[1] != 0 {
		t.Fatalf("one carrier is enough: %v %v", got, ok)
	}
	if _, ok := Allocate(0, 25_000, []int64{600}); ok {
		t.Fatal("nothing to carry is not a consignment")
	}
	if _, ok := Allocate(1, 50_000, []int64{10}); ok {
		t.Fatal("a unit heavier than the carrier cannot be carried")
	}
}

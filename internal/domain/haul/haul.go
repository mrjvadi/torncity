// Package haul holds the pure rules of carrying goods between stores (docs/adr/0075-mining-rail-and-ratios.md section 6): how a
// consignment is cut into trips a carrier can take, how long a trip lasts, what it costs, and the book that proves nothing is
// created or lost while goods are on the road. It reads no database and no clock.
package haul

import (
	"errors"
	"sort"
	"time"
)

// BPS is one whole, in basis points.
const BPS = 10_000

// Carrier kinds.
const (
	KindCart  = "cart"
	KindWagon = "wagon"
	KindRail  = "rail"
)

// Split cuts a consignment of qty units, each unitG grams, into trips that fit a carrier of capKg kilograms. The trips add up to
// qty exactly, none is empty, none is heavier than the carrier takes (at least one unit per trip: a unit heavier than the carrier
// is refused).
func Split(qty, unitG, capKg int64) ([]int64, error) {
	if qty <= 0 {
		return nil, errors.New("haul: nothing to carry")
	}
	if unitG <= 0 {
		unitG = 1000
	}
	per := capKg * 1000 / unitG
	if per <= 0 {
		return nil, errors.New("haul: the carrier cannot lift one unit")
	}
	var trips []int64
	for qty > 0 {
		n := min(qty, per)
		trips = append(trips, n)
		qty -= n
	}
	return trips, nil
}

// Duration is how long a trip of lengthM metres lasts at speedKmh, with the road's or the track's condition: below
// brokenBelowBPS the way is broken and the pace is halved (docs/adr/0075 6.2).
func Duration(lengthM, speedKmh, conditionBPS, brokenBelowBPS int64) time.Duration {
	if speedKmh <= 0 {
		speedKmh = 1
	}
	if conditionBPS < brokenBelowBPS {
		speedKmh = max(speedKmh/2, 1)
	}
	secs := (lengthM*3600 + speedKmh*1000 - 1) / (speedKmh * 1000)
	return time.Duration(secs) * time.Second
}

// FreightFee is what carrying kg kilograms over lengthM metres costs at a rate per tonne-kilometre, minor units, rounded up
// to the unit so a short trip is never free (the Stockton and Darlington charged by the ton-mile, docs/adr/0075 2.2).
func FreightFee(ratePerTonneKm, kg, lengthM int64) int64 {
	if ratePerTonneKm <= 0 || kg <= 0 || lengthM <= 0 {
		return 0
	}
	// rate * (kg/1000 t) * (lengthM/1000 km), in one division
	return (ratePerTonneKm*kg*lengthM + 999_999) / 1_000_000
}

// Book proves conservation: goods leave a store into the road and arrive from it, and the total of stores plus the road never
// changes. It is the pure counterpart of the item journal (reasons haul_out and haul_in) and the open loads.
type Book struct {
	// Stores is item -> store -> units. Road is item -> units on the way.
	Stores map[string]map[string]int64
	Road   map[string]int64
}

// NewBook starts a book.
func NewBook() *Book {
	return &Book{Stores: map[string]map[string]int64{}, Road: map[string]int64{}}
}

// Put adds goods to a store (setup only).
func (b *Book) Put(store, item string, qty int64) {
	if b.Stores[item] == nil {
		b.Stores[item] = map[string]int64{}
	}
	b.Stores[item][store] += qty
}

// ErrNotEnough means the store holds less than the departure asks.
var ErrNotEnough = errors.New("haul: the store holds less than the load")

// Depart takes a load out of a store onto the road.
func (b *Book) Depart(store, item string, qty int64) error {
	if qty <= 0 || b.Stores[item][store] < qty {
		return ErrNotEnough
	}
	b.Stores[item][store] -= qty
	b.Road[item] += qty
	return nil
}

// Arrive brings a load from the road into a store.
func (b *Book) Arrive(store, item string, qty int64) error {
	if qty <= 0 || b.Road[item] < qty {
		return errors.New("haul: more arrives than was on the road")
	}
	b.Road[item] -= qty
	b.Put(store, item, qty)
	return nil
}

// Total is every unit of an item, in the stores and on the road.
func (b *Book) Total(item string) int64 {
	t := b.Road[item]
	for _, q := range b.Stores[item] {
		t += q
	}
	return t
}

// Items lists the items the book knows, sorted.
func (b *Book) Items() []string {
	seen := map[string]bool{}
	for i := range b.Stores {
		seen[i] = true
	}
	for i := range b.Road {
		seen[i] = true
	}
	out := make([]string, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sort.Strings(out)
	return out
}

// Package tender is the pure rules of public works by tender (docs/adr/0076 10): who wins, what a bond and a retention come to, how a price
// is paid by stage, and when an official may not award to a company. No clock, no storage.
package tender

import (
	"errors"
	"sort"
	"time"
)

// BPS is the scale of basis points.
const BPS = 10_000

// Sentinel errors.
var (
	// ErrConflict means an awarding official has a stake in the winning company.
	ErrConflict = errors.New("tender: the awarding official has a stake in the company")
	// ErrDebarred means a company kept from public tenders.
	ErrDebarred = errors.New("tender: the company is debarred")
	// ErrBid means a bid that cannot stand: no price, no days or a bond that is too small.
	ErrBid = errors.New("tender: a bid needs a price, a time and its bond")
)

// Rule is how a tender is awarded.
type Rule string

// The rules.
const (
	LowestPrice Rule = "lowest_price"
	Scored      Rule = "scored"
)

// Valid reports whether r is one of the two.
func (r Rule) Valid() bool { return r == LowestPrice || r == Scored }

// Bid is a company's offer on a tender.
type Bid struct {
	Company string
	Price   int64
	// Days is the time the company says it needs, in real days.
	Days int64
	// Record is the number of works the company finished before (a bigger record scores better).
	Record int64
	// Bond is what it posted; At when.
	Bond int64
	At   time.Time
}

// BondFor is the bond a price calls for: bondBPS of it, rounded up.
func BondFor(price, bondBPS int64) int64 {
	if price <= 0 || bondBPS <= 0 {
		return 0
	}
	return (price*bondBPS + BPS - 1) / BPS
}

// Check says whether a bid can stand on a tender: a price within the budget cap (when there is one), a time, and a bond of at least what
// the price calls for.
func Check(b Bid, budgetCap, bondBPS int64) error {
	if b.Price < 1 || b.Days < 1 || b.Bond < BondFor(b.Price, bondBPS) || (budgetCap > 0 && b.Price > budgetCap) {
		return ErrBid
	}
	return nil
}

// score ranks a bid under the scored rule: a lower number is better. Price counts for most, then the time, then the record: each day over
// the quickest bid costs one percent of the price, each finished work is worth one percent off, never below half the price.
func score(b Bid, quickest int64) int64 {
	over := max(b.Days-quickest, 0)
	adj := b.Price + b.Price*over/100 - b.Price*min(b.Record, 50)/100
	return max(adj, b.Price/2)
}

// Winner picks the winning bid by the rule. Ties go to the earlier bid, then to the smaller company id, so the same bids always give the
// same winner on every replica. It is false when there is no bid.
func Winner(rule Rule, bids []Bid) (Bid, bool) {
	if len(bids) == 0 {
		return Bid{}, false
	}
	quickest := bids[0].Days
	for _, b := range bids {
		quickest = min(quickest, b.Days)
	}
	key := func(b Bid) int64 {
		if rule == Scored {
			return score(b, quickest)
		}
		return b.Price
	}
	sorted := append([]Bid(nil), bids...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ki, kj := key(sorted[i]), key(sorted[j])
		if ki != kj {
			return ki < kj
		}
		if !sorted[i].At.Equal(sorted[j].At) {
			return sorted[i].At.Before(sorted[j].At)
		}
		return sorted[i].Company < sorted[j].Company
	})
	return sorted[0], true
}

// Company is who stands behind a bidding company.
type Company struct {
	Owner    string
	Managers []string
	// Stakes are the holdings of shareholders, basis points of the company, by player.
	Stakes map[string]int64
}

// Conflicts reports whether any of the officials who award a tender is the company's owner or manager, or holds at least refuseFromBPS
// of it. The officials are the acting one and every holder of the awarding office.
func Conflicts(officials []string, c Company, refuseFromBPS int64) bool {
	for _, o := range officials {
		if o == "" {
			continue
		}
		if o == c.Owner {
			return true
		}
		for _, m := range c.Managers {
			if o == m {
				return true
			}
		}
		if refuseFromBPS > 0 && c.Stakes[o] >= refuseFromBPS {
			return true
		}
	}
	return false
}

// Debarred reports whether a company is kept from tenders at now.
func Debarred(until time.Time, now time.Time) bool { return !until.IsZero() && now.Before(until) }

// Stages is how many stages a work is paid in.
const Stages = 3

// StageShare is the part of the price a stage is worth: the price in equal shares, the remainder on the last stage.
func StageShare(price int64, stage int) int64 {
	if stage < 1 || stage > Stages || price < 1 {
		return 0
	}
	s := price / Stages
	if stage == Stages {
		s += price % Stages
	}
	return s
}

// StagePay splits what a stage is worth into what is paid now and what is held back as retention (rounded up, in the company's disfavour
// by at most one unit, so the held part never rounds to nothing while a retention is due).
func StagePay(price int64, stage int, retentionBPS int64) (pay, held int64) {
	share := StageShare(price, stage)
	if share == 0 {
		return 0, 0
	}
	held = (share*max(retentionBPS, 0) + BPS - 1) / BPS
	if held > share {
		held = share
	}
	return share - held, held
}

// StageDone reports whether the work done reaches a stage: the work points of the building against the points it needs, a third each.
func StageDone(done, required int64, stage int) bool {
	if required <= 0 || stage < 1 || stage > Stages {
		return false
	}
	return done*Stages >= required*int64(stage)
}

// StagesDone is how many stages the work has reached.
func StagesDone(done, required int64) int {
	n := 0
	for k := 1; k <= Stages; k++ {
		if StageDone(done, required, k) {
			n = k
		}
	}
	return n
}

// Overdue reports whether a stage deadline and its grace have passed.
func Overdue(deadline time.Time, grace time.Duration, now time.Time) bool {
	return !now.Before(deadline.Add(grace))
}

// RetentionReleaseAt is when the held retention is due: the defects period after the last stage was accepted.
func RetentionReleaseAt(lastStageAt time.Time, defectsDays int64) time.Time {
	return lastStageAt.AddDate(0, 0, int(max(defectsDays, 0)))
}

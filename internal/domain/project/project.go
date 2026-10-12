// Package project holds the pure rules of special projects (docs/adr/0077 4): a long public work of many stages, funded by the treasury and
// by pledges, built from materials delivered to the site and crew-hours worked on it, finished stage by stage and kept up afterwards. It reads
// no database and no clock. The effects a finished work has are content (special_projects.yml) and the application's.
package project

import (
	"errors"
	"sort"
	"time"
)

// BPS is one whole, in basis points.
const BPS = 10_000

// Stage is what one stage of a kind asks: the materials delivered to the site, the crew-hours worked on it, and the least time it takes.
type Stage struct {
	Materials   map[string]int64
	LabourHours int64
	MinHours    int64
}

// Why says what a stage waits for.
type Why string

// The reasons a stage is not done.
const (
	Ready         Why = ""
	WaitMaterials Why = "materials"
	WaitCrew      Why = "crew"
	WaitTime      Why = "time"
	WaitDelay     Why = "delay"
)

// Missing is what is still to be delivered to the stage: the stage's materials less the stock at the site, items still lacking only.
func Missing(s Stage, stock map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for item, need := range s.Materials {
		if have := stock[item]; have < need {
			out[item] = need - have
		}
	}
	return out
}

// Check says whether a stage can be accepted, or what it waits for first. The order is the order a player can act on: materials, then the
// crew, then the time that must pass (a long work does not finish at once however much is poured into it), then a delay done to it.
func Check(s Stage, stock map[string]int64, hours int64, startedAt, delayedUntil, now time.Time) Why {
	if len(Missing(s, stock)) > 0 {
		return WaitMaterials
	}
	if hours < s.LabourHours {
		return WaitCrew
	}
	if s.MinHours > 0 && (startedAt.IsZero() || now.Before(startedAt.Add(time.Duration(s.MinHours)*time.Hour))) {
		return WaitTime
	}
	if !delayedUntil.IsZero() && now.Before(delayedUntil) {
		return WaitDelay
	}
	return Ready
}

// Take is the stock left after a stage uses its materials up. The stock never goes under zero: Check must have passed.
func Take(s Stage, stock map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for item, have := range stock {
		out[item] = have
	}
	for item, need := range s.Materials {
		out[item] -= need
		if out[item] < 0 {
			out[item] = 0
		}
	}
	return out
}

// Pledge is a gift to the fund.
type Pledge struct {
	ID     string
	Amount int64
}

// Refundable says a pledge can still be taken back: before the first stage has started. After that it is spent on the work.
func Refundable(stage int, started bool) bool { return stage <= 1 && !started }

// Refunds splits what is left of a fund among the pledges that are still held, pro rata to what each gave. The treasury's allocation is a
// pledge too (its holder is the settlement). The pieces add up to the fund exactly: the rounding remainder goes to the largest pledge, ties
// to the lowest id, so every replica reaches the same split.
func Refunds(fund int64, pledges []Pledge) map[string]int64 {
	out := map[string]int64{}
	var total int64
	for _, p := range pledges {
		if p.Amount > 0 {
			total += p.Amount
		}
	}
	if fund <= 0 || total <= 0 {
		return out
	}
	var given int64
	for _, p := range pledges {
		if p.Amount <= 0 {
			continue
		}
		share := fund * p.Amount / total
		out[p.ID] = share
		given += share
	}
	if rest := fund - given; rest != 0 {
		ps := append([]Pledge(nil), pledges...)
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].Amount != ps[j].Amount {
				return ps[i].Amount > ps[j].Amount
			}
			return ps[i].ID < ps[j].ID
		})
		out[ps[0].ID] += rest
	}
	return out
}

// CancelOpen says whether a project may still be cancelled: before the stage named by cancelBefore starts.
func CancelOpen(stage int, cancelBefore int64) bool { return int64(stage) < cancelBefore }

// Errors of the pure rules.
var (
	ErrNoStages = errors.New("project: a kind with no stages")
	ErrBadStage = errors.New("project: a stage that asks nothing")
)

// Validate checks the stages of a kind.
func Validate(stages []Stage) error {
	if len(stages) == 0 {
		return ErrNoStages
	}
	for _, s := range stages {
		if len(s.Materials) == 0 && s.LabourHours <= 0 {
			return ErrBadStage
		}
	}
	return nil
}

// ShiftWage is what a crew shift earns for hours worked.
func ShiftWage(hours, perHour int64) int64 {
	if hours <= 0 || perHour <= 0 {
		return 0
	}
	return hours * perHour
}

// NPCHours is how many hours of hired labour the pool gives a project on a day: the most it can hire, less what it has already hired that
// day, and never more than the stage still asks.
func NPCHours(perDay, hiredToday, stillNeeded int64) int64 {
	free := perDay - hiredToday
	if free <= 0 || stillNeeded <= 0 {
		return 0
	}
	return min(free, stillNeeded)
}

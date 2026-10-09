// Package research is the pure part of ADR 0048, research capacity and speed: how many projects a settlement can run
// at once (slots from staffed research buildings, one free slot for everybody), how fast a slot works (the skill of
// its scholars, its building, the settlement's literacy, the catch-up of a settlement behind, a sharing treaty), what a
// project costs when it is ahead of the world's era, and how real work in a field becomes a discount (breakthrough
// progress, "learning by doing").
//
// It performs no I/O, reads no clock and no content: the application layer hands in numbers. Everything is integer
// basis points or whole seconds, so every replica computes the same quote and the quote is what the project records.
//
// The rules are grounded in the real world, not copied from a game: Rogers' diffusion of innovations (the pioneers pay
// more, the followers less), Cohen and Levinthal's absorptive capacity (a settlement learns what its own prior
// knowledge and people let it), Arrow's learning by doing (experience of work, not of study, is what makes the next
// step cheaper), technology transfer between parties by agreement (a sharing treaty). Games are references only.
package research

import (
	"errors"
	"sort"
	"time"
)

// BPS is 100 percent in basis points.
const BPS int64 = 10_000

// Rules are the tuning numbers (config settlement.research_*), copied in.
type Rules struct {
	// FreeSlots every settlement has with no building (the owner's default: one).
	FreeSlots int
	// SpeedFloorBPS is the slowest a slot may work (10000: never slower than the old single project).
	SpeedFloorBPS int64
	// ScholarFloorBPS is what any staffed scholar adds, however unskilled; SkillBPSPerLevel what each level of the
	// scholar's skill adds on top; ScholarCapBPS the most one scholar adds. NPCScholarLevel is the skill level an NPC
	// scholar of the pool is taken to have.
	ScholarFloorBPS, SkillBPSPerLevel, ScholarCapBPS int64
	NPCScholarLevel                                  int
	// LiteracyBonusBPS is what a fully literate settlement adds to every slot (research is read and written).
	LiteracyBonusBPS int64
	// CatchUpBPS is what an item held by every settlement would add to the speed of one that lacks it: the more the
	// world holds it, the easier it is to learn (diffusion, Rogers).
	CatchUpBPS int64
	// Era: the world's frontier depth is the deepest depth held by at least EraShareBPS of the settlements, never less
	// than EraBaseDepth; an item deeper than the frontier plus EraGrace is ahead by the steps beyond it and costs
	// AheadPerStepBPS more (cost and time) per step, at most AheadCapBPS more.
	EraBaseDepth, EraGrace          int
	EraShareBPS                     int64
	AheadPerStepBPS, AheadCapBPS    int64
	// Sharing: each partner of a research-sharing treaty that holds the item adds SharePerPartnerBPS to the speed, at
	// most ShareCapBPS in all (the owner's +50 percent).
	SharePerPartnerBPS, ShareCapBPS int64
	// Breakthrough: BreakthroughNeedPerDepth points of experience in the item's field per depth of the item give the
	// whole BreakthroughMaxBPS discount; less gives a proportional part.
	BreakthroughNeedPerDepth, BreakthroughMaxBPS int64
}

// Enabled reports whether the rules are configured.
func (r Rules) Enabled() bool { return r.FreeSlots > 0 && r.SpeedFloorBPS > 0 }

// Node is one knowledge item as the depth rule sees it.
type Node struct {
	Code               string
	Requires           []string
	RequiresCapability []string
	Provides           []string
}

// ErrCycle means the prerequisites loop (the content lint refuses it first).
var ErrCycle = errors.New("research: the prerequisites form a cycle")

// Depths is the depth of every item: one more than the deepest of its prerequisites; a capability counts as the
// shallowest item that provides it (any provider satisfies it).
func Depths(nodes []Node) (map[string]int, error) {
	by := map[string]Node{}
	prov := map[string][]string{}
	for _, n := range nodes {
		by[n.Code] = n
		p := n.Provides
		if len(p) == 0 {
			p = []string{n.Code}
		}
		for _, c := range p {
			prov[c] = append(prov[c], n.Code)
		}
	}
	out := map[string]int{}
	state := map[string]int{} // 1 visiting, 2 done
	var depth func(code string) (int, error)
	depth = func(code string) (int, error) {
		if state[code] == 2 {
			return out[code], nil
		}
		if state[code] == 1 {
			return 0, ErrCycle
		}
		state[code] = 1
		n, ok := by[code]
		if !ok {
			state[code] = 2
			return 0, nil
		}
		best := 0
		for _, r := range n.Requires {
			d, err := depth(r)
			if err != nil {
				return 0, err
			}
			best = max(best, d)
		}
		for _, c := range n.RequiresCapability {
			shallowest := -1
			providers := append([]string(nil), prov[c]...)
			sort.Strings(providers)
			for _, p := range providers {
				if p == code {
					continue
				}
				d, err := depth(p)
				if err != nil {
					return 0, err
				}
				if shallowest < 0 || d < shallowest {
					shallowest = d
				}
			}
			best = max(best, max(shallowest, 0))
		}
		out[code] = best + 1
		state[code] = 2
		return out[code], nil
	}
	codes := make([]string, 0, len(by))
	for c := range by {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		if _, err := depth(c); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Frontier is the depth the world has reached: the deepest depth of an item held by at least EraShareBPS of the
// settlements (holders share in basis points), and never less than EraBaseDepth.
func Frontier(depths map[string]int, holdersShareBPS func(code string) int64, r Rules) int {
	f := r.EraBaseDepth
	for code, d := range depths {
		if d > f && holdersShareBPS(code) >= r.EraShareBPS {
			f = d
		}
	}
	return f
}

// AheadBPS is the factor (10000 is none) an item of that depth costs against the world's frontier: it grows by
// AheadPerStepBPS for each step the item is beyond the frontier and the grace, up to AheadCapBPS more.
func AheadBPS(depth, frontier int, r Rules) int64 {
	steps := depth - frontier - r.EraGrace
	if steps <= 0 {
		return BPS
	}
	extra := int64(steps) * r.AheadPerStepBPS
	if r.AheadCapBPS > 0 && extra > r.AheadCapBPS {
		extra = r.AheadCapBPS
	}
	return BPS + extra
}

// ScholarBPS is what one scholar of that skill level adds to a slot's speed: a floor for any staffed scholar, plus the
// skill, never above the cap.
func ScholarBPS(level int, r Rules) int64 {
	v := r.ScholarFloorBPS + int64(max(level, 0))*r.SkillBPSPerLevel
	if r.ScholarCapBPS > 0 && v > r.ScholarCapBPS {
		v = r.ScholarCapBPS
	}
	return max(v, 0)
}

// Building is one research building as one local day found it.
type Building struct {
	ID string
	// Slots is what it gives while it is staffed; MinStaff the scholars it needs for that; BonusBPS the speed its
	// shelves, benches and rooms add to a project in it.
	Slots, MinStaff int
	BonusBPS        int64
	// Skills are the skill levels of the scholars on duty today (players' own, NPCs' at the NPC level).
	Skills []int
}

// Slot is the capacity the free slot or one building gives today.
type Slot struct {
	// Ref is "free" or the building id.
	Ref      string
	Capacity int
	// BonusBPS is the building's own; StaffBPS the sum of its scholars (shared by its running projects).
	BonusBPS, StaffBPS int64
}

// FreeRef is the reference of the free slot.
const FreeRef = "free"

// Slots lists today's capacity: the free slots first, then every staffed building in the order given.
func Slots(buildings []Building, r Rules) []Slot {
	out := []Slot{{Ref: FreeRef, Capacity: r.FreeSlots}}
	for _, b := range buildings {
		if b.Slots < 1 || len(b.Skills) < max(b.MinStaff, 1) {
			continue // unstaffed: it stands idle and gives nothing
		}
		s := Slot{Ref: b.ID, Capacity: b.Slots, BonusBPS: b.BonusBPS}
		for _, lv := range b.Skills {
			s.StaffBPS += ScholarBPS(lv, r)
		}
		out = append(out, s)
	}
	return out
}

// Capacity is the number of projects the settlement can run at once.
func Capacity(slots []Slot) int {
	n := 0
	for _, s := range slots {
		n += s.Capacity
	}
	return n
}

// Input is everything the price and the pace of one project depend on.
type Input struct {
	BaseCost int64
	BaseTime time.Duration
	Depth    int
	Frontier int
	// Slot is the slot the project would take; Running the projects already running in it (the new one is one more).
	Slot    Slot
	Running int
	// LiteracyBPS is the settlement's literacy share; HoldersShareBPS how widely the world holds the item (0 when it
	// is not yet held anywhere); SharePartners how many partners of research-sharing treaties hold it.
	LiteracyBPS, HoldersShareBPS int64
	SharePartners                int
	// Experience is the settlement's points in the item's field (0 when the item names none).
	Experience int64
}

// Quote is what a project costs and how long it takes, and the parts it is made of; the project records all of them.
type Quote struct {
	Cost     int64
	Effort   time.Duration // the time at speed 10000
	Duration time.Duration // the time at the project's speed
	SpeedBPS int64
	AheadBPS int64
	// DiscountBPS is the breakthrough discount, ShareBPS the sharing bonus locked into the speed, CatchUpBPS the part
	// of the speed that is the catch-up of a settlement behind.
	DiscountBPS, ShareBPS, CatchUpBPS int64
}

// Price makes the quote. The cost and the effort are the base times the ahead-of-era factor times what the breakthrough
// leaves; the speed is the base plus the building, the staff's share, the literacy, the catch-up and the sharing, never
// below the floor; the duration is the effort at that speed. A cost is at least 1 and a duration at least a second.
func Price(in Input, r Rules) Quote {
	q := Quote{AheadBPS: AheadBPS(in.Depth, in.Frontier, r)}
	need := int64(max(in.Depth, 1)) * r.BreakthroughNeedPerDepth
	q.DiscountBPS = Discount(in.Experience, need, r.BreakthroughMaxBPS)
	keep := BPS - q.DiscountBPS
	q.Cost = max(in.BaseCost*q.AheadBPS/BPS*keep/BPS, min(in.BaseCost, 1))
	secs := int64(in.BaseTime / time.Second)
	effort := secs * q.AheadBPS / BPS * keep / BPS
	q.Effort = time.Duration(effort) * time.Second
	q.CatchUpBPS = CatchUp(in.HoldersShareBPS, r)
	q.ShareBPS = min(int64(max(in.SharePartners, 0))*r.SharePerPartnerBPS, r.ShareCapBPS)
	speed := BPS + in.Slot.BonusBPS + in.Slot.StaffBPS/int64(max(in.Running+1, 1)) +
		in.LiteracyBPS*r.LiteracyBonusBPS/BPS + q.CatchUpBPS + q.ShareBPS
	q.SpeedBPS = max(speed, r.SpeedFloorBPS)
	q.Duration = time.Duration(max(effort*BPS/q.SpeedBPS, 1)) * time.Second
	if secs == 0 {
		q.Duration = 0
	}
	return q
}

// CatchUp is the speed a settlement behind gains when the world already holds the item: proportional to the share of
// settlements that hold it.
func CatchUp(holdersShareBPS int64, r Rules) int64 {
	if holdersShareBPS <= 0 || r.CatchUpBPS <= 0 {
		return 0
	}
	return min(holdersShareBPS, BPS) * r.CatchUpBPS / BPS
}

// Discount is the share of an item's price that experience in its field has already paid: proportional to the points
// against what the item needs, at most maxBPS.
func Discount(points, need, maxBPS int64) int64 {
	if points <= 0 || need <= 0 || maxBPS <= 0 {
		return 0
	}
	return min(points*maxBPS/need, maxBPS)
}

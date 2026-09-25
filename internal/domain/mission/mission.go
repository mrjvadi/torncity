// Package mission holds the rules of missions: what a mission asks for (its
// objectives), how something that happened in the game moves it on, when a
// player may take one — once, or again after a cooldown on the game clock,
// after the missions it builds on and at a level — and how much of its cash
// reward the caps let through.
//
// A mission is content (configs/content/missions.yml). An objective is
// expressed as one of the events the game already has: a journey arriving, a
// shift worked, a course completed, a good bought, sold or used, a crime
// done — or goods handed in at the board. Nothing here reads or writes
// anything; every rule is a pure function.
package mission

import (
	"errors"
	"fmt"
	"time"
)

// Kind is what an objective counts. The set is closed: each kind is one
// event the game emits, or the hand-in at a board.
type Kind string

const (
	// Travel counts journeys arriving; a target names the city.
	Travel Kind = "travel"
	// Work counts shifts worked; a target names a career or a category.
	Work Kind = "work_shift"
	// Course counts courses completed; a target names the course.
	Course Kind = "course"
	// Buy counts units of goods bought at a city shop; a target names the
	// good.
	Buy Kind = "buy_item"
	// Sell counts units sold, to a shop or on the market; a target names the
	// good.
	Sell Kind = "sell_item"
	// Crime counts crimes that succeeded; a target names a crime or its
	// category.
	Crime Kind = "crime"
	// Use counts goods used; a target names the good or its category.
	Use Kind = "use_item"
	// Deliver is units of a good handed in at the mission's board: counted
	// at the hand-in, never from an event. The target is the good.
	Deliver Kind = "deliver"
)

var kinds = map[Kind]bool{Travel: true, Work: true, Course: true, Buy: true, Sell: true, Crime: true, Use: true, Deliver: true}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return kinds[k] }

// Objective is one thing a mission asks for.
type Objective struct {
	Kind Kind
	// Target narrows what counts; empty counts any.
	Target string
	// Count is how many times, or how many units, it takes.
	Count int64
}

// Event is something that happened, as a mission reads it.
type Event struct {
	Kind Kind
	// Targets are the codes it can be matched by: the city; the career and
	// its category; the course; the good and its category; the crime and
	// its category.
	Targets []string
	// Qty is the units it moves: a purchase or a sale of several; 1
	// otherwise.
	Qty int64
}

// Matches reports whether the event counts toward the objective.
func (o Objective) Matches(e Event) bool {
	if o.Kind != e.Kind || o.Kind == Deliver {
		return false
	}
	if o.Target == "" {
		return true
	}
	for _, t := range e.Targets {
		if t == o.Target {
			return true
		}
	}
	return false
}

// Reward is what completing a mission gives.
type Reward struct {
	Cash  int64
	XP    int64
	Items []ItemReward
}

// ItemReward is units of a good a reward gives.
type ItemReward struct {
	Item string
	Qty  int64
}

// Mission is one mission.
type Mission struct {
	Code  string
	Board string
	// MinLevel is the character level it asks for; Requires the missions
	// the player must have completed first.
	MinLevel int
	Requires []string
	// Objectives, in order; all must be met.
	Objectives []Objective
	// Repeatable says it may be taken again; Cooldown is how long after a
	// completion, GAME time (a daily mission: 24h).
	Repeatable bool
	Cooldown   time.Duration
	// TimeLimit is how long a taken mission may run, GAME time; zero is no
	// limit.
	TimeLimit time.Duration
	Reward    Reward
}

// Limits bound what content may ask.
const (
	MaxObjectives = 6
	MaxCount      = 1_000
	MaxCash       = 10_000_000
	MaxXP         = 100_000
	MaxItems      = 5
	MaxItemQty    = 100
)

// ErrInvalidMission means a mission is unusable.
var ErrInvalidMission = errors.New("mission: invalid mission")

// Validate checks a mission's own shape; cross-references (a board, a good,
// a city) are the content loader's.
func (m Mission) Validate() error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrInvalidMission, m.Code, fmt.Sprintf(format, args...))
	}
	switch {
	case m.Code == "" || m.Board == "":
		return bad("no code or board")
	case len(m.Objectives) == 0 || len(m.Objectives) > MaxObjectives:
		return bad("%d objectives, want 1..%d", len(m.Objectives), MaxObjectives)
	case m.MinLevel < 0:
		return bad("min level %d", m.MinLevel)
	case m.Cooldown < 0 || m.TimeLimit < 0 || (!m.Repeatable && m.Cooldown > 0):
		return bad("cooldown %s, time limit %s, repeatable %v", m.Cooldown, m.TimeLimit, m.Repeatable)
	case m.Reward.Cash < 0 || m.Reward.Cash > MaxCash || m.Reward.XP < 0 || m.Reward.XP > MaxXP:
		return bad("reward %d cash, %d xp", m.Reward.Cash, m.Reward.XP)
	case len(m.Reward.Items) > MaxItems:
		return bad("%d reward items", len(m.Reward.Items))
	case m.Reward.Cash == 0 && m.Reward.XP == 0 && len(m.Reward.Items) == 0:
		return bad("rewards nothing")
	}
	for i, o := range m.Objectives {
		if !o.Kind.Valid() || o.Count < 1 || o.Count > MaxCount || (o.Kind == Deliver && o.Target == "") {
			return bad("objective %d: %s %q x%d", i, o.Kind, o.Target, o.Count)
		}
	}
	for i, it := range m.Reward.Items {
		if it.Item == "" || it.Qty < 1 || it.Qty > MaxItemQty {
			return bad("reward item %d: %q x%d", i, it.Item, it.Qty)
		}
	}
	for _, r := range m.Requires {
		if r == m.Code || r == "" {
			return bad("requires itself or nothing")
		}
	}
	return nil
}

// Advance applies an event to a mission's progress (one count per
// objective): each objective it matches moves on by its quantity, never past
// its count. It returns the new progress and whether anything moved.
func Advance(objs []Objective, progress []int64, e Event) ([]int64, bool) {
	next := normalise(objs, progress)
	qty := e.Qty
	if qty < 1 {
		qty = 1
	}
	moved := false
	for i, o := range objs {
		if !o.Matches(e) || next[i] >= o.Count {
			continue
		}
		next[i] = min(o.Count, next[i]+qty)
		moved = true
	}
	return next, moved
}

// Deliverable is how many units of item the hand-in at the board would
// take for each deliver objective still open, holding held units.
func Deliverable(objs []Objective, progress []int64, item string, held int64) []int64 {
	p := normalise(objs, progress)
	out := make([]int64, len(objs))
	for i, o := range objs {
		if o.Kind != Deliver || o.Target != item || held <= 0 {
			continue
		}
		take := min(o.Count-p[i], held)
		if take > 0 {
			out[i] = take
			held -= take
		}
	}
	return out
}

// Done reports whether every objective is met.
func Done(objs []Objective, progress []int64) bool {
	p := normalise(objs, progress)
	for i, o := range objs {
		if p[i] < o.Count {
			return false
		}
	}
	return true
}

// normalise is progress with one count per objective.
func normalise(objs []Objective, progress []int64) []int64 {
	out := make([]int64, len(objs))
	copy(out, progress)
	return out
}

// History is what decides whether a player may take a mission now.
type History struct {
	Level int
	// Active is whether they have it running; Completed when they last
	// completed each mission, zero for never.
	Active    bool
	Completed map[string]time.Time
}

// Why a mission cannot be taken.
const (
	BlockedNone      = ""
	BlockedActive    = "active"
	BlockedLevel     = "level"
	BlockedRequires  = "requires"
	BlockedDone      = "done"
	BlockedCooldown  = "cooldown"
	BlockedTooMany   = "too_many"
	BlockedDailyCaps = "caps"
)

// Available says whether a player with history h may take m at now, and if
// not, why; for a cooldown, the real wait left. wait turns the mission's
// GAME cooldown into real time (gametime.Scale.RealWait).
func (m Mission) Available(h History, now time.Time, wait func(time.Duration) time.Duration) (string, time.Duration) {
	switch {
	case h.Active:
		return BlockedActive, 0
	case h.Level < m.MinLevel:
		return BlockedLevel, 0
	}
	for _, r := range m.Requires {
		if h.Completed[r].IsZero() {
			return BlockedRequires, 0
		}
	}
	last := h.Completed[m.Code]
	if last.IsZero() {
		return BlockedNone, 0
	}
	if !m.Repeatable {
		return BlockedDone, 0
	}
	if m.Cooldown > 0 && wait != nil {
		if left := last.Add(wait(m.Cooldown)).Sub(now); left > 0 {
			return BlockedCooldown, left
		}
	}
	return BlockedNone, 0
}

// Expired reports whether a mission taken at accepted, with a time limit
// ending at expires (zero for none), has run out at now.
func Expired(expires, now time.Time) bool { return !expires.IsZero() && !now.Before(expires) }

// CapCash is what of a cash reward is paid, and what is withheld, when the
// player may still receive playerLeft today and the whole economy
// economyLeft: never more than either. A negative allowance counts as none.
func CapCash(cash, playerLeft, economyLeft int64) (paid, withheld int64) {
	if cash <= 0 {
		return 0, 0
	}
	paid = min(cash, max(playerLeft, 0), max(economyLeft, 0))
	return paid, cash - paid
}

// Package intel holds the pure rules of intelligence between settlements (docs/adr/0077 5 and 6): the cover an agent builds by living and
// working in the target, the chance of an operation, the banded report it brings, the exposure of a blown operation, the expulsion, and the
// limits of sabotage. It reads no database and no clock; every chance is a seeded roll, so any replica reaches the same answer.
//
// This is a model of an institution, not a manual: roles, chances, costs and consequences. A report can only hold the codes of a band
// vocabulary, so it cannot carry an exact figure or a person.
package intel

import (
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BPS is one whole, in basis points.
const BPS = 10_000

// Rules are the tuning of intelligence (config intel.*).
type Rules struct {
	BaseChanceBPS, SkillBPSPerLevel, ChanceFloorBPS, ChanceCapBPS int64
	CoverCapDays, CoverBPSPerDay                                  int64
	CounterPostBPS, CounterVetBPS, CounterSmallBPS                int64
	SmallResidents                                                int64
	DetectBPS, AttributionBPS                                     int64
	ExpelDays                                                     int64
	ReportTTL                                                     time.Duration
}

// Cover is the strength of an agent's cover in basis points: a bonus for each day of real residence and work in the target, up to a cap.
func (r Rules) Cover(days int64) int64 {
	if days <= 0 {
		return 0
	}
	return min(days, r.CoverCapDays) * r.CoverBPSPerDay
}

// Counter is the defender's counter-intelligence in basis points: each staffed watch or border post on duty, the charter's vetting policy,
// and the plain fact that a small settlement notices a stranger.
func (r Rules) Counter(staffedPosts int64, vetting bool, residents int64) int64 {
	c := max(staffedPosts, 0) * r.CounterPostBPS
	if vetting {
		c += r.CounterVetBPS
	}
	if residents > 0 && residents < r.SmallResidents {
		c += r.CounterSmallBPS
	}
	return c
}

// Chance is the chance of an operation: a base, the agent's skill, the cover, less the defender's counter-intelligence, held between a floor
// and a cap so that nothing is certain and nothing is hopeless.
func (r Rules) Chance(skillLevel, coverDays, counter int64) int64 {
	c := r.BaseChanceBPS + max(skillLevel, 0)*r.SkillBPSPerLevel + r.Cover(coverDays) - max(counter, 0)
	return min(max(c, r.ChanceFloorBPS), r.ChanceCapBPS)
}

// Outcome is how an operation went.
type Outcome string

// The outcomes. Clean brings a report of high or medium confidence, partial one of low confidence, blown none and a test of exposure.
const (
	Clean   Outcome = "clean"
	Partial Outcome = "partial"
	Blown   Outcome = "blown"
)

// Confidence of a report.
const (
	ConfLow    = "low"
	ConfMedium = "medium"
	ConfHigh   = "high"
)

// Resolve is the outcome of a roll against a chance: under two thirds of the chance is clean, under the chance partial, else blown. The
// confidence follows how far under the roll was.
func Resolve(chance, roll int64) (Outcome, string) {
	switch {
	case roll >= chance:
		return Blown, ""
	case roll < chance/3:
		return Clean, ConfHigh
	case roll < chance*2/3:
		return Clean, ConfMedium
	}
	return Partial, ConfLow
}

// Roll is a seeded roll in 0..9999 from a label (the operation's id and a purpose). The same label gives the same roll on any replica.
func Roll(label string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(label))
	return int64(h.Sum64() % BPS)
}

// Detected reports whether a roll is under the chance.
func Detected(chance, roll int64) bool { return roll < chance }

// Combine is the chance that at least one of several independent detectors notices: one minus the product of the misses.
func Combine(chances []int64) int64 {
	miss := int64(BPS)
	for _, c := range chances {
		c = min(max(c, 0), BPS)
		miss = miss * (BPS - c) / BPS
	}
	return BPS - miss
}

// Expel is the instant until which an expelled agent may not reside in the settlement again.
func (r Rules) Expel(now time.Time) time.Time { return now.Add(time.Duration(r.ExpelDays) * 24 * time.Hour) }

// Aged reports whether a report is older than the time it keeps fresh for.
func (r Rules) Aged(takenAt, now time.Time) bool { return now.Sub(takenAt) >= r.ReportTTL }

// Fresh is the instant a report stops being fresh.
func (r Rules) Fresh(takenAt time.Time) time.Time { return takenAt.Add(r.ReportTTL) }

// ---------------------------------------------------------------------------
// Bands: what a report may say
// ---------------------------------------------------------------------------

// Band is a metric's vocabulary: the names of its bands, low to high, and the upper edges between them (one fewer than the names).
type Band struct {
	Names []string
	Edges []int64
}

// Of names the band a value falls in. The value itself goes no further.
func (b Band) Of(v int64) string {
	if len(b.Names) == 0 {
		return ""
	}
	for i, e := range b.Edges {
		if v < e && i < len(b.Names) {
			return b.Names[i]
		}
	}
	return b.Names[len(b.Names)-1]
}

// Validate checks the band: names are plain words, edges ascend, one more name than edges.
func (b Band) Validate() error {
	if len(b.Names) < 2 || len(b.Edges) != len(b.Names)-1 {
		return errors.New("intel: a band has one more name than edges, and two names at least")
	}
	for _, n := range b.Names {
		if !word(n) {
			return errors.New("intel: a band name is a plain word, not a figure: " + n)
		}
	}
	for i := 1; i < len(b.Edges); i++ {
		if b.Edges[i-1] >= b.Edges[i] {
			return errors.New("intel: band edges ascend")
		}
	}
	return nil
}

// word is a lowercase word of letters and underscores: never a number.
func word(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == '_') {
			return false
		}
	}
	return true
}

// Fields is a report's body: a metric code and the name of a band, nothing else.
type Fields map[string]string

// ErrFigure means a report field carries something other than a band name: a figure, a name or free text.
var ErrFigure = errors.New("intel: a report holds band names, never a figure or a name")

// Check refuses a field set that holds anything but the names of the bands of its metrics.
func (f Fields) Check(bands map[string]Band) error {
	for k, v := range f {
		b, ok := bands[k]
		if !ok || !word(k) || !word(v) {
			return ErrFigure
		}
		found := false
		for _, n := range b.Names {
			if n == v {
				found = true
			}
		}
		if !found {
			return ErrFigure
		}
		if _, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return ErrFigure
		}
	}
	return nil
}

// Widen turns a partial report's bands one step toward the middle: a low-confidence report is vaguer. A band at an end moves inward.
func Widen(f Fields, bands map[string]Band) Fields {
	out := Fields{}
	for k, v := range f {
		b, ok := bands[k]
		if !ok {
			continue
		}
		i := 0
		for j, n := range b.Names {
			if n == v {
				i = j
			}
		}
		mid := (len(b.Names) - 1) / 2
		switch {
		case i < mid:
			i++
		case i > mid:
			i--
		}
		out[k] = b.Names[i]
	}
	return out
}

// Keys lists a field set's metric codes in order.
func (f Fields) Keys() []string {
	ks := make([]string, 0, len(f))
	for k := range f {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// ---------------------------------------------------------------------------
// Sabotage
// ---------------------------------------------------------------------------

// SabotageRules are the caps of sabotage (config intel.sabotage_*).
type SabotageRules struct {
	DelayHours, SpoilBPS, BlindBPS int64
	WeeklyLimit                    int64
	PerTargetDays, SkipDays        int64
	Cost                           int64
}

// Kind of sabotage.
const (
	DelayWork      = "delay_work"
	SpoilStock     = "spoil_stock"
	BlindPost      = "blind_post"
	SkipServiceDay = "skip_service_day"
)

// Kinds lists the kinds of sabotage there are. There is no kind that destroys a building, a stage or a person's property, or harms a person.
func Kinds() []string { return []string{DelayWork, SpoilStock, BlindPost, SkipServiceDay} }

// Refusal says why an act of sabotage is refused.
type Refusal string

// The refusals.
const (
	Allowed      Refusal = ""
	UnknownKind  Refusal = "unknown_kind"
	WeeklyLimit  Refusal = "weekly_limit"
	TooSoon      Refusal = "too_soon"
	Protected    Refusal = "protected"
	Forbidden    Refusal = "forbidden"
	NoCover      Refusal = "no_cover"
	NothingToDo  Refusal = "nothing_to_do"
	PersonalGood Refusal = "personal"
)

// History is what the sponsor and the target have already seen of sabotage.
type History struct {
	// ActsThisWeek is the sponsor's acts in the last seven days; LastSameAt the last act of this kind on this very target (zero if none).
	ActsThisWeek int64
	LastSameAt   time.Time
	// ShieldedOrGrace: the target is in its founding grace or under a post-loss shield. TreatyForbids: a treaty between the two forbids it.
	ShieldedOrGrace, TreatyForbids bool
	// HasCover: the agent resides and works in the target now.
	HasCover bool
}

// Check decides whether an act may be done now.
func (r SabotageRules) Check(kind string, h History, now time.Time) Refusal {
	var gap int64
	switch kind {
	case DelayWork, SpoilStock, BlindPost:
		gap = r.PerTargetDays
	case SkipServiceDay:
		gap = r.SkipDays
	default:
		return UnknownKind
	}
	switch {
	case !h.HasCover:
		return NoCover
	case h.ShieldedOrGrace:
		return Protected
	case h.TreatyForbids:
		return Forbidden
	case h.ActsThisWeek >= r.WeeklyLimit:
		return WeeklyLimit
	case !h.LastSameAt.IsZero() && now.Sub(h.LastSameAt) < time.Duration(gap)*24*time.Hour:
		return TooSoon
	}
	return Allowed
}

// Spoil is how much of one stack an act of spoilage destroys: the share, at least one unit, never more than the stack.
func (r SabotageRules) Spoil(stack int64) int64 {
	if stack <= 0 {
		return 0
	}
	n := stack * r.SpoilBPS / BPS
	if n < 1 {
		n = 1
	}
	return min(n, stack)
}

// Delay is the instant a stage may be accepted from after an act of delay: the hours are added to whatever delay still stands, never more
// than DelayHours past now (two acts do not add up beyond one act's reach).
func (r SabotageRules) Delay(standing, now time.Time) time.Time {
	limit := now.Add(time.Duration(r.DelayHours) * time.Hour)
	next := standing
	if next.Before(now) {
		next = now
	}
	next = next.Add(time.Duration(r.DelayHours) * time.Hour)
	if next.After(limit) {
		return limit
	}
	return next
}

// Blind is a detector's chance for the day of a blinding: lowered by the share, never under zero.
func (r SabotageRules) Blind(chance int64) int64 { return max(chance-chance*r.BlindBPS/BPS, 0) }

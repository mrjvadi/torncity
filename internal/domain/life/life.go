// Package life holds the rules of a character's life (docs/adr/0025): the
// needs that drift on the game clock — hunger, sleep and stress — and the
// mood they and the rest of a life push happiness toward; how a hard-pressed
// body works, steals and learns a little worse; age; intelligence and what
// it speeds up; the rank a player holds by what they are worth, which rises
// and falls with it; a bio's rules; and the score a city is ranked by.
//
// Every number here is CONTENT (configs/content/life.yml) handed in by the
// layer above; the formulas are the code. Nothing ticks: a need is stored
// with the instant it describes, and its value at any later instant is a pure
// function of the two (Needs.At). The package reads no clock and no file.
package life

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/domain/gametime"
)

// ErrInvalid means life rules content cannot use.
var ErrInvalid = errors.New("life: invalid rules")

// Milli is the resolution a need is stored at: a need of 42.5 points is
// 42500. Points are what a player sees, 0..MaxPoints.
const Milli = 1000

// MaxPoints is the top of every need and of happiness.
const MaxPoints = 100

// bpsWhole is 100%.
const bpsWhole = 10_000

// gameDay is the unit every drift rate is written per.
const gameDay = 24 * time.Hour

// Needs are a character's hunger, need of sleep and stress, each 0..100
// points (stored in milli-points), and the instant they describe. Higher is
// worse: 0 hunger is a full stomach, 100 starving.
type Needs struct {
	Hunger, Sleep, Stress int64
	Since                 time.Time
}

// Points is a milli-point value as the whole points a player sees, rounded
// to the nearest.
func Points(milli int64) int {
	return int((clampMilli(milli) + Milli/2) / Milli)
}

func clampMilli(v int64) int64 { return min(max(v, 0), MaxPoints*Milli) }

// Drift is how the needs move on their own, in points per GAME day: hunger
// and the need of sleep rise; stress falls, and falls faster the happier a
// character is (HappyStressFallPerDay at full happiness, in proportion
// below).
type Drift struct {
	HungerPerDay          int64
	SleepPerDay           int64
	StressFallPerDay      int64
	HappyStressFallPerDay int64
}

// Validate checks the rates.
func (d Drift) Validate() error {
	for _, v := range []int64{d.HungerPerDay, d.SleepPerDay, d.StressFallPerDay, d.HappyStressFallPerDay} {
		if v < 0 || v > 10*MaxPoints*24 {
			return fmt.Errorf("%w: a drift rate %d is outside 0..%d a game day", ErrInvalid, v, 10*MaxPoints*24)
		}
	}
	return nil
}

// GameElapsed is how much GAME time passes between two real instants on the
// clock: the real time times the scale. Time running backwards is none. It
// saturates rather than overflows for an absurd span.
func GameElapsed(from, to time.Time, scale gametime.Scale) time.Duration {
	if !to.After(from) {
		return 0
	}
	if scale.Validate() != nil {
		scale = 1
	}
	real := to.Sub(from)
	const ceiling = time.Duration(1<<62 - 1)
	if real > ceiling/time.Duration(scale) {
		return ceiling
	}
	return real * time.Duration(scale)
}

// perGameTime is what rate points a game day come to over elapsed game time,
// in milli-points.
func perGameTime(ratePerDay int64, elapsed time.Duration) int64 {
	if ratePerDay <= 0 || elapsed <= 0 {
		return 0
	}
	days := float64(elapsed) / float64(gameDay)
	v := float64(ratePerDay*Milli) * days
	if v > float64(MaxPoints*Milli) {
		return MaxPoints * Milli
	}
	return int64(v)
}

// At is the needs as they stand at now: hunger and sleep risen, stress
// fallen, each held inside 0..100. happiness (0..100) is the mood over the
// span, which speeds stress's fall. An earlier now changes nothing.
func (n Needs) At(now time.Time, scale gametime.Scale, d Drift, happiness int) Needs {
	if n.Since.IsZero() {
		n.Since = now
		return n
	}
	elapsed := GameElapsed(n.Since, now, scale)
	if elapsed <= 0 {
		return n
	}
	mood := int64(min(max(happiness, 0), MaxPoints))
	fall := perGameTime(d.StressFallPerDay, elapsed) + perGameTime(d.HappyStressFallPerDay, elapsed)*mood/MaxPoints
	return Needs{
		Hunger: clampMilli(n.Hunger + perGameTime(d.HungerPerDay, elapsed)),
		Sleep:  clampMilli(n.Sleep + perGameTime(d.SleepPerDay, elapsed)),
		Stress: clampMilli(n.Stress - fall),
		Since:  now,
	}
}

// Change moves the needs by whole points (negative lowers), each held inside
// 0..100: eating lowers hunger, a night's sleep the need of sleep, a shift
// raises stress.
func (n Needs) Change(hunger, sleep, stress int) Needs {
	n.Hunger = clampMilli(n.Hunger + int64(hunger)*Milli)
	n.Sleep = clampMilli(n.Sleep + int64(sleep)*Milli)
	n.Stress = clampMilli(n.Stress + int64(stress)*Milli)
	return n
}

// Condition is how hard needs press on a character, and what a low mood
// costs.
type Condition struct {
	// High and Severe are the points a need starts to cost at, and costs
	// more at.
	High, Severe int
	// HighBPS and SevereBPS are what one need over each costs; MaxBPS the
	// most all of them together may.
	HighBPS, SevereBPS, MaxBPS int
	// LowMood is the happiness under which experience comes slower, by
	// LowMoodXPBPS.
	LowMood      int
	LowMoodXPBPS int
}

// Validate checks the condition rules.
func (c Condition) Validate() error {
	switch {
	case c.High < 1 || c.High > MaxPoints || c.Severe < c.High || c.Severe > MaxPoints:
		return fmt.Errorf("%w: need thresholds high %d, severe %d", ErrInvalid, c.High, c.Severe)
	case c.HighBPS < 0 || c.SevereBPS < c.HighBPS || c.MaxBPS < 0 || c.MaxBPS > 9000:
		return fmt.Errorf("%w: need penalties %d, %d, max %d (at most 9000)", ErrInvalid, c.HighBPS, c.SevereBPS, c.MaxBPS)
	case c.LowMood < 0 || c.LowMood > MaxPoints || c.LowMoodXPBPS < 0 || c.LowMoodXPBPS > 5000:
		return fmt.Errorf("%w: low mood %d at %d bps (at most 5000)", ErrInvalid, c.LowMood, c.LowMoodXPBPS)
	}
	return nil
}

// Effects are what a character's condition does to what they do, each a
// multiplier where 10000 is unchanged. They are bounded: never below
// 10000 − MaxBPS, never above 10000. Nothing kills.
type Effects struct {
	// BodyBPS scales energy regeneration, a shift's output and a crime's
	// chance: a hungry, tired or stressed body does all three worse.
	BodyBPS int
	// XPBPS scales experience earned: a low mood learns slower.
	XPBPS int
	// Pressing lists the needs that are over the High mark, for the screen.
	Pressing []string
}

// Need names, as screens and content name them.
const (
	NeedHunger = "hunger"
	NeedSleep  = "sleep"
	NeedStress = "stress"
)

// Effects works out what needs and mood do.
func (c Condition) Effects(n Needs, happiness int) Effects {
	penalty := 0
	var pressing []string
	for _, x := range []struct {
		name string
		v    int64
	}{{NeedHunger, n.Hunger}, {NeedSleep, n.Sleep}, {NeedStress, n.Stress}} {
		p := Points(x.v)
		switch {
		case p >= c.Severe:
			penalty += c.SevereBPS
			pressing = append(pressing, x.name)
		case p >= c.High:
			penalty += c.HighBPS
			pressing = append(pressing, x.name)
		}
	}
	e := Effects{BodyBPS: bpsWhole - min(penalty, c.MaxBPS), XPBPS: bpsWhole, Pressing: pressing}
	if happiness < c.LowMood {
		e.XPBPS = bpsWhole - c.LowMoodXPBPS
	}
	return e
}

// Scale applies a multiplier to an amount, rounding down, never below zero
// for a non-negative amount. bps outside 0..10000 is held inside it.
func Scale(amount int64, bps int) int64 {
	if amount <= 0 {
		return amount
	}
	bps = min(max(bps, 0), bpsWhole)
	if amount > (1<<62)/bpsWhole {
		return amount / bpsWhole * int64(bps)
	}
	return amount * int64(bps) / bpsWhole
}

// Mood is what a life pushes happiness toward, and how fast.
type Mood struct {
	// Base is the happiness of a character with nothing going for them.
	Base int
	// Home is added for a home to live in; Friend for each friend, up to
	// FriendCap; Faction for belonging to one; Achievement for each earned,
	// up to AchievementCap.
	Home, Friend, FriendCap, Faction, Achievement, AchievementCap int
	// PressingNeed is taken off for each need over the condition's High mark.
	PressingNeed int
	// PerDay is how many points a GAME day happiness moves toward the
	// target.
	PerDay int64
}

// Validate checks the mood rules.
func (m Mood) Validate() error {
	for _, v := range []int{m.Base, m.Home, m.Friend, m.FriendCap, m.Faction, m.Achievement, m.AchievementCap, m.PressingNeed} {
		if v < 0 || v > MaxPoints {
			return fmt.Errorf("%w: a mood value %d is outside 0..%d", ErrInvalid, v, MaxPoints)
		}
	}
	if m.PerDay < 0 || m.PerDay > 10*MaxPoints*24 {
		return fmt.Errorf("%w: mood drift %d a game day", ErrInvalid, m.PerDay)
	}
	return nil
}

// Life is what a character has going for them, for the mood.
type Life struct {
	Home         bool
	Friends      int
	Faction      bool
	Achievements int
}

// Target is the happiness a life pushes toward, 0..100.
func (m Mood) Target(l Life, pressing int) int {
	t := m.Base + min(l.Friends*m.Friend, m.FriendCap) + min(l.Achievements*m.Achievement, m.AchievementCap)
	if l.Home {
		t += m.Home
	}
	if l.Faction {
		t += m.Faction
	}
	t -= pressing * m.PressingNeed
	return min(max(t, 0), MaxPoints)
}

// Drift moves happiness toward target over the real span from..to on the
// game clock, one whole point at a time. It returns the new happiness and
// the instant it now describes: from moved on only by the time the points
// used, so what is left of a point is not lost by looking often.
func (m Mood) Drift(happiness, target int, from, to time.Time, scale gametime.Scale) (int, time.Time) {
	if from.IsZero() || !to.After(from) {
		if from.IsZero() {
			return happiness, to
		}
		return happiness, from
	}
	gap := target - happiness
	if gap == 0 || m.PerDay <= 0 {
		return happiness, to
	}
	elapsed := GameElapsed(from, to, scale)
	steps := int64(float64(elapsed) / float64(gameDay) * float64(m.PerDay))
	if steps <= 0 {
		return happiness, from
	}
	dist := int64(gap)
	if dist < 0 {
		dist = -dist
	}
	if steps >= dist {
		return target, to
	}
	moved := happiness + int(steps)
	if gap < 0 {
		moved = happiness - int(steps)
	}
	// The real time those steps took.
	s := int64(max(scale, 1))
	used := time.Duration(float64(gameDay) * float64(steps) / float64(m.PerDay) / float64(s))
	return moved, from.Add(used)
}

// Aging is how a character ages: from StartAge, one year every
// GameDaysPerYear days of GAME time, through the stages of a life.
type Aging struct {
	StartAge        int
	GameDaysPerYear int
	// Stages are the ages a stage of life begins at, ascending; a
	// character is in the last one whose From they have reached.
	Stages []Stage
}

// Stage is one stage of a life: school, university, working life,
// retirement.
type Stage struct {
	Code string
	From int
}

// Validate checks the aging rules.
func (a Aging) Validate() error {
	if a.StartAge < 1 || a.StartAge > 120 || a.GameDaysPerYear < 1 || a.GameDaysPerYear > 100_000 {
		return fmt.Errorf("%w: start age %d, %d game days a year", ErrInvalid, a.StartAge, a.GameDaysPerYear)
	}
	last := -1
	for _, s := range a.Stages {
		if s.Code == "" || s.From <= last || s.From > 150 {
			return fmt.Errorf("%w: stage %q from %d", ErrInvalid, s.Code, s.From)
		}
		last = s.From
	}
	return nil
}

// Age is the character's age at now, born (joined the game) at born.
func (a Aging) Age(born, now time.Time, scale gametime.Scale) int {
	if a.GameDaysPerYear < 1 {
		return a.StartAge
	}
	years := GameElapsed(born, now, scale) / (time.Duration(a.GameDaysPerYear) * gameDay)
	return a.StartAge + int(years)
}

// StageOf is the stage of life at an age, "" before the first.
func (a Aging) StageOf(age int) string {
	out := ""
	for _, s := range a.Stages {
		if age >= s.From {
			out = s.Code
		}
	}
	return out
}

// Mind is intelligence: what raises it and what it speeds up.
type Mind struct {
	// Max is the top of intelligence.
	Max int
	// PerCourse is gained for a course finished; PerCertificate more when
	// it gave a certificate.
	PerCourse, PerCertificate int
	// CourseBPSPerPoint shortens a course per point, up to CourseMaxBPS;
	// SkillBPSPerPoint adds skill experience per point, up to SkillMaxBPS.
	CourseBPSPerPoint, CourseMaxBPS int
	SkillBPSPerPoint, SkillMaxBPS   int
}

// Validate checks the intelligence rules.
func (m Mind) Validate() error {
	switch {
	case m.Max < 1 || m.Max > 1000 || m.PerCourse < 0 || m.PerCertificate < 0 || m.PerCourse+m.PerCertificate > m.Max:
		return fmt.Errorf("%w: intelligence max %d, per course %d, per certificate %d", ErrInvalid, m.Max, m.PerCourse,
			m.PerCertificate)
	case m.CourseBPSPerPoint < 0 || m.CourseMaxBPS < 0 || m.CourseMaxBPS > 5000:
		return fmt.Errorf("%w: course speed %d a point, max %d (at most 5000)", ErrInvalid, m.CourseBPSPerPoint, m.CourseMaxBPS)
	case m.SkillBPSPerPoint < 0 || m.SkillMaxBPS < 0 || m.SkillMaxBPS > 10000:
		return fmt.Errorf("%w: skill bonus %d a point, max %d", ErrInvalid, m.SkillBPSPerPoint, m.SkillMaxBPS)
	}
	return nil
}

// Learn is intelligence after a course finished.
func (m Mind) Learn(iq int, certified bool) int {
	iq += m.PerCourse
	if certified {
		iq += m.PerCertificate
	}
	return min(max(iq, 0), m.Max)
}

// CourseBPS is how much of a course's time a mind of iq spends: 10000 less
// its speed-up.
func (m Mind) CourseBPS(iq int) int {
	return bpsWhole - min(max(iq, 0)*m.CourseBPSPerPoint, m.CourseMaxBPS)
}

// CourseTime is a course's game time shortened by intelligence, never under
// a second.
func (m Mind) CourseTime(d time.Duration, iq int) time.Duration {
	if d <= 0 {
		return d
	}
	out := time.Duration(int64(d) / bpsWhole * int64(m.CourseBPS(iq)))
	return max(out, time.Second)
}

// SkillBPS is the skill experience multiplier of a mind of iq, 10000 and up.
func (m Mind) SkillBPS(iq int) int {
	return bpsWhole + min(max(iq, 0)*m.SkillBPSPerPoint, m.SkillMaxBPS)
}

// SkillXP is skill experience raised by intelligence.
func (m Mind) SkillXP(xp int64, iq int) int64 {
	if xp <= 0 {
		return xp
	}
	return xp + xp*int64(m.SkillBPS(iq)-bpsWhole)/bpsWhole
}

// Worth is what a player is worth, part by part, in minor units.
type Worth struct {
	// Cash, Bank and Escrow are their money: carried, banked and held.
	Cash, Bank, Escrow int64
	// Equity is their share of their companies' books.
	Equity int64
	// Property is what their property would fetch from its city now.
	Property int64
	// Goods is what they hold, at reference prices.
	Goods int64
	// Savings is their savings account; Gold their gold at what the dealer
	// pays for it now (docs/adr/0026).
	Savings, Gold int64
	// Debts is what they owe on their property; Loans what they owe the
	// bank on loans of their own (a company's loans lower its equity).
	Debts, Loans int64
}

// Total is net worth: everything held less every debt. It may be negative.
func (w Worth) Total() int64 {
	return w.Cash + w.Bank + w.Escrow + w.Equity + w.Property + w.Goods + w.Savings + w.Gold - w.Debts - w.Loans
}

// Rank is one rung of the ladder of wealth.
type Rank struct {
	Code string
	// Min is the net worth the rank begins at, minor units.
	Min int64
}

// Ladder is the ranks a player climbs and falls down by what they are
// worth, lowest first, and the band that keeps a rank from flickering: a
// player keeps their rank until they are worth HysteresisBPS less than its
// Min.
type Ladder struct {
	Ranks         []Rank
	HysteresisBPS int64
}

// Validate checks the ladder.
func (l Ladder) Validate() error {
	if len(l.Ranks) == 0 {
		return fmt.Errorf("%w: no ranks", ErrInvalid)
	}
	if l.HysteresisBPS < 0 || l.HysteresisBPS > 5000 {
		return fmt.Errorf("%w: hysteresis %d bps outside 0..5000", ErrInvalid, l.HysteresisBPS)
	}
	seen := map[string]bool{}
	for i, r := range l.Ranks {
		if r.Code == "" || seen[r.Code] {
			return fmt.Errorf("%w: rank %d has no code or repeats one", ErrInvalid, i)
		}
		seen[r.Code] = true
		if i == 0 && r.Min > 0 {
			return fmt.Errorf("%w: the first rank %q must begin at or below zero", ErrInvalid, r.Code)
		}
		if i > 0 && r.Min <= l.Ranks[i-1].Min {
			return fmt.Errorf("%w: rank %q does not begin above %q", ErrInvalid, r.Code, l.Ranks[i-1].Code)
		}
	}
	return nil
}

// index is where a code stands on the ladder, -1 for none.
func (l Ladder) index(code string) int {
	for i, r := range l.Ranks {
		if r.Code == code {
			return i
		}
	}
	return -1
}

// Straight is the rank a net worth reaches with no history: the highest
// whose Min it has.
func (l Ladder) Straight(worth int64) int {
	at := 0
	for i, r := range l.Ranks {
		if worth >= r.Min {
			at = i
		}
	}
	return at
}

// Next is the rank a player holding current, now worth worth, holds: up to
// the highest rank they reach, or down — only once they are worth less than
// their rank's Min by the hysteresis band — to the rank their worth reaches.
// A current rank the ladder no longer has is replaced by the straight one.
// It reports the new code, and +1 for a rise, −1 for a fall, 0 for none.
func (l Ladder) Next(current string, worth int64) (string, int) {
	if len(l.Ranks) == 0 {
		return current, 0
	}
	straight := l.Straight(worth)
	at := l.index(current)
	if at < 0 {
		return l.Ranks[straight].Code, 0
	}
	switch {
	case straight > at:
		return l.Ranks[straight].Code, 1
	case straight < at:
		if worth >= l.Keeps(at) {
			return current, 0
		}
		return l.Ranks[straight].Code, -1
	}
	return current, 0
}

// Keeps is the least a player holding the rank at i may be worth and keep
// it: its Min less the hysteresis band.
func (l Ladder) Keeps(i int) int64 {
	floor := l.Ranks[i].Min
	if floor <= 0 {
		return floor
	}
	return floor - floor/bpsWhole*l.HysteresisBPS - floor%bpsWhole*l.HysteresisBPS/bpsWhole
}

// Bio is the rules of a player's bio: its length, whether a link may be in
// it, and words it may not contain.
type Bio struct {
	MinRunes, MaxRunes int
	AllowLinks         bool
	Blocked            []string
}

// Validate checks the bio rules.
func (b Bio) Validate() error {
	if b.MinRunes < 0 || b.MaxRunes < max(b.MinRunes, 1) || b.MaxRunes > 500 {
		return fmt.Errorf("%w: bio length %d..%d", ErrInvalid, b.MinRunes, b.MaxRunes)
	}
	for _, w := range b.Blocked {
		if strings.TrimSpace(w) == "" {
			return fmt.Errorf("%w: an empty blocked word", ErrInvalid)
		}
	}
	return nil
}

// Refusals of a bio.
var (
	ErrBioLength  = errors.New("life: bio length")
	ErrBioChars   = errors.New("life: bio characters")
	ErrBioLink    = errors.New("life: bio link")
	ErrBioBlocked = errors.New("life: bio blocked word")
)

// linkMarks are what make text a link or a handle.
var linkMarks = []string{"http:", "https:", "www.", "t.me", "telegram.me", "tg:", "://", ".com", ".ir", ".org", ".net",
	".io", ".me/"}

// Clean checks a bio and returns it as it is stored: one line, spaces
// collapsed, trimmed. An empty bio clears it and is always allowed.
func (b Bio) Clean(text string) (string, error) {
	var sb strings.Builder
	space := false
	for _, r := range strings.TrimSpace(text) {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || unicode.IsSpace(r):
			if !space {
				sb.WriteRune(' ')
			}
			space = true
			continue
		case r == '\u200c' || r == '\u200d':
			// The Persian non-joiner is part of a word's spelling.
		case unicode.IsControl(r) || r == '\u200e' || r == '\u200f' || r == '\u202a' || r == '\u202b' ||
			r == '\u202c' || r == '\u202d' || r == '\u202e' || r == '\ufeff':
			return "", ErrBioChars
		case r == '<' || r == '>' || r == '`':
			return "", ErrBioChars
		}
		space = false
		sb.WriteRune(r)
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", nil
	}
	if n := utf8.RuneCountInString(out); n < b.MinRunes || n > b.MaxRunes {
		return "", ErrBioLength
	}
	low := strings.ToLower(out)
	if !b.AllowLinks {
		if strings.Contains(low, "@") {
			return "", ErrBioLink
		}
		for _, m := range linkMarks {
			if strings.Contains(low, m) {
				return "", ErrBioLink
			}
		}
	}
	squashed := strings.NewReplacer(" ", "", "\u200c", "", ".", "", "_", "", "-", "").Replace(low)
	for _, w := range b.Blocked {
		w = strings.ToLower(strings.TrimSpace(w))
		if strings.Contains(low, w) || strings.Contains(squashed, strings.ReplaceAll(w, " ", "")) {
			return "", ErrBioBlocked
		}
	}
	return out, nil
}

// CityScore is how the best cities are ranked: points per resident and per
// company, a share of the treasury, all cut by the city's war damage.
type CityScore struct {
	PerResident, PerCompany int64
	TreasuryBPS             int64
}

// Validate checks the city score.
func (s CityScore) Validate() error {
	if s.PerResident < 0 || s.PerCompany < 0 || s.TreasuryBPS < 0 || s.TreasuryBPS > bpsWhole ||
		s.PerResident+s.PerCompany+s.TreasuryBPS == 0 {
		return fmt.Errorf("%w: city score %+v", ErrInvalid, s)
	}
	return nil
}

// Score is one city's score.
func (s CityScore) Score(residents, companies, treasury, damageBPS int64) int64 {
	v := residents*s.PerResident + companies*s.PerCompany + max(treasury, 0)/bpsWhole*s.TreasuryBPS
	damageBPS = min(max(damageBPS, 0), bpsWhole)
	return v/bpsWhole*(bpsWhole-damageBPS) + v%bpsWhole*(bpsWhole-damageBPS)/bpsWhole
}

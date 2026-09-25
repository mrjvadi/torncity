package content

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/life"
)

// This file holds a character's life (configs/content/life.yml;
// docs/adr/0025-life-and-legacy.md): how the needs drift and what they cost,
// the mood, what the game's own events do to a life, where a player may
// sleep, age, intelligence, the ladder of ranks by net worth, the bio's
// rules, the avatars, the life history and the leaderboards. The rules are
// internal/domain/life.

// ErrInvalidLifeContent means life.yml is unusable.
var ErrInvalidLifeContent = errors.New("content: invalid life content")

// LifeEvent kinds: what of the game's own events touches a life. The set is
// closed because code reads each one; content says only by how much.
const (
	LifeShiftWorked        = "shift_worked"
	LifeCrimeAttempted     = "crime_attempted"
	LifeJailed             = "jailed"
	LifeHospitalised       = "hospitalised"
	LifeWarStruck          = "war_struck"
	LifeAchievementAwarded = "achievement_awarded"
	LifePromoted           = "promoted"
	LifeCourseCompleted    = "course_completed"
	LifePropertyBought     = "property_bought"
	LifeElectionWon        = "election_won"
)

// LifeEventKinds lists them.
var LifeEventKinds = []string{LifeShiftWorked, LifeCrimeAttempted, LifeJailed, LifeHospitalised, LifeWarStruck,
	LifeAchievementAwarded, LifePromoted, LifeCourseCompleted, LifePropertyBought, LifeElectionWon}

// NeedsDef is how the needs drift and what they cost.
type NeedsDef struct {
	// Start is where a new life begins, points.
	Start NeedPoints `yaml:"start" json:"start"`
	// Rates in points per GAME day.
	HungerPerGameDay          int64 `yaml:"hunger_per_game_day" json:"hunger_per_game_day"`
	SleepPerGameDay           int64 `yaml:"sleep_per_game_day" json:"sleep_per_game_day"`
	StressFallPerGameDay      int64 `yaml:"stress_fall_per_game_day" json:"stress_fall_per_game_day"`
	HappyStressFallPerGameDay int64 `yaml:"happy_stress_fall_per_game_day" json:"happy_stress_fall_per_game_day"`
	// High and Severe are the points a need starts to cost at; HighBPS,
	// SevereBPS and MaxBPS what it costs (life.Condition).
	High      int `yaml:"high" json:"high"`
	Severe    int `yaml:"severe" json:"severe"`
	HighBPS   int `yaml:"high_bps" json:"high_bps"`
	SevereBPS int `yaml:"severe_bps" json:"severe_bps"`
	MaxBPS    int `yaml:"max_bps" json:"max_bps"`
}

// NeedPoints are the three needs, whole points.
type NeedPoints struct {
	Hunger int `yaml:"hunger" json:"hunger"`
	Sleep  int `yaml:"sleep" json:"sleep"`
	Stress int `yaml:"stress" json:"stress"`
}

// MoodDef is what a life pushes happiness toward (life.Mood), and the low
// mood that slows experience.
type MoodDef struct {
	Base           int   `yaml:"base" json:"base"`
	Home           int   `yaml:"home" json:"home"`
	Friend         int   `yaml:"friend" json:"friend"`
	FriendCap      int   `yaml:"friend_cap" json:"friend_cap"`
	Faction        int   `yaml:"faction" json:"faction"`
	Achievement    int   `yaml:"achievement" json:"achievement"`
	AchievementCap int   `yaml:"achievement_cap" json:"achievement_cap"`
	PressingNeed   int   `yaml:"pressing_need" json:"pressing_need"`
	PerGameDay     int64 `yaml:"per_game_day" json:"per_game_day"`
	Low            int   `yaml:"low" json:"low"`
	LowXPBPS       int   `yaml:"low_xp_bps" json:"low_xp_bps"`
}

// LifeEventDef is what one kind of event does to a life: points added to
// stress and to happiness (negative takes away).
type LifeEventDef struct {
	Event     string `yaml:"event" json:"event"`
	Stress    int    `yaml:"stress,omitempty" json:"stress,omitempty"`
	Happiness int    `yaml:"happiness,omitempty" json:"happiness,omitempty"`
}

// SleepDef is where a player sleeps: at home (property.rest), and the spots
// of a city anyone may sleep at.
type SleepDef struct {
	Home  SleepEffect    `yaml:"home" json:"home"`
	Spots []SleepSpotDef `yaml:"spots" json:"spots"`
	// Cooldown is the GAME time before the player can sleep at a spot
	// again.
	Cooldown string `yaml:"cooldown" json:"cooldown"`
}

// CooldownDuration is the cooldown between two nights at a spot, zero when
// unreadable.
func (s SleepDef) CooldownDuration() time.Duration {
	d, _ := time.ParseDuration(s.Cooldown)
	return d
}

// SleepEffect is what a night's sleep does: points of the need of sleep
// taken away (Rest), and of stress (Relief; negative adds).
type SleepEffect struct {
	Rest   int `yaml:"rest" json:"rest"`
	Relief int `yaml:"relief" json:"relief"`
}

// SleepSpotDef is a place anyone may sleep at: a hostel bed, a park bench.
type SleepSpotDef struct {
	Code string `yaml:"code" json:"code"`
	Name string `yaml:"name" json:"name"`
	// Place is where in a city it is (places.yml).
	Place string `yaml:"place" json:"place"`
	// Price is what a night costs, minor units, paid to the city; zero is
	// free.
	Price  int64 `yaml:"price,omitempty" json:"price,omitempty"`
	Rest   int   `yaml:"rest" json:"rest"`
	Relief int   `yaml:"relief" json:"relief"`
}

// AgeDef is how a character ages.
type AgeDef struct {
	Start           int        `yaml:"start" json:"start"`
	GameDaysPerYear int        `yaml:"game_days_per_year" json:"game_days_per_year"`
	Stages          []StageDef `yaml:"stages" json:"stages"`
}

// StageDef is one stage of life.
type StageDef struct {
	Code string `yaml:"code" json:"code"`
	Name string `yaml:"name" json:"name"`
	From int    `yaml:"from" json:"from"`
}

// IntelligenceDef is intelligence (life.Mind) and where it starts.
type IntelligenceDef struct {
	Start             int `yaml:"start" json:"start"`
	Max               int `yaml:"max" json:"max"`
	PerCourse         int `yaml:"per_course" json:"per_course"`
	PerCertificate    int `yaml:"per_certificate" json:"per_certificate"`
	CourseBPSPerPoint int `yaml:"course_bps_per_point" json:"course_bps_per_point"`
	CourseMaxBPS      int `yaml:"course_max_bps" json:"course_max_bps"`
	SkillBPSPerPoint  int `yaml:"skill_bps_per_point" json:"skill_bps_per_point"`
	SkillMaxBPS       int `yaml:"skill_max_bps" json:"skill_max_bps"`
}

// RanksDef is the ladder of ranks by net worth.
type RanksDef struct {
	HysteresisBPS int64     `yaml:"hysteresis_bps" json:"hysteresis_bps"`
	Ladder        []RankDef `yaml:"ladder" json:"ladder"`
}

// RankDef is one rank.
type RankDef struct {
	Code  string `yaml:"code" json:"code"`
	Name  string `yaml:"name" json:"name"`
	Emoji string `yaml:"emoji" json:"emoji"`
	Min   int64  `yaml:"min" json:"min"`
}

// BioDef is the bio's rules.
type BioDef struct {
	Min        int      `yaml:"min" json:"min"`
	Max        int      `yaml:"max" json:"max"`
	AllowLinks bool     `yaml:"allow_links,omitempty" json:"allow_links,omitempty"`
	Blocked    []string `yaml:"blocked,omitempty" json:"blocked,omitempty"`
}

// AvatarDef is one avatar a player may choose.
type AvatarDef struct {
	Code  string `yaml:"code" json:"code"`
	Name  string `yaml:"name" json:"name"`
	Emoji string `yaml:"emoji" json:"emoji"`
}

// HistoryDef tunes the life history.
type HistoryDef struct {
	PageSize int `yaml:"page_size" json:"page_size"`
	// BigTrade is the value from which a trade enters the history, minor
	// units.
	BigTrade int64 `yaml:"big_trade" json:"big_trade"`
}

// LeaderboardsDef tunes the leaderboards.
type LeaderboardsDef struct {
	// Period is the GAME time between two refreshes.
	Period string `yaml:"period" json:"period"`
	Size   int    `yaml:"size" json:"size"`
	// Keep is how many periods of boards are kept.
	Keep      int          `yaml:"keep" json:"keep"`
	CityScore CityScoreDef `yaml:"city_score" json:"city_score"`
}

// PeriodDuration is the refresh period, zero when unreadable.
func (l LeaderboardsDef) PeriodDuration() time.Duration {
	d, _ := time.ParseDuration(l.Period)
	return d
}

// CityScoreDef is how cities are scored (life.CityScore).
type CityScoreDef struct {
	PerResident int64 `yaml:"per_resident" json:"per_resident"`
	PerCompany  int64 `yaml:"per_company" json:"per_company"`
	TreasuryBPS int64 `yaml:"treasury_bps" json:"treasury_bps"`
}

// LifeDef is life.yml's life section.
type LifeDef struct {
	Needs        NeedsDef        `yaml:"needs" json:"needs"`
	Mood         MoodDef         `yaml:"mood" json:"mood"`
	Events       []LifeEventDef  `yaml:"events" json:"events"`
	Sleep        SleepDef        `yaml:"sleep" json:"sleep"`
	Age          AgeDef          `yaml:"age" json:"age"`
	Intelligence IntelligenceDef `yaml:"intelligence" json:"intelligence"`
	Ranks        RanksDef        `yaml:"ranks" json:"ranks"`
	Bio          BioDef          `yaml:"bio" json:"bio"`
	Avatars      []AvatarDef     `yaml:"avatars" json:"avatars"`
	// PhotoTTL is how long (real time) a Telegram profile photo's file id,
	// kept per bot, is trusted before it is fetched again.
	PhotoTTL     string          `yaml:"photo_ttl" json:"photo_ttl"`
	History      HistoryDef      `yaml:"history" json:"history"`
	Leaderboards LeaderboardsDef `yaml:"leaderboards" json:"leaderboards"`
}

// Drift is the domain's drift.
func (d LifeDef) Drift() life.Drift {
	return life.Drift{HungerPerDay: d.Needs.HungerPerGameDay, SleepPerDay: d.Needs.SleepPerGameDay,
		StressFallPerDay: d.Needs.StressFallPerGameDay, HappyStressFallPerDay: d.Needs.HappyStressFallPerGameDay}
}

// Condition is the domain's condition.
func (d LifeDef) Condition() life.Condition {
	return life.Condition{High: d.Needs.High, Severe: d.Needs.Severe, HighBPS: d.Needs.HighBPS,
		SevereBPS: d.Needs.SevereBPS, MaxBPS: d.Needs.MaxBPS, LowMood: d.Mood.Low, LowMoodXPBPS: d.Mood.LowXPBPS}
}

// MoodRules is the domain's mood.
func (d LifeDef) MoodRules() life.Mood {
	m := d.Mood
	return life.Mood{Base: m.Base, Home: m.Home, Friend: m.Friend, FriendCap: m.FriendCap, Faction: m.Faction,
		Achievement: m.Achievement, AchievementCap: m.AchievementCap, PressingNeed: m.PressingNeed, PerDay: m.PerGameDay}
}

// Aging is the domain's aging.
func (d LifeDef) Aging() life.Aging {
	a := life.Aging{StartAge: d.Age.Start, GameDaysPerYear: d.Age.GameDaysPerYear}
	for _, s := range d.Age.Stages {
		a.Stages = append(a.Stages, life.Stage{Code: s.Code, From: s.From})
	}
	return a
}

// Mind is the domain's intelligence.
func (d LifeDef) Mind() life.Mind {
	i := d.Intelligence
	return life.Mind{Max: i.Max, PerCourse: i.PerCourse, PerCertificate: i.PerCertificate,
		CourseBPSPerPoint: i.CourseBPSPerPoint, CourseMaxBPS: i.CourseMaxBPS, SkillBPSPerPoint: i.SkillBPSPerPoint,
		SkillMaxBPS: i.SkillMaxBPS}
}

// Ladder is the domain's ladder.
func (d LifeDef) Ladder() life.Ladder {
	l := life.Ladder{HysteresisBPS: d.Ranks.HysteresisBPS}
	for _, r := range d.Ranks.Ladder {
		l.Ranks = append(l.Ranks, life.Rank{Code: r.Code, Min: r.Min})
	}
	return l
}

// BioRules is the domain's bio rules.
func (d LifeDef) BioRules() life.Bio {
	return life.Bio{MinRunes: d.Bio.Min, MaxRunes: d.Bio.Max, AllowLinks: d.Bio.AllowLinks, Blocked: d.Bio.Blocked}
}

// CityScore is the domain's city score.
func (d LifeDef) CityScore() life.CityScore {
	s := d.Leaderboards.CityScore
	return life.CityScore{PerResident: s.PerResident, PerCompany: s.PerCompany, TreasuryBPS: s.TreasuryBPS}
}

// Event is what one kind of event does, zero for nothing.
func (d LifeDef) Event(kind string) LifeEventDef {
	for _, e := range d.Events {
		if e.Event == kind {
			return e
		}
	}
	return LifeEventDef{Event: kind}
}

// Rank finds a rank by code.
func (d LifeDef) Rank(code string) (RankDef, bool) {
	for _, r := range d.Ranks.Ladder {
		if r.Code == code {
			return r, true
		}
	}
	return RankDef{}, false
}

// Avatar finds an avatar by code.
func (d LifeDef) Avatar(code string) (AvatarDef, bool) {
	for _, a := range d.Avatars {
		if a.Code == code {
			return a, true
		}
	}
	return AvatarDef{}, false
}

// Spot finds a sleeping spot by code.
func (d LifeDef) Spot(code string) (SleepSpotDef, bool) {
	for _, s := range d.Sleep.Spots {
		if s.Code == code {
			return s, true
		}
	}
	return SleepSpotDef{}, false
}

// Stage finds a stage of life by code.
func (d LifeDef) Stage(code string) (StageDef, bool) {
	for _, s := range d.Age.Stages {
		if s.Code == code {
			return s, true
		}
	}
	return StageDef{}, false
}

// PhotoTTLDuration is how long a cached photo is trusted.
func (d LifeDef) PhotoTTLDuration() time.Duration {
	v, _ := time.ParseDuration(d.PhotoTTL)
	return v
}

// Life returns the life section, and whether the content has one.
func (s *Snapshot) Life() (LifeDef, bool) {
	if s.life == nil {
		return LifeDef{}, false
	}
	return *s.life, true
}

// validateLife checks life.yml.
func (p *Pack) validateLife(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidLifeContent, fmt.Sprintf(format, args...)))
	}
	if len(p.Life) == 0 {
		return
	}
	if len(p.Life) > 1 {
		bad("life is declared %d times", len(p.Life))
		return
	}
	d := p.Life[0]
	for _, v := range []int{d.Needs.Start.Hunger, d.Needs.Start.Sleep, d.Needs.Start.Stress} {
		if v < 0 || v > life.MaxPoints {
			bad("needs.start %d is outside 0..%d", v, life.MaxPoints)
		}
	}
	for _, err := range []error{d.Drift().Validate(), d.Condition().Validate(), d.MoodRules().Validate(),
		d.Aging().Validate(), d.Mind().Validate(), d.Ladder().Validate(), d.BioRules().Validate(),
		d.CityScore().Validate()} {
		if err != nil {
			bad("%v", err)
		}
	}
	known := map[string]bool{}
	for _, k := range LifeEventKinds {
		known[k] = true
	}
	seen := map[string]bool{}
	for i, e := range d.Events {
		if !known[e.Event] || seen[e.Event] {
			bad("events[%d] %q is not a known kind or repeats one", i, e.Event)
		}
		seen[e.Event] = true
		if e.Stress < -life.MaxPoints || e.Stress > life.MaxPoints || e.Happiness < -life.MaxPoints ||
			e.Happiness > life.MaxPoints {
			bad("events[%d] %q moves a value by more than %d", i, e.Event, life.MaxPoints)
		}
	}
	checkSleep := func(where string, e SleepEffect) {
		if e.Rest < 0 || e.Rest > life.MaxPoints || e.Relief < -life.MaxPoints || e.Relief > life.MaxPoints {
			bad("%s rest %d, relief %d", where, e.Rest, e.Relief)
		}
	}
	checkSleep("sleep.home", d.Sleep.Home)
	places := map[string]bool{}
	for _, v := range p.Venues {
		places[v.Code] = true
	}
	spots := map[string]bool{}
	for i, s := range d.Sleep.Spots {
		where := fmt.Sprintf("sleep.spots[%d] %q", i, s.Code)
		if !transportCodePattern.MatchString(s.Code) || spots[s.Code] {
			bad("%s: code is not a code or repeated", where)
		}
		spots[s.Code] = true
		if s.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: life %s", ErrMissingDisplayName, where))
		}
		if !places[s.Place] {
			bad("%s: place %q is not in places.yml", where, s.Place)
		}
		if s.Price < 0 || s.Price > 10_000_000 {
			bad("%s: price %d", where, s.Price)
		}
		if s.Rest < 1 {
			bad("%s: a spot that rests nobody", where)
		}
		checkSleep(where, SleepEffect{Rest: s.Rest, Relief: s.Relief})
	}
	if cd, err := time.ParseDuration(d.Sleep.Cooldown); err != nil || cd < time.Minute || cd > 30*24*time.Hour {
		bad("sleep.cooldown %q is not a game duration of 1m..720h", d.Sleep.Cooldown)
	}
	codes := map[string]bool{}
	for i, s := range d.Age.Stages {
		if !transportCodePattern.MatchString(s.Code) || codes[s.Code] {
			bad("age.stages[%d] %q: code is not a code or repeated", i, s.Code)
		}
		codes[s.Code] = true
		if s.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: life age.stages[%d] %q", ErrMissingDisplayName, i, s.Code))
		}
	}
	if d.Intelligence.Start < 0 || d.Intelligence.Start > d.Intelligence.Max {
		bad("intelligence.start %d is outside 0..%d", d.Intelligence.Start, d.Intelligence.Max)
	}
	for i, r := range d.Ranks.Ladder {
		if !transportCodePattern.MatchString(r.Code) {
			bad("ranks.ladder[%d] %q is not a code", i, r.Code)
		}
		if r.Name == "" || r.Emoji == "" {
			*problems = append(*problems, fmt.Errorf("%w: life ranks.ladder[%d] %q needs a name and an emoji",
				ErrMissingDisplayName, i, r.Code))
		}
	}
	avatars := map[string]bool{}
	for i, a := range d.Avatars {
		if !transportCodePattern.MatchString(a.Code) || avatars[a.Code] || a.Code == AvatarPhoto || a.Code == AvatarNone {
			bad("avatars[%d] %q: code is not a code, repeated or reserved", i, a.Code)
		}
		avatars[a.Code] = true
		if a.Name == "" || a.Emoji == "" {
			*problems = append(*problems, fmt.Errorf("%w: life avatars[%d] %q needs a name and an emoji",
				ErrMissingDisplayName, i, a.Code))
		}
	}
	if len(d.Avatars) == 0 {
		bad("no avatars to choose from")
	}
	if ttl, err := time.ParseDuration(d.PhotoTTL); err != nil || ttl < time.Minute || ttl > 90*24*time.Hour {
		bad("photo_ttl %q is not a duration of 1m..2160h", d.PhotoTTL)
	}
	if d.History.PageSize < 3 || d.History.PageSize > 20 || d.History.BigTrade < 1 {
		bad("history page_size %d (3..20), big_trade %d", d.History.PageSize, d.History.BigTrade)
	}
	lb := d.Leaderboards
	if per, err := time.ParseDuration(lb.Period); err != nil || per < time.Hour || per > 30*24*time.Hour {
		bad("leaderboards.period %q is not a game duration of 1h..720h", lb.Period)
	}
	if lb.Size < 3 || lb.Size > 25 || lb.Keep < 1 || lb.Keep > 100 {
		bad("leaderboards size %d (3..25), keep %d (1..100)", lb.Size, lb.Keep)
	}
}

// Reserved avatar choices: the player's Telegram profile photo, and none.
const (
	AvatarPhoto = "photo"
	AvatarNone  = "none"
)

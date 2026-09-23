package screens

import (
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Crime screens: the crime hub, a category's crimes, one crime in detail,
// an attempt's outcome, a timed crime under way, the criminal record, jail
// and bail, and the victim's side — the notice, the report and the case.
//
// # Who sees what
//
// The game is played in groups (docs/adr/0019-crime-engine.md, section on
// visibility). An attempt and its outcome are ordinary group messages: the
// group sees "X tried to pick a pocket at the train station and was caught".
// What stays out of the group is money and identity: a shared screen
// (Context.Shared) never shows the sum taken, a fine or a bail, and nothing
// ever names a player victim to the group. The thief learns the exact take
// privately, the victim learns of the theft by a private notice, and the
// report and case screens are private screens (presenter.Response.Private).
//
// A crime, a category, a tier and a venue are content keyed on a code. Their
// names are looked up in the catalogue — crime_name.<code>,
// crime_category.<code>, crime_tier.<code>, venue.<code> — and fall back to
// the name the content file was authored with, as a career's does.

// Callback addresses of the crime screens.
const (
	AddrCrimeHub    = "crime:hub"
	AddrCrimeList   = "crime:list"
	AddrCrimeView   = "crime:view"
	AddrCrimeCommit = "crime:commit"
	AddrCrimeRecord = "crime:record"
	AddrCrimeJail   = "crime:jail"
	AddrCrimeBail   = "crime:bail"
	AddrCrimeReport = "crime:report"
	AddrCrimeCases  = "crime:cases"
)

// ReportConfirmation is the argument that turns crime.report from "are you
// sure" into the report itself.
const ReportConfirmation = "yes"

// Named is a content entry for a screen: its code and its authored name.
type Named struct {
	Code string
	Name string
}

// CrimeName is a crime's display name in this context's language.
func (c Context) CrimeName(n Named) string { return c.named("crime_name."+n.Code, n.Name) }

// CrimeCategoryName is a crime category's display name.
func (c Context) CrimeCategoryName(n Named) string {
	return c.named("crime_category."+n.Code, n.Name)
}

// CrimeTierName is a criminal experience tier's display name.
func (c Context) CrimeTierName(n Named) string { return c.named("crime_tier."+n.Code, n.Name) }

// VenueName is a venue's display name.
func (c Context) VenueName(n Named) string { return c.named("venue."+n.Code, n.Name) }

// Requirement kinds of a crime, on top of the work ones.
const (
	ReqCrimeTier = "crime_tier"
	ReqVenue     = "venue"
	ReqFacility  = "facility"
)

// CrimeRequirement is one condition of a crime, met or not.
type CrimeRequirement struct {
	Requirement
	// Tier and HaveTier name criminal tiers, for crime_tier.
	Tier     Named
	HaveTier Named
	// Venues are where the crime can be committed and Here where the
	// player is, for venue.
	Venues []Named
	Here   Named
	// Facility is a facility code, for facility.
	Facility string
}

// crimeRequirementLine renders a crime requirement, falling back to the work
// requirement sentences for level, skill and certificate.
func (c Context) crimeRequirementLine(r CrimeRequirement) string {
	var text string
	switch r.Kind {
	case ReqCrimeTier:
		if r.Met {
			text = c.T("crime.requirement.tier", map[string]any{"tier": c.CrimeTierName(r.Tier)})
		} else {
			text = c.T("crime.requirement.tier_have", map[string]any{
				"tier": c.CrimeTierName(r.Tier), "have": c.CrimeTierName(r.HaveTier)})
		}
	case ReqVenue:
		names := make([]string, 0, len(r.Venues))
		for _, v := range r.Venues {
			names = append(names, c.VenueName(v))
		}
		list := strings.Join(names, c.T("crime.list_separator", nil))
		if r.Met {
			text = c.T("crime.requirement.venue", map[string]any{"venues": list})
		} else {
			text = c.T("crime.requirement.venue_here", map[string]any{"venues": list, "here": c.VenueName(r.Here)})
		}
	case ReqFacility:
		text = c.T("crime.requirement.facility", map[string]any{"facility": c.named("crime.facility."+r.Facility, r.Facility)})
	default:
		return c.requirementLine(r.Requirement)
	}
	if r.Met {
		return c.T("requirement.met", map[string]any{"text": text})
	}
	return c.T("requirement.unmet", map[string]any{"text": text})
}

func (c Context) crimeRequirementLines(rs []CrimeRequirement) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if line := c.crimeRequirementLine(r); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// CrimeProgress is a timed crime under way, or a sentence being served.
type CrimeProgress struct {
	Crime     Named
	Remaining time.Duration
	EndsAt    time.Time
}

// NerveView is the player's nerve.
type NerveView struct {
	Nerve, Max int
	// FullIn is how long until it is full, zero when it is.
	FullIn time.Duration
}

func (c Context) nerveLine(n NerveView) string {
	args := map[string]any{"nerve": FormatNumber(c, int64(n.Nerve)), "max": FormatNumber(c, int64(n.Max))}
	if n.FullIn > 0 && n.Nerve < n.Max {
		args["duration"] = FormatDuration(c, n.FullIn)
		return c.T("crime.nerve_refilling", args)
	}
	return c.T("crime.nerve", args)
}

// HeatView is the player's heat and the wanted level it shows as.
type HeatView struct {
	Heat, Max int
	Wanted    int
	Stars     int
}

func (c Context) heatLine(h HeatView) string {
	if h.Heat <= 0 {
		return c.T("crime.heat_none", nil)
	}
	return c.T("crime.heat", map[string]any{
		"heat": FormatNumber(c, int64(h.Heat)), "max": FormatNumber(c, int64(h.Max)),
		"wanted": FormatNumber(c, int64(h.Wanted)), "stars": FormatNumber(c, int64(h.Stars)),
	})
}

// TierView is the player's criminal experience.
type TierView struct {
	Tier Named
	XP   int64
	// Next is the next tier, zero at the top; NextXP where it starts.
	Next   Named
	NextXP int64
}

func (c Context) tierLine(t TierView) string {
	args := map[string]any{"tier": c.CrimeTierName(t.Tier), "xp": FormatNumber(c, t.XP)}
	if t.Next.Code == "" {
		return c.T("crime.tier", args)
	}
	args["next"], args["next_xp"] = c.CrimeTierName(t.Next), FormatNumber(c, t.NextXP)
	return c.T("crime.tier_next", args)
}

// CrimeHubView is the crime hub.
type CrimeHubView struct {
	CityCode, City string
	Venue          Named
	Nerve          NerveView
	Heat           HeatView
	Tier           TierView
	// Travelling means the player is on the road: nothing can be done.
	Travelling bool
	// Jail is the sentence being served, nil when free.
	Jail *CrimeProgress
	// Busy is the timed crime under way, nil when none.
	Busy       *CrimeProgress
	Categories []Named
}

// CrimeHub renders the hub: where the player is, their nerve, heat and
// criminal rank, what holds them back, and the categories of crime.
func CrimeHub(c Context, v CrimeHubView) *presenter.Response {
	var where string
	switch {
	case v.Travelling:
		where = c.T("crime.travelling", nil)
	case v.City != "":
		where = c.T("crime.where", map[string]any{"venue": c.VenueName(v.Venue), "city": c.CityName(v.CityCode, v.City)})
	default:
		where = c.T("crime.nowhere", nil)
	}
	facts := body(where, c.nerveLine(v.Nerve), c.heatLine(v.Heat), c.tierLine(v.Tier))

	var state string
	switch {
	case v.Jail != nil:
		state = body(c.T("crime.in_jail", map[string]any{"remaining": FormatDuration(c, v.Jail.Remaining)}),
			clockLine(c, "crime.free_at", v.Jail.EndsAt))
	case v.Busy != nil:
		state = body(c.T("crime.busy", map[string]any{
			"crime": c.CrimeName(v.Busy.Crime), "remaining": FormatDuration(c, v.Busy.Remaining)}),
			clockLine(c, "crime.back_at", v.Busy.EndsAt))
	}

	kb := keyboards.New()
	buttons := make([]presenter.Button, 0, len(v.Categories))
	for _, cat := range v.Categories {
		if btn, ok := keyboards.Button(c.CrimeCategoryName(cat), AddrCrimeList, cat.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	kb.Grid(2, buttons...)
	record, _ := keyboards.Button(c.T("crime.button.record", nil), AddrCrimeRecord)
	cases, _ := keyboards.Button(c.T("crime.button.cases", nil), AddrCrimeCases)
	kb.Row(record, cases)
	if v.Jail != nil {
		jail, _ := keyboards.Button(c.T("crime.button.jail", nil), AddrCrimeJail)
		kb.Row(jail)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrCrimeHub}))

	var choose string
	if len(v.Categories) > 0 && v.Jail == nil {
		choose = c.T("crime.choose_category", nil)
	}
	return c.respond(paragraphs(c.T("crime.hub_title", nil), facts, state, choose), kb.Build())
}

// CrimeLine is one crime in a category's list.
type CrimeLine struct {
	Crime Named
	Nerve int
	// Duration is the real wait of a timed crime, zero for an instant one.
	Duration time.Duration
	// Eligible is whether the player meets every requirement now.
	Eligible bool
}

// CrimeListView is one category's crimes, one page of them.
type CrimeListView struct {
	Category Named
	Crimes   []CrimeLine
	Page     int
	Pages    int
}

// CrimeList renders a category's crimes, each a button to its details.
func CrimeList(c Context, v CrimeListView) *presenter.Response {
	lines := make([]string, 0, len(v.Crimes))
	buttons := make([]presenter.Button, 0, len(v.Crimes))
	for _, cr := range v.Crimes {
		key := "crime.line"
		if !cr.Eligible {
			key = "crime.line_locked"
		}
		line := c.T(key, map[string]any{"crime": c.CrimeName(cr.Crime), "nerve": FormatNumber(c, int64(cr.Nerve))})
		if cr.Duration > 0 {
			line = c.T("crime.line_timed", map[string]any{"line": line, "duration": FormatDuration(c, cr.Duration)})
		}
		lines = append(lines, line)
		if btn, ok := keyboards.Button(c.CrimeName(cr.Crime), AddrCrimeView, cr.Crime.Code); ok {
			buttons = append(buttons, btn)
		}
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("crime.list_empty", nil)
	}
	var indicator string
	if v.Pages > 1 {
		indicator = c.T("page.indicator", map[string]any{"page": v.Page, "pages": v.Pages})
	}
	kb := keyboards.New()
	kb.Grid(2, buttons...)
	kb.Nav(c.nav(keyboards.Nav{
		Prefix:   keyboards.Data(AddrCrimeList, v.Category.Code),
		Page:     v.Page,
		HasPrev:  v.Page > 1,
		HasNext:  v.Page < v.Pages,
		BackData: AddrCrimeHub,
	}))
	return c.respond(paragraphs(c.T("crime.list_title", map[string]any{"category": c.CrimeCategoryName(v.Category)}),
		list, indicator), kb.Build())
}

// Why a crime cannot be committed now, beyond its requirements.
const (
	CrimeBlockedJail       = "jail"
	CrimeBlockedBusy       = "busy"
	CrimeBlockedWork       = "work"
	CrimeBlockedTravelling = "travelling"
	CrimeBlockedNerve      = "nerve"
	CrimeBlockedNowhere    = "nowhere"
)

// CrimeDetailView is one crime in detail.
type CrimeDetailView struct {
	Crime    Named
	Category Named
	Nerve    int
	// Duration is the real wait of a timed crime, zero for an instant one.
	Duration time.Duration
	// ChanceBPS is the player's odds against an NPC victim here and now.
	ChanceBPS int
	// HitsPlayers is whether the crime can land on a player nearby.
	HitsPlayers bool
	HitsNPCs    bool
	// MinTake and MaxTake are an NPC victim's take range.
	MinTake, MaxTake int64
	// JailMin and JailMax are the real sentence range at this city's
	// policy; FineMin and FineMax the fine range.
	JailMin, JailMax time.Duration
	FineMin, FineMax int64
	Requirements     []CrimeRequirement
	// Blocked says why the player cannot commit it now, "" when they can
	// (given CanCommit). Need and Have are nerve, for nerve.
	Blocked    string
	Need, Have int
	Wait       time.Duration
	CanCommit  bool
	// Nonce is the one-time token of the commit button: pressing it twice
	// is one attempt.
	Nonce string
}

// CrimeDetail renders one crime: its cost, odds, risks and requirements, and
// — only when everything is in order — the button that commits it.
func CrimeDetail(c Context, v CrimeDetailView) *presenter.Response {
	facts := []string{
		c.T("crime.view_category", map[string]any{"category": c.CrimeCategoryName(v.Category)}),
		c.T("crime.view_nerve", map[string]any{"nerve": FormatNumber(c, int64(v.Nerve))}),
	}
	if v.Duration > 0 {
		facts = append(facts, c.T("crime.view_duration", map[string]any{"duration": FormatDuration(c, v.Duration)}))
	} else {
		facts = append(facts, c.T("crime.view_instant", nil))
	}
	facts = append(facts, c.T("crime.view_chance", map[string]any{"percent": PercentFromBPS(c, v.ChanceBPS)}))
	if v.HitsNPCs && v.MaxTake > 0 {
		facts = append(facts, c.T("crime.view_take", map[string]any{
			"min": FormatMoney(c, v.MinTake), "max": FormatMoney(c, v.MaxTake)}))
	}
	if v.JailMax > 0 {
		facts = append(facts, c.T("crime.view_risk", map[string]any{
			"jail_min": FormatDuration(c, v.JailMin), "jail_max": FormatDuration(c, v.JailMax),
			"fine_min": FormatMoney(c, v.FineMin), "fine_max": FormatMoney(c, v.FineMax)}))
	}
	victims := c.T("crime.view_victims_npc", nil)
	if v.HitsPlayers {
		victims = c.T("crime.view_victims_player", nil)
	}

	reqs := c.T("crime.requirements_none", nil)
	if lines := c.crimeRequirementLines(v.Requirements); len(lines) > 0 {
		reqs = body(append([]string{c.T("crime.requirements", nil)}, lines...)...)
	}

	var blocked string
	switch v.Blocked {
	case CrimeBlockedJail:
		blocked = c.T("crime.blocked.jail", nil)
	case CrimeBlockedBusy:
		blocked = c.T("crime.blocked.busy", nil)
	case CrimeBlockedWork:
		blocked = c.T("crime.blocked.work", nil)
	case CrimeBlockedTravelling:
		blocked = c.T("crime.blocked.travelling", nil)
	case CrimeBlockedNowhere:
		blocked = c.T("crime.nowhere", nil)
	case CrimeBlockedNerve:
		blocked = c.T("crime.blocked.nerve", map[string]any{
			"need": FormatNumber(c, int64(v.Need)), "have": FormatNumber(c, int64(v.Have)),
			"wait": FormatDuration(c, v.Wait)})
	}

	kb := keyboards.New()
	if v.CanCommit {
		if btn, ok := keyboards.Button(c.T("crime.button.commit", nil), AddrCrimeCommit, v.Crime.Code, v.Nonce); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{
		BackData:    keyboards.Data(AddrCrimeList, v.Category.Code),
		RefreshData: keyboards.Data(AddrCrimeView, v.Crime.Code),
	}))
	return c.respond(paragraphs(c.T("crime.view_title", map[string]any{"crime": c.CrimeName(v.Crime)}),
		body(facts...), victims, reqs, blocked), kb.Build())
}

// CrimeResultView is how an attempt ended.
type CrimeResultView struct {
	// Player is the offender's shown name, for the group's version.
	Player string
	Crime  Named
	Venue  Named
	// CityCode and City are where it happened.
	CityCode, City string
	// Result is succeeded, escaped or caught (crime.Result's spelling).
	Result string
	// VictimPlayer is whether the victim was a player; never who.
	VictimPlayer bool
	// Take is what the thief gained; DrySpell means an NPC take that the
	// economy's daily cap cut to nothing.
	Take     int64
	DrySpell bool
	XP       int64
	// CriminalXP is the criminal experience gained.
	CriminalXP int64
	Skills     []SkillGain
	// Level is the character level reached, else 0.
	Level int
	Heat  HeatView
	Nerve NerveView
	// Jail is the sentence on an arrest: its real length and end.
	Jail *CrimeProgress
	// Fine is the fine ordered on an arrest and FinePaid what was paid.
	Fine, FinePaid int64
	// Notice marks the private notice of a timed crime's end or of a group
	// success's take, rather than the reply to a press.
	Notice bool
}

// Outcome spellings, as crime.Result writes them.
const (
	CrimeOutcomeSucceeded = "succeeded"
	CrimeOutcomeEscaped   = "escaped"
	CrimeOutcomeCaught    = "caught"
)

// CrimeResult renders an attempt's outcome. In a group (Context.Shared) it
// tells the room what happened and leaves every sum out; the thief's take
// reaches them privately as a notice.
func CrimeResult(c Context, v CrimeResultView) *presenter.Response {
	args := map[string]any{
		"player": v.Player,
		"crime":  c.CrimeName(v.Crime),
		"venue":  c.VenueName(v.Venue),
		"city":   c.CityName(v.CityCode, v.City),
	}
	var head string
	var lines []string
	switch v.Result {
	case CrimeOutcomeSucceeded:
		switch {
		case c.Shared:
			head = c.T("crime.result.success_public", args)
			lines = append(lines, c.T("crime.result.take_private", nil))
		case v.Notice:
			head = c.T("crime.result.success_notice", args)
		default:
			head = c.T("crime.result.success", args)
		}
		if !c.Shared {
			amount := map[string]any{"amount": FormatMoney(c, v.Take)}
			switch {
			case v.Take > 0 && v.VictimPlayer:
				lines = append(lines, c.T("crime.result.take_player", amount))
			case v.Take > 0:
				lines = append(lines, c.T("crime.result.take_npc", amount))
			case v.VictimPlayer:
				lines = append(lines, c.T("crime.result.take_empty", nil))
			case v.DrySpell:
				lines = append(lines, c.T("crime.result.take_dry", nil))
			}
		}
	case CrimeOutcomeEscaped:
		key := "crime.result.escaped"
		if v.Notice {
			key = "crime.result.escaped_notice"
		}
		head = c.T(key, args)
	case CrimeOutcomeCaught:
		if v.Jail != nil {
			args["term"] = FormatDuration(c, v.Jail.Remaining)
		}
		key := "crime.result.caught"
		if v.Notice {
			key = "crime.result.caught_notice"
		}
		head = c.T(key, args)
		switch {
		case v.Fine <= 0:
		case c.Shared:
			lines = append(lines, c.T("crime.result.fine_public", nil))
		case v.FinePaid >= v.Fine:
			lines = append(lines, c.T("crime.result.fine", map[string]any{"fine": FormatMoney(c, v.Fine)}))
		default:
			lines = append(lines, c.T("crime.result.fine_short", map[string]any{
				"fine": FormatMoney(c, v.Fine), "paid": FormatMoney(c, v.FinePaid),
				"unpaid": FormatMoney(c, v.Fine-v.FinePaid)}))
		}
		if v.Jail != nil {
			lines = append(lines, clockLine(c, "crime.free_at", v.Jail.EndsAt))
		}
	default:
		return Error(c, nil)
	}

	if v.XP > 0 {
		lines = append(lines, c.T("crime.result.xp", map[string]any{"xp": FormatNumber(c, v.XP)}))
	}
	if v.CriminalXP > 0 {
		lines = append(lines, c.T("crime.result.criminal_xp", map[string]any{"xp": FormatNumber(c, v.CriminalXP)}))
	}
	for _, s := range v.Skills {
		if s.XP <= 0 {
			continue
		}
		skill := c.T("skill."+s.Skill, nil)
		lines = append(lines, c.T("job.shift_skill", map[string]any{"skill": skill, "xp": FormatNumber(c, s.XP)}))
		if s.Level > 0 {
			lines = append(lines, c.T("job.shift_skill_level", map[string]any{"skill": skill, "level": FormatNumber(c, int64(s.Level))}))
		}
	}
	if v.Level > 0 {
		lines = append(lines, c.T("job.shift_level", map[string]any{"level": FormatNumber(c, int64(v.Level))}))
	}
	lines = append(lines, c.heatLine(v.Heat))
	if v.Result != CrimeOutcomeCaught && v.Nerve.Max > 0 {
		lines = append(lines, c.nerveLine(v.Nerve))
	}

	kb := keyboards.New()
	if v.Result == CrimeOutcomeCaught {
		jail, _ := keyboards.Button(c.T("crime.button.jail", nil), AddrCrimeJail)
		kb.Row(jail)
	} else if again, ok := keyboards.Button(c.T("crime.button.again", nil), AddrCrimeView, v.Crime.Code); ok {
		kb.Row(again)
	}
	hub, _ := keyboards.Button(c.T("crime.button.hub", nil), AddrCrimeHub)
	kb.Row(hub)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(paragraphs(head, body(lines...)), kb.Build())
}

// CrimeStartedView is a timed crime that has just begun.
type CrimeStartedView struct {
	Player   string
	Crime    Named
	Venue    Named
	Duration time.Duration
	EndsAt   time.Time
	Nerve    NerveView
}

// CrimeStarted renders the start of a timed crime. Its outcome arrives as a
// private notice when it ends.
func CrimeStarted(c Context, v CrimeStartedView) *presenter.Response {
	head := c.T("crime.started", map[string]any{
		"player": v.Player, "crime": c.CrimeName(v.Crime), "venue": c.VenueName(v.Venue),
		"duration": FormatDuration(c, v.Duration)})
	kb := keyboards.New()
	hub, _ := keyboards.Button(c.T("crime.button.hub", nil), AddrCrimeHub)
	kb.Row(hub)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(paragraphs(head, body(clockLine(c, "crime.back_at", v.EndsAt), c.nerveLine(v.Nerve)),
		c.T("crime.started_notice", nil)), kb.Build())
}

// CrimeRecordLine is one past attempt on the record.
type CrimeRecordLine struct {
	Crime  Named
	Result string
	At     time.Time
}

// CrimeRecordView is a player's criminal record.
type CrimeRecordView struct {
	Nerve       NerveView
	Heat        HeatView
	Tier        TierView
	Attempts    int
	Successes   int
	Arrests     int
	Convictions int
	// UnpaidRestitution and UnpaidFines are what convictions ordered and
	// could not be paid; private.
	UnpaidRestitution int64
	UnpaidFines       int64
	Recent            []CrimeRecordLine
}

// CrimeRecord renders the record. A group sees the counts and the recent
// attempts; the unpaid sums are the player's own.
func CrimeRecord(c Context, v CrimeRecordView) *presenter.Response {
	counts := c.T("crime.record_counts", map[string]any{
		"attempts": FormatNumber(c, int64(v.Attempts)), "successes": FormatNumber(c, int64(v.Successes)),
		"arrests": FormatNumber(c, int64(v.Arrests)), "convictions": FormatNumber(c, int64(v.Convictions))})
	var unpaid string
	if !c.Shared && (v.UnpaidRestitution > 0 || v.UnpaidFines > 0) {
		unpaid = c.T("crime.record_unpaid", map[string]any{
			"restitution": FormatMoney(c, v.UnpaidRestitution), "fines": FormatMoney(c, v.UnpaidFines)})
	}
	recent := c.T("crime.record_clean", nil)
	if len(v.Recent) > 0 {
		lines := []string{c.T("crime.record_recent", nil)}
		for _, r := range v.Recent {
			lines = append(lines, c.T("crime.record_line."+r.Result, map[string]any{"crime": c.CrimeName(r.Crime)}))
		}
		recent = body(lines...)
	}
	kb := keyboards.New()
	hub, _ := keyboards.Button(c.T("crime.button.hub", nil), AddrCrimeHub)
	kb.Row(hub)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCrimeHub, RefreshData: AddrCrimeRecord}))
	return c.respond(paragraphs(c.T("crime.record_title", nil),
		body(c.tierLine(v.Tier), c.heatLine(v.Heat), c.nerveLine(v.Nerve)), body(counts, unpaid), recent), kb.Build())
}

// JailView is the jail screen.
type JailView struct {
	// InJail is false for a free player; nothing else is set then.
	InJail         bool
	CityCode, City string
	// Reason is arrest or conviction.
	Reason    string
	Remaining time.Duration
	EndsAt    time.Time
	// Bail is what leaving now costs; Nonce the bail button's one-time
	// token.
	Bail  int64
	Nonce string
}

// Jail renders the jail screen: the time left and the bail button.
func Jail(c Context, v JailView) *presenter.Response {
	kb := keyboards.New()
	if !v.InJail {
		hub, _ := keyboards.Button(c.T("crime.button.hub", nil), AddrCrimeHub)
		kb.Row(hub)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrCrimeJail}))
		return c.respond(paragraphs(c.T("crime.jail_title", nil), c.T("crime.jail_free", nil)), kb.Build())
	}
	reason := c.T("crime.jail_reason."+v.Reason, nil)
	lines := []string{
		c.T("crime.jail_where", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		reason,
		c.T("crime.in_jail", map[string]any{"remaining": FormatDuration(c, v.Remaining)}),
		clockLine(c, "crime.free_at", v.EndsAt),
	}
	if v.Bail > 0 {
		lines = append(lines, c.T("crime.jail_bail", map[string]any{"bail": FormatMoney(c, v.Bail)}))
		if btn, ok := keyboards.Button(c.T("crime.button.bail", nil), AddrCrimeBail, v.Nonce); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCrimeHub, RefreshData: AddrCrimeJail}))
	return c.respond(paragraphs(c.T("crime.jail_title", nil), body(lines...), c.T("crime.jail_blocks", nil)), kb.Build())
}

// BailedView is a bail paid.
type BailedView struct {
	Player string
	Bail   int64
}

// Bailed renders a release on bail. A group reads that the player walked
// out; the sum is theirs.
func Bailed(c Context, v BailedView) *presenter.Response {
	text := c.T("crime.bailed", map[string]any{"bail": FormatMoney(c, v.Bail)})
	if c.Shared {
		text = c.T("crime.bailed_public", map[string]any{"player": v.Player})
	}
	kb := keyboards.New()
	hub, _ := keyboards.Button(c.T("crime.button.hub", nil), AddrCrimeHub)
	kb.Row(hub)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(text, kb.Build())
}

// ReleasedNotice tells a player their sentence is served.
func ReleasedNotice(c Context, city Named) *presenter.Response {
	kb := keyboards.New()
	hub, _ := keyboards.Button(c.T("crime.button.hub", nil), AddrCrimeHub)
	kb.Row(hub)
	return c.respond(c.T("crime.released", map[string]any{"city": c.CityName(city.Code, city.Name)}), kb.Build()).MarkPrivate()
}

// VictimNoticeView is a theft, as its victim learns of it.
type VictimNoticeView struct {
	Crime          Named
	Venue          Named
	CityCode, City string
	Amount         int64
	// ThiefName and ThiefCode are set only when a witness saw who did it.
	ThiefName, ThiefCode string
	// CrimeID addresses the report button.
	CrimeID string
	// ReportFee is the fee in force; ReportWithin how long is left to
	// report.
	ReportFee    int64
	ReportWithin time.Duration
}

// VictimNotice tells a player they were robbed, and offers the report. It is
// private: nobody else learns that they were robbed or of how much.
func VictimNotice(c Context, v VictimNoticeView) *presenter.Response {
	head := c.T("crime.victim.head", map[string]any{
		"crime": c.CrimeName(v.Crime), "venue": c.VenueName(v.Venue), "city": c.CityName(v.CityCode, v.City),
		"amount": FormatMoney(c, v.Amount)})
	witness := c.T("crime.victim.unseen", nil)
	if v.ThiefName != "" {
		witness = c.T("crime.victim.seen", map[string]any{"thief": v.ThiefName, "code": v.ThiefCode})
	}
	report := body(c.T("crime.victim.report", map[string]any{"fee": FormatMoney(c, v.ReportFee)}),
		c.T("crime.victim.report_by", map[string]any{"duration": FormatDuration(c, v.ReportWithin)}))
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("crime.button.report", nil), AddrCrimeReport, v.CrimeID); ok {
		kb.Row(btn)
	}
	return c.respond(paragraphs(head, witness, report), kb.Build()).MarkPrivate()
}

// ReportConfirmView asks a victim to confirm a report.
type ReportConfirmView struct {
	CrimeID        string
	Crime          Named
	CityCode, City string
	Amount         int64
	Fee            int64
	// Investigation is how long the investigation takes, real time;
	// ReportWithin how long is left to report.
	Investigation time.Duration
	ReportWithin  time.Duration
}

// ReportConfirm renders the report's confirmation, with its fee.
func ReportConfirm(c Context, v ReportConfirmView) *presenter.Response {
	text := c.T("crime.report.confirm", map[string]any{
		"crime": c.CrimeName(v.Crime), "city": c.CityName(v.CityCode, v.City),
		"amount": FormatMoney(c, v.Amount), "fee": FormatMoney(c, v.Fee),
		"duration": FormatDuration(c, v.Investigation)})
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("crime.button.report_confirm", nil), AddrCrimeReport, v.CrimeID, ReportConfirmation); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCrimeCases}))
	return c.respond(body(text, c.T("crime.victim.report_by", map[string]any{"duration": FormatDuration(c, v.ReportWithin)})),
		kb.Build()).MarkPrivate()
}

// CaseFiled renders a report filed.
func CaseFiled(c Context, investigation time.Duration, endsAt time.Time) *presenter.Response {
	kb := keyboards.New()
	cases, _ := keyboards.Button(c.T("crime.button.cases", nil), AddrCrimeCases)
	kb.Row(cases)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(c.T("crime.report.filed", map[string]any{"duration": FormatDuration(c, investigation)}),
		clockLine(c, "crime.report.outcome_at", endsAt)), kb.Build()).MarkPrivate()
}

// CaseLine is one report on the victim's list.
type CaseLine struct {
	Crime          Named
	CityCode, City string
	Amount         int64
	// Status is investigating, solved or unsolved.
	Status    string
	Remaining time.Duration
	// Thief names the convicted thief of a solved case.
	Thief    string
	Restored int64
}

// CasesView is the victim's reports.
type CasesView struct{ Cases []CaseLine }

// Cases renders the victim's reports. Private: what they lost is theirs.
func Cases(c Context, v CasesView) *presenter.Response {
	lines := make([]string, 0, len(v.Cases))
	for _, k := range v.Cases {
		args := map[string]any{
			"crime": c.CrimeName(k.Crime), "city": c.CityName(k.CityCode, k.City),
			"amount": FormatMoney(c, k.Amount), "remaining": FormatDuration(c, k.Remaining),
			"thief": k.Thief, "restored": FormatMoney(c, k.Restored),
		}
		key := "crime.case." + k.Status
		if k.Status == application.ReportInvestigating && k.Remaining < arrivingThreshold {
			key = "crime.case.closing"
		}
		lines = append(lines, c.T(key, args))
	}
	list := body(lines...)
	if len(lines) == 0 {
		list = c.T("crime.cases_none", nil)
	}
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCrimeHub, RefreshData: AddrCrimeCases}))
	return c.respond(paragraphs(c.T("crime.cases_title", nil), list), kb.Build()).MarkPrivate()
}

// CaseOutcomeView is a concluded case, as its victim or its thief learns of
// it.
type CaseOutcomeView struct {
	Crime          Named
	CityCode, City string
	Solved         bool
	// Thief names the convicted thief, for the victim.
	Thief, ThiefCode string
	Stolen           int64
	Restored         int64
	Shortfall        int64
	Fine, FinePaid   int64
	// Term is the real length of the sentence handed down.
	Term time.Duration
}

// CaseSolvedNotice tells a victim how their case ended.
func CaseSolvedNotice(c Context, v CaseOutcomeView) *presenter.Response {
	args := map[string]any{
		"crime": c.CrimeName(v.Crime), "city": c.CityName(v.CityCode, v.City),
		"thief": v.Thief, "code": v.ThiefCode, "restored": FormatMoney(c, v.Restored),
		"short": FormatMoney(c, v.Shortfall),
	}
	kb := keyboards.New()
	cases, _ := keyboards.Button(c.T("crime.button.cases", nil), AddrCrimeCases)
	kb.Row(cases)
	if !v.Solved {
		return c.respond(c.T("crime.case_closed", args), kb.Build()).MarkPrivate()
	}
	lines := []string{c.T("crime.case_solved", args)}
	if v.Shortfall > 0 {
		lines = append(lines, c.T("crime.case_shortfall", args))
	}
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// ConvictedNotice tells a thief a reported theft was traced to them.
func ConvictedNotice(c Context, v CaseOutcomeView) *presenter.Response {
	lines := []string{c.T("crime.convicted", map[string]any{
		"crime": c.CrimeName(v.Crime), "city": c.CityName(v.CityCode, v.City),
		"restored": FormatMoney(c, v.Restored), "fine": FormatMoney(c, v.FinePaid),
		"term": FormatDuration(c, v.Term)})}
	if v.Shortfall > 0 || v.FinePaid < v.Fine {
		lines = append(lines, c.T("crime.convicted_unpaid", map[string]any{
			"restitution": FormatMoney(c, v.Shortfall), "fines": FormatMoney(c, v.Fine-v.FinePaid)}))
	}
	kb := keyboards.New()
	jail, _ := keyboards.Button(c.T("crime.button.jail", nil), AddrCrimeJail)
	kb.Row(jail)
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// Crime refusal kinds: a crime request that cannot be done, each with its
// own sentence and next step.
const (
	CrimeRefusedRequirements  = "requirements"
	CrimeRefusedNotFound      = "not_found"
	CrimeRefusedJail          = "jail"
	CrimeRefusedBusy          = "busy"
	CrimeRefusedWork          = "work"
	CrimeRefusedTravelling    = "travelling"
	CrimeRefusedNowhere       = "nowhere"
	CrimeRefusedNerve         = "nerve"
	CrimeRefusedNoVictim      = "no_victim"
	CrimeRefusedNotYours      = "not_yours"
	CrimeRefusedExpired       = "expired"
	CrimeRefusedCannotAfford  = "cannot_afford"
	CrimeRefusedNotJailed     = "not_jailed"
	CrimeRefusedNothingStolen = "nothing_stolen"
)

// CrimeRefusalView is a refused crime request.
type CrimeRefusalView struct {
	Kind    string
	Crime   Named
	Missing []CrimeRequirement
	// Need and Have are nerve, for nerve; Amount and Cash a fee or a bail
	// and what the player holds, for cannot_afford.
	Need, Have   int
	Wait         time.Duration
	Amount, Cash int64
	// Remaining is the time left in jail or on a timed crime.
	Remaining time.Duration
}

// crimeRefusals maps a refusal to its sentence and one next step.
var crimeRefusals = map[string]struct{ key, label, addr string }{
	CrimeRefusedRequirements:  {"crime.refused.requirements", "crime.button.hub", AddrCrimeHub},
	CrimeRefusedNotFound:      {"crime.refused.not_found", "crime.button.hub", AddrCrimeHub},
	CrimeRefusedJail:          {"crime.refused.jail", "crime.button.jail", AddrCrimeJail},
	CrimeRefusedBusy:          {"crime.refused.busy", "crime.button.hub", AddrCrimeHub},
	CrimeRefusedWork:          {"crime.refused.work", "job.button.my_job", AddrJobStatus},
	CrimeRefusedTravelling:    {"crime.refused.travelling", "button.journey", AddrTravelStatus},
	CrimeRefusedNowhere:       {"crime.nowhere", "button.map", AddrMap},
	CrimeRefusedNerve:         {"crime.refused.nerve", "crime.button.hub", AddrCrimeHub},
	CrimeRefusedNoVictim:      {"crime.refused.no_victim", "crime.button.hub", AddrCrimeHub},
	CrimeRefusedNotYours:      {"crime.refused.not_yours", "crime.button.cases", AddrCrimeCases},
	CrimeRefusedExpired:       {"crime.refused.expired", "crime.button.cases", AddrCrimeCases},
	CrimeRefusedCannotAfford:  {"crime.refused.cannot_afford", "button.bank", AddrBank},
	CrimeRefusedNotJailed:     {"crime.jail_free", "crime.button.hub", AddrCrimeHub},
	CrimeRefusedNothingStolen: {"crime.refused.nothing_stolen", "crime.button.cases", AddrCrimeCases},
}

// CrimeRefusal renders a refused crime request.
func CrimeRefusal(c Context, v CrimeRefusalView) *presenter.Response {
	r, ok := crimeRefusals[v.Kind]
	if !ok {
		return Error(c, nil)
	}
	head := c.T(r.key, map[string]any{
		"crime": c.CrimeName(v.Crime),
		"need":  FormatNumber(c, int64(v.Need)), "have": FormatNumber(c, int64(v.Have)),
		"wait":      FormatDuration(c, v.Wait),
		"amount":    FormatMoney(c, v.Amount),
		"cash":      FormatMoney(c, v.Cash),
		"remaining": FormatDuration(c, v.Remaining),
	})
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T(r.label, nil), r.addr); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrCrimeHub}))
	resp := c.respond(body(append([]string{head}, c.crimeRequirementLines(v.Missing)...)...), kb.Build())
	switch v.Kind {
	case CrimeRefusedCannotAfford, CrimeRefusedNotYours, CrimeRefusedExpired, CrimeRefusedNothingStolen:
		// The player's money and their being robbed stay out of a group.
		resp.MarkPrivate()
	}
	return resp
}

// crimeError names the refusals other features raise for the crime engine:
// a jailed player, or one in the middle of a timed crime, asking to travel,
// work or withdraw.
func crimeError(c Context, err error) (string, map[string]any, bool) {
	switch {
	case identical(err, application.ErrInJail):
		secs := detailInt(err, "remaining_seconds")
		if secs <= 0 {
			return "crime.error.in_jail_later", nil, true
		}
		return "crime.error.in_jail", map[string]any{
			"remaining": FormatDuration(c, time.Duration(secs)*time.Second)}, true
	case identical(err, application.ErrCrimeInProgress):
		return "crime.error.in_progress", nil, true
	}
	return "", nil, false
}

func init() {
	errorNextStep["crime.error.in_jail"] = struct{ label, addr string }{"crime.button.jail", AddrCrimeJail}
	errorNextStep["crime.error.in_jail_later"] = struct{ label, addr string }{"crime.button.jail", AddrCrimeJail}
	errorNextStep["crime.error.in_progress"] = struct{ label, addr string }{"crime.button.hub", AddrCrimeHub}
}

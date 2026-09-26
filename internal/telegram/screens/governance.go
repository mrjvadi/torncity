package screens

import (
	stderrors "errors"
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// This file holds the screens of player-held offices
// (docs/adr/0015-player-held-offices.md): a city's offices and policies, the
// office holder's own screen, the flow that changes a policy, and the public
// record of changes.
//
// Every value shown here was answered by the policy resolver; a screen only
// formats it. Offices, levers and places are named through the catalogue by
// their content code — office.<code>, lever.<code>, jurisdiction.<code> — and
// a code with no entry reads as a generic phrase, never as the code itself: a
// lever code, an office code or an id is not something a player should read.

// Callback addresses of the governance screens.
//
// A lever is addressed by its content code and the code of the place it is
// set in, never by a database id: both are short, stable and authored, and
// the core looks the place up again and re-checks, through SetPolicy, that
// this player may change the lever there. A value in an address is only a
// proposal: SetPolicy refuses one out of bounds whatever a button said.
const (
	AddrGovCity    = "gov:city"
	AddrGovHistory = "gov:history"
	AddrGovOffice  = "gov:office"
	AddrGovLever   = "gov:lever"
	AddrGovConfirm = "gov:confirm"
	AddrGovSet     = "gov:set"
	// The allocation editor (a budget): the draft travels in the address,
	// one character per category (docs/adr/0024-property-and-politics.md).
	AddrGovAlloc        = "gov:alloc"
	AddrGovAllocConfirm = "gov:allocok"
	AddrGovAllocSet     = "gov:allocset"
)

// Catalogue namespaces for content-coded names.
const (
	officeKeyPrefix       = "office."
	leverKeyPrefix        = "lever."
	jurisdictionKeyPrefix = "jurisdiction."
)

// Lever value types the screens can format. They mirror the scalar types of
// internal/content; a structured lever never reaches a screen.
const (
	leverTypeBPS   = "bps"
	leverTypeMoney = "money"
	leverTypeInt   = "int"
)

// cityKind is the level whose places are named through city.<code>.
const cityKind = "city"

// countryKind is the level of a country.
const countryKind = "country"

// GovPlayer names another player: the display name and the public code, the
// two things one player may see of another.
type GovPlayer struct {
	Name string
	Code string
}

// GovPlace is one jurisdiction: a city or a country.
type GovPlace struct {
	Kind string
	Code string
	// Name is the authored name, the fallback for an untranslated code.
	Name string
}

// GovOffice is one office of a place and who sits in it.
type GovOffice struct {
	Code  string
	Seats int
	// Holders are the players in its held seats.
	Holders []GovPlayer
	// ActingCode and Acting name the deputy office acting for this one
	// while every seat of it is vacant, and who sits in it. Empty when the
	// office is held or nobody acts for it.
	ActingCode string
	Acting     []GovPlayer
}

// GovLever is one policy as the resolver answered it, with the constitution
// around it.
type GovLever struct {
	Code string
	Type string
	// Value is the value in force now.
	Value             int64
	Default, Min, Max int64
	// FromOffice says an office holder set Value; SetBy is who, when known.
	FromOffice bool
	SetBy      *GovPlayer
	// Pending is the next announced change, if any.
	Pending *GovPending
	// HeldBy is the office deciding the lever.
	HeldBy           string
	Notice, Cooldown time.Duration
	// Vote says the lever is decided by a vote of HeldBy: nobody changes it
	// alone; a member proposes and the body votes.
	Vote bool
	// ConfirmBy is the body whose vote confirms a change, empty for none.
	ConfirmBy string
	// Allocation and Categories are an allocation lever's shares in force
	// and its categories, in order; nil for a scalar lever.
	Allocation map[string]int64
	Categories []string
}

// isAllocation reports whether the lever divides a budget.
func (l GovLever) isAllocation() bool { return l.Type == application.LeverAllocation }

// leverValue renders the lever's value in force: an allocation's shares, a
// scalar in its unit.
func (c Context) leverValue(l GovLever) string {
	if l.isAllocation() {
		return c.AllocationText(l.Allocation, l.Categories)
	}
	return FormatPolicyValue(c, l.Code, l.Type, l.Value)
}

// GovPending is a change announced and not yet in force.
type GovPending struct {
	Value int64
	// Allocation is an allocation lever's announced shares.
	Allocation map[string]int64
	// In is how long until it takes effect.
	In time.Duration
	By *GovPlayer
}

// GovSection is one place's offices and policies.
type GovSection struct {
	Place   GovPlace
	Offices []GovOffice
	Levers  []GovLever
}

// CityGovView is the city hall screen: the city's offices and policies, and
// those of every place above it.
type CityGovView struct {
	City GovPlace
	// Sections are the city first, then each place above it.
	Sections []GovSection
	// HoldsOffice offers the viewer a way to their own office screen.
	HoldsOffice bool
	// NoCity means the viewer asked for their own city and is in none.
	NoCity bool
}

// GovSeat is one seat the viewer holds, and what it lets them change.
type GovSeat struct {
	Office string
	Place  GovPlace
	// ActingFor names the vacant office this seat acts for, when it acts
	// as a deputy.
	ActingFor string
	// Levers are the policies the viewer can change from this seat now.
	Levers []GovLever
	// VoteLevers are the policies this office decides by a vote.
	VoteLevers []GovLever
	// Appointees are the seats this office appoints to or may remove the
	// holder of.
	Appointees []GovAppointee
}

// MyOfficeView is the office holder's screen.
type MyOfficeView struct {
	Seats []GovSeat
}

// LeverEditView is one policy the viewer can change, with a proposed value.
type LeverEditView struct {
	Place GovPlace
	Lever GovLever
	// Draft is the value being proposed; it starts at the value that will
	// be in force.
	Draft int64
	// FineStep and CoarseStep are the two step sizes of the +/- buttons.
	FineStep, CoarseStep int64
	// NextChangeIn is how long until the lever may change again; zero when
	// it may change now.
	NextChangeIn time.Duration
}

// PolicyConfirmView asks the office holder to confirm one change.
type PolicyConfirmView struct {
	Place    GovPlace
	Lever    GovLever
	NewValue int64
	// VoteBy is the body the change goes to for a vote, empty when it is
	// announced at once.
	VoteBy string
}

// confirmNotice says when a change takes effect: after its notice, or —
// when a body must confirm it — after the vote.
func (c Context) confirmNotice(l GovLever, body string) string {
	if body != "" {
		return c.confirmVote(l, body)
	}
	return c.T("gov.confirm.notice", map[string]any{"notice": FormatSpan(c, l.Notice)})
}

// confirmVote is the line saying a change goes to a vote first.
func (c Context) confirmVote(l GovLever, body string) string {
	if body == "" {
		return ""
	}
	return c.T("gov.confirm.vote", map[string]any{"office": c.OfficeName(body)})
}

// PolicyAnnouncedView reports a change that was made.
type PolicyAnnouncedView struct {
	Place    GovPlace
	Lever    GovLever
	Old, New int64
	// OldAllocation and NewAllocation are an allocation lever's shares.
	OldAllocation, NewAllocation map[string]int64
	// In is how long until it takes effect.
	In time.Duration
}

// GovHistoryEntry is one change in the public record.
type GovHistoryEntry struct {
	Place GovPlace
	Lever string
	// Type formats the values; empty when the lever is no longer in the
	// active content, and the values are shown as plain numbers.
	Type     string
	Office   string
	By       *GovPlayer
	Old, New int64
	// Ago is how long since it was announced; EffectiveIn how long until it
	// takes effect, negative once it has.
	Ago         time.Duration
	EffectiveIn time.Duration
}

// GovHistoryView is one page of a city's public record.
type GovHistoryView struct {
	City    GovPlace
	Entries []GovHistoryEntry
	Page    int
	Pages   int
}

// PolicyRefusalView is a governance refusal with what the screen knows about
// its lever, so bounds and waits can be written in the lever's unit.
type PolicyRefusalView struct {
	Err error
	// Place and Lever are nil when the refusal came before either was known.
	Place *GovPlace
	Lever *GovLever
	// Now is when the refusal happened, for "available in …".
	Now time.Time
}

// PlaceName names a jurisdiction in this context's language: a city through
// city.<code>, anything else through jurisdiction.<code>, and the authored
// name when the catalogue has no entry.
func (c Context) PlaceName(p GovPlace) string {
	if p.Kind == cityKind {
		return c.CityName(p.Code, p.Name)
	}
	if p.Code != "" {
		key := jurisdictionKeyPrefix + p.Code
		if text := c.T(key, nil); text != key {
			return text
		}
	}
	return p.Name
}

// OfficeName names an office, or says "an office" for a code the catalogue
// does not know.
func (c Context) OfficeName(code string) string {
	return c.coded(officeKeyPrefix, code, "gov.unnamed_office")
}

// LeverName names a policy, or says "a policy" for a code the catalogue does
// not know.
func (c Context) LeverName(code string) string {
	return c.coded(leverKeyPrefix, code, "gov.unnamed_lever")
}

func (c Context) coded(prefix, code, fallback string) string {
	if code != "" {
		key := prefix + code
		if text := c.T(key, nil); text != key {
			return text
		}
	}
	return c.T(fallback, nil)
}

// govPlayer renders another player as "name (code)".
func (c Context) govPlayer(p *GovPlayer) string {
	if p == nil || (p.Name == "" && p.Code == "") {
		return c.T("social.unknown_player", nil)
	}
	if p.Name == "" {
		return p.Code
	}
	if p.Code == "" {
		return p.Name
	}
	return c.T("gov.player", map[string]any{"name": p.Name, "code": p.Code})
}

func (c Context) govPlayers(ps []GovPlayer) string {
	out := ""
	for i := range ps {
		if i > 0 {
			out += c.T("gov.list_separator", nil)
		}
		out += c.govPlayer(&ps[i])
	}
	return out
}

// FormatPolicyValue renders a value of one lever: its words when the
// catalogue names this value of it (lever_label.<code>.v<value>, for a lever
// whose integers stand for choices, such as an arms export policy), else in
// its unit as FormatLeverValue.
func FormatPolicyValue(c Context, code, typ string, v int64) string {
	if code != "" {
		key := "lever_label." + code + ".v" + strconv.FormatInt(v, 10)
		if text := c.T(key, nil); text != key {
			return text
		}
	}
	return FormatLeverValue(c, typ, v)
}

// labelled reports whether a lever's values are named choices
// (lever_label.<code>.v<min> exists), and at most a handful of them.
func labelled(c Context, l GovLever) bool {
	if l.Code == "" || l.Max-l.Min > 10 {
		return false
	}
	key := "lever_label." + l.Code + ".v" + strconv.FormatInt(l.Min, 10)
	return c.T(key, nil) != key
}

// FormatLeverValue renders a policy value in its unit: a basis-point rate as
// a percentage, money through FormatMoney, a count with grouped digits.
func FormatLeverValue(c Context, typ string, v int64) string {
	switch typ {
	case leverTypeBPS:
		return c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v))})
	case leverTypeMoney:
		return FormatMoney(c, v)
	}
	return FormatNumber(c, v)
}

// FormatSpan renders a duration that may run to days: whole days and hours
// from two days up, and FormatDuration below that.
func FormatSpan(c Context, d time.Duration) string {
	const day = 24 * time.Hour
	if d < 2*day {
		return FormatDuration(c, d)
	}
	hours := int((d + time.Hour - 1) / time.Hour)
	days, rest := hours/24, hours%24
	if rest == 0 {
		return c.T("gov.span_d", map[string]any{"days": days})
	}
	return c.T("gov.span_dh", map[string]any{"days": days, "hours": rest})
}

// leverAddr addresses one lever in one place, with an optional value.
func leverAddr(action string, l GovLever, p GovPlace, value ...int64) []string {
	parts := []string{action, l.Code, p.Code}
	for _, v := range value {
		parts = append(parts, strconv.FormatInt(v, 10))
	}
	return parts
}

// CityGovernance renders a city's offices and policies.
func CityGovernance(c Context, v CityGovView) *presenter.Response {
	return c.withView(renderCityGovernance(c, v), ScreenCityGovernance, v)
}

func renderCityGovernance(c Context, v CityGovView) *presenter.Response {
	kb := keyboards.New()
	if v.NoCity {
		kb.Add(c.T("button.map", nil), AddrMap)
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap}))
		return c.respond(paragraphs(c.T("gov.city.title_plain", nil), c.T("gov.city.no_city", nil)), kb.Build())
	}

	blocks := []string{c.T("gov.city.title", map[string]any{"city": c.PlaceName(v.City)})}
	for _, s := range v.Sections {
		blocks = append(blocks, govSection(c, s))
	}

	if b, ok := keyboards.Button(c.T("gov.button.history", nil), AddrGovHistory, v.City.Code); ok {
		elections, _ := keyboards.Button(c.T("gov.button.elections", nil), AddrElections)
		kb.Row(b, elections)
	}
	if b, ok := keyboards.Button(c.T("gov.button.budget", nil), AddrBudget, v.City.Code); ok {
		laws, _ := keyboards.Button(c.T("gov.button.laws", nil), AddrBills)
		kb.Row(b, laws)
	}
	if v.HoldsOffice {
		kb.Add(c.T("gov.button.my_office", nil), AddrGovOffice)
	}
	for _, sec := range v.Sections {
		if sec.Place.Kind == countryKind {
			kb.Add(c.T("military.button.ministry", map[string]any{"country": c.PlaceName(sec.Place)}), AddrMinistry, sec.Place.Code)
		}
	}
	refresh := keyboards.Data(AddrGovCity, v.City.Code)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap, RefreshData: refresh}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

func govSection(c Context, s GovSection) string {
	lines := []string{c.T("gov.city.place", map[string]any{"place": c.PlaceName(s.Place)})}

	lines = append(lines, c.T("gov.city.offices", nil))
	if len(s.Offices) == 0 {
		lines = append(lines, c.T("gov.city.no_offices", nil))
	}
	for _, o := range s.Offices {
		lines = append(lines, govOfficeLine(c, o))
	}

	if len(s.Levers) > 0 {
		lines = append(lines, c.T("gov.city.policies", nil))
	}
	for _, l := range s.Levers {
		lines = append(lines, govLeverLines(c, l)...)
	}
	return body(lines...)
}

func govOfficeLine(c Context, o GovOffice) string {
	name := c.OfficeName(o.Code)
	switch {
	case len(o.Holders) > 0 && o.Seats > 1:
		return c.T("gov.office.seats", map[string]any{
			"office": name, "held": len(o.Holders), "seats": o.Seats, "players": c.govPlayers(o.Holders),
		})
	case len(o.Holders) > 0:
		return c.T("gov.office.held", map[string]any{"office": name, "players": c.govPlayers(o.Holders)})
	case o.ActingCode != "" && len(o.Acting) > 0:
		return c.T("gov.office.acting", map[string]any{
			"office": name, "deputy": c.OfficeName(o.ActingCode), "players": c.govPlayers(o.Acting),
		})
	}
	return c.T("gov.office.vacant", map[string]any{"office": name})
}

// govLeverLines is one policy: its value and where the value came from, then
// the announced change, if any.
func govLeverLines(c Context, l GovLever) []string {
	value := c.leverValue(l)
	var lines []string
	if l.FromOffice {
		lines = append(lines, c.T("gov.lever.line_set", map[string]any{
			"lever": c.LeverName(l.Code), "value": value, "player": c.govPlayer(l.SetBy),
		}))
	} else {
		lines = append(lines, c.T("gov.lever.line_default", map[string]any{
			"lever": c.LeverName(l.Code), "value": value,
		}))
	}
	if l.Pending != nil {
		pending := FormatPolicyValue(c, l.Code, l.Type, l.Pending.Value)
		if l.isAllocation() {
			pending = c.AllocationText(l.Pending.Allocation, l.Categories)
		}
		lines = append(lines, c.T("gov.lever.pending", map[string]any{
			"value": pending, "when": FormatSpan(c, l.Pending.In),
		}))
	}
	return lines
}

// MyOffice renders the viewer's offices and the policies they can change.
func MyOffice(c Context, v MyOfficeView) *presenter.Response {
	return c.withView(renderMyOffice(c, v), ScreenMyOffice, v)
}

func renderMyOffice(c Context, v MyOfficeView) *presenter.Response {
	kb := keyboards.New()
	blocks := []string{c.T("gov.mine.title", nil)}

	if len(v.Seats) == 0 {
		blocks = append(blocks, c.T("gov.mine.none", nil))
	}
	for _, s := range v.Seats {
		place := c.PlaceName(s.Place)
		lines := []string{c.T("gov.mine.seat", map[string]any{"office": c.OfficeName(s.Office), "place": place})}
		if s.ActingFor != "" {
			lines = append(lines, c.T("gov.mine.acting_for", map[string]any{"office": c.OfficeName(s.ActingFor)}))
		}
		if len(s.Levers) == 0 && len(s.VoteLevers) == 0 {
			lines = append(lines, c.T("gov.mine.no_levers", nil))
		}
		for _, l := range s.Levers {
			lines = append(lines, c.T("gov.mine.lever", map[string]any{
				"lever": c.LeverName(l.Code), "value": c.leverValue(l),
			}))
			if l.ConfirmBy != "" {
				lines = append(lines, c.T("gov.mine.confirmed_by", map[string]any{"office": c.OfficeName(l.ConfirmBy)}))
			}
			kb.Add(c.T("gov.button.change", map[string]any{"lever": c.LeverName(l.Code), "place": place}),
				leverAddr(AddrGovLever, l, s.Place)...)
		}
		for _, l := range s.VoteLevers {
			lines = append(lines, c.T("gov.mine.lever_vote", map[string]any{
				"lever": c.LeverName(l.Code), "value": c.leverValue(l), "office": c.OfficeName(l.HeldBy),
			}))
			kb.Add(c.T("gov.button.propose", map[string]any{"lever": c.LeverName(l.Code), "place": place}),
				leverAddr(AddrGovLever, l, s.Place)...)
		}
		lines = append(lines, appointeeLines(c, kb, s.Appointees)...)
		blocks = append(blocks, body(lines...))
	}
	// A country's offices lead to its ministry of defence and its
	// diplomacy, once per country.
	shown := map[string]bool{}
	for _, s := range v.Seats {
		if s.Place.Kind != countryKind || shown[s.Place.Code] {
			continue
		}
		shown[s.Place.Code] = true
		kb.Add(c.T("military.button.ministry", map[string]any{"country": c.PlaceName(s.Place)}), AddrMinistry, s.Place.Code)
	}

	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovCity, RefreshData: AddrGovOffice}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// LeverEdit renders one policy the viewer may change, the value they are
// proposing, and the buttons that move it.
func LeverEdit(c Context, v LeverEditView) *presenter.Response {
	return c.withView(renderLeverEdit(c, v), ScreenLeverEdit, v)
}

func renderLeverEdit(c Context, v LeverEditView) *presenter.Response {
	l := v.Lever
	format := func(x int64) string { return FormatPolicyValue(c, l.Code, l.Type, x) }
	kb := keyboards.New()

	head := []string{
		c.T("gov.edit.title", map[string]any{"lever": c.LeverName(l.Code), "place": c.PlaceName(v.Place)}),
	}
	head = append(head, govLeverLines(c, l)...)

	bounds := c.T("gov.edit.bounds", map[string]any{"min": format(l.Min), "max": format(l.Max)})
	if labelled(c, l) {
		// Named choices have no range to state: the buttons are the choices.
		bounds = ""
	}
	rules := body(
		bounds,
		c.T("gov.edit.default", map[string]any{"value": format(l.Default)}),
		c.T("gov.edit.notice", map[string]any{"notice": FormatSpan(c, l.Notice)}),
		c.T("gov.edit.cooldown", map[string]any{"cooldown": FormatSpan(c, l.Cooldown)}),
	)

	var action string
	if v.NextChangeIn > 0 {
		action = c.T("gov.edit.cooldown_active", map[string]any{"wait": FormatSpan(c, v.NextChangeIn)})
	} else {
		action = c.T("gov.edit.draft", map[string]any{"value": format(v.Draft)})
		addr := func(x int64) []string { return leverAddr(AddrGovLever, l, v.Place, x) }

		if labelled(c, l) {
			// A lever whose values are choices is set by choosing: one
			// button per choice, never a step.
			var choices []presenter.Button
			for x := l.Min; x <= l.Max; x++ {
				if x == v.Draft {
					continue
				}
				if b, ok := keyboards.Button(format(x), addr(x)...); ok {
					choices = append(choices, b)
				}
			}
			kb.Grid(1, choices...)
			if v.Draft != l.Value {
				kb.Add(c.T("gov.button.review", nil), leverAddr(AddrGovConfirm, l, v.Place, v.Draft)...)
			}
			kb.Nav(c.nav(keyboards.Nav{
				BackData:    AddrGovOffice,
				RefreshData: keyboards.Data(leverAddr(AddrGovLever, l, v.Place)...),
			}))
			return c.respond(paragraphs(body(head...), rules, action), kb.Build())
		}

		var steps []presenter.Button
		for _, s := range []struct {
			key   string
			delta int64
		}{
			{"gov.button.down", -v.CoarseStep},
			{"gov.button.down", -v.FineStep},
			{"gov.button.up", v.FineStep},
			{"gov.button.up", v.CoarseStep},
		} {
			if s.delta == 0 {
				continue
			}
			next := clampValue(v.Draft+s.delta, l.Min, l.Max)
			if next == v.Draft {
				continue
			}
			amount := s.delta
			if amount < 0 {
				amount = -amount
			}
			if b, ok := keyboards.Button(c.T(s.key, map[string]any{"amount": format(amount)}), addr(next)...); ok {
				steps = append(steps, b)
			}
		}
		kb.Grid(4, steps...)

		var presets []presenter.Button
		for _, p := range []struct {
			key   string
			value int64
		}{
			{"gov.button.min", l.Min},
			{"gov.button.default", l.Default},
			{"gov.button.max", l.Max},
		} {
			if p.value == v.Draft {
				continue
			}
			if b, ok := keyboards.Button(c.T(p.key, map[string]any{"value": format(p.value)}), addr(p.value)...); ok {
				presets = append(presets, b)
			}
		}
		kb.Grid(3, presets...)

		if v.Draft != l.Value {
			kb.Add(c.T("gov.button.review", nil), leverAddr(AddrGovConfirm, l, v.Place, v.Draft)...)
		}
	}

	kb.Nav(c.nav(keyboards.Nav{
		BackData:    AddrGovOffice,
		RefreshData: keyboards.Data(leverAddr(AddrGovLever, l, v.Place)...),
	}))
	return c.respond(paragraphs(body(head...), rules, action), kb.Build())
}

// PolicyConfirm asks the office holder to confirm one change before it is
// announced.
func PolicyConfirm(c Context, v PolicyConfirmView) *presenter.Response {
	return c.withView(renderPolicyConfirm(c, v), ScreenPolicyConfirm, v)
}

func renderPolicyConfirm(c Context, v PolicyConfirmView) *presenter.Response {
	l := v.Lever
	text := paragraphs(
		c.T("gov.confirm.title", nil),
		body(
			c.T("gov.confirm.change", map[string]any{
				"lever": c.LeverName(l.Code),
				"place": c.PlaceName(v.Place),
				"old":   FormatPolicyValue(c, l.Code, l.Type, l.Value),
				"new":   FormatPolicyValue(c, l.Code, l.Type, v.NewValue),
			}),
			c.confirmNotice(l, v.VoteBy),
			c.T("gov.confirm.cooldown", map[string]any{"cooldown": FormatSpan(c, l.Cooldown)}),
		),
	)
	kb := keyboards.New()
	yes, _ := keyboards.Button(c.T("gov.button.confirm", nil), leverAddr(AddrGovSet, l, v.Place, v.NewValue)...)
	no, _ := keyboards.Button(c.T("gov.button.cancel", nil), leverAddr(AddrGovLever, l, v.Place, v.NewValue)...)
	kb.Row(yes, no)
	return c.respond(text, kb.Build())
}

// PolicyAnnounced reports a change that was just announced.
func PolicyAnnounced(c Context, v PolicyAnnouncedView) *presenter.Response {
	return c.withView(renderPolicyAnnounced(c, v), ScreenPolicyAnnounced, v)
}

func renderPolicyAnnounced(c Context, v PolicyAnnouncedView) *presenter.Response {
	l := v.Lever
	text := paragraphs(
		c.T("gov.announced.title", nil),
		c.T("gov.announced.body", map[string]any{
			"lever": c.LeverName(l.Code),
			"place": c.PlaceName(v.Place),
			"old":   c.announcedValue(l, v.Old, v.OldAllocation),
			"new":   c.announcedValue(l, v.New, v.NewAllocation),
			"when":  FormatSpan(c, v.In),
		}),
	)
	kb := keyboards.New()
	kb.Add(c.T("gov.button.my_office", nil), AddrGovOffice)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovOffice, RefreshData: keyboards.Data(leverAddr(AddrGovLever, l, v.Place)...)}))
	return c.respond(text, kb.Build())
}

// announcedValue is one side of an announced change.
func (c Context) announcedValue(l GovLever, v int64, shares map[string]int64) string {
	if l.isAllocation() {
		return c.AllocationText(shares, l.Categories)
	}
	return FormatPolicyValue(c, l.Code, l.Type, v)
}

// AllocationLine is one category of an allocation being drafted, with the
// drafts one press down and up leads to ("" where it cannot move).
type AllocationLine struct {
	Code     string
	Share    int64
	Down, Up string
}

// AllocationEditView is an allocation the viewer may change: the draft,
// encoded for the addresses, and its lines.
type AllocationEditView struct {
	Place GovPlace
	Lever GovLever
	Draft string
	Lines []AllocationLine
	// Total is what the draft allocates, bps; SpendShareBPS the share of the
	// treasury the budget spends each period, zero when not a budget.
	Total         int64
	SpendShareBPS int64
	NextChangeIn  time.Duration
	// Changed says the draft differs from the value in force.
	Changed bool
}

// AllocationEdit renders the allocation editor.
func AllocationEdit(c Context, v AllocationEditView) *presenter.Response {
	return c.withView(renderAllocationEdit(c, v), ScreenAllocationEdit, v)
}

func renderAllocationEdit(c Context, v AllocationEditView) *presenter.Response {
	l := v.Lever
	kb := keyboards.New()
	head := []string{c.T("gov.edit.title", map[string]any{"lever": c.LeverName(l.Code), "place": c.PlaceName(v.Place)})}
	head = append(head, govLeverLines(c, l)...)
	rules := body(
		c.spendShareLine(v.SpendShareBPS),
		c.T("gov.edit.notice", map[string]any{"notice": FormatSpan(c, l.Notice)}),
		c.T("gov.edit.cooldown", map[string]any{"cooldown": FormatSpan(c, l.Cooldown)}),
		c.confirmVote(l, l.ConfirmBy),
	)
	if v.NextChangeIn > 0 {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovOffice,
			RefreshData: keyboards.Data(leverAddr(AddrGovLever, l, v.Place)...)}))
		return c.respond(paragraphs(body(head...), rules,
			c.T("gov.edit.cooldown_active", map[string]any{"wait": FormatSpan(c, v.NextChangeIn)})), kb.Build())
	}
	draft := []string{c.T("gov.alloc.draft", nil)}
	for _, line := range v.Lines {
		draft = append(draft, c.T("gov.alloc.line", map[string]any{"line": c.BudgetLineName(line.Code),
			"share": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(line.Share))})}))
		var row []presenter.Button
		if line.Down != "" {
			if b, ok := keyboards.Button(c.T("gov.alloc.down", map[string]any{"line": c.BudgetLineName(line.Code)}),
				AddrGovAlloc, l.Code, v.Place.Code, line.Down); ok {
				row = append(row, b)
			}
		}
		if line.Up != "" {
			if b, ok := keyboards.Button(c.T("gov.alloc.up", map[string]any{"line": c.BudgetLineName(line.Code)}),
				AddrGovAlloc, l.Code, v.Place.Code, line.Up); ok {
				row = append(row, b)
			}
		}
		if len(row) > 0 {
			kb.Row(row...)
		}
	}
	draft = append(draft, c.T("gov.alloc.total", map[string]any{
		"total": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v.Total))}),
		"left":  c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(10000-v.Total))})}))
	if v.Changed {
		kb.Add(c.T("gov.button.review", nil), AddrGovAllocConfirm, l.Code, v.Place.Code, v.Draft)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovOffice,
		RefreshData: keyboards.Data(AddrGovAlloc, l.Code, v.Place.Code, v.Draft)}))
	return c.respond(paragraphs(body(head...), rules, body(draft...)), kb.Build())
}

// spendShareLine says what share of the treasury a budget spends.
func (c Context) spendShareLine(bps int64) string {
	if bps <= 0 {
		return ""
	}
	return c.T("gov.alloc.spend_share", map[string]any{"share": c.T("gov.percent",
		map[string]any{"value": PercentFromBPS(c, int(bps))})})
}

// AllocationConfirmView asks to confirm an allocation.
type AllocationConfirmView struct {
	Place GovPlace
	Lever GovLever
	Draft string
	New   map[string]int64
	// VoteBy is the body it goes to, empty when announced at once.
	VoteBy string
}

// AllocationConfirm asks the holder to confirm an allocation.
func AllocationConfirm(c Context, v AllocationConfirmView) *presenter.Response {
	return c.withView(renderAllocationConfirm(c, v), ScreenAllocationConfirm, v)
}

func renderAllocationConfirm(c Context, v AllocationConfirmView) *presenter.Response {
	l := v.Lever
	notice := c.confirmNotice(l, v.VoteBy)
	text := paragraphs(
		c.T("gov.confirm.title", nil),
		body(
			c.T("gov.confirm.change", map[string]any{
				"lever": c.LeverName(l.Code), "place": c.PlaceName(v.Place),
				"old": c.leverValue(l), "new": c.AllocationText(v.New, l.Categories),
			}),
			notice,
			c.T("gov.confirm.cooldown", map[string]any{"cooldown": FormatSpan(c, l.Cooldown)}),
		),
	)
	kb := keyboards.New()
	yes, _ := keyboards.Button(c.T("gov.button.confirm", nil), AddrGovAllocSet, l.Code, v.Place.Code, v.Draft)
	no, _ := keyboards.Button(c.T("gov.button.cancel", nil), AddrGovAlloc, l.Code, v.Place.Code, v.Draft)
	kb.Row(yes, no)
	return c.respond(text, kb.Build())
}

// GovHistory renders one page of a city's public record of changes.
func GovHistory(c Context, v GovHistoryView) *presenter.Response {
	return c.withView(renderGovHistory(c, v), ScreenGovHistory, v)
}

func renderGovHistory(c Context, v GovHistoryView) *presenter.Response {
	blocks := []string{c.T("gov.history.title", map[string]any{"city": c.PlaceName(v.City)})}
	if len(v.Entries) == 0 {
		blocks = append(blocks, c.T("gov.history.empty", nil))
	}
	for _, e := range v.Entries {
		lines := []string{
			c.T("gov.history.change", map[string]any{
				"lever": c.LeverName(e.Lever),
				"place": c.PlaceName(e.Place),
				"old":   FormatPolicyValue(c, e.Lever, e.Type, e.Old),
				"new":   FormatPolicyValue(c, e.Lever, e.Type, e.New),
			}),
			c.T("gov.history.by", map[string]any{
				"office": c.OfficeName(e.Office),
				"player": c.govPlayer(e.By),
				"ago":    FormatSpan(c, e.Ago),
			}),
		}
		if e.EffectiveIn > 0 {
			lines = append(lines, c.T("gov.history.effective_in", map[string]any{"when": FormatSpan(c, e.EffectiveIn)}))
		} else {
			lines = append(lines, c.T("gov.history.effective_since", map[string]any{"when": FormatSpan(c, -e.EffectiveIn)}))
		}
		blocks = append(blocks, body(lines...))
	}
	blocks = append(blocks, pageIndicator(c, v.Page, v.Pages))

	kb := keyboards.New()
	prefix := keyboards.Data(AddrGovHistory, v.City.Code)
	kb.Nav(c.nav(keyboards.Nav{
		Prefix:   prefix,
		Page:     v.Page,
		HasPrev:  pageOrOne(v.Page) > 1,
		HasNext:  pageOrOne(v.Page) < v.Pages,
		BackData: keyboards.Data(AddrGovCity, v.City.Code),
	}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// PolicyRefused renders a governance refusal, with the way back to where the
// player came from.
func PolicyRefused(c Context, v PolicyRefusalView) *presenter.Response {
	return c.withView(renderPolicyRefused(c, v), ScreenPolicyRefused, v)
}

func renderPolicyRefused(c Context, v PolicyRefusalView) *presenter.Response {
	key, args, ok := governanceRefusal(c, v.Err, v.Lever, v.Now)
	if !ok {
		return Error(c, v.Err)
	}
	kb := keyboards.New()
	back := AddrGovOffice
	if v.Lever != nil && v.Place != nil {
		back = keyboards.Data(leverAddr(AddrGovLever, *v.Lever, *v.Place)...)
		kb.Add(c.T("gov.button.my_office", nil), AddrGovOffice)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T(key, args), kb.Build())
}

// IsGovernanceRefusal reports whether err is one of the governance refusals
// these screens have a sentence for.
func IsGovernanceRefusal(err error) bool {
	_, _, ok := governanceRefusal(Context{}, err, nil, time.Time{})
	return ok
}

// governanceSentinels maps every governance refusal that needs no numbers to
// its sentence. The ones that carry numbers are handled in governanceRefusal.
var governanceSentinels = []struct {
	target error
	key    string
}{
	{application.ErrUnknownLever, "gov.refusal.unknown_lever"},
	{application.ErrJurisdictionNotFound, "gov.refusal.unknown_place"},
	{application.ErrWrongJurisdiction, "gov.refusal.wrong_place"},
	{application.ErrLeverKindUnsupported, "gov.refusal.unsupported"},
	{application.ErrInvalidAllocation, "gov.refusal.invalid_allocation"},
	{application.ErrOfficeNotFound, "gov.refusal.office_not_found"},
	{application.ErrOfficeOccupied, "gov.refusal.office_occupied"},
	{application.ErrOfficeVacant, "gov.refusal.office_vacant"},
	{application.ErrAlreadyHoldsSeat, "gov.refusal.already_holds"},
	{application.ErrIncompatibleOffices, "gov.refusal.incompatible"},
}

// governanceRefusal picks the sentence for a governance sentinel, or reports
// that err is none. lever, when known, puts the bounds in the lever's unit;
// now, when set, turns a cooldown into a wait.
func governanceRefusal(c Context, err error, lever *GovLever, now time.Time) (string, map[string]any, bool) {
	switch {
	case stderrors.Is(err, application.ErrNotOfficeHolder):
		office := detailString(err, "office")
		if office == "" && lever != nil {
			office = lever.HeldBy
		}
		return "gov.refusal.not_holder", map[string]any{"office": c.OfficeName(office)}, true

	case stderrors.Is(err, application.ErrPolicyRequiresConfirmation):
		return "gov.refusal.requires_confirmation", map[string]any{"office": c.OfficeName(detailString(err, "body"))}, true

	case stderrors.Is(err, application.ErrPolicyRequiresVote):
		body := detailString(err, "body")
		if body == "" && lever != nil {
			body = lever.HeldBy
		}
		return "gov.refusal.requires_vote", map[string]any{"office": c.OfficeName(body)}, true

	case stderrors.Is(err, application.ErrPolicyOutOfBounds):
		if lever == nil {
			return "gov.refusal.out_of_range_plain", nil, true
		}
		return "gov.refusal.out_of_range", map[string]any{
			"min": FormatPolicyValue(c, lever.Code, lever.Type, lever.Min),
			"max": FormatPolicyValue(c, lever.Code, lever.Type, lever.Max),
		}, true

	case stderrors.Is(err, application.ErrPolicyCooldown):
		at, ok := detailTime(err, "available_at")
		if !ok || now.IsZero() || !at.After(now) {
			return "gov.refusal.cooldown_later", nil, true
		}
		return "gov.refusal.cooldown", map[string]any{"wait": FormatSpan(c, at.Sub(now))}, true
	}
	for _, s := range governanceSentinels {
		if stderrors.Is(err, s.target) {
			return s.key, nil, true
		}
	}
	return "", nil, false
}

// clampValue keeps a proposed value inside the lever's bounds, so a step
// button never proposes what SetPolicy would refuse.
func clampValue(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// detailString reads one string detail off a classified error.
func detailString(err error, key string) string {
	var e *errors.Error
	if !stderrors.As(err, &e) {
		return ""
	}
	s, _ := e.Details[key].(string)
	return s
}

// detailTime reads one time detail off a classified error.
func detailTime(err error, key string) (time.Time, bool) {
	var e *errors.Error
	if !stderrors.As(err, &e) {
		return time.Time{}, false
	}
	t, ok := e.Details[key].(time.Time)
	return t, ok
}

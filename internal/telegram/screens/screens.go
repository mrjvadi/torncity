// Package screens turns what a use case worked out into a presentation model.
//
// A screen decides LAYOUT: which facts appear, in what order, and which
// buttons sit under them. It never decides WORDING. Every word a player reads
// is looked up in the message catalogue by key, so a rewording, a tone change
// or a new language is a file edit and a restart. tests/no_hardcoded_text_test.go
// is the gate that keeps it that way.
//
// A screen performs no I/O, holds no state and makes no decision the core has
// not already made. It receives values and returns a *presenter.Response.
//
// # Editing beats sending
//
// 17_TELEGRAM_UX.md asks for the current message to be edited wherever
// possible, and a session that answers forty presses with forty new messages
// is unreadable. Every screen therefore edits when Context carries a message
// id and sends when it does not. A press of an inline button always carries
// one; a typed command does not.
package screens

import (
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/presentation/life"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Translator resolves a message key for a language.
//
// It is declared here, at the point of use, for the same reason the handlers
// package declares its own: a screen needs one method, not a catalogue type,
// so a loaded catalogue, a hot-reloadable store and a test spy are all equally
// acceptable and none of them is imported here.
type Translator interface {
	T(lang, key string, args map[string]any) string
}

// Callback addresses.
//
// They are constants so a screen and the handler behind it cannot drift
// apart, and they are addresses and nothing more: a screen name and, where a
// list is involved, a page number. No identifier that grants anything, no
// price, no total, no "already checked" flag. The core re-validates every
// press against its own state, so the worst a forged address achieves is
// opening a screen the player could have opened anyway.
//
// AddrHome is the profile rather than a screen of its own: /start already
// routes there, so a back button pointing at it can never land on a command
// nobody serves.
const (
	AddrHome        = "player:profile.get"
	AddrProfile     = "player:profile.get"
	AddrMap         = "map:list"
	AddrTravelStart = "travel:start"
	// AddrTravelOptions is the choice of transport to one city. Pressing a
	// destination on the map opens it; nothing departs until a mode is
	// chosen there.
	AddrTravelOptions = "travel:options"
	AddrTravelStatus  = "travel:status"
	AddrSkills        = "skills:list"
	AddrSearch        = "social:search"
	AddrFriendList    = "social:friend.list"
	AddrFriendAdd     = "social:friend.add"
	AddrFriendAccept  = "social:friend.accept"
	AddrFriendView    = "social:friend.view"
	AddrFriendRemove  = "social:friend.remove"
	AddrFactionInvite = "faction:invite"
	AddrSettings      = "player:settings"
	AddrLanguageSet   = "player:language.set"
)

// lineBreak separates the lines of a body and blankLine separates its
// paragraphs. They are punctuation, not text: there is nothing here for a
// translator to change.
const (
	lineBreak = "\n"
	blankLine = "\n\n"
)

// Context is everything a screen needs that is not about the screen itself.
type Context struct {
	// Msgs is the catalogue. A nil Msgs renders keys, which is visibly
	// wrong rather than silently blank.
	Msgs Translator
	// Lang is the language the reply is written in: the player's stored
	// choice where there is one, else their Telegram client's (see
	// handlers.RenderLanguage). Empty is fine: the catalogue falls back.
	Lang string
	// MessageID is the message this response should replace. Zero means
	// there is nothing to edit, so the screen sends.
	MessageID int64
	// Shared says the screen is rendered where others can see it: a group
	// (envelope.Metadata.InGroup). A shared screen leaves the player's own
	// money out — the cash they carry, their bank balance — and shows
	// everything else.
	Shared bool
	// Zone is the time zone a clock time is shown in (FormatClock). Nil
	// means the process default, SetDefaultZone — the configured
	// player.default_timezone — so a player is never shown UTC.
	Zone *time.Location
}

// T resolves one key in this context's language.
//
// An integer argument is written as a number of this language before it is
// substituted — its digits, its thousands separator — so a screen may hand
// over a level, a page or a count as it stands and still never put ASCII
// digits in the middle of a Persian sentence. See numerals.go.
func (c Context) T(key string, args map[string]any) string {
	if c.Msgs == nil {
		return key
	}
	return c.Msgs.T(c.Lang, key, c.localiseArgs(args))
}

// CityName resolves a city's display name in this context's language.
//
// A city's NAME is content keyed on its code, like a skill's: "city.<code>"
// in the catalogue, so a Persian player reads a Persian name rather than the
// one cities.yml was authored with. name is that authored name, and it is the
// fallback when the catalogue has no entry for the code — a city added to
// content before anyone translated it reads in its authored form instead of
// as a raw key. An empty code means the caller has no code, so name is used
// as it stands.
//
// The catalogue signals a missing key by returning the key itself (see
// i18n.Catalog.T), and that is the check used here rather than Has: it
// honours the same language fallback every other line on the screen does,
// and it works through any Translator, including a hot-reloadable store.
func (c Context) CityName(code, name string) string {
	if code == "" {
		return name
	}
	key := cityKeyPrefix + code
	if text := c.T(key, nil); text != key {
		return text
	}
	return name
}

// cityKeyPrefix is the catalogue namespace that holds city display names.
const cityKeyPrefix = "city."

// respond edits when there is a message to edit and sends otherwise.
func (c Context) respond(text string, kb *presenter.Keyboard) *presenter.Response {
	if c.MessageID != 0 {
		return presenter.Edit(c.MessageID, text, kb)
	}
	return presenter.Message(text, kb)
}

// nav fills in the three labels every navigation block shares, so no screen
// can ship with two of the three that 17_TELEGRAM_UX.md requires.
func (c Context) nav(n keyboards.Nav) keyboards.Nav {
	n.PrevText = c.T("button.previous", nil)
	n.NextText = c.T("button.next", nil)
	n.BackText = c.T("button.back", nil)
	n.RefreshText = c.T("button.refresh", nil)
	if n.BackData == "" {
		n.BackData = AddrHome
	}
	return n
}

// body joins the lines of a message, dropping the empty ones so a missing
// optional line does not leave a gap in the middle of a screen.
func body(lines ...string) string {
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, lineBreak)
}

// paragraphs joins whole blocks of a message, dropping the empty ones.
func paragraphs(blocks ...string) string {
	kept := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block != "" {
			kept = append(kept, block)
		}
	}
	return strings.Join(kept, blankLine)
}

// FormatDuration renders a duration through the catalogue, in the largest
// units that read naturally: "2h 15m", "2h", "35m", or "40s" under a minute.
//
// The numbers are computed here; the shape of the phrase around them is not,
// so it comes from format.duration_* and a translator can put the units
// wherever that language puts them. A zero or negative duration reads as one
// second rather than as "0m": a screen showing a duration is saying something
// is still to come, and a caller that means "done" has its own message.
func FormatDuration(c Context, d time.Duration) string {
	if d < time.Second {
		d = time.Second
	}
	if d < time.Minute {
		return c.T("format.duration_s", map[string]any{"seconds": int(d / time.Second)})
	}
	// Round up to the minute so a journey never reads as shorter than it is.
	minutesTotal := int((d + time.Minute - 1) / time.Minute)
	hours, minutes := minutesTotal/60, minutesTotal%60
	switch {
	case hours == 0:
		return c.T("format.duration_m", map[string]any{"minutes": minutes})
	case minutes == 0:
		return c.T("format.duration_h", map[string]any{"hours": hours})
	}
	return c.T("format.duration_hm", map[string]any{"hours": hours, "minutes": minutes})
}

// Error turns a failure into the screen a player sees.
//
// # Why this matches on identity and not on class
//
// internal/shared/errors matches with errors.Is BY CODE: two errors of the
// same class are the same kind of failure as far as errors.Is is concerned.
// That is right for deciding whether to retry and wrong for choosing a
// sentence, because ErrCityNotFound, ErrNoActiveTravel, ErrSkillNotFound and
// ErrNotFriends are all NOT_FOUND and errors.Is cannot tell them apart. So the
// application sentinels are matched by walking the chain looking for the very
// value, and only the plain domain sentinels — which are ordinary errors.New
// values with no Code — are matched with errors.Is.
//
// The fallback is by class, so a failure nobody anticipated still produces a
// sentence rather than a blank bubble.
func Error(c Context, err error) *presenter.Response {
	return ErrorFrom(c, life.ErrorOf(err))
}

// ErrorFrom words an error the core classified (life.ErrorOf): its code is the
// catalogue key of the sentence, its arguments are data that this edge writes
// in the language's own numerals and units.
func ErrorFrom(c Context, v ErrorView) *presenter.Response {
	key, args := errorSentence(c, v)
	kb := keyboards.New()
	// Where a refusal has an obvious next step, that step is one press away
	// instead of described and left for the player to find.
	if next, ok := errorNextStep[key]; ok {
		if btn, ok := keyboards.Button(c.T(next.label, nil), next.addr); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return asError(c.respond(c.T(key, args), kb.Build()))
}

// errorSentence is the key and the placeholder values of an error's sentence.
func errorSentence(c Context, v ErrorView) (string, map[string]any) {
	n := func(k string) int64 { return rawInt(v.Args[k]) }
	switch v.Code {
	case "error.not_enough_energy":
		needed, current := n("needed"), n("current")
		return v.Code, map[string]any{
			"needed":  FormatNumber(c, needed),
			"current": FormatNumber(c, current),
			"wait":    FormatDuration(c, energyWait(needed-current)),
		}
	case "error.cooldown":
		return v.Code, map[string]any{"wait": FormatDuration(c, time.Duration(n("seconds"))*time.Second)}
	case "bank.error.below_minimum":
		return v.Code, map[string]any{"min": FormatMoney(c, n("min"))}
	case "bank.error.above_maximum":
		return v.Code, map[string]any{"max": FormatMoney(c, n("max"))}
	case "bank.error.not_enough_cash":
		return v.Code, map[string]any{"available": FormatMoney(c, n("available"))}
	case "bank.error.not_enough_in_bank":
		return v.Code, map[string]any{"available": FormatMoney(c, n("available")), "needed": FormatMoney(c, n("needed"))}
	case "crime.error.in_jail", "health.error.hospitalised":
		return v.Code, map[string]any{"remaining": FormatDuration(c, time.Duration(n("remaining_seconds"))*time.Second)}
	case "gov.refusal.not_holder", "gov.refusal.requires_confirmation", "gov.refusal.requires_vote":
		office, _ := v.Args["office"].(string)
		return v.Code, map[string]any{"office": c.OfficeName(office)}
	}
	return v.Code, nil
}

// rawInt reads a whole number out of a view's argument: it crossed the bus as JSON, so it may be any numeric type.
func rawInt(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// errorNextStep maps a refusal to the one screen that resolves it. A refusal
// missing from here gets the back button alone.
var errorNextStep = map[string]struct{ label, addr string }{
	"error.already_travelling":      {"button.journey", AddrTravelStatus},
	"error.at_work":                 {"job.button.my_job", AddrJobStatus},
	"travel.none":                   {"button.map", AddrMap},
	"travel.same_city":              {"button.map", AddrMap},
	"travel.no_route":               {"button.map", AddrMap},
	"travel.mode_unavailable":       {"button.map", AddrMap},
	"error.city_not_found":          {"button.map", AddrMap},
	"error.skill_not_found":         {"button.skills", AddrSkills},
	"error.not_friends":             {"button.social", AddrFriendList},
	"error.already_friends":         {"button.social", AddrFriendList},
	"error.unsupported_language":    {"button.settings", AddrSettings},
	"bank.error.not_in_city":        {"button.bank", AddrBank},
	"bank.error.not_enough_cash":    {"button.bank", AddrBank},
	"bank.error.not_enough_in_bank": {"button.bank", AddrBank},
	"bank.error.insufficient":       {"button.bank", AddrBank},
	"bank.error.payee_not_found":    {"button.find_player", AddrSearch},
	"bank.error.invalid_amount":     {"button.bank", AddrBank},
	"bank.error.below_minimum":      {"button.bank", AddrBank},
	"bank.error.above_maximum":      {"button.bank", AddrBank},
}

// energyWait is the longest a player short of missing energy has to wait for
// it: whole regeneration ticks, straight from the domain's constants. It is
// an upper bound, because the current tick may already be part-way through,
// which is why the message says "within".
func energyWait(missing int64) time.Duration {
	amount := int64(player.EnergyRegenAmount)
	if amount <= 0 || missing <= 0 {
		return 0
	}
	ticks := (missing + amount - 1) / amount
	return time.Duration(ticks) * player.EnergyRegenInterval
}

// detailInt reads one structured detail off a classified error, or zero.
// Details are metadata, so only numbers a message needs are read back out.
func detailInt(err error, key string) int64 {
	var e *errors.Error
	if !stderrors.As(err, &e) {
		return 0
	}
	switch v := e.Details[key].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

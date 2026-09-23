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
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
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
	AddrHome         = "player:profile.get"
	AddrProfile      = "player:profile.get"
	AddrMap          = "map:list"
	AddrTravelStart  = "travel:start"
	AddrTravelStatus = "travel:status"
	AddrSkills       = "skills:list"
	AddrSearch       = "social:search"
	AddrFriendList   = "social:friend.list"
	AddrFriendAdd    = "social:friend.add"
	AddrFriendAccept = "social:friend.accept"
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
	// Lang is the player's language as it arrived on the request. Empty is
	// fine: the catalogue falls back.
	Lang string
	// MessageID is the message this response should replace. Zero means
	// there is nothing to edit, so the screen sends.
	MessageID int64
}

// T resolves one key in this context's language.
func (c Context) T(key string, args map[string]any) string {
	if c.Msgs == nil {
		return key
	}
	return c.Msgs.T(c.Lang, key, args)
}

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

// FormatNumber renders an integer with "," between each group of three
// digits, so 12500 reads as 12,500. Every count a player compares — XP,
// distance, energy — goes through it.
func FormatNumber(n int64) string {
	if n < 0 {
		return "-" + FormatNumber(-n)
	}
	digits := strconv.FormatInt(n, 10)
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteString(thousandsSeparator)
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// thousandsSeparator is punctuation, not text: both shipped languages use
// ASCII digits, and "," reads correctly in a right-to-left line too.
const thousandsSeparator = ","

// PercentFromBPS renders a basis-point rate as a percentage.
//
// Integer arithmetic throughout, like world.City.TaxOn: 750 bps is "7.5" and
// 1000 bps is "10", with no float anywhere near a number a player compares
// two cities by.
func PercentFromBPS(bps int) string {
	if bps < 0 {
		bps = 0
	}
	whole := bps / 100
	frac := bps % 100
	switch {
	case frac == 0:
		return strconv.Itoa(whole)
	case frac%10 == 0:
		return strconv.Itoa(whole) + "." + strconv.Itoa(frac/10)
	default:
		return strconv.Itoa(whole) + "." + pad2(frac)
	}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
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
	if err == nil {
		return c.respond(c.T("error.internal", nil), nil)
	}

	key, args := errorMessage(c, err)
	kb := keyboards.New()
	// Where a refusal has an obvious next step, that step is one press away
	// instead of described and left for the player to find.
	if next, ok := errorNextStep[key]; ok {
		if btn, ok := keyboards.Button(c.T(next.label, nil), next.addr); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T(key, args), kb.Build())
}

// errorNextStep maps a refusal to the one screen that resolves it. A refusal
// missing from here gets the back button alone.
var errorNextStep = map[string]struct{ label, addr string }{
	"error.already_travelling": {"button.journey", AddrTravelStatus},
	"travel.none":              {"button.map", AddrMap},
	"travel.same_city":         {"button.map", AddrMap},
	"travel.no_route":          {"button.map", AddrMap},
	"travel.unknown_speed":     {"button.map", AddrMap},
	"error.city_not_found":     {"button.map", AddrMap},
	"error.skill_not_found":    {"button.skills", AddrSkills},
	"error.not_friends":        {"button.social", AddrFriendList},
	"error.already_friends":    {"button.social", AddrFriendList},
}

// errorMessage picks the key and the placeholder values for a failure.
func errorMessage(c Context, err error) (string, map[string]any) {
	// Identity first: see Error for why class matching cannot do this.
	for _, s := range applicationSentinels {
		if identical(err, s.target) {
			return s.key, nil
		}
	}

	switch {
	case stderrors.Is(err, player.ErrNotEnoughEnergy):
		needed, current := detailInt(err, "needed"), detailInt(err, "current")
		if needed <= current {
			// Without the two numbers there is no honest wait to quote.
			return "error.not_enough_energy_later", nil
		}
		return "error.not_enough_energy", map[string]any{
			"needed":  FormatNumber(needed),
			"current": FormatNumber(current),
			"wait":    FormatDuration(c, energyWait(needed-current)),
		}
	case stderrors.Is(err, travel.ErrSameCity):
		return "travel.same_city", nil
	case stderrors.Is(err, world.ErrNoRoute), stderrors.Is(err, world.ErrUnknownCity):
		return "travel.no_route", nil
	case stderrors.Is(err, travel.ErrUnknownSpeed), stderrors.Is(err, travel.ErrSpeedNotPriced):
		return "travel.unknown_speed", nil
	}

	switch errors.CodeOf(err) {
	case errors.CodeNotFound:
		return "error.not_found", nil
	case errors.CodeInvalidInput:
		return "error.invalid_input", nil
	case errors.CodeConflict:
		return "error.conflict", nil
	case errors.CodeRateLimited:
		return "error.rate_limited", nil
	case errors.CodeCooldown:
		return "error.cooldown", map[string]any{
			"wait": FormatDuration(c, time.Duration(detailInt(err, "seconds"))*time.Second),
		}
	case errors.CodeUnauthorized:
		return "error.unauthorized", nil
	}
	return "error.internal", nil
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

// applicationSentinels maps each phase 1 sentinel to its sentence. Order is
// irrelevant because identity matching cannot produce a false positive.
var applicationSentinels = []struct {
	target error
	key    string
}{
	{application.ErrCityNotFound, "error.city_not_found"},
	{application.ErrNoActiveTravel, "travel.none"},
	{application.ErrAlreadyTravelling, "error.already_travelling"},
	{application.ErrSkillNotFound, "error.skill_not_found"},
	{application.ErrNotFriends, "error.not_friends"},
	{application.ErrAlreadyFriends, "error.already_friends"},
	{application.ErrPlayerNotFound, "error.player_not_found"},
}

// identical reports whether target appears anywhere in err's chain as that
// exact value, ignoring the Is method entirely.
func identical(err, target error) bool {
	for e := err; e != nil; e = stderrors.Unwrap(e) {
		if e == target {
			return true
		}
	}
	return false
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

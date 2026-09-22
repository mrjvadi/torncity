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

// FormatDuration renders a duration through the catalogue.
//
// The hours and minutes are numbers; the shape of the sentence around them is
// not, so it comes from format.duration and a translator can put the units
// wherever that language puts them.
func FormatDuration(c Context, d time.Duration) string {
	if d < 0 {
		d = 0
	}
	hours := int(d / time.Hour)
	minutes := int((d % time.Hour) / time.Minute)
	if hours == 0 && minutes == 0 && d > 0 {
		// Under a minute still has to read as some time remaining rather
		// than as none at all.
		minutes = 1
	}
	return c.T("format.duration", map[string]any{
		"hours":   hours,
		"minutes": minutes,
	})
}

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
	kb := keyboards.New().Nav(c.nav(keyboards.Nav{BackData: AddrHome})).Build()
	return c.respond(c.T(key, args), kb)
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
		return "error.not_enough_energy", map[string]any{
			"needed":  detailInt(err, "needed"),
			"current": detailInt(err, "current"),
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
		return "error.cooldown", map[string]any{"seconds": detailInt(err, "seconds")}
	case errors.CodeUnauthorized:
		return "error.unauthorized", nil
	}
	return "error.internal", nil
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

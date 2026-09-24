// Package routing turns a Telegram update into a command name and a payload.
//
// That sentence is the whole contract. MASTER_PROMPT section 73 forbids the
// gateway from running business logic, and section 13 says a callback must be
// translated, not executed: market:buy:123 becomes the command market.buy and
// the game core is the one that checks ownership, availability, the wallet and
// the cooldown. Nothing in this package knows what a market, a job or an item
// is; it knows how a command is spelled, and — from the game's own table, see
// below — which spellings the game answers.
//
// # The parsing scheme
//
// Text commands:
//
//	/<domain> <action> [arg ...]
//
// The first token is the slash-command and names the domain; the token after
// it names the action; everything that follows is a positional argument. So
// "/job apply developer" is domain "job", action "apply", one argument
// "developer", which makes the command "job.apply" with the payload
// {"role": "developer"}. A trailing @botname on the
// slash-command is stripped, because Telegram appends it in groups
// ("/job@torncity_bot apply"). Tokens are lower-cased.
//
// Callback data:
//
//	<domain>:<action>[:arg ...]
//
// Same shape with colons, because callback data is limited to 64 bytes and
// every character counts. So "market:buy:123" is the command "market.buy" with
// one argument, "123".
//
// # Shortcuts
//
// Players do not type "/player profile.get". The shortcuts table maps the
// slash-commands people naturally type onto the commands they mean, in two
// ways, and nowhere else in the gateway knows about either:
//
//   - on its own: "/map" is map.list, "/settings" is player.settings, and
//     /start — which Telegram itself sends as the first message of every chat
//     — is the profile.
//   - followed by words that are not one of its commands: "/social @mrjvadi"
//     is social.search for "@mrjvadi", while "/social search @mrjvadi" and
//     "/social friend.list" still mean exactly what they say. The words
//     become the command's positional arguments, so "/start <payload>" deep
//     links arrive as arguments of the profile, and "/map 2" is page two.
//
// /find is the search and nothing else: "/find K7Q2M9A" searches, and a bare
// /find asks for a search with no query, which the game answers by explaining
// what can be searched. A bare /social stays the friend list.
//
// # How positional arguments become a payload
//
// Payload keys come from argNames, a table that gives a command's positional
// arguments their names: job.apply names its first argument "role", so
// "/job apply developer" yields {"role":"developer"}. The table holds names
// only. It encodes no rule, no validation and no default; a wrong name costs a
// rejected command from the domain handler, never a wrong game outcome.
//
// A command that is absent from argNames still parses. Its arguments arrive
// under the key "args" as a []string, and so do any arguments beyond the names
// the table lists.
//
// A command listed in joinRest is the exception: its LAST named argument takes
// every remaining word, joined by single spaces. social.search is the one
// such command. Its query is one identifier, and splitting "/social ali reza"
// into a query of "ali" and a page of "reza" answered a question nobody
// asked; joined, the game sees "ali reza" whole and can say plainly that it
// is not something a player can be found by.
//
// # Only commands the game serves are routed
//
// Parse answers "is this spelled like a command". Route, which is what the
// gateway calls, also answers "does the game serve it, from a player" by
// checking internal/commands — the same table cmd/game subscribes from, so
// there is no second copy here to drift. An unknown command is refused with
// ErrUnknownCommand and is never published.
//
// This package once argued the opposite: that a whitelist here would be a
// stale copy, and that an unknown command would surface downstream as a
// no-responder. The second half is false on JetStream. The command stream is
// a work queue, a publish to a subject nobody consumes succeeds, and the
// player who typed "/social mrjvadi" got no reply at all. The first half is
// answered by reading the game's own table instead of keeping a copy.
//
// # Callback data is untrusted input
//
// Callback data is a string the client sends back to us. Anyone can craft one,
// it is not signed, and Telegram neither hides it nor validates it. Therefore:
//
//   - NEVER put anything sensitive in callback data. No token, no cost, no
//     wallet total, no server-side decision, no "already authorised" flag.
//     Treat every byte of it as if a player typed it, because a player can.
//   - What it carries is an addressing hint: which screen, which row. The
//     domain re-checks that the player may act on that row (section 13).
//
// Parse enforces Telegram's 64-byte limit and a conservative character set
// (see safeCallbackByte) so that malformed or hostile data is rejected at the
// edge rather than becoming a strange NATS subject.
package routing

import (
	"errors"
	"regexp"
	"strings"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/telegram/client"
)

// MaxCallbackDataBytes is Telegram's documented limit on callback_data:
// 1-64 bytes. It is measured in bytes, not runes, which matters the moment a
// Persian or emoji character ends up in a button.
const MaxCallbackDataBytes = 64

// Parse failures. They are plain sentinels so errors.Is can tell them apart;
// internal/shared/errors matches on Code, which would make every one of these
// equal to every other INVALID_INPUT error. A caller answering a player maps
// them to errors.InvalidInput with wording from the i18n layer.
var (
	// ErrNoCommand means the update carries nothing routable: a photo with no
	// caption, an edited message, an update type phase 0 ignores.
	ErrNoCommand = errors.New("routing: update carries no command")

	// ErrNotACommand means the message was plain text, not a slash command.
	// Phase 0 has no free-text input, so this is a normal, frequent outcome.
	ErrNotACommand = errors.New("routing: message text is not a slash command")

	// ErrMissingAction means a domain arrived with no action after it
	// ("/job"), and it is not one of the bare aliases.
	ErrMissingAction = errors.New("routing: command has no action")

	// ErrMalformedCommand means the domain or action contains something that
	// is not a legal subject token.
	ErrMalformedCommand = errors.New("routing: command is not a valid domain.action")

	// ErrEmptyCallbackData means a button sent an empty data field.
	ErrEmptyCallbackData = errors.New("routing: callback data is empty")

	// ErrCallbackDataTooLong means the data exceeded MaxCallbackDataBytes.
	// Telegram would normally have refused to send such a button, so this is
	// either a client that is not Telegram or a bug in our own keyboards.
	ErrCallbackDataTooLong = errors.New("routing: callback data exceeds Telegram's 64-byte limit")

	// ErrCallbackDataUnsafe means the data contained a byte outside the safe
	// set. See safeCallbackByte for what is allowed and why.
	ErrCallbackDataUnsafe = errors.New("routing: callback data contains characters outside the safe set")

	// ErrUnknownCommand means the update is spelled like a command but names
	// none the game serves to players. See Route.
	ErrUnknownCommand = errors.New("routing: the game serves no such command")
)

// tokenPattern is what a single subject token may look like.
//
// It is intentionally narrower than "anything without a dot": subjects.Grammar
// accepts [a-z_]+ per segment, so a command with a digit or a dash in it would
// build a subject that the grammar rejects, and the failure would appear far
// from its cause. Rejecting it here makes the error land on the input that
// caused it.
var tokenPattern = regexp.MustCompile(`^[a-z_]+$`)

// shortcut is what one slash-command means when it is not followed by one of
// its own commands. See the package doc.
type shortcut struct {
	// Bare is the command the slash-command means on its own.
	Bare string
	// Words is the command the slash-command means when words follow it that
	// do not name one of its commands; the words are that command's
	// arguments. Empty means such words are read as an action, as usual.
	Words string
}

// shortcuts maps the slash-commands players naturally type to the commands
// they mean.
//
// Every command named here must be one the game serves to players; a test
// holds this table to internal/commands.
var shortcuts = map[string]shortcut{
	// Telegram sends /start as the first message of every chat, and a deep
	// link arrives as "/start <payload>".
	"start":    {Bare: "player.profile.get", Words: "player.profile.get"},
	"profile":  {Bare: "player.profile.get"},
	"settings": {Bare: "player.settings"},
	"skills":   {Bare: "skills.list"},
	// "/map" is the map of the player's own city; "/map bazaar" walks to
	// that place.
	"map":    {Bare: "map.list", Words: "place.go"},
	"social": {Bare: "social.friend.list", Words: "social.search"},
	"find":   {Bare: "social.search", Words: "social.search"},
	// The bank: "/bank" is the bank screen, "/bank deposit 5000" says what it
	// says; "/pay @ali" opens a payment to that player and
	// "/pay @ali 5000 card" asks to confirm one.
	"bank": {Bare: "bank.show"},
	"pay":  {Bare: "bank.pay", Words: "bank.pay"},
	// Player-held offices: "/city" is the player's own city, "/city ostmarch"
	// another one; "/office" is the office holder's screen.
	"city":   {Bare: "gov.city", Words: "gov.city"},
	"office": {Bare: "gov.office"},
	// Work and study: "/job" is the player's job, "/jobs" the openings in
	// their city, "/study" the courses.
	"job":   {Bare: "job.status"},
	"jobs":  {Bare: "job.list"},
	"study": {Bare: "education.list"},
	// Crime: "/crime" is the hub and "/crime pickpocketing" commits that
	// crime where the player stands — it never names a victim; "/jail" is
	// the jail screen.
	"crime": {Bare: "crime.hub", Words: "crime.commit"},
	"jail":  {Bare: "crime.jail"},
	// Goods: "/bag" (or "/inventory") is what the player carries; "/shop"
	// the shops of their city and "/shop pharmacy" one of them; "/market"
	// the market and "/market bread" one good's book; "/auction" the
	// auction house and "/auction 12" one auction.
	"inventory": {Bare: "inventory.show"},
	"bag":       {Bare: "inventory.show"},
	"shop":      {Bare: "shop.list", Words: "shop.view"},
	"market":    {Bare: "market.list", Words: "market.book"},
	"auction":   {Bare: "auction.list", Words: "auction.view"},
}

// argNames names the positional arguments of a command, in order.
//
// Adding a command here is a one-line change and requires no code. Leaving one
// out is also fine: its arguments arrive under "args". Names must match what
// the domain handler decodes, which is why the payload contract belongs to
// internal/application/dto and this table only mirrors it.
var argNames = map[string][]string{
	"job.apply":  {"role"},
	"market.buy": {"id"},

	// Phase 1: the player, the world and the social graph.
	//
	// travel.start names its destination by city CODE rather than by database
	// id, because a code is the stable authored key a content file, a button
	// and a typed command all agree on, and it is short enough to leave room
	// inside the 64-byte callback budget. travel.options is the choice of
	// transport to that city ("/travel options berlin", or the map's button);
	// travel.start departs by one mode at a fare the player was shown:
	// "travel:start:berlin:train:1200". The fare is a ceiling the game core
	// re-checks, never a price it trusts; without a mode or a fare,
	// travel.start answers with the choice of transport.
	"travel.options": {"city"},
	"travel.start":   {"city", "mode", "max", "method"},
	// travel.status takes nothing. It is listed anyway so that this table
	// reads as the set of commands phase 1 speaks rather than as the subset
	// of them that happens to have arguments.
	"travel.status": {},
	"skills.list":   {"page"},
	// social.search takes one argument, the query, and every word after
	// the command is part of it (see joinRest). A search names one player
	// exactly, so there is no page.
	"social.search":     {"query"},
	"social.friend.add": {"player"},
	// social.friend.list and social.friend.accept complete the pair; both
	// route through SplitCommand as domain "social" with the two-token
	// action "friend.accept".
	"social.friend.accept": {"player"},
	"social.friend.list":   {"page"},
	"map.list":             {"page"},
	"map.cities":           {"page"},
	"place.go":             {"place"},

	// The profile takes nothing. A /start deep-link payload still arrives,
	// under "args", for whoever reads it one day.
	"player.profile.get": {},

	// Settings. player.settings takes nothing; player.language.set names the
	// language by its catalogue code ("/player language.set en", or the
	// button "player:language.set:en"). The game core checks the code
	// against the languages it actually ships.
	"player.settings":     {},
	"player.language.set": {"lang"},

	// The bank. An amount is whole minor units as typed ("5000", "12,500");
	// the game core parses and bounds it. nonce is the one-time token a
	// button carries so a second press of it is a replay, never a second
	// payment; a typed command has none. A payee ("to") is anything
	// /social finds a player by: @username, player code or Telegram ID.
	"bank.show":     {},
	"bank.deposit":  {"amount", "nonce"},
	"bank.withdraw": {"amount", "nonce"},
	// origin is the group a payment was started in (a negative chat id),
	// carried by the buttons so the group can be told it was made.
	"bank.pay":      {"to", "amount", "method", "origin"},
	"bank.pay.send": {"to", "amount", "method", "nonce", "origin"},

	// Player-held offices. A lever is addressed by its content code and the
	// code of the place it is set in; value is a proposed value, which the
	// game core checks against the lever's bounds through SetPolicy.
	"gov.city":    {"city"},
	"gov.history": {"city", "page"},
	"gov.office":  {},
	"gov.lever":   {"lever", "place", "value"},
	"gov.confirm": {"lever", "place", "value"},
	"gov.set":     {"lever", "place", "value"},

	// Work and study. A career and a course are named by their content code
	// (jobs.yml, education.yml). job.apply's argument keeps the name "role"
	// it has always had here. job.quit asks first; "job:quit:yes" confirms.
	"job.status":       {},
	"job.list":         {"page"},
	"job.view":         {"role"},
	"job.work":         {},
	"job.promote":      {},
	"job.quit":         {"confirm"},
	"education.list":   {"page"},
	"education.view":   {"course"},
	"education.enroll": {"course", "method"},

	// Crime. A crime, a category are named by their content code
	// (crimes.yml); nonce is a button's one-time token, so a second press
	// is a replay, never a second attempt. crime.report names the theft by
	// its attempt id, from the victim's notice; "crime:report:<id>:yes"
	// files it.
	"crime.hub":    {},
	"crime.list":   {"category", "page"},
	"crime.view":   {"crime"},
	"crime.commit": {"crime", "nonce"},
	"crime.record": {},
	"crime.jail":   {},
	"crime.bail":   {"nonce", "method"},
	"crime.report": {"crime", "confirm"},
	"crime.cases":  {},

	// Goods. A good is named by its content code (items.yml) or, for a
	// unique piece, by its serial; a shop by its code (shops.yml); an order
	// or an auction by its public number. nonce is a button's one-time
	// token; method is how a charge is paid, cash or card.
	"inventory.show": {"page"},
	"inventory.item": {"item"},
	"inventory.use":  {"item", "nonce"},
	"inventory.give": {"item", "nonce", "to"},
	"inventory.drop": {"item", "confirm", "nonce"},
	"shop.list":      {},
	"shop.view":      {"shop"},
	"shop.buy":       {"shop", "item", "qty", "method", "nonce"},
	"shop.offers":    {"item"},
	"shop.sell":      {"shop", "item"},
	"market.list":    {"page"},
	"market.book":    {"item"},
	"market.order":   {"side", "item", "qty", "price", "nonce", "method"},
	"market.cancel":  {"no"},
	"market.mine":    {"page"},
	"auction.list":   {},
	"auction.view":   {"no"},
	"auction.new":    {"item", "reserve", "duration", "nonce"},
	"auction.bid":    {"no", "amount", "nonce", "method"},
	"auction.mine":   {},
}

// landings name the screen a domain opens on when one of its commands cannot
// be replayed as it stands (it needs arguments a link does not carry), for
// the domains no bare shortcut already opens.
var landings = map[string][]string{
	"travel": {"map.cities", "map.list"},
}

// Landing is the command a deep link replays for command: the command itself
// when it takes no arguments, else the screen its domain opens on — "/bank"
// for a deposit, the map for a journey — else the profile.
func Landing(command string) string {
	if names, ok := argNames[command]; ok && len(names) == 0 {
		return command
	}
	domain, _, _ := strings.Cut(command, ".")
	for _, l := range landings[domain] {
		if commands.FromPlayerCommand(l) {
			return l
		}
	}
	if s, ok := shortcuts[domain]; ok && s.Bare != "" {
		return s.Bare
	}
	for _, s := range shortcuts {
		if s.Bare != "" && strings.HasPrefix(s.Bare, domain+".") {
			return s.Bare
		}
	}
	return "player.profile.get"
}

// joinRest lists the commands whose last named argument takes every word
// that follows, joined into one string. See the package doc.
var joinRest = map[string]bool{
	"social.search": true,
}

// Route is Parse restricted to the commands the game serves to players, and it
// is the only entry point the gateway may publish from.
//
// A command that parses but is not in internal/commands, or is in it only as
// a scheduled command (travel.arrive), is refused with ErrUnknownCommand. See
// the package doc for why this check cannot be left to the broker.
func Route(update client.Update) (command string, payload map[string]any, err error) {
	command, payload, err = Parse(update)
	if err != nil {
		return "", nil, err
	}
	if !commands.FromPlayerCommand(command) {
		return "", nil, ErrUnknownCommand
	}
	return command, payload, nil
}

// NeedsHelp reports whether a routing failure is a player trying to say
// something the game did not understand, and so deserves an answer: an
// unknown or misspelled command, a button from an older version of the game,
// or, in a private chat, plain text. What does not get one is an update that
// carries nothing at all (a photo, an edit) and plain text in a group, where
// the bot answering every message would be noise.
func NeedsHelp(err error, chatType string) bool {
	switch {
	case err == nil, errors.Is(err, ErrNoCommand):
		return false
	case errors.Is(err, ErrHelpRequested):
		return true
	case errors.Is(err, ErrNotACommand):
		return chatType == privateChat
	}
	return true
}

// privateChat is Telegram's chat type for a one-to-one chat with the bot.
const privateChat = "private"

// Parse extracts the command and payload from one update.
//
// It checks spelling only. Whether the game serves the command is Route's
// question, so this can be tested, and reasoned about, without the table.
//
// The payload is always non-nil on success, so a caller can marshal it without
// a nil check and a command with no arguments serialises as {} rather than
// null.
func Parse(update client.Update) (command string, payload map[string]any, err error) {
	switch {
	case update.CallbackQuery != nil:
		return parseCallbackData(update.CallbackQuery.Data)
	case update.Message != nil:
		return parseText(update.Message.Text)
	default:
		// An edited message is deliberately not routed. Re-running a command
		// because someone edited the text they sent five minutes ago is a
		// replay, and the gateway should not invent one.
		return "", nil, ErrNoCommand
	}
}

// ParseText exposes the text branch on its own, for callers that already have
// the message text in hand (the command middleware, and tests).
func ParseText(text string) (command string, payload map[string]any, err error) {
	return parseText(text)
}

// ParseCallbackData exposes the callback branch on its own. Use it wherever
// callback data is handled outside an update, and never skip it: the length
// and character checks live here.
func ParseCallbackData(data string) (command string, payload map[string]any, err error) {
	return parseCallbackData(data)
}

func parseText(text string) (string, map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil, ErrNoCommand
	}
	if !strings.HasPrefix(text, "/") {
		return "", nil, ErrNotACommand
	}

	fields := strings.Fields(text)
	head := strings.ToLower(strings.TrimPrefix(fields[0], "/"))

	// "/job@torncity_bot apply" — in a group Telegram addresses the bot by
	// name, and the suffix is not part of the command.
	if at := strings.IndexByte(head, '@'); at >= 0 {
		head = head[:at]
	}
	if head == "" {
		return "", nil, ErrMalformedCommand
	}

	if head == AliasHelp && len(fields) == 1 {
		return "", nil, ErrHelpRequested
	}

	short, hasShortcut := shortcuts[head]
	if len(fields) < 2 {
		if hasShortcut && short.Bare != "" {
			return finish(short.Bare, nil)
		}
		return "", nil, ErrMissingAction
	}

	command := head + "." + strings.ToLower(fields[1])
	if hasShortcut && short.Words != "" && !commands.FromPlayerCommand(command) {
		// "/social mrjvadi": the words are not one of this slash-command's
		// commands, so they are the arguments of the one it stands for.
		return finish(short.Words, fields[1:])
	}
	return finish(command, fields[2:])
}

func parseCallbackData(data string) (string, map[string]any, error) {
	if data == "" {
		return "", nil, ErrEmptyCallbackData
	}
	if len(data) > MaxCallbackDataBytes {
		return "", nil, ErrCallbackDataTooLong
	}
	for i := 0; i < len(data); i++ {
		if !safeCallbackByte(data[i]) {
			return "", nil, ErrCallbackDataUnsafe
		}
	}

	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return "", nil, ErrMissingAction
	}
	for _, part := range parts {
		if part == "" {
			// "market::123" or a trailing colon: the shape is wrong, and
			// guessing which segment was meant would route the press to the
			// wrong handler.
			return "", nil, ErrMalformedCommand
		}
	}

	domain := strings.ToLower(parts[0])
	action := strings.ToLower(parts[1])
	return finish(domain+"."+action, parts[2:])
}

// safeCallbackByte reports whether a byte may appear in callback data.
//
// The set is letters, digits, underscore, hyphen, dot and the colon separator.
// It is chosen to be boring: these bytes cannot terminate a NATS subject
// token, cannot introduce whitespace or control characters into a log line,
// and are safe to echo back into an error message. Everything else, including
// every multi-byte character, is rejected. Our own keyboards only ever emit
// this set, so a rejection means the data did not come from a button we built.
func safeCallbackByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_', b == '-', b == '.', b == ':':
		return true
	}
	return false
}

// finish validates the assembled command and builds its payload.
func finish(command string, args []string) (string, map[string]any, error) {
	if _, _, err := SplitCommand(command); err != nil {
		return "", nil, err
	}
	return command, buildPayload(command, args), nil
}

// buildPayload maps positional arguments onto the names in argNames, putting
// whatever is left over under "args".
func buildPayload(command string, args []string) map[string]any {
	payload := make(map[string]any, len(args))

	names := argNames[command]
	used := 0
	for used < len(names) && used < len(args) {
		if used == len(names)-1 && joinRest[command] {
			payload[names[used]] = strings.Join(args[used:], " ")
			used = len(args)
			break
		}
		payload[names[used]] = args[used]
		used++
	}
	if used < len(args) {
		// Copied so the payload never aliases the caller's slice.
		rest := make([]string, len(args)-used)
		copy(rest, args[used:])
		payload["args"] = rest
	}
	return payload
}

// SplitCommand splits a command into the two parts subjects.Command needs.
//
//	job.apply          -> ("job", "apply")
//	player.profile.get -> ("player", "profile.get")
//
// The domain is the first token; the action is everything after it, dots and
// all, because a multi-level action is a legal subject ("profile.get") while a
// multi-level domain is not: the domain is what a JetStream consumer filters
// on and what a team owns.
//
// Every token is validated against tokenPattern, so the result is always safe
// to hand to subjects.Command.
func SplitCommand(command string) (domain, action string, err error) {
	if command == "" {
		return "", "", ErrNoCommand
	}

	tokens := strings.Split(command, ".")
	if len(tokens) < 2 {
		return "", "", ErrMissingAction
	}
	for _, token := range tokens {
		if !tokenPattern.MatchString(token) {
			return "", "", ErrMalformedCommand
		}
	}
	return tokens[0], strings.Join(tokens[1:], "."), nil
}

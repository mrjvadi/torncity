package routing

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mrjvadi/torncity/internal/commands"
)

// Command aliases: commands typed without a slash, in any language.
//
// Players in a Persian group type «دزدی», not "/crime". The words a language
// uses for each command are data, in that language's locale file:
//
//	command_alias:
//	  crime: "دزدی | خلاف"
//	  bank:
//	    deposit: "واریز"
//
// The key names the slash-command the words stand for: its first segment is
// the slash-command, the rest (dots kept) is the action, so command_alias.crime
// is "/crime" and command_alias.bank.deposit is "/bank deposit". A word that
// matches is replaced by that spelling and the message is then read exactly
// as if it had been typed with the slash: «واریز ۵۰۰۰» is "/bank deposit 5000".
// Two keys are not slash-commands the game serves and mean something to the
// gateway itself: help (the help screen) and cancel (drop a pending input).
//
// Every loaded language's words are live at once, so a Persian player whose
// Telegram is set to English still reaches the game with «دزدی». That makes a
// collision possible — one word meaning two commands in two languages — and
// LoadAliases refuses such a word, and reports it, rather than picking one.
// Adding a language is a new locale file and nothing else.
//
// # Normalisation
//
// A word is compared after NormalizeWord: Arabic yeh and kaf are read as their
// Persian forms (a keyboard difference, not a different word), the zero-width
// non-joiner and other joiners and diacritics are dropped, Latin letters are
// lower-cased and punctuation around the word is ignored. Arguments keep their
// spelling, except that Persian and Arabic digits become ASCII digits
// (NormalizeArg), because every argument a command takes is an ASCII token.
//
// An alias may also be a short phrase of up to MaxAliasPhraseWords words
// («ساخت روستا» for settlement.found) rather than a single word — a
// deliberately distinctive multi-word trigger is far less likely to appear by
// accident in ordinary group chatter than a common single noun would be, the
// same nuisance concern the crime example above already raises. Rewrite tries
// the longest possible phrase at the message's start first, falling back to
// shorter ones, so a phrase and a single-word alias can never shadow one
// another ambiguously.
//
// # Groups
//
// In a group, most messages are people talking to each other, and a bot that
// answers «دزدی کردند!» with a crime screen is a nuisance. So in a group
// (strict) only a message that is nothing but an alias and argument-shaped
// tokens — numbers, @usernames, player codes — is a command. In a private
// chat every alias works with any arguments.

// AliasSection is the locale section command aliases live in.
const AliasSection = "command_alias"

// The two alias keys the gateway answers itself.
const (
	AliasHelp   = "help"
	AliasCancel = "cancel"
)

// HelpText and CancelText are the slash spellings the two gateway-answered
// aliases rewrite to.
const (
	HelpText   = "/help"
	CancelText = "/cancel"
)

// ErrHelpRequested means the message asked for help (/help or its alias). It
// is answered with the help screen and never published.
var ErrHelpRequested = errors.New("routing: help requested")

// ErrAliasCollision reports a word that two aliases claim.
var ErrAliasCollision = errors.New("routing: an alias word names two commands")

// ErrAliasTarget reports an alias key that is not a command the game serves.
var ErrAliasTarget = errors.New("routing: an alias names no command the game serves")

// ErrAliasWord reports a word that is empty or longer than
// MaxAliasPhraseWords words.
var ErrAliasWord = errors.New("routing: an alias word is not a usable word or phrase")

// maxStrictArgs bounds how many argument tokens a group message may carry
// after its alias and still be a command. The longest typed command
// ("پرداخت @ali 5000 card") has three.
const maxStrictArgs = 3

// MaxAliasPhraseWords bounds how many words a single alias may be. Kept
// small on purpose: an alias is a trigger a player types on purpose, never a
// sentence the gateway tries to understand.
const MaxAliasPhraseWords = 4

// AliasSource is where alias words are read from; *i18n.Catalog satisfies it.
type AliasSource interface {
	Languages() []string
	Section(lang, prefix string) map[string]string
}

// Aliases maps alias words or short phrases to the slash spelling they stand
// for. The zero value and nil match nothing.
type Aliases struct {
	words          map[string]string // normalised word/phrase -> "/crime", "/bank deposit"
	maxPhraseWords int               // the longest loaded alias, in words; at least 1
}

// LoadAliases reads every language's aliases.
//
// It returns the aliases it could use even when it also returns an error:
// a word two commands claim and a key that names no served command are left
// out, and every such problem is joined into the error, so one bad locale
// line costs that line and not the gateway. Callers log the error; the
// shipped locales are held to none by a test.
func LoadAliases(src AliasSource) (*Aliases, error) {
	a := &Aliases{words: map[string]string{}, maxPhraseWords: 1}
	if src == nil {
		return a, nil
	}
	var problems []error
	claimed := map[string]map[string][]string{} // word/phrase -> target -> languages
	for _, lang := range src.Languages() {
		section := src.Section(lang, AliasSection)
		keys := make([]string, 0, len(section))
		for k := range section {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			target, err := aliasTarget(key)
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %s.%s: %w", lang, AliasSection, key, err))
				continue
			}
			for _, raw := range splitAliasWords(section[key]) {
				n := len(strings.Fields(raw))
				word := normalizePhrase(raw)
				if word == "" {
					continue
				}
				if n > MaxAliasPhraseWords || strings.HasPrefix(word, "/") {
					problems = append(problems, fmt.Errorf("%w: %s: %s.%s: %q", ErrAliasWord, lang, AliasSection, key, raw))
					continue
				}
				if claimed[word] == nil {
					claimed[word] = map[string][]string{}
				}
				claimed[word][target] = append(claimed[word][target], lang)
			}
		}
	}

	words := make([]string, 0, len(claimed))
	for w := range claimed {
		words = append(words, w)
	}
	sort.Strings(words)
	for _, w := range words {
		targets := claimed[w]
		if len(targets) > 1 {
			var names []string
			for t, langs := range targets {
				names = append(names, fmt.Sprintf("%s (%s)", t, strings.Join(langs, ",")))
			}
			sort.Strings(names)
			problems = append(problems, fmt.Errorf("%w: %q is %s", ErrAliasCollision, w, strings.Join(names, " and ")))
			continue
		}
		for t := range targets {
			a.words[w] = t
		}
		if n := len(strings.Fields(w)); n > a.maxPhraseWords {
			a.maxPhraseWords = n
		}
	}
	return a, errors.Join(problems...)
}

// aliasTarget turns an alias key into the slash spelling it stands for, and
// checks that it is something the gateway can act on.
func aliasTarget(key string) (string, error) {
	switch key {
	case AliasHelp:
		return HelpText, nil
	case AliasCancel:
		return CancelText, nil
	}
	head, action, _ := strings.Cut(key, ".")
	text := "/" + head
	if action != "" {
		text += " " + action
	}
	command, _, err := parseText(text)
	if err != nil || !commands.FromPlayerCommand(command) {
		return "", ErrAliasTarget
	}
	return text, nil
}

// splitAliasWords splits a locale value into its words. Words are separated by
// "|" or a comma, Persian or Latin.
func splitAliasWords(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == '|' || r == ',' || r == '،' })
}

// Len is the number of alias words.
func (a *Aliases) Len() int {
	if a == nil {
		return 0
	}
	return len(a.words)
}

// Rewrite turns a plain-text message that starts with an alias — a word or a
// short phrase, see the file comment — into the slash-command it stands for,
// arguments normalised. ok is false when the message does not start with an
// alias or, when strict (a group), carries anything but argument-shaped
// tokens after it. A message that already starts with a slash is not an
// alias. The longest phrase at the message's start is tried first, so a
// two-word alias is never shadowed by a shorter one sharing its first word.
func (a *Aliases) Rewrite(text string, strict bool) (string, bool) {
	if a == nil || len(a.words) == 0 {
		return "", false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 || strings.HasPrefix(fields[0], "/") {
		return "", false
	}
	maxK := a.maxPhraseWords
	if maxK > len(fields) {
		maxK = len(fields)
	}
	for k := maxK; k >= 1; k-- {
		phraseWords := make([]string, k)
		for i := 0; i < k; i++ {
			phraseWords[i] = NormalizeWord(fields[i])
		}
		target, ok := a.words[strings.Join(phraseWords, " ")]
		if !ok {
			continue
		}
		args := fields[k:]
		if strict {
			if len(args) > maxStrictArgs {
				continue
			}
			shaped := true
			for _, arg := range args {
				if !argumentShaped(NormalizeArg(arg)) {
					shaped = false
					break
				}
			}
			if !shaped {
				continue
			}
		}
		out := target
		for _, arg := range args {
			out += " " + NormalizeArg(arg)
		}
		return out, true
	}
	return "", false
}

// Is reports whether text is exactly the alias for key (help or cancel, or a
// command key such as "crime"), in any language.
func (a *Aliases) Is(text, key string) bool {
	if a == nil {
		return false
	}
	fields := strings.Fields(text)
	if len(fields) != 1 {
		return false
	}
	target, err := aliasTarget(key)
	if err != nil {
		return false
	}
	return a.words[NormalizeWord(fields[0])] == target
}

// argumentShaped is what a group message may carry after its alias: a number
// (with group separators), an @username or a player code.
var argumentShaped = regexp.MustCompile(`^(?:[0-9][0-9,]*|@[A-Za-z0-9_]{3,32}|[0-9A-Z]{7})$`).MatchString

// NormalizeWord is how an alias word is compared; see the file comment.
func NormalizeWord(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 'ي' || r == 'ى' || r == 'ئ':
			b.WriteRune('ی')
		case r == 'ك':
			b.WriteRune('ک')
		case r == 'ۀ' || r == 'ة':
			b.WriteRune('ه')
		case r == '\u200c' || r == '\u200d' || r == '\u200e' || r == '\u200f' || r == '\u0640' || r == '\ufeff':
			// Joiners, direction marks and tatweel carry no letter.
		case r >= 'ً' && r <= 'ٟ', r == 'ٰ':
			// Diacritics.
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.TrimFunc(b.String(), func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r)
	})
}

// normalizePhrase is how a multi-word alias ("ساخت روستا") is compared: each
// of its words normalised by NormalizeWord and rejoined with a single space,
// so irregular whitespace in a locale file never keeps a phrase from
// matching the same words as typed.
func normalizePhrase(s string) string {
	fields := strings.Fields(s)
	words := make([]string, 0, len(fields))
	for _, f := range fields {
		if w := NormalizeWord(f); w != "" {
			words = append(words, w)
		}
	}
	return strings.Join(words, " ")
}

// NormalizeArg writes Persian and Arabic digits, and their group and decimal
// marks, in ASCII, and leaves everything else as typed.
func NormalizeArg(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r == '٬' || r == '،':
			b.WriteRune(',')
		case r == '٫':
			b.WriteRune('.')
		case r == '\u200c' || r == '\u200e' || r == '\u200f':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

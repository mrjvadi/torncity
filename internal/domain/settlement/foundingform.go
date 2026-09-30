package settlement

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The founding form (docs/adr/0028-world-and-settlements.md section 3.1,
// extended): before a village exists, the player who is about to found it
// chooses its name, its emblem, its motto and the national currency it
// reserves for the day it becomes a country. This file is the rules of that
// form — what a name may be made of, how long it may be, what is refused —
// as pure functions over plain values; the lists they read (palette, shapes,
// icons, reserved and banned words) are content (configs/content/
// founding.yml) and the bounds are configuration (settlement.founding_*),
// handed in through FormRules. Nothing here touches a database: whether a
// name or a code is already TAKEN is the repository's answer
// (application.SettlementRepository), reported with the same Problem codes.

// Problem codes. A client localises each; the server gives a sentence in the
// player's language too (locales, founding.problem.<code>).
const (
	ProblemNameShort         = "name_short"
	ProblemNameLong          = "name_long"
	ProblemNameChars         = "name_chars"
	ProblemNameLink          = "name_link"
	ProblemNameForbidden     = "name_forbidden"
	ProblemNameReserved      = "name_reserved"
	ProblemNameTaken         = "name_taken"
	ProblemMottoLong         = "motto_long"
	ProblemMottoChars        = "motto_chars"
	ProblemMottoLink         = "motto_link"
	ProblemMottoForbidden    = "motto_forbidden"
	ProblemCurrencyNameShort = "currency_name_short"
	ProblemCurrencyNameLong  = "currency_name_long"
	ProblemCurrencyNameChars = "currency_name_chars"
	ProblemCurrencyNameBad   = "currency_name_forbidden"
	ProblemCurrencyNameTaken = "currency_name_taken"
	ProblemCurrencyCode      = "currency_code_format"
	ProblemCurrencyCodeRes   = "currency_code_reserved"
	ProblemCurrencyCodeBad   = "currency_code_forbidden"
	ProblemCurrencyCodeTaken = "currency_code_taken"
	ProblemCurrencySymbol    = "currency_symbol_invalid"
	ProblemEmblem            = "emblem_invalid"
)

// Form fields a Problem names.
const (
	FieldName           = "name"
	FieldMotto          = "motto"
	FieldCurrencyName   = "currency_name"
	FieldCurrencyCode   = "currency_code"
	FieldCurrencySymbol = "currency_symbol"
	FieldEmblem         = "emblem"
)

// Problem is one thing wrong with a submitted form.
type Problem struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

// Emblem is a village's heraldic badge as the few choices that draw it: a
// shape, two colours from the palette and a symbol from the icon catalogue.
// It is stored and sent as these four codes; the client draws the SVG and the
// server writes the emoji fallback (FormRules.EmblemText) from the same codes.
type Emblem struct {
	Shape  string `json:"shape"`
	ColorA string `json:"color_a"`
	ColorB string `json:"color_b"`
	Icon   string `json:"icon"`
}

// Form is what the founder submits.
type Form struct {
	Name           string `json:"name"`
	Motto          string `json:"motto"`
	CurrencyName   string `json:"currency_name"`
	CurrencyCode   string `json:"currency_code"`
	CurrencySymbol string `json:"currency_symbol"`
	Emblem         Emblem `json:"emblem"`
}

// Choice is one entry of an emblem catalogue: its code and the emoji that
// stands for it where a picture cannot be drawn (a Telegram message).
type Choice struct {
	Code  string
	Emoji string
}

// FormRules are the bounds and lists the form is checked against.
type FormRules struct {
	NameMin, NameMax           int
	MottoMax                   int
	CurrencyNameMin            int
	CurrencyNameMax            int
	CurrencyCodeLen            int
	CurrencySymbolMax          int
	Shapes, Icons, Palette     []Choice
	BannedWords, ReservedNames []string
	// ReservedCodes are currency codes no village may take: the game's own
	// (SUP, NIL) and the real-world ones.
	ReservedCodes []string
}

// Normalize prepares free text: Arabic letter variants become the Persian
// ones, invisible direction marks and the kashida go, runs of white space
// become one space and the ends are trimmed. The zero-width non-joiner
// (U+200C) is kept: Persian words need it.
func Normalize(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case 'ي':
			r = 'ی'
		case 'ك':
			r = 'ک'
		case '\u0640', '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069', '\ufeff', '\u200b':
			continue
		}
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// NameKey is what two names are compared by: normalised, lower-cased, with
// spaces, hyphens and the non-joiner taken out, so "Ab  Cd", "ab-cd" and
// "abcd" are one name.
func NameKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(Normalize(s)) {
		if r == ' ' || r == '-' || r == '‌' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isWordRune(r rune) bool {
	if !unicode.IsLetter(r) {
		return false
	}
	return unicode.Is(unicode.Arabic, r) || unicode.Is(unicode.Latin, r)
}

// nameRunesOK: Persian and Latin letters, spaces, the non-joiner and hyphens.
func nameRunesOK(s string) bool {
	for _, r := range s {
		if !isWordRune(r) && r != ' ' && r != '‌' && r != '-' {
			return false
		}
	}
	return true
}

// mottoRunesOK: a name's characters, digits, and the marks a short phrase
// needs. A full stop, a colon and a slash are not among them: they make
// addresses.
func mottoRunesOK(s string) bool {
	for _, r := range s {
		switch {
		case isWordRune(r), r == ' ', r == '‌', r == '-':
		case unicode.IsDigit(r):
		case strings.ContainsRune("،؛؟!?,;'\"«»()", r):
		default:
			return false
		}
	}
	return true
}

var dottedAddress = regexp.MustCompile(`[\p{L}\p{N}]\.[\p{L}]{2,}`)

// looksLikeLink reports text that is, or contains, an address or a mention.
func looksLikeLink(s string) bool {
	l := strings.ToLower(s)
	for _, w := range []string{"http", "www", "t.me", "://", "@", "#", "/", "\\"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return dottedAddress.MatchString(l)
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// forbidden reports text containing a banned word: as a whole word always,
// and anywhere inside the joined text when the banned word is long enough
// (five characters or more) for that not to catch innocent words.
func (r FormRules) forbidden(s string) bool {
	words := strings.Fields(strings.ToLower(strings.NewReplacer("‌", " ", "-", " ").Replace(Normalize(s))))
	joined := NameKey(s)
	for _, bad := range r.BannedWords {
		b := NameKey(bad)
		if b == "" {
			continue
		}
		for _, w := range words {
			if w == b {
				return true
			}
		}
		if runeLen(b) >= 5 && strings.Contains(joined, b) {
			return true
		}
	}
	return false
}

func (r FormRules) reservedName(s string) bool {
	k := NameKey(s)
	for _, n := range r.ReservedNames {
		if NameKey(n) == k {
			return true
		}
	}
	return false
}

func has(list []Choice, code string) bool {
	for _, c := range list {
		if c.Code == code {
			return true
		}
	}
	return false
}

func emojiOf(list []Choice, code string) string {
	for _, c := range list {
		if c.Code == code {
			return c.Emoji
		}
	}
	return ""
}

// EmblemValid reports an emblem whose four codes are all in the catalogues
// and whose two colours differ.
func (r FormRules) EmblemValid(e Emblem) bool {
	return has(r.Shapes, e.Shape) && has(r.Icons, e.Icon) && has(r.Palette, e.ColorA) && has(r.Palette, e.ColorB) && e.ColorA != e.ColorB
}

// EmblemText is the emblem where it cannot be drawn: the shape's emoji, the
// icon's, then one dot per colour ("🛡 🌾 🔴🟡").
func (r FormRules) EmblemText(e Emblem) string {
	parts := []string{emojiOf(r.Shapes, e.Shape), emojiOf(r.Icons, e.Icon), emojiOf(r.Palette, e.ColorA) + emojiOf(r.Palette, e.ColorB)}
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// Check normalises the form and returns it with every problem found (none
// when it is acceptable). The optional motto may be empty; the symbol
// defaults to the code.
func (r FormRules) Check(f Form) (Form, []Problem) {
	var out []Problem
	add := func(field, code string) { out = append(out, Problem{Field: field, Code: code}) }

	f.Name = Normalize(f.Name)
	f.Motto = Normalize(f.Motto)
	f.CurrencyName = Normalize(f.CurrencyName)
	f.CurrencyCode = strings.ToUpper(strings.TrimSpace(f.CurrencyCode))
	f.CurrencySymbol = strings.TrimSpace(f.CurrencySymbol)
	if f.CurrencySymbol == "" {
		f.CurrencySymbol = f.CurrencyCode
	}

	switch n := runeLen(f.Name); {
	case looksLikeLink(f.Name):
		add(FieldName, ProblemNameLink)
	case !nameRunesOK(f.Name):
		add(FieldName, ProblemNameChars)
	case n < r.NameMin:
		add(FieldName, ProblemNameShort)
	case n > r.NameMax:
		add(FieldName, ProblemNameLong)
	case r.reservedName(f.Name):
		add(FieldName, ProblemNameReserved)
	case r.forbidden(f.Name):
		add(FieldName, ProblemNameForbidden)
	}

	if f.Motto != "" {
		switch {
		case looksLikeLink(f.Motto):
			add(FieldMotto, ProblemMottoLink)
		case !mottoRunesOK(f.Motto):
			add(FieldMotto, ProblemMottoChars)
		case runeLen(f.Motto) > r.MottoMax:
			add(FieldMotto, ProblemMottoLong)
		case r.forbidden(f.Motto):
			add(FieldMotto, ProblemMottoForbidden)
		}
	}

	switch n := runeLen(f.CurrencyName); {
	case looksLikeLink(f.CurrencyName), !nameRunesOK(f.CurrencyName):
		add(FieldCurrencyName, ProblemCurrencyNameChars)
	case n < r.CurrencyNameMin:
		add(FieldCurrencyName, ProblemCurrencyNameShort)
	case n > r.CurrencyNameMax:
		add(FieldCurrencyName, ProblemCurrencyNameLong)
	case r.forbidden(f.CurrencyName):
		add(FieldCurrencyName, ProblemCurrencyNameBad)
	}

	codeOK := runeLen(f.CurrencyCode) == r.CurrencyCodeLen
	for _, c := range f.CurrencyCode {
		if c < 'A' || c > 'Z' {
			codeOK = false
		}
	}
	switch {
	case !codeOK:
		add(FieldCurrencyCode, ProblemCurrencyCode)
	case r.codeReserved(f.CurrencyCode):
		add(FieldCurrencyCode, ProblemCurrencyCodeRes)
	case r.forbidden(f.CurrencyCode):
		add(FieldCurrencyCode, ProblemCurrencyCodeBad)
	}

	if !symbolOK(f.CurrencySymbol, r.CurrencySymbolMax) {
		add(FieldCurrencySymbol, ProblemCurrencySymbol)
	}

	if !r.EmblemValid(f.Emblem) {
		add(FieldEmblem, ProblemEmblem)
	}
	return f, out
}

func (r FormRules) codeReserved(code string) bool {
	for _, c := range r.ReservedCodes {
		if strings.EqualFold(c, code) {
			return true
		}
	}
	return false
}

// symbolOK: one to max characters, each a letter, a digit or a symbol —
// never white space, punctuation that makes addresses, or a control.
func symbolOK(s string, max int) bool {
	n := runeLen(s)
	if n < 1 || n > max {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSymbol(r)) {
			return false
		}
		if strings.ContainsRune("@#/\\<>&\"'", r) {
			return false
		}
	}
	return true
}

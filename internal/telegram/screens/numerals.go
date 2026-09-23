package screens

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Numbers as a language writes them.
//
// A number on screen is not only its value: Persian writes ۱۲٬۵۰۰ where
// English writes 12,500, and a Persian sentence with ASCII digits in it reads
// as unfinished work. How a language writes its numbers is locale DATA, not a
// branch on the language code, so it lives in the catalogue next to the
// words:
//
//	format.digits             the ten digits, zero to nine, in order
//	format.group_separator    between each group of three digits
//	format.decimal_separator  between the whole part and the fraction
//
// A language that leaves a key out gets the ASCII default for it, so a new
// locale renders correctly before anyone thinks about its numerals.
//
// Every number a player reads goes through here: FormatNumber, FormatMoney,
// FormatDuration, PercentFromBPS, and any integer handed to Context.T as a
// placeholder value. A string argument is never touched — a player code such
// as K7Q2M9A or a command example must stay exactly as it has to be typed.

// Catalogue keys of the numeral data.
const (
	keyDigits           = "format.digits"
	keyGroupSeparator   = "format.group_separator"
	keyDecimalSeparator = "format.decimal_separator"
)

// ASCII defaults, used when a locale does not say otherwise. Punctuation, not
// text: they carry no words.
const (
	asciiDigits             = "0123456789"
	defaultGroupSeparator   = ","
	defaultDecimalSeparator = "."
)

// numerals is how one language writes numbers.
type numerals struct {
	digits  [10]string
	group   string
	decimal string
}

// numerals reads this context's numeral data from the catalogue.
func (c Context) numerals() numerals {
	n := numerals{group: defaultGroupSeparator, decimal: defaultDecimalSeparator}
	for i, r := range asciiDigits {
		n.digits[i] = string(r)
	}
	if c.Msgs == nil {
		return n
	}
	if d := c.Msgs.T(c.Lang, keyDigits, nil); d != keyDigits && utf8.RuneCountInString(d) == 10 {
		i := 0
		for _, r := range d {
			n.digits[i] = string(r)
			i++
		}
	}
	if g := c.Msgs.T(c.Lang, keyGroupSeparator, nil); g != keyGroupSeparator && g != "" {
		n.group = g
	}
	if d := c.Msgs.T(c.Lang, keyDecimalSeparator, nil); d != keyDecimalSeparator && d != "" {
		n.decimal = d
	}
	return n
}

// localise rewrites the ASCII digits of s in this language's digits and its
// "." as this language's decimal separator. s is a number the code produced
// (strconv output), never a player's input.
func (n numerals) localise(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteString(n.digits[r-'0'])
		case r == '.':
			b.WriteString(n.decimal)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// group writes a non-negative integer with the group separator between each
// group of three digits.
func (n numerals) grouped(v uint64) string {
	digits := strconv.FormatUint(v, 10)
	if len(digits) <= 3 {
		return n.localise(digits)
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteString(groupMark)
		}
		b.WriteString(digits[i : i+3])
	}
	return strings.ReplaceAll(n.localise(b.String()), groupMark, n.group)
}

// groupMark stands in for the group separator while digits are localised, so
// a separator that is itself a digit-like rune cannot be rewritten.
const groupMark = "\x00"

// FormatNumber renders an integer the way this context's language writes
// numbers: its digits, and its separator between each group of three, so
// 12500 reads as 12,500 in English and ۱۲٬۵۰۰ in Persian. Every count a player
// compares — XP, distance, energy — goes through it.
func FormatNumber(c Context, v int64) string {
	n := c.numerals()
	if v < 0 {
		// The magnitude of MinInt64 does not fit an int64; as a uint64 it
		// does.
		return "-" + n.grouped(uint64(-(v+1))+1)
	}
	return n.grouped(uint64(v))
}

// PercentFromBPS renders a basis-point rate as a percentage number, in this
// context's digits and decimal separator. The percent sign is the template's.
//
// Integer arithmetic throughout, like world.City.TaxOn: 750 bps is "7.5" and
// 1000 bps is "10", with no float anywhere near a number a player compares
// two cities by.
func PercentFromBPS(c Context, bps int) string {
	if bps < 0 {
		bps = 0
	}
	whole := bps / 100
	frac := bps % 100
	var s string
	switch {
	case frac == 0:
		s = strconv.Itoa(whole)
	case frac%10 == 0:
		s = strconv.Itoa(whole) + "." + strconv.Itoa(frac/10)
	default:
		s = strconv.Itoa(whole) + "." + pad2(frac)
	}
	return c.numerals().localise(s)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// localiseArgs returns args with every integer value written as a number of
// this context's language. The caller's map is never modified: a screen may
// reuse one argument map for two keys.
func (c Context) localiseArgs(args map[string]any) map[string]any {
	var out map[string]any
	for k, v := range args {
		s, ok := c.integerText(v)
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(args))
			for k2, v2 := range args {
				out[k2] = v2
			}
		}
		out[k] = s
	}
	if out == nil {
		return args
	}
	return out
}

// integerText formats v when it is an integer of any width.
func (c Context) integerText(v any) (string, bool) {
	switch n := v.(type) {
	case int:
		return FormatNumber(c, int64(n)), true
	case int8:
		return FormatNumber(c, int64(n)), true
	case int16:
		return FormatNumber(c, int64(n)), true
	case int32:
		return FormatNumber(c, int64(n)), true
	case int64:
		return FormatNumber(c, n), true
	case uint:
		return c.numerals().grouped(uint64(n)), true
	case uint8:
		return c.numerals().grouped(uint64(n)), true
	case uint16:
		return c.numerals().grouped(uint64(n)), true
	case uint32:
		return c.numerals().grouped(uint64(n)), true
	case uint64:
		return c.numerals().grouped(n), true
	}
	return "", false
}

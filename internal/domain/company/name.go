package company

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Name errors. Each needs a different correction from the player.
var (
	// ErrNameLength means a name shorter or longer than allowed. The detail
	// is a NameLength.
	ErrNameLength = errors.New("company: name length")
	// ErrNameCharset means a name with a character a name may not hold, or
	// fewer than two letters.
	ErrNameCharset = errors.New("company: name characters")
	// ErrNameReserved means a name that impersonates an office or the
	// game itself.
	ErrNameReserved = errors.New("company: name is reserved")
	// ErrNameTaken means another company of the city bears the name.
	ErrNameTaken = errors.New("company: name is taken in this city")
)

// NameLength details ErrNameLength.
type NameLength struct{ Min, Max, Have int }

func (e NameLength) Error() string {
	return fmt.Sprintf("%v: %d characters, allowed %d..%d", ErrNameLength, e.Have, e.Min, e.Max)
}

// Unwrap lets errors.Is match ErrNameLength.
func (e NameLength) Unwrap() error { return ErrNameLength }

// NameRules are what a company name may be. The bounds are configuration and
// the reserved words content; the rule is here.
type NameRules struct {
	MinRunes, MaxRunes int
	// Reserved are words no name may contain, compared by NameKey: the
	// names of offices and institutions, so no company passes itself off
	// as the city.
	Reserved []string
}

// zwnj is the Persian zero-width non-joiner, part of how Persian is spelled.
const zwnj = '‌'

// nameMarks are the punctuation a name may hold between its words.
const nameMarks = "-.&'"

// CleanName trims a typed name and collapses every run of white space to
// one space; the result is what is stored and shown.
func CleanName(raw string) string {
	return strings.Join(strings.Fields(raw), " ")
}

// NameKey is the form two names are compared in: case folded, the Arabic
// forms of yeh and kaf read as the Persian ones, the non-joiner and every
// space and mark dropped. "Kaveh Foods" and "kaveh  foods." are one name;
// so are «کاوه» spelt with an Arabic or a Persian kaf.
func NameKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r == 'ي' || r == 'ى':
			r = 'ی'
		case r == 'ك':
			r = 'ک'
		case r == zwnj || unicode.IsSpace(r) || strings.ContainsRune(nameMarks, r):
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// CheckName cleans a typed name and returns it, or says why it cannot be a
// company's name: its length in characters, a character that is not a
// letter, a digit, a space, the non-joiner or one of - . & ', fewer than two
// letters, a first character that is not a letter or a digit, or a reserved
// word inside it. Uniqueness in the city is the store's to answer.
func CheckName(raw string, r NameRules) (string, error) {
	name := CleanName(raw)
	n := utf8.RuneCountInString(name)
	if n < r.MinRunes || n > r.MaxRunes {
		return "", NameLength{Min: r.MinRunes, Max: r.MaxRunes, Have: n}
	}
	letters := 0
	for i, c := range name {
		switch {
		case unicode.IsLetter(c):
			letters++
		case unicode.IsDigit(c):
		case i > 0 && (c == ' ' || c == zwnj || strings.ContainsRune(nameMarks, c)):
		default:
			return "", fmt.Errorf("%w: %q", ErrNameCharset, c)
		}
		if unicode.Is(unicode.Mn, c) || unicode.IsControl(c) {
			return "", fmt.Errorf("%w: %q", ErrNameCharset, c)
		}
	}
	if letters < 2 {
		return "", fmt.Errorf("%w: fewer than two letters", ErrNameCharset)
	}
	key := NameKey(name)
	for _, w := range r.Reserved {
		if k := NameKey(w); k != "" && strings.Contains(key, k) {
			return "", fmt.Errorf("%w: %q", ErrNameReserved, w)
		}
	}
	return name, nil
}

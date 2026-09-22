// Package i18n is the message catalogue every player-visible string is read
// from.
//
// No user-facing text lives in Go source. Text is content, not code: a
// wording fix, a new language or a tone change must be a file edit and a
// restart, never a recompile and a deploy. Keeping the strings out of the
// binary is also what makes a second language possible at all — a string
// welded into a function body has exactly one translation forever.
//
// Two rules shape the design.
//
// A missing translation returns the key. Returning empty text would ship a
// blank bubble to a player and look like a bug in the game; "profile.title"
// on screen is obviously a missing translation and gets fixed the same
// morning. Loud beats tidy.
//
// Substitution is literal. A value put in place of a placeholder is never
// scanned again, so a player who names themself "{balance}" gets a name
// containing those characters and no access to the rest of the template data.
//
// Concurrency: a Catalog is immutable once Load returns. Nothing mutates it,
// so any number of goroutines may read it without locking. Reloading does not
// change a Catalog; it builds a new one and swaps the pointer, which is what
// Store does.
package i18n

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// DefaultLanguage is the language every other language falls back to. It is
// the language of the audience the game launches for, so it is the one locale
// guaranteed to be complete.
const DefaultLanguage = "fa"

// localeExt is the extension Load accepts. A locale directory may hold notes
// or other files; only these are read.
const localeExt = ".yml"

// Failures a caller may want to distinguish.
var (
	ErrSyntax              = errors.New("i18n: locale file is not valid YAML")
	ErrNotString           = errors.New("i18n: locale value is not a message string")
	ErrNoLocales           = errors.New("i18n: no locale files found")
	ErrNoDefaultLocale     = errors.New("i18n: default locale is missing")
	ErrEmptyLocale         = errors.New("i18n: locale file defines no messages")
	ErrMissingKey          = errors.New("i18n: key missing from a locale")
	ErrPlaceholderMismatch = errors.New("i18n: placeholders differ between locales")
)

// Catalog holds every message for every language.
//
// It is read-only after construction. See the package doc for why that
// matters and how reloading works.
type Catalog struct {
	defaultLang string
	langs       []string                     // sorted, for stable output
	messages    map[string]map[string]string // language -> dotted key -> text
}

// Load reads every *.yml in dir as one language, named after the file, and
// falls back to DefaultLanguage.
//
// Load does not call Validate. Loading and checking are separate so a caller
// can decide whether an incomplete translation stops the process or only
// writes a warning; the tests in this package rely on that separation too.
func Load(dir string) (*Catalog, error) {
	return LoadWithDefault(dir, DefaultLanguage)
}

// LoadWithDefault is Load with an explicit fallback language, for a
// deployment whose primary audience is not the default one.
func LoadWithDefault(dir, defaultLang string) (*Catalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: read locale directory: %w", err)
	}

	messages := map[string]map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != localeExt {
			continue
		}
		lang := strings.TrimSuffix(name, localeExt)
		path := filepath.Join(dir, name)

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("i18n: read %s: %w", name, err)
		}
		msgs, err := parseLocale(name, data)
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			return nil, fmt.Errorf("%w: %s", ErrEmptyLocale, name)
		}
		messages[lang] = msgs
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("%w in %s", ErrNoLocales, dir)
	}
	if _, ok := messages[defaultLang]; !ok {
		return nil, fmt.Errorf("%w: %s%s", ErrNoDefaultLocale, defaultLang, localeExt)
	}

	langs := make([]string, 0, len(messages))
	for lang := range messages {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	return &Catalog{defaultLang: defaultLang, langs: langs, messages: messages}, nil
}

// T resolves key for lang and substitutes args.
//
// The chain is: the requested language, then the default language, then the
// key itself. A nil Catalog resolves to the key as well, so a half-wired
// caller shows keys instead of panicking in a player's chat.
func (c *Catalog) T(lang, key string, args map[string]any) string {
	if c == nil {
		return key
	}
	if text, ok := c.lookup(lang, key); ok {
		return expand(text, args)
	}
	if text, ok := c.lookup(c.defaultLang, key); ok {
		return expand(text, args)
	}
	return key
}

// Has reports whether lang itself defines key, ignoring the fallback chain.
// Callers use it to decide whether a translation exists, which is a different
// question from what T would return.
func (c *Catalog) Has(lang, key string) bool {
	if c == nil {
		return false
	}
	_, ok := c.lookup(lang, key)
	return ok
}

// Languages returns the loaded language names, sorted. The slice is a copy:
// the catalogue stays immutable no matter what a caller does with it.
func (c *Catalog) Languages() []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c.langs))
	copy(out, c.langs)
	return out
}

// Default returns the fallback language.
func (c *Catalog) Default() string {
	if c == nil {
		return ""
	}
	return c.defaultLang
}

func (c *Catalog) lookup(lang, key string) (string, bool) {
	msgs, ok := c.messages[lang]
	if !ok {
		return "", false
	}
	text, ok := msgs[key]
	return text, ok
}

// Validate reports every way the locales disagree with each other.
//
// Two failures matter. A key present in one locale and absent from another
// means that language silently falls back and a player sees the wrong
// language mid-screen. A key whose placeholder set differs between locales
// means a translation drops information: a balance line without {amount} is
// not a shorter sentence, it is a wrong one.
//
// Every problem found is reported, not just the first, so one run tells a
// translator everything to fix. Errors are joined; errors.Is matches
// ErrMissingKey or ErrPlaceholderMismatch against the result.
func (c *Catalog) Validate() error {
	if c == nil || len(c.messages) == 0 {
		return ErrNoLocales
	}
	if _, ok := c.messages[c.defaultLang]; !ok {
		return fmt.Errorf("%w: %s", ErrNoDefaultLocale, c.defaultLang)
	}

	union := map[string]bool{}
	for _, msgs := range c.messages {
		for key := range msgs {
			union[key] = true
		}
	}
	keys := make([]string, 0, len(union))
	for key := range union {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var problems []error
	for _, key := range keys {
		// The reference is the default locale when it has the key, else
		// the first language that does, so a key the default is missing
		// still gets its placeholders compared.
		ref, refLang := "", ""
		if text, ok := c.lookup(c.defaultLang, key); ok {
			ref, refLang = text, c.defaultLang
		}

		for _, lang := range c.langs {
			text, ok := c.lookup(lang, key)
			if !ok {
				problems = append(problems, fmt.Errorf("%w: %q is defined elsewhere but not in %s", ErrMissingKey, key, lang))
				continue
			}
			if refLang == "" {
				ref, refLang = text, lang
				continue
			}
			if lang == refLang {
				continue
			}
			want, got := placeholders(ref), placeholders(text)
			if !sameSet(want, got) {
				problems = append(problems, fmt.Errorf("%w: %q has %v in %s but %v in %s",
					ErrPlaceholderMismatch, key, sorted(want), refLang, sorted(got), lang))
			}
		}
	}
	return errors.Join(problems...)
}

// Store holds the current catalogue behind an atomic pointer so a reload can
// replace it while requests are being served.
//
// Readers never lock: they take the pointer once and read an immutable
// catalogue. A request that started before a reload finishes with the old
// text, which is correct — a message must not change wording halfway through
// being built.
type Store struct {
	current atomic.Pointer[Catalog]
}

// NewStore returns a Store serving c.
func NewStore(c *Catalog) *Store {
	s := &Store{}
	s.Replace(c)
	return s
}

// Replace swaps in a new catalogue. The caller validates before replacing;
// Store deliberately does not, so an operator can force a partial catalogue
// in an emergency.
func (s *Store) Replace(c *Catalog) { s.current.Store(c) }

// Catalog returns the catalogue in force right now.
func (s *Store) Catalog() *Catalog { return s.current.Load() }

// T resolves through the current catalogue, so a Store can be injected
// wherever a catalogue can.
func (s *Store) T(lang, key string, args map[string]any) string {
	return s.Catalog().T(lang, key, args)
}

// expand substitutes {name} placeholders.
//
// The output is built in one pass and a substituted value is never re-read,
// so a value that itself contains "{other}" stays those literal characters.
// An unknown placeholder is left on screen as written, for the same reason a
// missing key returns the key: a visible gap gets fixed, an invisible one
// does not.
func expand(tmpl string, args map[string]any) string {
	if !strings.ContainsRune(tmpl, '{') {
		return tmpl
	}
	var b strings.Builder
	b.Grow(len(tmpl))
	for i := 0; i < len(tmpl); {
		if tmpl[i] != '{' {
			b.WriteByte(tmpl[i])
			i++
			continue
		}
		name, end, ok := readPlaceholder(tmpl, i)
		if !ok {
			b.WriteByte('{')
			i++
			continue
		}
		if v, found := args[name]; found {
			b.WriteString(format(v))
		} else {
			b.WriteString(tmpl[i:end])
		}
		i = end
	}
	return b.String()
}

// readPlaceholder reads "{name}" starting at i and returns the name and the
// index just past the closing brace.
func readPlaceholder(tmpl string, i int) (string, int, bool) {
	for j := i + 1; j < len(tmpl); j++ {
		c := tmpl[j]
		if c == '}' {
			if j == i+1 {
				return "", 0, false
			}
			return tmpl[i+1 : j], j + 1, true
		}
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-'
		if !ok {
			return "", 0, false
		}
	}
	return "", 0, false
}

// placeholders is the set of placeholder names a template uses.
func placeholders(tmpl string) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < len(tmpl); {
		if tmpl[i] != '{' {
			i++
			continue
		}
		name, end, ok := readPlaceholder(tmpl, i)
		if !ok {
			i++
			continue
		}
		out[name] = true
		i = end
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, "{"+k+"}")
	}
	sort.Strings(out)
	return out
}

// format renders an argument. A string is used as it stands; anything else
// goes through the standard formatter.
func format(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

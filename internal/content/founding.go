package content

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// This file holds the founding form's catalogue (configs/content/
// founding.yml; docs/adr/0028-world-and-settlements.md section 3): the
// shapes, colours and icons a village's emblem is built from, and the word
// lists its name, motto and currency are checked against. The rules that use
// them are internal/domain/settlement (FormRules). Storage follows
// recruitment.go's precedent: one content_documents row, kind "founding".

// ErrInvalidFoundingContent means founding.yml is unusable.
var ErrInvalidFoundingContent = errors.New("content: invalid founding content")

// FoundingChoiceDef is one shape, palette colour or icon.
type FoundingChoiceDef struct {
	Code  string `yaml:"code" json:"code"`
	Emoji string `yaml:"emoji" json:"emoji"`
	// Hex is a palette colour's sRGB value ("#b3261e"); empty for a shape
	// or an icon.
	Hex string `yaml:"hex,omitempty" json:"hex,omitempty"`
}

// FoundingDef is founding.yml's founding section.
type FoundingDef struct {
	Shapes                []FoundingChoiceDef `yaml:"shapes" json:"shapes"`
	Palette               []FoundingChoiceDef `yaml:"palette" json:"palette"`
	Icons                 []FoundingChoiceDef `yaml:"icons" json:"icons"`
	BannedWords           []string            `yaml:"banned_words" json:"banned_words"`
	ReservedNames         []string            `yaml:"reserved_names" json:"reserved_names"`
	ReservedCurrencyCodes []string            `yaml:"reserved_currency_codes" json:"reserved_currency_codes"`
}

// Founding returns the founding section, and whether the content has one.
func (s *Snapshot) Founding() (FoundingDef, bool) {
	if s.founding == nil {
		return FoundingDef{}, false
	}
	return *s.founding, true
}

var (
	foundingCode = regexp.MustCompile(`^[a-z][a-z0-9_]{1,23}$`)
	foundingHex  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	foundingCCY  = regexp.MustCompile(`^[A-Z]{2,6}$`)
)

// validateFounding checks founding.yml.
func (p *Pack) validateFounding(problems *[]error) {
	bad := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf("%w: %s", ErrInvalidFoundingContent, fmt.Sprintf(format, args...)))
	}
	if len(p.Founding) == 0 {
		return
	}
	if len(p.Founding) > 1 {
		bad("founding is declared %d times", len(p.Founding))
		return
	}
	d := p.Founding[0]
	list := func(name string, defs []FoundingChoiceDef, min int, hex bool) {
		if len(defs) < min {
			bad("%s has %d entries, want at least %d", name, len(defs), min)
		}
		seen := map[string]bool{}
		for i, c := range defs {
			where := fmt.Sprintf("%s[%d]", name, i)
			switch {
			case !foundingCode.MatchString(c.Code):
				bad("%s: code %q is not a lower-case identifier", where, c.Code)
			case seen[c.Code]:
				bad("%s: code %q is declared twice", where, c.Code)
			}
			seen[c.Code] = true
			if c.Emoji == "" {
				bad("%s: emoji is empty", where)
			}
			if hex && !foundingHex.MatchString(c.Hex) {
				bad("%s: hex %q is not #rrggbb", where, c.Hex)
			}
			if !hex && c.Hex != "" {
				bad("%s: only a palette colour has a hex", where)
			}
		}
	}
	list("shapes", d.Shapes, 1, false)
	list("palette", d.Palette, 2, true)
	list("icons", d.Icons, 1, false)
	for i, w := range d.BannedWords {
		if strings.TrimSpace(w) == "" {
			bad("banned_words[%d] is empty", i)
		}
	}
	for i, w := range d.ReservedNames {
		if strings.TrimSpace(w) == "" {
			bad("reserved_names[%d] is empty", i)
		}
	}
	codes := map[string]bool{}
	for i, c := range d.ReservedCurrencyCodes {
		if !foundingCCY.MatchString(c) {
			bad("reserved_currency_codes[%d] %q is not 2 to 6 capital letters", i, c)
		}
		codes[c] = true
	}
	// The game's own currencies must never be free to take.
	for _, c := range []string{"SUP", "NIL"} {
		if !codes[c] {
			bad("reserved_currency_codes must include %s", c)
		}
	}
}

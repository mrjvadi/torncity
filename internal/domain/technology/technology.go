// Package technology holds the rules of what a company knows how to make:
// the technology tree, who may research what, and what the owner of a
// technology may do with it (docs/adr/0005-item-and-production-model.md §7,
// docs/adr/0021-production-economy.md).
//
// A technology is content: its prerequisites, what researching it costs and
// how long it takes on the game clock, which kinds of company can research it
// and what skill their best member needs. Ownership is game state: a company
// that finishes the research owns the technology and chooses, per technology,
// how others may use it:
//
//	private    only the owner uses it                     reversible
//	license    the owner sells licenses at a price it sets reversible
//	published  free for everyone, forever                 NOT reversible
//
// WHAT A TECHNOLOGY GATES. Making a component that requires it, and authoring
// a design around such a component (item.CanManufacture, item.ValidateDesign).
// Never buying one, never holding a design someone else made, and never
// producing from a held design: a company that bought chipsets may build a
// phone from them (ADR 0005 §6).
//
// WHAT NEVER GRANTS A TECHNOLOGY. Reverse engineering. item.ReverseOutcome has
// no field for one, and Access below is built only from ownership, the
// published set and licenses: there is no input through which a copied design
// could add to it.
//
// NO CLOCK, NO RANDOMNESS. Durations are returned for the caller to schedule
// on the game clock.
package technology

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// Sentinel errors.
var (
	// ErrInvalidTech means a technology's definition is unusable.
	ErrInvalidTech = errors.New("technology: invalid technology")
	// ErrUnknownPrerequisite means a technology requires one that does not
	// exist.
	ErrUnknownPrerequisite = errors.New("technology: unknown prerequisite")
	// ErrCycle means the prerequisites loop: nothing on the loop could
	// ever be researched.
	ErrCycle = errors.New("technology: prerequisites form a cycle")

	// ErrAlreadyOwned means the company owns the technology already.
	ErrAlreadyOwned = errors.New("technology: already owned")
	// ErrPrerequisiteMissing means a prerequisite is neither owned by the
	// company nor published.
	ErrPrerequisiteMissing = errors.New("technology: prerequisite not unlocked")
	// ErrWrongCompanyType means the company's kind cannot research it.
	ErrWrongCompanyType = errors.New("technology: company type cannot research it")
	// ErrSkillTooLow means nobody in the company has the skill it needs.
	ErrSkillTooLow = errors.New("technology: skill too low")
	// ErrBusy means the company is researching something already.
	ErrBusy = errors.New("technology: research already running")

	// ErrUnknownMode means a sharing mode is not one of the three.
	ErrUnknownMode = errors.New("technology: unknown sharing mode")
	// ErrPublishedForever means a published technology was asked to become
	// private or licensed again. Publishing cannot be taken back: designs
	// and factories all over the world were built on it.
	ErrPublishedForever = errors.New("technology: a published technology stays published")
	// ErrInvalidPrice means a license price is outside its bounds.
	ErrInvalidPrice = errors.New("technology: invalid license price")

	// ErrNotForSale means the owner does not sell licenses to it.
	ErrNotForSale = errors.New("technology: owner does not license it")
	// ErrNoNeed means the buyer can already use the technology.
	ErrNoNeed = errors.New("technology: buyer can already use it")
	// ErrOwnLicense means a company tried to buy a license from itself.
	ErrOwnLicense = errors.New("technology: a company cannot license from itself")
)

// Bounds on authored content.
const (
	// MaxResearchTime is the longest research may run, game time.
	MaxResearchTime = 365 * 24 * time.Hour
	// MaxCost bounds a research cost, minor units.
	MaxCost = 1_000_000_000_000
)

// Tech is one technology of the tree. It is content.
type Tech struct {
	Code string
	// Requires are the technologies a company must have unlocked (owned or
	// published) before it may research this one.
	Requires []string
	// Cost is what researching it costs the company, minor units; it
	// leaves the economy.
	Cost int64
	// Time is how long research takes, GAME time.
	Time time.Duration
	// CompanyTypes are the kinds of company that may research it.
	CompanyTypes []string
	// Skill and Level: the company's best member in Skill must stand at
	// Level at least. An empty Skill needs nobody in particular.
	Skill string
	Level int
	// Control is export control on its licenses.
	Control Control
}

// Tree is the whole technology tree, keyed by code.
type Tree map[string]Tech

// Vocabulary is what a technology may name: the kinds of company and the
// skills that exist.
type Vocabulary struct {
	CompanyTypes item.Set
	Skills       item.Set
}

// ValidateTree applies every load-time rule to the tree and returns every
// problem found, joined.
func ValidateTree(techs []Tech, vocab Vocabulary) error {
	var errs []error
	fail := func(sentinel error, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, args...)))
	}
	tree := Tree{}
	for _, t := range techs {
		if t.Code == "" {
			fail(ErrInvalidTech, "a technology has no code")
			continue
		}
		if _, dup := tree[t.Code]; dup {
			fail(ErrInvalidTech, "%q declared twice", t.Code)
		}
		tree[t.Code] = t
		if t.Cost < 0 || t.Cost > MaxCost {
			fail(ErrInvalidTech, "%q cost %d", t.Code, t.Cost)
		}
		if t.Time <= 0 || t.Time > MaxResearchTime {
			fail(ErrInvalidTech, "%q research time %s", t.Code, t.Time)
		}
		if len(t.CompanyTypes) == 0 {
			fail(ErrInvalidTech, "%q names no company type that may research it", t.Code)
		}
		for _, ct := range t.CompanyTypes {
			if !vocab.CompanyTypes.Has(ct) {
				fail(ErrInvalidTech, "%q names unknown company type %q", t.Code, ct)
			}
		}
		if t.Skill != "" && !vocab.Skills.Has(t.Skill) {
			fail(ErrInvalidTech, "%q needs unknown skill %q", t.Code, t.Skill)
		}
		if t.Level < 0 || t.Level > item.MaxSkillLevel || (t.Skill == "" && t.Level != 0) {
			fail(ErrInvalidTech, "%q skill level %d", t.Code, t.Level)
		}
	}
	for _, t := range techs {
		seen := map[string]bool{}
		for _, r := range t.Requires {
			switch {
			case r == t.Code:
				fail(ErrCycle, "%q requires itself", t.Code)
			case seen[r]:
				fail(ErrInvalidTech, "%q requires %q twice", t.Code, r)
			default:
				if _, ok := tree[r]; !ok {
					fail(ErrUnknownPrerequisite, "%q requires %q", t.Code, r)
				}
			}
			seen[r] = true
		}
	}
	if cyc := findCycle(tree); cyc != "" {
		fail(ErrCycle, "through %q", cyc)
	}
	return errors.Join(errs...)
}

// findCycle returns a technology on a prerequisite cycle, or "".
func findCycle(tree Tree) string {
	const (
		white = iota
		grey
		black
	)
	colour := make(map[string]int, len(tree))
	var visit func(code string) string
	visit = func(code string) string {
		colour[code] = grey
		for _, r := range tree[code].Requires {
			if _, ok := tree[r]; !ok || r == code {
				continue
			}
			switch colour[r] {
			case grey:
				return r
			case white:
				if c := visit(r); c != "" {
					return c
				}
			}
		}
		colour[code] = black
		return ""
	}
	for _, code := range sortedCodes(tree) {
		if colour[code] == white {
			if c := visit(code); c != "" {
				return c
			}
		}
	}
	return ""
}

func sortedCodes(tree Tree) []string {
	out := make([]string, 0, len(tree))
	for c := range tree {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Standing is what a company brings to a research decision.
type Standing struct {
	// CompanyType is the company's kind.
	CompanyType string
	// Owned are the technologies it owns; Published those anyone owns and
	// has published. Together they are what it has unlocked.
	Owned     item.Set
	Published item.Set
	// Researching is whether a research of the company is running.
	Researching bool
	// SkillLevel returns its best member's level in a skill.
	SkillLevel func(skill string) int
}

func (s Standing) unlocked(code string) bool { return s.Owned.Has(code) || s.Published.Has(code) }

// Missing lists the prerequisites of t the company has not unlocked, in
// order.
func (s Standing) Missing(t Tech) []string {
	var out []string
	for _, r := range t.Requires {
		if !s.unlocked(r) {
			out = append(out, r)
		}
	}
	return out
}

// CanResearch reports whether a company may start researching t now, or the
// first reason it may not. A published technology may still be researched —
// owning it lets the company license it or keep improving on it — but a
// technology the company owns may not.
func CanResearch(t Tech, s Standing) error {
	if s.Owned.Has(t.Code) {
		return fmt.Errorf("%w: %q", ErrAlreadyOwned, t.Code)
	}
	allowed := false
	for _, ct := range t.CompanyTypes {
		if ct == s.CompanyType {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("%w: %q by %q", ErrWrongCompanyType, t.Code, s.CompanyType)
	}
	if missing := s.Missing(t); len(missing) > 0 {
		return fmt.Errorf("%w: %q needs %q", ErrPrerequisiteMissing, t.Code, missing[0])
	}
	if t.Skill != "" {
		level := 0
		if s.SkillLevel != nil {
			level = s.SkillLevel(t.Skill)
		}
		if level < t.Level {
			return fmt.Errorf("%w: %q needs %s %d, best is %d", ErrSkillTooLow, t.Code, t.Skill, t.Level, level)
		}
	}
	if s.Researching {
		return ErrBusy
	}
	return nil
}

// Mode is how an owner shares a technology.
type Mode string

const (
	Private   Mode = "private"
	Licensed  Mode = "license"
	Published Mode = "published"
)

// String is the mode as content and the database spell it.
func (m Mode) String() string { return string(m) }

// Validate rejects anything but the three modes.
func (m Mode) Validate() error {
	switch m {
	case Private, Licensed, Published:
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnknownMode, string(m))
}

// ChangeMode checks a change of sharing mode with its license price. Leaving
// published is refused; entering license needs a price in [1, maxPrice].
func ChangeMode(from, to Mode, price, maxPrice int64) error {
	if err := to.Validate(); err != nil {
		return err
	}
	if from == Published && to != Published {
		return ErrPublishedForever
	}
	if to == Licensed && (price < 1 || price > maxPrice) {
		return fmt.Errorf("%w: %d", ErrInvalidPrice, price)
	}
	return nil
}

// Offer is one owner's standing on a technology, as a buyer sees it.
type Offer struct {
	OwnerID string
	Mode    Mode
	Price   int64
}

// CheckLicense reports whether buyerID may buy a license for a technology
// from the owner of offer, given what the buyer can already use. A buyer who
// owns it, holds a license, or can use it because it is published has no
// need of one.
func CheckLicense(buyerID string, offer Offer, access item.TechAccess, code string) error {
	if offer.OwnerID == buyerID {
		return ErrOwnLicense
	}
	if access.Allows(code) {
		return fmt.Errorf("%w: %q", ErrNoNeed, code)
	}
	if offer.Mode != Licensed {
		return fmt.Errorf("%w: %q is %s", ErrNotForSale, code, offer.Mode)
	}
	if offer.Price < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidPrice, offer.Price)
	}
	return nil
}

// Access is what a company may build on: what it owns and what anyone
// published (Unlocked), and what it holds a license for. It is the only way
// this game builds an item.TechAccess, and it has no parameter for a design:
// reverse engineering cannot add to it.
func Access(owned, published, licensed []string) item.TechAccess {
	unlocked := item.NewSet(owned...)
	for _, p := range published {
		unlocked[p] = struct{}{}
	}
	return item.TechAccess{Unlocked: unlocked, Licensed: item.NewSet(licensed...)}
}

// ErrNotCleared means a buyer is not of a class allowed to buy a restricted
// technology's license or a restricted good: export control.
var ErrNotCleared = errors.New("technology: buyer is not cleared for it")

// Buyer is who is buying a license or a good. Kind is what holds the money
// and the goods — "player" or an organisation kind ("company" today; a state
// or an army later) — and Sector the organisation's sector (companies.yml
// sector: civilian, defence…). Both are data; a new kind or sector needs no
// code here.
type Buyer struct {
	Kind   string
	Sector string
}

// Classes are the buyer classes the buyer belongs to: its kind, and its
// kind with its sector ("company", "company:defence").
func (b Buyer) Classes() []string {
	if b.Sector == "" {
		return []string{b.Kind}
	}
	return []string{b.Kind, b.Kind + ":" + b.Sector}
}

// Control is what export control says of a technology or a good: whether it
// is restricted, and the buyer classes it may go to when it is (content: a
// technology's restricted and buyer_classes, a good's buyer_classes). A
// restricted one with no class may go to nobody.
type Control struct {
	Restricted   bool
	BuyerClasses []string
}

// Cleared is the one export-control check every license sale and every
// sale of goods passes through. Today no shipped content restricts anything;
// the military stage flags technologies and goods, and governments and
// armies arrive as buyer kinds, without a change here.
func Cleared(c Control, b Buyer) error {
	if !c.Restricted && len(c.BuyerClasses) == 0 {
		return nil
	}
	allowed := c.BuyerClasses
	for _, have := range b.Classes() {
		for _, a := range allowed {
			if a == have {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %v is not one of %v", ErrNotCleared, b.Classes(), allowed)
}

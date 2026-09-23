package item

import (
	"errors"
	"fmt"
)

// ErrTechnologyLocked means a technology a rule needs is neither unlocked for
// nor licensed to the company asking.
var ErrTechnologyLocked = errors.New("item: technology not unlocked or licensed")

// Component is a concrete part or material — cpu_a7, whole-wheat flour —
// that fills a slot (ADR 0005 §1). It is content, loaded from YAML.
type Component struct {
	Code     string
	Category string
	// Attributes are the values archetype attributes fold together.
	Attributes map[string]int64
	// RequiresTechnology lists what a company must hold to MAKE this
	// component, and to author a new design around it. It does not gate
	// buying one: a company that bought cpu_a7 on the market may build a
	// phone from it without owning semiconductor technology (ADR 0005 §6).
	RequiresTechnology []string
}

// Components is the catalog a design is validated against, keyed by code.
// The caller decides what is in it — the whole content catalog, or only what
// a company can source.
type Components map[string]Component

// ValidateComponent rejects a component with no code, or whose category does
// not exist.
func ValidateComponent(c Component, categories Set) error {
	var errs []error
	if c.Code == "" {
		errs = append(errs, fmt.Errorf("%w: component has no code", ErrEmptyCode))
	}
	if !categories.Has(c.Category) {
		errs = append(errs, fmt.Errorf("%w: component %q category %q", ErrUnknownCategory, c.Code, c.Category))
	}
	for _, tech := range c.RequiresTechnology {
		if tech == "" {
			errs = append(errs, fmt.Errorf("%w: component %q requires an unnamed technology", ErrEmptyCode, c.Code))
		}
	}
	return errors.Join(errs...)
}

// TechAccess is what a company may build on: the technologies unlocked for it
// (owned, or published for everyone — ADR 0005 §7) and those it holds a
// license for. Both are inputs; working them out from ownership and license
// rows is the caller's job.
type TechAccess struct {
	Unlocked Set
	Licensed Set
}

// Allows reports whether the company may use a technology.
func (t TechAccess) Allows(tech string) bool {
	return t.Unlocked.Has(tech) || t.Licensed.Has(tech)
}

// CanManufacture reports whether a company with this access may make the
// component itself, or ErrTechnologyLocked naming the first missing
// technology.
//
// This is the check reverse engineering can never satisfy on anyone's
// behalf: taking a phone apart tells you it contains cpu_a7, and nothing
// more. See ReverseEngineer.
func CanManufacture(c Component, access TechAccess) error {
	for _, tech := range c.RequiresTechnology {
		if !access.Allows(tech) {
			return fmt.Errorf("%w: %q needs %q", ErrTechnologyLocked, c.Code, tech)
		}
	}
	return nil
}

// Package companies is the companies, production and recruitment area's
// neutral contract (docs/adr/0039-presentation-split.md): the views of a
// city's company registry, a company's page and management, its staff and
// openings, the warehouse, research, design, orders, goods for sale and the
// recruitment of specialists, and the constructors that turn a view into a
// neutral response. It imports no edge and holds no wording.
package companies

import (
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// The shared pieces of a view, as the core names them.
type (
	// Named is a content entry: its code and its authored name.
	Named = presentation.Named
	// Ref names a place to go back to.
	Ref = presentation.Ref
	// JobRef names a position: a career and one of its tiers.
	JobRef = presentation.JobRef
	// CourseRef names a course.
	CourseRef = presentation.CourseRef
	// GovPlayer names another player.
	GovPlayer = presentation.GovPlayer
	// Requirement is one condition of a position, met or not.
	Requirement = presentation.Requirement
	// PaymentChoice is the ways to pay one charge.
	PaymentChoice = presentation.PaymentChoice
	// Way is the walk to the place a service is at.
	Way = presentation.Way
	// CompanyRef names a company.
	CompanyRef = presentation.CompanyRef
	// Good names a component, a good or a good of a design.
	Good = presentation.Good
	// Unavailable is the data of a "not available here" state: the stage a
	// thing starts at, what it needs and the nearest place that has it. It
	// is the economy area's type, the one the service gate fills.
	Unavailable = economy.Unavailable
	// NeedBuilding is a building a settlement must have standing for a thing
	// to run.
	NeedBuilding = economy.NeedBuilding
)

// Payment methods, as the core spells them.
const (
	MethodCash = presentation.MethodCash
	MethodCard = presentation.MethodCard
)

// DesignTarget is the argument that names a design as a target.
func DesignTarget(no int64) string { return presentation.DesignTarget(no) }

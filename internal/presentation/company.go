package presentation

import "strconv"

// DesignTargetPrefix marks a production target or a listing that is a design
// («d12»), not a component or a good.
const DesignTargetPrefix = "d"

// DesignTarget is the argument that names a design as a target.
func DesignTarget(no int64) string { return DesignTargetPrefix + strconv.FormatInt(no, 10) }

// CompanyRef names a company: its name, public code and kind. Companies,
// production, recruitment and the defence industry all name one.
type CompanyRef struct {
	Code string
	Name string
	Type Named
}

// Good names what a warehouse line, an order, a listing or a stock of arms is:
// a component, a good of the catalogue, or a good of a design.
type Good struct {
	// Component is true for a component or material.
	Component bool
	Item      Named
	// Design is the design's name and number when it is a good of one.
	Design   string
	DesignNo int64
}

// TargetArg is the argument that names a good as a production or sale target:
// «d12» for a design, else its code.
func (g Good) TargetArg() string {
	if g.DesignNo > 0 {
		return DesignTarget(g.DesignNo)
	}
	return g.Item.Code
}

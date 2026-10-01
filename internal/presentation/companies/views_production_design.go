package companies

// Design statuses and origins as screens take them.
const (
	DesignDraft             = "draft"
	DesignFinal             = "final"
	DesignAuthored          = "authored"
	DesignReverseEngineered = "reverse_engineered"
)

// DesignLine is one design of the studio.
type DesignLine struct {
	No     int64
	Name   string
	Item   Named
	Status string
	Origin string
}

// StudioView is a company's design studio.
type StudioView struct {
	Ref     CompanyRef
	Designs []DesignLine
	// Kinds are the goods the company may design now; Next those one
	// research or one license away, each with the way in. Hidden is set
	// when goods further away are kept out of sight until then
	// (docs/adr/0021, section 14).
	Kinds  []Named
	Next   []StudioKind
	Hidden bool
	// CanResearch is the owner, who alone runs the lab: the locked goods
	// lead there.
	CanResearch bool
	// Skill is the craft the best designer is measured in, per kind; the
	// studio shows the company's best level and the level needed.
	Need int
	// CanDesign is false when the company is at its design limit.
	CanDesign bool
	Max       int
}

// SlotLine is one slot of a design as the editor shows it.
type SlotLine struct {
	Slot     string
	Optional bool
	// Min and Max are the quantity it takes; Unit its display unit, which
	// has a locale entry (production.unit.<unit>) when it has one at all.
	Min, Max int64
	Unit     string
	// Component and Qty fill it; an empty component is an empty slot.
	Component Named
	Qty       int64
}

// Candidate is a component that fits the slot being filled.
type Candidate struct {
	Component Named
	Price     int64
	// Locked means the company can neither make it nor design with it:
	// the technology it requires is neither owned nor licensed.
	Locked bool
	// Quality is its own quality.
	Quality int
}

// AttributeLine is one computed attribute of a design.
type AttributeLine struct {
	Name  string
	Value int64
	// Observable attributes are what a buyer sees on the market.
	Observable bool
}

// DesignView is one design: the editor while it is a draft, the record once
// it is final.
type DesignView struct {
	Ref    CompanyRef
	No     int64
	Name   string
	Item   Named
	Status string
	Origin string
	// Source names the design a copy was taken from.
	Source string
	Slots  []SlotLine
	// Choosing is the slot whose candidates are shown.
	Choosing   string
	Candidates []Candidate
	Attributes []AttributeLine
	// CostFloor is what one unit's inputs cost at reference prices.
	CostFloor      int64
	QualityLossBPS int64
	OverheadBPS    int64
	// Complete is a draft whose required slots are all filled.
	Complete bool
	// Locked are the technologies a draft's components need and the
	// company lacks.
	Locked []Named
	// Version is this design's generation within its lineage, 1 for the
	// first ever authored. PrevAttributes, when non-nil, are the version
	// before's computed attributes, for the ▲▼ delta a revision's page
	// shows next to each number.
	Version        int64
	PrevAttributes map[string]int64
}

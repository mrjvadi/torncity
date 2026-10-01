package companies

import "time"

// ImprovementView is the plan of an improvement project on one attribute of
// one design.
type ImprovementView struct {
	Ref       CompanyRef
	No        int64
	Design    Good
	Attribute Named
	// GainBPS is what the project would add, in basis points, before it is
	// capped (item.NextImprovementBPS).
	GainBPS  int64
	Cost     int64
	Duration time.Duration
	FinishAt time.Time
	// Started is set once the project is running.
	Started bool
}

// RetrofitView is the plan of a retrofit job: consuming one upgrade kit to
// move one existing unit to the kit's target version.
type RetrofitView struct {
	Ref      CompanyRef
	KitNo    int64
	Good     Good
	FromVer  int64
	ToVer    int64
	Duration time.Duration
	FinishAt time.Time
	Started  bool
}

// KitPurchaseView is the defence minister's plan or result of buying
// upgrade kits from a contractor.
type KitPurchaseView struct {
	Bought  bool
	Seller  string
	Country string
}

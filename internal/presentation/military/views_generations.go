package military

import "time"

// KitPurchaseView is the defence minister's plan or result of buying upgrade
// kits from a contractor.
type KitPurchaseView struct {
	Bought  bool
	Seller  string
	Country string
}

// StateRetrofitView is the plan of applying one upgrade kit the state holds to
// one of its own assets, or the start of that job: a branch commander's step.
type StateRetrofitView struct {
	Country string
	// KitSerial and TargetSerial name the kit and the unit it is applied to.
	KitSerial, TargetSerial string
	Good                    Good
	FromVer                 int64
	ToVer                   int64
	Duration                time.Duration
	FinishAt                time.Time
	// Started is set once the job is running.
	Started bool
}

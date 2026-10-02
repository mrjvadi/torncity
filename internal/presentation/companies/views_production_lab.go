package companies

import "time"

// Where a company stands on a technology.
const (
	TechOwned     = "owned"
	TechLicense   = "licensed"
	TechPublic    = "published"
	TechRunning   = "running"
	TechAvailable = "available"
	TechLocked    = "locked"
)

// TechLine is one technology of the tree.
type TechLine struct {
	Tech Named
	// State is where the company stands on it; Mode how it shares it when
	// it owns it.
	State string
	Mode  string
	Price int64
	Cost  int64
	// Missing are the prerequisites it has not unlocked.
	Missing []Named
	// Offers is how many other companies sell licenses for it, for a
	// company that cannot use it: the license market at a glance.
	Offers int
}

// LabView is a company's research lab.
type LabView struct {
	Ref CompanyRef
	// Available is the money the company may spend.
	Available int64
	// Running is the research running now, if any.
	Running *ResearchLine
	Techs   []TechLine
	// Hidden is how many technologies further away are kept out of sight
	// until the company comes closer (docs/adr/0021, section 14).
	Hidden int
}

// ResearchLine is a research running.
type ResearchLine struct {
	Tech     Named
	FinishAt time.Time
	Left     time.Duration
}

// TechOffer is another company that owns a technology, as a would-be
// licensee sees it.
type TechOffer struct {
	Company CompanyRef
	// Price is its license price; zero when it does not license it.
	Price int64
}

// TechRequirement is a prerequisite of a technology and whether the company
// has unlocked it.
type TechRequirement struct {
	Tech Named
	Met  bool
}

// TechNotice is what just happened on the technology screen.
type TechNotice struct {
	// Kind is started, private, license, published or bought.
	Kind    string
	Price   int64
	Company CompanyRef
}

// Tech notice kinds.
const (
	TechNoticeStarted   = "started"
	TechNoticePrivate   = "private"
	TechNoticeLicense   = "license"
	TechNoticePublished = "published"
	TechNoticeBought    = "bought"
)

// TechView is one technology as a company stands on it.
type TechView struct {
	Ref   CompanyRef
	Tech  Named
	State string
	Cost  int64
	// Time is the research's wait on the wall clock.
	Time     time.Duration
	Requires []TechRequirement
	// Skill and Level are what research needs; Best what the company's
	// best member has.
	Skill string
	Level int
	Best  int
	// Unlocks are the components it lets the company make and design with.
	Unlocks []Named
	// Mode and Price: how an owner shares it; Sold the licenses it sold.
	Mode  string
	Price int64
	Sold  int
	// Blocked says why research cannot start: busy, wrong_type, skill,
	// prerequisite, funds; empty when it can.
	Blocked   string
	Available int64
	Running   *ResearchLine
	// Offers are other companies that own it, for a company that lacks it.
	Offers []TechOffer
	// Confirm asks before a publication or a license purchase.
	ConfirmPublish bool
	ConfirmLicense *TechOffer
	Notice         *TechNotice
	// Gap, for research blocked on a skill, is how to close it: recruit a
	// specialist, or train (docs/adr/0027).
	Gap *SkillGap
}
